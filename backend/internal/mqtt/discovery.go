package mqtt

type discoveryDevice struct {
	Identifiers []string `json:"identifiers"`
	Name        string   `json:"name"`
	Model       string   `json:"model"`
}

type discoveryPayload struct {
	Name              string          `json:"name"`
	UniqueID          string          `json:"unique_id"`
	Device            discoveryDevice `json:"device"`
	StateTopic        string          `json:"state_topic"`
	AvailabilityTopic string          `json:"availability_topic"`
	ValueTemplate     string          `json:"value_template"`
	DeviceClass       string          `json:"device_class"`
	StateClass        string          `json:"state_class,omitempty"`
	Unit              string          `json:"unit_of_measurement,omitempty"`
	QoS               int             `json:"qos"`
}

type discoveryMessage struct {
	topic   string
	payload discoveryPayload
}

func discoveryMessages(config Config) []discoveryMessage {
	if !config.Discovery {
		return nil
	}
	deviceID := "debian-router-" + config.ClientID
	device := discoveryDevice{Identifiers: []string{deviceID}, Name: "Debian Router " + config.ClientID, Model: "B5-2840"}
	entities := []struct {
		component, id, name, template, deviceClass, stateClass, unit string
	}{
		{"sensor", "uptime", "Uptime", "{{ value_json.uptime_seconds }}", "duration", "total_increasing", "s"},
		{"sensor", "ram_used", "RAM used", "{{ value_json.ram_used_bytes }}", "data_size", "measurement", "B"},
		{"sensor", "cpu_temperature", "CPU temperature", "{{ value_json.cpu_temperature_c }}", "temperature", "measurement", "°C"},
		{"binary_sensor", "wan_link", "WAN link", linkTemplate("wan_carrier"), "connectivity", "", ""},
		{"binary_sensor", "lan_link", "LAN link", linkTemplate("lan_carrier"), "connectivity", "", ""},
	}
	messages := make([]discoveryMessage, 0, len(entities))
	for _, e := range entities {
		messages = append(messages, discoveryMessage{
			topic: "homeassistant/" + e.component + "/" + deviceID + "/" + e.id + "/config",
			payload: discoveryPayload{
				Name: e.name, UniqueID: deviceID + "_" + e.id, Device: device,
				StateTopic: config.TopicPrefix + "/status", AvailabilityTopic: config.TopicPrefix + "/availability",
				ValueTemplate: e.template, DeviceClass: e.deviceClass, StateClass: e.stateClass, Unit: e.unit, QoS: 1,
			},
		})
	}
	return messages
}

func linkTemplate(field string) string {
	value := "value_json." + field
	return "{{ 'ON' if " + value + " is sameas true else 'OFF' if " + value + " is sameas false else none }}"
}
