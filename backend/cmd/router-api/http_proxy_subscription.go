package main

import (
	"crypto/rand"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

func (a *api) readProxySubscription(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdministrator(w, r) {
		return
	}
	report, err := proxy.ReadReport(proxyStateDirectory())
	if err != nil {
		proxyImportError(w, http.StatusInternalServerError, "subscription_storage_failed")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (a *api) importProxySubscription(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return
	}
	if !a.authorizeAdministrator(w, r) {
		return
	}
	if !a.proxyImportLock.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "subscription_import_in_progress"})
		return
	}
	defer a.proxyImportLock.Unlock()
	var request struct {
		Source string `json:"source"`
	}
	if err := decodeLimitedJSON(w, r, &request, 2<<20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			proxyImportError(w, http.StatusRequestEntityTooLarge, "subscription_too_large")
		} else {
			proxyImportError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	if len(request.Source) > proxy.MaxSubscriptionBytes {
		proxyImportError(w, http.StatusRequestEntityTooLarge, "subscription_too_large")
		return
	}
	content := request.Source
	source := strings.TrimSpace(content)
	lowerSource := strings.ToLower(source)
	if !strings.ContainsAny(source, "\r\n") && (strings.HasPrefix(lowerSource, "https://") || strings.HasPrefix(lowerSource, "http://")) {
		var err error
		content, err = proxy.Fetch(r.Context(), source)
		if err != nil {
			writeProxySourceError(w, err)
			return
		}
	}
	subscription, err := proxy.Parse(content)
	if err != nil {
		writeProxySourceError(w, err)
		return
	}
	report := subscription.Report
	log.Printf("proxy subscription parsed: imported=%d rejected=%d", report.Imported, report.Rejected)
	if report.Imported == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, report)
		return
	}
	report.ID = rand.Text()
	subscription.Report = report
	if err := proxy.Save(proxyStateDirectory(), subscription); err != nil {
		proxyImportError(w, http.StatusInternalServerError, "subscription_storage_failed")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func writeProxySourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxy.ErrInvalidURL):
		proxyImportError(w, http.StatusBadRequest, "invalid_subscription_url")
	case errors.Is(err, proxy.ErrFetchFailed):
		proxyImportError(w, http.StatusBadGateway, "subscription_fetch_failed")
	case errors.Is(err, proxy.ErrSubscriptionTooLarge):
		proxyImportError(w, http.StatusRequestEntityTooLarge, "subscription_too_large")
	default:
		proxyImportError(w, http.StatusBadRequest, "invalid_subscription")
	}
}

func proxyImportError(w http.ResponseWriter, status int, code string) {
	log.Printf("proxy subscription: %s", code)
	writeJSON(w, status, map[string]string{"error": code})
}

func proxyStateDirectory() string {
	if path := os.Getenv("STATE_DIRECTORY"); path != "" {
		return path
	}
	return "."
}
