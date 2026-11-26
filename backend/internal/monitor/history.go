package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const historyVersion = 1
const historyMaxAge = 7 * 24 * time.Hour
const historyMaxRows = 25_000

type History struct {
	db *sql.DB
}

func OpenHistory(ctx context.Context, dir string) (*History, error) {
	db, err := openDB(dir, "rwc")
	if err != nil {
		return nil, err
	}
	if err := initialize(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	return &History{db: db}, nil
}

func openDB(dir, mode string) (*sql.DB, error) {
	path, err := filepath.Abs(filepath.Join(dir, "monitor.db"))
	if err != nil {
		return nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=" + mode + "&_pragma=busy_timeout%285000%29"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func initialize(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 0:
		if _, err := tx.ExecContext(ctx, `CREATE TABLE checks (
			id INTEGER PRIMARY KEY,
			target TEXT NOT NULL,
			checked_at_ms INTEGER NOT NULL,
			result TEXT NOT NULL,
			dns TEXT NOT NULL,
			transport TEXT NOT NULL,
			duration_ms INTEGER NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "CREATE INDEX checks_by_time ON checks(checked_at_ms)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", historyVersion)); err != nil {
			return err
		}
	case historyVersion:
	default:
		return fmt.Errorf("unsupported monitor history version %d", version)
	}
	return tx.Commit()
}

func (h *History) Close() error { return h.db.Close() }

func (h *History) Save(ctx context.Context, samples []Sample, now time.Time) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO checks
			(target, checked_at_ms, result, dns, transport, duration_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			sample.Target, sample.CheckedAt.UnixMilli(), sample.Result, sample.DNS, sample.Transport, sample.DurationMS,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM checks
		WHERE checked_at_ms < ? OR id <= (SELECT MAX(id) FROM checks) - ?`,
		now.Add(-historyMaxAge).UnixMilli(), historyMaxRows,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func ReadHistory(ctx context.Context, dir string, limit int) ([]Sample, error) {
	if limit < 1 || limit > 10_000 {
		return nil, fmt.Errorf("history limit must be 1–10000")
	}
	db, err := openDB(dir, "ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != historyVersion {
		return nil, fmt.Errorf("unsupported monitor history version %d", version)
	}
	rows, err := db.QueryContext(ctx, `SELECT target, checked_at_ms, result, dns, transport, duration_ms
		FROM checks ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]Sample, 0, limit)
	for rows.Next() {
		var sample Sample
		var checkedAt int64
		if err := rows.Scan(&sample.Target, &checkedAt, &sample.Result, &sample.DNS, &sample.Transport, &sample.DurationMS); err != nil {
			return nil, err
		}
		sample.CheckedAt = time.UnixMilli(checkedAt).UTC()
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
