package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const DefaultConfigPath = "/etc/debian-router-b5-2840/monitor.json"

type Config struct {
	IntervalSeconds int      `json:"interval_seconds"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	DNSServer       string   `json:"dns_server"`
	Targets         []Target `json:"targets"`
}

type Target struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	host    string
	port    string
}

var targetName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var dnsLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func LoadConfig(path string) (Config, error) {
	cfg := Config{IntervalSeconds: 60, TimeoutSeconds: 5}
	err := readConfig(path, &cfg)
	if errors.Is(err, os.ErrNotExist) && path == DefaultConfigPath {
		cfg.Targets = []Target{
			{Name: "internet", Address: "github.com:443"},
			{Name: "sing-box-listener", Address: "127.0.0.1:11080"},
		}
	} else if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func readConfig(path string, cfg *Config) error {
	const maxSize = 64 * 1024
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return err
	}
	if len(body) > maxSize {
		return errors.New("monitor configuration exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return errors.New("invalid monitor configuration JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("monitor configuration must contain one JSON object")
	}
	return nil
}

func (c *Config) validate() error {
	if c.IntervalSeconds < 1 || c.IntervalSeconds > 3600 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 30 {
		return errors.New("monitor interval must be 1–3600 seconds and timeout 1–30 seconds")
	}
	if c.DNSServer != "" {
		server, err := netip.ParseAddrPort(c.DNSServer)
		if err != nil || server.Port() == 0 {
			return errors.New("dns_server must be an IP address with a nonzero port")
		}
	}
	if len(c.Targets) < 1 || len(c.Targets) > 16 {
		return errors.New("monitor configuration needs 1–16 targets")
	}
	seen := make(map[string]bool, len(c.Targets))
	for i := range c.Targets {
		target := &c.Targets[i]
		if !targetName.MatchString(target.Name) || seen[target.Name] {
			return fmt.Errorf("target %d needs a unique name using letters, digits, hyphens or underscores", i+1)
		}
		seen[target.Name] = true
		host, port, err := net.SplitHostPort(target.Address)
		number, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || portErr != nil || number == 0 || !validHost(host) {
			return fmt.Errorf("target %d needs a DNS name or IP address with a nonzero port", i+1)
		}
		target.host = host
		target.port = strconv.FormatUint(number, 10)
	}
	return nil
}

func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}
