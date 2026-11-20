package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ActiveNetworkStatus contains the live IPv4 settings on an interface,
// regardless of whether they came from DHCP or a static networkd profile.
type ActiveNetworkStatus struct {
	LinkState string
	Addresses []string
	Gateway   string
	DNS       []string
}

// ReadActiveNetworkStatus reads the live IPv4 state for one interface.
func ReadActiveNetworkStatus(interfaceName string) (ActiveNetworkStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ReadActiveNetworkStatusWith(ctx, interfaceName, runNetworkctl)
}

// ReadActiveNetworkStatusWith parses networkctl output supplied by the caller.
func ReadActiveNetworkStatusWith(
	ctx context.Context,
	interfaceName string,
	run func(context.Context, string) ([]byte, error),
) (ActiveNetworkStatus, error) {
	if !ValidInterfaceName(interfaceName) {
		return ActiveNetworkStatus{}, errors.New("invalid interface name")
	}
	output, err := run(ctx, interfaceName)
	if err != nil {
		return ActiveNetworkStatus{}, errors.New("read active network status")
	}
	var status networkctlStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return ActiveNetworkStatus{}, errors.New("decode active network status")
	}

	result := ActiveNetworkStatus{
		LinkState: status.CarrierState,
		Addresses: []string{},
		DNS:       []string{},
	}
	for _, address := range status.Addresses {
		if address.Family != 2 || address.PrefixLength > 32 {
			continue
		}
		ip, err := addressString(address.Address)
		if err == nil {
			result.Addresses = append(result.Addresses, fmt.Sprintf("%s/%d", ip, address.PrefixLength))
		}
	}
	for _, route := range status.Routes {
		if route.Family != 2 || route.DestinationPrefixLength != 0 {
			continue
		}
		gateway, err := addressString(route.Gateway)
		if err != nil {
			continue
		}
		if route.ConfigSource == "static" {
			result.Gateway = gateway
			break
		}
		if result.Gateway == "" {
			result.Gateway = gateway
		}
	}
	for _, dns := range status.DNS {
		if dns.Family != 2 {
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
