package main

import (
	"archive/zip"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func TestBackupIncludesCommittedWALAndExcludesSessions(t *testing.T) {
	db, _ := newAdministratorDB(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO settings VALUES ('backup_fixture', 'committed')"); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(ctx, db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := privilegedagent.BackupConfig{Name: "etc/managed.json", Data: []byte(`{"enabled":true}`)}
	if err := createBackup(ctx, db, dir, time.Now(), []privilegedagent.BackupConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(filepath.Join(dir, backupFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	manifest, err := fs.ReadFile(&archive.Reader, "SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	expected := []string{"format.json", "database/router.db", "configs/" + cfg.Name}
	if len(lines) != len(expected) || len(archive.File) != len(expected)+1 {
		t.Fatal("unexpected backup members")
	}
	for i, name := range expected {
		data, err := fs.ReadFile(&archive.Reader, name)
		if err != nil || lines[i] != fmt.Sprintf("%x  %s", sha256.Sum256(data), name) {
			t.Fatalf("missing or invalid checksum for %s", name)
		}
	}
	data, err := fs.ReadFile(&archive.Reader, "database/router.db")
	if err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(dir, "extracted.db")
	if err := os.WriteFile(extracted, data, 0600); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", extracted)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var integrity, setting string
	var sessions int
	if copyDB.QueryRow("PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" ||
		copyDB.QueryRow("SELECT value FROM settings WHERE key='backup_fixture'").Scan(&setting) != nil || setting != "committed" ||
		copyDB.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions) != nil || sessions != 0 {
		t.Fatal("invalid extracted SQLite snapshot")
	}
	if valid, err := authsession.Valid(ctx, db, token, time.Now()); err != nil || !valid {
		t.Fatal("backup affected the live session")
	}
}

func TestBackupPreservesHTTPSAndAdministratorBoundary(t *testing.T) {
	db, password := newAdministratorDB(t)
	ctx := t.Context()
	token, err := authsession.Create(ctx, db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := newAPI(db)
	request := func(cookie, origin string, tlsEnabled bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://localhost/api/backup", nil)
		r.TLS = nil
		if tlsEnabled {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Origin", origin)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if request("", "https://localhost", false).Code != 426 ||
		request("", "https://localhost", true).Code != 401 ||
		request(token, "https://localhost", true).Code != 403 {
		t.Fatal("backup exposed before completion of administrator authentication")
	}
	if err := admin.ChangePassword(ctx, db, password, password+"-replacement"); err != nil {
		t.Fatal(err)
	}
	token, err = authsession.Create(ctx, db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := request(token, "https://unrelated.invalid", true)
	if w.Code != 403 || w.Header().Get("Content-Disposition") != "" ||
		!strings.Contains(w.Body.String(), "origin_rejected") {
		t.Fatal("cross-origin request passed the backup boundary")
	}
}
