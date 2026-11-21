package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ipBinary          = "/usr/sbin/ip"
	ipv6PolicyTable   = "24226"
	staticRouteMetric = 26
)

var (
	ErrInvalidRouting   = errors.New("invalid_routing")
	ErrRoutingGateway   = errors.New("routing_gateway_unreachable")
	ErrRoutingOverlap   = errors.New("routing_overlap")
	ErrRoutingWAN       = errors.New("routing_requires_wan")
	ErrRoutingResources = errors.New("routing_resources_in_use")
)

type StaticRoute struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

type RoutedIPv6 struct {
	WANAddress string `json:"wan_address"`
	WANGateway string `json:"wan_gateway"`
	LANPrefix  string `json:"lan_prefix"`
}

type RoutingConfig struct {
	Routes []StaticRoute `json:"routes"`
	IPv6   *RoutedIPv6   `json:"ipv6"`
}

type RoutingStatus struct {
	Config     RoutingConfig `json:"config"`
	IPv6Active bool          `json:"ipv6_active"`
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")
var uniqueLocalIPv6 = netip.MustParsePrefix("fc00::/7")

func routableIPv6(ip netip.Addr) bool {
	return globalIPv6.Contains(ip) || uniqueLocalIPv6.Contains(ip)
}

func validIPv6Gateway(ip netip.Addr, wan netip.Prefix) bool {
	return ip.Zone() == "" && ip != wan.Addr() &&
		(ip.IsLinkLocalUnicast() && ip.Is6() || routableIPv6(ip) && wan.Contains(ip))
}

func (config RoutedIPv6) lanAddress() netip.Prefix {
	prefix := netip.MustParsePrefix(config.LANPrefix)
	return netip.PrefixFrom(prefix.Addr().Next(), prefix.Bits())
}

func normalizeRouting(lan LANConfig, requested RoutingConfig) (RoutingConfig, error) {
	if len(requested.Routes) > 24 {
		return RoutingConfig{}, ErrInvalidRouting
	}
	result := RoutingConfig{Routes: make([]StaticRoute, 0, len(requested.Routes))}
	var wan6, lan6 netip.Prefix
	if config := requested.IPv6; config != nil {
		wan, wanErr := netip.ParsePrefix(config.WANAddress)
		downstream, lanErr := netip.ParsePrefix(config.LANPrefix)
		gateway, gatewayErr := netip.ParseAddr(config.WANGateway)
		if wanErr != nil || lanErr != nil || gatewayErr != nil || !routableIPv6(wan.Addr()) ||
			!routableIPv6(downstream.Addr()) || wan.Bits() < 3 || downstream.Bits() != 64 ||
			downstream != downstream.Masked() || !validIPv6Gateway(gateway, wan) {
			return RoutingConfig{}, ErrInvalidRouting
		}
		if wan.Overlaps(downstream) {
			return RoutingConfig{}, ErrRoutingOverlap
		}
		result.IPv6 = &RoutedIPv6{wan.String(), gateway.String(), downstream.String()}
		wan6, lan6 = wan, downstream
	}
	lan4 := netip.MustParsePrefix(lan.Address).Masked()
	seen := map[netip.Prefix]bool{}
	for _, candidate := range requested.Routes {
		destination, destErr := netip.ParsePrefix(candidate.Destination)
		gateway, gatewayErr := netip.ParseAddr(candidate.Gateway)
		if destErr != nil || gatewayErr != nil || destination.Bits() == 0 ||
			destination.Addr().BitLen() != gateway.BitLen() || destination.Addr().Is4In6() ||
			gateway.Is4In6() || gateway.Zone() != "" {
			return RoutingConfig{}, ErrInvalidRouting
		}
		destination = destination.Masked()
		if seen[destination] {
			return RoutingConfig{}, ErrInvalidRouting
		}
		seen[destination] = true
		if gateway.Is4() {
			if !isUsableIPv4(gateway) || !destination.Addr().IsGlobalUnicast() {
				return RoutingConfig{}, ErrInvalidRouting
			}
			if destination.Overlaps(lan4) {
				return RoutingConfig{}, ErrRoutingOverlap
			}
		} else {
			if result.IPv6 == nil || !validIPv6Gateway(gateway, wan6) || !routableIPv6(destination.Addr()) {
				return RoutingConfig{}, ErrInvalidRouting
			}
			if destination.Overlaps(lan6) || destination.Overlaps(wan6) {
				return RoutingConfig{}, ErrRoutingOverlap
			}
		}
		result.Routes = append(result.Routes, StaticRoute{destination.String(), gateway.String()})
	}
	return result, nil
}

func (m *StaticWANManager) routingPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "routing.json")
}

func (m *StaticWANManager) readRouting() (RoutingConfig, error) {
	config := RoutingConfig{Routes: []StaticRoute{}}
	data, exists, _, err := readStaticWANFile(m.routingPath())
	if err != nil || !exists {
		return config, err
	}
	err = json.Unmarshal(data, &config)
	return config, err
}

