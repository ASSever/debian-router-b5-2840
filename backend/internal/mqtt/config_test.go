package mqtt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRequiresSafeBrokerAndTopics(t *testing.T) {
	const config = `{"broker":"mqtts://broker.example:8883","client_id":"b5-router","topic_prefix":"router/b5"}`
	path := filepath.Join(t.TempDir(), "mqtt.json")
	for _, sample := range []struct {
		body string
		ok   bool
	}{
		{config, true},
		{strings.Replace(config, "broker.example", "[::1]", 1), true},
		{strings.Replace(config, "broker.example", "user:fixture@broker.example", 1), false},
		{strings.Replace(config, "8883", "0", 1), false},
		{strings.Replace(config, "router/b5", "router/+", 1), false},
		{strings.Replace(config, "mqtts://", "http://", 1), false},
		{config + " {}", false},
	} {
		if err := os.WriteFile(path, []byte(sample.body), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadConfig(path)
		if (err == nil) != sample.ok {
			t.Fatalf("unexpected config verdict: %v", err)
		}
		if err == nil && got.IntervalSeconds != 30 {
			t.Fatalf("interval = %d", got.IntervalSeconds)
		}
	}
}

func TestCredentialsRequirePair(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadCredentials(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mqtt-username"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(dir); err == nil {
		t.Fatal("accepted a missing password")
	}
	if err := os.WriteFile(filepath.Join(dir, "mqtt-password"), []byte("example only\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	creds, err := LoadCredentials(dir)
	if err != nil || creds.username != "fixture" || creds.password != "example only" {
		t.Fatal("credential pair not loaded")
	}
}
