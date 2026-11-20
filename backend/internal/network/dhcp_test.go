package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadDHCPStatusWithFiltersStaticDataAndClientIdentifier(t *testing.T) {
	payload := []byte("{\"CarrierState\":\"carrier\",\"Addresses\":[{\"Family\":2,\"Address\":[192,0,2,10],\"PrefixLength\":24,\"ConfigSource\":\"DHCPv4\"},{\"Family\":2,\"Address\":[10,0,0,1],\"PrefixLength\":24,\"ConfigSource\":\"static\"}],\"Routes\":[{\"Family\":2,\"DestinationPrefixLength\":0,\"Gateway\":[192,0,2,1],\"ConfigSource\":\"DHCPv4\"}],\"DNS\":[{\"Family\":2,\"Address\":[192,0,2,53],\"ConfigSource\":\"DHCPv4\"},{\"Family\":2,\"Address\":[10,0,0,53],\"ConfigSource\":\"static\"}],\"DHCPv4Client\":{\"Lease\":{\"LeaseTimestampUSec\":123},\"ClientIdentifier\":[1,2,3]}}")
	got, err := ReadDHCPStatusWith(context.Background(), "enp1s0", func(_ context.Context, name string) ([]byte, error) {
		if name != "enp1s0" {
			t.Fatalf("interface = %q, want enp1s0", name)
		}
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.LeaseState != "leased" || got.Gateway != "192.0.2.1" {
		t.Fatalf("status = %#v, want DHCP lease and gateway", got)
	}
	if len(got.Addresses) != 1 || got.Addresses[0] != "192.0.2.10/24" {
		t.Fatalf("addresses = %#v, want only DHCP address", got.Addresses)
	}
	if len(got.DNS) != 1 || got.DNS[0] != "192.0.2.53" {
		t.Fatalf("DNS = %#v, want only DHCP DNS", got.DNS)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ClientIdentifier") {
		t.Fatal("DHCP client identifier leaked into API result")
	}
}

func TestReadDHCPStatusWithReportsLinkDownAndWaiting(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantState string
	}{
		{name: "link down", payload: "{\"CarrierState\":\"no-carrier\"}", wantState: "link_down"},
		{name: "waiting", payload: "{\"CarrierState\":\"carrier\"}", wantState: "waiting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ReadDHCPStatusWith(context.Background(), "enp1s0", func(context.Context, string) ([]byte, error) {
				return []byte(test.payload), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.LeaseState != test.wantState {
				t.Fatalf("lease state = %q, want %q", got.LeaseState, test.wantState)
			}
		})
	}
}

func TestReadDHCPStatusWithRejectsUnsafeInterfaceName(t *testing.T) {
	called := false
	_, err := ReadDHCPStatusWith(context.Background(), "../enp1s0", func(context.Context, string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err == nil || called {
		t.Fatal("invalid interface name was not rejected before querying networkctl")
	}
}
