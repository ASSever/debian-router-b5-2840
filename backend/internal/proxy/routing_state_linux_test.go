package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileManagerRestartsPreviousAndRecoversPendingOnStartup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "initial"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeProfileFile(filepath.Join(root, "initial", "config.json"), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	m := NewProfileManager(root)
	if err := m.switchActive("initial"); err != nil {
		t.Fatal(err)
	}
	m.validate = func(context.Context, []byte) error { return nil }
	var restarts []string
	m.restart = func(_ context.Context) error {
		generation, err := m.activeGeneration()
		if err != nil {
			return err
		}
		restarts = append(restarts, generation)
		if generation != "initial" {
			return errors.New("injected start failure")
		}
		return nil
	}
	if err := m.Apply(t.Context(), ProfileChange{Profile: DefaultProfile()}); err != ErrProfileRolledBack {
		t.Fatalf("apply: %v", err)
	}
	if len(restarts) != 2 || restarts[0] == "initial" || restarts[1] != "initial" {
		t.Fatal("previous generation was not restarted")
	}
	if err := writeProfileFile(filepath.Join(root, "pending"), []byte("initial")); err != nil {
		t.Fatal(err)
	}
	if err := m.switchActive("generation-99"); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if generation, err := m.activeGeneration(); err != nil || generation != "initial" {
		t.Fatal("startup did not recover the previous profile")
	}
	if _, err := os.Stat(filepath.Join(root, "pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery did not clear pending marker")
	}
}
