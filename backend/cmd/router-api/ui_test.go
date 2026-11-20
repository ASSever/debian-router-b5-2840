package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIAssetsAreServedOverHTTPS(t *testing.T) {
	db, _ := newAdministratorDB(t)
	uiDirectory := t.TempDir()
	assetsDirectory := filepath.Join(uiDirectory, "assets")
	if err := os.Mkdir(assetsDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":       "<!doctype html><title>Router</title>",
		"assets/app.js":    "console.log('ready')",
		"assets/app.css":   "body { color: black; }",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(uiDirectory, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	server := httptest.NewTLSServer(newAPIWithUI(db, uiDirectory))
	defer server.Close()

	for path, expected := range map[string]string{
		"/":                 "<!doctype html>",
		"/assets/app.js":    "console.log",
		"/assets/app.css":   "body {",
	} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
		if !strings.Contains(string(body), expected) {
			t.Errorf("GET %s body = %q, want it to contain %q", path, body, expected)
		}
		policy := response.Header.Get("Content-Security-Policy")
		if !strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "unsafe-inline") {
			t.Errorf("GET %s Content-Security-Policy = %q", path, policy)
		}
	}
}

func TestUIIsUnavailableWhenNoDirectoryIsConfigured(t *testing.T) {
	db, _ := newAdministratorDB(t)
	server := httptest.NewTLSServer(newAPIWithUI(db, ""))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / without a UI directory status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}
