package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	paho "github.com/eclipse/paho.mqtt.golang"
)

const operationTimeout = 5 * time.Second

type routerStatus struct {
	SampledAt time.Time `json:"sampled_at"`
	Uptime    *uint64   `json:"uptime_seconds"`
	WAN       *bool     `json:"wan_carrier"`
	LAN       *bool     `json:"lan_carrier"`
}

func sampleStatus() routerStatus {
	status := routerStatus{SampledAt: time.Now().UTC()}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		seconds, _, _ := strings.Cut(string(data), ".")
		if uptime, err := strconv.ParseUint(seconds, 10, 64); err == nil {
			status.Uptime = &uptime
		}
	}
	links, _ := network.ReadInterfaces()
	for _, link := range links {
		switch link.Name {
		case network.StaticWANInterface:
			status.WAN = link.Carrier
		case network.LANInterface:
			status.LAN = link.Carrier
		}
	}
	return status
}

// Run connects and samples in the same loop. A new session publishes a fresh
// sample before marking the router online, so outages never replay old data.
func Run(ctx context.Context, config Config, credentials Credentials) {
	delay := 2 * time.Second
	for ctx.Err() == nil {
		published := runSession(ctx, config, credentials)
		if ctx.Err() != nil {
			return
		}
		if published {
			delay = 2 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func runSession(ctx context.Context, config Config, credentials Credentials) bool {
	availability := config.TopicPrefix + "/availability"
	statusTopic := config.TopicPrefix + "/status"
	lost := make(chan struct{}, 1)
	broker := strings.Replace(config.Broker, "mqtt://", "tcp://", 1)
	broker = strings.Replace(broker, "mqtts://", "ssl://", 1)
	opts := paho.NewClientOptions().AddBroker(broker).
		SetClientID(config.ClientID).SetProtocolVersion(4).SetCleanSession(true).
		SetUsername(credentials.username).SetPassword(credentials.password).
		SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetConnectTimeout(operationTimeout).SetWriteTimeout(operationTimeout).
		SetKeepAlive(15*time.Second).SetPingTimeout(operationTimeout).
		SetAutoReconnect(false).SetConnectRetry(false).
		SetWill(availability, "offline", 1, true).
		SetConnectionLostHandler(func(paho.Client, error) {
			select {
			case lost <- struct{}{}:
			default:
			}
		})
	client := paho.NewClient(opts)
	if err := await(ctx, client.Connect()); err != nil {
		client.Disconnect(0)
		if ctx.Err() == nil {
			log.Print("MQTT connection failed. Retrying.")
		}
		return false
	}
	defer func() {
		if client.IsConnectionOpen() {
			stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = await(stop, client.Publish(availability, 1, true, "offline"))
		}
		client.Disconnect(200)
	}()
	publish := func() bool {
		payload, err := json.Marshal(sampleStatus())
		return err == nil && await(ctx, client.Publish(statusTopic, 1, true, payload)) == nil
	}
	if !publish() || await(ctx, client.Publish(availability, 1, true, "online")) != nil {
		log.Print("MQTT initial status publish failed. Retrying.")
		return false
	}
	log.Print("MQTT connected and published current status.")
	ticker := time.NewTicker(time.Duration(config.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-lost:
			log.Print("MQTT connection lost. Retrying.")
			return true
		case <-ticker.C:
			if !publish() {
				log.Print("MQTT status publish failed. Retrying.")
				return true
			}
		}
	}
}

func await(ctx context.Context, token paho.Token) error {
	timer := time.NewTimer(operationTimeout + time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("MQTT operation timed out")
	case <-token.Done():
		return token.Error()
	}
}
