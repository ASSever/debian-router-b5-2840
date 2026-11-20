package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func applyPPPoEToAgent(ctx context.Context, config networkinventory.PPPoEConfig) (networkinventory.PPPoETransaction, error) {
	response, err := privilegedagent.SendApplyPPPoE(ctx, agentSocketPath(), config)
	if err != nil || response.Error != "" || response.PPPoETransaction == nil {
		return networkinventory.PPPoETransaction{}, errors.New("apply PPPoE configuration")
	}
	return *response.PPPoETransaction, nil
}

func readPPPoEStatusFromAgent(ctx context.Context) (networkinventory.PPPoEStatus, error) {
	response, err := privilegedagent.SendPPPoEStatus(ctx, agentSocketPath())
	if err != nil || response.Error != "" || response.PPPoEStatus == nil {
		return networkinventory.PPPoEStatus{}, errors.New("read PPPoE status")
	}
	return *response.PPPoEStatus, nil
}

func confirmPPPoEAtAgent(ctx context.Context, id string) error {
	response, err := privilegedagent.SendConfirmPPPoE(ctx, agentSocketPath(), id)
	if err != nil || response.Error != "" || response.Status != "confirmed" {
		return errors.New("confirm PPPoE configuration")
	}
	return nil
}

func rollbackPPPoEAtAgent(ctx context.Context, id string) error {
	response, err := privilegedagent.SendRollbackPPPoE(ctx, agentSocketPath(), id)
	if err != nil || response.Error != "" || response.Status != "rolled_back" {
		return errors.New("roll back PPPoE configuration")
	}
	return nil
}

func reconnectPPPoEAtAgent(ctx context.Context) error {
	response, err := privilegedagent.SendReconnectPPPoE(ctx, agentSocketPath())
	if err != nil || response.Error != "" || response.Status != "reconnecting" {
		return errors.New("reconnect PPPoE")
	}
	return nil
}

func (a *api) pppoeStatus(w http.ResponseWriter, r *http.Request) {
	_, ok, err := a.validSession(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if a.readPPPoEStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	status, err := a.readPPPoEStatus(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_status_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *api) applyPPPoESettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	var config networkinventory.PPPoEConfig
	if decodeJSON(w, r, &config) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := networkinventory.ValidatePPPoEConfig(config); err != nil {
		code := "invalid_pppoe_config"
		if errors.Is(err, networkinventory.ErrPPPoEUsername) {
			code = "invalid_pppoe_username"
		} else if errors.Is(err, networkinventory.ErrPPPoEPassword) {
			code = "invalid_pppoe_password"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
		return
	}
	if a.applyPPPoE == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	transaction, err := a.applyPPPoE(ctx, config)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_apply_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, transaction)
}

func (a *api) confirmPPPoESettings(w http.ResponseWriter, r *http.Request) {
	a.finishPPPoESettings(w, r, true)
}

func (a *api) rollbackPPPoESettings(w http.ResponseWriter, r *http.Request) {
	a.finishPPPoESettings(w, r, false)
}

func (a *api) finishPPPoESettings(w http.ResponseWriter, r *http.Request, confirm bool) {
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
	operation := a.rollbackPPPoE
	result := "rolled_back"
	if confirm {
		operation = a.confirmPPPoE
		result = "confirmed"
	}
	if operation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	if operation(ctx, request.TransactionID) != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pppoe_transaction_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": result})
}

func (a *api) reconnectPPPoESettings(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	if a.reconnectPPPoE == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pppoe_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	if a.reconnectPPPoE(ctx) != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pppoe_reconnect_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reconnecting"})
}
