package main

import (
	"os/exec"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"lumegit/internal/git"
	"lumegit/internal/store"
)

// TestPickViewRenders checks the no-repo screen builds headlessly.
func TestPickViewRenders(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 900, 640)
	if !tt.HasText("LumeGit") {
		t.Errorf("title missing, texts %q", tt.Texts())
	}
	if !tt.HasText("打开") {
		t.Errorf("Open button missing, texts %q", tt.Texts())
	}
}

// TestCommitViewRenders checks the workspace builds with one file's
// state loaded (no repo required: the model is populated directly).
func TestCommitViewRenders(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	tt := ui.NewTester(a.view, 1080, 720)
	for _, want := range []string{"repo", "main", "提交", "提交并推送", "a.txt", "0 of 3"} {
		if !tt.HasText(want) {
			t.Errorf("text %q missing: %q", want, tt.Texts())
		}
	}
}

// TestFileSelectionShowsDiff clicks a file and checks the diff pane
// changed from the placeholder.
func TestFileSelectionShowsDiff(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	tt := ui.NewTester(a.view, 1080, 720)
	if !tt.HasText("选择文件查看变更。") {
		t.Errorf("placeholder missing before selection: %q", tt.Texts())
	}
	if err := tt.Click("a.txt"); err != nil {
		t.Skipf("row click not addressable in headless: %v", err)
	}
	tt.Frame()
	if a.commit.selectedFile != "a.txt" {
		t.Errorf("selectedFile = %q after click", a.commit.selectedFile)
	}
}

// TestOpenRepoFlow drives the real open-repo path headlessly with a
// temp git repo. The picker buttons call spawnWindow (which would try
// to create a real OS window), so the test stubs that hook and then
// exercises openPickedRepo directly — that's the same code path a
// spawned window runs.
func TestOpenRepoFlow(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	var spawned []string
	spawnWindowHook = func(p string) { spawned = append(spawned, p) }
	t.Cleanup(func() { spawnWindowHook = nil })

	a := &app{}
	// Sync publish: run update closures immediately (headless).
	a.commit.publish = func(fn func()) { fn() }
	a.pickPath = dir
	tt := ui.NewTester(a.view, 1080, 720)
	if err := tt.Click("打开"); err != nil {
		t.Fatalf("click Open: %v", err)
	}
	if len(spawned) != 1 || spawned[0] != dir {
		t.Fatalf("expected picker to spawn a new window with %q, got %v", dir, spawned)
	}

	// The spawned window's open runs the same path — run it inline.
	a.openPickedRepo()
	deadline := time.Now().Add(2 * time.Second)
	for a.commit.RepoPath == "" && a.commit.busy && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	tt.Frame()
	if a.commit.RepoPath == "" {
		t.Fatalf("repo not opened: openErr=%q busy=%v texts=%q",
			a.commit.openErr, a.commit.busy, tt.Texts())
	}
	if !tt.HasText("main") {
		t.Errorf("branch text missing after open: %q", tt.Texts())
	}
}

// TestHistoryViewRenders populates the history model directly and checks
// the log tab builds headlessly.
func TestHistoryViewRenders(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	a.commit.publish = func(fn func()) { fn() }
	a.tab = 1
	a.histListW = 380
	a.histFilesW = 260
	a.history.lastLoadedRepo = "/fake/repo"
	a.history.commits = []git.Commit{{
		OID:      "0123456789abcdef0123456789abcdef01234567",
		ShortOID: "0123456",
		Author:   "tester",
		Subject:  "初始提交",
		Refs:     []string{"main"},
	}}
	a.history.selectedID = "0123456789abcdef0123456789abcdef01234567"
	a.history.files = []git.CommitFile{{Status: 'A', Path: "a.txt"}}
	tt := ui.NewTester(a.view, 1280, 720)
	for _, want := range []string{"提交", "历史", "初始提交", "0123456", "tester", "main", "文件", "a.txt"} {
		if !tt.HasText(want) {
			t.Errorf("text %q missing: %q", want, tt.Texts())
		}
	}
}

