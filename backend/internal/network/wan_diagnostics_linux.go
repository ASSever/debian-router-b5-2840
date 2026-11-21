package network

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"syscall"
	"time"
)

// RunWANDiagnostic uses the configured networkd IPv4 Ethernet uplink.
// All packets are bound to that link, independent of the host's default WAN.
func RunWANDiagnostic(ctx context.Context) (WANDiagnostic, error) {
	device := StaticWANInterface
	status, err := ReadActiveNetworkStatusWith(ctx, device, runNetworkctl)
	if err != nil {
		return WANDiagnostic{}, err
	}
	// A managed VLAN rides on the physical WAN. Check physical carrier first.
	if status.LinkState == "carrier" || status.LinkState == "degraded-carrier" {
		if vlan, err := net.InterfaceByName(wanVLANName); err == nil && vlan.Flags&net.FlagUp != 0 {
			device = vlan.Name
			status, err = ReadActiveNetworkStatusWith(ctx, device, runNetworkctl)
			if err != nil {
				return WANDiagnostic{}, err
			}
		}
	}
	var gateway string
	if len(status.Addresses) != 0 {
		gateway, err = diagnosticGateway(ctx, device)
		if err != nil {
			return WANDiagnostic{}, err
		}
	}
	return diagnoseWAN(ctx, device, gateway, status, wanProbes{probeGateway, probeWANResolver, probeWANExternal}), nil
}

func diagnosticGateway(ctx context.Context, device string) (string, error) {
	output, err := runCommand(ctx, ipBinary, "-4", "-json", "route", "show", "table", "main", "default", "dev", device)
	if err != nil {
		return "", err
	}
	var routes []firewallRoute
	if err := json.Unmarshal([]byte(output), &routes); err != nil {
		return "", err
	}
	var selected *firewallRoute
	for _, route := range routes {
		ip, err := netip.ParseAddr(route.Gateway)
		if err == nil && ip.Is4() && isUsableIPv4(ip) && !slices.Contains(route.Flags, "linkdown") &&
			(selected == nil || route.Metric < selected.Metric) {
			selected = &route
		}
	}
	if selected != nil {
		return selected.Gateway, nil
	}
	return "", nil
}

func probeGateway(ctx context.Context, device, gateway string) string {
	if exec.CommandContext(ctx, "/usr/bin/ping", "-4", "-n", "-c", "1", "-w", "4", "-I", device, gateway).Run() == nil {
		return "ok"
	}
	// A freshly reachable neighbour can have ICMP echo disabled.
	output, err := runCommand(ctx, ipBinary, "-4", "-json", "neigh", "show", "to", gateway, "dev", device)
	if err != nil {
		return "unverified"
	}
	var neighbours []struct {
		State []string `json:"state"`
	}
	if json.Unmarshal([]byte(output), &neighbours) == nil && len(neighbours) == 1 {
		if slices.Contains(neighbours[0].State, "REACHABLE") {
			return "ok"
		}
		if slices.Contains(neighbours[0].State, "FAILED") {
			return "failed"
		}
	}
	return "unverified"
}

func wanDialer(device string) net.Dialer {
	return net.Dialer{
		Timeout: 1500 * time.Millisecond,
		Control: func(_, _ string, socket syscall.RawConn) error {
			var bindErr error
			err := socket.Control(func(fd uintptr) {
				bindErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, device)
			})
			return errors.Join(err, bindErr)
		},
	}
}

func probeWANResolver(ctx context.Context, device, server string) bool {
	target := net.JoinHostPort(server, "53")
	dialer := wanDialer(device)
	resolver := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, protocol, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, protocol, target)
		},
	}
	query, cancel := context.WithTimeout(ctx, 1300*time.Millisecond)
	defer cancel()
	addresses, err := resolver.LookupNetIP(query, "ip4", "example.com.")
	return err == nil && len(addresses) != 0
}

func probeWANExternal(ctx context.Context, device string) bool {
	dialer := wanDialer(device)
	for _, target := range []string{"1.1.1.1:443", "8.8.8.8:443"} {
		connection, err := dialer.DialContext(ctx, "tcp4", target)
		if err == nil {
			connection.Close()
			return true
		}
	}
	return false
}
