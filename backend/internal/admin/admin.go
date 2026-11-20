package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordKey   = "admin.password_hash"
	mustChangeKey = "admin.must_change"
	minPassword   = 12
	maxPassword   = 1024
	saltBytes     = 16
	hashBytes     = 32
	argonTime     = 3
	argonMemory   = 64 * 1024
	argonThreads  = 4
)

var (
	// ErrInvalidCredentials hides whether an administrator account exists.
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	// ErrPasswordLength indicates that a new password is outside the accepted range.
	ErrPasswordLength = errors.New("password must contain 12 to 1024 bytes")
	// ErrPasswordUnchanged indicates that a replacement matches the current password.
	ErrPasswordUnchanged = errors.New("new password must differ from the current password")
)

// Exists reports whether the local administrator has been initialized.
func Exists(ctx context.Context, db *sql.DB) (bool, error) {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Initialize creates the administrator and presents its random password once.
// Presentation happens inside the transaction so a failed write cannot leave an
// account whose initial password was never shown.
func Initialize(ctx context.Context, db *sql.DB, present func(string) error) (bool, error) {
	if present == nil {
		return false, errors.New("password presentation is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&existing)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	password, hash, err := newPassword()
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key, value) VALUES (?, ?), (?, ?)",
		passwordKey, hash, mustChangeKey, "1"); err != nil {
		return false, err
	}
	if err := present(password); err != nil {
		return false, fmt.Errorf("present administrator password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// Reset replaces a lost password locally and requires a change at next login.
func Reset(ctx context.Context, db *sql.DB, present func(string) error) error {
	if present == nil {
		return errors.New("password presentation is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredentials
		}
		return err
	}
	password, hash, err := newPassword()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET value = ? WHERE key = ?", hash, passwordKey); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE settings SET value = '1' WHERE key = ?", mustChangeKey)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("administrator change flag is missing")
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("revoke sessions after password reset: %w", err)
	}
	if err := present(password); err != nil {
		return fmt.Errorf("present administrator password: %w", err)
	}
	return tx.Commit()
}

// Authenticate checks a password and reports whether it must be changed.
func Authenticate(ctx context.Context, db *sql.DB, password string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	mustChange, err := AuthenticateTx(ctx, tx, password)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return mustChange, nil
}

// AuthenticateTx checks a password using the caller's transaction. Browser login
// uses the same transaction to insert a session, so a concurrent password reset
// cannot create a valid session from a password that has already been replaced.
func AuthenticateTx(ctx context.Context, tx *sql.Tx, password string) (bool, error) {
	if len(password) > maxPassword {
		return false, ErrInvalidCredentials
	}
	hash, mustChange, err := readAccount(ctx, tx)
	if err != nil {
		return false, err
	}
	valid, err := verifyPassword(hash, password)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, ErrInvalidCredentials
	}
	return mustChange, nil
}

// ChangePassword verifies the current password and clears the first-login flag.
func ChangePassword(ctx context.Context, db *sql.DB, current, replacement string) error {
	if len(current) > maxPassword {
		return ErrInvalidCredentials
	}
	if len(replacement) < minPassword || len(replacement) > maxPassword {
		return ErrPasswordLength
	}
	if current == replacement {
		return ErrPasswordUnchanged
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hash string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredentials
		}
		return err
	}
	valid, err := verifyPassword(hash, current)
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidCredentials
	}
	newHash, err := hashPassword(replacement)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET value = ? WHERE key = ?", newHash, passwordKey); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE settings SET value = '0' WHERE key = ?", mustChangeKey)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("administrator change flag is missing")
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	return tx.Commit()
}

// MustChange reports whether the administrator must replace the initial or reset password.
func MustChange(ctx context.Context, db *sql.DB) (bool, error) {
	var flag string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", mustChangeKey).Scan(&flag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrInvalidCredentials
		}
		return false, err
	}
	switch flag {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, errors.New("invalid administrator change flag")
	}
}

func readAccount(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, bool, error) {
	var hash, flag string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", passwordKey).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, ErrInvalidCredentials
		}
		return "", false, err
	}
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", mustChangeKey).Scan(&flag); err != nil {
		return "", false, err
	}
	if flag != "0" && flag != "1" {
		return "", false, errors.New("invalid administrator change flag")
	}
	return hash, flag == "1", nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, hashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func newPassword() (string, string, error) {
	secret := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return "", "", fmt.Errorf("generate administrator password: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	for i := range secret {
		secret[i] = 0
	}
	hash, err := hashPassword(password)
	return password, hash, err
}

func verifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("invalid administrator password hash")
	}
	var memory, timeCost, threads uint64
	params := strings.Split(parts[3], ",")
	if len(params) != 3 || !strings.HasPrefix(params[0], "m=") ||
		!strings.HasPrefix(params[1], "t=") || !strings.HasPrefix(params[2], "p=") {
		return false, errors.New("invalid administrator password parameters")
	}
	var err error
	if memory, err = strconv.ParseUint(params[0][2:], 10, 32); err != nil {
		return false, err
	}
	if timeCost, err = strconv.ParseUint(params[1][2:], 10, 32); err != nil {
		return false, err
	}
	if threads, err = strconv.ParseUint(params[2][2:], 10, 8); err != nil {
		return false, err
	}
	if memory < 8*1024 || memory > 256*1024 || timeCost < 1 || timeCost > 10 ||
		threads < 1 || threads > 8 {
		return false, errors.New("administrator password cost is out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltBytes {
		return false, errors.New("invalid administrator password salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != hashBytes {
		return false, errors.New("invalid administrator password digest")
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(timeCost), uint32(memory), uint8(threads), hashBytes)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
