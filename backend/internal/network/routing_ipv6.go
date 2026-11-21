package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const ipv6Marker = "# Managed by debian-router-b5-2840 (routed IPv6)\n"

type ipv6LinkFlags struct {
	Index      int
	Forwarding string
	AcceptRA   string
}

type ipv6Runtime struct {
	Boot       string
	All        string
	Default    string
	Links      map[string]ipv6LinkFlags
	Configured bool
}

func (m *StaticWANManager) ipv6StatePath() string {
	return filepath.Join(filepath.Dir(m.statePath), "ipv6-runtime.json")
}

func (m *StaticWANManager) readIPv6Runtime() (*ipv6Runtime, error) {
	data, exists, _, err := readStaticWANFile(m.ipv6StatePath())
	if err != nil || !exists {
		return nil, err
	}
	var state ipv6Runtime
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func readIPv6Flag(device, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc/sys/net/ipv6/conf", device, key))
	return strings.TrimSpace(string(data)), err
}

func captureIPv6Runtime(boot string) (ipv6Runtime, error) {
	state := ipv6Runtime{Boot: boot, Links: map[string]ipv6LinkFlags{}}
	var err error
	if state.All, err = readIPv6Flag("all", "forwarding"); err != nil {
		return state, err
	}
	if state.Default, err = readIPv6Flag("default", "forwarding"); err != nil {
		return state, err
	}
	devices, err := net.Interfaces()
	if err != nil {
		return state, err
	}
	for _, device := range devices {
		forwarding, err := readIPv6Flag(device.Name, "forwarding")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return state, err
		}
		acceptRA, err := readIPv6Flag(device.Name, "accept_ra")
		if err != nil {
			return state, err
		}
		state.Links[device.Name] = ipv6LinkFlags{device.Index, forwarding, acceptRA}
	}
	return state, nil
}

func (m *StaticWANManager) writeIPv6Flags(ctx context.Context, flags []string) error {
	if len(flags) == 0 {
		return nil
	}
	return m.lanNetAdmin(ctx, append([]string{"/usr/sbin/sysctl", "-q", "-w"}, flags...)...)
}

func ipv6Sysctl(device, setting, value string) string {
	return "net/ipv6/conf/" + device + "/" + setting + "=" + value
}

func ipv6SnapshotMatches(name string, flags ipv6LinkFlags) bool {
	link, err := net.InterfaceByName(name)
	return err == nil && link.Index == flags.Index
}

func (m *StaticWANManager) enableIPv6Forwarding(ctx context.Context, state *ipv6Runtime) error {
	all, err := readIPv6Flag("all", "forwarding")
	if err != nil || all == "1" && state.Configured {
		return err
	}
	if all == "0" {
		// Linux 6.12 purges kernel RA defaults on enabling forwarding unless
		// accept_ra is 2. Preserve host routes before toggling the global flag.
		var prepare []string
		for name, flags := range state.Links {
			if flags.AcceptRA == "1" && name != StaticWANInterface && name != LANInterface && ipv6SnapshotMatches(name, flags) {
				prepare = append(prepare, ipv6Sysctl(name, "accept_ra", "2"))
			}
		}
		prepare = append(prepare, ipv6Sysctl("all", "forwarding", "1"))
		if err := m.writeIPv6Flags(ctx, prepare); err != nil {
			return err
		}
	}
	settings := []string{ipv6Sysctl("default", "forwarding", state.Default)}
	for name, flags := range state.Links {
		if name != StaticWANInterface && name != LANInterface && ipv6SnapshotMatches(name, flags) {
			settings = append(settings, ipv6Sysctl(name, "forwarding", flags.Forwarding),
				ipv6Sysctl(name, "accept_ra", flags.AcceptRA))
		}
	}
	if err := m.writeIPv6Flags(ctx, settings); err != nil {
		return err
	}
	state.Configured = true
	return writeJSONFile(m.ipv6StatePath(), state)
}

