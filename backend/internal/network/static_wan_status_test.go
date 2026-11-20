package network

import (
	"context"
	"reflect"
	"testing"
)

func TestReadActiveNetworkStatusIncludesStaticSettings(t *testing.T) {
	payload := []byte(`{"CarrierState":"carrier","Addresses":[{"Family":2,"Address":[192,168,1,104],"PrefixLength":24,"ConfigSource":"static"},{"Family":10,"Address":[0,0,0,0],"PrefixLength":64,"ConfigSource":"static"}],"Routes":[{"Family":2,"DestinationPrefixLength":0,"Gateway":[192,168,1,1],"ConfigSource":"static"}],"DNS":[{"Family":2,"Address":[1,1,1,1],"ConfigSource":"static"},{"Family":2,"Address":[192,168,1,1],"ConfigSource":"static"}]}`)
	got, err := ReadActiveNetworkStatusWith(context.Background(), "enp1s0", func(_ context.Context, name string) ([]byte, error) {
		if name != "enp1s0" {
			t.Fatalf("interface = %q, want enp1s0", name)
		}
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ActiveNetworkStatus{
		LinkState: "carrier",
		Addresses: []string{"192.168.1.104/24"},
		Gateway:   "192.168.1.1",
		DNS:       []string{"1.1.1.1", "192.168.1.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active network status = %#v, want %#v", got, want)
	}
}

func TestReadActiveNetworkStatusPrefersStaticDefaultRoute(t *testing.T) {
	payload := []byte(`{"CarrierState":"carrier","Routes":[{"Family":2,"DestinationPrefixLength":0,"Gateway":[192,168,1,1],"ConfigSource":"DHCPv4"},{"Family":2,"DestinationPrefixLength":0,"Gateway":[192,168,1,254],"ConfigSource":"static"}]}`)
	got, err := ReadActiveNetworkStatusWith(context.Background(), "enp1s0", func(context.Context, string) ([]byte, error) {
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway != "192.168.1.254" {
		t.Fatalf("gateway = %q, want static route gateway 192.168.1.254", got.Gateway)
	}
}
