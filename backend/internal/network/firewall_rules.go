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
	"slices"
	"strconv"
	"strings"
)

const maxFirewallEntries = 32

var ErrInvalidFirewallRules = errors.New("invalid_firewall_rules")

type PortForward struct {
	Protocol   string `json:"protocol"`
	WANPort    uint16 `json:"wan_port"`
	LANAddress string `json:"lan_address"`
	LANPort    uint16 `json:"lan_port"`
}

type FirewallRules struct {
	Forwards []PortForward `json:"forwards"`
	Allow    []string      `json:"allow"`
	Block    []string      `json:"block"`
}

type RuleCounter struct {
	Packets uint64 `json:"packets,string"`
	Bytes   uint64 `json:"bytes,string"`
}

type FirewallRulesStatus struct {
	Enabled  bool                   `json:"enabled"`
	Rules    FirewallRules          `json:"rules"`
	Counters map[string]RuleCounter `json:"counters"`
}

type firewallIngress struct {
	Device string
	IPv4   []string
}

func (m *StaticWANManager) firewallRulesPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "firewall-rules.json")
}

func (m *StaticWANManager) readFirewallRules() (FirewallRules, error) {
	rules := FirewallRules{Forwards: []PortForward{}, Allow: []string{}, Block: []string{}}
	data, exists, _, err := readStaticWANFile(m.firewallRulesPath())
	if err != nil || !exists {
		return rules, err
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return rules, err
	}
	return rules, nil
}

func normalizedSources(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[netip.Prefix]bool, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			if addr, addrErr := netip.ParseAddr(value); addrErr == nil && addr.Is4() {
				prefix = netip.PrefixFrom(addr, 32)
			}
		}
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			return nil, ErrInvalidFirewallRules
		}
		prefix = prefix.Masked()
		if seen[prefix] {
			return nil, ErrInvalidFirewallRules
		}
		seen[prefix] = true
		result = append(result, prefix.String())
	}
	return result, nil
}

func normalizeFirewallRules(lan LANConfig, rules FirewallRules) (FirewallRules, error) {
	if len(rules.Forwards) > maxFirewallEntries || len(rules.Allow) > maxFirewallEntries || len(rules.Block) > maxFirewallEntries {
		return FirewallRules{}, ErrInvalidFirewallRules
	}
	seen := map[string]bool{}
	for _, rule := range rules.Forwards {
		if (rule.Protocol != "tcp" && rule.Protocol != "udp") || rule.WANPort == 0 || rule.LANPort == 0 ||
			!validReservationAddress(lan, rule.LANAddress) || seen[forwardCounter(rule)] {
			return FirewallRules{}, ErrInvalidFirewallRules
		}
		seen[forwardCounter(rule)] = true
	}
	rules.Forwards = append(make([]PortForward, 0, len(rules.Forwards)), rules.Forwards...)
	var err error
	rules.Allow, err = normalizedSources(rules.Allow)
	if err != nil {
		return FirewallRules{}, err
	}
	rules.Block, err = normalizedSources(rules.Block)
	return rules, err
}

func forwardCounter(rule PortForward) string {
	return "forward_" + rule.Protocol + "_" + strconv.FormatUint(uint64(rule.WANPort), 10)
}

func (m *StaticWANManager) forwardIngress(ctx context.Context) (firewallIngress, error) {
	route, err := m.preferredWAN(ctx)
	if err != nil || route.Device == "" {
		return firewallIngress{}, err
	}
	device, err := net.InterfaceByName(route.Device)
	// The route can disappear between the dump and interface lookup.
	if err != nil {
		return firewallIngress{}, nil
	}
	addresses, err := device.Addrs()
	if err != nil {
		return firewallIngress{}, err
	}
	ingress := firewallIngress{Device: route.Device}
	for _, value := range addresses {
		prefix, err := netip.ParsePrefix(value.String())
		if err == nil && prefix.Addr().Is4() {
			ingress.IPv4 = append(ingress.IPv4, prefix.Addr().String())
		}
	}
	slices.Sort(ingress.IPv4)
	return ingress, nil
}

func renderForwardCounters(out *strings.Builder, rules FirewallRules) {
	out.WriteString(" counter source_allowed {}\n counter source_blocked {}\n counter source_not_allowed {}\n")
	for _, rule := range rules.Forwards {
		fmt.Fprintf(out, " counter %s {}\n", forwardCounter(rule))
	}
}

