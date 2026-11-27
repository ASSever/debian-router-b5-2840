package database

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

const restoreKey = "restore.commit_id"

// ReadRestoreSettings opens the uploaded snapshot read-only. Its SQL objects
// are never attached to the live database.
func ReadRestoreSettings(ctx context.Context, path string) (map[string]string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1&_pragma=trusted_schema(OFF)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, errors.New("invalid SQLite snapshot")
	}
	tables, err := db.QueryContext(ctx, "SELECT name, type, sql FROM sqlite_schema WHERE sql IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer tables.Close()
	expected := map[string]bool{"settings": true, "sessions": true, "schema_migrations": true}
	for tables.Next() {
		var name, kind, definition string
		if err := tables.Scan(&name, &kind, &definition); err != nil {
			return nil, err
		}
		if !expected[name] || kind != "table" || !strings.HasPrefix(definition, "CREATE TABLE "+name+" (") {
			return nil, errors.New("unsupported SQLite schema")
		}
		delete(expected, name)
	}
	if tables.Err() != nil || len(expected) != 0 {
		return nil, errors.New("incomplete SQLite schema")
	}
	if err := tables.Close(); err != nil {
		return nil, err
	}
	var version, migrations, first, sessions int
	if err := db.QueryRowContext(ctx, "SELECT max(version), count(*), min(version) FROM schema_migrations").Scan(&version, &migrations, &first); err != nil ||
		version != schemaVersion || migrations != schemaVersion || first != 1 {
		return nil, errors.New("unsupported SQLite version")
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		return nil, errors.New("invalid archived sessions")
	}
	rows, err := db.QueryContext(ctx, "SELECT key, value, typeof(key), typeof(value) FROM settings LIMIT 4097")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := make(map[string]string)
	for rows.Next() {
		var key, value, keyType, valueType string
		if err := rows.Scan(&key, &value, &keyType, &valueType); err != nil {
			return nil, err
		}
		if keyType != "text" || valueType != "text" || key == "" {
			return nil, errors.New("invalid archived setting")
		}
		if _, duplicate := settings[key]; duplicate || len(settings) == 4096 {
			return nil, errors.New("unexpected archived settings")
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if settings["admin.password_hash"] == "" || (settings["admin.must_change"] != "0" && settings["admin.must_change"] != "1") {
		return nil, errors.New("archive lacks initialized administrator")
	}
	return settings, nil
}

// WriteRestoredSettings replaces application settings in the caller's
// transaction, preserving local credentials and revoking all live sessions.
func WriteRestoredSettings(ctx context.Context, tx *sql.Tx, settings map[string]string, id string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key NOT GLOB 'admin.*'"); err != nil {
		return err
	}
	for key, value := range settings {
		if strings.HasPrefix(key, "admin.") || strings.HasPrefix(key, "restore.") {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?)", key, value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?)", restoreKey, id)
	return err
}

// RestoreWasCommitted uses SQLite as the durable commit decision for recovery.
func RestoreWasCommitted(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", restoreKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return value == id, err
}
