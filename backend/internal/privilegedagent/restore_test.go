package privilegedagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreConfigRollbackAndInvalidCandidate(t *testing.T) {
	dir := t.TempDir()
	name := "etc/debian-router-b5-2840/mqtt.json"
	path := filepath.Join(dir, name)
	for _, parent := range []string{filepath.Dir(path), filepath.Join(dir, restoreState)} {
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := `{"broker":"mqtt://localhost:1883","client_id":"before","topic_prefix":"test"}`
	next := `{"broker":"mqtt://localhost:1883","client_id":"after","topic_prefix":"test"}`
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	assertConfig := func(want string) {
		t.Helper()
		state, err := os.ReadFile(path)
		if err != nil || string(state) != want {
			t.Fatal("config differs from expected state")
		}
	}
	id := "0123456789abcdef0123456789abcdef"
	request := RestoreRequest{ID: id, Configs: []BackupConfig{{Name: name, Data: []byte("{}")}}}
	if _, err := restoreConfigOperation(dir, "restore-apply", request); err == nil {
		t.Fatal("invalid config accepted")
	}
	assertConfig(old)
	request.Configs[0].Data = []byte(next)
	if _, err := restoreConfigOperation(dir, "restore-apply", request); err != nil {
		t.Fatal(err)
	}
	assertConfig(next)
	if _, err := restoreConfigOperation(dir, "restore-rollback", RestoreRequest{ID: id}); err != nil {
		t.Fatal(err)
	}
	assertConfig(old)
}
