package monitor

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestProbeSeparatesDNSAndTransport(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	cfg := Config{
		IntervalSeconds: 1,
		TimeoutSeconds:  1,
		Targets: []Target{
			{Name: "direct", Address: listener.Addr().String()},
			{Name: "named", Address: net.JoinHostPort("monitor-fixture.test.", port)},
			{Name: "dns", Address: net.JoinHostPort("unresolved.test.", port)},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	prober := NewProber(cfg)
	prober.resolve = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "monitor-fixture.test." {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return nil, errors.New("DNS unavailable")
	}
	ctx := context.Background()
	success := prober.check(ctx, cfg.Targets[0])
	if success.Result != OK || success.DNS != "not_required" || success.Transport != OK {
		t.Fatalf("direct result: %+v", success)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	results := prober.CheckAll(ctx, cfg.Targets[1:])
	if got := results[0]; got.Result != TransportFailed || got.DNS != OK || got.Transport != "failed" {
		t.Fatalf("transport failure: %+v", got)
	}
	if got := results[1]; got.Result != DNSFailed || got.DNS != "failed" || got.Transport != "skipped" {
		t.Fatalf("DNS failure: %+v", got)
	}
}

func TestConfigRejectsInvalidTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.json")
	cases := []string{
		`{"targets":[{"name":"a","address":"https://example.test:443"}]}`,
		`{"targets":[{"name":"a","address":"example.test:0"}]}`,
		`{"dns_server":"example.test:53","targets":[{"name":"a","address":"example.test:443"}]}`,
		`{"targets":[{"name":"a","address":"example.test:443"},{"name":"a","address":"[::1]:80"}]}`,
		`{"targets":[{"name":"a","address":"example.test:443"}]} {}`,
	}
	for _, body := range cases {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("accepted invalid config %s", body)
		}
	}
}

func TestHistoryPersistsAndPrunesRows(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	writer, err := OpenHistory(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	now := time.UnixMilli(1_800_000_000_123).UTC()
	samples := []Sample{
		{Target: "expired", CheckedAt: now.Add(-historyMaxAge - time.Second)},
		{Target: "older", CheckedAt: now.Add(-time.Second), Result: DNSFailed, DNS: "failed", Transport: "skipped"},
		{Target: "latest", CheckedAt: now, Result: TransportFailed, DNS: OK, Transport: "failed", DurationMS: 75},
	}
	if err := writer.Save(ctx, samples, now); err != nil {
		t.Fatal(err)
	}
	history, err := ReadHistory(ctx, dir, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0] != samples[2] || history[1] != samples[1] {
		t.Fatalf("history after age pruning: %+v", history)
	}

	if _, err := writer.db.ExecContext(ctx, "UPDATE checks SET id=? WHERE target='latest'", historyMaxRows+2); err != nil {
		t.Fatal(err)
	}
	if err := writer.Save(ctx, []Sample{samples[2]}, now); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err = OpenHistory(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	history, err = ReadHistory(ctx, dir, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0] != samples[2] || history[1] != samples[2] {
		t.Fatalf("reopened history after row cap: %+v", history)
	}
}

func TestHistorySchemaChecks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := openDB(dir, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE TABLE conflict (time INTEGER); CREATE INDEX checks_by_time ON conflict(time)"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenHistory(ctx, dir); err == nil {
		t.Fatal("failed schema initialization unexpectedly succeeded")
	}
	var tables, version int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='checks'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || version != 0 {
		t.Fatalf("partial initialization, tables=%d version=%d", tables, version)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenHistory(ctx, dir); err == nil {
		t.Fatal("newer schema unexpectedly accepted")
	}
}
