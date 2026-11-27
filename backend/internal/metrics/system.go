package metrics

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

type Space struct {
	Used  uint64 `json:"used_bytes"`
	Total uint64 `json:"total_bytes"`
}

type Link struct {
	network.Interface
	Addresses []string `json:"addresses"`
}

type System struct {
	CPU         *float64                       `json:"cpu_percent"`
	Temperature *float64                       `json:"temperature_c"`
	Memory      *Space                         `json:"memory"`
	Disk        *Space                         `json:"disk"`
	Uptime      *float64                       `json:"uptime_seconds"`
	WAN         *Link                          `json:"wan"`
	LAN         *Link                          `json:"lan"`
	WANGateway  *string                        `json:"wan_gateway"`
	WANDNS      []string                       `json:"wan_dns"`
	Firewall    *bool                          `json:"firewall_enabled"`
	Services    []privilegedagent.ServiceState `json:"services"`
}

type cpuCounters struct {
	total uint64
	idle  uint64
}

func cpuSnapshot() *cpuCounters {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	first, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(first)
	if len(fields) < 9 || fields[0] != "cpu" {
		return nil
	}
	var read cpuCounters
	for i, field := range fields[1:9] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return nil
		}
		read.total += value
		if i == 3 || i == 4 {
			read.idle += value
		}
	}
	return &read
}

func cpuUsage(previous, current *cpuCounters) *float64 {
	if previous == nil || current == nil || current.total <= previous.total || current.idle < previous.idle {
		return nil
	}
	total, idle := current.total-previous.total, current.idle-previous.idle
	if idle > total {
		return nil
	}
	value := 100 * float64(total-idle) / float64(total)
	return &value
}

func memory() *Space {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && (fields[0] == "MemTotal:" || fields[0] == "MemAvailable:") && fields[2] == "kB" {
			if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				values[fields[0]] = value * 1024
			}
		}
	}
	total, available := values["MemTotal:"], values["MemAvailable:"]
	if _, ok := values["MemAvailable:"]; !ok || total == 0 || available > total {
		return nil
	}
	return &Space{Total: total, Used: total - available}
}

func temperature() *float64 {
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, zone := range zones {
		kind, err := os.ReadFile(filepath.Join(zone, "type"))
		if err == nil && strings.TrimSpace(string(kind)) == "x86_pkg_temp" {
			if value := firstNumber(filepath.Join(zone, "temp")); value != nil {
				degrees := *value / 1000
				return &degrees
			}
		}
	}
	return nil
}

func firstNumber(path string) *float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return nil
	}
	return &value
}

func activeLink(link network.Interface) *Link {
	device, err := net.InterfaceByName(link.Name)
	if err != nil {
		return nil
	}
	addresses, err := device.Addrs()
	if err != nil {
		return nil
	}
	info := Link{Interface: link, Addresses: make([]string, 0, len(addresses))}
	for _, address := range addresses {
		info.Addresses = append(info.Addresses, address.String())
	}
	return &info
}

func readSystem(ctx context.Context, dir, socket string, previous *cpuCounters) (System, *cpuCounters) {
	current := cpuSnapshot()
	system := System{
		CPU: cpuUsage(previous, current), Temperature: temperature(), Memory: memory(), Disk: diskSpace(dir),
		Uptime: firstNumber("/proc/uptime"), Services: privilegedagent.UnavailableServiceStates(),
	}
	links, _ := network.ReadInterfaces()
	for _, link := range links {
		switch link.Name {
		case network.StaticWANInterface:
			system.WAN = activeLink(link)
		case network.LANInterface:
			system.LAN = activeLink(link)
		}
	}
	if response, err := privilegedagent.SendRequest(ctx, socket, "service-status"); err == nil && len(response.ServiceStates) != 0 {
		system.Services = response.ServiceStates
	}
	if ctx.Err() == nil {
		if response, err := privilegedagent.SendRequest(ctx, socket, "wan-probe-state"); err == nil && response.WANProbeState != nil {
			state := response.WANProbeState.Status
			system.WANGateway = &state.Gateway
			system.WANDNS = state.DNS
		}
	}
	if ctx.Err() == nil {
		if response, err := privilegedagent.SendRequest(ctx, socket, "firewall-status"); err == nil && response.Firewall != nil {
			system.Firewall = &response.Firewall.Enabled
		}
	}
	return system, current
}
