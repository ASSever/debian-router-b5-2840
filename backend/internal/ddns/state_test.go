package ddns

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCheckRetriesChangedAddressAndKeepsLastAcceptedAddress(t *testing.T) {
	const record = "router.example.test"
	var mu sync.Mutex
	address := "192.0.2.11"
	response := "good"
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/address" {
			if r.Header.Get("Authorization") != "" {
				t.Error("address source received provider credentials")
			}
			fmt.Fprintln(w, address)
			return
		}
		requests++
		user, password, _ := r.BasicAuth()
		if user != "test-user" || password != "test-password" || r.URL.Query().Get("hostname") != record ||
			r.URL.Query().Get("myip") != address {
			t.Error("wrong DDNS request")
		}
		if response == "good" {
			fmt.Fprintln(w, response, address)
			return
		}
		fmt.Fprintln(w, response)
	}))
	defer server.Close()
	config := Config{Endpoint: server.URL + "/nic/update", Record: record, AddressURL: server.URL + "/address"}
	u := updater{config: config, creds: Credentials{"test-user", "test-password"}, client: server.Client()}
	ctx := context.Background()
	now := time.Now().UTC()
	status := u.check(ctx, Status{ConfigID: config.identity(), Record: record}, now)
	if status.Result != "updated" || status.PublishedAddress != "192.0.2.11" {
		t.Fatalf("initial status %+v", status)
	}
	mu.Lock()
	address, response = "192.0.2.12", "911"
	mu.Unlock()
	status = u.check(ctx, status, now.Add(time.Minute))
	if status.Error != "provider_unavailable" || status.ObservedAddress != "192.0.2.12" ||
		status.PublishedAddress != "192.0.2.11" || status.RetryAt == nil || !status.RetryAt.Equal(now.Add(31*time.Minute)) {
		t.Fatalf("provider failure status %+v", status)
	}
	dir := t.TempDir()
	if err := saveStatus(dir, status); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	status = u.check(ctx, restored, now.Add(30*time.Minute))
	if status.Error != "provider_unavailable" {
		t.Fatalf("cooldown status %+v", status)
	}
	mu.Lock()
	response = "good"
	mu.Unlock()
	status = u.check(ctx, status, now.Add(31*time.Minute))
	if status.Result != "updated" || status.Error != "" || status.PublishedAddress != "192.0.2.12" {
		t.Fatalf("recovery status %+v", status)
	}
	status = u.check(ctx, status, now.Add(32*time.Minute))
	mu.Lock()
	defer mu.Unlock()
	if status.Result != "unchanged" || requests != 3 {
		t.Fatalf("unchanged status %+v, %d update attempts", status, requests)
	}
}
