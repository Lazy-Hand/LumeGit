package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTempRepo builds a throwaway repo exercising the fixture states:
// staged + unstaged on one file, staged rename, untracked, deletion,
// and an unborn repo case in a subtest. Skipped when git is missing.
func setupTempRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@e",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main", ".")
	write("a.txt", "one\ntwo\n")
	write("b.txt", "hello\n")
	write("gone.txt", "bye\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")

	// a.txt: unstaged only; b.txt: staged + then edited (MM); gone.txt:
	// unstaged delete; c.txt: staged new file; d.txt: untracked.
	write("a.txt", "one\nTWO2\n")
	write("b.txt", "hello\nstaged\n")
	write("d.txt", "d\n")
	write("c.txt", "new\n")
	run("add", "b.txt", "c.txt")
	write("b.txt", "hello\nstaged\nedited\n")
	os.Remove(filepath.Join(dir, "gone.txt"))

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r, dir
}

func TestSnapshotMatchesStatus(t *testing.T) {
	r, _ := setupTempRepo(t)
	s, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s.Unborn || s.HeadName != "main" {
		t.Errorf("head: %+v", s.HeadName)
	}
	if s.InProgress != "" {
		t.Errorf("no operation expected, got %q", s.InProgress)
	}
	paths := map[string]FileChange{}
	for _, f := range s.Files {
		paths[f.Path] = f
	}
	if f, ok := paths["a.txt"]; !ok || f.IndexState != ' ' || f.WorktreeState != 'M' {
		t.Errorf("a.txt = %+v", f)
	}
	if f, ok := paths["b.txt"]; !ok || f.IndexState != 'M' || f.WorktreeState != 'M' ||
		!f.Staged() || !f.Unstaged() {
		t.Errorf("b.txt (MM) = %+v", f)
	}
	if f, ok := paths["gone.txt"]; !ok || f.WorktreeState != 'D' {
		t.Errorf("gone.txt = %+v", f)
	}
	if f, ok := paths["c.txt"]; !ok || f.Kind != KindAdded || f.IndexState != 'A' || !f.Staged() {
		t.Errorf("c.txt = %+v", f)
	}
	if f, ok := paths["d.txt"]; !ok || f.Kind != KindUntracked {
		t.Errorf("d.txt = %+v", f)
	}
}

func TestFileDiffHeadWorktree(t *testing.T) {
	r, _ := setupTempRepo(t)
	ctx := context.Background()
	d, err := r.FileDiff(ctx, FileDiffRequest{Path: "a.txt", From: TreeHead, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff: %v", err)
	}
	if string(d.Old) != "one\ntwo\n" {
		t.Errorf("old = %q", d.Old)
	}
	if string(d.New) != "one\nTWO2\n" {
		t.Errorf("new = %q", d.New)
	}
	if d.OldLabel != "HEAD" || d.NewLabel != "worktree" {
		t.Errorf("labels: %q %q", d.OldLabel, d.NewLabel)
	}
}

func TestFileDiffHeadIndex(t *testing.T) {
	r, _ := setupTempRepo(t)
	ctx := context.Background()
	// b.txt staged content vs HEAD.
	d, err := r.FileDiff(ctx, FileDiffRequest{Path: "b.txt", From: TreeHead, To: TreeIndex})
	if err != nil {
		t.Fatalf("FileDiff: %v", err)
	}
	if string(d.Old) != "hello\n" {
		t.Errorf("old = %q", d.Old)
	}
	if string(d.New) != "hello\nstaged\n" {
		t.Errorf("new = %q (must be the staged text, not the worktree edit)", d.New)
	}
	// Net HEAD→worktree: the third line appears.
	d2, err := r.FileDiff(ctx, FileDiffRequest{Path: "b.txt", From: TreeHead, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff net: %v", err)
	}
	if string(d2.New) != "hello\nstaged\nedited\n" {
		t.Errorf("net new = %q", d2.New)
	}
}

func TestFileDiffMissingSides(t *testing.T) {
	r, _ := setupTempRepo(t)
	ctx := context.Background()
	// deleted file: worktree side absent → empty New, non-empty Old.
	d, err := r.FileDiff(ctx, FileDiffRequest{Path: "gone.txt", From: TreeHead, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff: %v", err)
	}
	if len(d.Old) == 0 || len(d.New) != 0 {
		t.Errorf("gone.txt diff: old=%d new=%d", len(d.Old), len(d.New))
	}
}

func TestFileDiffUntracked(t *testing.T) {
	r, _ := setupTempRepo(t)
	ctx := context.Background()
	d, err := r.FileDiff(ctx, FileDiffRequest{Path: "d.txt", From: TreeIndex, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff: %v", err)
	}
	if len(d.Old) != 0 || string(d.New) != "d\n" {
		t.Errorf("untracked: old=%q new=%q", d.Old, d.New)
	}
}

func TestFileDiffBinary(t *testing.T) {
	r, dir := setupTempRepo(t)
	ctx := context.Background()
	if err := os.WriteFile(dir+"/bin.dat", []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := r.FileDiff(ctx, FileDiffRequest{Path: "bin.dat", From: TreeHead, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff: %v", err)
	}
	if !d.Binary {
		t.Errorf("bin.dat should be reported binary (numstat '-')")
	}
}

func TestSnapshotUnborn(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !s.Unborn || s.HeadOID != "" {
		t.Errorf("unborn snapshot: %+v", s)
	}
}

func TestSnapshotWorktreeRename(t *testing.T) {
	r, _ := setupTempRepo(t)
	ctx := context.Background()
	res, err := r.Run(ctx, "mv", "a.txt", "a2.txt")
	if err != nil || !res.ExitOK() {
		t.Fatalf("git mv a.txt a2.txt: %v", err)
	}
	s, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var rename *FileChange
	for i := range s.Files {
		if s.Files[i].Kind == KindRenamed {
			rename = &s.Files[i]
		}
	}
	if rename == nil || rename.Path != "a2.txt" || rename.OrigPath != "a.txt" {
		t.Errorf("no rename detected: %+v", s.Files)
	}
	// HEAD→worktree diff of the rename: old side must come from a.txt
	// (index no longer holds the original path).
	if rename != nil {
		d, err := r.FileDiffRename(ctx, rename.Path, rename.OrigPath, TreeHead, TreeWorktree)
		if err != nil {
			t.Fatalf("FileDiffRename: %v", err)
		}
		if d.OldPath != "a.txt" || len(d.Old) == 0 {
			t.Errorf("renamed diff: oldpath=%q oldlen=%d", d.OldPath, len(d.Old))
		}
	}

	// Requesting the index side of a rename's original path is a
	// caller error and must fail loudly, not return the wrong blob.
	_, err = r.FileDiffRename(ctx, rename.Path, rename.OrigPath, TreeIndex, TreeWorktree)
	if err == nil {
		t.Errorf("FileDiffRename index side: want error for absent origPath")
	}
}
