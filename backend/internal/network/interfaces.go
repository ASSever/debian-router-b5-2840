package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const sysfsInterfacesDirectory = "/sys/class/net"

// Interface contains a small, read-only view of a Linux network interface.
type Interface struct {
	Name             string `json:"name"`
	OperationalState string `json:"operational_state"`
	Carrier          *bool  `json:"carrier"`
	SpeedMbps        *int   `json:"speed_mbps"`
}

// ReadInterfaces reads link metadata from the kernel's network-interface sysfs tree.
func ReadInterfaces() ([]Interface, error) {
	return ReadInterfacesFrom(sysfsInterfacesDirectory)
}

// ReadInterfacesFrom reads only fixed link metadata files below root.
func ReadInterfacesFrom(root string) ([]Interface, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read network interface directory: %w", err)
	}

	interfaces := make([]Interface, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}

		interfacePath := filepath.Join(root, name)
		interfaces = append(interfaces, Interface{
			Name:             name,
			OperationalState: readOperationalState(filepath.Join(interfacePath, "operstate")),
			Carrier:          readCarrier(filepath.Join(interfacePath, "carrier")),
			SpeedMbps:        readSpeed(filepath.Join(interfacePath, "speed")),
		})
	}
	return interfaces, nil
}

func readOperationalState(path string) string {
	state := strings.TrimSpace(readFile(path))
	if state == "" {
		return "unknown"
	}
	return state
}

func readCarrier(path string) *bool {
	switch strings.TrimSpace(readFile(path)) {
	case "0":
		value := false
		return &value
	case "1":
		value := true
		return &value
	default:
		return nil
	}
}

func readSpeed(path string) *int {
	speed, err := strconv.Atoi(strings.TrimSpace(readFile(path)))
	if err != nil || speed <= 0 {
		return nil
	}
	return &speed
}

func readFile(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(contents)
}
