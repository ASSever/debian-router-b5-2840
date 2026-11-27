package privilegedagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/ddns"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/monitor"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/mqtt"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

const restoreState = "var/lib/debian-router-agent"
const restoreJournalFile = restoreState + "/restore-journal.json"
const restoreWireLimit = 2 * MaxBackupConfigBytes

type RestoreRequest struct {
	ID      string         `json:"id"`
	Configs []BackupConfig `json:"configs,omitempty"`
}

type restoreMutation struct {
	Name   string
	Before RestoreFile
	After  RestoreFile `json:"-"`
}

type restoreJournal struct {
	ID      string
	Files   []restoreMutation
	Created []string
	Network bool
}

type restoreGeneration struct {
	dir    string
	files  map[string][]byte
	zapret bool
}

func SendRestore(ctx context.Context, socket, operation string, request RestoreRequest) (Response, error) {
	return sendRequest(ctx, socket, Request{Operation: operation, Restore: &request})
}

func runRestoreWorker(operation string, request RestoreRequest) (bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 19*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemd-run", "--system", "--quiet", "--wait", "--pipe", "--collect",
		"--property=Type=exec", "--property=RuntimeMaxSec=17s", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=CAP_CHOWN", "--property=ProtectSystem=strict",
		"--property=ProtectHome=yes", "--property=ReadWritePaths=/etc /var/lib/debian-router-agent /run/lock",
		"--property=RestrictAddressFamilies=AF_UNIX", "--", "/usr/bin/flock", "--nonblock",
		"/run/lock/debian-router-install.lock", executable, "restore-worker", operation)
	command.Stdin = bytes.NewReader(data)
	out, err := command.Output()
	return string(out) == "network\n", err
}

