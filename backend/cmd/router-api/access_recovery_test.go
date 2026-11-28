package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/metrics"
)

func TestLocalAdminResetRevokesExistingHTTPSAccess(t *testing.T) {
	db, initialPassword := newAdministratorDB(t)
	const oldPassword = "admin password before reset"
	if err := admin.ChangePassword(t.Context(), db, initialPassword, oldPassword); err != nil {
		t.Fatal(err)
	}
	store, err := metrics.Open(t.Context(), filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewTLSServer(withDashboard(newAPI(db), db, store))
	defer server.Close()
	client := server.Client()

	login := func(password string, wantStatus int, wantChange bool) *http.Cookie {
		t.Helper()
		response := postLogin(t, client, server.URL, password)
		defer response.Body.Close()
		if response.StatusCode != wantStatus {
			t.Fatalf("login status = %d, want %d", response.StatusCode, wantStatus)
		}
		if wantStatus != http.StatusOK {
			return nil
		}
		var body struct {
			MustChangePassword bool `json:"must_change_password"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.MustChangePassword != wantChange {
			t.Fatalf("must_change_password = %t, want %t", body.MustChangePassword, wantChange)
		}
		return findSessionCookie(t, response.Cookies())
	}
	checkRequest := func(method, endpoint, body string, cookie *http.Cookie, wantStatus int) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(cookie)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", server.URL)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != wantStatus {
			t.Fatalf("%s %s status = %d, want %d", method, endpoint, response.StatusCode, wantStatus)
		}
	}

	first := login(oldPassword, http.StatusOK, false)
	second := login(oldPassword, http.StatusOK, false)
	const session = "/api/auth/session"
	const dashboard = "/api/dashboard"
	checkRequest(http.MethodGet, dashboard, "", first, http.StatusOK)
	checkRequest(http.MethodGet, dashboard, "", second, http.StatusOK)

	var temporaryPassword string
	if err := admin.Reset(t.Context(), db, func(password string) error {
		temporaryPassword = password
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checkRequest(http.MethodGet, session, "", first, http.StatusUnauthorized)
	checkRequest(http.MethodGet, session, "", second, http.StatusUnauthorized)
	login(oldPassword, http.StatusUnauthorized, false)
	temporary := login(temporaryPassword, http.StatusOK, true)
	checkRequest(http.MethodGet, dashboard, "", temporary, http.StatusForbidden)

	const replacement = "admin password after reset"
	body, err := json.Marshal(map[string]string{
		"current_password": temporaryPassword,
		"new_password":     replacement,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRequest(http.MethodPut, "/api/auth/password", string(body), temporary, http.StatusNoContent)
	checkRequest(http.MethodGet, session, "", temporary, http.StatusUnauthorized)
	login(temporaryPassword, http.StatusUnauthorized, false)
	current := login(replacement, http.StatusOK, false)
	checkRequest(http.MethodGet, dashboard, "", current, http.StatusOK)
}
