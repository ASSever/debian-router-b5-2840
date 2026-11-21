package network

import (
	"context"
	"strings"
	"testing"
)

func TestValidateWANLine(t *testing.T) {
	for _, config := range []WANLineConfig{
		{},
		{VLANID: 1, Priority: 7, MTU: 1280, MAC: "02:00:00:00:00:01"},
		{VLANID: 4094, MTU: 1500, MAC: "FC:06:8C:13:F1:DE"},
	} {
		if err := ValidateWANLine(config); err != nil {
			t.Errorf("valid config %+v: %v", config, err)
		}
	}
	for _, config := range []WANLineConfig{
		{VLANID: -1}, {VLANID: 4095}, {Priority: 1}, {VLANID: 1, Priority: 8},
		{MTU: 1279}, {MTU: 1501}, {MAC: "00:00:00:00:00:00"},
		{MAC: "01:00:00:00:00:01"}, {MAC: "02:00:00:00:00:00:00:01"},
		{MAC: "02:00:00:00:00:01\nDHCP=no"},
	} {
		if err := ValidateWANLine(config); err == nil {
			t.Errorf("accepted invalid config %+v", config)
		}
	}
}

func TestWANLineRendersDHCPOnTaggedLink(t *testing.T) {
	profile := wanLineProfile{
		Config:   WANLineConfig{VLANID: 20, Priority: 5, MTU: 1400, MAC: "02:00:00:00:00:20"},
		Baseline: wanLinkSettings{MAC: "02:00:00:00:00:01", MTU: 1500},
	}
	parent, child, netdev := renderWANLine(profile)
	for _, check := range []struct {
		content []byte
		line    string
	}{
		{parent, "DHCP=no"}, {parent, "VLAN=router-wan-vlan"}, {parent, "MTUBytes=1500"},
		{child, "DHCP=ipv4"}, {child, "UseMTU=no"}, {child, "MTUBytes=1400"}, {child, "ClientIdentifier=mac"},
		{netdev, "Id=20"}, {netdev, "EgressQOSMaps=0-5"}, {netdev, "MACAddress=02:00:00:00:00:20"},
	} {
		if !strings.Contains(string(check.content), "\n"+check.line+"\n") {
			t.Errorf("missing %q in %s", check.line, check.content)
		}
	}
	if decoded, err := decodeWANLine(parent); err != nil || decoded != profile {
		t.Fatalf("profile round trip = %+v, %v", decoded, err)
	}
}

func TestWANLineRequiresRequestedKernelVLAN(t *testing.T) {
	manager := &StaticWANManager{run: func(context.Context, string, ...string) (string, error) {
		return `[{"link":"enp1s0","linkinfo":{"info_kind":"vlan","info_data":{"id":20,"egress_qos":[{"from":0,"to":5}]}}}]`, nil
	}}
	ctx := context.Background()
	if !manager.vlanMatches(ctx, WANLineConfig{VLANID: 20, Priority: 5}) {
		t.Fatal("rejected requested VLAN and PCP")
	}
	if manager.vlanMatches(ctx, WANLineConfig{VLANID: 20, Priority: 0}) ||
		manager.vlanMatches(ctx, WANLineConfig{VLANID: 21, Priority: 5}) {
		t.Fatal("accepted mismatched VLAN or PCP")
	}
}
