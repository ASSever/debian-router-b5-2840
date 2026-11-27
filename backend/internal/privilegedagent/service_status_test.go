package privilegedagent

import "testing"

func TestServiceStatesReportAbsentDataAndIgnoreOtherUnits(t *testing.T) {
	got := parseServiceStates(`Id=debian-router-api.service
LoadState=loaded
ActiveState=active

Id=debian-router-lan-dnsmasq.service
LoadState=not-found
ActiveState=inactive

Id=unrelated.service
LoadState=loaded
ActiveState=failed
`)
	if len(got) != len(dashboardUnits) {
		t.Fatalf("unexpected states: %+v", got)
	}
	want := map[string]string{
		"debian-router-api.service":         "active",
		"debian-router-lan-dnsmasq.service": "not_loaded",
	}
	for _, service := range got {
		expected := want[service.Unit]
		if expected == "" {
			expected = "unavailable"
		}
		if service.State != expected || service.Unit == "unrelated.service" {
			t.Fatalf("unexpected state for %s: %s", service.Unit, service.State)
		}
	}
}
