package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

func TestDiagnosticReportRequiresAnAdministratorWithChangedPassword(t *testing.T) {
	db, password := newAdministratorDB(t)
	token, err := authsession.Create(t.Context(), db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a := &api{db: db, now: time.Now}
	handler := requireHTTPS(a.diagnosticReport(func(context.Context) *privilegedagent.DiagnosticSnapshot {
		return &privilegedagent.DiagnosticSnapshot{
			JournalStatus: "available",
			Events:        []privilegedagent.DiagnosticEvent{},
		}
	}))
	request := func(token string, https bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/diagnostics/report", nil)
		if https {
			r.TLS = &tls.ConnectionState{}
		}
		if token != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		token string
		https bool
		want  int
	}{
		{token, false, http.StatusUpgradeRequired},
		{"", true, http.StatusUnauthorized},
		{token, true, http.StatusForbidden},
	} {
		if got := request(test.token, test.https); got.Code != test.want || got.Header().Get("Content-Disposition") != "" {
			t.Errorf("status=%d, want %d without an attachment", got.Code, test.want)
		}
	}
	if err := admin.ChangePassword(t.Context(), db, password, rand.Text()); err != nil {
		t.Fatal(err)
	}
	token, err = authsession.Create(t.Context(), db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := request(token, true)
	if got.Code != http.StatusOK ||
		got.Header().Get("Content-Disposition") != `attachment; filename="router-failure-report.json"` ||
		got.Header().Get("Cache-Control") != "no-store" || got.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected attachment response: status=%d headers=%v", got.Code, got.Header())
	}
	var report struct {
		SchemaVersion int `json:"schema_version"`
		privilegedagent.DiagnosticSnapshot
	}
	if err := json.Unmarshal(got.Body.Bytes(), &report); err != nil ||
		report.SchemaVersion != 1 || report.JournalStatus != "available" || report.Events == nil {
		t.Fatal("attachment does not contain the versioned diagnostic snapshot")
	}
}
