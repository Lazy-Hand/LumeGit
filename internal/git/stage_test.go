package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupWorkRepo builds a temp repo with one committed file.
func setupWorkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.email", "t@t")
	run("config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "seed")
	return dir
}

// TestStageUnstage exercises index movement against a real repo.
func TestStageUnstage(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	// Modify + new file.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Staged()) != 0 {
		t.Fatalf("staged = %v, want empty", snap.Staged())
	}
	if len(snap.Unstaged()) != 2 {
		t.Fatalf("unstaged = %v, want 2", snap.Unstaged())
	}

	// Stage the modification only.
	if err := repo.Stage(ctx, []string{"a.txt"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	snap, _ = repo.Snapshot(ctx)
	if len(snap.Staged()) != 1 || snap.Staged()[0].Path != "a.txt" {
		t.Errorf("staged = %+v", snap.Staged())
	}

	// Unstage it back.
	if err := repo.Unstage(ctx, []string{"a.txt"}); err != nil {
		t.Fatalf("Unstage: %v", err)
	}
	snap, _ = repo.Snapshot(ctx)
	if len(snap.Staged()) != 0 {
		t.Errorf("staged after unstage = %+v", snap.Staged())
	}
	if len(snap.Unstaged()) != 2 {
		t.Errorf("unstaged after unstage = %+v", snap.Unstaged())
	}
}

// TestCommitStaged verifies index-only commits.
func TestCommitStaged(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	// Empty index → error.
	if _, err := repo.CommitStaged(ctx, "empty", false); err == nil {
		t.Error("CommitStaged with empty index: want error")
	}

	// Stage then commit.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	out, err := repo.CommitStaged(ctx, "staged commit", false)
	if err != nil {
		t.Fatalf("CommitStaged: %v", err)
	}
	if !out.ExitOK {
		t.Fatalf("commit failed: %s", out.HookOut)
	}
	// Index now clean.
	snap, _ := repo.Snapshot(ctx)
	if len(snap.Staged()) != 0 {
		t.Errorf("staged after commit = %+v", snap.Staged())
	}
}

// TestFileDiffUnbornHead verifies untracked-file diffs work on a repo
// with no commits yet (HEAD doesn't resolve).
func TestFileDiffUnbornHead(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewRepo(dir)
	d, err := repo.FileDiff(ctx, FileDiffRequest{Path: "new.txt", From: TreeHead, To: TreeWorktree})
	if err != nil {
		t.Fatalf("FileDiff on unborn HEAD: %v", err)
	}
	if len(d.Old) != 0 {
		t.Errorf("old = %q, want empty", d.Old)
	}
	if string(d.New) != "brand new\n" {
		t.Errorf("new = %q", d.New)
	}
}
