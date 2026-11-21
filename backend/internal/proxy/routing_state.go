package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const proxyUnit = "debian-router-sing-box.service"

var (
	ErrProfileUnavailable    = errors.New("proxy_profile_unavailable")
	ErrProfileRejected       = errors.New("proxy_config_rejected")
	ErrProfileRolledBack     = errors.New("proxy_apply_rolled_back")
	ErrProfileRollbackFailed = errors.New("proxy_rollback_failed")
	profileGeneration        = regexp.MustCompile(`^(initial|generation-[0-9]+)$`)
	singBoxVersion           = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// InstalledSingBox locates the checksum-pinned binary selected by the current release.
func InstalledSingBox() string {
	const prefix = "/opt/debian-router-b5-2840"
	marker, err := os.ReadFile(filepath.Join(prefix, "current", "SING_BOX_SUPPORT"))
	if err != nil {
		return ""
	}
	version, _, _ := strings.Cut(string(marker), "\n")
	if !singBoxVersion.MatchString(version) {
		return ""
	}
	return filepath.Join(prefix, "sing-box", version, "sing-box")
}

// ProfileManager serializes replacements and keeps a rollback marker across agent interruptions.
type ProfileManager struct {
	root     string
	lock     sync.Mutex
	validate func(context.Context, []byte) error
	restart  func(context.Context) error
}

func NewProfileManager(root string) *ProfileManager {
	return &ProfileManager{
		root: root,
		validate: func(ctx context.Context, config []byte) error {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return commandWithConfig(ctx, InstalledSingBox(), "check", config).Run()
		},
		restart: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_ = exec.CommandContext(ctx, "/usr/bin/systemctl", "reset-failed", proxyUnit).Run()
			if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "restart", proxyUnit).Run(); err != nil {
				return err
			}
			return exec.CommandContext(ctx, "/usr/bin/systemctl", "is-active", "--quiet", proxyUnit).Run()
		},
	}
}

func (m *ProfileManager) Status() (Profile, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if _, err := os.Stat(filepath.Join(m.root, "pending")); !errors.Is(err, os.ErrNotExist) {
		return Profile{}, ErrProfileUnavailable
	}
	name, err := m.activeGeneration()
	if err != nil {
		return Profile{}, err
	}
	if name == "initial" {
		return DefaultProfile(), nil
	}
	data, err := os.ReadFile(filepath.Join(m.root, name, "profile.json"))
	if err != nil {
		return Profile{}, ErrProfileUnavailable
	}
	var profile Profile
	if json.Unmarshal(data, &profile) != nil {
		return Profile{}, ErrProfileUnavailable
	}
	return profile, nil
}

func (m *ProfileManager) Apply(ctx context.Context, change ProfileChange) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	if err := m.recover(ctx); err != nil {
		return err
	}
	previous, err := m.activeGeneration()
	if err != nil {
		return err
	}
	config, err := CompileProfile(change)
	if err != nil {
		return ErrInvalidProfile
	}
	if err := m.validate(ctx, config); err != nil {
		return ErrProfileRejected
	}
	model, err := json.Marshal(change.Profile)
	if err != nil {
		return ErrInvalidProfile
	}
	staged, err := os.MkdirTemp(m.root, "generation-")
	if err != nil {
		return ErrProfileUnavailable
	}
	name := filepath.Base(staged)
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staged)
		}
	}()
	if writeProfileFile(filepath.Join(staged, "config.json"), config) != nil ||
		writeProfileFile(filepath.Join(staged, "profile.json"), model) != nil ||
		writeProfileFile(filepath.Join(m.root, "pending"), []byte(previous)) != nil {
		return ErrProfileUnavailable
	}
	if m.switchActive(name) != nil || m.restart(ctx) != nil || m.clearPending(previous) != nil {
		// A timed-out caller must not cancel the attempt to restore the working service.
		if err := m.restore(context.Background(), previous); err != nil {
			_ = writeProfileFile(filepath.Join(m.root, "pending"), []byte(previous))
			keep = true
			return ErrProfileRollbackFailed
		}
		return ErrProfileRolledBack
	}
	keep = true
	if previous != "initial" {
		_ = os.RemoveAll(filepath.Join(m.root, previous))
	}
	return nil
}

func (m *ProfileManager) Recover(ctx context.Context) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.recover(ctx)
}

func (m *ProfileManager) recover(ctx context.Context) error {
	data, err := os.ReadFile(filepath.Join(m.root, "pending"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrProfileRollbackFailed
	}
	return m.restore(ctx, string(data))
}

func (m *ProfileManager) restore(ctx context.Context, previous string) error {
	if !profileGeneration.MatchString(previous) {
		return ErrProfileRollbackFailed
	}
	if _, err := os.Stat(filepath.Join(m.root, previous, "config.json")); err != nil {
		return ErrProfileRollbackFailed
	}
	abandoned, _ := m.activeGeneration()
	if m.switchActive(previous) != nil || m.restart(ctx) != nil || m.clearPending(previous) != nil {
		return ErrProfileRollbackFailed
	}
	if abandoned != "" && abandoned != "initial" && abandoned != previous {
		_ = os.RemoveAll(filepath.Join(m.root, abandoned))
	}
	return nil
}

func (m *ProfileManager) activeGeneration() (string, error) {
	name, err := os.Readlink(filepath.Join(m.root, "active"))
	if err != nil || !profileGeneration.MatchString(name) {
		return "", ErrProfileUnavailable
	}
	return name, nil
}

func (m *ProfileManager) switchActive(name string) error {
	staged := filepath.Join(m.root, ".active-next")
	_ = os.Remove(staged)
	if err := os.Symlink(name, staged); err != nil {
		return err
	}
	defer os.Remove(staged)
	if err := os.Rename(staged, filepath.Join(m.root, "active")); err != nil {
		return err
	}
	return syncProfileDirectory(m.root)
}

func (m *ProfileManager) clearPending(previous string) error {
	path := filepath.Join(m.root, "pending")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncProfileDirectory(m.root); err != nil {
		if restoreErr := writeProfileFile(path, []byte(previous)); restoreErr != nil {
			return ErrProfileRollbackFailed
		}
		return err
	}
	return nil
}

func writeProfileFile(path string, data []byte) error {
	staged, err := os.CreateTemp(filepath.Dir(path), ".proxy-write-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if _, err := staged.Write(data); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), path); err != nil {
		return err
	}
	return syncProfileDirectory(filepath.Dir(path))
}

func syncProfileDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
