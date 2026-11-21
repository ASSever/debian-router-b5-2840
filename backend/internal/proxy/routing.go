package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var (
	ErrInvalidProfile = errors.New("invalid_proxy_profile")
	groupName         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
)

// Profile describes the policy for clients using the router's explicit proxy.
type Profile struct {
	Default        string  `json:"default"`
	Groups         []Group `json:"groups"`
	Rules          []Rule  `json:"rules"`
	SubscriptionID string  `json:"subscription_id,omitempty"`
}

type Group struct {
	Name     string `json:"name"`
	Members  []int  `json:"members"`
	Selected int    `json:"selected"`
}

type Rule struct {
	Match  string `json:"match"`
	Domain string `json:"domain"`
	Target string `json:"target"`
}

// ProfileChange carries selected private server settings across the local agent socket.
type ProfileChange struct {
	Profile Profile  `json:"profile"`
	Servers []Server `json:"servers,omitempty"`
}

func DefaultProfile() Profile {
	return Profile{Default: "direct", Groups: []Group{}, Rules: []Rule{}}
}

// PrepareProfile resolves line numbers against the exact imported revision.
func PrepareProfile(directory string, profile Profile) (ProfileChange, error) {
	change := ProfileChange{Profile: profile}
	lines, err := profileLines(profile)
	if err != nil {
		return ProfileChange{}, err
	}
	if len(lines) > 0 {
		for _, line := range lines {
			server, err := ReadServer(directory, profile.SubscriptionID, line)
			if err != nil {
				return ProfileChange{}, err
			}
			change.Servers = append(change.Servers, server)
		}
	}
	return change, nil
}

func profileLines(profile Profile) ([]int, error) {
	if len(profile.Groups) > 8 || len(profile.Rules) > 32 || len(profile.SubscriptionID) > 64 {
		return nil, ErrInvalidProfile
	}
	var lines []int
	seen := make(map[int]bool)
	for _, group := range profile.Groups {
		if len(group.Members) == 0 || len(group.Members) > 16 {
			return nil, ErrInvalidProfile
		}
		for _, line := range group.Members {
			if line < 1 {
				return nil, ErrInvalidProfile
			}
			if !seen[line] {
				lines = append(lines, line)
				seen[line] = true
			}
		}
	}
	if len(lines) > 16 {
		return nil, ErrInvalidProfile
	}
	return lines, nil
}

// CompileProfile accepts a structured policy and emits a single, loopback-only sing-box configuration.
func CompileProfile(change ProfileChange) ([]byte, error) {
	profile := change.Profile
	lines, err := profileLines(profile)
	if err != nil || len(lines) != len(change.Servers) {
		return nil, ErrInvalidProfile
	}
	outbounds := []map[string]any{{"type": "direct", "tag": "direct"}}
	servers := make(map[int]Server)
	for _, server := range change.Servers {
		servers[server.Line] = server
	}
	for _, line := range lines {
		server, found := servers[line]
		if !found || !validHost(server.Host) || server.Port == 0 || server.Credential == "" ||
			(server.Protocol == "vless" && !uuidPattern.MatchString(server.Credential)) {
			return nil, ErrInvalidProfile
		}
		outbound, ok := checkOutbound(server)
		if !ok {
			return nil, ErrInvalidProfile
		}
		outbound["tag"], outbound["server"], outbound["domain_resolver"] = serverTag(line), server.Host, "local-dns"
		outbounds = append(outbounds, outbound)
	}
	targets := map[string]string{"direct": "direct", "block": "block"}
	for _, group := range profile.Groups {
		target, tag := "group:"+group.Name, "group-"+group.Name
		if !groupName.MatchString(group.Name) || targets[target] != "" {
			return nil, ErrInvalidProfile
		}
		members := make([]string, 0, len(group.Members))
		seen := make(map[int]bool)
		for _, member := range group.Members {
			if seen[member] {
				return nil, ErrInvalidProfile
			}
			seen[member] = true
			members = append(members, serverTag(member))
		}
		if !seen[group.Selected] {
			return nil, ErrInvalidProfile
		}
		outbounds = append(outbounds, map[string]any{"type": "selector", "tag": tag, "outbounds": members, "default": serverTag(group.Selected)})
		targets[target] = tag
	}
	if targets[profile.Default] == "" {
		return nil, ErrInvalidProfile
	}
	rules := make([]map[string]any, 0, len(profile.Rules))
	for _, rule := range profile.Rules {
		domain := strings.TrimSuffix(strings.ToLower(rule.Domain), ".")
		_, ipErr := netip.ParseAddr(domain)
		if !validHost(domain) || ipErr == nil || targets[rule.Target] == "" || !oneOf(rule.Match, "exact", "suffix") {
			return nil, ErrInvalidProfile
		}
		match := "domain"
		if rule.Match == "suffix" {
			match = "domain_suffix"
		}
		compiled := map[string]any{match: []string{domain}, "action": "reject"}
		if rule.Target != "block" {
			compiled["action"], compiled["outbound"] = "route", targets[rule.Target]
		}
		rules = append(rules, compiled)
	}
	final := targets[profile.Default]
	if profile.Default == "block" {
		rules = append(rules, map[string]any{"inbound": []string{"local-proxy"}, "action": "reject"})
		final = "direct"
	}
	return json.Marshal(map[string]any{
		"log":       map[string]any{"disabled": true},
		"dns":       map[string]any{"servers": []map[string]string{{"type": "local", "tag": "local-dns"}}},
		"inbounds":  []map[string]any{{"type": "mixed", "tag": "local-proxy", "listen": "127.0.0.1", "listen_port": 11080}},
		"outbounds": outbounds,
		"route":     map[string]any{"rules": rules, "final": final, "default_domain_resolver": "local-dns"},
	})
}

func serverTag(line int) string { return fmt.Sprintf("server-%d", line) }
