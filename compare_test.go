package main

// TestCompareSelectFile exercises the compare overlay end to end:
// openFor lists diff files from a real repo, selectFile loads a diff.
// Guards the two-dot CompareFiles semantics — a three-dot list used
// to name files whose ref/HEAD blobs were identical (empty-looking
// diff).

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"lumegit/internal/git"
	"lumegit/internal/vcs"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCompareSelectFile(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "a@b.c")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init")
	gitT(t, dir, "checkout", "-qb", "b1")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0644)
	gitT(t, dir, "commit", "-qam", "b")
	gitT(t, dir, "checkout", "-q", "master")

	a := &app{}
	a.commit.RepoPath = dir
	pub := &testPublisher{}
	sync := updater{update: pub.publish}
	waitCmp := func() {
		deadline := time.Now().Add(3 * time.Second)
		for (a.compare.loading || a.compare.diffLoad) && time.Now().Before(deadline) {
			pub.drain()
			time.Sleep(20 * time.Millisecond)
		}
		pub.drain()
	}

	a.compare.openFor(dir, "b1", sync)
	waitCmp()
	t.Logf("loading=%v err=%q files=%d cmp=%+v", a.compare.loading, a.compare.err, len(a.compare.files), a.compare.cmp)
	if a.compare.err != "" {
		t.Fatalf("load err: %s", a.compare.err)
	}
	if len(a.compare.files) == 0 {
		t.Fatal("no files listed")
	}

	a.compare.selectFile(dir, a.compare.files[0].Path, sync)
	waitCmp()
	t.Logf("diff=%v err=%q", a.compare.diff != nil, a.compare.diffErr)
	if a.compare.diffErr != "" {
		t.Fatalf("diff err: %s", a.compare.diffErr)
	}
	if a.compare.diff == nil {
		t.Fatal("diff stayed nil")
	}
	t.Logf("old=%q new=%q", a.compare.diff.Old, a.compare.diff.New)
}

// TestCompareBehindBranch covers the "当前分支 独有 N, ref 独有 0"
// case the screenshot hit: comparing an ancestor ref against HEAD used
// to show an empty file list because merge-base→ref is a no-op. The
// panel should surface the divergence instead — base→HEAD here.
func TestCompareBehindBranch(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "a@b.c")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init")
	gitT(t, dir, "checkout", "-qb", "release") // release stays at init
	gitT(t, dir, "checkout", "-q", "master")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0644)
	gitT(t, dir, "commit", "-qam", "ahead")

	a := &app{}
	a.commit.RepoPath = dir
	pub := &testPublisher{}
	sync := updater{update: pub.publish}
	waitCmp := func() {
		deadline := time.Now().Add(3 * time.Second)
		for (a.compare.loading || a.compare.diffLoad) && time.Now().Before(deadline) {
			pub.drain()
			time.Sleep(20 * time.Millisecond)
		}
		pub.drain()
	}

	a.compare.openFor(dir, "release", sync)
	waitCmp()
	if a.compare.err != "" {
		t.Fatalf("load err: %s", a.compare.err)
	}
	// release has nothing unique; HEAD is 1 ahead. The file list must
	// still show the divergence (base→HEAD), not read "没有差异文件".
	if len(a.compare.files) == 0 {
		t.Fatalf("behind-branch compare listed no files; cmp=%+v", a.compare.cmp)
	}
	if a.compare.cmp.FilesTo != "HEAD" {
		t.Fatalf("FilesTo = %q, want HEAD", a.compare.cmp.FilesTo)
	}

	// Clicking a file diffs base→HEAD — the change HEAD has.
	a.compare.selectFile(dir, "f.txt", sync)
	waitCmp()
	if a.compare.diffErr != "" {
		t.Fatalf("diff err: %s", a.compare.diffErr)
	}
	if a.compare.diff == nil || string(a.compare.diff.New) != "a\nb\n" {
		t.Fatalf("diff = %+v", a.compare.diff)
	}
}

