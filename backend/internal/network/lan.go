package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	LANInterface   = "enp2s0"
	LANHostname    = "router.home.arpa"
	LANPort        = "8443"
	LANProfilePath = "/etc/systemd/network/20-lan.network"
	lanMarker      = "# router-lan "
	lanDNSUnit     = "debian-router-lan-dnsmasq.service"
	lanTable       = "debian_router_lan"
)

type LANConfig struct {
	Address   string `json:"address"`
	PoolStart string `json:"pool_start"`
	PoolEnd   string `json:"pool_end"`
}

type LANStatus struct {
	Config    LANConfig             `json:"config"`
	Addresses []string              `json:"addresses"`
	Managed   bool                  `json:"managed"`
	WANOnline bool                  `json:"wan_online"`
	Pending   *StaticWANTransaction `json:"pending,omitempty"`
}

type LANConflict struct {
	Interfaces []string   `json:"interfaces"`
	Prefix     string     `json:"prefix"`
	Suggestion *LANConfig `json:"suggestion,omitempty"`
}

func (*LANConflict) Error() string { return "lan_overlap" }

var defaultLAN = LANConfig{"192.168.10.1/24", "192.168.10.100", "192.168.10.200"}

func ValidateLAN(config LANConfig) error {
	prefix, err := netip.ParsePrefix(config.Address)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 ||
		!usableLANAddress(prefix.Masked(), prefix.Addr()) {
		return errors.New("invalid_lan_address")
	}
	first, firstErr := netip.ParseAddr(config.PoolStart)
	last, lastErr := netip.ParseAddr(config.PoolEnd)
	if firstErr != nil || lastErr != nil || !usableLANAddress(prefix.Masked(), first) ||
		!usableLANAddress(prefix.Masked(), last) || first.Compare(last) > 0 ||
		first.Compare(prefix.Addr()) <= 0 && last.Compare(prefix.Addr()) >= 0 {
		return errors.New("invalid_lan_pool")
	}
	return nil
}

func usableLANAddress(subnet netip.Prefix, address netip.Addr) bool {
	return address.Is4() && isUsableIPv4(address) && subnet.Contains(address) &&
		address != subnet.Addr() && !isBroadcastAddress(subnet, address)
}

type upstreamPrefix struct {
	name   string
	subnet netip.Prefix
}

func (m *StaticWANManager) upstreamLANPrefixes(ctx context.Context) ([]upstreamPrefix, error) {
	links, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []upstreamPrefix
	for _, link := range links {
		if link.Name == LANInterface || link.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := link.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil && prefix.Addr().Is4() {
				result = append(result, upstreamPrefix{link.Name, prefix.Masked()})
			}
		}
	}
	// Point-to-point peers and configured link routes may have no matching
	// local address prefix. Ignore default and gateway-routed networks.
	output, err := m.run(ctx, "/usr/sbin/ip", "-4", "-json", "route", "show", "scope", "link")
	if err != nil {
		return nil, err
	}
	var routes []struct {
		Destination string `json:"dst"`
		Device      string `json:"dev"`
	}
	if err := json.Unmarshal([]byte(output), &routes); err != nil {
		return nil, err
	}
	for _, route := range routes {
		if route.Device == LANInterface || route.Device == "lo" || route.Device == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Destination)
		if err != nil {
			if addr, err := netip.ParseAddr(route.Destination); err == nil && addr.Is4() {
				prefix = netip.PrefixFrom(addr, 32)
			}
		}
		if prefix.IsValid() && prefix.Addr().Is4() && prefix.Bits() > 0 {
			result = append(result, upstreamPrefix{route.Device, prefix.Masked()})
		}
	}
	return result, nil
}

func overlappingLAN(config LANConfig, upstream []upstreamPrefix) *LANConflict {
	prefix := netip.MustParsePrefix(config.Address).Masked()
	var conflict *LANConflict
	seen := make(map[string]bool)
	for _, route := range upstream {
		if !prefix.Overlaps(route.subnet) {
			continue
		}
		if conflict == nil {
			conflict = &LANConflict{Prefix: route.subnet.String()}
		}
		if !seen[route.name] && len(conflict.Interfaces) < 12 {
			conflict.Interfaces = append(conflict.Interfaces, route.name)
			seen[route.name] = true
		}
	}
	if conflict != nil {
		for _, candidate := range []LANConfig{
			defaultLAN,
			{"192.168.50.1/24", "192.168.50.100", "192.168.50.200"},
			{"172.23.50.1/24", "172.23.50.100", "172.23.50.200"},
			{"10.77.50.1/24", "10.77.50.100", "10.77.50.200"},
		} {
			candidatePrefix := netip.MustParsePrefix(candidate.Address).Masked()
			safe := true
			for _, route := range upstream {
				safe = safe && !candidatePrefix.Overlaps(route.subnet)
			}
			if safe {
				conflict.Suggestion = &candidate
				break
			}
		}
	}
	return conflict
}

