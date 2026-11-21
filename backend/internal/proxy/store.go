package proxy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const stateFile = "proxy-subscription.json"

var (
	ErrServerNotFound      = errors.New("server_not_found")
	ErrSubscriptionChanged = errors.New("subscription_changed")
)

// ReadServer retrieves one private server from the same subscription revision the caller displayed.
func ReadServer(directory, id string, line int) (Server, error) {
	data, err := os.ReadFile(filepath.Join(directory, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return Server{}, ErrServerNotFound
	}
	if err != nil {
		return Server{}, err
	}
	var state Subscription
	if err := json.Unmarshal(data, &state); err != nil {
		return Server{}, err
	}
	if state.Report.ID != id {
		return Server{}, ErrSubscriptionChanged
	}
	for _, server := range state.Servers {
		if server.Line == line {
			return server, nil
		}
	}
	return Server{}, ErrServerNotFound
}

// ReadReport loads the last saved diagnostics without returning any credentials.
func ReadReport(directory string) (Report, error) {
	data, err := os.ReadFile(filepath.Join(directory, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return Report{Rows: []Row{}}, nil
	}
	if err != nil {
		return Report{}, err
	}
	var state struct {
		Report Report `json:"report"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return Report{}, err
	}
	return state.Report, nil
}

// Save atomically replaces the subscription. The temporary file is created with mode 0600.
func Save(directory string, subscription Subscription) error {
	staged, err := os.CreateTemp(directory, ".proxy-subscription-*")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if err := json.NewEncoder(staged).Encode(subscription); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	return os.Rename(staged.Name(), filepath.Join(directory, stateFile))
}
