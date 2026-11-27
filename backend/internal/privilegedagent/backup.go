package privilegedagent

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

const MaxBackupConfigBytes = 8 << 20

type BackupConfig struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

var backupPaths = []string{
	"etc/debian-router-b5-2840/monitor.json",
	"etc/debian-router-b5-2840/failover.json",
	"etc/debian-router-b5-2840/recovery.json",
	"etc/debian-router-b5-2840/mqtt.json",
	"etc/debian-router-b5-2840/ddns.json",
	"etc/debian-router-sing-box.json",
	"etc/systemd/network/10-wan.network",
	"etc/systemd/network/20-lan.network",
	"etc/systemd/network/09-router-wan-vlan.netdev",
	"etc/systemd/network/09-router-wan-vlan.network",
	"etc/systemd/network/10-wan.network.d/50-router-ipv6.conf",
	"etc/systemd/network/20-lan.network.d/50-router-ipv6.conf",
	"etc/systemd/network/20-router-dhcp.hosts",
	"var/lib/debian-router-agent/lan-dns-settings.json",
	"var/lib/debian-router-agent/firewall.json",
	"var/lib/debian-router-agent/firewall-rules.json",
	"var/lib/debian-router-agent/routing.json",
	"var/lib/debian-router-agent/pppoe-active/options",
}

var proxyBackupGeneration = regexp.MustCompile(`^(initial|generation-[0-9]+)$`)
var zapretBackupGeneration = regexp.MustCompile(`^profile\.[[:alnum:]]{8}$`)

const proxyBackupDir = "var/lib/debian-router-agent/proxy-routing"
const zapretBackupDir = "etc/debian-router-b5-2840/zapret"
const APIBackupSubscription = "api/proxy-subscription.json"

var backupProfiles = []struct {
	dir, link string
	pattern   *regexp.Regexp
	names     []string
}{
	{proxyBackupDir, "active", proxyBackupGeneration, []string{"config.json", "profile.json"}},
	{zapretBackupDir, "current", zapretBackupGeneration, []string{"profile", "exclude-hosts.txt"}},
}

// IsBackupConfig recognizes the logical paths emitted in version 1 archives.
func IsBackupConfig(name string) bool {
	if name == APIBackupSubscription || slices.Contains(backupPaths, name) {
		return true
	}
	for _, profile := range backupProfiles {
		for _, file := range profile.names {
			if name == profile.dir+"/"+profile.link+"/"+file {
				return true
			}
		}
	}
	return false
}

func readBackupConfigs(rootPath string) ([]BackupConfig, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := make([]BackupConfig, 0, len(backupPaths))
	remaining := int64(MaxBackupConfigBytes)
	add := func(path, name string, optional bool) error {
		data, err := ReadBackupFile(root, path, remaining)
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		remaining -= int64(len(data))
		files = append(files, BackupConfig{Name: name, Data: data})
		return nil
	}
	for _, name := range backupPaths {
		if err := add(name, name, true); err != nil {
			return nil, err
		}
	}
	for _, profile := range backupProfiles {
		path := filepath.Join(rootPath, profile.dir, profile.link)
		target, err := os.Readlink(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if filepath.IsAbs(target) && filepath.Dir(target) == filepath.Dir(path) && profile.link == "current" {
			target = filepath.Base(target)
		}
		if err != nil || !profile.pattern.MatchString(target) {
			return nil, errors.New("invalid active backup profile")
		}
		for _, name := range profile.names {
			optional := target == "initial" && name == "profile.json"
			if err := add(profile.dir+"/"+target+"/"+name, profile.dir+"/"+profile.link+"/"+name, optional); err != nil {
				return nil, err
			}
		}
		if current, err := os.Readlink(path); err != nil ||
			(current != target && (profile.link != "current" || current != filepath.Join(filepath.Dir(path), target))) {
			return nil, errors.New("active backup profile changed")
		}
	}
	return files, nil
}

// ReadBackupFile reads a fixed regular file below a root controlled by the service.
func ReadBackupFile(root *os.Root, name string, limit int64) ([]byte, error) {
	if !filepath.IsLocal(name) {
		return nil, errors.New("invalid backup path")
	}
	for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
		info, err := root.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("backup parent is not a directory")
		}
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("unsafe or oversized backup file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("backup file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("backup file exceeded size limit")
	}
	return data, err
}