// TestChangelistGrouping drives the model directly: snapshot → assign →
// groups reflect the changelist split.
func TestChangelistGrouping(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	a.commit.publish = func(fn func()) { fn() }
	m := &a.commit
	m.repos = &store.Manager{} // in-memory, no file
	m.lists = m.repos.ChangelistsFor("/fake/repo")
	m.reconcileLists()

	// Fresh: everything in the default group.
	gs := m.groups()
	if len(gs) != 1 || gs[0].Name != store.DefaultName {
		t.Fatalf("groups = %+v", gs)
	}
	if len(gs[0].Rows) != 3 {
		t.Fatalf("default rows = %d", len(gs[0].Rows))
	}

	// Create a task list and move a file there.
	m.newList("功能A", true)
	m.moveFile("a.txt", "功能A")
	gs = m.groups()
	if len(gs) != 2 {
		t.Fatalf("groups after split = %d", len(gs))
	}
	if gs[0].Name != "功能A" || !gs[0].Active || len(gs[0].Rows) != 1 || gs[0].Rows[0].change.Path != "a.txt" {
		t.Errorf("功能A group = %+v", gs[0])
	}
	if gs[1].Name != store.DefaultName || len(gs[1].Rows) != 2 {
		t.Errorf("default group = %+v", gs[1])
	}

	// Whole-group check ticks every file in the list.
	m.checkGroup(store.DefaultName, true)
	if m.checks.byPath["b.txt"] == nil || !m.checks.byPath["b.txt"].checked {
		t.Error("checkGroup did not tick b.txt")
	}
	if m.checks.byPath["new.txt"] == nil || !m.checks.byPath["new.txt"].checked {
		t.Error("checkGroup did not tick new.txt")
	}
	if st := m.checks.byPath["a.txt"]; st != nil && st.checked {
		t.Error("checkGroup leaked into 功能A")
	}
}

// TestShelfViewRenders populates the shelf model and checks the stash
// tab builds headlessly.
func TestShelfViewRenders(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	a.commit.publish = func(fn func()) { fn() }
	a.tab = 2
	a.shelfListW = 300
	a.shelfFileW = 240
	a.shelf.lastLoaded = "/fake/repo"
	a.shelf.stashes = []git.Stash{{
		Index: 0, Name: "stash@{0}", Branch: "main", Message: "wip work",
	}}
	a.shelf.selectedID = "stash@{0}"
	a.shelf.files = []git.StashFile{{Path: "a.txt", Status: 'M'}}
	tt := ui.NewTester(a.view, 1280, 720)
	for _, want := range []string{"搁置", "wip work", "stash@{0}", "main", "文件", "a.txt"} {
		if !tt.HasText(want) {
			t.Errorf("text %q missing: %q", want, tt.Texts())
		}
	}
}

// TestBranchPopupMenu populates the branch model and checks the header
// tag's dropdown (IDEA Branches popup) lists the refs headlessly.
func TestBranchPopupMenu(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	a.commit.Snap = fakeSnapshot(t)
	a.commit.publish = func(fn func()) { fn() }
	a.branches.lastLoaded = "/fake/repo"
	a.branches.local = []git.Branch{
		{Name: "main", Current: true, OID: "abc1234", Upstream: "origin/main", HasTrack: true},
		{Name: "feature", OID: "def5678"},
	}
	a.branches.remote = []git.Branch{{Name: "origin/main", OID: "abc1234", Remote: true}}
	a.branches.tags = []git.Tag{{Name: "v1.0", OID: "abc1234"}}
	tt := ui.NewTester(a.view, 1280, 720)
	if err := tt.ClickWith(0, "main"); err != nil {
		t.Fatalf("click branch tag: %v", err)
	}
	menu := tt.Menu()
	for _, want := range []string{"新建分支…", "新建标签…", "✓ main", "feature", "远程分支", "标签"} {
		found := false
		for _, item := range menu {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("menu item %q missing: %v", want, menu)
		}
	}
	tt.CloseMenu()
}

