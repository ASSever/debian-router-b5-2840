package monitor

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	OK              = "ok"
	DNSFailed       = "dns_failed"
	TransportFailed = "transport_failed"
)

type Sample struct {
	Target     string    `json:"target"`
	CheckedAt  time.Time `json:"checked_at"`
	Result     string    `json:"result"`
	DNS        string    `json:"dns"`
	Transport  string    `json:"transport"`
	DurationMS int64     `json:"duration_ms"`
}

type Prober struct {
	resolve func(context.Context, string, string) ([]netip.Addr, error)
	timeout time.Duration
}

func NewProber(cfg Config) Prober {
	resolver := &net.Resolver{PreferGo: true}
	if cfg.DNSServer != "" {
		resolver.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, cfg.DNSServer)
		}
	}
	return Prober{resolve: resolver.LookupNetIP, timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}
}

func (p Prober) CheckAll(ctx context.Context, targets []Target) []Sample {
	var workers sync.WaitGroup
	results := make([]Sample, len(targets))
	for i, target := range targets {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[i] = p.check(ctx, target)
		}()
	}
	workers.Wait()
	return results
}

func (p Prober) check(ctx context.Context, target Target) (sample Sample) {
	started := time.Now()
	sample = Sample{Target: target.Name, CheckedAt: started.UTC(), Transport: "skipped"}
	defer func() { sample.DurationMS = time.Since(started).Milliseconds() }()

	ip, err := netip.ParseAddr(target.host)
	addresses := []netip.Addr{ip}
	if err == nil {
		sample.DNS = "not_required"
	} else {
		dnsCtx, cancel := context.WithTimeout(ctx, p.timeout)
		addresses, err = p.resolve(dnsCtx, "ip", target.host)
		cancel()
		if err != nil || len(addresses) == 0 {
			sample.DNS = "failed"
			sample.Result = DNSFailed
			return sample
		}
		sample.DNS = OK
	}

	sample.Result = TransportFailed
	sample.Transport = "failed"
	transportCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	deadline, _ := transportCtx.Deadline()
	var dialer net.Dialer
	for i, address := range addresses {
		// Reserve some of the transport budget for each remaining address.
		budget := time.Until(deadline) / time.Duration(len(addresses)-i)
		if budget <= 0 || transportCtx.Err() != nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(transportCtx, budget)
		conn, err := dialer.DialContext(attemptCtx, "tcp", net.JoinHostPort(address.String(), target.port))
		cancel()
		if err == nil {
			conn.Close()
			sample.Transport = OK
			sample.Result = OK
			break
		}
	}
	return sample
}
