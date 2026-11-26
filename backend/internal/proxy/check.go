package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const CheckURL = "https://www.gstatic.com/generate_204"

// CheckResult describes a single HTTPS request made through the selected server.
type CheckResult struct {
	Line      int    `json:"line"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// CheckServer uses a private sing-box process with one outbound and no direct fallback.
func CheckServer(ctx context.Context, binary string, server Server) CheckResult {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	reason := checkServer(ctx, binary, server)
	switch {
	case reason == "":
		return CheckResult{Line: server.Line, Status: "passed", ElapsedMS: time.Since(started).Milliseconds()}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		reason = "timed_out"
	case errors.Is(ctx.Err(), context.Canceled):
		reason = "canceled"
	}
	return CheckResult{Line: server.Line, Status: "failed", Reason: reason, ElapsedMS: time.Since(started).Milliseconds()}
}

func checkServer(ctx context.Context, binary string, server Server) string {
	outbound, ok := checkOutbound(server)
	if !ok {
		return "unsupported_options"
	}
	ip, reason := resolveCheckAddress(ctx, server.Host)
	if reason != "" {
		return reason
	}
	outbound["server"] = ip.String()
	return checkWorker(ctx, binary, outbound, CheckURL, nil, http.StatusNoContent)
}

func checkWorker(ctx context.Context, binary string, outbound map[string]any, target string, trust *tls.Config, expected int) string {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "worker_unavailable"
	}
	defer listener.Close()
	local := listener.Addr().(*net.TCPAddr)
	password := rand.Text()
	config, err := json.Marshal(map[string]any{
		"log": map[string]any{"disabled": true},
		"dns": map[string]any{"servers": []map[string]string{{"type": "local", "tag": "local-dns"}}},
		"inbounds": []map[string]any{{
			"type": "http", "listen": "127.0.0.1", "listen_port": local.Port,
			"users": []map[string]string{{"username": "check", "password": password}},
		}},
		"outbounds": []map[string]any{outbound},
		"route":     map[string]string{"final": "selected", "default_domain_resolver": "local-dns"},
	})
	if err != nil || binary == "" {
		return "worker_unavailable"
	}
	validate := commandWithConfig(ctx, binary, "check", config)
	if err := validate.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "config_rejected"
		}
		return "worker_unavailable"
	}
	worker := commandWithConfig(ctx, binary, "run", config)
	// Keep the port reserved until immediately before starting the authenticated listener.
	if err := listener.Close(); err != nil {
		return "worker_unavailable"
	}
	if err := worker.Start(); err != nil {
		return "worker_unavailable"
	}
	done := make(chan struct{})
	go func() {
		_ = worker.Wait()
		close(done)
	}()
	defer func() {
		_ = worker.Process.Kill()
		<-done
	}()
	if !waitForWorker(ctx, local.String(), done) {
		return "worker_start_failed"
	}
	return requestThroughWorker(ctx, local.String(), password, target, trust, expected)
}

func commandWithConfig(ctx context.Context, binary, action string, config []byte) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, action, "-c", "/dev/stdin")
	cmd.Stdin = bytes.NewReader(config)
	// Neither arguments nor captured diagnostics should contain provider credentials.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	return cmd
}

func waitForWorker(ctx context.Context, address string, done <-chan struct{}) bool {
	startup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	dialer := net.Dialer{Timeout: 120 * time.Millisecond}
	for {
		select {
		case <-done:
			return false
		case <-startup.Done():
			return false
		case <-ticker.C:
			if conn, err := dialer.DialContext(startup, "tcp4", address); err == nil {
				conn.Close()
				return true
			}
		}
	}
}

func requestThroughWorker(ctx context.Context, address, password, target string, trust *tls.Config, expected int) string {
	proxyURL := &url.URL{Scheme: "http", Host: address, User: url.UserPassword("check", password)}
	transport := &http.Transport{
		Proxy:                  http.ProxyURL(proxyURL),
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 16 << 10,
		TLSClientConfig:        trust,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "worker_unavailable"
	}
	response, err := client.Do(request)
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return "timed_out"
		}
		return "proxy_request_failed"
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		return "unexpected_http_status"
	}
	return ""
}

func resolveCheckAddress(ctx context.Context, host string) (netip.Addr, string) {
	lookup, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(lookup, "ip", host)
	if err != nil || len(addresses) == 0 {
		return netip.Addr{}, "server_dns_failed"
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return netip.Addr{}, "nonpublic_destination"
		}
	}
	// Prefer IPv4 when the provider publishes both families. B5 may have no IPv6 route.
	for _, ip := range addresses {
		if ip.Unmap().Is4() {
			return ip.Unmap(), ""
		}
	}
	return addresses[0], ""
}

func checkOutbound(server Server) (map[string]any, bool) {
	options := server.Options
	name := options["sni"]
	if name == "" {
		name = server.Host
	}
	tls := map[string]any{"enabled": true, "server_name": name}
	if alpn := options["alpn"]; alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	outbound := map[string]any{
		"type": server.Protocol, "tag": "selected", "server_port": server.Port, "tls": tls,
	}
	switch server.Protocol {
	case "vless":
		outbound["uuid"] = server.Credential
		if flow := options["flow"]; flow != "" {
			outbound["flow"] = flow
		}
		if fingerprint := options["fp"]; !oneOf(fingerprint, "", "none") {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
		}
	case "hysteria2":
		if options["insecure"] == "1" || !supportedQUICOptions(options["fm"]) {
			return nil, false
		}
		outbound["password"] = server.Credential
		if obfs := options["obfs"]; obfs != "" {
			outbound["obfs"] = map[string]string{"type": obfs, "password": options["obfs-password"]}
		}
	default:
		return nil, false
	}
	return outbound, true
}

// The provider's default BBR settings already match sing-box. Reject unknown settings instead of dropping them.
func supportedQUICOptions(raw string) bool {
	if raw == "" {
		return true
	}
	var options struct {
		QUICParams struct {
			Debug      bool   `json:"debug"`
			Congestion string `json:"congestion"`
		} `json:"quicParams"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF) && !options.QUICParams.Debug &&
		oneOf(options.QUICParams.Congestion, "", "bbr")
}