// RestoreWorker runs with a separate systemd write sandbox. It never reloads
// networkd or restarts a router service.
func RestoreWorker(operation string, input io.Reader, output io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("restore worker requires root")
	}
	var request RestoreRequest
	decoder := json.NewDecoder(io.LimitReader(input, restoreWireLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !ValidRestoreID(request.ID) || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid restore request")
	}
	if (operation == "restore-rollback" || operation == "restore-finalize") && len(request.Configs) == 0 {
		if _, err := os.Lstat("/" + restoreJournalFile); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	manager, err := network.NewStaticWANManager()
	if err != nil {
		return err
	}
	var changed bool
	err = manager.WithStableBackupConfig(func() error {
		var operationErr error
		changed, operationErr = restoreConfigOperation("/", operation, request)
		return operationErr
	})
	if err != nil {
		return errors.New("configuration restore failed")
	}
	if changed {
		_, err = fmt.Fprintln(output, "network")
	}
	return err
}

func restoreConfigOperation(directory, operation string, request RestoreRequest) (bool, error) {
	path := filepath.Join(directory, restoreJournalFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var journal restoreJournal
	if err == nil && (json.Unmarshal(data, &journal) != nil || journal.ID != request.ID) {
		return false, errors.New("another restore is pending")
	}
	switch operation {
	case "restore-rollback", "restore-finalize":
		if journal.ID == "" {
			return false, nil
		}
		if operation == "restore-rollback" {
			for i := len(journal.Files) - 1; i >= 0; i-- {
				file := journal.Files[i]
				if err := WriteRestoreFile(filepath.Join(directory, file.Name), file.Before); err != nil {
					return false, err
				}
			}
			for _, dir := range journal.Created {
				if err := os.RemoveAll(filepath.Join(directory, dir)); err != nil {
					return false, err
				}
			}
		} else if journal.Network {
			boot, err := network.CurrentRestoreBoot()
			if err != nil {
				return false, err
			}
			if err := WriteRestoreFile(filepath.Join(directory, network.RestoreNetworkHold),
				RestoreFile{Exists: true, Mode: 0600, Data: []byte(boot)}); err != nil {
				return false, err
			}
		}
		return journal.Network, WriteRestoreFile(path, RestoreFile{})
	case "restore-apply":
		if journal.ID != "" {
			return false, errors.New("restore already started")
		}
		return applyConfigRestore(directory, path, request)
	default:
		return false, errors.New("invalid restore operation")
	}
}

func applyConfigRestore(directory, journalPath string, request RestoreRequest) (bool, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false, err
	}
	defer root.Close()
	configs := make(map[string][]byte, len(request.Configs))
	total := 0
	for _, file := range request.Configs {
		total += len(file.Data)
		if !IsBackupConfig(file.Name) || file.Name == APIBackupSubscription ||
			configs[file.Name] != nil || len(file.Data) == 0 && !strings.HasSuffix(file.Name, "/exclude-hosts.txt") &&
			!strings.HasSuffix(file.Name, "/20-router-dhcp.hosts") ||
			total > MaxBackupConfigBytes {
			return false, errors.New("invalid restore config list")
		}
		configs[file.Name] = append([]byte{}, file.Data...)
	}
	journal := restoreJournal{ID: request.ID}
	for _, name := range backupPaths {
		data, ok := configs[name]
		if !ok {
			continue
		}
		before, err := CaptureRestoreFile(root, name)
		if err != nil {
			return false, err
		}
		if before.Exists && bytes.Equal(before.Data, data) {
			continue
		}
		if err := validateConfigRestore(name, data); err != nil {
			return false, err
		}
		after := RestoreFile{Exists: true, Data: data, Mode: 0600}
		if strings.HasPrefix(name, "etc/systemd/network/") {
			after.Mode = 0644
		}
		if strings.HasPrefix(name, "etc/systemd/network/") ||
			strings.HasPrefix(name, restoreState+"/") {
			journal.Network = true
		}
		journal.Files = append(journal.Files, restoreMutation{name, before, after})
	}
	var generations []restoreGeneration
	for _, profile := range backupProfiles {
		generation, err := planRestoreProfile(directory, root, profile.dir, profile.link, configs, &journal)
		if err != nil {
			return false, err
		}
		if generation != nil {
			generations = append(generations, *generation)
		}
	}
	if err := SaveRestoreJSON(journalPath, journal); err != nil {
		return false, err
	}
	for _, generation := range generations {
		if err := writeRestoreGeneration(directory, root, generation); err != nil {
			return false, err
		}
	}
	for _, file := range journal.Files {
		if err := ensureRestoreParent(root, filepath.Dir(file.Name)); err != nil {
			return false, err
		}
		if err := WriteRestoreFile(filepath.Join(directory, file.Name), file.After); err != nil {
			return false, err
		}
	}
	return journal.Network, nil
}

func ensureRestoreParent(root *os.Root, name string) error {
	if name == "." {
		return nil
	}
	if err := ensureRestoreParent(root, filepath.Dir(name)); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return root.Mkdir(name, 0755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("unsafe restore directory")
	}
	return nil
}

func planRestoreProfile(directory string, root *os.Root, base, link string, configs map[string][]byte, journal *restoreJournal) (*restoreGeneration, error) {
	name := base + "/" + link
	isProxy := base == proxyBackupDir
	primary, secondary := "profile", "exclude-hosts.txt"
	if isProxy {
		primary, secondary = "config.json", "profile.json"
	}
	first, firstOK := configs[name+"/"+primary]
	second, secondOK := configs[name+"/"+secondary]
	if !firstOK && !secondOK {
		return nil, nil
	}
	if !firstOK || (!isProxy && !secondOK) {
		return nil, errors.New("incomplete restore profile")
	}
	target, err := os.Readlink(filepath.Join(directory, name))
	var before RestoreFile
	if !errors.Is(err, os.ErrNotExist) {
		generation := target
		if !isProxy && filepath.IsAbs(target) && filepath.Dir(target) == filepath.Join(directory, base) {
			generation = filepath.Base(target)
		}
		if err != nil || (isProxy && !proxyBackupGeneration.MatchString(generation)) ||
			(!isProxy && !zapretBackupGeneration.MatchString(generation)) {
			return nil, errors.New("unsafe restore profile link")
		}
		before = RestoreFile{Exists: true, Link: target}
		oldFirst, e1 := ReadBackupFile(root, base+"/"+generation+"/"+primary, MaxBackupConfigBytes)
		oldSecond, e2 := ReadBackupFile(root, base+"/"+generation+"/"+secondary, MaxBackupConfigBytes)
		if e1 == nil && bytes.Equal(first, oldFirst) &&
			((!secondOK && errors.Is(e2, os.ErrNotExist)) || (e2 == nil && bytes.Equal(second, oldSecond))) {
			return nil, nil
		}
	}
	generation := "profile." + journal.ID[:8]
	if isProxy {
		if err := validateJSONConfig(first); err != nil {
			return nil, err
		}
		if !secondOK {
			second, _ = json.Marshal(proxy.DefaultProfile())
		}
		if err := validateJSONConfig(second); err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(journal.ID))
		generation = "generation-" + strconv.FormatUint(binary.BigEndian.Uint64(sum[:8]), 10)
	} else if err := validateZapretRestore(first, second); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, base, generation)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("restore profile already exists")
	}
	dir := base + "/" + generation
	journal.Created = append(journal.Created, dir)
	target = generation
	if !isProxy {
		target = path
	}
	journal.Files = append(journal.Files, restoreMutation{name, before, RestoreFile{Exists: true, Link: target}})
	return &restoreGeneration{dir: dir, files: map[string][]byte{primary: first, secondary: second}, zapret: !isProxy}, nil
}

