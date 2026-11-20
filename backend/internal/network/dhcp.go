package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"time"
)

var interfaceNamePattern = regexp.MustCompile("^[a-zA-Z0-9_.-]{1,15}$")

// ValidInterfaceName reports whether an interface name is safe to pass as a single argument.
func ValidInterfaceName(name string) bool {
	return interfaceNamePattern.MatchString(name)
}

// DHCPStatus contains only lease details needed by the WAN status view.
// The DHCP client identifier returned by networkctl is deliberately omitted.
type DHCPStatus struct {
	Interface  string
	LinkState  string
	LeaseState string
	Addresses  []string
	Gateway    string
	DNS        []string
}

type networkctlStatus struct {
	CarrierState string
	Addresses    []struct {
		Family       int
		Address      []int
		PrefixLength int
		ConfigSource string
	}
	Routes []struct {
		Family                  int
		DestinationPrefixLength int
		Gateway                 []int
		ConfigSource            string
	}
	DNS []struct {
		Family       int
		Address      []int
		ConfigSource string
	}
	DHCPv4Client *struct {
		Lease *struct {
			LeaseTimestampUSec uint64
		}
	}
}

// ReadDHCPStatus reads current systemd-networkd status for one interface.
func ReadDHCPStatus(interfaceName string) (DHCPStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ReadDHCPStatusWith(ctx, interfaceName, runNetworkctl)
}

func ReadDHCPStatusWith(
	ctx context.Context,
	interfaceName string,
	run func(context.Context, string) ([]byte, error),
) (DHCPStatus, error) {
	if !ValidInterfaceName(interfaceName) {
		return DHCPStatus{}, errors.New("invalid interface name")
	}
	output, err := run(ctx, interfaceName)
	if err != nil {
		return DHCPStatus{}, errors.New("read network status")
	}
	var status networkctlStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return DHCPStatus{}, errors.New("decode network status")
	}

	result := DHCPStatus{
		Interface:  interfaceName,
		LinkState:  status.CarrierState,
		LeaseState: "waiting",
		Addresses:  []string{},
		DNS:        []string{},
	}
	if status.DHCPv4Client != nil && status.DHCPv4Client.Lease != nil {
		result.LeaseState = "leased"
	}
	if status.CarrierState == "no-carrier" || status.CarrierState == "off" {
		result.LeaseState = "link_down"
	}
	for _, address := range status.Addresses {
		if address.Family != 2 || address.ConfigSource != "DHCPv4" {
			continue
		}
		ip, err := addressString(address.Address)
		if err != nil || address.PrefixLength > 32 {
			continue
		}
		result.Addresses = append(result.Addresses, fmt.Sprintf("%s/%d", ip, address.PrefixLength))
	}
	for _, route := range status.Routes {
		if route.Family != 2 || route.DestinationPrefixLength != 0 || route.ConfigSource != "DHCPv4" {
			continue
		}
		if gateway, err := addressString(route.Gateway); err == nil {
			result.Gateway = gateway
			break
		}
	}
	for _, dns := range status.DNS {
		if dns.Family != 2 || dns.ConfigSource != "DHCPv4" {
			continue
		}
		if server, err := addressString(dns.Address); err == nil {
			result.DNS = append(result.DNS, server)
		}
	}
	sort.Strings(result.Addresses)
	result.DNS = uniqueSorted(result.DNS)
	return result, nil
}

func runNetworkctl(ctx context.Context, interfaceName string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "/usr/bin/networkctl", "status", "--json=short", interfaceName, "--no-pager").Output()
	if err != nil {
		return nil, errors.New("networkctl failed")
	}
	return output, nil
}

func addressString(value []int) (string, error) {
	if len(value) != net.IPv4len {
		return "", errors.New("invalid IPv4 address")
	}
	bytes := make(net.IP, net.IPv4len)
	for index, part := range value {
		if part < 0 || part > 255 {
			return "", errors.New("invalid IPv4 address")
		}
		bytes[index] = byte(part)
	}
	return bytes.String(), nil
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	if len(values) < 2 {
		return values
	}
	unique := values[:1]
	for _, value := range values[1:] {
		if value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	return unique
}
