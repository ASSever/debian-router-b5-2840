package network

import (
	"strings"
	"testing"
)

func TestLANDNSValidationAndRendering(t *testing.T) {
	config, err := normalizeLANDNS(LANDNSConfig{
		Primary: "192.0.2.53", Backup: "2001:db8::53", RouterOnly: true,
		Records: []DNSRecord{{Name: "Nas.Home.Arpa.", Address: "192.168.10.20"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(appendLANDNS(nil, config))
	for _, want := range []string{"no-resolv\nstrict-order\n", "server=192.0.2.53\nserver=2001:db8::53\n", "host-record=nas.home.arpa,192.168.10.20\n"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in %q", want, rendered)
		}
	}
	for _, invalid := range []LANDNSConfig{
		{Backup: "192.0.2.53"}, {Primary: "127.0.0.1"},
		{Primary: "192.0.2.53", Backup: "192.0.2.53"},
		{Records: []DNSRecord{{Name: LANHostname, Address: "192.0.2.2"}}},
		{Records: []DNSRecord{{Name: "test.home.arpa\nserver=192.0.2.1", Address: "192.0.2.2"}}},
	} {
		if _, err := normalizeLANDNS(invalid); err == nil {
			t.Errorf("accepted %+v", invalid)
		}
	}
}

func TestDNSFirewallOnlyBlocksRoutedLANPort53(t *testing.T) {
	locked := string(renderLANFirewall(defaultLAN, true))
	open := string(renderLANFirewall(defaultLAN, false))
	if !strings.Contains(locked, `iifname "enp2s0" meta l4proto { tcp, udp } th dport 53 counter drop`) ||
		strings.Contains(open, "hook forward") {
		t.Fatal("unexpected forward policy")
	}
}
