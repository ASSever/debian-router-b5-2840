package proxy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoverySerializesWithManualChanges(t *testing.T) {
	root := t.TempDir()
	initial := filepath.Join(root, "initial")
	if err := os.Mkdir(initial, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeProfileFile(filepath.Join(initial, "config.json"), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	manual := NewProfileManager(root)
	worker := NewProfileManager(root)
	for _, manager := range []*ProfileManager{manual, worker} {
		manager.validate = func(context.Context, []byte) error { return nil }
		manager.restart = func(context.Context) error { return nil }
	}
	if err := manual.switchActive("initial"); err != nil {
		t.Fatal(err)
	}
	change := ProfileChange{
		Profile: Profile{
			Default: "group:office",
			Groups:  []Group{{Name: "office", Members: []int{1, 2}, Selected: 1}},
			Rules:   []Rule{{Match: "exact", Domain: "excluded.example", Target: "block"}},
		},
		Servers: []Server{
			{Line: 1, Protocol: "vless", Host: "first.example", Port: 443, Credential: "12ecc646-b0c1-4e87-b094-6d5cf3d12189"},
			{Line: 2, Protocol: "vless", Host: "second.example", Port: 443, Credential: "a744c4ea-b56d-4892-9ea1-f9d985151645"},
		},
	}
	if err := manual.Apply(t.Context(), change); err != nil {
		t.Fatal(err)
	}
	r := Recovery{Manager: worker, Group: "office", installLockPath: filepath.Join(root, "installer.lock")}
	stale, err := r.read(t.Context())
	if err != nil || stale.generation == "" {
		t.Fatalf("read: %v", err)
	}
	if err := manual.Apply(t.Context(), change); err != nil {
		t.Fatal(err)
	}
	if err := r.promote(t.Context(), stale, 2); !errors.Is(err, errRecoverySuperseded) {
		t.Fatalf("stale promotion: %v", err)
	}
	current, err := r.read(t.Context())
	if err != nil || current.generation == stale.generation {
		t.Fatalf("read current: %v", err)
	}
	if err := r.promote(t.Context(), current, 2); err != nil {
		t.Fatal(err)
	}
	selected, err := manual.Status()
	if err != nil || selected.Groups[0].Selected != 2 {
		t.Fatalf("profile not updated: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "active", "config.json"))
	if err != nil || !bytes.Contains(data, []byte(`"default":"server-2"`)) ||
		!bytes.Contains(data, []byte(`"domain":["excluded.example"]`)) {
		t.Fatal("selection or existing route was lost")
	}

	unlock, err := lockProfiles(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := worker.Apply(ctx, change); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("unexpected apply with profile lock held: %v", err)
	}
}
