package main

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/metrics"
)

func withDashboard(handler http.Handler, db *sql.DB, store *metrics.Store) http.Handler {
	a := &api{db: db, now: time.Now}
	mux := http.NewServeMux()
	mux.Handle("GET /api/dashboard", securityHeaders(requireHTTPS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.authorizeAdministrator(w, r) {
			return
		}
		if store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dashboard_unavailable"})
			return
		}
		recent, err := store.Recent(r.Context(), 60)
		if err != nil {
			log.Printf("read metrics history: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dashboard_unavailable"})
			return
		}
		stale := len(recent) != 0 && time.Since(recent[len(recent)-1].At) > 3*metrics.Interval
		writeJSON(w, http.StatusOK, struct {
			Stale  bool            `json:"stale"`
			Points []metrics.Point `json:"points"`
		}{Stale: stale, Points: recent})
	}))))
	mux.Handle("/", handler)
	return mux
}
