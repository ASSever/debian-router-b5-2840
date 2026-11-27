package hostmetrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Space holds byte counts for memory or filesystem capacity.
type Space struct {
	Used  uint64 `json:"used_bytes"`
	Total uint64 `json:"total_bytes"`
}

// Memory reports used memory relative to Linux's estimate of available memory.
func Memory() *Space {
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

// CPUTemperature returns the x86 package reading, in degrees Celsius.
func CPUTemperature() *float64 {
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

// UptimeSeconds reports the current boot's elapsed time.
func UptimeSeconds() *float64 { return firstNumber("/proc/uptime") }

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
