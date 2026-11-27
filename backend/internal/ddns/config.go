package ddns

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultConfigPath = "/etc/debian-router-b5-2840/ddns.json"
const DefaultStateDir = "/var/lib/debian-router-ddns"

type Config struct {
	Endpoint        string `json:"endpoint"`
	Record          string `json:"record"`
	Interface       string `json:"interface"`
	AddressURL      string `json:"address_url"`
	IntervalSeconds int    `json:"interval_seconds"`
}

var recordLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)

func safeHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		u.Fragment == "" && u.RawQuery == "" && !u.ForceQuery
}

func LoadConfig(path string) (Config, error) {
	data, err := readLimited(path, 8192)
	if err != nil {
		return Config{}, errors.New("cannot read DDNS config")
	}
	config := Config{IntervalSeconds: 300}
	data = bytes.TrimSpace(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) == 0 || data[0] != '{' || decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return Config{}, errors.New("invalid DDNS config JSON")
	}
	config.Record = strings.ToLower(strings.TrimSuffix(config.Record, "."))
	labels := strings.Split(config.Record, ".")
	if len(config.Record) > 253 || len(labels) < 2 {
		return Config{}, errors.New("record must be a fully qualified DNS name")
	}
	for _, label := range labels {
		if !recordLabel.MatchString(label) {
			return Config{}, errors.New("record must contain valid ASCII DNS labels")
		}
	}
	if !safeHTTPS(config.Endpoint) {
		return Config{}, errors.New("endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	if (config.Interface == "") == (config.AddressURL == "") {
		return Config{}, errors.New("configure exactly one of interface or address_url")
	}
	if config.Interface != "" && !interfaceName.MatchString(config.Interface) ||
		config.AddressURL != "" && !safeHTTPS(config.AddressURL) {
		return Config{}, errors.New("invalid DDNS address source")
	}
	if config.IntervalSeconds < 5 || config.IntervalSeconds > 3600 {
		return Config{}, errors.New("interval_seconds must be 5–3600")
	}
	return config, nil
}

func (c Config) identity() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{c.Endpoint, c.Record, c.Interface, c.AddressURL}, "\x00"))))
}

type Credentials struct {
	username string
	password string
}

func LoadCredentials(dir string) (Credentials, error) {
	if dir == "" {
		return Credentials{}, errors.New("DDNS systemd credentials are missing")
	}
	read := func(name string) (string, bool) {
		data, err := readLimited(filepath.Join(dir, name), 4096)
		value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		return value, err == nil && value != "" && !strings.ContainsAny(value, "\x00\r\n")
	}
	username, userOK := read("ddns-username")
	password, passOK := read("ddns-password")
	if !userOK || !passOK || strings.Contains(username, ":") {
		return Credentials{}, errors.New("DDNS requires a valid username and password credential pair")
	}
	return Credentials{username: username, password: password}, nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("file exceeds size limit")
	}
	return data, err
}
