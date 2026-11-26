package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckUsesAuthenticatedProxyWithoutDirectFallback(t *testing.T) {
	const auth = "test-proxy-secret"
	requests := make(chan struct{}, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected, _ := http.NewRequest(http.MethodConnect, CheckURL, nil)
		expected.SetBasicAuth("check", auth)
		if r.Method != http.MethodConnect || r.Host != "www.gstatic.com:443" ||
			r.Header.Get("Proxy-Authorization") != expected.Header.Get("Authorization") {
			t.Error("probe did not connect through the authenticated local proxy")
		}
		requests <- struct{}{}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	reason := requestThroughWorker(ctx, strings.TrimPrefix(fixture.URL, "http://"), auth, CheckURL, nil, http.StatusNoContent)
	if reason != "proxy_request_failed" || len(requests) != 1 {
		t.Fatalf("result = %q and proxy requests = %d; expected one rejected CONNECT", reason, len(requests))
	}
}

func TestCheckRejectsUnsafeAndUnsupportedServerOptions(t *testing.T) {
	for _, options := range []map[string]string{
		{"fm": `{"udp":[{"type":"unknown"}]}`},
		{"fm": `{"quicParams":{"congestion":"brutal"}}`},
		{"insecure": "1"},
	} {
		result := CheckServer(t.Context(), "", Server{Protocol: "hysteria2", Host: "127.0.0.1", Options: options})
		if result.Reason != "unsupported_options" {
			t.Fatalf("result = %+v", result)
		}
	}
	result := CheckServer(t.Context(), "", Server{Protocol: "hysteria2", Host: "::ffff:127.0.0.1",
		Options: map[string]string{"fm": `{"quicParams":{"congestion":"bbr","debug":false}}`}})
	if result.Reason != "nonpublic_destination" {
		t.Fatalf("result = %+v", result)
	}
}
