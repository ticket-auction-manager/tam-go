package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingSettingsLoadsSurvivingBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Defaults()
	want.RemoteServer, want.RemoteKey, want.RemoteFingerprint = "replacement.test", "test-key", strings.Repeat("a", 64)
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(BackupPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	var loadError *LoadError
	if got != want || !errors.As(err, &loadError) || !loadError.FromBackup {
		t.Errorf("missing primary: got %+v, %v; want surviving backup and warning", got, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("loading recovery settings rewrote the missing primary: %v", err)
	}
	after, err := os.ReadFile(BackupPath(path))
	if err != nil || string(after) != string(before) {
		t.Errorf("loading recovery settings changed the backup: %q, %v", after, err)
	}
	live := Open(path)
	for i := 0; i < 3; i++ {
		if live.Get() != want || !strings.Contains(live.Problem(), "settings.json.bak") {
			t.Fatalf("live recovery settings: %+v, problem=%q", live.Get(), live.Problem())
		}
	}
	if _, err := live.Update(func(s Settings) (Settings, error) { return s, nil }); err != nil {
		t.Fatal(err)
	}
	if live.Problem() != "" {
		t.Fatalf("explicit settings save did not clear the warning: %q", live.Problem())
	}
	if restored, err := Load(path); err != nil || restored != want {
		t.Fatalf("explicit repair: %+v, %v", restored, err)
	}
}

func TestMissingSettingsDoesNotOverwriteBrokenBackup(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "unreadable"}[unreadable], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			backup := BackupPath(path)
			if unreadable {
				if err := os.Mkdir(backup, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(backup, []byte(`{"remote_key":"recover-me",`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			var loadError *LoadError
			if got != Defaults() || !errors.As(err, &loadError) || loadError.FromBackup {
				t.Errorf("unusable backup should show default settings with a warning: %+v, %v", got, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("unusable backup was treated as a fresh installation: %v", err)
			}
			if !unreadable {
				data, err := os.ReadFile(backup)
				if err != nil || string(data) != `{"remote_key":"recover-me",` {
					t.Fatalf("recovery bytes changed: %q, %v", data, err)
				}
			}
		})
	}
}

func TestMissingSettingsKeepsNewerLiveSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	old := Defaults()
	old.RemoteServer, old.RemoteKey = "old.test", "old-key"
	if err := Save(path, old); err != nil {
		t.Fatal(err)
	}
	live := Open(path)
	newer := old
	newer.RemoteServer, newer.RemoteKey = "new.test", "new-key"
	// A hand edit can be newer than the backup written by the last UI save.
	if err := os.WriteFile(path, []byte(`{"remote_server":"new.test","remote_key":"new-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if live.Get() != newer {
		t.Fatal("hand edit was not loaded")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if live.Get() != newer || live.Problem() == "" {
		t.Fatalf("missing primary discarded newer live settings: %+v, %q", live.Get(), live.Problem())
	}
	backup, err := Load(BackupPath(path))
	if err != nil || backup != old {
		t.Fatalf("missing primary overwrote the previous backup: %+v, %v", backup, err)
	}
}
