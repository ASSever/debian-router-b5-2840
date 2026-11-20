package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
)

func TestHealthRequiresHTTPS(t *testing.T) {
	db, _ := newAdministratorDB(t)
	handler := newAPI(db)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("HTTP health status = %d, want %d", response.Code, http.StatusUpgradeRequired)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.TLS = &tls.ConnectionState{}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTPS health status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestLoginRejectsHTTPAndCrossOriginRequests(t *testing.T) {
	db, password := newAdministratorDB(t)
	handler := newAPI(db)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	request.Header.Set("Origin", "https://router.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("HTTP login status = %d, want %d", response.Code, http.StatusUpgradeRequired)
	}
	if strings.Contains(response.Body.String(), password) {
		t.Fatal("HTTP rejection echoed the password")
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("HTTP login created %d sessions, err=%v", sessions, err)
	}

	server := httptest.NewTLSServer(handler)
	defer server.Close()
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	responseFromServer, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer responseFromServer.Body.Close()
	if responseFromServer.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login status = %d, want %d", responseFromServer.StatusCode, http.StatusForbidden)
	}
}

func TestSessionLifecycleAndPasswordChangeRevokesSessions(t *testing.T) {
	db, password := newAdministratorDB(t)
	server := httptest.NewTLSServer(newAPI(db))
	defer server.Close()
	client := server.Client()

	loginResponse := postLogin(t, client, server.URL, password)
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.StatusCode)
	}
	var loginBody struct {
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.NewDecoder(loginResponse.Body).Decode(&loginBody); err != nil {
		t.Fatal(err)
	}
	loginResponse.Body.Close()
	if !loginBody.MustChangePassword {
		t.Fatal("first login did not require a password change")
	}
	cookie := findSessionCookie(t, loginResponse.Cookies())
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unsafe session cookie attributes: %+v", cookie)
	}
	var storedHash []byte
	if err := db.QueryRow("SELECT token_hash FROM sessions").Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256([]byte(cookie.Value))
	if !bytes.Equal(storedHash, expectedHash[:]) {
		t.Fatal("session token was not stored as its SHA-256 digest")
	}

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/auth/session", nil)
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d", response.StatusCode)
	}
	response.Body.Close()

	const replacement = "a replacement password long enough"
	request, _ = http.NewRequest(http.MethodPut, server.URL+"/api/auth/password", strings.NewReader(`{"current_password":"`+password+`","new_password":"`+replacement+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("change password status = %d, body=%s", response.StatusCode, responseBody(t, response))
	}
	response.Body.Close()
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("password change left %d sessions, err=%v", sessions, err)
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/auth/session", nil)
	request.AddCookie(cookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	loginResponse = postLogin(t, client, server.URL, replacement)
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login after password change status = %d", loginResponse.StatusCode)
	}
	loginResponse.Body.Close()
	newCookie := findSessionCookie(t, loginResponse.Cookies())
	request, _ = http.NewRequest(http.MethodDelete, server.URL+"/api/auth/session", nil)
	request.Header.Set("Origin", server.URL)
	request.AddCookie(newCookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", response.StatusCode)
	}
	response.Body.Close()
	if len(response.Cookies()) == 0 || response.Cookies()[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the session cookie")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("logout left %d sessions, err=%v", sessions, err)
	}
}

func newAdministratorDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var password string
	created, err := admin.Initialize(t.Context(), db, func(secret string) error { password = secret; return nil })
	if err != nil || !created {
		t.Fatalf("initialize administrator: created=%t err=%v", created, err)
	}
	return db, password
}

func postLogin(t *testing.T, client *http.Client, baseURL, password string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", baseURL)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func findSessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatal("session cookie was not set")
	return nil
}

func responseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	return body.String()
}
