package mqtt

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryKeepsStableTopicsAndEntityIDs(t *testing.T) {
	config := Config{ClientID: "b5-test", TopicPrefix: "test/router"}
	if got := discoveryMessages(config); len(got) != 0 {
		t.Fatal("discovery should be opt-in")
	}
	config.Discovery = true
	first, repeated := discoveryMessages(config), discoveryMessages(config)
	if !reflect.DeepEqual(first, repeated) || len(first) != 5 {
		t.Fatalf("expected five stable discovery messages: %+v", first)
	}
	expected := map[string][4]string{
		"uptime":          {"sensor", "s", "duration", "total_increasing"},
		"ram_used":        {"sensor", "B", "data_size", "measurement"},
		"cpu_temperature": {"sensor", "°C", "temperature", "measurement"},
		"wan_link":        {"binary_sensor", "", "connectivity", ""},
		"lan_link":        {"binary_sensor", "", "connectivity", ""},
	}
	for _, message := range first {
		var body map[string]any
		payload, err := json.Marshal(message.payload)
		if err != nil || json.Unmarshal(payload, &body) != nil {
			t.Fatalf("invalid discovery payload: %s", payload)
		}
		id := strings.TrimPrefix(message.payload.UniqueID, "debian-router-b5-test_")
		want, ok := expected[id]
		if !ok {
			t.Fatalf("unexpected or duplicate ID %q", id)
		}
		if message.topic != "homeassistant/"+want[0]+"/debian-router-b5-test/"+id+"/config" ||
			message.payload.Unit != want[1] || message.payload.DeviceClass != want[2] || message.payload.StateClass != want[3] ||
			message.payload.StateTopic != "test/router/status" || message.payload.AvailabilityTopic != "test/router/availability" ||
			!reflect.DeepEqual(message.payload.Device.Identifiers, []string{"debian-router-b5-test"}) {
			t.Fatalf("unexpected discovery payload: %s", payload)
		}
		if want[0] == "binary_sensor" && (body["unit_of_measurement"] != nil || body["state_class"] != nil) {
			t.Fatalf("binary sensor has numeric metadata: %s", payload)
		}
		delete(expected, id)
	}
}
