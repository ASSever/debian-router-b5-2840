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
	rules := string(renderFirewall(defaultLAN))
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
