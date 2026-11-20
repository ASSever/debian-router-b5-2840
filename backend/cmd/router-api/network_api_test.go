package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

func TestNetworkInterfacesReturnsReadOnlyInventoryOverHTTPS(t *testing.T) {
	db, _ := newAdministratorDB(t)
	want := []networkinventory.Interface{{
		Name:             "enp1s0",
		OperationalState: "up",
		Carrier:          boolPointer(true),
		SpeedMbps:        intPointer(1000),
	}}
	server := httptest.NewTLSServer(newAPIWithInterfaceReader(db, "", func() ([]networkinventory.Interface, error) {
		return want, nil
	}))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/network/interfaces")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Interfaces []networkinventory.Interface `json:"interfaces"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Interfaces) != 1 || body.Interfaces[0].Name != want[0].Name || body.Interfaces[0].SpeedMbps == nil || *body.Interfaces[0].SpeedMbps != 1000 {
		t.Fatalf("GET body = %#v, want the fixed interface inventory", body)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/network/interfaces", nil)
	plainResponse := httptest.NewRecorder()
	newAPIWithInterfaceReader(db, "", func() ([]networkinventory.Interface, error) {
		return want, nil
	}).ServeHTTP(plainResponse, request)
	if plainResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain HTTP status = %d, want %d", plainResponse.Code, http.StatusUpgradeRequired)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/network/interfaces", strings.NewReader(`{}`))
	request.TLS = &tls.ConnectionState{}
	plainResponse = httptest.NewRecorder()
	newAPIWithInterfaceReader(db, "", func() ([]networkinventory.Interface, error) {
		return want, nil
	}).ServeHTTP(plainResponse, request)
	if plainResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", plainResponse.Code, http.StatusMethodNotAllowed)
	}
}

func TestNetworkInterfacesHidesSysfsReadErrors(t *testing.T) {
	db, _ := newAdministratorDB(t)
	server := httptest.NewTLSServer(newAPIWithInterfaceReader(db, "", func() ([]networkinventory.Interface, error) {
		return nil, errors.New("private sysfs detail")
	}))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/network/interfaces")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(responseBytes, &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "network_interfaces_unavailable" || strings.Contains(string(responseBytes), "private sysfs detail") {
		t.Fatalf("unexpected error response: %s", responseBytes)
	}
}

func boolPointer(value bool) *bool { return &value }

func intPointer(value int) *int { return &value }
