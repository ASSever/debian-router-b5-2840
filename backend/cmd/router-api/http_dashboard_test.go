package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/authsession"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/metrics"
)

func TestDashboardServesStoredMeasurementsToAdministrator(t *testing.T) {
	db, password := newAdministratorDB(t)
	store, err := metrics.Open(t.Context(), filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := withDashboard(newAPI(db), db, store)

	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
		r.TLS = &tls.ConnectionState{}
		if token != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request("").Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", got)
	}
	if err := admin.ChangePassword(t.Context(), db, password, "Dashboard fixture administrator"); err != nil {
		t.Fatal(err)
	}
	token, err := authsession.Create(t.Context(), db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	point := metrics.Point{At: time.Now().Add(-5 * time.Minute), TrafficAvailable: true,
		Interfaces: []metrics.TrafficRate{{Name: "enp2s0", RX: &zero}}}
	if err := store.Save(t.Context(), point); err != nil {
		t.Fatal(err)
	}
	got := request(token)
	var data struct {
		Stale  bool
		Points []metrics.Point
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &data) != nil || !data.Stale || len(data.Points) != 1 {
		t.Fatalf("unexpected dashboard response: %d %s", got.Code, got.Body)
	}
	rate := data.Points[0].Interfaces[0]
	if rate.Name != "enp2s0" || rate.RX == nil || *rate.RX != 0 || rate.TX != nil {
		t.Fatalf("unexpected stored rate: %+v", rate)
	}
}
