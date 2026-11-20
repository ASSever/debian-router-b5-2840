package network

import (
	"errors"
	"testing"
)

func TestValidateStaticWAN(t *testing.T) {
	valid := StaticWANConfig{
		Address: "192.168.1.104/24",
		Gateway: "192.168.1.1",
		DNS:     []string{"192.168.1.1"},
	}
	if err := ValidateStaticWAN(valid); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}

	tests := []struct {
		name   string
		config StaticWANConfig
		want   error
	}{
		{name: "bad address", config: StaticWANConfig{Address: "not-an-ip/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANAddress},
		{name: "network address", config: StaticWANConfig{Address: "192.168.1.0/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANAddress},
		{name: "broadcast address", config: StaticWANConfig{Address: "192.168.1.255/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANAddress},
		{name: "unsupported prefix", config: StaticWANConfig{Address: "192.168.1.104/31", Gateway: "192.168.1.105", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANAddress},
		{name: "gateway outside subnet", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.2.1", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANGateway},
		{name: "gateway is network address", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.0", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANGateway},
		{name: "gateway is broadcast address", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.255", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANGateway},
		{name: "gateway is local address", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.104", DNS: []string{"192.168.1.1"}}, want: ErrStaticWANGateway},
		{name: "missing DNS", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.1"}, want: ErrStaticWANDNS},
		{name: "IPv6 DNS", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"2001:db8::1"}}, want: ErrStaticWANDNS},
		{name: "duplicate DNS", config: StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1", "192.168.1.1"}}, want: ErrStaticWANDNS},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateStaticWAN(test.config)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateStaticWAN() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRenderStaticWANUsesRouteMetricsAndOnlyTargetsWAN(t *testing.T) {
	config := StaticWANConfig{
		Address: "192.168.1.104/24",
		Gateway: "192.168.1.1",
		DNS:     []string{"192.168.1.1", "1.1.1.1"},
	}
	got, err := RenderStaticWAN(config)
	if err != nil {
		t.Fatalf("RenderStaticWAN() error = %v", err)
	}
	want := "[Match]\n" +
		"Name=enp1s0\n\n" +
		"[Network]\n" +
		"DHCP=no\n" +
		"LinkLocalAddressing=no\n" +
		"IPv6AcceptRA=no\n" +
		"DNS=192.168.1.1\n" +
		"DNS=1.1.1.1\n\n" +
		"[Address]\n" +
		"Address=192.168.1.104/24\n" +
		"RouteMetric=5000\n\n" +
		"[Route]\n" +
		"Gateway=192.168.1.1\n" +
		"Metric=5000\n"
	if string(got) != want {
		t.Fatalf("RenderStaticWAN() = %q, want %q", got, want)
	}
}

func TestRenderStaticWANRejectsInvalidProfile(t *testing.T) {
	_, err := RenderStaticWAN(StaticWANConfig{
		Address: "192.168.1.104/24",
		Gateway: "192.168.2.1",
		DNS:     []string{"192.168.1.1"},
	})
	if !errors.Is(err, ErrStaticWANGateway) {
		t.Fatalf("RenderStaticWAN() error = %v, want invalid gateway", err)
	}
}
