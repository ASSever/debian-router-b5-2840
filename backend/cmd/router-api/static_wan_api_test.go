package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

func TestStaticWANAPIRejectsInvalidGatewayBeforeCallingAgent(t *testing.T) {
	applyCalled := false
	client, server, cookie := newStaticWANAPITestServer(t,
		func(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error) {
			applyCalled = true
			return networkinventory.StaticWANTransaction{}, nil
		},
		func(context.Context, string) error { return nil },
		func(context.Context, string) error { return nil },
	)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/network/wan/static", strings.NewReader(
		`{"address":"192.168.1.104/24","gateway":"192.168.2.1","dns":["192.168.1.1"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || applyCalled {
		t.Fatalf("status = %d, agent called = %t; want 400 before agent", response.StatusCode, applyCalled)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "invalid_static_gateway" {
		t.Fatalf("error = %q, want invalid_static_gateway", body["error"])
	}
}

func TestStaticWANAPIStartsAuthorizedApplyTransaction(t *testing.T) {
	var received networkinventory.StaticWANConfig
	client, server, cookie := newStaticWANAPITestServer(t,
		func(_ context.Context, config networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error) {
			received = config
			return networkinventory.StaticWANTransaction{
				ID:        "0123456789abcdef0123456789abcdef",
				ExpiresAt: time.Now().Add(120 * time.Second).UTC(),
			}, nil
		},
		func(context.Context, string) error { return nil },
		func(context.Context, string) error { return nil },
	)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/network/wan/static", strings.NewReader(
		`{"address":"192.168.1.104/24","gateway":"192.168.1.1","dns":["192.168.1.1","1.1.1.1"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	if received.Address != "192.168.1.104/24" || received.Gateway != "192.168.1.1" || len(received.DNS) != 2 {
		t.Fatalf("agent received config = %#v", received)
	}
	var transaction networkinventory.StaticWANTransaction
	if err := json.NewDecoder(response.Body).Decode(&transaction); err != nil {
		t.Fatal(err)
	}
	if transaction.ID != "0123456789abcdef0123456789abcdef" || transaction.ExpiresAt.IsZero() {
		t.Fatalf("response transaction = %#v", transaction)
	}
}

func TestStaticWANMutationRequiresSameOriginAndAdministratorSession(t *testing.T) {
	applyCalled := false
	client, server, _ := newStaticWANAPITestServer(t,
		func(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error) {
			applyCalled = true
			return networkinventory.StaticWANTransaction{}, nil
		},
		func(context.Context, string) error { return nil },
		func(context.Context, string) error { return nil },
	)
	defer server.Close()

	for _, test := range []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "origin rejected", origin: "https://other.example", wantStatus: http.StatusForbidden},
		{name: "no session", origin: server.URL, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api/network/wan/static", strings.NewReader(
				`{"address":"192.168.1.104/24","gateway":"192.168.1.1","dns":["192.168.1.1"]}`,
			))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.wantStatus || applyCalled {
				t.Fatalf("status = %d, agent called = %t; want %d and no agent call", response.StatusCode, applyCalled, test.wantStatus)
			}
		})
	}
}

func newStaticWANAPITestServer(
	t *testing.T,
	apply func(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error),
	confirm func(context.Context, string) error,
	rollback func(context.Context, string) error,
) (*http.Client, *httptest.Server, *http.Cookie) {
	t.Helper()
	db, initialPassword := newAdministratorDB(t)
	if err := admin.ChangePassword(t.Context(), db, initialPassword, "a static WAN API test password"); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(t.Context(), db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := newAPIWithNetworkOperations(
		db,
		"",
		func() ([]networkinventory.Interface, error) { return nil, nil },
		func(string) (networkinventory.DHCPStatus, error) { return networkinventory.DHCPStatus{}, nil },
		apply,
		confirm,
		rollback,
	)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server.Client(), server, &http.Cookie{Name: sessionCookieName, Value: token}
}
