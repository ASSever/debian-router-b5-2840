package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	firewallTable     = "debian_router_edge"
	wanRouteTable     = "24224"
	wanPolicyPriority = 24224
	wanRouteMetric    = 10
)

var ErrFirewallUnavailable = errors.New("firewall_unavailable")

type FirewallStatus struct {
	Enabled bool                  `json:"enabled"`
	WAN     string                `json:"wan,omitempty"`
	Pending *StaticWANTransaction `json:"pending,omitempty"`
}

type forwardingSnapshot struct {
	Index      int    `json:"index"`
	Forwarding string `json:"forwarding"`
	RPFilter   string `json:"rp_filter"`
}

type firewallState struct {
	Boot       string                        `json:"boot"`
	Interfaces map[string]forwardingSnapshot `json:"interfaces"`
}

type firewallRoute struct {
	Device      string          `json:"dev"`
	Gateway     string          `json:"gateway"`
	Metric      int             `json:"metric"`
	Table       json.RawMessage `json:"table"`
	Destination string          `json:"dst"`
	Type        string          `json:"type"`
	Flags       []string        `json:"flags"`
}

func isFirewallWAN(name string) bool {
	return name == StaticWANInterface || name == wanVLANName || name == "ppp0"
}

func selectWANRoute(routes []firewallRoute) firewallRoute {
	var selected firewallRoute
	for _, route := range routes {
		if !isFirewallWAN(route.Device) || route.Destination != "default" ||
			route.Type != "" && route.Type != "unicast" {
			continue
		}
		unusable := false
		for _, flag := range route.Flags {
			unusable = unusable || flag == "dead" || flag == "linkdown"
		}
		if route.Gateway != "" {
			ip, err := netip.ParseAddr(route.Gateway)
			unusable = unusable || err != nil || !ip.Is4() || !isUsableIPv4(ip)
		}
		if !unusable && (selected.Device == "" || route.Metric < selected.Metric) {
			selected = route
		}
	}
	return selected
}

func renderFirewall(lan LANConfig, rules FirewallRules, ingress firewallIngress) []byte {
	subnet := netip.MustParsePrefix(lan.Address).Masked()
	var out strings.Builder
	fmt.Fprintf(&out, `destroy table inet %[1]s
table inet %[1]s {
 set wan_devices {
  type ifname
  elements = { "%[2]s", "%[3]s", "ppp0" }
 }
`, firewallTable, StaticWANInterface, wanVLANName)
	renderForwardCounters(&out, rules)
	renderForwardNAT(&out, rules, ingress)
	out.WriteString(`
 chain input {
  type filter hook input priority filter; policy accept;
  iifname @wan_devices tcp dport { 22, 80, 443, 8080, 8081, 8443 } counter drop
  iifname @wan_devices ct state established,related accept
  iifname @wan_devices ip protocol udp udp sport 67 udp dport 68 accept
  iifname @wan_devices meta nfproto ipv6 udp sport 547 udp dport 546 accept
  iifname @wan_devices meta l4proto ipv6-icmp accept
  iifname @wan_devices counter drop
 }
 chain forward {
  type filter hook forward priority filter; policy accept;
`)
	renderForwardAllows(&out, rules, ingress)
	fmt.Fprintf(&out, `  iifname @wan_devices oifname "%[1]s" ct status dnat counter drop
  iifname "%[1]s" oifname @wan_devices ip saddr %[2]s ct state new,established,related counter accept
  iifname @wan_devices oifname "%[1]s" ip daddr %[2]s ct state established,related counter accept
  iifname "%[1]s" counter drop
  oifname "%[1]s" counter drop
  iifname @wan_devices counter drop
  oifname @wan_devices counter drop
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  iifname "%[1]s" oifname @wan_devices ip saddr %[2]s counter masquerade
 }
}
`, LANInterface, subnet)
	return []byte(out.String())
}

func (m *StaticWANManager) firewallPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "firewall.json")
}

func (m *StaticWANManager) readFirewall() (*firewallState, error) {
	data, exists, _, err := readStaticWANFile(m.firewallPath())
	if err != nil || !exists {
		return nil, err
	}
	var state firewallState
	if err := json.Unmarshal(data, &state); err != nil || state.Boot == "" || state.Interfaces == nil {
		return nil, errors.New("invalid firewall state")
	}
	return &state, nil
}

func currentBoot() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
}

func (m *StaticWANManager) ipRoutes(ctx context.Context, args ...string) ([]firewallRoute, error) {
	output, err := m.run(ctx, "/usr/sbin/ip", append([]string{"-4", "-json", "route", "show"}, args...)...)
	if err != nil {
		return nil, err
	}
	var routes []firewallRoute
	err = json.Unmarshal([]byte(output), &routes)
	return routes, err
}

