package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

const sessionCookieName = "__Host-router_session"

type api struct {
	db                    *sql.DB
	now                   func() time.Time
	readNetworkInterfaces func() ([]networkinventory.Interface, error)
	readNetworkDHCPStatus func(string) (networkinventory.DHCPStatus, error)
}

func newAPI(db *sql.DB) http.Handler {
	return newAPIWithUI(db, os.Getenv("ROUTER_UI_DIR"))
}

func newAPIWithUI(db *sql.DB, uiDirectory string) http.Handler {
	return newAPIWithInterfaceReader(db, uiDirectory, networkinventory.ReadInterfaces)
}

func newAPIWithInterfaceReader(
	db *sql.DB,
	uiDirectory string,
	readNetworkInterfaces func() ([]networkinventory.Interface, error),
) http.Handler {
	return newAPIWithNetworkReaders(
		db,
		uiDirectory,
		readNetworkInterfaces,
		networkinventory.ReadDHCPStatus,
	)
}

func newAPIWithNetworkReaders(
	db *sql.DB,
	uiDirectory string,
	readNetworkInterfaces func() ([]networkinventory.Interface, error),
	readNetworkDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) http.Handler {
	a := &api{
		db:                    db,
		now:                   time.Now,
		readNetworkInterfaces: readNetworkInterfaces,
		readNetworkDHCPStatus: readNetworkDHCPStatus,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /api/network/interfaces", a.networkInterfaces)
	mux.HandleFunc("GET /api/network/dhcp", a.networkDHCPStatus)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/auth/session", a.sessionStatus)
	mux.HandleFunc("DELETE /api/auth/session", a.logout)
	mux.HandleFunc("PUT /api/auth/password", a.changePassword)
	ui := newUIHandler(uiDirectory)
	mux.Handle("GET /{$}", ui)
	mux.Handle("GET /assets/", ui)
	return requireHTTPS(securityHeaders(mux))
}

func (a *api) networkDHCPStatus(w http.ResponseWriter, r *http.Request) {
	interfaceName := r.URL.Query().Get("interface")
	if !networkinventory.ValidInterfaceName(interfaceName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_interface"})
		return
	}
	status, err := a.readNetworkDHCPStatus(interfaceName)
	if err != nil {
		log.Printf("read DHCP status for interface %s: %v", interfaceName, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "wan_dhcp_status_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interface":  status.Interface,
		"link_state": status.LinkState,
		"lease_state": status.LeaseState,
		"addresses":  status.Addresses,
		"gateway":    status.Gateway,
		"dns":        status.DNS,
	})
}

func (a *api) networkInterfaces(w http.ResponseWriter, _ *http.Request) {
	interfaces, err := a.readNetworkInterfaces()
	if err != nil {
		log.Printf("read network interfaces: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "network_interfaces_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Interfaces []networkinventory.Interface `json:"interfaces"`
	}{Interfaces: interfaces})
}

func newUIHandler(directory string) http.Handler {
	if directory == "" {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(directory, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

func requireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			writeJSON(w, http.StatusUpgradeRequired, map[string]string{"error": "https_required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("authenticate administrator: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	defer tx.Rollback()
	mustChange, err := admin.AuthenticateTx(r.Context(), tx, request.Password)
	if err != nil {
		if errors.Is(err, admin.ErrInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}
		log.Printf("authenticate administrator: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	token, err := authsession.CreateTx(r.Context(), tx, a.now())
	if err != nil {
		log.Printf("create administrator session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("commit administrator session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	setSessionCookie(w, token, a.now())
	writeJSON(w, http.StatusOK, map[string]bool{"must_change_password": mustChange})
}

func (a *api) sessionStatus(w http.ResponseWriter, r *http.Request) {
	_, ok, err := a.validSession(r)
	if err != nil {
		log.Printf("read administrator session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	mustChange, err := admin.MustChange(r.Context(), a.db)
	if err != nil {
		log.Printf("read administrator password state: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true, "must_change_password": mustChange})
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := authsession.Revoke(r.Context(), a.db, cookie.Value); err != nil {
			log.Printf("revoke administrator session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) changePassword(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	_, ok, err := a.validSession(r)
	if err != nil {
		log.Printf("read administrator session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := admin.ChangePassword(r.Context(), a.db, request.CurrentPassword, request.NewPassword); err != nil {
		switch {
		case errors.Is(err, admin.ErrInvalidCredentials):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		case errors.Is(err, admin.ErrPasswordLength), errors.Is(err, admin.ErrPasswordUnchanged):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_new_password"})
		default:
			log.Printf("change administrator password: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		}
		return
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) validSession(r *http.Request) (string, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false, nil
	}
	valid, err := authsession.Valid(r.Context(), a.db, cookie.Value, a.now())
	if err != nil || !valid {
		return "", false, err
	}
	return cookie.Value, true, nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Host, r.Host) &&
		parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func setSessionCookie(w http.ResponseWriter, token string, now time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  now.Add(authsession.Lifetime),
		MaxAge:   int(authsession.Lifetime.Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write API response: %v", err)
	}
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
