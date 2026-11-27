package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

const backupFilename = "router-backup.zip"

func (a *api) backupDownload() http.HandlerFunc {
	inProgress := make(chan struct{}, 1)
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorizeAdministrator(w, r) {
			return
		}
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
			return
		}
		select {
		case inProgress <- struct{}{}:
			defer func() { <-inProgress }()
		default:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "backup_in_progress"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		dir, err := os.MkdirTemp("", "router-backup-")
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_unavailable"})
			return
		}
		defer os.RemoveAll(dir)
		configs, err := loadBackupConfigs(ctx)
		if err == nil {
			err = createBackup(ctx, a.db, dir, a.now(), configs)
		}
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_unavailable"})
			return
		}
		file, err := os.Open(filepath.Join(dir, backupFilename))
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_unavailable"})
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_unavailable"})
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(90 * time.Second))
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="router-backup.zip"`)
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		_, _ = io.Copy(w, file)
	}
}

func loadBackupConfigs(ctx context.Context) ([]privilegedagent.BackupConfig, error) {
	response, err := privilegedagent.SendRequest(ctx, agentSocketPath(), "backup-configs")
	if err != nil || response.Status != "ok" || response.Error != "" || response.BackupConfigs == nil {
		return nil, errors.New("router configs unavailable")
	}
	root, err := os.OpenRoot(proxyStateDirectory())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	const subscription = "proxy-subscription.json"
	data, err := privilegedagent.ReadBackupFile(root, subscription, privilegedagent.MaxBackupConfigBytes)
	if errors.Is(err, os.ErrNotExist) {
		return *response.BackupConfigs, nil
	}
	if err != nil {
		return nil, err
	}
	return append(*response.BackupConfigs, privilegedagent.BackupConfig{
		Name: "api/" + subscription, Data: data,
	}), nil
}

func snapshotBackupDB(ctx context.Context, db *sql.DB, path string) error {
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	snapshot, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	snapshot.SetMaxOpenConns(1)
	if _, err := snapshot.ExecContext(ctx, "PRAGMA secure_delete = ON"); err != nil {
		return err
	}
	if _, err := snapshot.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	var integrity string
	if err := snapshot.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("SQLite snapshot failed integrity check")
	}
	return nil
}

func createBackup(ctx context.Context, db *sql.DB, dir string, now time.Time, configs []privilegedagent.BackupConfig) error {
	dbPath := filepath.Join(dir, "router.db")
	if err := snapshotBackupDB(ctx, db, dbPath); err != nil {
		return err
	}
	dbFile, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer dbFile.Close()
	out, err := os.OpenFile(filepath.Join(dir, backupFilename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	archive := zip.NewWriter(out)
	var manifest strings.Builder
	seen := map[string]bool{}
	add := func(name string, source io.Reader) error {
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\r\n") || seen[name] {
			return errors.New("invalid or duplicate archive path")
		}
		seen[name] = true
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now.UTC()}
		header.SetMode(0600)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		hash := sha256.New()
		if _, err := io.Copy(io.MultiWriter(entry, hash), source); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "%x  %s\n", hash.Sum(nil), name)
		return ctx.Err()
	}
	version, err := json.Marshal(struct {
		Version   int       `json:"version"`
		CreatedAt time.Time `json:"created_at"`
	}{1, now.UTC()})
	if err != nil {
		return err
	}
	if err := add("format.json", strings.NewReader(string(version))); err != nil {
		return err
	}
	if err := add("database/router.db", dbFile); err != nil {
		return err
	}
	for _, config := range configs {
		if err := add("configs/"+config.Name, bytes.NewReader(config.Data)); err != nil {
			return err
		}
	}
	entry, err := archive.Create("SHA256SUMS")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(entry, manifest.String()); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return out.Close()
}