func renderLANNetwork(config LANConfig) []byte {
	data, _ := json.Marshal(config)
	return fmt.Appendf(nil, "%s%s\n[Match]\nName=%s\n\n[Link]\nRequiredForOnline=no\n\n[Network]\nAddress=%s\nDHCP=no\nConfigureWithoutCarrier=yes\nLinkLocalAddressing=no\nIPv6AcceptRA=no\n",
		lanMarker, data, LANInterface, config.Address)
}

func parseLANNetwork(data []byte) (*LANConfig, error) {
	first, _, _ := bytes.Cut(data, []byte("\n"))
	payload, managed := bytes.CutPrefix(first, []byte(lanMarker))
	if !managed {
		return nil, nil
	}
	var config LANConfig
	if json.Unmarshal(payload, &config) != nil || ValidateLAN(config) != nil ||
		!bytes.Equal(data, renderLANNetwork(config)) {
		return nil, errors.New("invalid managed LAN profile")
	}
	return &config, nil
}

// ManagedLAN reads the exact profile consumed by the agent and the HTTPS listener.
func ManagedLAN(path string) (*LANConfig, error) {
	data, exists, _, err := readStaticWANFile(path)
	if err != nil || !exists {
		return nil, err
	}
	return parseLANNetwork(data)
}

func (m *StaticWANManager) lanPath() string {
	return filepath.Join(filepath.Dir(m.configPath), filepath.Base(LANProfilePath))
}

func (m *StaticWANManager) ApplyLAN(ctx context.Context, config LANConfig) (StaticWANTransaction, error) {
	if err := ValidateLAN(config); err != nil {
		return StaticWANTransaction{}, err
	}
	if err := m.ensureStateDirectory(); err != nil {
		return StaticWANTransaction{}, err
	}
	var transaction StaticWANTransaction
	err := withFileLock(wanLockPath(m.statePath), func() error {
		for _, path := range []string{m.statePath, filepath.Join(filepath.Dir(m.statePath), "wan-pppoe")} {
			pending, err := (&StaticWANManager{statePath: path}).hasPendingTransaction()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if pending {
				return ErrStaticWANPending
			}
		}
		if _, err := net.InterfaceByName(LANInterface); err != nil {
			return err
		}
		upstream, err := m.upstreamLANPrefixes(ctx)
		if err != nil {
			return err
		}
		if conflict := overlappingLAN(config, upstream); conflict != nil {
			return conflict
		}
		_, reservations, err := m.readReservations()
		if err != nil {
			return err
		}
		for _, reservation := range reservations {
			if !validReservationAddress(config, reservation.Address) {
				return ErrReservationConflict
			}
		}
		dnsConfig, err := m.readLANDNS()
		if err != nil {
			return err
		}
		if lanUsesDNSAddress(config, dnsConfig) {
			return ErrInvalidLANDNS
		}
		rules, err := m.readFirewallRules()
		if err != nil {
			return err
		}
		if _, err := normalizeFirewallRules(config, rules); err != nil {
			return err
		}
		data, exists, _, err := readStaticWANFile(m.lanPath())
		if err != nil {
			return err
		}
		if exists {
			old, err := parseLANNetwork(data)
			if err != nil {
				return err
			}
			if old == nil && !bytes.HasPrefix(data, []byte("[Match]\nName="+LANInterface+"\n\n")) {
				return errors.New("existing LAN profile is not scoped to the LAN port")
			}
		}
		transaction, err = m.startTransaction(ctx, staticWANRecord{LAN: &config}, m.lanPath(), func() error {
			if err := writeAtomicFile(m.lanPath(), renderLANNetwork(config), 0644); err != nil {
				return err
			}
			return m.syncLAN(ctx, &config, true)
		})
		return err
	})
	return transaction, err
}

