package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) readLANDNS(w http.ResponseWriter, r *http.Request) {
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
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{Operation: "lan-dns-status"})
	if err != nil || response.DNSStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lan_dns_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, response.DNSStatus)
}

func (a *api) changeLANDNS(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	request := privilegedagent.Request{Operation: "lan-dns-clear"}
	if r.Method == http.MethodPut {
		var config network.LANDNSConfig
		if decodeJSON(w, r, &config) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_lan_dns"})
			return
		}
		request.Operation = "lan-dns-save"
		request.DNSConfig = &config
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), request)
	if err == nil && response.Status == "saved" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	status := http.StatusServiceUnavailable
	switch response.Error {
	case "invalid_lan_dns":
		status = http.StatusBadRequest
	case "network_change_pending":
		status = http.StatusConflict
	default:
		response.Error = "lan_dns_unavailable"
	}
	writeJSON(w, status, map[string]string{"error": response.Error})
}
