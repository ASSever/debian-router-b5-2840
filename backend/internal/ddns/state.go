package ddns

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Status struct {
	ConfigID         string     `json:"config_id,omitempty"`
	Record           string     `json:"record"`
	ObservedAddress  string     `json:"observed_address,omitempty"`
	PublishedAddress string     `json:"published_address,omitempty"`
	Result           string     `json:"result"`
	Error            string     `json:"error,omitempty"`
	CheckedAt        time.Time  `json:"checked_at"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
	RetryAt          *time.Time `json:"retry_at,omitempty"`
	RequiresRestart  bool       `json:"requires_restart,omitempty"`
}

func ReadStatus(dir string) (Status, error) {
	var status Status
	data, err := readLimited(filepath.Join(dir, "status.json"), 16384)
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(data, &status)
	return status, err
}

func saveStatus(dir string, status Status) error {
	file, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(status)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "status.json"))
}

func (u *updater) check(ctx context.Context, status Status, now time.Time) Status {
	status.CheckedAt = now
	address, err := u.address(ctx)
	status.ObservedAddress = ""
	if err == nil {
		status.ObservedAddress = address.String()
	}
	if status.RequiresRestart || status.RetryAt != nil && now.Before(*status.RetryAt) {
		return status
	}
	status.RetryAt = nil
	status.Error = ""
	switch {
	case err != nil:
		status.Result, status.Error = "error", err.Error()
	case status.ObservedAddress == status.PublishedAddress:
		status.Result = "unchanged"
	default:
		code, cooldown := u.update(ctx, address)
		switch code {
		case "good", "nochg":
			status.Result = "updated"
			if code == "nochg" {
				status.Result = "unchanged"
			}
			status.PublishedAddress = status.ObservedAddress
			status.UpdatedAt = &now
		default:
			status.Result, status.Error = "error", code
			status.RequiresRestart = code == "authentication_failed" || code == "provider_rejected"
			if cooldown {
				retryAt := now.Add(30 * time.Minute)
				status.RetryAt = &retryAt
			}
		}
	}
	return status
}

func Run(ctx context.Context, config Config, creds Credentials, dir string) {
	status, err := ReadStatus(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Print("DDNS could not read saved status")
	}
	if err != nil || status.ConfigID != config.identity() {
		status = Status{ConfigID: config.identity(), Record: config.Record}
	}
	status.RequiresRestart = false
	updater := newUpdater(config, creds)
	defer updater.client.CloseIdleConnections()
	ticker := time.NewTicker(time.Duration(config.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		next := updater.check(ctx, status, time.Now().UTC())
		if ctx.Err() != nil {
			return
		}
		if next.Error != "" && next.Error != status.Error {
			log.Printf("DDNS record %s: %s", next.Record, next.Error)
		}
		if next.Error == "" && (status.Error != "" || next.PublishedAddress != status.PublishedAddress) {
			log.Printf("DDNS record %s: %s (%s)", next.Record, next.Result, next.PublishedAddress)
		}
		if err := saveStatus(dir, next); err != nil {
			log.Print("DDNS could not save status")
		}
		status = next
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
