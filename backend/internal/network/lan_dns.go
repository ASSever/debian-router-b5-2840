package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type DNSRecord struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type LANDNSConfig struct {
	Primary    string      `json:"primary"`
	Backup     string      `json:"backup"`
	RouterOnly bool        `json:"router_only"`
	Records    []DNSRecord `json:"records"`
}

type LANDNSStatus struct {
	Managed bool         `json:"managed"`
	Config  LANDNSConfig `json:"config"`
}

var ErrInvalidLANDNS = errors.New("invalid_lan_dns")
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeLANDNS(config LANDNSConfig) (LANDNSConfig, error) {
	if len(config.Records) > 24 || (config.Primary == "" && config.Backup != "") {
		return LANDNSConfig{}, ErrInvalidLANDNS
	}
	for _, field := range []*string{&config.Primary, &config.Backup} {
		if *field == "" {
			continue
		}
		addr, err := netip.ParseAddr(*field)
		if err != nil || addr.Zone() != "" || !addr.IsGlobalUnicast() {
			return LANDNSConfig{}, ErrInvalidLANDNS
		}
		*field = addr.Unmap().String()
	}
	if config.Primary != "" && config.Primary == config.Backup {
		return LANDNSConfig{}, ErrInvalidLANDNS
	}
	out := make([]DNSRecord, 0, len(config.Records))
	seen := map[DNSRecord]bool{}
	for _, entry := range config.Records {
		entry.Name = strings.ToLower(strings.TrimSuffix(entry.Name, "."))
		if len(entry.Name) > 253 || !strings.Contains(entry.Name, ".") ||
			entry.Name == LANHostname || entry.Name == "localhost" || strings.HasSuffix(entry.Name, ".localhost") {
			return LANDNSConfig{}, ErrInvalidLANDNS
		}
		for _, label := range strings.Split(entry.Name, ".") {
			if !dnsLabel.MatchString(label) {
				return LANDNSConfig{}, ErrInvalidLANDNS
			}
		}
		addr, err := netip.ParseAddr(entry.Address)
		if err != nil || addr.Zone() != "" || !addr.IsGlobalUnicast() {
			return LANDNSConfig{}, ErrInvalidLANDNS
		}
		entry.Address = addr.Unmap().String()
		if seen[entry] {
			return LANDNSConfig{}, ErrInvalidLANDNS
		}
		seen[entry] = true
		out = append(out, entry)
	}
	config.Records = out
	return config, nil
}

func (m *StaticWANManager) dnsSettingsPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "lan-dns-settings.json")
}

func lanUsesDNSAddress(lan LANConfig, dns LANDNSConfig) bool {
	address := netip.MustParsePrefix(lan.Address).Addr().String()
	return dns.Primary == address || dns.Backup == address
}

func (m *StaticWANManager) readLANDNS() (LANDNSConfig, error) {
	config := LANDNSConfig{Records: []DNSRecord{}}
	data, exists, _, err := readStaticWANFile(m.dnsSettingsPath())
	if err != nil || !exists {
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	return normalizeLANDNS(config)
}

func (m *StaticWANManager) LANDNSStatus() (LANDNSStatus, error) {
	var status LANDNSStatus
	err := withFileLock(wanLockPath(m.statePath), func() error {
		lan, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		status.Managed = lan != nil
		status.Config, err = m.readLANDNS()
		return err
	})
	return status, err
}

func (m *StaticWANManager) withManagedDNS(update func(*LANConfig) error) error {
	return withFileLock(wanLockPath(m.statePath), func() error {
		pending, err := m.hasPendingTransaction()
		if err != nil {
			return err
		}
		if pending {
			return ErrStaticWANPending
		}
		lan, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		if lan == nil {
			return ErrReservationsUnavailable
		}
		return update(lan)
	})
}

func (m *StaticWANManager) ApplyLANDNS(ctx context.Context, config LANDNSConfig) error {
	config, err := normalizeLANDNS(config)
	if err != nil {
		return err
	}
	return m.withManagedDNS(func(lan *LANConfig) error {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && (prefix.Addr().Unmap().String() == config.Primary || prefix.Addr().Unmap().String() == config.Backup) {
				return ErrInvalidLANDNS
			}
		}
		txn, err := m.startTransaction(ctx, staticWANRecord{DNS: true}, m.dnsSettingsPath(), func() error {
			if err := writeJSONFile(m.dnsSettingsPath(), config); err != nil {
				return err
			}
			return m.syncLAN(ctx, lan, false)
		})
		if err != nil {
			return err
		}
		if err := m.stopRollback(ctx, txn.ID, true); err != nil {
			return err
		}
		return m.removeTransaction(filepath.Join(m.statePath, txn.ID))
	})
}

func (m *StaticWANManager) restoreDNS(ctx context.Context, directory string, backup staticWANBackup) error {
	path := m.dnsSettingsPath()
	if backup.OriginalExists {
		data, err := os.ReadFile(filepath.Join(directory, "original"))
		if err != nil {
			return err
		}
		if err := writeAtomicFile(path, data, os.FileMode(backup.OriginalMode)); err != nil {
			return err
		}
	} else if err := removeRegularFile(path); err != nil {
		return err
	}
	lan, err := ManagedLAN(m.lanPath())
	if err != nil {
		return err
	}
	return m.syncLAN(ctx, lan, false)
}

func (m *StaticWANManager) ClearLANDNSCache(ctx context.Context) error {
	return m.withManagedDNS(func(_ *LANConfig) error { return m.reloadLANDNS(ctx) })
}

func (m *StaticWANManager) reloadLANDNS(ctx context.Context) error {
	_, err := m.run(ctx, "/usr/bin/systemctl", "--system", "kill", "--signal=HUP", "--kill-whom=main", lanDNSUnit)
	return err
}

func appendLANDNS(content []byte, config LANDNSConfig) []byte {
	if config.Primary != "" {
		content = fmt.Appendf(content, "no-resolv\nstrict-order\nfast-dns-retry=500,4000\nserver=%s\n", config.Primary)
		if config.Backup != "" {
			content = fmt.Appendf(content, "server=%s\n", config.Backup)
		}
	}
	for _, entry := range config.Records {
		content = fmt.Appendf(content, "host-record=%s,%s\n", entry.Name, entry.Address)
	}
	return content
}
