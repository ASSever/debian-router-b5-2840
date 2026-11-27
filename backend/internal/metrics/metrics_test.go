package metrics

import (
	"database/sql"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

func TestTrafficPointDistinguishesZeroFromGaps(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.168.10.2")
	before := &network.TrafficSnapshot{
		SampledAt: now.Add(-Interval), ClientsSince: now.Add(-time.Hour),
		Interfaces: []network.InterfaceTraffic{{Name: "enp2s0", Index: 3, RX: network.TrafficCount{Bytes: 100}}},
		Clients:    []network.ClientTraffic{{Address: ip, Download: network.TrafficCount{Bytes: 100}}},
	}
	after := &network.TrafficSnapshot{
		SampledAt: now, ClientsSince: before.ClientsSince,
		Interfaces: []network.InterfaceTraffic{{Name: "enp2s0", Index: 3, RX: network.TrafficCount{Bytes: 40}}},
		Clients:    []network.ClientTraffic{{Address: ip, Download: network.TrafficCount{Bytes: 400}}},
	}
	got := trafficPoint(now, before, after)
	if !got.TrafficAvailable || got.Interfaces[0].RX != nil || got.Interfaces[0].TX == nil || *got.Interfaces[0].TX != 0 ||
		got.Clients[0].RX == nil || *got.Clients[0].RX != 10 {
		t.Fatalf("unexpected traffic rates: %+v", got)
	}
	after.ClientsSince = now
	if trafficPoint(now, before, after).Clients[0].RX != nil ||
		trafficPoint(now, nil, after).Interfaces[0].TX != nil ||
		trafficPoint(now, before, nil).TrafficAvailable {
		t.Fatal("reset or missing reading was treated as a measured rate")
	}
	after.SampledAt = now.Add(2 * Interval)
	if trafficPoint(now, before, after).Interfaces[0].TX != nil {
		t.Fatal("rate bridged a missing interval")
	}
}

func TestStoreRetainsChronologicalSamplesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	ctx := t.Context()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, at := range []time.Time{now.Add(-25 * time.Hour), now.Add(-Interval), now, now.Add(-2 * Interval)} {
		if err := db.Save(ctx, Point{At: at}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Recent(ctx, 60)
	if err != nil || len(got) != 3 || !got[0].At.Equal(now.Add(-2*Interval)) || !got[2].At.Equal(now) {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
}

func TestStoreRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if opened, err := Open(t.Context(), path); err == nil {
		opened.Close()
		t.Fatal("opened newer schema")
	}
}
