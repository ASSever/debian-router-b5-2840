package main

import (
	"context"
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
	"sync"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

const sessionCookieName = "__Host-router_session"

type api struct {
	db                    *sql.DB
	proxyImportLock       sync.Mutex
	now                   func() time.Time
	readNetworkInterfaces func() ([]networkinventory.Interface, error)
	readNetworkDHCPStatus func(string) (networkinventory.DHCPStatus, error)
	applyStaticWAN        func(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error)
	confirmStaticWAN      func(context.Context, string) error
	rollbackStaticWAN     func(context.Context, string) error
	applyPPPoE            func(context.Context, networkinventory.PPPoEConfig) (networkinventory.PPPoETransaction, error)
	readPPPoEStatus       func(context.Context) (networkinventory.PPPoEStatus, error)
	confirmPPPoE          func(context.Context, string) error
	rollbackPPPoE         func(context.Context, string) error
	reconnectPPPoE        func(context.Context) error
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
		readDHCPStatusFromAgent,
	)
}

func readDHCPStatusFromAgent(interfaceName string) (networkinventory.DHCPStatus, error) {
	socketPath := os.Getenv("ROUTER_AGENT_SOCKET")
	if socketPath == "" {
		socketPath = privilegedagent.DefaultSocketPath()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := privilegedagent.SendDHCPStatus(ctx, socketPath, interfaceName)
	if err != nil || response.Error != "" || response.DHCPStatus == nil {
		return networkinventory.DHCPStatus{}, errors.New("read network status")
	}
	return *response.DHCPStatus, nil
}

func newAPIWithNetworkReaders(
	db *sql.DB,
	uiDirectory string,
	readNetworkInterfaces func() ([]networkinventory.Interface, error),
	readNetworkDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) http.Handler {
	return newAPIWithNetworkOperations(
		db,
		uiDirectory,
		readNetworkInterfaces,
		readNetworkDHCPStatus,
		applyStaticWANToAgent,
		confirmStaticWANAtAgent,
		rollbackStaticWANAtAgent,
	)
}

func newAPIWithNetworkOperations(
	db *sql.DB,
	uiDirectory string,
	readNetworkInterfaces func() ([]networkinventory.Interface, error),
	readNetworkDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	applyStaticWAN func(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error),
	confirmStaticWAN func(context.Context, string) error,
	rollbackStaticWAN func(context.Context, string) error,
) http.Handler {
	a := &api{
		db:                    db,
		now:                   time.Now,
		readNetworkInterfaces: readNetworkInterfaces,
		readNetworkDHCPStatus: readNetworkDHCPStatus,
		applyStaticWAN:        applyStaticWAN,
		confirmStaticWAN:      confirmStaticWAN,
		rollbackStaticWAN:     rollbackStaticWAN,
		applyPPPoE:            applyPPPoEToAgent,
		readPPPoEStatus:       readPPPoEStatusFromAgent,
		confirmPPPoE:          confirmPPPoEAtAgent,
		rollbackPPPoE:         rollbackPPPoEAtAgent,
		reconnectPPPoE:        reconnectPPPoEAtAgent,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /api/diagnostics/report", a.diagnosticReport(readAgentDiagnostics))
	mux.HandleFunc("POST /api/backup", a.backupDownload())
	mux.HandleFunc("GET /api/network/interfaces", a.networkInterfaces)
	mux.HandleFunc("GET /api/network/dhcp", a.networkDHCPStatus)
	mux.HandleFunc("POST /api/network/wan/static", a.applyStaticWANSettings)
	mux.HandleFunc("POST /api/network/wan/static/confirm", a.confirmStaticWANSettings)
	mux.HandleFunc("POST /api/network/wan/static/rollback", a.rollbackStaticWANSettings)
	mux.HandleFunc("GET /api/network/wan/line", a.wanLineStatus)
	mux.HandleFunc("POST /api/network/wan/diagnostic", a.wanDiagnostic)
	mux.HandleFunc("POST /api/network/wan/line", a.applyWANLineSettings)
	mux.HandleFunc("POST /api/network/wan/line/confirm", a.confirmStaticWANSettings)
	mux.HandleFunc("POST /api/network/wan/line/rollback", a.rollbackStaticWANSettings)
	mux.HandleFunc("GET /api/network/lan", a.readLANSettings)
	mux.HandleFunc("GET /api/network/lan/dns", a.readLANDNS)
	mux.HandleFunc("GET /api/network/firewall", a.firewallSettings)
	mux.HandleFunc("GET /api/network/traffic", a.trafficStatus)
	mux.HandleFunc("POST /api/network/firewall", a.firewallSettings)
	mux.HandleFunc("GET /api/network/firewall/rules", a.firewallRuleSettings)
	mux.HandleFunc("PUT /api/network/firewall/rules", a.firewallRuleSettings)
	mux.HandleFunc("GET /api/network/routing", a.routingSettings)
	mux.HandleFunc("PUT /api/network/routing", a.routingSettings)
	mux.HandleFunc("POST /api/network/firewall/confirm", a.confirmStaticWANSettings)
	mux.HandleFunc("POST /api/network/firewall/rollback", a.rollbackStaticWANSettings)
	mux.HandleFunc("PUT /api/network/lan/dns", a.changeLANDNS)
	mux.HandleFunc("POST /api/network/lan/dns/clear-cache", a.changeLANDNS)
	mux.HandleFunc("GET /api/network/lan/reservations", a.readDHCPReservations)
	mux.HandleFunc("POST /api/network/lan/reservations", a.changeDHCPReservation)
	mux.HandleFunc("PATCH /api/network/lan/reservations", a.changeDHCPReservation)
	mux.HandleFunc("DELETE /api/network/lan/reservations", a.changeDHCPReservation)
	mux.HandleFunc("POST /api/network/lan", a.applyLANSettings)
	mux.HandleFunc("POST /api/network/lan/confirm", a.confirmLANSettings)
	mux.HandleFunc("POST /api/network/lan/rollback", a.rollbackStaticWANSettings)
	mux.HandleFunc("GET /api/network/wan/pppoe", a.pppoeStatus)
	mux.HandleFunc("POST /api/network/wan/pppoe", a.applyPPPoESettings)
	mux.HandleFunc("POST /api/network/wan/pppoe/confirm", a.confirmPPPoESettings)
	mux.HandleFunc("POST /api/network/wan/pppoe/rollback", a.rollbackPPPoESettings)
	mux.HandleFunc("POST /api/network/wan/pppoe/reconnect", a.reconnectPPPoESettings)
	mux.HandleFunc("GET /api/proxy/subscription", a.readProxySubscription)
	mux.HandleFunc("POST /api/proxy/subscription", a.importProxySubscription)
	mux.HandleFunc("POST /api/proxy/subscription/check", a.checkProxyServer)
	mux.HandleFunc("GET /api/proxy/profile", a.proxyProfile)
	mux.HandleFunc("PUT /api/proxy/profile", a.proxyProfile)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/auth/session", a.sessionStatus)
	mux.HandleFunc("DELETE /api/auth/session", a.logout)
	mux.HandleFunc("PUT /api/auth/password", a.changePassword)
	ui := newUIHandler(uiDirectory)
	mux.Handle("GET /{$}", ui)
	mux.Handle("GET /assets/", ui)
	return requireHTTPS(securityHeaders(mux))
}

func applyStaticWANToAgent(ctx context.Context, config networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error) {
	socketPath := agentSocketPath()
	response, err := privilegedagent.SendApplyStaticWAN(ctx, socketPath, config)
	if err != nil || response.Error != "" || response.StaticWANTransaction == nil {
		return networkinventory.StaticWANTransaction{}, errors.New("apply static WAN configuration")
	}
	return *response.StaticWANTransaction, nil
}

func confirmStaticWANAtAgent(ctx context.Context, transactionID string) error {
	response, err := privilegedagent.SendConfirmStaticWAN(ctx, agentSocketPath(), transactionID)
	if err != nil || response.Error != "" || response.Status != "confirmed" {
		return errors.New("confirm static WAN configuration")
	}
	return nil
}

func rollbackStaticWANAtAgent(ctx context.Context, transactionID string) error {
	response, err := privilegedagent.SendRollbackStaticWAN(ctx, agentSocketPath(), transactionID)
	if err != nil || response.Error != "" || response.Status != "rolled_back" {
		return errors.New("roll back static WAN configuration")
	}
	return nil
}

func agentSocketPath() string {
	if socketPath := os.Getenv("ROUTER_AGENT_SOCKET"); socketPath != "" {
		return socketPath
	}
	return privilegedagent.DefaultSocketPath()
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
		"interface":   status.Interface,
		"link_state":  status.LinkState,
		"lease_state": status.LeaseState,
		"addresses":   status.Addresses,
		"gateway":     status.Gateway,
		"dns":         status.DNS,
	})
}