func (m *StaticWANManager) preferredWAN(ctx context.Context) (firewallRoute, error) {
	routes, err := m.ipRoutes(ctx, "default")
	if err != nil {
		return firewallRoute{}, err
	}
	return selectWANRoute(routes), nil
}

func (m *StaticWANManager) policyRuleStatus(ctx context.Context) (matching, foreign bool, err error) {
	output, err := m.run(ctx, "/usr/sbin/ip", "-4", "-json", "rule", "show")
	if err != nil {
		return false, false, err
	}
	var rules []struct {
		Priority int             `json:"priority"`
		Input    string          `json:"iif"`
		Table    json.RawMessage `json:"table"`
		Source   string          `json:"src"`
	}
	if err := json.Unmarshal([]byte(output), &rules); err != nil {
		return false, false, err
	}
	for _, rule := range rules {
		if rule.Priority != wanPolicyPriority {
			continue
		}
		own := rule.Input == LANInterface && rule.Source == "all" &&
			strings.Trim(string(rule.Table), `"`) == wanRouteTable
		matching = matching || own
		foreign = foreign || !own
	}
	return matching, foreign, nil
}

func (m *StaticWANManager) policyRoutes(ctx context.Context) ([]firewallRoute, error) {
	routes, err := m.ipRoutes(ctx, "table", "all")
	if err != nil {
		return nil, err
	}
	var result []firewallRoute
	for _, route := range routes {
		if strings.Trim(string(route.Table), `"`) == wanRouteTable {
			result = append(result, route)
		}
	}
	return result, nil
}

func (m *StaticWANManager) firewallTableExists(ctx context.Context) (bool, error) {
	output, err := m.runNetAdmin(ctx, "/usr/sbin/nft", "-j", "list", "tables")
	if err != nil {
		return false, err
	}
	var inventory struct {
		Entries []struct {
			Table struct {
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"table"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(output), &inventory); err != nil {
		return false, err
	}
	for _, entry := range inventory.Entries {
		if entry.Table.Family == "inet" && entry.Table.Name == firewallTable {
			return true, nil
		}
	}
	return false, nil
}

func (m *StaticWANManager) installFirewallRules(ctx context.Context, lan LANConfig) error {
	content, err := m.firewallContent(ctx, lan)
	if err != nil {
		return err
	}
	return m.applyFirewallContent(ctx, content)
}

func (m *StaticWANManager) firewallContent(ctx context.Context, lan LANConfig) ([]byte, error) {
	rules, err := m.readFirewallRules()
	if err != nil {
		return nil, err
	}
	rules, err = normalizeFirewallRules(lan, rules)
	if err != nil {
		return nil, err
	}
	var ingress firewallIngress
	if len(rules.Forwards) != 0 {
		ingress, err = m.forwardIngress(ctx)
		if err != nil {
			return nil, err
		}
	}
	return renderFirewall(lan, rules, ingress), nil
}

func (m *StaticWANManager) appliedFirewallPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "firewall.nft")
}

func (m *StaticWANManager) applyFirewallContent(ctx context.Context, content []byte) error {
	path := m.appliedFirewallPath() + ".next"
	if err := writeAtomicFile(path, content, 0600); err != nil {
		return err
	}
	defer os.Remove(path)
	if err := m.lanNetAdmin(ctx, "/usr/sbin/nft", "-c", "-f", path); err != nil {
		return err
	}
	if err := m.lanNetAdmin(ctx, "/usr/sbin/nft", "-f", path); err != nil {
		return err
	}
	return os.Rename(path, m.appliedFirewallPath())
}

func (m *StaticWANManager) syncPolicyRoute(ctx context.Context, desired firewallRoute) error {
	routes, err := m.policyRoutes(ctx)
	if err != nil {
		return err
	}
	var fallback, matching, hasWAN bool
	for _, route := range routes {
		fallback = fallback || route.Type == "unreachable" && route.Destination == "default"
		if route.Metric == wanRouteMetric && route.Destination == "default" {
			hasWAN = true
			matching = route.Device == desired.Device && route.Gateway == desired.Gateway
		}
	}
	if !fallback {
		if err := m.lanNetAdmin(ctx, "/usr/sbin/ip", "-4", "route", "replace", "unreachable", "default",
			"table", wanRouteTable, "metric", "32767"); err != nil {
			return err
		}
	}
	if desired.Device == "" && hasWAN {
		return m.lanNetAdmin(ctx, "/usr/sbin/ip", "-4", "route", "del", "default", "table", wanRouteTable, "metric", "10")
	}
	if desired.Device != "" && !matching {
		args := []string{"/usr/sbin/ip", "-4", "route", "replace", "default", "table", wanRouteTable, "metric", "10"}
		if desired.Gateway != "" {
			args = append(args, "via", desired.Gateway, "dev", desired.Device, "onlink")
		} else {
			args = append(args, "dev", desired.Device)
		}
		return m.lanNetAdmin(ctx, args...)
	}
	return nil
}

