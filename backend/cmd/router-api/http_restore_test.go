package main

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func TestRestoreFromBackupKeepsLocalAdministrator(t *testing.T) {
	db, oldPassword := newAdministratorDB(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, "INSERT INTO settings VALUES ('fixture', 'archived')"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := []privilegedagent.BackupConfig{{
		Name: "etc/debian-router-b5-2840/monitor.json", Data: []byte("{}"),
	}}
	if err := createBackup(ctx, db, dir, time.Now(), files); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, backupFilename))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := parseRestoreArchive(ctx, raw, t.TempDir())
	if err != nil || restored.settings["fixture"] != "archived" || len(restored.configs) != 1 {
		t.Fatalf("could not read exported backup: %v", err)
	}
	localPassword := oldPassword + "-current"
	if err := admin.ChangePassword(ctx, db, oldPassword, localPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = 'current' WHERE key = 'fixture'"); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(ctx, db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bad := rewriteBackupMember(t, raw, "configs/"+files[0].Name)
	request := httptest.NewRequest(http.MethodPost, "https://localhost"+restoreRoute, bytes.NewReader(bad))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Origin", "https://localhost")
	request.Header.Set("Content-Type", "application/zip")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	recorder := httptest.NewRecorder()
	newAPI(db).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("tampered backup status=%d", recorder.Code)
	}
	var current string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='fixture'").Scan(&current); err != nil || current != "current" {
		t.Fatal("bad ZIP changed the working database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id := "0123456789abcdef0123456789abcdef"
	if err := database.WriteRestoredSettings(ctx, tx, restored.settings, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='fixture'").Scan(&current); err != nil || current != "archived" {
		t.Fatal("backup setting was not restored")
	}
	if valid, err := authsession.Valid(ctx, db, token, time.Now()); err != nil || valid {
		t.Fatal("session survived restore")
	}
	if change, err := admin.Authenticate(ctx, db, localPassword); err != nil || change {
		t.Fatal("local password or change flag was not preserved")
	}
	committed, err := database.RestoreWasCommitted(ctx, db, id)
	if err != nil || !committed {
		t.Fatal("missing durable commit decision")
	}
}

func rewriteBackupMember(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	original, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range original.File {
		body, err := fs.ReadFile(original, file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == name {
			body = append(body, ' ')
		}
		entry, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
