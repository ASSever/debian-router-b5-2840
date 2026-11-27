package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var serialName = regexp.MustCompile(`^tty(S|USB|ACM)[0-9]+$`)

type serialPort struct {
	path  string
	kind  string
	sysfs string
}

func listPorts() ([]serialPort, error) {
	const ttyClass = "/sys/class/tty"
	entries, err := os.ReadDir(ttyClass)
	if err != nil {
		return nil, err
	}
	var ports []serialPort
	for _, entry := range entries {
		name := entry.Name()
		if !serialName.MatchString(name) {
			continue
		}
		path := filepath.Join("/dev", name)
		node, err := os.Lstat(path)
		if err != nil || node.Mode()&os.ModeCharDevice == 0 {
			continue
		}
		base := filepath.Join(ttyClass, name)
		device, err := filepath.EvalSymlinks(filepath.Join(base, "device"))
		if err != nil {
			continue
		}
		kind := "USB"
		if strings.HasPrefix(name, "ttyS") {
			driverType, err := os.ReadFile(filepath.Join(base, "type"))
			value := strings.TrimSpace(string(driverType))
			if err != nil || value == "0" || value == "" {
				continue
			}
			kind = "UART"
		}
		ports = append(ports, serialPort{path: path, kind: kind, sysfs: device})
	}
	return ports, nil
}

func checkPort(path string) error {
	ports, err := listPorts()
	if err != nil {
		return err
	}
	for _, port := range ports {
		if port.path != path {
			continue
		}
		consoles, err := os.ReadFile("/sys/class/tty/console/active")
		if err != nil {
			return fmt.Errorf("check active consoles: %w", err)
		}
		for _, console := range strings.Fields(string(consoles)) {
			if console == filepath.Base(path) {
				return fmt.Errorf("%s is an active system console", path)
			}
		}
		return nil
	}
	return fmt.Errorf("%s is not an available serial device from list", path)
}
