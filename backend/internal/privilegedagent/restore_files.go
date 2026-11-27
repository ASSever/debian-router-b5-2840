package privilegedagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

var restoreIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidRestoreID(id string) bool { return restoreIDPattern.MatchString(id) }

// RestoreFile records the former state of a fixed destination.
type RestoreFile struct {
	Exists bool        `json:"exists"`
	Data   []byte      `json:"data,omitempty"`
	Mode   os.FileMode `json:"mode,omitempty"`
	Link   string      `json:"link,omitempty"`
}

func CaptureRestoreFile(root *os.Root, name string) (RestoreFile, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return RestoreFile{}, nil
	}
	if err != nil {
		return RestoreFile{}, err
	}
	data, err := ReadBackupFile(root, name, MaxBackupConfigBytes)
	if err != nil {
		return RestoreFile{}, err
	}
	return RestoreFile{Exists: true, Data: data, Mode: info.Mode().Perm()}, nil
}

// SaveRestoreJSON makes a rollback record durable before replacing its files.
func SaveRestoreJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return WriteRestoreFile(path, RestoreFile{Exists: true, Data: data, Mode: 0600})
}

// WriteRestoreFile replaces one pathname atomically, then syncs its directory.
func WriteRestoreFile(path string, state RestoreFile) error {
	dir := filepath.Dir(path)
	if !state.Exists {
		if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return syncRestoreDir(dir)
	}
	if state.Link != "" {
		tmp, err := os.MkdirTemp(dir, ".restore-link-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		link := filepath.Join(tmp, "link")
		if err := os.Symlink(state.Link, link); err != nil {
			return err
		}
		if err := os.Rename(link, path); err != nil {
			return err
		}
		return syncRestoreDir(dir)
	}
	f, err := os.CreateTemp(dir, ".restore-file-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	writeErr := f.Chmod(state.Mode)
	if writeErr == nil {
		_, writeErr = f.Write(state.Data)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncRestoreDir(dir)
}

func syncRestoreDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
