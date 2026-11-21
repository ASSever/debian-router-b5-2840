package network

import (
	"context"
	"net/netip"
	"time"
)

type WANDiagnostic struct {
	Interface string     `json:"interface"`
	Address   string     `json:"address,omitempty"`
	Gateway   string     `json:"gateway,omitempty"`
	Resolver  string     `json:"resolver,omitempty"`
	Result    string     `json:"result"`
	Checks    []WANCheck `json:"checks"`
}

type WANCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type wanProbes struct {
	gateway  func(context.Context, string, string) string
	dns      func(context.Context, string, string) bool
	external func(context.Context, string) bool
}

func diagnoseWAN(ctx context.Context, device, gateway string, status ActiveNetworkStatus, probes wanProbes) WANDiagnostic {
	report := WANDiagnostic{Interface: device}
	finish := func(result string, checks ...WANCheck) WANDiagnostic {
		report.Result = result
		report.Checks = append(report.Checks, checks...)
		return report
	}
	switch status.LinkState {
	case "carrier", "degraded-carrier":
	case "no-carrier", "off", "dormant", "lower-layer-down":
		return finish("link_down", WANCheck{"link", "failed"})
	default:
		return finish("link_unknown", WANCheck{"link", "unverified"})
	}
	report.Checks = append(report.Checks, WANCheck{"link", "ok"})
	for _, raw := range status.Addresses {
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Addr().Is4() && isUsableIPv4(prefix.Addr()) {
			report.Address = raw
			break
		}
	}
	if report.Address == "" {
		return finish("no_address", WANCheck{"address", "failed"})
	}
	report.Checks = append(report.Checks, WANCheck{"address", "ok"})
	report.Gateway = gateway
	if gateway == "" {
		return finish("gateway_failed", WANCheck{"gateway", "failed"})
	}

	gatewayStatus := probes.gateway(ctx, device, gateway)
	type dnsResult struct {
		server string
		ok     bool
	}
	dns := make(chan dnsResult, 1)
	external := make(chan bool, 1)
	go func() {
		dnsCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		for _, server := range status.DNS {
			target, err := netip.ParseAddr(server)
			if err == nil && target.Is4() && isUsableIPv4(target) && probes.dns(dnsCtx, device, server) {
				dns <- dnsResult{server, true}
				return
			}
		}
		dns <- dnsResult{}
	}()
	go func() { external <- probes.external(ctx, device) }()
	resolved, online := <-dns, <-external
	report.Resolver = resolved.server
	checks := []WANCheck{
		{"gateway", gatewayStatus}, {"dns", wanProbeStatus(resolved.ok)}, {"external", wanProbeStatus(online)},
	}
	switch {
	case gatewayStatus == "failed":
		return finish("gateway_failed", checks...)
	case gatewayStatus == "unverified" && !online:
		return finish("gateway_unverified", checks...)
	case !online:
		return finish("external_failed", checks...)
	case !resolved.ok:
		return finish("dns_failed", checks...)
	default:
		return finish("online", checks...)
	}
}

func wanProbeStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}
