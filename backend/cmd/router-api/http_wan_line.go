package main

import (
	"context"
	"net/http"
	"time"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) wanLineStatus(w http.ResponseWriter, r *http.Request) {
	_, ok, err := a.validSession(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	response, err := privilegedagent.SendWANLine(ctx, agentSocketPath(), nil)
	if err != nil || response.Error != "" || response.WANLineStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "wan_line_status_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, response.WANLineStatus)
}

func (a *api) applyWANLineSettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var config networkinventory.WANLineConfig
	if decodeJSON(w, r, &config) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := networkinventory.ValidateWANLine(config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendWANLine(ctx, agentSocketPath(), &config)
	if err != nil || response.Error != "" || response.StaticWANTransaction == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "wan_line_apply_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, response.StaticWANTransaction)
}
