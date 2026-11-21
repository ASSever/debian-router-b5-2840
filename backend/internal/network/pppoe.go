package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	pppoeStatePath  = "/var/lib/debian-router-agent/wan-pppoe"
	pppoeActivePath = "/var/lib/debian-router-agent/pppoe-active"
	pppoeService    = "debian-router-pppoe.service"
	pppoeUnitPrefix = "debian-router-wan-pppoe-"
	pppoeDelay      = 120 * time.Second
)

var (
	ErrPPPoEUsername     = errors.New("invalid PPPoE username")
	ErrPPPoEPassword     = errors.New("invalid PPPoE password")
	ErrPPPoEPending      = errors.New("a WAN change is already pending or PPPoE is active")
	ErrPPPoETransaction  = errors.New("PPPoE transaction is unavailable")
	ErrPPPoENotConnected = errors.New("PPPoE session is not connected")
	ErrPPPoELinkDown     = errors.New("WAN link is down")
)

type PPPoEConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type PPPoETransaction struct {
	ID        string    `json:"transaction_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PPPoEStatus struct {
	State       string `json:"state"`
	Interface   string `json:"interface"`
	Address     string `json:"address,omitempty"`
	PeerAddress string `json:"peer_address,omitempty"`
}

type PPPoEManager struct {
	configPath string
	statePath  string
	activePath string
	executable string
	now        func() time.Time
	run        commandRunner
	readWAN    func(string) (ActiveNetworkStatus, error)
	readPPP    func() (string, string, error)
}

// ValidatePPPoEConfig rejects control characters and limits input before it reaches pppd.
func ValidatePPPoEConfig(config PPPoEConfig) error {
	if !validPPPoEWord(config.Username) {
		return ErrPPPoEUsername
	}
	if !validPPPoEWord(config.Password) {
		return ErrPPPoEPassword
	}
	return nil
}

func validPPPoEWord(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func NewPPPoEManager() (*PPPoEManager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("locate router-agent executable")
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return &PPPoEManager{configPath: staticWANConfigPath, statePath: pppoeStatePath,
		activePath: pppoeActivePath, executable: executable, now: time.Now,
		run: runCommand, readWAN: ReadActiveNetworkStatus, readPPP: readPPPoEInterface}, nil
}

func newPPPoEManagerForTest(configPath, statePath, activePath string, run commandRunner,
	readWAN func(string) (ActiveNetworkStatus, error), readPPP func() (string, string, error)) *PPPoEManager {
	return &PPPoEManager{configPath: configPath, statePath: statePath, activePath: activePath,
		executable: "/opt/debian-router-b5-2840/agent-current/router-agent", now: time.Now,
		run: run, readWAN: readWAN, readPPP: readPPP}
}

func wanLockPath(statePath string) string { return filepath.Join(filepath.Dir(statePath), "wan.lock") }

func pppoeModeBusy(parent string) (bool, error) {
	active := filepath.Join(parent, "pppoe-active", "options")
	if _, err := os.Lstat(active); err == nil {
		return true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	entries, err := os.ReadDir(filepath.Join(parent, "wan-pppoe"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && validStaticWANTransactionID(entry.Name()) {
			return true, nil
		}
	}
	return false, nil
}

func (m *PPPoEManager) Apply(ctx context.Context, config PPPoEConfig) (PPPoETransaction, error) {
	if err := ValidatePPPoEConfig(config); err != nil {
		return PPPoETransaction{}, err
	}
	status, err := m.readWAN(StaticWANInterface)
	if err != nil || status.LinkState != "carrier" {
		return PPPoETransaction{}, ErrPPPoELinkDown
	}
	if err := m.ensureDirs(); err != nil {
		return PPPoETransaction{}, err
	}
	var transaction PPPoETransaction
	err = withFileLock(wanLockPath(m.statePath), func() error {
		busy, err := pppoeModeBusy(filepath.Dir(m.statePath))
		if err != nil {
			return err
		}
		if busy {
			return ErrPPPoEPending
		}
		static, err := (&StaticWANManager{statePath: filepath.Join(filepath.Dir(m.statePath), "wan-static")}).hasPendingTransaction()
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		if err != nil {
			return err
		}
		if static {
			return ErrPPPoEPending
		}
		if active, err := wanLineActive(m.configPath); err != nil || active {
			return ErrPPPoEPending
		}
		transaction, err = m.startTransaction(ctx, config)
		return err
	})
	return transaction, err
}

func (m *PPPoEManager) startTransaction(ctx context.Context, config PPPoEConfig) (PPPoETransaction, error) {
	id, err := newStaticWANTransactionID()
	if err != nil {
		return PPPoETransaction{}, errors.New("create PPPoE transaction")
	}
	directory := filepath.Join(m.statePath, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		return PPPoETransaction{}, errors.New("create PPPoE transaction directory")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = m.removeTransaction(directory)
		}
	}()
	original, exists, mode, err := readStaticWANFile(m.configPath)
	if err != nil {
		return PPPoETransaction{}, err
	}
	if exists {
		if err := writeSecureFile(filepath.Join(directory, "original"), original, 0600); err != nil {
			return PPPoETransaction{}, err
		}
	}
	backup := staticWANBackup{OriginalExists: exists}
	if exists {
		backup.OriginalMode = uint32(mode.Mode().Perm())
	}
	if err := writeJSONFile(filepath.Join(directory, "backup.json"), backup); err != nil {
		return PPPoETransaction{}, err
	}
	if err := m.startRollback(ctx, id); err != nil {
		return PPPoETransaction{}, err
	}
	// The only password copy is root-readable options; the timer was armed first.
	if err := writeSecureFile(filepath.Join(m.activePath, "options"), []byte(renderPPPoEOptions(config)), 0600); err != nil {
		if m.stopRollback(ctx, id, false) != nil {
			cleanup = false
		}
		return PPPoETransaction{}, errors.New("write PPPoE credentials")
	}
	profile := []byte("[Match]\nName=enp1s0\n\n[Network]\nDHCP=no\nIPv6AcceptRA=no\nLinkLocalAddressing=no\n")
	if err := ensureRegularParent(m.configPath); err == nil {
		err = writeAtomicFile(m.configPath, profile, 0644)
	}
	if err == nil {
		_, err = m.run(ctx, "/usr/bin/networkctl", "reload")
	}
	if err == nil {
		_, err = m.run(ctx, "/usr/bin/networkctl", "reconfigure", StaticWANInterface)
	}
	if err == nil {
		_, err = m.run(ctx, "/usr/bin/systemctl", "--system", "start", pppoeService)
	}
	if err != nil {
		// Keep the independent timer and snapshot unless full restoration succeeds.
		cleanup = false
		if restoreErr := m.restore(ctx, directory); restoreErr == nil {
			if stopErr := m.stopRollback(ctx, id, false); stopErr == nil {
				_ = m.removeTransaction(directory)
			}
		}
		return PPPoETransaction{}, errors.New("start PPPoE connection")
	}
	cleanup = false
	return PPPoETransaction{ID: id, ExpiresAt: m.now().Add(pppoeDelay)}, nil
}

func renderPPPoEOptions(config PPPoEConfig) string {
	return "plugin pppoe.so\nnic-enp1s0\nifname ppp0\nnoauth\nnoipdefault\n" +
		"defaultroute\ndefaultroute-metric 1000\nusepeerdns\nhide-password\n" +
		"persist\nmaxfail 0\nholdoff 5\nuser " + quotePPPoEWord(config.Username) +
		"\npassword " + quotePPPoEWord(config.Password) + "\n"
}

func quotePPPoEWord(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
}

func (m *PPPoEManager) Confirm(ctx context.Context, id string) error {
	if !validStaticWANTransactionID(id) {
		return ErrPPPoETransaction
	}
	return withFileLock(wanLockPath(m.statePath), func() error {
		directory, err := m.transactionDirectory(id)
		if err != nil {
			return err
		}
		address, _, err := m.readPPP()
		if err != nil || address == "" {
			return ErrPPPoENotConnected
		}
		service, err := m.run(ctx, "/usr/bin/systemctl", "--system", "is-active", pppoeService)
		if err != nil || strings.TrimSpace(service) != "active" {
			return ErrPPPoENotConnected
		}
		if err := m.stopRollback(ctx, id, true); err != nil {
			return err
		}
		return m.removeTransaction(directory)
	})
}

func (m *PPPoEManager) Rollback(ctx context.Context, id string) error {
	return m.rollback(ctx, id, false)
}

func (m *PPPoEManager) RollbackFromTimer(ctx context.Context, id string) error {
	return m.rollback(ctx, id, true)
}

func (m *PPPoEManager) rollback(ctx context.Context, id string, fromTimer bool) error {
	if !validStaticWANTransactionID(id) {
		return ErrPPPoETransaction
	}
	return withFileLock(wanLockPath(m.statePath), func() error {
		directory, err := m.transactionDirectory(id)
		if err != nil {
			return err
		}
		if err := m.restore(ctx, directory); err != nil {
			return err
		}
		if !fromTimer {
			if err := m.stopRollback(ctx, id, false); err != nil {
				return err
			}
		}
		return m.removeTransaction(directory)
	})
}

func (m *PPPoEManager) restore(ctx context.Context, directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, "backup.json"))
	if err != nil {
		return errors.New("read PPPoE rollback metadata")
	}
	var backup staticWANBackup
	if json.Unmarshal(data, &backup) != nil || backup.OriginalMode&^uint32(0777) != 0 || (!backup.OriginalExists && backup.OriginalMode != 0) {
		return errors.New("invalid PPPoE rollback metadata")
	}
	if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "stop", pppoeService); err != nil {
		return errors.New("stop PPPoE service")
	}
	// pppd may exit nonzero after SIGTERM even when systemctl stop succeeds.
	// Clear that stale failure so the restored DHCP mode reports PPPoE inactive.
	_, _ = m.run(ctx, "/usr/bin/systemctl", "--system", "reset-failed", pppoeService)
	if backup.OriginalExists {
		original, exists, _, err := readStaticWANFile(filepath.Join(directory, "original"))
		if err != nil || !exists {
			return errors.New("read PPPoE rollback copy")
		}
		if err := writeAtomicFile(m.configPath, original, os.FileMode(backup.OriginalMode)); err != nil {
			return errors.New("restore WAN configuration")
		}
	} else if err := removeRegularFile(m.configPath); err != nil {
		return err
	}
	if _, err := m.run(ctx, "/usr/bin/networkctl", "reload"); err != nil {
		return errors.New("reload restored WAN configuration")
	}
	if _, err := m.run(ctx, "/usr/bin/networkctl", "reconfigure", StaticWANInterface); err != nil {
		return errors.New("reconfigure restored WAN")
	}
	if err := removeRegularFile(filepath.Join(m.activePath, "options")); err != nil {
		return err
	}
	return nil
}

func (m *PPPoEManager) RecoverPending(ctx context.Context) error {
	if err := m.ensureDirs(); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.statePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && validStaticWANTransactionID(entry.Name()) {
			if err := m.Rollback(ctx, entry.Name()); err != nil {
				return fmt.Errorf("recover PPPoE transaction %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func (m *PPPoEManager) Reconnect(ctx context.Context) error {
	return withFileLock(wanLockPath(m.statePath), func() error {
		info, err := os.Lstat(filepath.Join(m.activePath, "options"))
		if errors.Is(err, fs.ErrNotExist) {
			return ErrPPPoENotConnected
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return errors.New("PPPoE credential file is unsafe")
		}
		if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "restart", pppoeService); err != nil {
			return errors.New("restart PPPoE service")
		}
		return nil
	})
}

func (m *PPPoEManager) Status() (PPPoEStatus, error) {
	result := PPPoEStatus{State: "disconnected", Interface: "ppp0"}
	info, err := os.Lstat(filepath.Join(m.activePath, "options"))
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return result, errors.New("PPPoE credential file is unsafe")
	}
	address, peer, err := m.readPPP()
	service, serviceErr := m.run(context.Background(), "/usr/bin/systemctl", "--system", "is-active", pppoeService)
	if err == nil && address != "" && serviceErr == nil && strings.TrimSpace(service) == "active" {
		result.State, result.Address, result.PeerAddress = "connected", address, peer
		return result, nil
	}
	result.State = "connecting"
	journal, err := m.run(context.Background(), "/usr/bin/journalctl", "--unit="+pppoeService,
		"--since="+info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"), "--lines=30", "--no-pager", "--output=cat")
	if err == nil {
		var authFailed, noPeer, reconnecting bool
		for _, line := range strings.Split(strings.ToLower(journal), "\n") {
			switch {
			case strings.Contains(line, "authentication failed"):
				authFailed = true
			case strings.Contains(line, "timeout waiting for pado"), strings.Contains(line, "no pppoe access concentrator"), strings.Contains(line, "no response to padi"):
				noPeer = true
			case strings.Contains(line, "connection terminated"), strings.Contains(line, "modem hangup"):
				reconnecting = true
			}
		}
		switch {
		case authFailed:
			result.State = "auth_failed"
		case noPeer:
			result.State = "no_peer"
		case reconnecting:
			result.State = "reconnecting"
		}
	}
	return result, nil
}

func readPPPoEInterface() (string, string, error) {
	iface, err := net.InterfaceByName("ppp0")
	if err != nil {
		return "", "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", "", err
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			return ipNet.IP.String(), "", nil
		}
	}
	return "", "", ErrPPPoENotConnected
}

func (m *PPPoEManager) ensureDirs() error {
	for _, directory := range []string{m.statePath, m.activePath} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return errors.New("prepare PPPoE storage")
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe PPPoE storage")
		}
		if err := os.Chmod(directory, 0700); err != nil {
			return errors.New("secure PPPoE storage")
		}
	}
	return nil
}

func (m *PPPoEManager) transactionDirectory(id string) (string, error) {
	directory := filepath.Join(m.statePath, id)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrPPPoETransaction
	}
	return directory, nil
}

func (m *PPPoEManager) removeTransaction(directory string) error {
	for _, name := range []string{"original", "backup.json"} {
		if err := removeRegularFile(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(directory); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (m *PPPoEManager) startRollback(ctx context.Context, id string) error {
	unit := pppoeUnitPrefix + id
	args := []string{"--system", "--quiet", "--no-block", "--collect", "--unit=" + unit,
		"--on-active=120s", "--timer-property=AccuracySec=1s", "--property=Type=oneshot",
		"--property=User=root", "--property=StateDirectory=debian-router-agent",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=",
		"--property=RestrictAddressFamilies=AF_UNIX AF_NETLINK",
		"--property=ReadWritePaths=/etc/systemd/network", "--", m.executable, "rollback-pppoe", id}
	if _, err := m.run(ctx, "/usr/bin/systemd-run", args...); err != nil {
		return errors.New("arm independent PPPoE rollback")
	}
	state, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".timer")
	if err != nil || strings.TrimSpace(state) != "active" {
		_, _ = m.run(ctx, "/usr/bin/systemctl", "--system", "stop", unit+".timer", unit+".service")
		return errors.New("PPPoE rollback timer is not active")
	}
	return nil
}

func (m *PPPoEManager) stopRollback(ctx context.Context, id string, required bool) error {
	unit := pppoeUnitPrefix + id
	state, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=LoadState", "--value", unit+".timer")
	if err != nil || strings.TrimSpace(state) != "loaded" {
		if required {
			return ErrPPPoETransaction
		}
		return nil
	}
	if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "stop", unit+".timer"); err != nil {
		return errors.New("stop PPPoE rollback timer")
	}
	state, err = m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".timer")
	if err != nil || state == "active" || state == "activating" {
		return errors.New("PPPoE rollback timer is still active")
	}
	return nil
}
