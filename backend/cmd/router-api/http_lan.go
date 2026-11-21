package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) readLANSettings(w http.ResponseWriter, r *http.Request) {
	_, ok, err := a.validSession(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{Operation: "lan-status"})
	if err != nil || response.LANStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lan_status_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, response.LANStatus)
}

func (a *api) applyLANSettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var config network.LANConfig
	if decodeJSON(w, r, &config) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := network.ValidateLAN(config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{Operation: "lan-apply", LANConfig: &config})
	switch {
	case response.LANConflict != nil:
		writeJSON(w, http.StatusConflict, struct {
			Error string `json:"error"`
			*network.LANConflict
		}{"lan_overlap", response.LANConflict})
	case response.Error == "network_change_pending", response.Error == "lan_reservation_conflict",
		response.Error == "lan_dns_address_conflict", response.Error == "lan_firewall_target_conflict":
		writeJSON(w, http.StatusConflict, map[string]string{"error": response.Error})
	case err != nil || response.StaticWANTransaction == nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lan_apply_failed"})
	default:
		writeJSON(w, http.StatusAccepted, response.StaticWANTransaction)
	}
}

func (a *api) confirmLANSettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var request struct {
		TransactionID string `json:"transaction_id"`
	}
	if decodeJSON(w, r, &request) != nil || !validStaticWANTransactionID(request.TransactionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	destination := lanRequestAddress(r)
	if destination == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "lan_confirmation_requires_new_address"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{
		Operation: "lan-confirm", TransactionID: request.TransactionID, LANDestination: destination,
	})
	if err != nil || response.Status != "confirmed" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "lan_confirmation_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

func lanRequestAddress(r *http.Request) string {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return ""
	}
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil || port != network.LANPort {
		return ""
	}
	return host
}
