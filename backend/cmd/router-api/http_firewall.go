package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
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

func (a *api) firewallRuleSettings(w http.ResponseWriter, r *http.Request) {
	request := privilegedagent.Request{Operation: "firewall-rules-status"}
	if r.Method == http.MethodPut {
		if !a.authorizeNetworkMutation(w, r) {
			return
		}
		var rules network.FirewallRules
		if decodeJSON(w, r, &rules) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_firewall_rules"})
			return
		}
		request.Operation = "firewall-rules-apply"
		request.FirewallRules = &rules
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
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), request)
	switch {
	case err == nil && response.RuleSettings != nil:
		writeJSON(w, http.StatusOK, response.RuleSettings)
	case err == nil && response.StaticWANTransaction != nil:
		writeJSON(w, http.StatusAccepted, response.StaticWANTransaction)
	case err == nil && response.Error == "invalid_firewall_rules":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": response.Error})
	case err == nil && response.Error == "network_change_pending":
		writeJSON(w, http.StatusConflict, map[string]string{"error": response.Error})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "firewall_unavailable"})
	}
}
