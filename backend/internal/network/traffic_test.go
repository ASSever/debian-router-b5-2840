package network

import "testing"

func TestParseInterfaceTraffic(t *testing.T) {
	links, err := parseInterfaceTraffic(`[{"ifname":"enp2s0","ifindex":2,"stats64":{"rx":{"bytes":4294967300,"packets":42},"tx":{"bytes":0,"packets":0}}}]`)
	if err != nil || len(links) != 1 || links[0].RX.Bytes != 4294967300 || links[0].TX.Bytes != 0 {
		t.Fatalf("links = %+v, error = %v", links, err)
	}
	if _, err := parseInterfaceTraffic(`[{"ifname":"enp2s0","ifindex":2}]`); err == nil {
		t.Fatal("missing stats accepted")
	}
}

func TestParseClientTraffic(t *testing.T) {
	clients, err := parseClientTraffic(`{"nftables":[
		{"set":{"name":"upload4","elem":[{"elem":{"val":"192.168.10.2","expires":3599,"counter":{"packets":4,"bytes":400}}}]}},
		{"set":{"name":"download4","elem":[{"elem":{"val":"192.168.10.2","counter":{"packets":3,"bytes":300}}}]}},
		{"set":{"name":"upload6"}},
		{"set":{"name":"download6","elem":[{"elem":{"val":"fd00::2","counter":{"packets":1,"bytes":100}}}]}}
	]}`)
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients = %+v, error = %v", clients, err)
	}
	if clients[0].Address.String() != "192.168.10.2" || clients[0].Upload.Bytes != 400 || clients[0].Download.Bytes != 300 ||
		clients[1].Address.String() != "fd00::2" || clients[1].Upload != (TrafficCount{}) || clients[1].Download.Bytes != 100 {
		t.Fatalf("unexpected clients: %+v", clients)
	}
	if _, err := parseClientTraffic(`{"nftables":[]}`); err == nil {
		t.Fatal("missing accounting sets accepted")
	}
}
