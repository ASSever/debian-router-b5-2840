package network

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func pppoeTestManager(t *testing.T, run commandRunner, readPPP func() (string, string, error)) (*PPPoEManager, string, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "etc", "systemd", "network", "10-wan.network")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state", "wan-pppoe")
	activePath := filepath.Join(root, "state", "pppoe-active")
	manager := newPPPoEManagerForTest(configPath, statePath, activePath, run,
		func(string) (ActiveNetworkStatus, error) { return ActiveNetworkStatus{LinkState: "carrier"}, nil }, readPPP)
	return manager, configPath, activePath
}

func pppoeCommandStub(commands *[]string) commandRunner {
	stopped := false
	return func(_ context.Context, path string, args ...string) (string, error) {
		joined := path + " " + strings.Join(args, " ")
		*commands = append(*commands, joined)
		if strings.Contains(joined, "--property=LoadState") {
			return "loaded", nil
		}
		if strings.Contains(joined, "--property=ActiveState") {
			if stopped {
				return "inactive", nil
			}
			return "active", nil
		}
		if strings.Contains(joined, " stop ") {
			stopped = true
		}
		if strings.Contains(joined, "is-active") {
			return "active", nil
		}
		return "", nil
	}
}

func TestPPPoEApplyKeepsSecretOutOfTransactionAndArmsTimerFirst(t *testing.T) {
	var commands []string
	manager, configPath, activePath := pppoeTestManager(t, pppoeCommandStub(&commands),
		func() (string, string, error) { return "", "", ErrPPPoENotConnected })
	original := []byte("[Match]\nName=enp1s0\n\n[Network]\nDHCP=ipv4\n")
	if err := os.WriteFile(configPath, original, 0640); err != nil {
		t.Fatal(err)
	}
	config := PPPoEConfig{Username: "user@example", Password: "secret-value"}
	transaction, err := manager.Apply(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.ID == "" || len(commands) < 4 || !strings.Contains(commands[0], "systemd-run") ||
		!strings.Contains(commands[0], "--on-active=120s") || !strings.Contains(commands[0], "rollback-pppoe") {
		t.Fatalf("timer not armed before configuration: %#v", commands)
	}
	for _, command := range commands {
		if strings.Contains(command, config.Password) || strings.Contains(command, config.Username) {
			t.Fatalf("credential in command: %s", command)
		}
	}
	optionsPath := filepath.Join(activePath, "options")
	options, err := os.ReadFile(optionsPath)
	if err != nil || !strings.Contains(string(options), "password \"secret-value\"") {
		t.Fatalf("options invalid: %v", err)
	}
	if !strings.Contains(string(options), "defaultroute-metric 1000") {
		t.Fatal("PPPoE default route must outrank the Wi-Fi management default route")
	}
	info, err := os.Stat(optionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("options mode = %v", info.Mode().Perm())
	}
	for _, name := range []string{"backup.json", "original"} {
		data, err := os.ReadFile(filepath.Join(manager.statePath, transaction.ID, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), config.Password) {
			t.Fatalf("secret in %s", name)
		}
	}
	if err := manager.RollbackFromTimer(context.Background(), transaction.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil || string(restored) != string(original) {
		t.Fatalf("restore = %q, %v", restored, err)
	}
	if _, err := os.Stat(optionsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret remains: %v", err)
	}
}

func TestPPPoEConfirmRequiresLiveSessionAndRecoveryRestores(t *testing.T) {
	var commands []string
	manager, configPath, _ := pppoeTestManager(t, pppoeCommandStub(&commands),
		func() (string, string, error) { return "", "", ErrPPPoENotConnected })
	if err := os.WriteFile(configPath, []byte("DHCP=ipv4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	transaction, err := manager.Apply(context.Background(), PPPoEConfig{Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(manager.Confirm(context.Background(), transaction.ID), ErrPPPoENotConnected) {
		t.Fatal("confirmation succeeded without a live PPP session")
	}
	if err := manager.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil || string(content) != "DHCP=ipv4\n" {
		t.Fatalf("recovery = %q, %v", content, err)
	}
}

func TestPPPoEValidationAndStatusHideCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes required")
	}
	for _, config := range []PPPoEConfig{
		{Username: "", Password: "p"}, {Username: "u\nother", Password: "p"},
		{Username: "u", Password: "p\nplugin malicious"},
	} {
		if ValidatePPPoEConfig(config) == nil {
			t.Fatalf("accepted %#v", config)
		}
	}
	var commands []string
	journalOutput := "Timeout waiting for PADO packets. password secret-value"
	run := pppoeCommandStub(&commands)
	manager, _, activePath := pppoeTestManager(t, func(ctx context.Context, path string, args ...string) (string, error) {
		if strings.Contains(path, "journalctl") {
			return journalOutput, nil
		}
		return run(ctx, path, args...)
	}, func() (string, string, error) { return "", "", ErrPPPoENotConnected })
	if err := manager.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activePath, "options"), []byte(renderPPPoEOptions(PPPoEConfig{Username: "u", Password: "secret-value"})), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status()
	if err != nil || status.State != "no_peer" {
		t.Fatalf("status = %#v, %v", status, err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "secret-value") {
		t.Fatal("status leaked password")
	}
	journalOutput = "PAP authentication failed\nConnection terminated."
	status, err = manager.Status()
	if err != nil || status.State != "auth_failed" {
		t.Fatalf("authentication status = %#v, %v", status, err)
	}
}

func TestPPPoERejectsPendingStaticWAN(t *testing.T) {
	var commands []string
	manager, _, _ := pppoeTestManager(t, pppoeCommandStub(&commands),
		func() (string, string, error) { return "", "", ErrPPPoENotConnected })
	staticPath := filepath.Join(filepath.Dir(manager.statePath), "wan-static", "0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(staticPath, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Apply(context.Background(), PPPoEConfig{Username: "u", Password: "p"})
	if !errors.Is(err, ErrPPPoEPending) || len(commands) != 0 {
		t.Fatalf("Apply = %v, commands = %#v", err, commands)
	}
}