func writeRestoreGeneration(directory string, root *os.Root, generation restoreGeneration) error {
	parent := filepath.Dir(generation.dir)
	if err := ensureRestoreParent(root, parent); err != nil {
		return err
	}
	path := filepath.Join(directory, generation.dir)
	if err := root.Mkdir(generation.dir, 0700); err != nil {
		return err
	}
	for name, data := range generation.files {
		if err := WriteRestoreFile(filepath.Join(path, name), RestoreFile{Exists: true, Data: data, Mode: 0600}); err != nil {
			return err
		}
	}
	if generation.zapret {
		const nfqwsGroup = 2147483647
		for _, item := range []struct {
			path string
			mode os.FileMode
		}{{path, 0710}, {filepath.Join(path, "exclude-hosts.txt"), 0640}} {
			if err := os.Chown(item.path, -1, nfqwsGroup); err != nil {
				return err
			}
			if err := os.Chmod(item.path, item.mode); err != nil {
				return err
			}
		}
	}
	return syncRestoreDir(filepath.Dir(path))
}

func validateJSONConfig(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return errors.New("invalid configuration JSON")
	}
	return nil
}

func validateConfigRestore(name string, data []byte) error {
	if filepath.Ext(name) == ".json" {
		if err := validateJSONConfig(data); err != nil {
			return err
		}
	}
	switch name {
	case "etc/debian-router-b5-2840/mqtt.json", "etc/debian-router-b5-2840/ddns.json",
		"etc/debian-router-b5-2840/monitor.json":
		f, err := os.CreateTemp("", "config-check-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, writeErr := f.Write(data)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return err
		}
		switch name {
		case "etc/debian-router-b5-2840/mqtt.json":
			_, err = mqtt.LoadConfig(f.Name())
		case "etc/debian-router-b5-2840/ddns.json":
			_, err = ddns.LoadConfig(f.Name())
		default:
			_, err = monitor.LoadConfig(f.Name())
		}
		return err
	case restoreState + "/pppoe-active/options":
		return network.ValidateRestoredPPPoEOptions(data)
	case "etc/systemd/network/10-wan.network":
		return validateNetworkRestore(data, "Match", "enp1s0")
	case "etc/systemd/network/20-lan.network":
		return validateNetworkRestore(data, "Match", "enp2s0")
	case "etc/systemd/network/09-router-wan-vlan.network":
		return validateNetworkRestore(data, "Match", "router-wan-vlan")
	case "etc/systemd/network/09-router-wan-vlan.netdev":
		return validateNetworkRestore(data, "NetDev", "router-wan-vlan")
	case "etc/systemd/network/10-wan.network.d/50-router-ipv6.conf", "etc/systemd/network/20-lan.network.d/50-router-ipv6.conf":
		if bytes.ContainsAny(data, "\\\x00") {
			return errors.New("invalid IPv6 drop-in")
		}
		for _, line := range strings.Split(string(data), "\n") {
			switch line = strings.TrimSpace(line); line {
			case "[Network]", "[Address]", "[IPv6SendRA]", "[IPv6Prefix]":
			default:
				if strings.HasPrefix(line, "[") {
					return errors.New("invalid IPv6 drop-in section")
				}
			}
		}
	}
	return nil
}

func validateNetworkRestore(data []byte, selector, iface string) error {
	if bytes.ContainsAny(data, "\\\x00") {
		return errors.New("invalid network profile")
	}
	section, names := "", 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
		}
		key, value, assignment := strings.Cut(line, "=")
		if section == selector && assignment && strings.TrimSpace(key) == "Name" {
			names++
			if strings.TrimSpace(value) != iface {
				return errors.New("invalid network interface")
			}
		}
	}
	if names != 1 {
		return errors.New("invalid network selector")
	}
	return nil
}

var restoreZapretHost = regexp.MustCompile(`^\^?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validateZapretRestore(profile, hosts []byte) error {
	if string(profile) != "alt11\n" && string(profile) != "alt11-tcp\n" {
		return errors.New("invalid Zapret profile")
	}
	for _, host := range strings.Fields(string(hosts)) {
		if len(host) > 254 || !restoreZapretHost.MatchString(host) {
			return errors.New("invalid Zapret hosts")
		}
	}
	return nil
}