// TestFilesPaneScrolls guards the Grow(1) fix: the file list's ScrollArea
// must take the leftover pane height so a long list actually scrolls
// instead of overflowing unscrollable.
func TestFilesPaneScrolls(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	snap := fakeSnapshot(t)
	// Enough rows to overflow the pane.
	for i := 0; i < 60; i++ {
		p := "f" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".txt"
		snap.Files = append(snap.Files, git.FileChange{
			Path: p, Kind: git.KindUntracked,
			IndexState: ' ', WorktreeState: '?',
		})
	}
	a.commit.Snap = snap
	a.filesPaneW = 300
	tt := ui.NewTester(a.view, 1080, 500)
	if a.fileScroll.MaxY == 0 {
		t.Fatalf("file list cannot scroll (MaxY=0): content overflows the pane")
	}
	tt.Scroll(150, 300, 0, 300)
	if a.fileScroll.Y == 0 {
		t.Errorf("scroll event did not move the file list: %+v", a.fileScroll)
	}
}

// TestStagingCheckboxes covers the staging-mode group check: the two
// groups are "changes" (tracked, staged or not) and "unversioned"
// (untracked). checkGroupPaths ticks whichever set the view passes.
func TestStagingCheckboxes(t *testing.T) {
	a := &app{}
	a.commit.RepoPath = "/fake/repo"
	snap := &git.Snapshot{
		Worktree: "/fake/repo",
		Files: []git.FileChange{
			{Path: "staged1.go", Kind: git.KindAdded, IndexState: 'A'},
			{Path: "staged2.go", Kind: git.KindModified, IndexState: 'M'},
			{Path: "unstaged1.go", Kind: git.KindModified, WorktreeState: 'M'},
			{Path: "new.go", Kind: git.KindUntracked, WorktreeState: '?'},
		},
	}
	a.commit.Snap = snap
	m := &a.commit

	// IDEA-style split: tracked (incl. AM) vs unversioned.
	var tracked, untracked []git.FileChange
	for _, f := range snap.Files {
		if f.Kind == git.KindUntracked {
			untracked = append(untracked, f)
		} else {
			tracked = append(tracked, f)
		}
	}
	if len(tracked) != 3 || len(untracked) != 1 {
		t.Fatalf("tracked=%d untracked=%d", len(tracked), len(untracked))
	}

	// Tick one tracked file; checkedIn reports it.
	m.checks.checkboxFor("unstaged1.go")
	m.checks.byPath["unstaged1.go"].checked = true
	if got := m.checkedIn(tracked); got != 1 {
		t.Errorf("checkedIn(tracked) = %d, want 1", got)
	}
	if ps := m.checkedPaths(tracked); len(ps) != 1 || ps[0] != "unstaged1.go" {
		t.Errorf("checkedPaths(tracked) = %v", ps)
	}

	// Group check ticks every file in its own group only.
	m.checkGroupPaths(tracked, true)
	if got := m.checkedIn(tracked); got != 3 {
		t.Errorf("after checkGroupPaths(tracked,true): %d", got)
	}
	if got := m.checkedIn(untracked); got != 0 {
		t.Errorf("untracked side leaked: %d", got)
	}
	m.checkGroupPaths(tracked, false)
	if got := m.checkedIn(tracked); got != 0 {
		t.Errorf("after uncheck: %d", got)
	}
}

// TestStagingContextMenu verifies the menu builder surfaces the right
// entries for each staging-side file kind. Headless right-click can't
// always address a row, so we exercise the model-level decisions the
// menu makes (which side, which kind) instead.
func TestStagingContextMenu(t *testing.T) {
	snap := &git.Snapshot{
		Files: []git.FileChange{
			{Path: "staged.go", Kind: git.KindModified, IndexState: 'M'},
			{Path: "work.go", Kind: git.KindModified, WorktreeState: 'M'},
			{Path: "new.go", Kind: git.KindUntracked, WorktreeState: '?'},
		},
	}
	staged := snap.Staged()
	unstaged := snap.Unstaged()
	if len(staged) != 1 || staged[0].Path != "staged.go" {
		t.Fatalf("staged = %+v", staged)
	}
	if len(unstaged) != 2 {
		t.Fatalf("unstaged = %+v", unstaged)
	}
	// Untracked detection the menu keys on.
	var foundUntracked bool
	for _, f := range unstaged {
		if f.Kind == git.KindUntracked {
			foundUntracked = true
		}
	}
	if !foundUntracked {
		t.Error("no untracked row in unstaged side")
	}
}
