package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) firewallSettings(w http.ResponseWriter, r *http.Request) {
	operation := "firewall-status"
	if r.Method == http.MethodPost {
		if !a.authorizeNetworkMutation(w, r) {
			return
		}
		operation = "firewall-apply"
	} else {
		_, ok, err := a.validSession(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{Operation: operation})
	switch {
	case err == nil && response.Firewall != nil:
		writeJSON(w, http.StatusOK, response.Firewall)
	case err == nil && response.StaticWANTransaction != nil:
		writeJSON(w, http.StatusAccepted, response.StaticWANTransaction)
	case err == nil && response.Error == "network_change_pending":
		writeJSON(w, http.StatusConflict, map[string]string{"error": response.Error})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "firewall_unavailable"})
	}
}