func (m *StaticWANManager) LANStatus(ctx context.Context) (LANStatus, error) {
	result := LANStatus{Config: defaultLAN, Addresses: []string{}}
	err := withFileLock(wanLockPath(m.statePath), func() error {
		config, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		if config != nil {
			result.Config, result.Managed = *config, true
		}
		if status, err := m.readStatus(LANInterface); err == nil {
			result.Addresses = status.Addresses
		}
		upstream, err := m.upstreamLANPrefixes(ctx)
		if err != nil {
			return err
		}
		for _, p := range upstream {
			if p.name == StaticWANInterface || p.name == wanVLANName || p.name == "ppp0" {
				result.WANOnline = true
			}
		}
		entries, err := os.ReadDir(m.statePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !validStaticWANTransactionID(entry.Name()) {
				continue
			}
			_, record, err := m.loadTransaction(entry.Name())
			if err == nil && record.LAN != nil {
				result.Pending = &StaticWANTransaction{ID: entry.Name(), ExpiresAt: *record.LANExpiry}
				break
			}
		}
		return nil
	})
	return result, err
}

func (m *StaticWANManager) lanMatches(ctx context.Context, config LANConfig, destination string) bool {
	if destination != netip.MustParsePrefix(config.Address).Addr().String() {
		return false
	}
	upstream, err := m.upstreamLANPrefixes(ctx)
	if err != nil || overlappingLAN(config, upstream) != nil {
		return false
	}
	status, err := m.readStatus(LANInterface)
	if err != nil || status.LinkState != "carrier" {
		return false
	}
	for _, address := range status.Addresses {
		if address == config.Address {
			_, err := m.run(ctx, "/usr/bin/systemctl", "--system", "is-active", lanDNSUnit)
			return err == nil
		}
	}
	return false
}

func (m *StaticWANManager) restoreLAN(ctx context.Context, directory string, backup staticWANBackup) error {
	if ValidateLAN(*backup.LAN) != nil {
		return errors.New("invalid LAN rollback metadata")
	}
	var config *LANConfig
	if backup.OriginalExists {
		data, err := os.ReadFile(filepath.Join(directory, "original"))
		if err != nil {
			return err
		}
		config, err = parseLANNetwork(data)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(m.lanPath(), data, os.FileMode(backup.OriginalMode)); err != nil {
			return err
		}
	} else if err := removeRegularFile(m.lanPath()); err != nil {
		return err
	}
	if err := m.syncLAN(ctx, config, true); err != nil {
		return err
	}
	if backup.OriginalExists {
		return nil
	}
	status, err := m.readStatus(LANInterface)
	if err != nil {
		return err
	}
	for _, address := range status.Addresses {
		if address == backup.LAN.Address {
			return m.lanNetAdmin(ctx, "/usr/sbin/ip", "address", "del", address, "dev", LANInterface)
		}
	}
	return nil
}

func (m *StaticWANManager) RecoverLAN(ctx context.Context) error {
	config, err := ManagedLAN(m.lanPath())
	if err != nil || config == nil {
		return err
	}
	return withFileLock(wanLockPath(m.statePath), func() error { return m.syncLAN(ctx, config, false) })
}

func (m *StaticWANManager) lanNetAdmin(ctx context.Context, argv ...string) error {
	_, err := m.runNetAdmin(ctx, argv...)
	return err
}

func (m *StaticWANManager) runNetAdmin(ctx context.Context, argv ...string) (string, error) {
	args := []string{"--system", "--quiet", "--wait", "--pipe", "--collect", "--property=Type=oneshot",
		"--property=CapabilityBoundingSet=CAP_NET_ADMIN", "--property=NoNewPrivileges=yes",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes",
		"--property=RestrictAddressFamilies=AF_UNIX AF_NETLINK", "--"}
	return m.run(ctx, "/usr/bin/systemd-run", append(args, argv...)...)
}

func renderLANFirewall(config LANConfig, routerOnly bool) []byte {
	prefix := netip.MustParsePrefix(config.Address)
	address, subnet := prefix.Addr(), prefix.Masked()
	rules := fmt.Appendf(nil, `destroy table inet %s
table inet %s {
 chain input {
  type filter hook input priority -10; policy accept;
  ip daddr %s tcp dport { 53, %s } iifname "%s" ip saddr %s counter accept
  ip daddr %s udp dport 53 iifname "%s" ip saddr %s counter accept
  ip daddr %s tcp dport { 53, %s } counter drop
  ip daddr %s udp dport 53 counter drop
 }
`, lanTable, lanTable, address, LANPort, LANInterface, subnet, address, LANInterface, subnet, address, LANPort, address)
	if routerOnly {
		rules = fmt.Appendf(rules, " chain forward {\n  type filter hook forward priority -10; policy accept;\n  iifname \"%s\" meta l4proto { tcp, udp } th dport 53 counter drop\n }\n", LANInterface)
	}
	return append(rules, "}\n"...)
}

