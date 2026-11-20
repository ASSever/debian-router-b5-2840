package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

func TestNetworkDHCPStatusReturnsFilteredLeaseDetailsOverHTTPS(t *testing.T) {
	db, _ := newAdministratorDB(t)
	handler := newAPIWithNetworkReaders(
		db,
		"",
		func() ([]networkinventory.Interface, error) { return nil, nil },
		func(name string) (networkinventory.DHCPStatus, error) {
			if name != "enp1s0" {
				t.Fatalf("interface = %q, want enp1s0", name)
			}
			return networkinventory.DHCPStatus{
				Interface:  name,
				LinkState:  "carrier",
				LeaseState: "leased",
				Addresses:  []string{"192.0.2.10/24"},
				Gateway:    "192.0.2.1",
				DNS:        []string{"192.0.2.53"},
			}, nil
		},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/network/dhcp?interface=enp1s0", nil)
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["lease_state"] != "leased" || body["gateway"] != "192.0.2.1" {
		t.Fatalf("response = %#v, want current lease and gateway", body)
	}
	if body["interface"] != "enp1s0" || body["addresses"] == nil || body["dns"] == nil {
		t.Fatalf("response = %#v, want interface, address, and DNS", body)
	}
}

func TestNetworkDHCPStatusRejectsInvalidInterface(t *testing.T) {
	db, _ := newAdministratorDB(t)
	called := false
	handler := newAPIWithNetworkReaders(
		db,
		"",
		func() ([]networkinventory.Interface, error) { return nil, nil },
		func(string) (networkinventory.DHCPStatus, error) {
			called = true
			return networkinventory.DHCPStatus{}, nil
		},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/network/dhcp?interface=../enp1s0", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain HTTP status = %d, want %d", response.Code, http.StatusUpgradeRequired)
	}
	request.TLS = new(tls.ConnectionState)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("status = %d, reader called = %t; want 400 and no reader call", response.Code, called)
	}
}
