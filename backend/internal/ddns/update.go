package ddns

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type updater struct {
	config Config
	creds  Credentials
	client *http.Client
}

func newUpdater(config Config, creds Credentials) *updater {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &updater{config: config, creds: creds, client: &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (u *updater) address(ctx context.Context) (netip.Addr, error) {
	if u.config.AddressURL != "" {
		body, status, err := u.get(ctx, u.config.AddressURL, false)
		ip, parseErr := netip.ParseAddr(strings.TrimSpace(body))
		if err != nil || status != http.StatusOK || parseErr != nil || !usable(ip) {
			return netip.Addr{}, errors.New("address_source_error")
		}
		return ip, nil
	}
	link, err := net.InterfaceByName(u.config.Interface)
	if err != nil || link.Flags&net.FlagUp == 0 {
		return netip.Addr{}, errors.New("interface_unavailable")
	}
	addresses, err := link.Addrs()
	if err != nil {
		return netip.Addr{}, errors.New("interface_unavailable")
	}
	var selected netip.Addr
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil || !usable(prefix.Addr()) {
			continue
		}
		if selected.IsValid() && selected != prefix.Addr() {
			return netip.Addr{}, errors.New("multiple_interface_addresses")
		}
		selected = prefix.Addr()
	}
	if !selected.IsValid() {
		return netip.Addr{}, errors.New("no_interface_address")
	}
	return selected, nil
}

func usable(address netip.Addr) bool { return address.Is4() && address.IsGlobalUnicast() }

func (u *updater) get(ctx context.Context, endpoint string, authenticated bool) (string, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("User-Agent", "Debian-Router-B5-2840-DDNS/1.0")
	if authenticated {
		request.SetBasicAuth(u.creds.username, u.creds.password)
	}
	response, err := u.client.Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	if err != nil || len(body) > 1024 {
		return "", response.StatusCode, errors.New("response cannot be read")
	}
	return string(body), response.StatusCode, nil
}

func (u *updater) update(ctx context.Context, address netip.Addr) (code string, cooldown bool) {
	endpoint, _ := url.Parse(u.config.Endpoint)
	query := url.Values{"hostname": {u.config.Record}, "myip": {address.String()}}
	endpoint.RawQuery = query.Encode()
	body, status, err := u.get(ctx, endpoint.String(), true)
	switch status {
	case http.StatusTooManyRequests:
		return "rate_limited", true
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_failed", false
	case http.StatusInternalServerError:
		return "provider_unavailable", true
	}
	if err != nil {
		return "provider_transport_error", false
	}
	if status != http.StatusOK {
		return "provider_http_error", false
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return "invalid_provider_response", false
	}
	switch fields[0] {
	case "good", "nochg":
		if len(fields) > 2 || len(fields) == 2 && fields[1] != address.String() {
			return "invalid_provider_response", false
		}
		return fields[0], false
	case "911", "dnserr":
		return "provider_unavailable", true
	case "badauth":
		return "authentication_failed", false
	case "nohost", "notfqdn", "abuse", "badagent", "!donator", "numhost":
		return "provider_rejected", false
	default:
		return "invalid_provider_response", false
	}
}
