package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	wanLineFile   = "09-router-wan.network"
	wanVLANFiles  = "09-router-wan-vlan"
	wanVLANName   = "router-wan-vlan"
	wanLineMarker = "# router-wan-line "
)

// WANLineConfig customizes the Ethernet transport for IPv4 DHCP.
// Zero VLAN ID means untagged; zero MTU and an empty MAC retain the defaults.
type WANLineConfig struct {
	VLANID   int    `json:"vlan_id"`
	Priority int    `json:"priority"`
	MTU      int    `json:"mtu"`
	MAC      string `json:"mac"`
}

type wanLinkSettings struct {
	MAC string `json:"mac"`
	MTU int    `json:"mtu"`
}

type wanLineProfile struct {
	Config   WANLineConfig   `json:"config"`
	Baseline wanLinkSettings `json:"baseline"`
}

type WANLineStatus struct {
	Config    WANLineConfig `json:"config"`
	Interface string        `json:"interface"`
	Lease     string        `json:"lease_state"`
	Addresses []string      `json:"addresses"`
	Gateway   string        `json:"gateway"`
}

func ValidateWANLine(config WANLineConfig) error {
	switch {
	case config.VLANID < 0 || config.VLANID > 4094:
		return errors.New("invalid_vlan_id")
	case config.Priority < 0 || config.Priority > 7 || config.VLANID == 0 && config.Priority != 0:
		return errors.New("invalid_vlan_priority")
	case config.MTU != 0 && (config.MTU < 1280 || config.MTU > 1500):
		return errors.New("invalid_wan_mtu")
	case config.MAC != "" && !validWANMAC(config.MAC):
		return errors.New("invalid_wan_mac")
	}
	return nil
}

func validWANMAC(value string) bool {
	mac, err := net.ParseMAC(value)
	return err == nil && len(mac) == 6 && mac[0]&1 == 0 && mac.String() != "00:00:00:00:00:00"
}

func (p wanLineProfile) valid() bool {
	return ValidateWANLine(p.Config) == nil && validWANMAC(p.Baseline.MAC) && p.Baseline.MTU >= 1280 && p.Baseline.MTU <= 65535
}

func (p wanLineProfile) interfaceName() string {
	if p.Config.VLANID != 0 {
		return wanVLANName
	}
	return StaticWANInterface
}

func (p wanLineProfile) settings() wanLinkSettings {
	settings := p.Baseline
	if p.Config.MAC != "" {
		mac, _ := net.ParseMAC(p.Config.MAC)
		settings.MAC = mac.String()
	}
	if p.Config.MTU != 0 {
		settings.MTU = p.Config.MTU
	}
	return settings
}

func interfaceSettings(name string) (wanLinkSettings, error) {
	link, err := net.InterfaceByName(name)
	if err != nil {
		return wanLinkSettings{}, err
	}
	return wanLinkSettings{MAC: link.HardwareAddr.String(), MTU: link.MTU}, nil
}

func wanLinePath(basePath string) string {
	return filepath.Join(filepath.Dir(basePath), wanLineFile)
}

func wanLineActive(basePath string) (bool, error) {
	_, exists, _, err := readStaticWANFile(wanLinePath(basePath))
	return exists, err
}

func decodeWANLine(content []byte) (wanLineProfile, error) {
	var profile wanLineProfile
	header, _, _ := bytes.Cut(content, []byte("\n"))
	metadata, ok := bytes.CutPrefix(header, []byte(wanLineMarker))
	if !ok || json.Unmarshal(metadata, &profile) != nil || !profile.valid() {
		return profile, errors.New("unrecognized WAN line profile")
	}
	return profile, nil
}

func renderWANLink(name string, settings wanLinkSettings) string {
	return fmt.Sprintf("[Match]\nName=%s\n\n[Link]\nMACAddress=%s\nMTUBytes=%d\n\n[Network]\nLinkLocalAddressing=no\nIPv6AcceptRA=no\n", name, settings.MAC, settings.MTU)
}

func renderLineDHCP(cloned bool) string {
	profile := "DHCP=ipv4\n\n[DHCPv4]\nRouteMetric=5000\nUseMTU=no\n"
	if cloned {
		profile += "ClientIdentifier=mac\n"
	}
	return profile
}

