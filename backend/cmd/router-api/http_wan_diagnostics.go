package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

var wanDiagnosticLock sync.Mutex

func (a *api) wanDiagnostic(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeNetworkMutation(w, r) {
		return
	}
	if !wanDiagnosticLock.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "diagnostic_in_progress"})
		return
	}
	defer wanDiagnosticLock.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	stateCtx, stopState := context.WithTimeout(ctx, 4*time.Second)
	state, err := privilegedagent.SendLAN(stateCtx, agentSocketPath(), privilegedagent.Request{Operation: "wan-probe-state"})
	stopState()
	if err != nil || state.WANProbeState == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "diagnostic_unavailable"})
		return
	}
	report, err := network.RunWANDiagnostic(ctx, *state.WANProbeState)
	if err != nil || ctx.Err() != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "diagnostic_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, report)
}
