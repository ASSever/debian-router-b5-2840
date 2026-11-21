package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

var (
	ErrInvalidURL       = errors.New("invalid_subscription_url")
	ErrFetchFailed      = errors.New("subscription_fetch_failed")
	errLocalDestination = errors.New("subscription destination is not public")
	publicIPv6          = netip.MustParsePrefix("2000::/3")
	reservedRanges      = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
	}
)

// Fetch downloads one public HTTPS resource. Its errors never include the URL, response or network error text.
func Fetch(ctx context.Context, source string) (string, error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" ||
		!validHost(parsed.Hostname()) || !oneOf(parsed.Port(), "", "443") {
		return "", ErrInvalidURL
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil && !publicAddress(ip) {
		return "", ErrInvalidURL
	}
	transport := &http.Transport{
		DialContext:            dialPublicHTTPS,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   12 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", ErrInvalidURL
	}
	request.Header.Set("User-Agent", "sing-box")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errLocalDestination) {
			return "", ErrInvalidURL
		}
		return "", ErrFetchFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", ErrFetchFailed
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxSubscriptionBytes+1))
	if err != nil {
		return "", ErrFetchFailed
	}
	if len(body) > MaxSubscriptionBytes {
		return "", ErrSubscriptionTooLarge
	}
	return string(body), nil
}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.Zone() != "" ||
		(ip.Is6() && !publicIPv6.Contains(ip)) {
		return false
	}
	for _, reserved := range reservedRanges {
		if reserved.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicHTTPS(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, errLocalDestination
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, ErrFetchFailed
	}
	// Dial the validated IPs directly so a second DNS lookup cannot change the destination.
	for _, ip := range ips {
		if !publicAddress(ip) {
			return nil, errLocalDestination
		}
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	for _, ip := range ips {
		if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port)); err == nil {
			return conn, nil
		}
	}
	return nil, ErrFetchFailed
}
