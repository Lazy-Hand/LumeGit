package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestChangelistBasics covers create/assign/move/remove/active.
func TestChangelistBasics(t *testing.T) {
	m := &Manager{path: filepath.Join(t.TempDir(), "repos.json"), lists: map[string]*Changelists{}}
	cl := m.ChangelistsFor("/repo")

	// Fresh repo: only Default, and it's active.
	if got := cl.Active(); got != DefaultName {
		t.Fatalf("default active = %q", got)
	}
	if len(cl.Lists) != 0 {
		t.Fatalf("lists = %v", cl.Lists)
	}

	cl.Create("登录功能")
	cl.Create("修复设置")
	if len(cl.Lists) != 2 {
		t.Fatalf("lists = %d", len(cl.Lists))
	}

	// Assign puts a file in exactly one list.
	cl.Assign("a.go", "登录功能")
	cl.Assign("b.go", "登录功能")
	cl.Assign("c.go", "修复设置")
	if got := cl.ListOf("a.go"); got != "登录功能" {
		t.Errorf("ListOf a.go = %q", got)
	}
	if got := cl.ListOf("x.go"); got != DefaultName {
		t.Errorf("unassigned ListOf = %q", got)
	}

	// Re-assign moves it out of the old list.
	cl.Assign("a.go", "修复设置")
	if got := cl.ListOf("a.go"); got != "修复设置" {
		t.Errorf("moved ListOf = %q", got)
	}
	login := cl.Find("登录功能")
	if len(login.Paths) != 1 || login.Paths[0] != "b.go" {
		t.Errorf("登录功能 paths = %v", login.Paths)
	}

	// SetActive.
	cl.SetActive("修复设置")
	if got := cl.Active(); got != "修复设置" {
		t.Errorf("active = %q", got)
	}
	cl.SetActive(DefaultName)
	if got := cl.Active(); got != DefaultName {
		t.Errorf("default re-active = %q", got)
	}

	// Remove returns files to default.
	cl.Remove("修复设置")
	if cl.Find("修复设置") != nil {
		t.Error("list not removed")
	}
	if got := cl.ListOf("c.go"); got != DefaultName {
		t.Errorf("orphaned file ListOf = %q", got)
	}
	if got := cl.ListOf("a.go"); got != DefaultName {
		t.Errorf("moved-orphan ListOf = %q", got)
	}
}

// TestChangelistReconcile drops stale paths on refresh.
func TestChangelistReconcile(t *testing.T) {
	m := &Manager{path: filepath.Join(t.TempDir(), "repos.json"), lists: map[string]*Changelists{}}
	cl := m.ChangelistsFor("/repo")
	cl.Create("任务")
	cl.Assign("keep.go", "任务")
	cl.Assign("gone.go", "任务")

	cl.Reconcile(map[string]bool{"keep.go": true})
	l := cl.Find("任务")
	if len(l.Paths) != 1 || l.Paths[0] != "keep.go" {
		t.Errorf("after reconcile paths = %v", l.Paths)
	}
}

// TestChangelistPersists round-trips through the JSON file.
func TestChangelistPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	m := &Manager{path: path, lists: map[string]*Changelists{}}
	cl := m.ChangelistsFor("/repo")
	cl.Create("任务A")
	cl.Assign("x.go", "任务A")
	cl.SetActive("任务A")
	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload via the real Load path.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("empty save")
	}
	m2 := &Manager{path: path, lists: map[string]*Changelists{}}
	// Simulate the Load unmarshal.
	var raw struct {
		Repos []Repo
		Lists map[string]*rawChangelists `json:"lists"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m2.repos = raw.Repos
	for p, rc := range raw.Lists {
		m2.lists[p] = &Changelists{Default: rc.Default, Lists: rc.Lists}
		if m2.lists[p].Default == "" {
			m2.lists[p].Default = DefaultName
		}
	}
	cl2 := m2.ChangelistsFor("/repo")
	if got := cl2.ListOf("x.go"); got != "任务A" {
		t.Errorf("reloaded ListOf = %q", got)
	}
	if got := cl2.Active(); got != "任务A" {
		t.Errorf("reloaded active = %q", got)
	}
}
