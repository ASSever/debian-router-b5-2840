package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMixedSubscription(t *testing.T) {
	plain := "# Provider\r\n\r\nvless://12ecc646-b0c1-4e87-b094-6d5cf3d12189@vless.example:443?security=tls&type=tcp&encryption=none&flow=xtls-rprx-vision&fp=firefox&alpn=h2%2Chttp%2F1.1#PrivateLabel\n" +
		"hy2://test-user:p%40ss@[2001:db8::2]?sni=hy.example&fm=%7B%22quicParams%22%3A%7B%7D%7D\n" +
		"vless://private-invalid-credential@vless.example:443?security=tls\n" +
		"trojan://other-private-credential@trojan.example:443\n" +
		"hysteria2://private-auth@hy.example:443?obfs=salamander\n" +
		"hy2://private-auth@hy.example:0\n" +
		"hy2://private-auth@hy.example:443?sni=one.example&sni=two.example\n" +
		"malformed text"
	for name, input := range map[string]string{
		"plain":        "\ufeff" + plain,
		"standard":     base64.StdEncoding.EncodeToString([]byte(plain)),
		"unpadded URL": base64.RawURLEncoding.EncodeToString([]byte(plain)),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Parse(input)
			if err != nil || result.Report.Imported != 2 || result.Report.Rejected != 6 {
				t.Fatalf("unexpected parse result: imported=%d rejected=%d err=%v", result.Report.Imported, result.Report.Rejected, err)
			}
			rows := result.Report.Rows
			if rows[0].Line != 3 || rows[1].Warning != "provider_options_unverified" || result.Servers[1].Credential != "test-user:p@ss" || result.Servers[1].Port != 443 {
				t.Fatal("normalization did not preserve the expected line, warning, or private settings")
			}
			for i, want := range []string{"invalid_server_credentials", "unsupported_protocol", "invalid_server_options", "invalid_server_port", "invalid_server_options", "invalid_server_uri"} {
				if rows[i+2].Error != want {
					t.Fatalf("row %d: error=%q want=%q", i+2, rows[i+2].Error, want)
				}
			}
			output, err := json.Marshal(result.Report)
			if err != nil || strings.Contains(string(output), "private-") || strings.Contains(string(output), "PrivateLabel") || strings.Contains(string(output), "p@ss") {
				t.Fatal("public report exposed private content")
			}
			directory := t.TempDir()
			result.Report.ID = "current-revision"
			if err := Save(directory, result); err != nil {
				t.Fatal(err)
			}
			if err := Save(directory, result); err != nil {
				t.Fatal(err)
			}
			stored, err := os.ReadFile(filepath.Join(directory, stateFile))
			if err != nil || !strings.Contains(string(stored), "test-user:p@ss") {
				t.Fatal("private settings did not survive a repeated save")
			}
			saved, err := ReadReport(directory)
			if err != nil || saved.Imported != result.Report.Imported {
				t.Fatal("saved summary unavailable")
			}
			if _, err := ReadServer(directory, "previous-revision", 3); err != ErrSubscriptionChanged {
				t.Fatal("stale selection was accepted")
			}
			if _, err := ReadServer(directory, result.Report.ID, 5); err != ErrServerNotFound {
				t.Fatal("rejected row was selectable")
			}
			if server, err := ReadServer(directory, result.Report.ID, 3); err != nil || server.Protocol != "vless" {
				t.Fatal("selected server was unavailable")
			}
		})
	}
}

func TestPublicSubscriptionDestination(t *testing.T) {
	for _, ip := range []string{"localhost", "127.0.0.1", "::1", "::ffff:192.168.1.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "192.0.2.1", "224.0.0.1", "fd00::1", "64:ff9b::a9fe:a9fe", "2001:db8::1", "2002:7f00:1::1"} {
		if address, err := netip.ParseAddr(ip); err == nil && publicAddress(address) {
			t.Errorf("nonpublic address accepted: %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("public address rejected: %s", ip)
		}
	}
}