func renderWANLine(profile wanLineProfile) (parent, child, netdev []byte) {
	metadata, _ := json.Marshal(profile)
	header := wanLineMarker + string(metadata) + "\n"
	cloned := profile.Config.MAC != ""
	if profile.Config.VLANID == 0 {
		return []byte(header + renderWANLink(StaticWANInterface, profile.settings()) + renderLineDHCP(cloned)), nil, nil
	}
	parent = []byte(header + renderWANLink(StaticWANInterface, profile.Baseline) + "DHCP=no\nVLAN=" + wanVLANName + "\n")
	child = []byte(renderWANLink(wanVLANName, profile.settings()) + renderLineDHCP(cloned))
	netdev = []byte(fmt.Sprintf("[NetDev]\nName=%s\nKind=vlan\nMACAddress=%s\n\n[VLAN]\nId=%d\nEgressQOSMaps=0-%d\n",
		wanVLANName, profile.settings().MAC, profile.Config.VLANID, profile.Config.Priority))
	return
}

// ApplyWANLine shares the WAN lock, backup and independent timer with Static WAN.
func (m *StaticWANManager) ApplyWANLine(ctx context.Context, config WANLineConfig) (StaticWANTransaction, error) {
	if err := ValidateWANLine(config); err != nil {
		return StaticWANTransaction{}, err
	}
	return m.applyWANChange(func() (StaticWANTransaction, error) {
		base, exists, _, err := readStaticWANFile(m.configPath)
		if err != nil || !exists || !bytes.Contains(base, []byte("\nDHCP=ipv4\n")) {
			return StaticWANTransaction{}, errors.New("WAN line settings require IPv4 DHCP")
		}
		path := wanLinePath(m.configPath)
		original, managed, _, err := readStaticWANFile(path)
		if err != nil {
			return StaticWANTransaction{}, err
		}
		previous := wanLineProfile{}
		if managed {
			previous, err = decodeWANLine(original)
			if parent, _, _ := renderWANLine(previous); err == nil && !bytes.Equal(parent, original) {
				err = errors.New("WAN line profile was edited outside the router")
			}
		} else {
			previous.Baseline, err = interfaceSettings(StaticWANInterface)
		}
		if err != nil {
			return StaticWANTransaction{}, err
		}
		if err := m.checkWANLineFiles(previous); err != nil {
			return StaticWANTransaction{}, err
		}
		profile := wanLineProfile{Config: config, Baseline: previous.Baseline}
		if !profile.valid() || profile.settings().MTU > profile.Baseline.MTU {
			return StaticWANTransaction{}, errors.New("unsupported WAN link settings")
		}
		return m.startTransaction(ctx, staticWANRecord{Line: &profile}, path, func() error {
			return m.installWANLine(ctx, profile, 0644, config == (WANLineConfig{}))
		})
	})
}

func (m *StaticWANManager) checkWANLineFiles(previous wanLineProfile) error {
	_, child, netdev := renderWANLine(previous)
	for _, file := range []struct {
		suffix string
		data   []byte
	}{{".network", child}, {".netdev", netdev}} {
		current, exists, mode, err := readStaticWANFile(m.wanVLANPath(file.suffix))
		if err != nil || exists != (file.data != nil) || exists && (mode.Mode().Perm() != 0644 || !bytes.Equal(file.data, current)) {
			return errors.New("WAN VLAN configuration files have unexpected contents")
		}
	}
	if _, err := net.InterfaceByName(wanVLANName); err == nil && previous.Config.VLANID == 0 {
		return errors.New("WAN VLAN device name is already in use")
	}
	return nil
}

func (m *StaticWANManager) wanVLANPath(suffix string) string {
	return filepath.Join(filepath.Dir(m.configPath), wanVLANFiles+suffix)
}

