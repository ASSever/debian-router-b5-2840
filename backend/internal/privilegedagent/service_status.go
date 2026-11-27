package privilegedagent

import (
	"context"
	"os/exec"
	"strings"
)

type ServiceState struct {
	Unit  string `json:"unit"`
	State string `json:"state"`
}

var dashboardUnits = []string{
	"debian-router-api.service", "debian-router-agent.service", "systemd-networkd.service",
	"debian-router-lan-dnsmasq.service", "debian-router-sing-box.service", "debian-router-zapret.service",
	"debian-router-monitor.service", "debian-router-failover.service", "debian-router-recovery.service",
	"debian-router-mqtt.service",
}

func readServiceStates(ctx context.Context) []ServiceState {
	return readUnitStates(ctx, dashboardUnits)
}

func readUnitStates(ctx context.Context, units []string) []ServiceState {
	args := append([]string{"--system", "--no-pager", "show", "--property=Id,LoadState,ActiveState", "--"}, units...)
	// A missing unit can produce a nonzero exit status alongside valid properties.
	output, _ := exec.CommandContext(ctx, "/usr/bin/systemctl", args...).Output()
	return parseUnitStates(string(output), units)
}

func parseServiceStates(output string) []ServiceState {
	return parseUnitStates(output, dashboardUnits)
}

func parseUnitStates(output string, units []string) []ServiceState {
	states := make(map[string]string, len(units))
	for _, block := range strings.Split(strings.TrimSpace(output), "\n\n") {
		properties := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				properties[key] = value
			}
		}
		state := properties["ActiveState"]
		if properties["LoadState"] == "not-found" {
			state = "not_loaded"
		}
		states[properties["Id"]] = state
	}
	services := make([]ServiceState, 0, len(units))
	for _, unit := range units {
		state := states[unit]
		switch state {
		case "active", "inactive", "failed", "activating", "deactivating", "reloading", "maintenance", "refreshing", "not_loaded":
		default:
			state = "unavailable"
		}
		services = append(services, ServiceState{Unit: unit, State: state})
	}
	return services
}

func UnavailableServiceStates() []ServiceState { return parseServiceStates("") }