func renderForwardNAT(out *strings.Builder, rules FirewallRules, ingress firewallIngress) {
	if len(rules.Forwards) == 0 || len(ingress.IPv4) == 0 {
		return
	}
	fmt.Fprintf(out, " set forward_wan_ipv4 { type ipv4_addr; elements = { %s }; }\n", strings.Join(ingress.IPv4, ", "))
	for _, list := range []struct {
		Name      string
		Addresses []string
	}{{"forward_allow", rules.Allow}, {"forward_block", rules.Block}} {
		if len(list.Addresses) != 0 {
			fmt.Fprintf(out, " set %s { type ipv4_addr; flags interval; auto-merge; elements = { %s }; }\n", list.Name, strings.Join(list.Addresses, ", "))
		}
	}
	out.WriteString(" chain forward_source {\n  type filter hook prerouting priority dstnat - 1; policy accept;\n")
	for _, rule := range rules.Forwards {
		match := fmt.Sprintf("iifname %q ip daddr @forward_wan_ipv4 %s dport %d", ingress.Device, rule.Protocol, rule.WANPort)
		if len(rules.Block) != 0 {
			fmt.Fprintf(out, "  %s ip saddr @forward_block counter name source_blocked drop\n", match)
		}
		if len(rules.Allow) != 0 {
			fmt.Fprintf(out, "  %s ip saddr != @forward_allow counter name source_not_allowed drop\n", match)
			fmt.Fprintf(out, "  %s ip saddr @forward_allow counter name source_allowed\n", match)
		}
	}
	out.WriteString(" }\n chain forward_dnat {\n  type nat hook prerouting priority dstnat; policy accept;\n")
	for _, rule := range rules.Forwards {
		fmt.Fprintf(out, "  iifname %q ip daddr @forward_wan_ipv4 %s dport %d dnat ip to %s:%d\n",
			ingress.Device, rule.Protocol, rule.WANPort, rule.LANAddress, rule.LANPort)
	}
	out.WriteString(" }\n")
}

func renderForwardAllows(out *strings.Builder, rules FirewallRules, ingress firewallIngress) {
	if len(ingress.IPv4) == 0 {
		return
	}
	for _, rule := range rules.Forwards {
		fmt.Fprintf(out, "  iifname %q oifname %q ct status dnat ct original ip daddr @forward_wan_ipv4 ip daddr %s %s dport %d ct original proto-dst %d counter name %s accept\n",
			ingress.Device, LANInterface, rule.LANAddress, rule.Protocol, rule.LANPort, rule.WANPort, forwardCounter(rule))
	}
}

func (m *StaticWANManager) firewallCounters(ctx context.Context, rules FirewallRules) (map[string]RuleCounter, error) {
	output, err := m.runNetAdmin(ctx, "/usr/sbin/nft", "-j", "list", "table", "inet", firewallTable)
	if err != nil {
		return nil, err
	}
	var inventory struct {
		Entries []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
				Bytes   uint64 `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(output), &inventory); err != nil {
		return nil, err
	}
	keys := map[string]bool{"source_allowed": true, "source_blocked": true, "source_not_allowed": true}
	for _, rule := range rules.Forwards {
		keys[forwardCounter(rule)] = true
	}
	result := map[string]RuleCounter{}
	for _, item := range inventory.Entries {
		if counter := item.Counter; counter != nil && keys[counter.Name] {
			result[counter.Name] = RuleCounter{Packets: counter.Packets, Bytes: counter.Bytes}
		}
	}
	return result, nil
}

func (m *StaticWANManager) FirewallRulesStatus(ctx context.Context) (FirewallRulesStatus, error) {
	var result FirewallRulesStatus
	err := withFileLock(wanLockPath(m.statePath), func() error {
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		result.Enabled = state != nil
		result.Rules, err = m.readFirewallRules()
		if err != nil {
			return err
		}
		if result.Enabled {
			result.Counters, _ = m.firewallCounters(ctx, result.Rules)
		}
		return nil
	})
	return result, err
}

func (m *StaticWANManager) ApplyFirewallRules(ctx context.Context, requested FirewallRules) (StaticWANTransaction, error) {
	var transaction StaticWANTransaction
	err := withFileLock(wanLockPath(m.statePath), func() error {
		lan, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		if lan == nil || state == nil {
			return ErrFirewallUnavailable
		}
		for _, path := range []string{m.statePath, filepath.Join(filepath.Dir(m.statePath), "wan-pppoe")} {
			pending, err := (&StaticWANManager{statePath: path}).hasPendingTransaction()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if pending {
				return ErrStaticWANPending
			}
		}
		rules, err := normalizeFirewallRules(*lan, requested)
		if err != nil {
			return err
		}
		now := m.now()
		transaction, err = m.startTransaction(ctx, staticWANRecord{FirewallExpiry: &now, FirewallRules: true}, m.firewallRulesPath(), func() error {
			if err := writeJSONFile(m.firewallRulesPath(), rules); err != nil {
				return err
			}
			return m.installFirewallRules(ctx, *lan)
		})
		return err
	})
	return transaction, err
}

func (m *StaticWANManager) restoreFirewallRules(ctx context.Context, directory string, backup staticWANBackup) error {
	path := m.firewallRulesPath()
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
	if lan == nil {
		return ErrFirewallUnavailable
	}
	return m.installFirewallRules(ctx, *lan)
}
