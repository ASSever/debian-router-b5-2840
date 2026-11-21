package network

import (
	"context"
	"testing"
)

func TestDiagnoseWANSeparatesFailures(t *testing.T) {
	for _, test := range []struct {
		link, address, gateway, neighbour string
		dns, external                     bool
		want                              string
	}{
		{"no-carrier", "", "", "", false, false, "link_down"},
		{"carrier", "", "", "", false, false, "no_address"},
		{"carrier", "192.0.2.2/24", "", "", false, false, "gateway_failed"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "failed", true, false, "gateway_failed"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "failed", true, true, "gateway_failed"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "unverified", true, false, "gateway_unverified"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "ok", true, false, "external_failed"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "ok", false, true, "dns_failed"},
		{"carrier", "192.0.2.2/24", "192.0.2.1", "unverified", true, true, "online"},
	} {
		t.Run(test.want, func(t *testing.T) {
			state := ActiveNetworkStatus{LinkState: test.link, Addresses: []string{test.address}, DNS: []string{"192.0.2.1"}}
			got := diagnoseWAN(context.Background(), "enp1s0", test.gateway, state, wanProbes{
				func(context.Context, string, string) string { return test.neighbour },
				func(context.Context, string, string) bool { return test.dns },
				func(context.Context, string) bool { return test.external },
			})
			if got.Result != test.want {
				t.Fatalf("result %q, want %q", got.Result, test.want)
			}
		})
	}
}