func readInterfaceSetting(name, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc/sys/net/ipv4/conf", name, key))
	return strings.TrimSpace(string(data)), err
}

func (m *StaticWANManager) enableForwarding(ctx context.Context, state *firewallState, names ...string) error {
	allRPF, err := readInterfaceSetting("all", "rp_filter")
	if err != nil {
		return err
	}
	var settings []string
	changed := false
	for _, name := range names {
		if name == "" {
			continue
		}
		link, err := net.InterfaceByName(name)
		if err != nil {
			return err
		}
		forward, err := readInterfaceSetting(name, "forwarding")
		if err != nil {
			return err
		}
		rpf, err := readInterfaceSetting(name, "rp_filter")
		if err != nil {
			return err
		}
		if state.Interfaces[name].Index != link.Index {
			state.Interfaces[name] = forwardingSnapshot{Index: link.Index, Forwarding: forward, RPFilter: rpf}
			changed = true
		}
		if forward != "1" {
			settings = append(settings, "net.ipv4.conf."+name+".forwarding=1")
		}
		if isFirewallWAN(name) && rpf != "2" && (allRPF == "1" || rpf == "1") {
			settings = append(settings, "net.ipv4.conf."+name+".rp_filter=2")
		}
	}
	if changed {
		if err := writeJSONFile(m.firewallPath(), state); err != nil {
			return err
		}
	}
	if len(settings) == 0 {
		return nil
	}
	return m.lanNetAdmin(ctx, append([]string{"/usr/sbin/sysctl", "-q", "-w"}, settings...)...)
}

func (m *StaticWANManager) syncFirewallRoute(ctx context.Context, state *firewallState) error {
	route, err := m.preferredWAN(ctx)
	if err != nil {
		return err
	}
	if err := m.syncPolicyRoute(ctx, route); err != nil {
		return err
	}
	rule, foreign, err := m.policyRuleStatus(ctx)
	if err != nil {
		return err
	}
	if foreign {
		return errors.New("reserved routing rule priority is in use")
	}
	if !rule {
		if err := m.lanNetAdmin(ctx, "/usr/sbin/ip", "-4", "rule", "add", "priority", strconv.Itoa(wanPolicyPriority),
			"iif", LANInterface, "lookup", wanRouteTable); err != nil {
			return err
		}
	}
	return m.enableForwarding(ctx, state, LANInterface, route.Device)
}

