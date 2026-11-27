package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

const restoreRoute = "/api/backup/restore"
const restoreJournalName = "restore-pending.json"
const restoreSubscription = "proxy-subscription.json"

type apiRestoreJournal struct {
	ID           string
	Subscription *privilegedagent.RestoreFile
}

func guardRestore(handler http.Handler) http.Handler {
	var writes sync.RWMutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
		case r.Method == http.MethodPost && r.URL.Path == restoreRoute:
			if !writes.TryLock() {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "restore_busy"})
				return
			}
			defer writes.Unlock()
		default:
			if !writes.TryRLock() {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "restore_busy"})
				return
			}
			defer writes.RUnlock()
			if _, err := os.Lstat(filepath.Join(proxyStateDirectory(), restoreJournalName)); !errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "restore_recovery_pending"})
				return
			}
		}
		handler.ServeHTTP(w, r)
	})
}

func (a *api) restoreDownload(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdministrator(w, r) {
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	if r.Header.Get("Content-Type") != "application/zip" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "zip_required"})
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(45 * time.Second))
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRestoreBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_restore_archive"})
		return
	}
	dir, err := os.MkdirTemp("", "router-restore-")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "restore_failed"})
		return
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	archive, err := parseRestoreArchive(ctx, raw, dir)
	cancel()
	if err != nil || r.Context().Err() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_restore_archive"})
		return
	}
	// Once the durable journal exists, recovery must outlive the browser.
	ctx, cancel = context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	network, committed, err := applyRestore(ctx, a.db, proxyStateDirectory(), archive)
	if err != nil && !committed {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "restore_failed"})
		return
	}
	clearSessionCookie(w)
	if err != nil {
		log.Print("backup restore committed; finalization pending")
		writeJSON(w, http.StatusAccepted, map[string]bool{"network": network, "finalization_pending": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"network": network})
}

func agentRestore(ctx context.Context, operation string, req privilegedagent.RestoreRequest) (bool, error) {
	r, err := privilegedagent.SendRestore(ctx, agentSocketPath(), operation, req)
	if err != nil || r.Error != "" || r.Status != "ok" {
		return false, errors.New("agent restore operation failed")
	}
	return r.RestoreNetwork, nil
}

func recoverRestore(ctx context.Context, db *sql.DB, stateDir string) (bool, error) {
	path := filepath.Join(stateDir, restoreJournalName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var journal apiRestoreJournal
	if json.Unmarshal(data, &journal) != nil || !privilegedagent.ValidRestoreID(journal.ID) {
		return false, errors.New("invalid local restore journal")
	}
	committed, err := database.RestoreWasCommitted(ctx, db, journal.ID)
	if err != nil {
		return false, err
	}
	op := "restore-finalize"
	var localErr error
	if !committed {
		op = "restore-rollback"
		if journal.Subscription != nil {
			localErr = privilegedagent.WriteRestoreFile(filepath.Join(stateDir, restoreSubscription), *journal.Subscription)
		}
	}
	_, err = agentRestore(ctx, op, privilegedagent.RestoreRequest{ID: journal.ID})
	if err := errors.Join(err, localErr); err != nil {
		return committed, err
	}
	return committed, privilegedagent.WriteRestoreFile(path, privilegedagent.RestoreFile{})
}

func applyRestore(ctx context.Context, db *sql.DB, stateDir string, archive restoreArchive) (bool, bool, error) {
	if _, err := recoverRestore(ctx, db, stateDir); err != nil {
		return false, false, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return false, false, err
	}
	journal := apiRestoreJournal{ID: hex.EncodeToString(id[:])}
	if archive.subscription != nil {
		root, err := os.OpenRoot(stateDir)
		if err != nil {
			return false, false, err
		}
		old, err := privilegedagent.CaptureRestoreFile(root, restoreSubscription)
		_ = root.Close()
		if err != nil {
			return false, false, err
		}
		journal.Subscription = &old
	}
	if err := privilegedagent.SaveRestoreJSON(filepath.Join(stateDir, restoreJournalName), journal); err != nil {
		return false, false, err
	}
	network, err := writeRestore(ctx, db, stateDir, archive, journal.ID)
	committed, recoveryErr := recoverRestore(ctx, db, stateDir)
	return network, committed, errors.Join(err, recoveryErr)
}

func writeRestore(ctx context.Context, db *sql.DB, stateDir string, archive restoreArchive, id string) (bool, error) {
	network, err := agentRestore(ctx, "restore-apply", privilegedagent.RestoreRequest{ID: id, Configs: archive.configs})
	if err != nil {
		return false, err
	}
	if archive.subscription != nil {
		state := privilegedagent.RestoreFile{Exists: true, Data: archive.subscription, Mode: 0600}
		if err := privilegedagent.WriteRestoreFile(filepath.Join(stateDir, restoreSubscription), state); err != nil {
			return false, err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := database.WriteRestoredSettings(ctx, tx, archive.settings, id); err != nil {
		return false, err
	}
	return network, tx.Commit()
}
