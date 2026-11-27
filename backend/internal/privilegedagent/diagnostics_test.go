package privilegedagent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticEventsKeepOnlyFixedFields(t *testing.T) {
	seed := strings.ToLower(rand.Text())
	password, token, opaque := "pw-"+seed, "bearer-"+seed, "opaque-"+seed
	subscription := "https://subscription.invalid/" + seed
	var journal bytes.Buffer
	add := func(unit, pid, target, priority string, message any) {
		t.Helper()
		if err := json.NewEncoder(&journal).Encode(map[string]any{
			"__REALTIME_TIMESTAMP": "1735689600123456", "_SYSTEMD_UNIT": unit,
			"_PID": pid, "UNIT": target, "PRIORITY": priority, "MESSAGE": message,
			"SUBSCRIPTION_URL": subscription, "_CMDLINE": opaque,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("debian-router-api.service", "27", "", "3",
		"Authentication failed: password="+password+"\nAuthorization: Bearer "+token+"\n"+subscription)
	add("init.scope", "1", "debian-router-mqtt.service", "4", "Main process exited: "+token)
	add("debian-router-api.service", "27", "", "4", opaque)
	add("debian-router-api.service", "27", "", "4", []int{65, 10, 0})
	add("unrelated.service", "27", "debian-router-api.service", "3", "connection refused")
	add("debian-router-api.service", "27", "", "5", "connection refused")

	events, err := diagnosticEvents(journal.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d allowed events, want 4", len(events))
	}
	want := []DiagnosticEvent{
		{time.UnixMicro(1735689600123456).UTC(), "debian-router-api.service", 3, "access_failure"},
		{time.UnixMicro(1735689600123456).UTC(), "debian-router-mqtt.service", 4, "service_failure"},
		{time.UnixMicro(1735689600123456).UTC(), "debian-router-api.service", 4, "unclassified"},
		{time.UnixMicro(1735689600123456).UTC(), "debian-router-api.service", 4, "unclassified"},
	}
	for i, event := range events {
		if event != want[i] {
			t.Errorf("event %d does not match expected diagnostic fields", i)
		}
	}
	output, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{seed, "authorization", "subscription.invalid", "_cmdline", "message"} {
		if bytes.Contains(bytes.ToLower(output), []byte(excluded)) {
			t.Error("diagnostic export contains a field or value from raw journal text")
		}
	}
}

func TestLargestDiagnosticJournalFitsAgentResponse(t *testing.T) {
	row := `{"__REALTIME_TIMESTAMP":"1735689600123456","_SYSTEMD_UNIT":"debian-router-lan-dnsmasq.service","PRIORITY":"4","MESSAGE":"connection refused"}` + "\n"
	events, err := diagnosticEvents([]byte(strings.Repeat(row, journalEventLimit+1)))
	if err != nil || len(events) != journalEventLimit {
		t.Fatalf("parsed %d events, error=%v", len(events), err)
	}
	snapshot := DiagnosticSnapshot{parseUnitStates("", diagnosticUnits), "available", events}
	wire, err := json.Marshal(Response{Diagnostic: &snapshot})
	if err != nil || len(wire)+1 >= maxResponseBytes {
		t.Fatalf("response size=%d error=%v", len(wire), err)
	}
}
