package mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const DefaultConfigPath = "/etc/debian-router-b5-2840/mqtt.json"

type Config struct {
	Broker          string `json:"broker"`
	ClientID        string `json:"client_id"`
	TopicPrefix     string `json:"topic_prefix"`
	IntervalSeconds int    `json:"interval_seconds"`
	Discovery       bool   `json:"home_assistant_discovery"`
}

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,23}$`)
var topicPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9][A-Za-z0-9_-]*)*$`)

// LoadConfig reads connection settings. Account details are systemd credentials.
func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("cannot open MQTT config")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return Config{}, errors.New("cannot read MQTT config")
	}
	config := Config{IntervalSeconds: 30}
	data = bytes.TrimSpace(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) == 0 || data[0] != '{' ||
		decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return Config{}, errors.New("invalid MQTT config JSON")
	}
	broker, err := url.Parse(config.Broker)
	if err != nil || (broker.Scheme != "mqtt" && broker.Scheme != "mqtts") ||
		broker.User != nil || broker.Path != "" || broker.RawQuery != "" || broker.ForceQuery || broker.Fragment != "" {
		return Config{}, errors.New("broker must be mqtt://host:port or mqtts://host:port")
	}
	host, port, err := net.SplitHostPort(broker.Host)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || host == "" || portErr != nil || number == 0 {
		return Config{}, errors.New("broker must include a host and port")
	}
	if !clientIDPattern.MatchString(config.ClientID) {
		return Config{}, errors.New("client_id must contain 1–23 ASCII letters, digits, hyphens or underscores")
	}
	if len(config.TopicPrefix) > 128 || !topicPattern.MatchString(config.TopicPrefix) {
		return Config{}, errors.New("topic_prefix must contain slash-separated names without wildcards")
	}
	if config.IntervalSeconds < 5 || config.IntervalSeconds > 3600 {
		return Config{}, errors.New("interval_seconds must be 5–3600")
	}
	return config, nil
}

type Credentials struct {
	username string
	password string
}

// LoadCredentials accepts anonymous operation or a complete credential pair.
func LoadCredentials(dir string) (Credentials, error) {
	if dir == "" {
		return Credentials{}, nil
	}
	username, userMissing, userErr := readCredential(filepath.Join(dir, "mqtt-username"))
	password, passwordMissing, passwordErr := readCredential(filepath.Join(dir, "mqtt-password"))
	if userErr != nil || passwordErr != nil || userMissing != passwordMissing {
		return Credentials{}, errors.New("MQTT credentials must contain a valid username and password pair")
	}
	return Credentials{username: username, password: password}, nil
}

func readCredential(path string) (value string, missing bool, err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", false, err
	}
	value = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(data) > 4096 || value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", false, errors.New("invalid credential")
	}
	return value, false, nil
}