func (m *StaticWANManager) syncLAN(ctx context.Context, config *LANConfig, reconfigure bool) error {
	var dnsConfig LANDNSConfig
	if config != nil {
		var err error
		dnsConfig, err = m.readLANDNS()
		if err != nil {
			return err
		}
		if lanUsesDNSAddress(*config, dnsConfig) {
			return ErrInvalidLANDNS
		}
	}
	load, err := m.run(ctx, "/usr/bin/systemctl", "--system", "show", "--property=LoadState", "--value", lanDNSUnit)
	if err != nil {
		return err
	}
	if strings.TrimSpace(load) == "loaded" {
		if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "stop", lanDNSUnit); err != nil {
			return err
		}
	}
	if config != nil {
		if err := m.waitLANUnitCollected(ctx); err != nil {
			return err
		}
	}
	state := filepath.Dir(m.statePath)
	rules := filepath.Join(state, "lan-nftables.conf")
	if config == nil {
		if err := m.lanNetAdmin(ctx, "/usr/sbin/nft", "destroy", "table", "inet", lanTable); err != nil {
			return err
		}
	} else {
		if err := writeAtomicFile(rules, renderLANFirewall(*config, dnsConfig.RouterOnly), 0600); err != nil {
			return err
		}
		if err := m.lanNetAdmin(ctx, "/usr/sbin/nft", "-f", rules); err != nil {
			return err
		}
	}
	if reconfigure {
		if _, err := m.run(ctx, "/usr/bin/networkctl", "reload"); err != nil {
			return err
		}
		if _, err := m.run(ctx, "/usr/bin/networkctl", "reconfigure", LANInterface); err != nil {
			return err
		}
	}
	if config != nil {
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		if state != nil {
			if err := m.installFirewallRules(ctx, *config); err != nil {
				return err
			}
		}
	}
	dns := filepath.Join(state, "lan-dnsmasq.conf")
	if config == nil {
		return errors.Join(removeRegularFile(rules), removeRegularFile(dns))
	}
	address := netip.MustParsePrefix(config.Address).Addr()
	hosts := m.reservationPath()
	if _, exists, _, err := readStaticWANFile(hosts); err != nil {
		return err
	} else if !exists {
		if err := writeAtomicFile(hosts, nil, 0644); err != nil {
			return err
		}
	}
	content := fmt.Appendf(nil, "interface=%s\nexcept-interface=lo\nbind-dynamic\nuser=nobody\ngroup=nogroup\nlocal=/%s/\nhost-record=%s,%s\ndhcp-authoritative\ndhcp-range=%s,%s,12h\ndhcp-option=option:router,%s\ndhcp-option=option:dns-server,%s\ndhcp-leasefile=/var/lib/debian-router-lan/leases/dnsmasq.leases\n",
		LANInterface, LANHostname, LANHostname, address, config.PoolStart, config.PoolEnd, address, address)
	content = fmt.Appendf(content, "dhcp-hostsfile=%s\n", hosts)
	content = appendLANDNS(content, dnsConfig)
	if err := writeAtomicFile(dns, content, 0600); err != nil {
		return err
	}
	_, err = m.run(ctx, "/usr/bin/systemd-run", "--system", "--quiet", "--collect", "--unit="+lanDNSUnit,
		"--property=Type=forking", "--property=PIDFile=/run/debian-router-lan/dnsmasq.pid",
		"--property=RuntimeDirectory=debian-router-lan", "--property=StateDirectory=debian-router-lan",
		"--property=ProtectSystem=strict",
		"--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=ExecStartPre=/usr/bin/install -d -o nobody -g nogroup -m 0700 /var/lib/debian-router-lan/leases",
		"--", "/usr/sbin/dnsmasq", "--pid-file=/run/debian-router-lan/dnsmasq.pid", "--conf-file="+dns)
	return err
}

func (m *StaticWANManager) waitLANUnitCollected(ctx context.Context) error {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := m.run(deadline, "/usr/bin/systemctl", "--system", "show", "--property=LoadState", "--value", lanDNSUnit)
		if err != nil {
			return err
		}
		if strings.TrimSpace(state) == "not-found" {
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("LAN DHCP unit was not collected")
		case <-ticker.C:
		}
	}
}
