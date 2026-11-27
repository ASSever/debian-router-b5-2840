package metrics

import (
	"context"
	"log"
	"net/netip"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
)

type TrafficRate struct {
	Name string   `json:"name"`
	RX   *float64 `json:"rx_bytes_per_second"`
	TX   *float64 `json:"tx_bytes_per_second"`
}

type Point struct {
	At               time.Time      `json:"sampled_at"`
	TrafficAvailable bool           `json:"traffic_available"`
	Interfaces       []TrafficRate  `json:"interfaces"`
	Clients          []TrafficRate  `json:"clients"`
	LANPrefixes      []netip.Prefix `json:"lan_prefixes"`
	System           System         `json:"system"`
}

func rate(before, after uint64, seconds float64) *float64 {
	if seconds <= 0 || after < before {
		return nil
	}
	result := float64(after-before) / seconds
	return &result
}

func trafficPoint(at time.Time, previous, current *network.TrafficSnapshot) Point {
	point := Point{At: at.UTC(), Interfaces: []TrafficRate{}, Clients: []TrafficRate{}, LANPrefixes: []netip.Prefix{}}
	if current == nil {
		return point
	}
	point.TrafficAvailable = true
	point.LANPrefixes = current.Prefixes
	seconds := 0.0
	links := map[int]network.InterfaceTraffic{}
	clients := map[netip.Addr]network.ClientTraffic{}
	if previous != nil {
		elapsed := current.SampledAt.Sub(previous.SampledAt)
		if elapsed > 0 && elapsed <= 2*Interval {
			seconds = elapsed.Seconds()
			for _, link := range previous.Interfaces {
				links[link.Index] = link
			}
			if current.ClientsSince.Equal(previous.ClientsSince) {
				for _, client := range previous.Clients {
					clients[client.Address] = client
				}
			}
		}
	}
	for _, link := range current.Interfaces {
		value := TrafficRate{Name: link.Name}
		if old, ok := links[link.Index]; ok && old.Name == link.Name {
			value.RX = rate(old.RX.Bytes, link.RX.Bytes, seconds)
			value.TX = rate(old.TX.Bytes, link.TX.Bytes, seconds)
		}
		point.Interfaces = append(point.Interfaces, value)
	}
	for _, client := range current.Clients {
		value := TrafficRate{Name: client.Address.String()}
		if old, ok := clients[client.Address]; ok {
			value.RX = rate(old.Download.Bytes, client.Download.Bytes, seconds)
			value.TX = rate(old.Upload.Bytes, client.Upload.Bytes, seconds)
		}
		point.Clients = append(point.Clients, value)
	}
	return point
}

// Collect samples until shutdown. Baselines are discarded on failed reads and restart.
func Collect(ctx context.Context, store *Store, socket, dataDir string) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	var previous *network.TrafficSnapshot
	var previousCPU *cpuCounters
	for ctx.Err() == nil {
		sampleCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		response, err := privilegedagent.SendRequest(sampleCtx, socket, "traffic-status")
		point := trafficPoint(time.Now(), previous, response.Traffic)
		if err != nil || response.Traffic == nil {
			log.Printf("dashboard traffic unavailable")
		}
		previous = response.Traffic
		cancel()
		systemCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		point.System, previousCPU = readSystem(systemCtx, dataDir, socket, previousCPU)
		cancel()
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = store.Save(writeCtx, point)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("save metrics sample: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
