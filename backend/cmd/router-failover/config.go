package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const defaultConfig = "/etc/debian-router-b5-2840/failover.json"
const proxyAddress = "127.0.0.1:11080"
const relayPort = 18084

type config struct {
	LAN       string       `json:"lan_interface"`
	WAN       string       `json:"wan_interface"`
	LANPrefix netip.Prefix `json:"lan_prefix"`
	Relay     netip.Addr   `json:"lan_address"`
	Target    netip.Addr   `json:"target_ip"`
	URL       string       `json:"probe_url"`
	CAFile    string       `json:"ca_file"`
	Seconds   int          `json:"interval_seconds"`
	probe     *url.URL
	tls       *tls.Config
}

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)
var dnsLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func loadConfig(path string) (config, error) {
	c := config{Seconds: 5}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	const maxSize = 16 * 1024
	body, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return c, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) > maxSize || decoder.Decode(&c) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return c, errors.New("invalid failover configuration JSON")
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	if c.CAFile != "" {
		ca, err := os.ReadFile(c.CAFile)
		if err != nil {
			return c, errors.New("cannot read failover CA file")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return c, errors.New("invalid failover CA file")
		}
		c.tls.RootCAs = roots
	}
	return c, nil
}

func (c *config) validate() error {
	if c.LAN == c.WAN || !interfaceName.MatchString(c.LAN) || !interfaceName.MatchString(c.WAN) ||
		c.LAN == "lo" || c.WAN == "lo" {
		return errors.New("failover needs distinct LAN and WAN interfaces")
	}
	if !c.LANPrefix.Addr().Is4() || c.LANPrefix != c.LANPrefix.Masked() ||
		c.LANPrefix.Bits() < 8 || !c.Relay.Is4() || !c.LANPrefix.Contains(c.Relay) ||
		!c.Relay.IsGlobalUnicast() || !c.Target.Is4() || !c.Target.IsGlobalUnicast() ||
		c.LANPrefix.Contains(c.Target) {
		return errors.New("failover needs a LAN IPv4 address and a target IPv4 outside the LAN")
	}
	if c.Seconds < 1 || c.Seconds > 60 {
		return errors.New("failover interval must be 1–60 seconds")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || !validDomain(u.Hostname()) || (u.Port() != "" && u.Port() != "443") {
		return errors.New("failover probe must be an HTTPS URL on port 443 without credentials, query or fragment")
	}
	c.probe = u
	c.tls = &tls.Config{MinVersion: tls.VersionTLS12}
	return nil
}

func validDomain(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func (c config) validateInterfaces() error {
	lan, err := net.InterfaceByName(c.LAN)
	if err != nil {
		return fmt.Errorf("failover LAN interface: %w", err)
	}
	if _, err := net.InterfaceByName(c.WAN); err != nil {
		return fmt.Errorf("failover WAN interface: %w", err)
	}
	addresses, err := lan.Addrs()
	if err != nil {
		return err
	}
	found := false
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil &&
			prefix.Addr() == c.Relay && prefix.Masked() == c.LANPrefix {
			found = true
		}
	}
	if !found {
		return errors.New("configured failover LAN address/prefix is absent")
	}
	local, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, address := range local {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr() == c.Target {
			return errors.New("failover target belongs to the router")
		}
	}
	return nil
}

func (c config) interval() time.Duration { return time.Duration(c.Seconds) * time.Second }
