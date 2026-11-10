package store

import (
	"time"
)

// Changelist is one named task group within a repository. Paths are
// repo-relative worktree paths (as reported by git status). A file
// belongs to exactly one list; unassigned paths live in the implicit
// "default" list which is always present.
type Changelist struct {
	Name    string
	Paths   []string // sorted, unique
	Active  bool     // new changes land here
	Created time.Time
}

// Changelists is the per-repo grouping metadata.
type Changelists struct {
	// Default is the always-present fallback list name (IDEA uses
	// "Default Changelist"). Unmatched or newly-seen files land here.
	Default string
	Lists   []*Changelist // ordered, does not include Default
}

// DefaultName is the fallback group every repo starts with.
const DefaultName = "默认"

// ChangelistsFor returns the repo's grouping, creating an empty set
// (with only Default) on first access. The returned pointer is shared
// by every window that has this repo open — mutation should happen on
// the UI thread (mygo serializes all windows' view/update on one main
// thread, so unsynchronized access there is safe).
func (m *Manager) ChangelistsFor(repoPath string) *Changelists {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lists == nil {
		m.lists = map[string]*Changelists{}
	}
	cl := m.lists[repoPath]
	if cl == nil {
		cl = &Changelists{Default: DefaultName}
		m.lists[repoPath] = cl
	}
	return cl
}

// rawChangelists is the on-disk shape inside repos.json.
type rawChangelists struct {
	Default string
	Lists   []*Changelist
}

// All returns every list including a synthesized Default entry at the
// head, so callers never special-case it.
func (cl *Changelists) All() []*Changelist {
	out := make([]*Changelist, 0, len(cl.Lists)+1)
	out = append(out, &Changelist{Name: cl.Default, Active: cl.activeIs(cl.Default)})
	out = append(out, cl.Lists...)
	return out
}

// Find locates a list by name; nil when absent.
func (cl *Changelists) Find(name string) *Changelist {
	if name == cl.Default {
		return &Changelist{Name: name, Active: cl.activeIs(name)}
	}
	for _, l := range cl.Lists {
		if l.Name == name {
			return l
		}
	}
	return nil
}

// activeIs reports whether name is the active list.
func (cl *Changelists) activeIs(name string) bool {
	for _, l := range cl.Lists {
		if l.Active {
			return l.Name == name
		}
	}
	return name == cl.Default // no explicit active → default is active
}

// Active returns the active list name.
func (cl *Changelists) Active() string {
	for _, l := range cl.Lists {
		if l.Active {
			return l.Name
		}
	}
	return cl.Default
}

// Create adds a named list (no-op when the name exists).
func (cl *Changelists) Create(name string) {
	if name == "" || name == cl.Default || cl.Find(name) != nil {
		return
	}
	cl.Lists = append(cl.Lists, &Changelist{Name: name, Created: time.Now()})
}

// Rename renames a non-default list.
func (cl *Changelists) Rename(old, new string) {
	l := cl.Find(old)
	if l == nil || old == cl.Default || new == "" || new == cl.Default {
		return
	}
	if cl.Find(new) != nil {
		return // target name taken
	}
	l.Name = new
}

// Remove deletes a list; its paths fall back to Default.
func (cl *Changelists) Remove(name string) {
	if name == cl.Default {
		return
	}
	for i, l := range cl.Lists {
		if l.Name == name {
			cl.Lists = append(cl.Lists[:i], cl.Lists[i+1:]...)
			return
		}
	}
}

// SetActive marks name as the active list (exactly one).
func (cl *Changelists) SetActive(name string) {
	if cl.Find(name) == nil {
		return
	}
	for _, l := range cl.Lists {
		l.Active = l.Name == name
	}
	// Setting default active means no named list is active.
	if name == cl.Default {
		for _, l := range cl.Lists {
			l.Active = false
		}
	}
}

// Assign puts path into the named list, removing it from any other.
// A path appears in at most one list. Unknown or empty list names go
// to Default (which means "remove from every named list").
func (cl *Changelists) Assign(path, list string) {
	cl.Unassign(path)
	if list == "" || list == cl.Default {
		return
	}
	l := cl.Find(list)
	if l == nil {
		return
	}
	l.Paths = appendUnique(l.Paths, path)
}

// Unassign removes path from every named list.
func (cl *Changelists) Unassign(path string) {
	for _, l := range cl.Lists {
		l.Paths = removeStr(l.Paths, path)
	}
}

// ListOf returns the name of the list holding path (Default when none).
func (cl *Changelists) ListOf(path string) string {
	for _, l := range cl.Lists {
		for _, p := range l.Paths {
			if p == path {
				return l.Name
			}
		}
	}
	return cl.Default
}

// Reconcile drops assignments for paths that no longer appear in the
// live status set and returns the still-valid ones. IDEA keeps a
// changelist's file assignments until they stop matching; we keep it
// simpler — a path not in status is unassigned, and callers re-add
// it to Default via the normal assignment path.
func (cl *Changelists) Reconcile(live map[string]bool) {
	for _, l := range cl.Lists {
		kept := l.Paths[:0]
		for _, p := range l.Paths {
			if live[p] {
				kept = append(kept, p)
			}
		}
		l.Paths = kept
	}
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func removeStr(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