func (m *StaticWANManager) routingConfig(lan LANConfig) (RoutingConfig, error) {
	config, err := m.readRouting()
	if err != nil {
		return RoutingConfig{}, err
	}
	return normalizeRouting(lan, config)
}

func interfacePrefixes(name string) ([]netip.Prefix, error) {
	device, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addresses, err := device.Addrs()
	if err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes, nil
}

func usableWANRoute(route StaticRoute, prefixes []netip.Prefix) bool {
	destination := netip.MustParsePrefix(route.Destination)
	gateway := netip.MustParseAddr(route.Gateway)
	connected := false
	for _, prefix := range prefixes {
		if !prefix.Addr().Is4() {
			continue
		}
		subnet := prefix.Masked()
		if destination.Overlaps(subnet) || gateway == prefix.Addr() {
			return false
		}
		connected = connected || (subnet.Contains(gateway) &&
			(subnet.Bits() >= 31 || usableLANAddress(subnet, gateway)))
	}
	return connected
}

func (m *StaticWANManager) checkRouting(ctx context.Context, config RoutingConfig) error {
	wan, err := m.preferredWAN(ctx)
	if err != nil {
		return err
	}
	if config.IPv6 != nil && wan.Device != StaticWANInterface {
		return ErrRoutingWAN
	}
	var upstream []netip.Prefix
	if len(config.Routes) > 0 && wan.Device != "" {
		upstream, err = interfacePrefixes(wan.Device)
		if err != nil {
			return err
		}
	}
	for _, route := range config.Routes {
		if netip.MustParseAddr(route.Gateway).Is4() && !usableWANRoute(route, upstream) {
			return ErrRoutingGateway
		}
	}
	if config.IPv6 == nil {
		return nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	wan6 := netip.MustParsePrefix(config.IPv6.WANAddress)
	lan6 := netip.MustParsePrefix(config.IPv6.LANPrefix)
	for _, link := range interfaces {
		if link.Name == StaticWANInterface || link.Name == LANInterface {
			continue
		}
		addresses, err := interfacePrefixes(link.Name)
		if err != nil {
			return err
		}
		for _, prefix := range addresses {
			if prefix.Overlaps(wan6) || prefix.Overlaps(lan6) {
				return ErrRoutingOverlap
			}
		}
	}
	return m.checkIPv6Resources(ctx)
}

func (m *StaticWANManager) RoutingStatus(ctx context.Context) (RoutingStatus, error) {
	var result RoutingStatus
	err := withFileLock(wanLockPath(m.statePath), func() error {
		config, err := m.readRouting()
		if err != nil {
			return err
		}
		result.Config = config
		result.IPv6Active = config.IPv6 != nil && m.ipv6Ready(ctx, *config.IPv6)
		return nil
	})
	return result, err
}

func (m *StaticWANManager) ApplyRouting(ctx context.Context, requested RoutingConfig) (StaticWANTransaction, error) {
	var txn StaticWANTransaction
	err := withFileLock(wanLockPath(m.statePath), func() error {
		lan, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		state, err := m.readFirewall()
		if err != nil {
			return err
		}
		if lan == nil || state == nil {
			return ErrFirewallUnavailable
		}
		for _, path := range []string{m.statePath, filepath.Join(filepath.Dir(m.statePath), "wan-pppoe")} {
			pending, err := (&StaticWANManager{statePath: path}).hasPendingTransaction()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if pending {
				return ErrStaticWANPending
			}
		}
		config, err := normalizeRouting(*lan, requested)
		if err != nil {
			return err
		}
		if err := m.checkRouting(ctx, config); err != nil {
			return err
		}
		now := m.now()
		txn, err = m.startTransaction(ctx, staticWANRecord{FirewallExpiry: &now, Routing: true}, m.routingPath(), func() error {
			return m.setRouting(ctx, *lan, state, config, func() error {
				return writeJSONFile(m.routingPath(), config)
			})
		})
		return err
	})
	return txn, err
}

func (m *StaticWANManager) setRouting(ctx context.Context, lan LANConfig, state *firewallState, config RoutingConfig, save func() error) error {
	// Keep the IPv6 guard loaded until the forwarding change is reverted.
	if config.IPv6 == nil {
		if err := m.syncIPv6(ctx, nil, nil); err != nil {
			return err
		}
	}
	if err := save(); err != nil {
		return err
	}
	if err := m.installFirewallRules(ctx, lan); err != nil {
		return err
	}
	return m.syncFirewallRoute(ctx, state)
}

func (m *StaticWANManager) restoreRouting(ctx context.Context, directory string, backup staticWANBackup) error {
	lan, err := ManagedLAN(m.lanPath())
	if err != nil {
		return err
	}
	state, err := m.readFirewall()
	if err != nil {
		return err
	}
	if lan == nil || state == nil {
		return ErrFirewallUnavailable
	}
	data, exists, _, err := readStaticWANFile(filepath.Join(directory, "original"))
	if err != nil {
		return err
	}
	var config RoutingConfig
	if backup.OriginalExists {
		if !exists || json.Unmarshal(data, &config) != nil {
			return errors.New("invalid routing rollback copy")
		}
		if config, err = normalizeRouting(*lan, config); err != nil {
			return err
		}
	}
	return m.setRouting(ctx, *lan, state, config, func() error {
		if !backup.OriginalExists {
			return removeRegularFile(m.routingPath())
		}
		return writeAtomicFile(m.routingPath(), data, os.FileMode(backup.OriginalMode))
	})
}

func (m *StaticWANManager) routingReady(ctx context.Context) bool {
	config, err := m.readRouting()
	if err != nil || config.IPv6 != nil && !m.ipv6Ready(ctx, *config.IPv6) {
		return false
	}
	wan, err := m.preferredWAN(ctx)
	if err != nil {
		return false
	}
	routes, err := m.policyRoutes(ctx)
	if err != nil {
		return false
	}
	if config.IPv6 != nil {
		ipv6, err := m.ipv6Routes(ctx)
		if err != nil {
			return false
		}
		routes = append(routes, ipv6...)
	}
	for _, route := range config.Routes {
		device := wan.Device
		if netip.MustParseAddr(route.Gateway).Is6() {
			device = StaticWANInterface
		}
		if !hasStaticRoute(routes, route, device) {
			return false
		}
	}
	exists, err := m.firewallTableExists(ctx)
	return err == nil && exists
}

func hasStaticRoute(routes []firewallRoute, desired StaticRoute, device string) bool {
	for _, route := range routes {
		if route.Metric == staticRouteMetric && route.Device == device &&
			route.Gateway == desired.Gateway && routingDestination(route.Destination) == desired.Destination {
			return true
		}
	}
	return false
}

func (m *StaticWANManager) syncStaticRoutes(ctx context.Context, config RoutingConfig, device, family, table string, current []firewallRoute) error {
	var prefixes []netip.Prefix
	var err error
	if family == "-4" && device != "" && len(config.Routes) != 0 {
		if prefixes, err = interfacePrefixes(device); err != nil {
			return err
		}
	}
	wanted := map[string]StaticRoute{}
	for _, route := range config.Routes {
		if family == "-6" && netip.MustParseAddr(route.Gateway).Is6() ||
			family == "-4" && usableWANRoute(route, prefixes) {
			wanted[route.Destination] = route
		}
	}
	metric := strconv.Itoa(staticRouteMetric)
	for _, route := range current {
		if route.Metric != staticRouteMetric {
			continue
		}
		destination := routingDestination(route.Destination)
		if destination == "" || !isFirewallWAN(route.Device) {
			return fmt.Errorf("%w: table %s", ErrRoutingResources, table)
		}
		if _, ok := wanted[destination]; !ok {
			if err := m.lanNetAdmin(ctx, ipBinary, family, "route", "del", destination, "dev", route.Device,
				"table", table, "metric", metric); err != nil {
				return err
			}
		}
	}
	for _, route := range config.Routes {
		if _, ok := wanted[route.Destination]; !ok || hasStaticRoute(current, route, device) {
			continue
		}
		if err := m.lanNetAdmin(ctx, ipBinary, family, "route", "replace", route.Destination,
			"via", route.Gateway, "dev", device, "onlink", "table", table, "metric", metric); err != nil {
			return err
		}
	}
	return nil
}

func (m *StaticWANManager) syncRouting(ctx context.Context, config RoutingConfig, wan string) error {
	current, err := m.policyRoutes(ctx)
	if err != nil {
		return err
	}
	if err := m.syncStaticRoutes(ctx, config, wan, "-4", wanRouteTable, current); err != nil {
		return err
	}
	if wan != "" && wan != StaticWANInterface {
		return m.syncIPv6(ctx, nil, nil)
	}
	return m.syncIPv6(ctx, config.IPv6, config.Routes)
}

func routingDestination(value string) string {
	if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Bits() != 0 && !prefix.Addr().Is4In6() {
		return prefix.Masked().String()
	}
	if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" && !address.Is4In6() {
		return netip.PrefixFrom(address, address.BitLen()).String()
	}
	return ""
}

func renderIPv6Forward(out *strings.Builder, config *RoutedIPv6) {
	if config == nil {
		return
	}
	fmt.Fprintf(out, `  iifname "%[1]s" oifname "%[2]s" ip6 saddr %[3]s ct state new,established,related counter accept
  iifname "%[2]s" oifname "%[1]s" ip6 daddr %[3]s ct state established,related counter accept
  meta nfproto ipv6 counter drop
`, LANInterface, StaticWANInterface, config.LANPrefix)
}