func (m *StaticWANManager) ApplyFirewall(ctx context.Context) (StaticWANTransaction, error) {
	var txn StaticWANTransaction
	err := withFileLock(wanLockPath(m.statePath), func() error {
		lan, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		if lan == nil {
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
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		if state != nil {
			return ErrFirewallUnavailable
		}
		route, err := m.preferredWAN(ctx)
		if err != nil || route.Device == "" {
			return ErrFirewallUnavailable
		}
		exists, err := m.firewallTableExists(ctx)
		if err != nil || exists {
			return ErrFirewallUnavailable
		}
		rule, foreign, err := m.policyRuleStatus(ctx)
		if err != nil || rule || foreign {
			return ErrFirewallUnavailable
		}
		routes, err := m.policyRoutes(ctx)
		if err != nil || len(routes) > 0 {
			return ErrFirewallUnavailable
		}
		boot, err := currentBoot()
		if err != nil {
			return err
		}
		now := m.now()
		txn, err = m.startTransaction(ctx, staticWANRecord{FirewallExpiry: &now}, m.firewallPath(), func() error {
			state := firewallState{Boot: boot, Interfaces: map[string]forwardingSnapshot{}}
			if err := writeJSONFile(m.firewallPath(), &state); err != nil {
				return err
			}
			if err := m.installFirewallRules(ctx, *lan); err != nil {
				return err
			}
			return m.syncFirewallRoute(ctx, &state)
		})
		return err
	})
	return txn, err
}

func (m *StaticWANManager) FirewallStatus(ctx context.Context) (FirewallStatus, error) {
	var result FirewallStatus
	err := withFileLock(wanLockPath(m.statePath), func() error {
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		result.Enabled = state != nil
		route, err := m.preferredWAN(ctx)
		if err != nil {
			return err
		}
		result.WAN = route.Device
		entries, err := os.ReadDir(m.statePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !validStaticWANTransactionID(entry.Name()) {
				continue
			}
			_, record, err := m.loadTransaction(entry.Name())
			if err == nil && record.FirewallExpiry != nil {
				result.Pending = &StaticWANTransaction{ID: entry.Name(), ExpiresAt: *record.FirewallExpiry}
				break
			}
		}
		return nil
	})
	return result, err
}

func (m *StaticWANManager) firewallReady(ctx context.Context) bool {
	state, err := m.readFirewall()
	if err != nil || state == nil {
		return false
	}
	route, err := m.preferredWAN(ctx)
	if err != nil || route.Device == "" {
		return false
	}
	routes, err := m.policyRoutes(ctx)
	if err != nil {
		return false
	}
	rule, foreign, err := m.policyRuleStatus(ctx)
	if err != nil || !rule || foreign {
		return false
	}
	for _, name := range []string{LANInterface, route.Device} {
		if value, err := readInterfaceSetting(name, "forwarding"); err != nil || value != "1" {
			return false
		}
	}
	for _, candidate := range routes {
		if candidate.Device == route.Device && candidate.Gateway == route.Gateway && candidate.Metric == wanRouteMetric {
			exists, err := m.firewallTableExists(ctx)
			return err == nil && exists
		}
	}
	return false
}

func (m *StaticWANManager) DisableFirewall(ctx context.Context) error {
	return withFileLock(wanLockPath(m.statePath), func() error {
		pending, err := m.hasPendingTransaction()
		if err != nil {
			return err
		}
		if pending {
			return ErrStaticWANPending
		}
		return m.restoreFirewall(ctx)
	})
}

func (m *StaticWANManager) restoreFirewall(ctx context.Context) error {
	state, err := m.readFirewall()
	if err != nil || state == nil {
		return err
	}
	boot, err := currentBoot()
	if err != nil {
		return err
	}
	var settings []string
	if boot == state.Boot {
		for name, old := range state.Interfaces {
			if name != LANInterface && !isFirewallWAN(name) {
				return errors.New("unexpected interface in firewall snapshot")
			}
			if (old.Forwarding != "0" && old.Forwarding != "1") || (old.RPFilter != "0" && old.RPFilter != "1" && old.RPFilter != "2") {
				return errors.New("invalid saved forwarding settings")
			}
			link, err := net.InterfaceByName(name)
			if err == nil && link.Index == old.Index {
				settings = append(settings, "net.ipv4.conf."+name+".forwarding="+old.Forwarding, "net.ipv4.conf."+name+".rp_filter="+old.RPFilter)
			}
		}
	}
	if len(settings) > 0 {
		if err := m.lanNetAdmin(ctx, append([]string{"/usr/sbin/sysctl", "-q", "-w"}, settings...)...); err != nil {
			return err
		}
	}
	rule, _, err := m.policyRuleStatus(ctx)
	if err != nil {
		return err
	}
	if rule {
		if err := m.lanNetAdmin(ctx, "/usr/sbin/ip", "-4", "rule", "del", "priority", strconv.Itoa(wanPolicyPriority),
			"iif", LANInterface, "lookup", wanRouteTable); err != nil {
			return err
		}
	}
	routes, err := m.policyRoutes(ctx)
	if err != nil {
		return err
	}
	if len(routes) > 0 {
		if err := m.lanNetAdmin(ctx, "/usr/sbin/ip", "-4", "route", "flush", "table", wanRouteTable); err != nil {
			return err
		}
	}
	if err := m.lanNetAdmin(ctx, "/usr/sbin/nft", "destroy", "table", "inet", firewallTable); err != nil {
		return err
	}
	if err := removeRegularFile(filepath.Join(filepath.Dir(m.statePath), "firewall.nft")); err != nil {
		return err
	}
	return removeRegularFile(m.firewallPath())
}

// MaintainFirewall tracks WAN DHCP, static and point-to-point route changes.
func (m *StaticWANManager) MaintainFirewall(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		task, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := withFileLock(wanLockPath(m.statePath), func() error {
			state, err := m.readFirewall()
			if err != nil || state == nil {
				return err
			}
			exists, err := m.firewallTableExists(task)
			if err != nil {
				return err
			}
			lan, err := ManagedLAN(m.lanPath())
			if err != nil {
				return err
			}
			if lan == nil {
				return ErrFirewallUnavailable
			}
			content, err := m.firewallContent(task, *lan)
			if err != nil {
				return err
			}
			applied, _, _, err := readStaticWANFile(m.appliedFirewallPath())
			if err != nil {
				return err
			}
			if !exists || !bytes.Equal(content, applied) {
				if err := m.applyFirewallContent(task, content); err != nil {
					return err
				}
			}
			boot, err := currentBoot()
			if err != nil {
				return err
			}
			if state.Boot != boot {
				state.Boot = boot
				state.Interfaces = map[string]forwardingSnapshot{}
				if err := writeJSONFile(m.firewallPath(), state); err != nil {
					return err
				}
			}
			return m.syncFirewallRoute(task, state)
		})
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("refresh firewall routing: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