func (m *StaticWANManager) installWANLine(ctx context.Context, p wanLineProfile, mode os.FileMode, reset bool) error {
	parent, child, netdev := renderWANLine(p)
	// VLAN ID and priority changes require a new link; networkd reload does
	// not remove an existing netdev when its configuration changes.
	if err := m.deleteWANVLAN(ctx); err != nil {
		return err
	}
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{m.wanVLANPath(".netdev"), netdev, 0644},
		{m.wanVLANPath(".network"), child, 0644},
		{wanLinePath(m.configPath), parent, mode},
	} {
		if file.data == nil {
			if err := removeRegularFile(file.path); err != nil {
				return err
			}
		} else if err := writeAtomicFile(file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	if err := m.reconfigureWAN(ctx); err != nil || !reset {
		return err
	}
	// networkd leaves configured MAC/MTU in place when a profile is removed.
	// Apply the baseline first, then let the original DHCP file take over.
	if err := waitWANLink(ctx, StaticWANInterface, p.Baseline); err != nil {
		return err
	}
	if err := removeRegularFile(wanLinePath(m.configPath)); err != nil {
		return err
	}
	return m.reconfigureWAN(ctx)
}

func (m *StaticWANManager) deleteWANVLAN(ctx context.Context) error {
	if _, err := net.InterfaceByName(wanVLANName); err != nil {
		return nil
	}
	link, err := m.readWANVLAN(ctx)
	if err != nil || link.Link != StaticWANInterface || link.LinkInfo.Kind != "vlan" {
		return errors.New("refuse to delete an unmanaged WAN VLAN device")
	}
	// networkctl delete uses netlink directly. Grant CAP_NET_ADMIN only to
	// the system-manager command that deletes this fixed virtual link.
	_, err = m.run(ctx, "/usr/bin/systemd-run", "--system", "--quiet", "--wait", "--collect",
		"--property=Type=oneshot", "--property=CapabilityBoundingSet=CAP_NET_ADMIN",
		"--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict",
		"--property=ProtectHome=yes", "--property=RestrictAddressFamilies=AF_NETLINK",
		"--", "/usr/sbin/ip", "link", "delete", "dev", wanVLANName)
	return err
}

func waitWANLink(ctx context.Context, name string, expected wanLinkSettings) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if current, err := interfaceSettings(name); err == nil && current == expected {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("WAN MAC or MTU did not reach requested value")
		case <-ticker.C:
		}
	}
}

func (m *StaticWANManager) restoreWANLine(ctx context.Context, directory string, backup staticWANBackup, applied wanLineProfile) error {
	if !backup.OriginalExists {
		return m.installWANLine(ctx, wanLineProfile{Baseline: applied.Baseline}, 0644, true)
	}
	original, err := os.ReadFile(filepath.Join(directory, "original"))
	if err != nil {
		return err
	}
	prior, err := decodeWANLine(original)
	if err != nil {
		return err
	}
	return m.installWANLine(ctx, prior, os.FileMode(backup.OriginalMode), false)
}

func (m *StaticWANManager) WANLineStatus() (WANLineStatus, error) {
	var status WANLineStatus
	err := withFileLock(wanLockPath(m.statePath), func() error {
		data, exists, _, err := readStaticWANFile(wanLinePath(m.configPath))
		if err != nil {
			return err
		}
		profile := wanLineProfile{}
		if exists {
			profile, err = decodeWANLine(data)
			if err != nil {
				return err
			}
		}
		status.Config = profile.Config
		status.Interface = profile.interfaceName()
		status.Lease = "waiting"
		status.Addresses = []string{}
		lease, err := ReadDHCPStatus(status.Interface)
		if err == nil {
			status.Lease, status.Addresses, status.Gateway = lease.LeaseState, lease.Addresses, lease.Gateway
		}
		return nil
	})
	return status, err
}

func (m *StaticWANManager) lineMatches(ctx context.Context, p wanLineProfile) bool {
	name := p.interfaceName()
	settings, err := interfaceSettings(name)
	if err != nil || settings != p.settings() {
		return false
	}
	if p.Config.VLANID != 0 && !m.vlanMatches(ctx, p.Config) {
		return false
	}
	lease, err := ReadDHCPStatus(name)
	return err == nil && lease.LinkState == "carrier" && lease.LeaseState == "leased" &&
		len(lease.Addresses) > 0 && lease.Gateway != ""
}

type wanVLANLink struct {
	Link     string `json:"link"`
	LinkInfo struct {
		Kind string `json:"info_kind"`
		Data struct {
			ID       int `json:"id"`
			Priority []struct {
				From int `json:"from"`
				To   int `json:"to"`
			} `json:"egress_qos"`
		} `json:"info_data"`
	} `json:"linkinfo"`
}

func (m *StaticWANManager) readWANVLAN(ctx context.Context) (wanVLANLink, error) {
	output, err := m.run(ctx, "/usr/sbin/ip", "-details", "-json", "link", "show", "dev", wanVLANName)
	if err != nil {
		return wanVLANLink{}, err
	}
	var links []wanVLANLink
	if json.Unmarshal([]byte(output), &links) != nil || len(links) != 1 {
		return wanVLANLink{}, errors.New("invalid WAN VLAN link details")
	}
	return links[0], nil
}

func (m *StaticWANManager) vlanMatches(ctx context.Context, config WANLineConfig) bool {
	link, err := m.readWANVLAN(ctx)
	if err != nil || link.Link != StaticWANInterface || link.LinkInfo.Kind != "vlan" || link.LinkInfo.Data.ID != config.VLANID {
		return false
	}
	for _, mapping := range link.LinkInfo.Data.Priority {
		if mapping.From == 0 {
			return mapping.To == config.Priority
		}
	}
	return config.Priority == 0
}
