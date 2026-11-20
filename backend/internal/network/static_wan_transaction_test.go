package network

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStaticWANApplyArmsRollbackBeforeChangingConfigAndConfirmKeepsStatic(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	original := []byte("[Match]\nName=enp1s0\n\n[Network]\nDHCP=ipv4\n")
	if err := os.WriteFile(configPath, original, 0640); err != nil {
		t.Fatal(err)
	}
	statusCalls := 0
	statusReader := func(string) (ActiveNetworkStatus, error) {
		statusCalls++
		if statusCalls == 1 {
			return ActiveNetworkStatus{LinkState: "carrier"}, nil
		}
		return ActiveNetworkStatus{
			LinkState: "carrier",
			Addresses: []string{"192.168.1.104/24"},
			Gateway:   "192.168.1.1",
			DNS:       []string{"1.1.1.1", "192.168.1.1"},
		}, nil
	}
	var commands []string
	stopped := false
	run := func(_ context.Context, command string, arguments ...string) (string, error) {
		joined := command + " " + strings.Join(arguments, " ")
		commands = append(commands, joined)
		if command == "/usr/bin/systemd-run" {
			current, err := os.ReadFile(configPath)
			if err != nil || string(current) != string(original) {
				t.Fatalf("config changed before rollback timer was armed: %v", err)
			}
			if !strings.Contains(joined, "--on-active=120s") || !strings.Contains(joined, "rollback ") ||
				!strings.Contains(joined, "--property=RestrictAddressFamilies=AF_UNIX AF_NETLINK") {
				t.Fatalf("rollback command is incomplete: %s", joined)
			}
			return "", nil
		}
		if strings.Contains(joined, "--property=ActiveState") {
			if stopped {
				return "inactive", nil
			}
			return "active", nil
		}
		if strings.Contains(joined, "--property=LoadState") {
			return "loaded", nil
		}
		if strings.Contains(joined, " stop ") {
			stopped = true
			return "", nil
		}
		return "", nil
	}
	manager := newStaticWANManagerForTest(configPath, statePath, run, statusReader)
	manager.now = func() time.Time { return time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC) }
	config := StaticWANConfig{Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1", "1.1.1.1"}}

	transaction, err := manager.Apply(context.Background(), config)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if transaction.ID == "" || !transaction.ExpiresAt.Equal(manager.now().Add(staticWANDelay)) {
		t.Fatalf("transaction = %#v, want an ID and 120-second expiry", transaction)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := RenderStaticWAN(config)
	if string(got) != string(want) {
		t.Fatalf("applied config = %q, want %q", got, want)
	}
	if len(commands) < 4 || !strings.Contains(commands[0], "/usr/bin/systemd-run") ||
		!strings.Contains(commands[1], "--property=ActiveState") ||
		!strings.Contains(commands[2], "/usr/bin/networkctl reload") ||
		!strings.Contains(commands[3], "/usr/bin/networkctl reconfigure enp1s0") {
		t.Fatalf("command order = %#v", commands)
	}

	if err := manager.Confirm(context.Background(), transaction.ID); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(statePath, transaction.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed transaction directory still exists: %v", err)
	}
	got, err = os.ReadFile(configPath)
	if err != nil || string(got) != string(want) {
		t.Fatalf("confirmed config = %q, read error = %v", got, err)
	}
}

func TestStaticWANTimerRollbackRestoresOriginalFile(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	original := []byte("exact previous config\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	run := staticWANCommandStub()
	manager := newStaticWANManagerForTest(configPath, statePath, run, func(string) (ActiveNetworkStatus, error) {
		return ActiveNetworkStatus{LinkState: "carrier"}, nil
	})
	transaction, err := manager.Apply(context.Background(), StaticWANConfig{
		Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := manager.RollbackFromTimer(context.Background(), transaction.ID); err != nil {
		t.Fatalf("RollbackFromTimer() error = %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("restored config = %q, read error = %v", got, err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat restored config: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("restored mode = %v; want 0600", info.Mode().Perm())
	}
}

func TestStaticWANTimerRollbackRemovesNewProfileWhenNoPreviousFileExists(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	manager := newStaticWANManagerForTest(configPath, statePath, staticWANCommandStub(), func(string) (ActiveNetworkStatus, error) {
		return ActiveNetworkStatus{LinkState: "carrier"}, nil
	})
	transaction, err := manager.Apply(context.Background(), StaticWANConfig{
		Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("static profile was not written: %v", err)
	}
	if err := manager.RollbackFromTimer(context.Background(), transaction.ID); err != nil {
		t.Fatalf("RollbackFromTimer() error = %v", err)
	}
	if _, err := os.Lstat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new network file still exists after rollback: %v", err)
	}
}

func TestStaticWANRejectsInvalidGatewayBeforeAnyCommandOrFileChange(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	original := []byte("[Network]\nDHCP=ipv4\n")
	if err := os.WriteFile(configPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	manager := newStaticWANManagerForTest(configPath, statePath, func(context.Context, string, ...string) (string, error) {
		called = true
		return "", nil
	}, func(string) (ActiveNetworkStatus, error) {
		called = true
		return ActiveNetworkStatus{LinkState: "carrier"}, nil
	})
	_, err := manager.Apply(context.Background(), StaticWANConfig{
		Address: "192.168.1.104/24", Gateway: "192.168.2.1", DNS: []string{"192.168.1.1"},
	})
	if !errors.Is(err, ErrStaticWANGateway) || called {
		t.Fatalf("Apply() error = %v, callbacks called = %t; want invalid gateway before any operation", err, called)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("config changed on invalid gateway: %q, error = %v", got, err)
	}
}

func TestStaticWANRejectsLinkDownBeforeArmingRollback(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	original := []byte("[Network]\nDHCP=ipv4\n")
	if err := os.WriteFile(configPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	manager := newStaticWANManagerForTest(configPath, statePath, func(context.Context, string, ...string) (string, error) {
		called = true
		return "", nil
	}, func(string) (ActiveNetworkStatus, error) { return ActiveNetworkStatus{LinkState: "off"}, nil })
	_, err := manager.Apply(context.Background(), StaticWANConfig{
		Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"},
	})
	if !errors.Is(err, ErrStaticWANLinkDown) || called {
		t.Fatalf("Apply() error = %v, timer command called = %t; want link-down before timer", err, called)
	}
}

func TestStaticWANRecoveryAfterRestartRestoresPendingProfile(t *testing.T) {
	configPath, statePath := staticWANTestPaths(t)
	original := []byte("[Network]\nDHCP=ipv4\n")
	if err := os.WriteFile(configPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	manager := newStaticWANManagerForTest(configPath, statePath, staticWANCommandStub(), func(string) (ActiveNetworkStatus, error) {
		return ActiveNetworkStatus{LinkState: "carrier"}, nil
	})
	transaction, err := manager.Apply(context.Background(), StaticWANConfig{
		Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := manager.RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending() error = %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("recovered config = %q, read error = %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(statePath, transaction.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending transaction remains after recovery: %v", err)
	}
}

func TestStaticWANRecoveryRestoresOriginalWithoutTransactionRecord(t *testing.T) {
	for _, test := range []struct {
		name   string
		remove bool
	}{
		{name: "missing record", remove: true},
		{name: "corrupt record"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath, statePath := staticWANTestPaths(t)
			original := []byte("[Network]\nDHCP=ipv4\n")
			if err := os.WriteFile(configPath, original, 0644); err != nil {
				t.Fatal(err)
			}
			manager := newStaticWANManagerForTest(configPath, statePath, staticWANCommandStub(), func(string) (ActiveNetworkStatus, error) {
				return ActiveNetworkStatus{LinkState: "carrier"}, nil
			})
			transaction, err := manager.Apply(context.Background(), StaticWANConfig{
				Address: "192.168.1.104/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"},
			})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			recordPath := filepath.Join(statePath, transaction.ID, "transaction.json")
			if test.remove {
				if err := os.Remove(recordPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(recordPath, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}

			if err := manager.RecoverPending(context.Background()); err != nil {
				t.Fatalf("RecoverPending() error = %v", err)
			}
			got, err := os.ReadFile(configPath)
			if err != nil || string(got) != string(original) {
				t.Fatalf("recovered config = %q, read error = %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(statePath, transaction.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("transaction directory remains: %v", err)
			}
		})
	}
}

func staticWANTestPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "etc", "systemd", "network", "10-wan.network")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	return configPath, filepath.Join(root, "state", "wan-static")
}

func staticWANCommandStub() commandRunner {
	stopped := false
	return func(_ context.Context, command string, arguments ...string) (string, error) {
		joined := command + " " + strings.Join(arguments, " ")
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
		return "", nil
	}
}
