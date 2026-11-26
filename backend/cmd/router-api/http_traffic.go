package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) trafficStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, ok, err := a.validSession(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendRequest(ctx, agentSocketPath(), "traffic-status")
	if err != nil || response.Traffic == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "traffic_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, response.Traffic)
}
