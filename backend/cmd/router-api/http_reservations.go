package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) readDHCPReservations(w http.ResponseWriter, r *http.Request) {
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
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{Operation: "reservation-list"})
	if err != nil || response.Reservations == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reservations_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, response.Reservations)
}

func (a *api) changeDHCPReservation(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var change network.ReservationChange
	if decodeJSON(w, r, &change) != nil ||
		(r.Method == http.MethodPost) != (change.PreviousMAC == "") ||
		(r.Method == http.MethodDelete) != (change.Reservation == nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_reservation"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), privilegedagent.Request{
		Operation: "reservation-change", ReservationChange: &change,
	})
	if err == nil && response.Status == "saved" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	status := http.StatusServiceUnavailable
	switch response.Error {
	case "invalid_reservation":
		status = http.StatusBadRequest
	case "reservation_conflict", "network_change_pending", "reservation_limit":
		status = http.StatusConflict
	case "reservation_missing":
		status = http.StatusNotFound
	default:
		response.Error = "reservations_unavailable"
	}
	writeJSON(w, status, map[string]string{"error": response.Error})
}
