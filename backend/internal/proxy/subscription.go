package proxy

import (
	"encoding/base64"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxSubscriptionBytes = 256 << 10

const (
	maxEntries   = 256
	maxLineBytes = 8 << 10
)

var (
	ErrInvalidSubscription  = errors.New("invalid_subscription")
	ErrSubscriptionTooLarge = errors.New("subscription_too_large")
	uuidPattern             = regexp.MustCompile(`(?i)^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
)

// Row contains only diagnostics that are safe to send to the browser.
type Row struct {
	Line     int    `json:"line"`
	Protocol string `json:"protocol,omitempty"`
	Error    string `json:"error,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Report describes parse results. Import does not attempt a connection to any server.
type Report struct {
	Imported int   `json:"imported"`
	Rejected int   `json:"rejected"`
	Rows     []Row `json:"rows"`
}

// Server holds private settings for later connection checks. It must never be logged or sent to the browser.
type Server struct {
	Line       int               `json:"line"`
	Protocol   string            `json:"protocol"`
	Host       string            `json:"host"`
	Port       uint16            `json:"port"`
	Credential string            `json:"credential"`
	Options    map[string]string `json:"options,omitempty"`
}

type Subscription struct {
	Report  Report   `json:"report"`
	Servers []Server `json:"servers"`
}

// Parse reads a plain or Base64-encoded URI list and keeps failures isolated to their lines.
func Parse(source string) (Subscription, error) {
	if len(source) > MaxSubscriptionBytes {
		return Subscription{}, ErrSubscriptionTooLarge
	}
	content := strings.TrimPrefix(source, "\ufeff")
	if !strings.Contains(content, "://") {
		content = decodeList(content)
	}
	if content == "" || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return Subscription{}, ErrInvalidSubscription
	}

	result := Subscription{Report: Report{Rows: []Row{}}}
	for i, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(result.Report.Rows) == maxEntries {
			return Subscription{}, ErrSubscriptionTooLarge
		}
		row, server := parseLine(i+1, line)
		result.Report.Rows = append(result.Report.Rows, row)
		if row.Error == "" {
			result.Report.Imported++
			result.Servers = append(result.Servers, server)
		} else {
			result.Report.Rejected++
		}
	}
	if len(result.Report.Rows) == 0 {
		return Subscription{}, ErrInvalidSubscription
	}
	return result, nil
}

func decodeList(content string) string {
	compact := strings.Join(strings.Fields(content), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.Strict().DecodeString(compact); err == nil {
			return strings.TrimPrefix(string(decoded), "\ufeff")
		}
	}
	return ""
}

func parseLine(number int, line string) (Row, Server) {
	row := Row{Line: number}
	if len(line) > maxLineBytes {
		row.Error = "line_too_long"
		return row, Server{}
	}
	scheme, _, hasScheme := strings.Cut(line, "://")
	switch strings.ToLower(scheme) {
	case "vless":
		row.Protocol = "vless"
	case "hysteria2", "hy2":
		row.Protocol = "hysteria2"
	default:
		row.Error = "invalid_server_uri"
		if hasScheme {
			row.Error = "unsupported_protocol"
		}
		return row, Server{}
	}
	parsed, err := url.Parse(line)
	if !hasScheme || err != nil || !validHost(parsed.Hostname()) || (parsed.Path != "" && parsed.Path != "/") || parsed.User == nil {
		row.Error = "invalid_server_uri"
		return row, Server{}
	}
	port := parsed.Port()
	if row.Protocol == "hysteria2" && port == "" && !strings.HasSuffix(parsed.Host, ":") {
		port = "443"
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		row.Error = "invalid_server_port"
		return row, Server{}
	}

	credential := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	if row.Protocol == "vless" && (hasPassword || !uuidPattern.MatchString(credential)) {
		row.Error = "invalid_server_credentials"
		return row, Server{}
	}
	if row.Protocol == "hysteria2" && hasPassword {
		credential += ":" + password
	}
	if strings.TrimSpace(credential) == "" || strings.ContainsFunc(credential, unicode.IsControl) {
		row.Error = "invalid_server_credentials"
		return row, Server{}
	}
	options, warning, problem := parseOptions(row.Protocol, parsed.RawQuery)
	row.Error, row.Warning = problem, warning
	return row, Server{
		Line: number, Protocol: row.Protocol, Host: parsed.Hostname(), Port: uint16(portNumber),
		Credential: credential, Options: options,
	}
}

func validHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Zone() == ""
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func parseOptions(protocol, rawQuery string) (map[string]string, string, string) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, "", "invalid_server_options"
	}
	options := make(map[string]string, len(values))
	for key, items := range values {
		if len(items) != 1 || !utf8.ValidString(items[0]) || strings.ContainsFunc(items[0], unicode.IsControl) {
			return nil, "", "invalid_server_options"
		}
		if !knownOption(protocol, key) {
			return nil, "", "unsupported_server_options"
		}
		options[key] = items[0]
	}
	if sni := options["sni"]; sni != "" && !validHost(sni) {
		return nil, "", "invalid_server_options"
	}
	if protocol == "vless" {
		if !oneOf(options["type"], "", "tcp", "raw") || options["security"] != "tls" ||
			!oneOf(options["encryption"], "", "none") || !oneOf(options["flow"], "", "xtls-rprx-vision") {
			return nil, "", "unsupported_server_options"
		}
		return options, "", ""
	}
	if !oneOf(options["insecure"], "", "0", "1") || !oneOf(options["obfs"], "", "salamander", "gecko") {
		return nil, "", "unsupported_server_options"
	}
	if (options["obfs"] == "") != (options["obfs-password"] == "") {
		return nil, "", "invalid_server_options"
	}
	if options["fm"] != "" {
		return options, "provider_options_unverified", ""
	}
	return options, "", ""
}

func knownOption(protocol, key string) bool {
	if key == "sni" || key == "alpn" {
		return true
	}
	if protocol == "vless" {
		return oneOf(key, "type", "security", "encryption", "flow", "fp")
	}
	return oneOf(key, "insecure", "obfs", "obfs-password", "fm")
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
