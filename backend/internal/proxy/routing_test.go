package proxy

import (
	"bytes"
	"testing"
)

func TestCompileProxyProfileKeepsOrderedRulesAndSelectedMember(t *testing.T) {
	profile := Profile{
		Default: "direct",
		Groups:  []Group{{Name: "office", Members: []int{1, 2}, Selected: 2}},
		Rules: []Rule{
			{Match: "exact", Domain: "skip.example", Target: "block"},
			{Match: "suffix", Domain: "example", Target: "group:office"},
		},
	}
	servers := []Server{
		{Line: 1, Protocol: "hysteria2", Host: "first.example", Port: 443, Credential: "test-only"},
		{Line: 2, Protocol: "vless", Host: "second.example", Port: 443, Credential: "12ecc646-b0c1-4e87-b094-6d5cf3d12189"},
	}
	config, err := CompileProfile(ProfileChange{Profile: profile, Servers: servers})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(config, []byte(`"default":"server-2"`)) ||
		!bytes.Contains(config, []byte(`"listen":"127.0.0.1"`)) ||
		!bytes.Contains(config, []byte(`"final":"direct"`)) ||
		bytes.Index(config, []byte(`"domain":["skip.example"]`)) >= bytes.Index(config, []byte(`"domain_suffix":["example"]`)) {
		t.Fatal("generated policy lost rule order, loopback binding, selection, or default")
	}
	profile.Groups[0].Selected = 3
	if _, err := CompileProfile(ProfileChange{Profile: profile, Servers: servers}); err != ErrInvalidProfile {
		t.Fatal("accepted a selection outside the group")
	}
	profile.Groups[0].Selected = 2
	profile.Rules[0].Target = "group:missing"
	if _, err := CompileProfile(ProfileChange{Profile: profile, Servers: servers}); err != ErrInvalidProfile {
		t.Fatal("accepted unknown target")
	}
}
