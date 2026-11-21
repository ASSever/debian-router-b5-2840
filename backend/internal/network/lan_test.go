package network

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestValidateLAN(t *testing.T) {
	valid := []LANConfig{
		defaultLAN,
		{"172.19.16.33/27", "172.19.16.34", "172.19.16.62"},
		{"10.20.30.1/30", "10.20.30.2", "10.20.30.2"},
	}
	for _, config := range valid {
		if err := ValidateLAN(config); err != nil {
			t.Errorf("valid config %+v: %v", config, err)
		}
	}
	invalid := []LANConfig{
		{"192.168.10.0/24", "192.168.10.2", "192.168.10.3"},
		{"192.168.10.1/24", "192.168.10.2", "192.168.10.255"},
		{"192.168.10.1/24", "192.168.10.4", "192.168.9.20"},
		{"192.168.10.1/24", "192.168.10.9", "192.168.10.4"},
		{"192.168.10.33/24", "192.168.10.2", "192.168.10.40"},
		{"2001:db8::1/64", "192.168.10.2", "192.168.10.3"},
	}
	for _, config := range invalid {
		if err := ValidateLAN(config); err == nil {
			t.Errorf("accepted invalid config %+v", config)
		}
	}
}

func TestLANReportsBothOverlapsAndChecksSuggestion(t *testing.T) {
	config := LANConfig{"192.168.1.33/27", "192.168.1.34", "192.168.1.61"}
	upstream := []upstreamPrefix{
		{"enp1s0", netip.MustParsePrefix("192.168.1.0/24")},
		{"wlp3s0", netip.MustParsePrefix("192.168.1.0/24")},
		{"wlp3s0", netip.MustParsePrefix("192.168.10.0/25")},
	}
	conflict := overlappingLAN(config, upstream)
	if conflict == nil || !slices.Equal(conflict.Interfaces, []string{"enp1s0", "wlp3s0"}) {
		t.Fatalf("overlap = %+v", conflict)
	}
	if conflict.Suggestion == nil || ValidateLAN(*conflict.Suggestion) != nil ||
		overlappingLAN(*conflict.Suggestion, upstream) != nil {
		t.Fatalf("invalid suggestion = %+v", conflict.Suggestion)
	}
}

func TestLANRenderChangesFirewallDestinationAndSource(t *testing.T) {
	config := LANConfig{"172.19.16.33/27", "172.19.16.34", "172.19.16.62"}
	data := renderLANNetwork(config)
	got, err := parseLANNetwork(data)
	if err != nil || got == nil || *got != config {
		t.Fatalf("network round trip = %+v, %v", got, err)
	}
	rules := string(renderLANFirewall(config, true))
	for _, want := range []string{"ip daddr 172.19.16.33", `iifname "enp2s0"`, "ip saddr 172.19.16.32/27", "counter drop"} {
		if !strings.Contains(rules, want) {
			t.Errorf("missing %q in %s", want, rules)
		}
	}
}