func (m *StaticWANManager) restoreIPv6Forwarding(ctx context.Context, state *ipv6Runtime) error {
	boot, err := currentBoot()
	if err != nil || state.Boot != boot {
		return err
	}
	var settings []string
	if all, err := readIPv6Flag("all", "forwarding"); err != nil {
		return err
	} else if all != state.All {
		settings = append(settings, ipv6Sysctl("all", "forwarding", state.All))
	}
	settings = append(settings, ipv6Sysctl("default", "forwarding", state.Default))
	for name, flags := range state.Links {
		if ipv6SnapshotMatches(name, flags) {
			settings = append(settings, ipv6Sysctl(name, "forwarding", flags.Forwarding),
				ipv6Sysctl(name, "accept_ra", flags.AcceptRA))
		}
	}
	return m.writeIPv6Flags(ctx, settings)
}

func (m *StaticWANManager) ipv6DropIn(profile string) string {
	return filepath.Join(profile+".d", "50-router-ipv6.conf")
}

func renderWANIPv6(config RoutedIPv6) []byte {
	return fmt.Appendf(nil, ipv6Marker+"[Network]\nLinkLocalAddressing=ipv6\nIPv6AcceptRA=no\n"+
		"\n[Address]\nAddress=%s\nAddPrefixRoute=no\n", config.WANAddress)
}

func renderLANIPv6(config RoutedIPv6) []byte {
	return fmt.Appendf(nil, ipv6Marker+"[Network]\nLinkLocalAddressing=ipv6\nIPv6AcceptRA=no\nIPv6SendRA=yes\n"+
		"\n[Address]\nAddress=%s\n\n[IPv6SendRA]\nEmitDNS=no\nEmitDomains=no\nRouterLifetimeSec=180\n"+
		"\n[IPv6Prefix]\nPrefix=%s\nPreferredLifetimeSec=180\nValidLifetimeSec=300\n",
		config.lanAddress(), config.LANPrefix)
}

func syncIPv6File(path string, content []byte) (bool, error) {
	current, exists, _, err := readStaticWANFile(path)
	if err != nil {
		return false, err
	}
	if exists && !bytes.HasPrefix(current, []byte(ipv6Marker)) {
		return false, ErrRoutingResources
	}
	if bytes.Equal(current, content) {
		return false, nil
	}
	if content == nil {
		return true, removeRegularFile(path)
	}
	parent := filepath.Dir(path)
	switch err := os.Mkdir(parent, 0755); {
	case err == nil:
		// The agent's umask is 0077. networkd reads these profiles as a
		// separate unprivileged user and needs to traverse the directory.
		if err := os.Chmod(parent, 0755); err != nil {
			_ = os.Remove(parent)
			return false, err
		}
	case !errors.Is(err, os.ErrExist):
		return false, err
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, ErrRoutingResources
	}
	return true, writeAtomicFile(path, content, 0644)
}

func (m *StaticWANManager) syncIPv6Link(ctx context.Context, profile, device string, content []byte, address netip.Prefix) error {
	changed, err := syncIPv6File(m.ipv6DropIn(profile), content)
	if err != nil {
		return err
	}
	if !changed && address.IsValid() {
		prefixes, err := interfacePrefixes(device)
		if err != nil {
			return err
		}
		for _, prefix := range prefixes {
			if prefix == address {
				return nil
			}
		}
	}
	// On teardown, reload even if an interrupted attempt already removed the file.
	if _, err := m.run(ctx, "/usr/bin/networkctl", "reload"); err != nil {
		return err
	}
	_, err = m.run(ctx, "/usr/bin/networkctl", "reconfigure", device)
	return err
}

func (m *StaticWANManager) ipv6Routes(ctx context.Context) ([]firewallRoute, error) {
	output, err := m.run(ctx, ipBinary, "-6", "-json", "route", "show", "table", "all")
	if err != nil {
		return nil, err
	}
	var all []firewallRoute
	if err := json.Unmarshal([]byte(output), &all); err != nil {
		return nil, err
	}
	var routes []firewallRoute
	for _, route := range all {
		if strings.Trim(string(route.Table), `"`) == ipv6PolicyTable {
			routes = append(routes, route)
		}
	}
	return routes, nil
}