func (a *api) applyStaticWANSettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var config networkinventory.StaticWANConfig
	if err := decodeJSON(w, r, &config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := networkinventory.ValidateStaticWAN(config); err != nil {
		switch {
		case errors.Is(err, networkinventory.ErrStaticWANAddress):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_static_address"})
		case errors.Is(err, networkinventory.ErrStaticWANGateway):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_static_gateway"})
		case errors.Is(err, networkinventory.ErrStaticWANDNS):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_static_dns"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_static_config"})
		}
		return
	}
	if a.applyStaticWAN == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "static_wan_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	transaction, err := a.applyStaticWAN(ctx, config)
	if err != nil {
		log.Printf("apply static WAN configuration: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "static_wan_apply_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, transaction)
}

func (a *api) confirmStaticWANSettings(w http.ResponseWriter, r *http.Request) {
	a.finishStaticWANSettings(w, r, true)
}

func (a *api) rollbackStaticWANSettings(w http.ResponseWriter, r *http.Request) {
	a.finishStaticWANSettings(w, r, false)
}

func (a *api) finishStaticWANSettings(w http.ResponseWriter, r *http.Request, confirm bool) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var request struct {
		TransactionID string `json:"transaction_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil || !validStaticWANTransactionID(request.TransactionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	operation := a.rollbackStaticWAN
	result := "rolled_back"
	if confirm {
		operation = a.confirmStaticWAN
		result = "confirmed"
	}
	if operation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "static_wan_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	if err := operation(ctx, request.TransactionID); err != nil {
		log.Printf("%s static WAN configuration: %v", result, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "static_wan_transaction_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": result})
}

func (a *api) authorizeNetworkMutation(w http.ResponseWriter, r *http.Request) bool {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return false
	}
	return a.authorizeAdministrator(w, r)
}

func (a *api) authorizeAdministrator(w http.ResponseWriter, r *http.Request) bool {
	_, ok, err := a.validSession(r)
	if err != nil {
		log.Printf("read administrator session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return false
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	mustChange, err := admin.MustChange(r.Context(), a.db)
	if err != nil {
		log.Printf("read administrator password state: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return false
	}
	if mustChange {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "password_change_required"})
		return false
	}
	return true
}

func validStaticWANTransactionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, value := range id {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
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
	return decodeLimitedJSON(w, r, target, 8192)
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
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
