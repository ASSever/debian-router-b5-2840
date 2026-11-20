package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationPreservesSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO settings(key, value) VALUES (?, ?)", "test.marker", "preserved"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", "test.marker").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" {
		t.Fatalf("setting = %q, want preserved", value)
	}
	var migrations int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != schemaVersion {
		t.Fatalf("migrations = %d, want %d", migrations, schemaVersion)
	}
}

func TestUpgradeCreatesSessionsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want 2", version)
	}
	if _, err := db.Exec("INSERT INTO sessions(token_hash, expires_at) VALUES (?, ?)", []byte("hash"), 1); err != nil {
		t.Fatalf("sessions table unavailable: %v", err)
	}
}

func TestUpgradeFromVersionOnePreservesSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(context.Background(), `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("INSERT INTO schema_migrations(version) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("INSERT INTO settings(key, value) VALUES (?, ?)", "preserve.me", "yes"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", "preserve.me").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "yes" {
		t.Fatalf("setting after upgrade = %q, want yes", value)
	}
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema after upgrade = %d, want 2", version)
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations(version) VALUES (?)", schemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected newer schema to be rejected")
	}
}
