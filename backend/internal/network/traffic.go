package network

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

const trafficTable = "router_traffic"

type TrafficCount struct {
	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`
}

type InterfaceTraffic struct {
	Name  string       `json:"name"`
	Index int          `json:"index"`
	RX    TrafficCount `json:"rx"`
	TX    TrafficCount `json:"tx"`
}

type ClientTraffic struct {
	Address  netip.Addr   `json:"address"`
	Upload   TrafficCount `json:"upload"`
	Download TrafficCount `json:"download"`
}

type TrafficSnapshot struct {
	SampledAt    time.Time          `json:"sampled_at"`
	ClientsSince time.Time          `json:"clients_since"`
	Prefixes     []netip.Prefix     `json:"lan_prefixes"`
	Interfaces   []InterfaceTraffic `json:"interfaces"`
	Clients      []ClientTraffic    `json:"clients"`
}

type trafficState struct {
	mu    sync.Mutex
	scope []netip.Prefix
	index int
	since time.Time
}

func lanTrafficScope() (int, []netip.Prefix, error) {
	links, err := net.Interfaces()
	if err != nil {
		return 0, nil, err
	}
	for _, link := range links {
		if link.Name != LANInterface {
			continue
		}
		addresses, err := link.Addrs()
		if err != nil {
			return 0, nil, err
		}
		var scope []netip.Prefix
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				return 0, nil, err
			}
			if prefix.Addr().IsGlobalUnicast() {
				scope = append(scope, prefix)
			}
		}
		slices.SortFunc(scope, func(a, b netip.Prefix) int {
			if order := a.Addr().Compare(b.Addr()); order != 0 {
				return order
			}
			return cmp.Compare(a.Bits(), b.Bits())
		})
		return link.Index, slices.Compact(scope), nil
	}
	return 0, nil, nil
}

type trafficDirection struct {
	name, hook, selector, field string
	priority                    int
}

var trafficDirections = []trafficDirection{
	{"upload", "prerouting", "iifname", "saddr", -150},
	{"download", "postrouting", "oifname", "daddr", 110},
}

func renderTraffic(scope []netip.Prefix) string {
	var rules strings.Builder
	fmt.Fprintf(&rules, "destroy table inet %[1]s\ntable inet %[1]s {\n", trafficTable)
	for _, direction := range trafficDirections {
		for _, version := range []int{4, 6} {
			fmt.Fprintf(&rules, " set %s%d { type ipv%d_addr; flags dynamic,timeout; timeout 1h; size 512; }\n",
				direction.name, version, version)
		}
		fmt.Fprintf(&rules, " chain %s {\n  type filter hook %s priority %d; policy accept;\n",
			direction.name, direction.hook, direction.priority)
		// Count each address once even when multiple configured prefixes overlap.
		for _, local := range scope {
			proto := "ip6"
			if local.Addr().Is4() {
				proto = "ip"
			}
			fmt.Fprintf(&rules, "  %s %q %s %s %s return\n", direction.selector, LANInterface, proto, direction.field, local.Addr())
		}
		for _, proto := range []string{"ip", "ip6"} {
			var prefixes []string
			for _, local := range scope {
				if local.Addr().Is4() == (proto == "ip") {
					prefixes = append(prefixes, local.Masked().String())
				}
			}
			if len(prefixes) == 0 {
				continue
			}
			version := 6
			if proto == "ip" {
				version = 4
				fmt.Fprintf(&rules, "  %s %q fib %s type broadcast return\n",
					direction.selector, LANInterface, direction.field)
			}
			fmt.Fprintf(&rules, "  %s %q %s %s { %s } update @%s%d { %s %s counter }\n",
				direction.selector, LANInterface, proto, direction.field, strings.Join(prefixes, ", "), direction.name, version, proto, direction.field)
		}
		rules.WriteString(" }\n")
	}
	rules.WriteString("}\n")
	return rules.String()
}

// trafficNFT grants only the one-shot nft command access to CAP_NET_ADMIN.
func (m *StaticWANManager) trafficNFT(ctx context.Context, args ...string) (string, error) {
	namespace := fmt.Sprintf("--property=NetworkNamespacePath=/proc/%d/ns/net", os.Getpid())
	return m.runNetAdminWithProperties(ctx, []string{namespace}, append([]string{"/usr/sbin/nft"}, args...)...)
}

// syncTraffic runs under the traffic lock and replaces only the accounting table.
func (m *StaticWANManager) syncTraffic(ctx context.Context) error {
	index, scope, err := lanTrafficScope()
	if err != nil {
		return err
	}
	if !m.traffic.since.IsZero() && index == m.traffic.index && slices.Equal(scope, m.traffic.scope) {
		return nil
	}
	return m.installTraffic(ctx, index, scope)
}

func (m *StaticWANManager) installTraffic(ctx context.Context, index int, scope []netip.Prefix) error {
	if _, err := m.trafficNFT(ctx, renderTraffic(scope)); err != nil {
		return err
	}
	m.traffic.scope, m.traffic.index, m.traffic.since = scope, index, m.now().UTC()
	return nil
}

func (m *StaticWANManager) MaintainTraffic(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer func() {
		m.traffic.mu.Lock()
		defer m.traffic.mu.Unlock()
		task, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := m.trafficNFT(task, "destroy", "table", "inet", trafficTable); err != nil {
			log.Printf("remove traffic accounting: %v", err)
		}
	}()
	for {
		task, cancel := context.WithTimeout(ctx, 4*time.Second)
		m.traffic.mu.Lock()
		err := m.syncTraffic(task)
		m.traffic.mu.Unlock()
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("initialize traffic accounting: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *StaticWANManager) Traffic(ctx context.Context) (TrafficSnapshot, error) {
	m.traffic.mu.Lock()
	defer m.traffic.mu.Unlock()
	if err := m.syncTraffic(ctx); err != nil {
		return TrafficSnapshot{}, err
	}
	output, err := m.run(ctx, "/usr/sbin/ip", "-j", "-s", "link", "show")
	if err != nil {
		return TrafficSnapshot{}, err
	}
	interfaces, err := parseInterfaceTraffic(output)
	if err != nil {
		return TrafficSnapshot{}, err
	}
	output, err = m.trafficNFT(ctx, "-j", "list", "table", "inet", trafficTable)
	if err != nil {
		// Reconcile on the next tick if an external ruleset reload removed the table.
		m.traffic.since = time.Time{}
		return TrafficSnapshot{}, err
	}
	clients, err := parseClientTraffic(output)
	if err != nil {
		return TrafficSnapshot{}, err
	}
	prefixes := make([]netip.Prefix, 0, len(m.traffic.scope))
	for _, local := range m.traffic.scope {
		if prefix := local.Masked(); !slices.Contains(prefixes, prefix) {
			prefixes = append(prefixes, prefix)
		}
	}
	return TrafficSnapshot{SampledAt: m.now().UTC(), ClientsSince: m.traffic.since,
		Prefixes: prefixes, Interfaces: interfaces, Clients: clients}, nil
}

func parseInterfaceTraffic(output string) ([]InterfaceTraffic, error) {
	var links []struct {
		Name  string `json:"ifname"`
		Index int    `json:"ifindex"`
		Stats *struct {
			RX TrafficCount `json:"rx"`
			TX TrafficCount `json:"tx"`
		} `json:"stats64"`
	}
	if err := json.Unmarshal([]byte(output), &links); err != nil {
		return nil, err
	}
	result := make([]InterfaceTraffic, 0, len(links))
	for _, link := range links {
		if link.Stats == nil {
			return nil, fmt.Errorf("missing kernel statistics for %s", link.Name)
		}
		result = append(result, InterfaceTraffic{Name: link.Name, Index: link.Index, RX: link.Stats.RX, TX: link.Stats.TX})
	}
	return result, nil
}

func parseClientTraffic(output string) ([]ClientTraffic, error) {
	var table struct {
		Entries []struct {
			Set *struct {
				Name     string `json:"name"`
				Elements []struct {
					Element struct {
						Address netip.Addr    `json:"val"`
						Counter *TrafficCount `json:"counter"`
					} `json:"elem"`
				} `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(output), &table); err != nil {
		return nil, err
	}
	clients := map[netip.Addr]ClientTraffic{}
	expected := map[string]bool{"upload4": true, "upload6": true, "download4": true, "download6": true}
	for _, entry := range table.Entries {
		if entry.Set == nil || !expected[entry.Set.Name] {
			continue
		}
		delete(expected, entry.Set.Name)
		for _, row := range entry.Set.Elements {
			element := row.Element
			if !element.Address.IsValid() || element.Counter == nil {
				return nil, fmt.Errorf("missing client address or counter in %s", entry.Set.Name)
			}
			client := clients[element.Address]
			client.Address = element.Address
			if strings.HasPrefix(entry.Set.Name, "upload") {
				client.Upload = *element.Counter
			} else {
				client.Download = *element.Counter
			}
			clients[element.Address] = client
		}
	}
	if len(expected) != 0 {
		return nil, fmt.Errorf("missing client accounting sets")
	}
	result := make([]ClientTraffic, 0, len(clients))
	for _, client := range clients {
		result = append(result, client)
	}
	slices.SortFunc(result, func(a, b ClientTraffic) int { return a.Address.Compare(b.Address) })
	return result, nil
}
