package admin

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
)

func TestAdministratorLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var password string
	created, err := Initialize(ctx, db, func(secret string) error {
		password = secret
		return nil
	})
	if err != nil || !created || len(password) != 32 {
		t.Fatalf("created=%t password length=%d err=%v", created, len(password), err)
	}
	var stored string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "$argon2id$") || strings.Contains(stored, password) {
		t.Fatal("password was not stored as an Argon2id hash")
	}
	calledAgain := false
	created, err = Initialize(ctx, db, func(string) error { calledAgain = true; return nil })
	if err != nil || created || calledAgain {
		t.Fatalf("repeat initialization: created=%t callback=%t err=%v", created, calledAgain, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustChange, err := Authenticate(ctx, db, password)
	if err != nil || !mustChange {
		t.Fatalf("first login: mustChange=%t err=%v", mustChange, err)
	}
	if _, err := Authenticate(ctx, db, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	if err := ChangePassword(ctx, db, password, "short"); !errors.Is(err, ErrPasswordLength) {
		t.Fatalf("short password error = %v", err)
	}
	if err := ChangePassword(ctx, db, password, password); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("reused password error = %v", err)
	}
	if err := ChangePassword(ctx, db, "wrong", "a longer replacement password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password error = %v", err)
	}
	const replacement = "a longer replacement password"
	if err := ChangePassword(ctx, db, password, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, password); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still accepted: %v", err)
	}
	mustChange, err = Authenticate(ctx, db, replacement)
	if err != nil || mustChange {
		t.Fatalf("replacement login: mustChange=%t err=%v", mustChange, err)
	}
	var resetPassword string
	if err := Reset(ctx, db, func(secret string) error { resetPassword = secret; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, replacement); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("pre-reset password still accepted: %v", err)
	}
	mustChange, err = Authenticate(ctx, db, resetPassword)
	if err != nil || !mustChange {
		t.Fatalf("login after reset: mustChange=%t err=%v", mustChange, err)
	}
}

func TestFailedPresentationRollsBack(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = Initialize(context.Background(), db, func(string) error { return errors.New("terminal disconnected") })
	if err == nil {
		t.Fatal("expected presentation failure")
	}
	exists, err := Exists(context.Background(), db)
	if err != nil || exists {
		t.Fatalf("account persisted after failed presentation: exists=%t err=%v", exists, err)
	}
}

func TestMissingAdministrator(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := Authenticate(context.Background(), db, "anything"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing account error = %v", err)
	}
	if _, err := Initialize(context.Background(), db, nil); err == nil {
		t.Fatal("expected missing presenter to fail")
	}
}

func TestResetRevokesSessions(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := Initialize(ctx, db, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(ctx, db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Reset(ctx, db, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if valid, err := authsession.Valid(ctx, db, token, time.Now()); err != nil || valid {
		t.Fatalf("session after password reset = %t, err=%v", valid, err)
	}
}
