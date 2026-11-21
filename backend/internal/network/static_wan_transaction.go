package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	staticWANConfigPath = "/etc/systemd/network/10-wan.network"
	staticWANStatePath  = "/var/lib/debian-router-agent/wan-static"
	staticWANUnitPrefix = "debian-router-wan-static-"
	staticWANDelay      = 120 * time.Second
)

var (
	ErrStaticWANLinkDown     = errors.New("WAN link is down")
	ErrStaticWANPending      = errors.New("a static WAN change is already pending")
	ErrStaticWANTransaction  = errors.New("static WAN transaction is unavailable")
	ErrStaticWANNotConfirmed = errors.New("static WAN configuration is not active")
)

// StaticWANTransaction is returned while an independent rollback is armed.
type StaticWANTransaction struct {
	ID        string    `json:"transaction_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type staticWANRecord struct {
	FirewallExpiry *time.Time      `json:"firewall_expiry,omitempty"`
	FirewallRules  bool            `json:"firewall_rules,omitempty"`
	DNS            bool            `json:"dns_change,omitempty"`
	Config         StaticWANConfig `json:"config"`
	Line           *wanLineProfile `json:"line,omitempty"`
	LAN            *LANConfig      `json:"lan,omitempty"`
	LANExpiry      *time.Time      `json:"lan_expiry,omitempty"`
}

type staticWANBackup struct {
	Firewall       bool            `json:"firewall,omitempty"`
	FirewallRules  bool            `json:"firewall_rules,omitempty"`
	DNS            bool            `json:"dns_change,omitempty"`
	OriginalExists bool            `json:"original_exists"`
	OriginalMode   uint32          `json:"original_mode"`
	Line           *wanLineProfile `json:"line,omitempty"`
	LAN            *LANConfig      `json:"lan,omitempty"`
}

// StaticWANManager applies only to enp1s0 and keeps an exact rollback copy.
type StaticWANManager struct {
	configPath string
	statePath  string
	executable string
	now        func() time.Time
	run        commandRunner
	readStatus func(string) (ActiveNetworkStatus, error)
}

type commandRunner func(context.Context, string, ...string) (string, error)

// NewStaticWANManager creates the production manager for the fixed B5 WAN port.
func NewStaticWANManager() (*StaticWANManager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("locate router-agent executable")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return &StaticWANManager{
		configPath: staticWANConfigPath,
		statePath:  staticWANStatePath,
		executable: executable,
		now:        time.Now,
		run:        runCommand,
		readStatus: ReadActiveNetworkStatus,
	}, nil
}

func newStaticWANManagerForTest(configPath, statePath string, run commandRunner, readStatus func(string) (ActiveNetworkStatus, error)) *StaticWANManager {
	return &StaticWANManager{
		configPath: configPath,
		statePath:  statePath,
		executable: "/opt/debian-router-b5-2840/agent-current/router-agent",
		now:        time.Now,
		run:        run,
		readStatus: readStatus,
	}
}

// Apply writes the profile only after a rollback timer is confirmed active.
func (m *StaticWANManager) Apply(ctx context.Context, config StaticWANConfig) (StaticWANTransaction, error) {
	content, err := RenderStaticWAN(config)
	if err != nil {
		return StaticWANTransaction{}, err
	}
	return m.applyWANChange(func() (StaticWANTransaction, error) {
		if active, err := wanLineActive(m.configPath); err != nil || active {
			return StaticWANTransaction{}, ErrStaticWANPending
		}
		return m.startTransaction(ctx, staticWANRecord{Config: config}, m.configPath, func() error {
			if err := m.writeNetworkConfig(content, 0644); err != nil {
				return err
			}
			return m.reconfigureWAN(ctx)
		})
	})
}

func (m *StaticWANManager) applyWANChange(start func() (StaticWANTransaction, error)) (StaticWANTransaction, error) {
	status, err := m.readStatus(StaticWANInterface)
	if err != nil || status.LinkState != "carrier" {
		return StaticWANTransaction{}, ErrStaticWANLinkDown
	}
	if err := m.ensureStateDirectory(); err != nil {
		return StaticWANTransaction{}, err
	}

	var transaction StaticWANTransaction
	err = withFileLock(wanLockPath(m.statePath), func() error {
		pending, err := m.hasPendingTransaction()
		if err != nil {
			return err
		}
		if pending {
			return ErrStaticWANPending
		}
		otherMode, err := pppoeModeBusy(filepath.Dir(m.statePath))
		if err != nil {
			return err
		}
		if otherMode {
			return ErrStaticWANPending
		}
		transaction, err = start()
		return err
	})
	if err != nil {
		return StaticWANTransaction{}, err
	}
	return transaction, nil
}

// Confirm disarms rollback only after networkd reports the requested settings.
func (m *StaticWANManager) Confirm(ctx context.Context, id string) error {
	return m.confirmNetworkChange(ctx, id, "")
}

func (m *StaticWANManager) ConfirmLAN(ctx context.Context, id, destination string) error {
	if destination == "" {
		return ErrStaticWANNotConfirmed
	}
	return m.confirmNetworkChange(ctx, id, destination)
}

func (m *StaticWANManager) confirmNetworkChange(ctx context.Context, id, destination string) error {
	if !validStaticWANTransactionID(id) {
		return ErrStaticWANTransaction
	}
	return withFileLock(wanLockPath(m.statePath), func() error {
		directory, record, err := m.loadTransaction(id)
		if err != nil {
			return err
		}
		matches := false
		if record.FirewallRules && record.FirewallExpiry != nil {
			exists, err := m.firewallTableExists(ctx)
			matches = destination == "" && m.now().Before(*record.FirewallExpiry) && err == nil && exists
		} else if record.FirewallExpiry != nil {
			matches = destination == "" && m.now().Before(*record.FirewallExpiry) && m.firewallReady(ctx)
		} else if record.LAN != nil {
			matches = m.lanMatches(ctx, *record.LAN, destination)
		} else if destination == "" {
			matches = m.matchesWANChange(ctx, record)
		}
		if !matches || record.LANExpiry != nil && !m.now().Before(*record.LANExpiry) {
			return ErrStaticWANNotConfirmed
		}
		if err := m.stopRollback(ctx, id, true); err != nil {
			return err
		}
		return m.removeTransaction(directory)
	})
}

// Rollback restores the original file and reconfigures only the WAN interface.
func (m *StaticWANManager) Rollback(ctx context.Context, id string) error {
	if !validStaticWANTransactionID(id) {
		return ErrStaticWANTransaction
	}
	return m.rollback(ctx, id, false)
}

// RollbackFromTimer is the root-only command used by the system-manager timer.
func (m *StaticWANManager) RollbackFromTimer(ctx context.Context, id string) error {
	if !validStaticWANTransactionID(id) {
		return ErrStaticWANTransaction
	}
	return m.rollback(ctx, id, true)
}

// RecoverPending restores an unconfirmed transaction after a reboot or agent restart.
func (m *StaticWANManager) RecoverPending(ctx context.Context) error {
	if err := m.ensureStateDirectory(); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.statePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validStaticWANTransactionID(entry.Name()) {
			continue
		}
		if err := m.rollback(ctx, entry.Name(), false); err != nil {
			if errors.Is(err, ErrStaticWANTransaction) {
				if _, statErr := os.Lstat(filepath.Join(m.statePath, entry.Name())); errors.Is(statErr, fs.ErrNotExist) {
					continue
				}
			}
			return fmt.Errorf("recover static WAN transaction %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (m *StaticWANManager) matchesWANChange(ctx context.Context, record staticWANRecord) bool {
	if record.Line != nil {
		return m.lineMatches(ctx, *record.Line)
	}
	status, err := m.readStatus(StaticWANInterface)
	return err == nil && staticStatusMatches(status, record.Config)
}

func (m *StaticWANManager) startTransaction(ctx context.Context, record staticWANRecord, originalPath string, apply func() error) (StaticWANTransaction, error) {
	id, err := newStaticWANTransactionID()
	if err != nil {
		return StaticWANTransaction{}, errors.New("create static WAN transaction")
	}
	directory := filepath.Join(m.statePath, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		return StaticWANTransaction{}, errors.New("create static WAN transaction directory")
	}
	cleanup := true
	defer func() {
		if cleanup {
			for _, name := range []string{"original", "backup.json", "transaction.json"} {
				_ = os.Remove(filepath.Join(directory, name))
			}
			_ = os.Remove(directory)
		}
	}()

	original, originalExists, originalMode, err := readStaticWANFile(originalPath)
	if err != nil {
		return StaticWANTransaction{}, err
	}
	if originalExists {
		if err := writeSecureFile(filepath.Join(directory, "original"), original, 0600); err != nil {
			return StaticWANTransaction{}, err
		}
	}
	expiresAt := m.now().Add(staticWANDelay)
	if record.FirewallExpiry != nil {
		record.FirewallExpiry = &expiresAt
	}
	if record.LAN != nil {
		expiresAt = m.now().Add(300 * time.Second)
		record.LANExpiry = &expiresAt
	}
	originalPermissions := uint32(0)
	if originalExists {
		originalPermissions = uint32(originalMode.Mode().Perm())
	}
	backup := staticWANBackup{
		Firewall:       record.FirewallExpiry != nil,
		FirewallRules:  record.FirewallRules,
		DNS:            record.DNS,
		OriginalExists: originalExists,
		OriginalMode:   originalPermissions,
		Line:           record.Line,
		LAN:            record.LAN,
	}
	if err := writeJSONFile(filepath.Join(directory, "backup.json"), backup); err != nil {
		return StaticWANTransaction{}, err
	}
	if err := writeJSONFile(filepath.Join(directory, "transaction.json"), record); err != nil {
		return StaticWANTransaction{}, err
	}
	if err := m.startRollback(ctx, id, expiresAt.Sub(m.now()).Round(time.Second)); err != nil {
		return StaticWANTransaction{}, err
	}
	if err := apply(); err != nil {
		if restoreErr := m.restoreOriginal(ctx, directory); restoreErr == nil {
			if stopErr := m.stopRollback(ctx, id, false); stopErr == nil {
				return StaticWANTransaction{}, err
			}
		}
		// Keep the rollback snapshot and timer when immediate restoration is not
		// fully confirmed. The independent timer can retry after this request ends.
		cleanup = false
		return StaticWANTransaction{}, err
	}
	cleanup = false
	return StaticWANTransaction{ID: id, ExpiresAt: expiresAt}, nil
}

func (m *StaticWANManager) rollback(ctx context.Context, id string, fromTimer bool) error {
	return withFileLock(wanLockPath(m.statePath), func() error {
		directory, err := m.transactionDirectory(id)
		if err != nil {
			return err
		}
		if err := m.restoreOriginal(ctx, directory); err != nil {
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

func (m *StaticWANManager) restoreOriginal(ctx context.Context, directory string) error {
	backupData, err := os.ReadFile(filepath.Join(directory, "backup.json"))
	if err != nil {
		return errors.New("read static WAN rollback metadata")
	}
	var backup staticWANBackup
	if err := json.Unmarshal(backupData, &backup); err != nil || backup.OriginalMode&^uint32(0777) != 0 || (!backup.OriginalExists && backup.OriginalMode != 0) {
		return errors.New("decode static WAN rollback metadata")
	}
	if backup.Line != nil {
		if !backup.Line.valid() {
			return errors.New("invalid WAN line rollback metadata")
		}
		return m.restoreWANLine(ctx, directory, backup, *backup.Line)
	}
	if backup.LAN != nil {
		return m.restoreLAN(ctx, directory, backup)
	}
	if backup.DNS {
		return m.restoreDNS(ctx, directory, backup)
	}
	if backup.FirewallRules {
		return m.restoreFirewallRules(ctx, directory, backup)
	}
	if backup.Firewall {
		return m.restoreFirewall(ctx)
	}
	if backup.OriginalExists {
		original, exists, _, err := readStaticWANFile(filepath.Join(directory, "original"))
		if err != nil {
			return errors.New("read static WAN rollback copy")
		}
		if !exists {
			return errors.New("static WAN rollback copy is missing")
		}
		mode := os.FileMode(backup.OriginalMode)
		if err := m.writeNetworkConfig(original, mode); err != nil {
			return err
		}
	} else if err := removeRegularFile(m.configPath); err != nil {
		return err
	}
	return m.reconfigureWAN(ctx)
}

func (m *StaticWANManager) startRollback(ctx context.Context, id string, delay time.Duration) error {
	unit := staticWANUnitPrefix + id
	args := []string{
		"--system", "--quiet", "--no-block", "--collect",
		"--unit=" + unit,
		"--on-active=" + fmt.Sprintf("%ds", int(delay/time.Second)),
		"--timer-property=AccuracySec=1s",
		"--property=Type=oneshot",
		"--property=User=root",
		"--property=StateDirectory=debian-router-agent",
		"--property=ProtectSystem=strict",
		"--property=ProtectHome=yes",
		"--property=PrivateTmp=yes",
		"--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=",
		"--property=RestrictAddressFamilies=AF_UNIX AF_NETLINK",
		"--property=ReadWritePaths=/etc/systemd/network",
		"--", m.executable, "rollback", id,
	}
	if _, err := m.run(ctx, "/usr/bin/systemd-run", args...); err != nil {
		return errors.New("arm independent static WAN rollback")
	}
	state, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".timer")
	if err != nil || strings.TrimSpace(state) != "active" {
		_, _ = m.run(ctx, "/usr/bin/systemctl", "--system", "stop", unit+".timer", unit+".service")
		return errors.New("static WAN rollback timer is not active")
	}
	return nil
}

func (m *StaticWANManager) stopRollback(ctx context.Context, id string, requireLoaded bool) error {
	unit := staticWANUnitPrefix + id
	state, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=LoadState", "--value", unit+".timer")
	if err != nil {
		if requireLoaded {
			return ErrStaticWANTransaction
		}
		return nil
	}
	if strings.TrimSpace(state) != "loaded" {
		if requireLoaded {
			return ErrStaticWANTransaction
		}
		return nil
	}
	if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "stop", unit+".timer"); err != nil {
		return errors.New("stop static WAN rollback timer")
	}
	state, err = m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".timer")
	if err != nil || strings.TrimSpace(state) == "active" || strings.TrimSpace(state) == "activating" {
		return errors.New("static WAN rollback timer is still active")
	}
	serviceLoad, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=LoadState", "--value", unit+".service")
	if err != nil {
		return errors.New("inspect static WAN rollback service")
	}
	if strings.TrimSpace(serviceLoad) != "loaded" {
		return nil
	}
	serviceState, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".service")
	if err != nil {
		return errors.New("inspect static WAN rollback service")
	}
	if strings.TrimSpace(serviceState) == "active" || strings.TrimSpace(serviceState) == "activating" {
		if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "stop", unit+".service"); err != nil {
			return errors.New("stop static WAN rollback service")
		}
		serviceState, err = m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=ActiveState", "--value", unit+".service")
		if err != nil || strings.TrimSpace(serviceState) == "active" || strings.TrimSpace(serviceState) == "activating" {
			return errors.New("static WAN rollback service is still active")
		}
	}
	return nil
}

func (m *StaticWANManager) reconfigureWAN(ctx context.Context) error {
	if _, err := m.run(ctx, "/usr/bin/networkctl", "reload"); err != nil {
		return errors.New("reload systemd-networkd configuration")
	}
	if _, err := m.run(ctx, "/usr/bin/networkctl", "reconfigure", StaticWANInterface); err != nil {
		return errors.New("reconfigure the WAN interface")
	}
	return nil
}

func (m *StaticWANManager) writeNetworkConfig(content []byte, mode os.FileMode) error {
	if err := ensureRegularParent(m.configPath); err != nil {
		return err
	}
	if err := writeAtomicFile(m.configPath, content, mode); err != nil {
		return errors.New("write static WAN network configuration")
	}
	return nil
}

func (m *StaticWANManager) ensureStateDirectory() error {
	if err := os.MkdirAll(m.statePath, 0700); err != nil {
		return errors.New("prepare static WAN transaction storage")
	}
	info, err := os.Lstat(m.statePath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("static WAN transaction storage is unsafe")
	}
	if err := os.Chmod(m.statePath, 0700); err != nil {
		return errors.New("secure static WAN transaction storage")
	}
	return nil
}

func (m *StaticWANManager) hasPendingTransaction() (bool, error) {
	entries, err := os.ReadDir(m.statePath)
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

func (m *StaticWANManager) loadTransaction(id string) (string, staticWANRecord, error) {
	directory, err := m.transactionDirectory(id)
	if err != nil {
		return "", staticWANRecord{}, err
	}
	data, err := os.ReadFile(filepath.Join(directory, "transaction.json"))
	if err != nil {
		return "", staticWANRecord{}, ErrStaticWANTransaction
	}
	var record staticWANRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", staticWANRecord{}, ErrStaticWANTransaction
	}
	switch {
	case record.FirewallExpiry != nil:
	case record.LAN != nil:
		if ValidateLAN(*record.LAN) != nil || record.LANExpiry == nil {
			return "", staticWANRecord{}, ErrStaticWANTransaction
		}
	case record.Line != nil:
		if !record.Line.valid() {
			return "", staticWANRecord{}, ErrStaticWANTransaction
		}
	case ValidateStaticWAN(record.Config) != nil:
		return "", staticWANRecord{}, ErrStaticWANTransaction
	}
	return directory, record, nil
}

func (m *StaticWANManager) transactionDirectory(id string) (string, error) {
	directory := filepath.Join(m.statePath, id)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrStaticWANTransaction
	}
	return directory, nil
}

func (m *StaticWANManager) removeTransaction(directory string) error {
	for _, name := range []string{"original", "backup.json", "transaction.json"} {
		if err := removeRegularFile(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(directory); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func readStaticWANFile(path string) ([]byte, bool, fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, nil, errors.New("WAN network file is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, nil, errors.New("read current WAN network file")
	}
	return data, true, info, nil
}

func writeSecureFile(path string, data []byte, mode os.FileMode) error {
	if err := writeAtomicFile(path, data, mode); err != nil {
		return errors.New("write static WAN rollback copy")
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeSecureFile(path, data, 0600)
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".router-wan-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func removeRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse to remove unsafe static WAN transaction file")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func ensureRegularParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return errors.New("WAN network configuration directory is unavailable")
	}
	return nil
}

func staticStatusMatches(status ActiveNetworkStatus, config StaticWANConfig) bool {
	if status.LinkState != "carrier" || status.Gateway != config.Gateway || len(status.Addresses) != 1 ||
		status.Addresses[0] != config.Address || len(status.DNS) != len(config.DNS) {
		return false
	}
	dns := append([]string(nil), config.DNS...)
	sort.Strings(dns)
	statusDNS := append([]string(nil), status.DNS...)
	sort.Strings(statusDNS)
	for index := range dns {
		if dns[index] != statusDNS[index] {
			return false
		}
	}
	return true
}

func newStaticWANTransactionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validStaticWANTransactionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func runCommand(ctx context.Context, path string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed", filepath.Base(path))
	}
	return strings.TrimSpace(string(output)), nil
}
