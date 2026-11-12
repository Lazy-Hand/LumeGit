package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"lumegit/internal/git"
	"lumegit/internal/store"
	"lumegit/internal/vcs"
)

func TestFilePaneRowsGroupsAndFilter(t *testing.T) {
	a := &app{}
	a.commit.Snap = &vcs.Snapshot{Files: []git.FileChange{
		{Path: "b.go", Kind: git.KindModified},
		{Path: "a.go", Kind: git.KindModified},
		{Path: "ignored", Kind: vcs.KindIgnored},
	}}
	a.commit.lists = &store.Changelists{Default: store.DefaultName, Lists: []*store.Changelist{
		{Name: "任务", Paths: []string{"b.go"}, Active: true},
	}}
	assertKeys := func(want ...string) {
		t.Helper()
		rows := a.filePaneRows()
		if len(rows) != len(want) {
			t.Fatalf("rows = %+v, want keys %v", rows, want)
		}
		for i := range want {
			if rows[i].key != want[i] {
				t.Fatalf("row %d key = %q, want %q", i, rows[i].key, want[i])
			}
		}
	}
	assertKeys("list-header:任务", "file:b.go", "list-header:默认", "file:a.go", "list-header:已忽略", "file:ignored")
	a.commit.collapsed["任务"] = true
	assertKeys("list-header:任务", "list-header:默认", "file:a.go", "list-header:已忽略", "file:ignored")
	a.commit.filter = "B.GO"
	assertKeys("file:b.go")
	a.commit.filter = ""
	a.commit.lists.Assign("a.go", "任务")
	delete(a.commit.collapsed, "任务")
	assertKeys("list-header:任务", "file:a.go", "file:b.go", "list-header:默认", "list-header:已忽略", "file:ignored")
}

func TestFilePaneRowsStagingGroups(t *testing.T) {
	a := &app{}
	a.commit.mode = 1
	a.commit.Snap = &vcs.Snapshot{Files: []git.FileChange{
		{Path: "tracked", Kind: git.KindModified},
		{Path: "untracked", Kind: git.KindUntracked},
	}}
	rows := a.filePaneRows()
	if len(rows) != 4 || rows[0].kind != paneStagingHeader || rows[1].file.change.Path != "tracked" ||
		rows[2].kind != paneStagingHeader || rows[3].file.change.Path != "untracked" {
		t.Fatalf("staging rows = %+v", rows)
	}
	a.commit.collapsed["stg-changes"] = true
	rows = a.filePaneRows()
	if len(rows) != 3 || rows[1].kind != paneStagingHeader || rows[2].file.change.Path != "untracked" {
		t.Fatalf("collapsed staging rows = %+v", rows)
	}
}

func TestFilePaneVirtualizesAndChecksOffscreenRows(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = &vcs.Snapshot{}
	for i := 0; i < 200; i++ {
		a.commit.Snap.Files = append(a.commit.Snap.Files, git.FileChange{
			Path: fmt.Sprintf("file%03d.go", i), Kind: git.KindModified, WorktreeState: 'M',
		})
	}
	tt := ui.NewTester(a.view, 1080, 500)
	if !tt.HasText("file000.go") || tt.HasText("file199.go") {
		t.Fatalf("expected only visible file rows to be rendered")
	}
	a.commit.checkGroup(store.DefaultName, true)
	if !a.commit.checks.byPath["file199.go"].checked {
		t.Fatal("group checkbox missed an offscreen file")
	}
	a.fileList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("file199.go") {
		t.Fatal("last file is not reachable by scrolling")
	}
}

func TestFileRowsReplaceSnapshot(t *testing.T) {
	m := &commitModel{Snap: &vcs.Snapshot{Files: []git.FileChange{{Path: "z"}, {Path: "a"}}}}
	if rows := m.fileRows(); rows[0].change.Path != "a" {
		t.Fatalf("first order = %+v", rows)
	}
	m.Snap = &vcs.Snapshot{Files: []git.FileChange{{Path: "new"}}}
	if rows := m.fileRows(); len(rows) != 1 || rows[0].change.Path != "new" {
		t.Fatalf("rows after replacing snapshot = %+v", rows)
	}
}

func TestReconcileListsSkipsIgnoredAndKeepsAssignments(t *testing.T) {
	m := &commitModel{
		Snap: &vcs.Snapshot{Files: []git.FileChange{
			{Path: "old", Kind: git.KindModified},
			{Path: "new", Kind: git.KindModified},
			{Path: "ignored", Kind: vcs.KindIgnored},
		}},
		lists: &store.Changelists{Default: store.DefaultName, Lists: []*store.Changelist{
			{Name: "active", Active: true},
			{Name: "other", Paths: []string{"old", "gone"}},
		}},
	}
	m.reconcileLists()
	if got := m.lists.ListOf("old"); got != "other" {
		t.Fatalf("old assignment = %q", got)
	}
	if got := m.lists.ListOf("new"); got != "active" {
		t.Fatalf("new assignment = %q", got)
	}
	if got := m.lists.ListOf("ignored"); got != store.DefaultName {
		t.Fatalf("ignored assignment = %q", got)
	}
	if got := m.lists.ListOf("gone"); got != store.DefaultName {
		t.Fatalf("removed assignment = %q", got)
	}
}
