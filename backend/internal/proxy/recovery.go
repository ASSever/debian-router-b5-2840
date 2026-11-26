package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

var errRecoverySuperseded = errors.New("proxy_profile_superseded")

type recoverySnapshot struct {
	generation string
	profile    Profile
	group      int
	config     map[string]json.RawMessage
	outbounds  []map[string]any
}

// Recovery checks the selected group with disposable single-outbound workers.
type Recovery struct {
	Manager    *ProfileManager
	Group      string
	URL        string
	Trust      *tls.Config
	StatusCode int
	Logger     *log.Logger

	generation      string
	failures        int
	installLockPath string
}

// Tick checks the selected member and promotes a confirmed backup after two failures.
func (r *Recovery) Tick(ctx context.Context) error {
	s, err := r.read(ctx)
	if err != nil || s.generation == "" {
		r.generation, r.failures = "", 0
		return err
	}
	if r.generation != s.generation {
		r.generation, r.failures = s.generation, 0
	}
	group := s.profile.Groups[s.group]
	reason := r.probe(ctx, s.outbound(serverTag(group.Selected)))
	if ctx.Err() != nil {
		return nil
	}
	if reason == "" {
		r.failures = 0
		return nil
	}
	if r.failures < 2 {
		r.failures++
	}
	r.Logger.Printf("group=%s selected=%d check_failed=%s", group.Name, group.Selected, reason)
	if r.failures < 2 {
		return nil
	}
	for _, line := range group.Members {
		if ctx.Err() != nil {
			return nil
		}
		if line == group.Selected {
			continue
		}
		if reason := r.probe(ctx, s.outbound(serverTag(line))); reason != "" {
			r.Logger.Printf("group=%s backup=%d check_failed=%s", group.Name, line, reason)
			continue
		}
		r.Logger.Printf("group=%s backup=%d check_passed", group.Name, line)
		err := r.promote(ctx, s, line)
		if errors.Is(err, errRecoverySuperseded) {
			return nil
		}
		if err != nil {
			return err
		}
		r.Logger.Printf("group=%s switched_from=%d switched_to=%d", group.Name, group.Selected, line)
		r.generation, r.failures = "", 0
		return nil
	}
	return nil
}

func (r *Recovery) installerLock(ctx context.Context) (func(), error) {
	path := r.installLockPath
	if path == "" {
		path = "/run/lock/debian-router-install.lock"
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	return lockFile(ctx, path)
}

func (r *Recovery) read(ctx context.Context) (recoverySnapshot, error) {
	unlock, err := r.installerLock(ctx)
	if err != nil {
		return recoverySnapshot{}, nil
	}
	defer unlock()
	m := r.Manager
	m.lock.Lock()
	defer m.lock.Unlock()
	release, err := lockProfiles(ctx, m.root)
	if err != nil {
		return recoverySnapshot{}, ErrProfileUnavailable
	}
	defer release()
	if err := m.recover(ctx); err != nil {
		return recoverySnapshot{}, err
	}
	name, err := m.activeGeneration()
	if err != nil {
		return recoverySnapshot{}, err
	}
	if name == "initial" {
		return recoverySnapshot{}, nil
	}
	s := recoverySnapshot{generation: name, group: -1}
	dir := filepath.Join(m.root, name)
	if readJSON(filepath.Join(dir, "profile.json"), &s.profile) != nil {
		return recoverySnapshot{}, ErrProfileUnavailable
	}
	for i, group := range s.profile.Groups {
		if group.Name == r.Group && len(group.Members) > 1 {
			s.group = i
			break
		}
	}
	if s.group < 0 {
		return recoverySnapshot{}, nil
	}
	if readJSON(filepath.Join(dir, "config.json"), &s.config) != nil ||
		json.Unmarshal(s.config["outbounds"], &s.outbounds) != nil {
		return recoverySnapshot{}, ErrProfileUnavailable
	}
	group := s.profile.Groups[s.group]
	if selector := s.outbound("group-" + group.Name); selector["type"] != "selector" ||
		selector["default"] != serverTag(group.Selected) {
		return recoverySnapshot{}, ErrProfileUnavailable
	}
	return s, nil
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (s recoverySnapshot) outbound(tag string) map[string]any {
	for _, outbound := range s.outbounds {
		if outbound["tag"] == tag {
			return outbound
		}
	}
	return nil
}

func (r *Recovery) probe(ctx context.Context, outbound map[string]any) string {
	if outbound["type"] != "vless" && outbound["type"] != "hysteria2" {
		return "unsupported_options"
	}
	copyOutbound := make(map[string]any, len(outbound))
	for key, value := range outbound {
		copyOutbound[key] = value
	}
	copyOutbound["tag"] = "selected"
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return checkWorker(ctx, InstalledSingBox(), copyOutbound, r.URL, r.Trust, r.StatusCode)
}

func (r *Recovery) promote(ctx context.Context, s recoverySnapshot, line int) error {
	unlock, err := r.installerLock(ctx)
	if err != nil {
		return errRecoverySuperseded
	}
	defer unlock()
	m := r.Manager
	m.lock.Lock()
	defer m.lock.Unlock()
	release, err := lockProfiles(ctx, m.root)
	if err != nil {
		return ErrProfileUnavailable
	}
	defer release()
	current, err := m.activeGeneration()
	if err != nil || current != s.generation {
		return errRecoverySuperseded
	}
	if _, err := os.Stat(filepath.Join(m.root, "pending")); !errors.Is(err, os.ErrNotExist) {
		return errRecoverySuperseded
	}
	group := &s.profile.Groups[s.group]
	group.Selected = line
	s.outbound("group-" + group.Name)["default"] = serverTag(line)
	data, err := json.Marshal(s.outbounds)
	if err != nil {
		return ErrProfileUnavailable
	}
	s.config["outbounds"] = data
	data, err = json.Marshal(s.config)
	if err != nil {
		return ErrProfileUnavailable
	}
	return m.apply(ctx, s.generation, s.profile, data)
}
