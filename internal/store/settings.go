package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Settings holds the user's persisted UI preferences. Zero-valued
// fields mean "not set yet" — callers fall back to their built-in
// defaults, so an old or partial settings.json stays valid across
// versions that add keys.
type Settings struct {
	// Diff viewing.
	DiffMode  int     `json:"diffMode,omitempty"`  // 0 = split, 1 = unified
	DiffSplit float32 `json:"diffSplit,omitempty"` // old-side width, percent 0-100

	// Pane widths (DIP). These persist separately from the window's own
	// geometry (handled by mygo's StateKey) because they describe the
	// split inside the content, not the OS window.
	FilesPaneW float32 `json:"filesPaneW,omitempty"`
	HistListW  float32 `json:"histListW,omitempty"`
	HistFilesW float32 `json:"histFilesW,omitempty"`
	ShelfListW float32 `json:"shelfListW,omitempty"`
	ShelfFileW float32 `json:"shelfFileW,omitempty"`

	// Last active main tab: 0=提交, 1=历史, 2=搁置.
	Tab int `json:"tab,omitempty"`

	// Commit workspace mode: 0 = Changelist, 1 = Git Staging.
	CommitMode int `json:"commitMode,omitempty"`
}

// SettingsManager loads and saves the single Settings. The file lives
// beside repos.json under os.UserConfigDir()/lumegit; one process-
// shared instance serializes access so concurrent windows can't tear
// the file.
type SettingsManager struct {
	path string
	mu   sync.Mutex
	s    Settings
}

// LoadSettings reads the preferences file; a missing or unreadable file
// yields a zero Settings (all defaults) rather than an error — the app
// must still start.
func LoadSettings() (*SettingsManager, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	m := &SettingsManager{path: filepath.Join(dir, "lumegit", "settings.json")}
	return m, m.loadFile()
}

// loadFile reads m.path into m.s. Missing and corrupt files both yield
// a zero Settings — the app must start regardless.
func (m *SettingsManager) loadFile() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(b, &m.s)
	return nil
}

// Get returns the current settings copy.
func (m *SettingsManager) Get() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}

// Save replaces the settings and writes the file atomically
// (temp + rename), same crash contract as Manager.Save.
func (m *SettingsManager) Save(s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s = s
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