// TestCompareWorktreeSelectFile exercises openForWorktree: the file
// list comes from `git diff ref` and selectFile reads the worktree
// side from disk — including a path absent in the ref (old side
// empty), which used to surface a cat-file error as "no reaction".
func TestCompareWorktreeSelectFile(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "a@b.c")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init")
	// Worktree edit + an untracked file not in HEAD.
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\n"), 0644)

	a := &app{}
	a.commit.RepoPath = dir
	pub := &testPublisher{}
	sync := updater{update: pub.publish}
	waitCmp := func() {
		deadline := time.Now().Add(3 * time.Second)
		for (a.compare.loading || a.compare.diffLoad) && time.Now().Before(deadline) {
			pub.drain()
			time.Sleep(20 * time.Millisecond)
		}
		pub.drain()
	}

	// Whole-repo mode lists the tracked worktree diff.
	a.compare.openForWorktree(dir, "HEAD", "", sync)
	waitCmp()
	if a.compare.err != "" {
		t.Fatalf("load err: %s", a.compare.err)
	}
	if len(a.compare.files) != 1 || a.compare.files[0].Path != "f.txt" {
		t.Fatalf("files = %+v, want [f.txt]", a.compare.files)
	}

	// A file in the worktree diff: old=HEAD blob, new=disk.
	a.compare.selectFile(dir, "f.txt", sync)
	waitCmp()
	if a.compare.diffErr != "" {
		t.Fatalf("diff err: %s", a.compare.diffErr)
	}
	if string(a.compare.diff.Old) != "a\n" || string(a.compare.diff.New) != "a\nb\n" {
		t.Fatalf("diff = %q → %q", a.compare.diff.Old, a.compare.diff.New)
	}

	// A path absent in HEAD (untracked): empty old side, not an error.
	a.compare.selectFile(dir, "new.txt", sync)
	waitCmp()
	if a.compare.diffErr != "" {
		t.Fatalf("untracked diff err: %s", a.compare.diffErr)
	}
	if a.compare.diff == nil || string(a.compare.diff.New) != "n\n" || len(a.compare.diff.Old) != 0 {
		t.Fatalf("untracked diff = %+v", a.compare.diff)
	}

	// Single-file mode opens straight onto the diff.
	a.compare.openForWorktree(dir, "HEAD", "f.txt", sync)
	waitCmp()
	if a.compare.selected != "f.txt" || a.compare.diff == nil {
		t.Fatalf("single-file open: selected=%q diff=%v", a.compare.selected, a.compare.diff)
	}
}

// TestCompareOverlayClick drives the real UI path: open the compare
// modal on a repo with a ref↔HEAD diff, CLICK a file row, and assert
// the diff pane leaves the placeholder — guards the whole
// row→selectFile→cmpDiffView chain, not just the model.
func TestCompareOverlayClick(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "a@b.c")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init")
	gitT(t, dir, "checkout", "-qb", "b1")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0644)
	gitT(t, dir, "commit", "-qam", "b")
	gitT(t, dir, "checkout", "-q", "master")

	a := &app{}
	a.commit.RepoPath = dir
	pub := &testPublisher{}
	a.commit.publish = pub.publish
	a.commit.Snap = vcs.WrapGit(&git.Snapshot{Worktree: dir, HeadName: "master"})

	a.compare.openFor(dir, "b1", a.updater())
	deadline := time.Now().Add(3 * time.Second)
	for a.compare.loading && time.Now().Before(deadline) {
		pub.drain()
		time.Sleep(20 * time.Millisecond)
	}
	pub.drain()

	tt := ui.NewTester(a.view, 1100, 700)
	tt.Frame()
	if !tt.HasText("f.txt") {
		t.Fatalf("file row not rendered: %q", tt.Texts())
	}
	if err := tt.Click("f.txt"); err != nil {
		t.Fatalf("row click: %v", err)
	}
	// selectFile is async — drain publishes until it lands.
	deadline = time.Now().Add(3 * time.Second)
	for a.compare.diffLoad && time.Now().Before(deadline) {
		pub.drain()
		time.Sleep(20 * time.Millisecond)
	}
	pub.drain()
	tt.Frame()
	tt.Frame() // DiffView's first build may Invalidate for a second pass
	if a.compare.diffErr != "" {
		t.Fatalf("diffErr: %s", a.compare.diffErr)
	}
	if a.compare.diff == nil {
		t.Fatalf("diff nil after click; texts %q", tt.Texts())
	}
	// The diff pane must have real geometry, not a zero-size collapse.
	r, ok := tt.Find("New Text")
	if !ok {
		// Unified diff labels its editor "New Text"; fall back to
		// checking the placeholder is gone at least.
		if tt.HasText("选择文件查看差异。") {
			t.Fatalf("placeholder still showing; texts %q", tt.Texts())
		}
		return
	}
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("diff pane has no size: %v", r)
	}

	// Double-click: the second click on the shown file must not blank
	// the pane back into a reload.
	if err := tt.Click("f.txt"); err != nil {
		t.Fatalf("re-click: %v", err)
	}
	tt.Frame()
	if a.compare.diffLoad || a.compare.diff == nil {
		t.Fatalf("re-click bounced the diff: load=%v diff=%v", a.compare.diffLoad, a.compare.diff)
	}
}
