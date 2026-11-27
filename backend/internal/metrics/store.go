package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const Interval = 30 * time.Second
const maxRows = 2880
const SchemaVersion = 1

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
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
		if _, err := tx.ExecContext(ctx, `CREATE TABLE samples (
			id INTEGER PRIMARY KEY,
			sampled_at_ms INTEGER NOT NULL,
			payload TEXT NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "CREATE INDEX samples_by_time ON samples(sampled_at_ms, id)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
			return err
		}
	case SchemaVersion:
	default:
		return fmt.Errorf("unsupported metrics schema %d", version)
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Save(ctx context.Context, point Point) error {
	payload, err := json.Marshal(point)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO samples(sampled_at_ms, payload) VALUES (?, ?)", point.At.UnixMilli(), string(payload)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM samples WHERE sampled_at_ms < ? OR
		id <= (SELECT MAX(id) FROM samples) - ?`, point.At.Add(-24*time.Hour).UnixMilli(), maxRows); err != nil {
		return err
	}
	return tx.Commit()
}

// Recent returns the latest limited window in chronological order.
func (s *Store) Recent(ctx context.Context, limit int) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM
		(SELECT id, sampled_at_ms, payload FROM samples ORDER BY sampled_at_ms DESC, id DESC LIMIT ?)
		ORDER BY sampled_at_ms, id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	points := make([]Point, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var point Point
		if err := json.Unmarshal(payload, &point); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}
