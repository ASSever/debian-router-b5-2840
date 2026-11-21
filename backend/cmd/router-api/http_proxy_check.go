package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

var singBoxVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func (a *api) checkProxyServer(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	if !a.authorizeAdministrator(w, r) {
		return
	}
	var request struct {
		Line           int    `json:"line"`
		SubscriptionID string `json:"subscription_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil || request.Line < 1 || len(request.SubscriptionID) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if !a.proxyImportLock.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "subscription_busy"})
		return
	}
	defer a.proxyImportLock.Unlock()
	server, err := proxy.ReadServer(proxyStateDirectory(), request.SubscriptionID, request.Line)
	switch {
	case errors.Is(err, proxy.ErrSubscriptionChanged):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "subscription_changed"})
	case errors.Is(err, proxy.ErrServerNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server_not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "subscription_storage_failed"})
	default:
		result := proxy.CheckServer(r.Context(), installedSingBox(), server)
		log.Printf("proxy server check: line=%d status=%s reason=%s elapsed_ms=%d", result.Line, result.Status, result.Reason, result.ElapsedMS)
		writeJSON(w, http.StatusOK, result)
	}
}

func installedSingBox() string {
	const prefix = "/opt/debian-router-b5-2840"
	marker, err := os.ReadFile(filepath.Join(prefix, "current", "SING_BOX_SUPPORT"))
	if err != nil {
		return ""
	}
	version, _, _ := strings.Cut(string(marker), "\n")
	if !singBoxVersion.MatchString(version) {
		return ""
	}
	return filepath.Join(prefix, "sing-box", version, "sing-box")
}
