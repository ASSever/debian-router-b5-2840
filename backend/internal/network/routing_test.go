package network

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestRoutingValidatesFamiliesAndConnectedPrefixes(t *testing.T) {
	ipv6 := &RoutedIPv6{"fd26:1::2/64", "fe80::1", "fd26:2::/64"}
	config, err := normalizeRouting(defaultLAN, RoutingConfig{
		IPv6: ipv6,
		Routes: []StaticRoute{
			{"198.51.100.8/24", "192.0.2.1"},
			{"fd26:4::8/64", "fe80::1"},
		},
	})
	if err != nil || config.IPv6.lanAddress().String() != "fd26:2::1/64" ||
		config.Routes[0].Destination != "198.51.100.0/24" || config.Routes[1].Destination != "fd26:4::/64" {
		t.Fatalf("normalized routing = %+v, %v", config, err)
	}
	for _, test := range []struct {
		config RoutingConfig
		want   error
	}{
		{RoutingConfig{Routes: []StaticRoute{{"0.0.0.0/0", "192.0.2.1"}}}, ErrInvalidRouting},
		{RoutingConfig{Routes: []StaticRoute{{"198.51.100.0/24", "fe80::1"}}}, ErrInvalidRouting},
		{RoutingConfig{Routes: []StaticRoute{{"fd26:4::/64", "fe80::1"}}}, ErrInvalidRouting},
		{RoutingConfig{Routes: []StaticRoute{{"192.168.10.8/32", "192.0.2.1"}}}, ErrRoutingOverlap},
		{RoutingConfig{IPv6: &RoutedIPv6{"fd26:1::2/64", "fe80::1", "fd26:1::/64"}}, ErrRoutingOverlap},
		{RoutingConfig{IPv6: ipv6, Routes: []StaticRoute{{"fd26:2::/64", "fe80::1"}}}, ErrRoutingOverlap},
	} {
		if _, err := normalizeRouting(defaultLAN, test.config); !errors.Is(err, test.want) {
			t.Errorf("normalizeRouting(%+v) = %v, want %v", test.config, err, test.want)
		}
	}
}

func TestRoutingIPv4GatewaysStayOnTheWANLink(t *testing.T) {
	upstream := []netip.Prefix{netip.MustParsePrefix("192.0.2.11/24")}
	for _, test := range []struct {
		route StaticRoute
		want  bool
	}{
		{StaticRoute{"198.51.100.0/24", "192.0.2.1"}, true},
		{StaticRoute{"198.51.100.0/24", "192.0.2.11"}, false},
		{StaticRoute{"198.51.100.0/24", "203.0.113.1"}, false},
		{StaticRoute{"192.0.2.0/25", "192.0.2.1"}, false},
	} {
		if got := usableWANRoute(test.route, upstream); got != test.want {
			t.Errorf("usableWANRoute(%+v) = %t, want %t", test.route, got, test.want)
		}
	}
}

func TestRoutedIPv6FirewallRestrictsTransitAndLANManagement(t *testing.T) {
	ipv6 := &RoutedIPv6{"fd26:1::2/64", "fe80::1", "fd26:2::/64"}
	rules := string(renderFirewall(defaultLAN, FirewallRules{}, firewallIngress{}, ipv6))
	order := []string{
		`ip6 daddr fd26:2::/64 iifname != { "enp2s0", "lo" } counter drop`,
		`iifname "enp2s0" oifname "enp1s0" ip6 saddr fd26:2::/64 ct state new,established,related counter accept`,
		`iifname "enp1s0" oifname "enp2s0" ip6 daddr fd26:2::/64 ct state established,related counter accept`,
		"meta nfproto ipv6 counter drop",
	}
	for _, rule := range order {
		_, rest, found := strings.Cut(rules, rule)
		if !found {
			t.Fatalf("missing or out-of-order rule %q", rule)
		}
		rules = rest
	}
	if strings.Contains(rules, "ip6 masquerade") {
		t.Fatal("IPv6 firewall must preserve client addresses")
	}
}
