package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"golang.org/x/term"
)

func adminCommand(db *sql.DB, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: router-api [admin-status|admin-init|admin-login|admin-reset|tls-certificate]")
	}
	ctx := context.Background()
	switch args[0] {
	case "admin-status":
		exists, err := admin.Exists(ctx, db)
		if err != nil {
			return err
		}
		if exists {
			fmt.Fprintln(os.Stdout, "initialized")
		} else {
			fmt.Fprintln(os.Stdout, "missing")
		}
		return nil
	case "admin-init":
		exists, err := admin.Exists(ctx, db)
		if err != nil {
			return err
		}
		if exists {
			fmt.Fprintln(os.Stdout, "Administrator already initialized.")
			return nil
		}
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("an interactive terminal is required to show the initial password")
		}
		created, err := admin.Initialize(ctx, db, func(secret string) error {
			_, err := fmt.Fprintf(os.Stdout,
				"Initial password for admin: %s\nSave it now; it will not be shown again.\n", secret)
			return err
		})
		if err != nil {
			return err
		}
		if !created {
			fmt.Fprintln(os.Stdout, "Administrator already initialized.")
		}
		return nil
	case "admin-reset":
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("an interactive terminal is required to show the new password")
		}
		return admin.Reset(ctx, db, func(secret string) error {
			_, err := fmt.Fprintf(os.Stdout,
				"New password for admin: %s\nSave it now; it will not be shown again.\n", secret)
			return err
		})
	case "admin-login":
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("an interactive terminal is required for local login")
		}
		password, err := readPassword("Admin password: ")
		if err != nil {
			return err
		}
		mustChange, err := admin.Authenticate(ctx, db, password)
		if err != nil {
			return err
		}
		if !mustChange {
			fmt.Fprintln(os.Stdout, "Login successful.")
			return nil
		}
		fmt.Fprintln(os.Stdout, "Password change required on first login.")
		replacement, err := readPassword("New password (12-1024 bytes): ")
		if err != nil {
			return err
		}
		confirmation, err := readPassword("Repeat new password: ")
		if err != nil {
			return err
		}
		if replacement != confirmation {
			return errors.New("new passwords do not match")
		}
		if err := admin.ChangePassword(ctx, db, password, replacement); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Password changed. Login successful.")
		return nil
	case "tls-certificate":
		dataDir := os.Getenv("STATE_DIRECTORY")
		if dataDir == "" {
			dataDir = "."
		}
		certPath, _, _, err := loadTLSCertificate(filepath.Clean(dataDir))
		if err != nil {
			return err
		}
		certificate, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read TLS certificate: %w", err)
		}
		_, err = os.Stdout.Write(certificate)
		return err
	default:
		return errors.New("usage: router-api [admin-status|admin-init|admin-login|admin-reset|tls-certificate]")
	}
}

func readPassword(prompt string) (string, error) {
	if _, err := fmt.Fprint(os.Stdout, prompt); err != nil {
		return "", err
	}
	bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return "", err
	}
	password := string(bytes)
	for i := range bytes {
		bytes[i] = 0
	}
	return password, nil
}