func (m *StaticWANManager) ipv6PolicyRule(ctx context.Context) (exists, foreign bool, err error) {
	output, err := m.run(ctx, ipBinary, "-6", "-json", "rule", "show")
	if err != nil {
		return false, false, err
	}
	var rules []struct {
		Priority int             `json:"priority"`
		Input    string          `json:"iif"`
		Table    json.RawMessage `json:"table"`
		Source   string          `json:"src"`
	}
	if err := json.Unmarshal([]byte(output), &rules); err != nil {
		return false, false, err
	}
	for _, rule := range rules {
		if rule.Priority != 24226 {
			continue
		}
		ours := rule.Input == LANInterface && rule.Source == "all" && strings.Trim(string(rule.Table), `"`) == ipv6PolicyTable
		exists, foreign = exists || ours, foreign || !ours
	}
	return exists, foreign, nil
}

func (m *StaticWANManager) checkIPv6Resources(ctx context.Context) error {
	state, err := m.readIPv6Runtime()
	if err != nil {
		return err
	}
	for _, profile := range []string{m.configPath, m.lanPath()} {
		content, exists, _, err := readStaticWANFile(m.ipv6DropIn(profile))
		if err != nil {
			return err
		}
		if exists && !bytes.HasPrefix(content, []byte(ipv6Marker)) {
			return ErrRoutingResources
		}
	}
	rule, foreign, err := m.ipv6PolicyRule(ctx)
	if err != nil {
		return err
	}
	routes, err := m.ipv6Routes(ctx)
	if err != nil {
		return err
	}
	if foreign || state == nil && (rule || len(routes) != 0) {
		return ErrRoutingResources
	}
	return nil
}

func (m *StaticWANManager) syncIPv6Policy(ctx context.Context, config RoutedIPv6) error {
	rule, foreign, err := m.ipv6PolicyRule(ctx)
	if err != nil {
		return err
	}
	if foreign {
		return ErrRoutingResources
	}
	routes, err := m.ipv6Routes(ctx)
	if err != nil {
		return err
	}
	var fallback, matched bool
	wanSubnet := netip.MustParsePrefix(config.WANAddress).Masked().String()
	var connected bool
	for _, route := range routes {
		if route.Destination == "default" {
			fallback = fallback || route.Type == "unreachable"
			matched = matched || route.Metric == wanRouteMetric && route.Device == StaticWANInterface && route.Gateway == config.WANGateway
		}
		if route.Metric != wanLinkMetric || route.Device != StaticWANInterface {
			continue
		}
		prefix := routingDestination(route.Destination)
		if prefix == wanSubnet {
			connected = true
		} else if prefix != "" {
			if err := m.lanNetAdmin(ctx, ipBinary, "-6", "route", "del", prefix, "dev", StaticWANInterface,
				"table", ipv6PolicyTable, "metric", "11"); err != nil {
				return err
			}
		}
	}
	if !fallback {
		if err := m.lanNetAdmin(ctx, ipBinary, "-6", "route", "replace", "unreachable", "default",
			"table", ipv6PolicyTable, "metric", "32767"); err != nil {
			return err
		}
	}
	if !matched {
		if err := m.lanNetAdmin(ctx, ipBinary, "-6", "route", "replace", "default", "via", config.WANGateway,
			"dev", StaticWANInterface, "onlink", "table", ipv6PolicyTable, "metric", "10"); err != nil {
			return err
		}
	}
	if !connected {
		if err := m.lanNetAdmin(ctx, ipBinary, "-6", "route", "replace", wanSubnet,
			"dev", StaticWANInterface, "table", ipv6PolicyTable, "metric", "11"); err != nil {
			return err
		}
	}
	if !rule {
		return m.lanNetAdmin(ctx, ipBinary, "-6", "rule", "add", "priority", ipv6PolicyTable, "iif", LANInterface,
			"lookup", ipv6PolicyTable)
	}
	return nil
}

func (m *StaticWANManager) removeIPv6Policy(ctx context.Context) error {
	rule, _, err := m.ipv6PolicyRule(ctx)
	if err != nil {
		return err
	}
	routes, err := m.ipv6Routes(ctx)
	if err != nil {
		return err
	}
	if len(routes) != 0 {
		if err := m.lanNetAdmin(ctx, ipBinary, "-6", "route", "flush", "table", ipv6PolicyTable); err != nil {
			return err
		}
	}
	if rule {
		return m.lanNetAdmin(ctx, ipBinary, "-6", "rule", "del", "priority", ipv6PolicyTable, "iif", LANInterface,
			"lookup", ipv6PolicyTable)
	}
	return nil
}

