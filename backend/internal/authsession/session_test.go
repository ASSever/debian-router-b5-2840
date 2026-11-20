package authsession

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
)

func TestCreateValidateAndRevoke(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Unix(1_800_000_000, 0)
	token, err := Create(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := Valid(context.Background(), db, token, now.Add(Lifetime-time.Second)); err != nil || !valid {
		t.Fatalf("valid before expiry = %t, err=%v", valid, err)
	}
	if valid, err := Valid(context.Background(), db, token, now.Add(Lifetime)); err != nil || valid {
		t.Fatalf("valid at expiry = %t, err=%v", valid, err)
	}
	if err := Revoke(context.Background(), db, token); err != nil {
		t.Fatal(err)
	}
	if valid, err := Valid(context.Background(), db, token, now); err != nil || valid {
		t.Fatalf("valid after revoke = %t, err=%v", valid, err)
	}
}
