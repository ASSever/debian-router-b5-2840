package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) routingSettings(w http.ResponseWriter, r *http.Request) {
	request := privilegedagent.Request{Operation: "routing-status"}
	if r.Method == http.MethodPut {
		if !a.authorizeNetworkMutation(w, r) {
			return
		}
		var config network.RoutingConfig
		if decodeJSON(w, r, &config) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_routing"})
			return
		}
		request.Operation, request.RoutingConfig = "routing-apply", &config
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
	case err == nil && response.RoutingStatus != nil:
		writeJSON(w, http.StatusOK, response.RoutingStatus)
	case err == nil && response.StaticWANTransaction != nil:
		writeJSON(w, http.StatusAccepted, response.StaticWANTransaction)
	case response.Error == "invalid_routing", response.Error == "routing_gateway_unreachable":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": response.Error})
	case response.Error == "network_change_pending", response.Error == "routing_overlap",
		response.Error == "routing_resources_in_use", response.Error == "routing_requires_wan":
		writeJSON(w, http.StatusConflict, map[string]string{"error": response.Error})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "routing_unavailable"})
	}
}
