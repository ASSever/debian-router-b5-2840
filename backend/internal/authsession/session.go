// Package authsession stores opaque browser sessions without persisting bearer tokens.
package authsession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"time"
)

const Lifetime = 12 * time.Hour

// Create generates a bearer token and persists only its SHA-256 digest.
func Create(ctx context.Context, db *sql.DB, now time.Time) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	token, err := CreateTx(ctx, tx, now)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

// CreateTx inserts a new session in the caller's transaction.
func CreateTx(ctx context.Context, tx *sql.Tx, now time.Time) (string, error) {
	secret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	for i := range secret {
		secret[i] = 0
	}
	digest := sha256.Sum256([]byte(token))
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now.Unix()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sessions(token_hash, expires_at) VALUES (?, ?)", digest[:], now.Add(Lifetime).Unix()); err != nil {
		return "", err
	}
	return token, nil
}

// Valid reports whether token identifies an unexpired session.
func Valid(ctx context.Context, db *sql.DB, token string, now time.Time) (bool, error) {
	if token == "" || len(token) > 128 {
		return false, nil
	}
	digest := sha256.Sum256([]byte(token))
	var found int
	err := db.QueryRowContext(ctx,
		"SELECT 1 FROM sessions WHERE token_hash = ? AND expires_at > ?", digest[:], now.Unix()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Revoke removes a session if it exists. Unknown tokens are harmless.
func Revoke(ctx context.Context, db *sql.DB, token string) error {
	if token == "" || len(token) > 128 {
		return nil
	}
	digest := sha256.Sum256([]byte(token))
	_, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", digest[:])
	return err
}
