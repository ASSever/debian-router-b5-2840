package main

import (
	"net/netip"
	"testing"
)

func TestFailoverHysteresis(t *testing.T) {
	state := decision{}
	steps := []struct {
		primary, backup, wantSwitch bool
	}{
		{false, true, false},
		{false, false, false},
		{false, true, false},
		{false, true, true},
		{false, false, false},
		{true, true, false},
		{false, true, false},
		{true, true, false},
		{true, false, false},
		{true, false, true},
	}
	for i, step := range steps {
		if got := state.transition(step.primary, step.backup); got != step.wantSwitch {
			t.Fatalf("step %d: switch=%t, want %t", i, got, step.wantSwitch)
		} else if got {
			state = decision{proxy: !state.proxy}
		}
	}
}

func TestRejectedRulesCanBeRetried(t *testing.T) {
	state := decision{}
	state.transition(false, true)
	for i := 0; i < 4; i++ {
		if !state.transition(false, true) || state.proxy {
			t.Fatal("failed application should keep the old mode and retry")
		}
	}
}

func TestFailoverConfigGuardsManagementTraffic(t *testing.T) {
	base := config{
		LAN: "eth1", WAN: "eth0", LANPrefix: netip.MustParsePrefix("192.168.10.0/24"),
		Relay: netip.MustParseAddr("192.168.10.1"), Target: netip.MustParseAddr("198.18.3.2"),
		URL: "https://canary.example.org/ready", Seconds: 5,
	}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*config){
		"same interface": func(c *config) { c.WAN = c.LAN },
		"nft syntax":     func(c *config) { c.LAN = "enp1s0\";drop" },
		"same subnet":    func(c *config) { c.Target = c.Relay },
		"loopback":       func(c *config) { c.Target = netip.MustParseAddr("127.0.0.1") },
		"credentials":    func(c *config) { c.URL = "https://secret@canary.example.org/ready" },
		"different port": func(c *config) { c.URL = "https://canary.example.org:8443/ready" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			edit(&c)
			if err := c.validate(); err == nil {
				t.Fatal("unexpected valid configuration")
			}
		})
	}
}
