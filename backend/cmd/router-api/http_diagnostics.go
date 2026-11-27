package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func (a *api) diagnosticReport(read func(context.Context) *privilegedagent.DiagnosticSnapshot) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.authorizeAdministrator(w, r) {
			return
		}
		snapshot := read(r.Context())
		if snapshot == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "diagnostic_report_unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="router-failure-report.json"`)
		report := struct {
			SchemaVersion int       `json:"schema_version"`
			GeneratedAt   time.Time `json:"generated_at"`
			*privilegedagent.DiagnosticSnapshot
		}{1, a.now().UTC(), snapshot}
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
	}
}

func readAgentDiagnostics(ctx context.Context) *privilegedagent.DiagnosticSnapshot {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := privilegedagent.SendRequest(ctx, agentSocketPath(), "diagnostic-report")
	if err != nil || response.Error != "" {
		return nil
	}
	return response.Diagnostic
}
