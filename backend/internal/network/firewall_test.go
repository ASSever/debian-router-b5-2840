package network

import (
	"context"
	"strings"
	"testing"
)

func TestFirewallSelectsUsableWANInsteadOfWiFiDefault(t *testing.T) {
	routes := []firewallRoute{
		{Destination: "default", Device: "wlp3s0", Gateway: "192.168.1.1", Metric: 3004},
		{Destination: "default", Device: "enp1s0", Gateway: "192.168.1.1", Metric: 5000},
		{Destination: "default", Device: "ppp0", Flags: []string{"linkdown"}},
	}
	if got := selectWANRoute(routes); got.Device != "enp1s0" {
		t.Fatalf("selected %#v", got)
	}
	routes[2].Flags = nil
	if got := selectWANRoute(routes); got.Device != "ppp0" {
		t.Fatalf("selected %#v", got)
	}
}

func TestFirewallChecksRuleSelectorAndTable(t *testing.T) {
	manager := &StaticWANManager{run: func(context.Context, string, ...string) (string, error) {
		return `[{"priority":24224,"src":"all","iif":"enp2s0","table":"24224"},{"priority":24224,"src":"all","iif":"wlp3s0","table":"main"}]`, nil
	}}
	own, foreign, err := manager.policyRuleStatus(context.Background())
	if err != nil || !own || !foreign {
		t.Fatalf("rule state %t %t %v", own, foreign, err)
	}
}

func TestFirewallScopesNATAndWANInput(t *testing.T) {
	rules := string(renderFirewall(defaultLAN, FirewallRules{}, firewallIngress{}))
	for _, want := range []string{
		"destroy table inet debian_router_edge",
		`iifname "enp2s0" oifname @wan_devices ip saddr 192.168.10.0/24 counter masquerade`,
		"iifname @wan_devices tcp dport { 22, 80, 443, 8080, 8081, 8443 } counter drop",
		"ct state established,related counter accept",
		"udp sport 67 udp dport 68 accept",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(rules, "flush ruleset") || strings.Contains(rules, "wlp3s0") {
		t.Fatal("unexpected global or Wi-Fi rule")
	}
}

func TestFirewallValidatesForwardDestinationsAndSources(t *testing.T) {
	forward := PortForward{Protocol: "tcp", WANPort: 18025, LANAddress: "192.168.10.177", LANPort: 18250}
	normalized, err := normalizeFirewallRules(defaultLAN, FirewallRules{
		Forwards: []PortForward{forward},
		Allow:    []string{"192.0.2.4", "192.0.2.29/24"},
	})
	if err != nil || strings.Join(normalized.Allow, ",") != "192.0.2.4/32,192.0.2.0/24" {
		t.Fatalf("normalizeFirewallRules() = %+v, %v", normalized, err)
	}
	invalid := []FirewallRules{
		{Forwards: []PortForward{forward, forward}},
		{Forwards: []PortForward{{Protocol: "udp", WANPort: 18025, LANAddress: "192.168.10.1", LANPort: 18250}}},
		{Forwards: []PortForward{{Protocol: "udp", WANPort: 18025, LANAddress: "192.168.1.77", LANPort: 18250}}},
		{Allow: []string{"192.0.2.4", "192.0.2.4/32"}},
		{Block: []string{"2001:db8::/32"}},
	}
	for _, rules := range invalid {
		if _, err := normalizeFirewallRules(defaultLAN, rules); err != ErrInvalidFirewallRules {
			t.Errorf("accepted invalid rules %+v: %v", rules, err)
		}
	}
}

func TestFirewallForwardVerdictsPrecedeGenericWANAcceptance(t *testing.T) {
	rules := FirewallRules{
		Forwards: []PortForward{{Protocol: "udp", WANPort: 18026, LANAddress: "192.168.10.177", LANPort: 18251}},
		Allow:    []string{"192.0.2.0/24"},
		Block:    []string{"192.0.2.9/32"},
	}
	content := string(renderFirewall(defaultLAN, rules, firewallIngress{Device: "enp1s0", IPv4: []string{"192.168.1.104"}}))
	offset := -1
	for _, rule := range []string{
		"elements = { 192.168.1.104 }",
		"ip saddr @forward_block counter name source_blocked drop",
		"ip saddr != @forward_allow counter name source_not_allowed drop",
		"ip saddr @forward_allow counter name source_allowed",
		`iifname "enp1s0" ip daddr @forward_wan_ipv4 udp dport 18026 dnat ip to 192.168.10.177:18251`,
		"ct original proto-dst 18026 counter name forward_udp_18026 accept",
		"ct status dnat counter drop",
		"ct state established,related counter accept",
	} {
		position := strings.Index(content[offset+1:], rule)
		if position < 0 {
			t.Fatalf("missing or out-of-order rule %q", rule)
		}
		offset += position + 1
	}
	offline := string(renderFirewall(defaultLAN, rules, firewallIngress{}))
	if strings.Contains(offline, "dnat ip to") || !strings.Contains(offline, "ct status dnat counter drop") {
		t.Fatal("WAN disappearance did not disable DNAT")
	}
}
