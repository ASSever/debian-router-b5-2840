package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const probeTimeout = 4 * time.Second

func (c config) probePaths(ctx context.Context) (bool, bool) {
	primary := make(chan bool, 1)
	go func() {
		primary <- c.zapretReady(ctx) && c.healthy(ctx, c.dialPrimary)
	}()
	reserve := c.healthy(ctx, c.dialProxy)
	return <-primary, reserve
}

func (c config) zapretReady(ctx context.Context) bool {
	queue, err := os.ReadFile("/proc/net/netfilter/nfnetlink_queue")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(queue), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "62232" {
			check, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			rules, err := exec.CommandContext(check, "/usr/sbin/nft", "list", "table", "inet", "b5_zapret").Output()
			return err == nil &&
				bytes.Contains(rules, []byte("chain mark_health_probe")) &&
				bytes.Contains(rules, []byte(fmt.Sprintf("iifname %q oifname %q", c.LAN, c.WAN)))
		}
	}
	return false
}

func (c config) dialPrimary(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Control: bindPrimary(c.WAN)}
	return dialer.DialContext(ctx, "tcp4", netip.AddrPortFrom(c.Target, 443).String())
}

func (c config) healthy(ctx context.Context, dial func(context.Context) (net.Conn, error)) bool {
	transport := &http.Transport{
		DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		TLSClientConfig: c.tls, DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.probe.String(), nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

type tunnelConn struct {
	net.Conn
	reader io.Reader
}

func (c tunnelConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c tunnelConn) CloseWrite() error          { return c.Conn.(*net.TCPConn).CloseWrite() }

func (c config) dialProxy(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp4", proxyAddress)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	authority := net.JoinHostPort(c.probe.Hostname(), "443")
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: authority}, Host: authority}
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(io.LimitReader(conn, 16*1024))
	res, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("sing-box rejected CONNECT")
	}
	buffered, err := reader.Peek(reader.Buffered())
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	keep = true
	return tunnelConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(buffered), conn)}, nil
}

func (c config) apply(ctx context.Context, proxy bool) error {
	// Replacing only our table in one batch makes a rejected change leave the
	// previous mode in place. The input guard also covers existing DNAT flows.
	rules := fmt.Sprintf(`destroy table ip b5_failover
table ip b5_failover {
 chain relay_guard {
  type filter hook input priority -5; policy accept;
  ip daddr %[2]s tcp dport %[4]d iifname %[1]q ip saddr %[3]s ct status dnat ct original ip daddr %[5]s ct original proto-dst 443 accept
  ip daddr %[2]s tcp dport %[4]d drop
 }
 chain steer {
  type nat hook prerouting priority -105; policy accept;
`, c.LAN, c.Relay, c.LANPrefix, relayPort, c.Target)
	if proxy {
		rules += fmt.Sprintf("  iifname %q ip saddr %s ip daddr %s fib daddr type != local tcp dport 443 counter dnat to %s:%d\n",
			c.LAN, c.LANPrefix, c.Target, c.Relay, relayPort)
	}
	rules += " }\n}\n"
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply failover nftables table: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (c config) listen() (*net.TCPListener, error) {
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IP(c.Relay.AsSlice()), Port: relayPort})
}

func clearRules() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exec.CommandContext(ctx, "/usr/sbin/nft", "destroy", "table", "ip", "b5_failover").Run()
}

func (c config) relay(ctx context.Context, client *net.TCPConn) {
	defer client.Close()
	source := client.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap()
	if !c.LANPrefix.Contains(source) || originalDestination(client) != netip.AddrPortFrom(c.Target, 443) {
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	reserve, err := c.dialProxy(dialCtx)
	cancel()
	if err != nil {
		return
	}
	defer reserve.Close()
	stop := context.AfterFunc(ctx, func() { client.Close(); reserve.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Minute)
	client.SetDeadline(deadline)
	reserve.SetDeadline(deadline)
	done := make(chan struct{})
	proxy := reserve.(tunnelConn)
	go func() {
		defer close(done)
		io.Copy(proxy, client)
		proxy.CloseWrite()
	}()
	io.Copy(client, proxy)
	client.CloseWrite()
	<-done
}

func modeName(proxy bool) string {
	if proxy {
		return "proxy"
	}
	return "zapret"
}
