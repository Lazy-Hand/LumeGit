// Package store keeps LumeGit's local app data: recent repositories,
// per-repo changelist drafts, and window layout. All writes go through
// one file so a crash at worst loses the whole file, not a half-migrated
// database.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Repo describes one known repository. Path is the absolute working
// tree; Name is its display label (defaults to the basename); Pinned
// keeps it on top of the list; LastOpened orders recency; VCSKind
// remembers which system manages it ("git"/"svn") so the picker can
// show the icon and skip re-detection hints.
type Repo struct {
	Path       string
	Name       string
	Pinned     bool
	LastOpened time.Time
	VCSKind    string // "git" | "svn" | "" (unknown / older entries)
}

// Manager loads and saves the repo list. The file lives under
// os.UserConfigDir()/lumegit so it survives worktree deletion.
type Manager struct {
	path  string     // JSON file path
	mu    sync.Mutex // guards repos + lists: shared across all app windows
	repos []Repo
	lists map[string]*Changelists // per-repo changelist groups
}

// Load reads the repo list; a missing file starts empty (no error).
func Load() (*Manager, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	path := filepath.Join(dir, "lumegit", "repos.json")
	m := &Manager{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	var raw struct {
		Repos []Repo
		Lists map[string]*rawChangelists `json:"lists"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	m.repos = raw.Repos
	m.lists = map[string]*Changelists{}
	for path, rc := range raw.Lists {
		m.lists[path] = &Changelists{Default: rc.Default, Lists: rc.Lists}
		if m.lists[path].Default == "" {
			m.lists[path].Default = DefaultName
		}
	}
	m.sort()
	return m, nil
}

// List returns repositories in display order: pinned first, then most
// recently opened.
func (m *Manager) List() []Repo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Repo, len(m.repos))
	copy(out, m.repos)
	return out
}

// Add remembers a repo. If already present, its LastOpened updates and
// an empty Name is repaired (not overwritten). kind is the detected
// VCS ("git"/"svn", "" when unknown) — once set it is never downgraded
// back to empty by later calls that don't know it.
func (m *Manager) Add(path, name, kind string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	for i := range m.repos {
		if m.repos[i].Path == path {
			m.repos[i].LastOpened = time.Now()
			if name != "" && m.repos[i].Name == "" {
				m.repos[i].Name = name
			}
			if kind != "" {
				m.repos[i].VCSKind = kind
			}
			m.sort()
			return
		}
	}
	if name == "" {
		name = filepath.Base(path)
	}
	m.repos = append(m.repos, Repo{Path: path, Name: name, LastOpened: time.Now(), VCSKind: kind})
	m.sort()
}

// Remove forgets a repo by path.
func (m *Manager) Remove(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	for i := range m.repos {
		if m.repos[i].Path == path {
			m.repos = append(m.repos[:i], m.repos[i+1:]...)
			return
		}
	}
}

// SetPinned toggles a repo's pinned state.
func (m *Manager) SetPinned(path string, pinned bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	for i := range m.repos {
		if m.repos[i].Path == path {
			m.repos[i].Pinned = pinned
			m.sort()
			return
		}
	}
}

// sort applies pinned-then-recent ordering.
func (m *Manager) sort() {
	sort.SliceStable(m.repos, func(i, j int) bool {
		a, b := m.repos[i], m.repos[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		return a.LastOpened.After(b.LastOpened)
	})
}

// Save writes the repo list atomically (temp file + rename).
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw := struct {
		Repos []Repo                     `json:"repos"`
		Lists map[string]*rawChangelists `json:"lists,omitempty"`
	}{Repos: m.repos}
	if len(m.lists) > 0 {
		raw.Lists = map[string]*rawChangelists{}
		for path, cl := range m.lists {
			raw.Lists[path] = &rawChangelists{Default: cl.Default, Lists: cl.Lists}
		}
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// ValidatePath checks a user-entered path is a usable directory. An
// empty path is allowed (means "use picker"); a non-existent or non-dir
// path is an error.
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("path is empty")
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}
