package privilegedagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

const journalEventLimit = 80

var diagnosticUnits = append(slices.Clone(dashboardUnits),
	"debian-router-ddns.service", "debian-router-pppoe.service",
)

type DiagnosticSnapshot struct {
	Services      []ServiceState    `json:"services"`
	JournalStatus string            `json:"journal_status"`
	Events        []DiagnosticEvent `json:"events"`
}

type DiagnosticEvent struct {
	At       time.Time `json:"at"`
	Unit     string    `json:"unit"`
	Priority int       `json:"priority"`
	Category string    `json:"category"`
}

func readDiagnostics(ctx context.Context) DiagnosticSnapshot {
	snapshot := DiagnosticSnapshot{
		Services:      readUnitStates(ctx, diagnosticUnits),
		JournalStatus: "unavailable",
		Events:        []DiagnosticEvent{},
	}
	args := []string{"--system", "--boot", "--since=-24h", "--priority=warning",
		"--lines=" + strconv.Itoa(journalEventLimit), "--no-pager", "--quiet", "--output=json",
		"--output-fields=_SYSTEMD_UNIT,_PID,UNIT,PRIORITY,MESSAGE"}
	for _, unit := range diagnosticUnits {
		args = append(args, "--unit="+unit)
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/journalctl", args...).Output()
	if err != nil {
		return snapshot
	}
	events, err := diagnosticEvents(output)
	if err == nil {
		snapshot.JournalStatus = "available"
		snapshot.Events = events
	}
	return snapshot
}

func diagnosticEvents(output []byte) ([]DiagnosticEvent, error) {
	events := make([]DiagnosticEvent, 0)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() && len(events) < journalEventLimit {
		var entry struct {
			At          string          `json:"__REALTIME_TIMESTAMP"`
			ProcessID   string          `json:"_PID"`
			ProcessUnit string          `json:"_SYSTEMD_UNIT"`
			ManagerUnit string          `json:"UNIT"`
			Priority    string          `json:"PRIORITY"`
			Message     json.RawMessage `json:"MESSAGE"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		unit := entry.ProcessUnit
		if entry.ProcessID == "1" {
			unit = entry.ManagerUnit
		}
		if !slices.Contains(diagnosticUnits, unit) {
			continue
		}
		priority, priorityErr := strconv.Atoi(entry.Priority)
		at, atErr := strconv.ParseInt(entry.At, 10, 64)
		timestamp := time.UnixMicro(at).UTC()
		if priorityErr != nil || priority < 0 || priority > 4 || atErr != nil || at <= 0 || timestamp.Year() > 9999 {
			continue
		}
		var message string
		_ = json.Unmarshal(entry.Message, &message)
		events = append(events, DiagnosticEvent{
			At: timestamp, Unit: unit, Priority: priority, Category: eventCategory(message),
		})
	}
	return events, scanner.Err()
}

// A journal message can contain arbitrary credentials. Only fixed categories
// cross the agent socket, including when a message has no recognized wording.
func eventCategory(message string) string {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "failed with result"),
		strings.Contains(message, "main process exited"),
		strings.Contains(message, "failed to start"),
		strings.Contains(message, "start request repeated"):
		return "service_failure"
	case strings.Contains(message, "authentication fail"),
		strings.Contains(message, "invalid credentials"),
		strings.Contains(message, "permission denied"):
		return "access_failure"
	case strings.Contains(message, "timed out"),
		strings.Contains(message, "timeout"),
		strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "connection refused"),
		strings.Contains(message, "connection reset"),
		strings.Contains(message, "network is unreachable"),
		strings.Contains(message, "no route to host"):
		return "connection_failure"
	case strings.Contains(message, "tls handshake"),
		strings.Contains(message, "certificate"),
		strings.Contains(message, "x509:"):
		return "tls_failure"
	default:
		return "unclassified"
	}
}
