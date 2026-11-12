package store

import (
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	m := &SettingsManager{path: filepath.Join(t.TempDir(), "settings.json")}
	got := m.Get()
	if got.DiffMode != 0 || got.FilesPaneW != 0 {
		t.Fatalf("fresh settings = %+v, want zero", got)
	}
	want := Settings{
		DiffMode: 1, DiffSplit: 42.5,
		FilesPaneW: 320, HistListW: 400, HistFilesW: 270,
		ShelfListW: 310, ShelfFileW: 230,
		Tab: 2, CommitMode: 1,
	}
	if err := m.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Re-read from disk through a second manager (as a new process would).
	m2 := &SettingsManager{path: m.path}
	if err := m2.loadFile(); err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if m2.Get() != want {
		t.Fatalf("round trip = %+v, want %+v", m2.Get(), want)
	}
}

func TestSettingsZeroFieldsMeanDefaults(t *testing.T) {
	// A settings.json written by an older version (missing keys) must
	// unmarshal to zeros so the caller's defaults apply — never an error.
	m := &SettingsManager{path: filepath.Join(t.TempDir(), "settings.json")}
	if err := m.Save(Settings{Tab: 1}); err != nil {
		t.Fatal(err)
	}
	if s := m.Get(); s.FilesPaneW != 0 || s.DiffMode != 0 || s.Tab != 1 {
		t.Fatalf("partial settings = %+v", s)
	}
}