func (m *StaticWANManager) syncIPv6(ctx context.Context, config *RoutedIPv6, static []StaticRoute) error {
	state, err := m.readIPv6Runtime()
	if err != nil {
		return err
	}
	if config == nil {
		if state == nil {
			return nil
		}
		if err := m.restoreIPv6Forwarding(ctx, state); err != nil {
			return err
		}
		if err := m.syncIPv6Link(ctx, m.lanPath(), LANInterface, nil, netip.Prefix{}); err != nil {
			return err
		}
		if err := m.syncIPv6Link(ctx, m.configPath, StaticWANInterface, nil, netip.Prefix{}); err != nil {
			return err
		}
		if err := m.removeIPv6Policy(ctx); err != nil {
			return err
		}
		return removeRegularFile(m.ipv6StatePath())
	}
	boot, err := currentBoot()
	if err != nil {
		return err
	}
	if state == nil || state.Boot != boot {
		if err := m.checkIPv6Resources(ctx); err != nil {
			return err
		}
		snapshot, err := captureIPv6Runtime(boot)
		if err != nil {
			return err
		}
		state = &snapshot
		if err := writeJSONFile(m.ipv6StatePath(), state); err != nil {
			return err
		}
	}
	if err := m.syncIPv6Link(ctx, m.configPath, StaticWANInterface, renderWANIPv6(*config),
		netip.MustParsePrefix(config.WANAddress)); err != nil {
		return err
	}
	if err := m.syncIPv6Policy(ctx, *config); err != nil {
		return err
	}
	routes, err := m.ipv6Routes(ctx)
	if err != nil {
		return err
	}
	if err := m.syncStaticRoutes(ctx, RoutingConfig{Routes: static}, StaticWANInterface, "-6", ipv6PolicyTable, routes); err != nil {
		return err
	}
	if err := m.enableIPv6Forwarding(ctx, state); err != nil {
		return err
	}
	// Announce the default to LAN clients only once the guarded path is ready.
	return m.syncIPv6Link(ctx, m.lanPath(), LANInterface, renderLANIPv6(*config), config.lanAddress())
}

func (m *StaticWANManager) ipv6Ready(ctx context.Context, config RoutedIPv6) bool {
	all, err := readIPv6Flag("all", "forwarding")
	if err != nil || all != "1" {
		return false
	}
	for _, item := range ipv6Addresses(config) {
		prefixes, err := m.usableIPv6Addresses(ctx, item.device)
		if err != nil {
			return false
		}
		found, linkLocal := false, false
		for _, prefix := range prefixes {
			found = found || prefix == item.address
			linkLocal = linkLocal || prefix.Addr().Is6() && prefix.Addr().IsLinkLocalUnicast()
		}
		if !found || !linkLocal {
			return false
		}
	}
	rule, foreign, err := m.ipv6PolicyRule(ctx)
	if err != nil || !rule || foreign {
		return false
	}
	routes, err := m.ipv6Routes(ctx)
	if err != nil {
		return false
	}
	for _, route := range routes {
		if route.Destination == "default" && route.Device == StaticWANInterface && route.Gateway == config.WANGateway {
			return true
		}
	}
	return false
}

func (m *StaticWANManager) usableIPv6Addresses(ctx context.Context, device string) ([]netip.Prefix, error) {
	output, err := m.run(ctx, ipBinary, "-6", "-json", "address", "show", "dev", device)
	if err != nil {
		return nil, err
	}
	var links []struct {
		Addresses []struct {
			IP        netip.Addr `json:"local"`
			Length    int        `json:"prefixlen"`
			Tentative bool       `json:"tentative"`
			DADFailed bool       `json:"dadfailed"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(output), &links); err != nil {
		return nil, err
	}
	var result []netip.Prefix
	for _, link := range links {
		for _, address := range link.Addresses {
			if address.IP.Is6() && !address.Tentative && !address.DADFailed {
				result = append(result, netip.PrefixFrom(address.IP, address.Length))
			}
		}
	}
	return result, nil
}

type ipv6Address struct {
	device  string
	address netip.Prefix
}

func ipv6Addresses(config RoutedIPv6) [2]ipv6Address {
	return [2]ipv6Address{
		{LANInterface, config.lanAddress()},
		{StaticWANInterface, netip.MustParsePrefix(config.WANAddress)},
	}
}
