package network

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const (
	StaticWANInterface     = "enp1s0"
	staticWANRouteMetric   = 5000
	maxStaticWANDNSServers = 4
)

var (
	ErrStaticWANAddress = errors.New("invalid static WAN address")
	ErrStaticWANGateway = errors.New("invalid static WAN gateway")
	ErrStaticWANDNS     = errors.New("invalid static WAN DNS")
)

// StaticWANConfig describes the IPv4 settings applied to the fixed WAN port.
type StaticWANConfig struct {
	Address string   `json:"address"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
}

// ValidateStaticWAN checks a static IPv4 profile without performing I/O.
func ValidateStaticWAN(config StaticWANConfig) error {
	prefix, err := netip.ParsePrefix(config.Address)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 {
		return ErrStaticWANAddress
	}
	host := prefix.Addr()
	network := prefix.Masked()
	if !isUsableIPv4(host) || host == network.Addr() || isBroadcastAddress(network, host) {
		return ErrStaticWANAddress
	}

	gateway, err := netip.ParseAddr(config.Gateway)
	if err != nil || !gateway.Is4() || !isUsableIPv4(gateway) ||
		!network.Contains(gateway) || gateway == network.Addr() || gateway == host || isBroadcastAddress(network, gateway) {
		return ErrStaticWANGateway
	}

	if len(config.DNS) == 0 || len(config.DNS) > maxStaticWANDNSServers {
		return ErrStaticWANDNS
	}
	seen := make(map[netip.Addr]struct{}, len(config.DNS))
	for _, value := range config.DNS {
		server, err := netip.ParseAddr(value)
		if err != nil || !server.Is4() || !isUsableIPv4(server) {
			return ErrStaticWANDNS
		}
		if _, ok := seen[server]; ok {
			return ErrStaticWANDNS
		}
		seen[server] = struct{}{}
	}
	return nil
}

// RenderStaticWAN returns the complete networkd file for enp1s0.
func RenderStaticWAN(config StaticWANConfig) ([]byte, error) {
	if err := ValidateStaticWAN(config); err != nil {
		return nil, err
	}
	var output strings.Builder
	output.WriteString("[Match]\nName=enp1s0\n\n[Network]\nDHCP=no\nLinkLocalAddressing=no\nIPv6AcceptRA=no\n")
	for _, server := range config.DNS {
		fmt.Fprintf(&output, "DNS=%s\n", server)
	}
	fmt.Fprintf(&output, "\n[Address]\nAddress=%s\nRouteMetric=%d\n", config.Address, staticWANRouteMetric)
	fmt.Fprintf(&output, "\n[Route]\nGateway=%s\nMetric=%d\n", config.Gateway, staticWANRouteMetric)
	return []byte(output.String()), nil
}

func isUsableIPv4(address netip.Addr) bool {
	return address.Is4() && address.IsGlobalUnicast() && !address.IsUnspecified() && !address.IsLoopback() &&
		!address.IsLinkLocalUnicast() && !address.IsMulticast()
}

func isBroadcastAddress(prefix netip.Prefix, address netip.Addr) bool {
	if !prefix.Addr().Is4() || !address.Is4() || prefix.Bits() == 32 {
		return false
	}
	base := prefix.Masked().Addr().As4()
	value := address.As4()
	mask := uint32(0xffffffff) >> prefix.Bits()
	baseValue := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	valueUint := uint32(value[0])<<24 | uint32(value[1])<<16 | uint32(value[2])<<8 | uint32(value[3])
	return valueUint == baseValue|mask
}
