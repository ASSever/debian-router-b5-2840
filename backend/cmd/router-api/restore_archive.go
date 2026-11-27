package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

const maxRestoreBytes = 32 << 20

type restoreArchive struct {
	settings     map[string]string
	configs      []privilegedagent.BackupConfig
	subscription []byte
}

func parseRestoreArchive(ctx context.Context, raw []byte, stagingDir string) (restoreArchive, error) {
	var result restoreArchive
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(archive.File) > 64 {
		return result, errors.New("invalid backup ZIP")
	}
	members := make(map[string][]byte, len(archive.File))
	remaining := int64(maxRestoreBytes)
	for _, file := range archive.File {
		name := file.Name
		if _, duplicate := members[name]; duplicate || !file.Mode().IsRegular() ||
			!allowedRestoreMember(name) || file.UncompressedSize64 > uint64(remaining) {
			return result, errors.New("invalid backup member")
		}
		reader, err := file.Open()
		if err != nil {
			return result, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, remaining+1))
		_ = reader.Close()
		if err != nil || int64(len(data)) > remaining || uint64(len(data)) != file.UncompressedSize64 {
			return result, errors.New("invalid or oversized backup member")
		}
		members[name] = data
		remaining -= int64(len(data))
	}
	if err := verifyRestoreManifest(members); err != nil {
		return result, err
	}
	var format struct {
		Version   int       `json:"version"`
		CreatedAt time.Time `json:"created_at"`
	}
	if json.Unmarshal(members["format.json"], &format) != nil || format.Version != 1 || format.CreatedAt.IsZero() {
		return result, errors.New("unsupported backup format")
	}
	var configSize int
	for _, file := range archive.File {
		relative, isConfig := strings.CutPrefix(file.Name, "configs/")
		if !isConfig {
			continue
		}
		data := members[file.Name]
		if relative == privilegedagent.APIBackupSubscription {
			var state map[string]json.RawMessage
			if len(data) > privilegedagent.MaxBackupConfigBytes || json.Unmarshal(data, &state) != nil || state == nil {
				return result, errors.New("invalid proxy subscription")
			}
			result.subscription = data
			continue
		}
		configSize += len(data)
		if configSize > privilegedagent.MaxBackupConfigBytes {
			return result, errors.New("backup configs exceed size limit")
		}
		result.configs = append(result.configs, privilegedagent.BackupConfig{Name: relative, Data: data})
	}
	dbPath := filepath.Join(stagingDir, "restore.db")
	if len(members["database/router.db"]) == 0 {
		return result, errors.New("missing SQLite snapshot")
	}
	if err := os.WriteFile(dbPath, members["database/router.db"], 0600); err != nil {
		return result, err
	}
	result.settings, err = database.ReadRestoreSettings(ctx, dbPath)
	return result, err
}

func allowedRestoreMember(name string) bool {
	if name == "format.json" || name == "SHA256SUMS" || name == "database/router.db" {
		return true
	}
	relative, ok := strings.CutPrefix(name, "configs/")
	return ok && privilegedagent.IsBackupConfig(relative)
}

func verifyRestoreManifest(members map[string][]byte) error {
	manifest, ok := members["SHA256SUMS"]
	if !ok || len(manifest) > 16384 || !bytes.HasSuffix(manifest, []byte("\n")) {
		return errors.New("invalid backup manifest")
	}
	seen := make(map[string]bool, len(members))
	for _, line := range strings.Split(strings.TrimSuffix(string(manifest), "\n"), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		body, exists := members[name]
		if !ok || !exists || seen[name] || name == "SHA256SUMS" {
			return errors.New("invalid backup manifest entry")
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != sum {
			return errors.New("backup checksum mismatch")
		}
		seen[name] = true
	}
	if len(seen) != len(members)-1 {
		return errors.New("incomplete backup manifest")
	}
	return nil
}
