package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
)

func TestSubscriptionImportPreservesSavedRowsAndRedactsSecrets(t *testing.T) {
	t.Setenv("STATE_DIRECTORY", t.TempDir())
	db, password := newAdministratorDB(t)
	if err := admin.ChangePassword(t.Context(), db, password, "subscription API test password"); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(t.Context(), db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := newAPI(db)
	var audit bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&audit)
	t.Cleanup(func() { log.SetOutput(writer) })
	check := func(method, source string, origin, session bool, status int, expected ...string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"source": source})
		request := httptest.NewRequest(method, "https://router.example/api/proxy/subscription", bytes.NewReader(payload))
		if origin {
			request.Header.Set("Origin", "https://router.example")
		}
		if session {
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("unexpected status: got %d want %d", response.Code, status)
		}
		for _, fragment := range expected {
			if !strings.Contains(response.Body.String(), fragment) {
				t.Fatalf("expected safe response field %q", fragment)
			}
		}
		if strings.Contains(response.Body.String(), "test-private-") || strings.Contains(audit.String(), "test-private-") {
			t.Fatal("private source, credentials, or label appeared in diagnostics")
		}
	}
	check(http.MethodGet, "", false, false, 401)
	check(http.MethodPost, "", true, false, 401)
	check(http.MethodPost, "", false, true, 403)
	check(http.MethodPost, "https://127.0.0.1/test-private-url", true, true, 400, "invalid_subscription_url")
	check(http.MethodPost, "hysteria2://test-private-password@hi.example#test-private-label\ninvalid://test-private-input", true, true, 200, `"imported":1`, "unsupported_protocol")
	check(http.MethodPost, "vless://test-private-invalid@v.example:443?security=tls", true, true, 422, `"imported":0`, "invalid_server_credentials")
	check(http.MethodGet, "", false, true, 200, `"imported":1`, "unsupported_protocol")
}
