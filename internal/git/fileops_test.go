package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRevertPaths discards a worktree edit.
func TestRevertPaths(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevertPaths(ctx, []string{"a.txt"}); err != nil {
		t.Fatalf("RevertPaths: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "one\n" {
		t.Errorf("a.txt = %q after revert", b)
	}
}

// TestRevertStagedPaths restores index+worktree to HEAD.
func TestRevertStagedPaths(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("staged change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevertStagedPaths(ctx, []string{"a.txt"}); err != nil {
		t.Fatalf("RevertStagedPaths: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "one\n" {
		t.Errorf("a.txt = %q after staged revert", b)
	}
	snap, _ := repo.Snapshot(ctx)
	if len(snap.Staged()) != 0 {
		t.Errorf("staged still holds entries: %+v", snap.Staged())
	}
}

// TestDeletePath removes an untracked file.
func TestDeletePath(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	p := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeletePath(ctx, "new.txt"); err != nil {
		t.Fatalf("DeletePath: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("new.txt still exists")
	}
}

// TestAddToGitignore appends an anchored pattern.
func TestAddToGitignore(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	if err := repo.AddToGitignore(ctx, "build/out.bin"); err != nil {
		t.Fatalf("AddToGitignore: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != "/build/out.bin\n" {
		t.Errorf(".gitignore = %q", b)
	}
	// Appends, doesn't clobber.
	if err := repo.AddToGitignore(ctx, "tmp/"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != "/build/out.bin\n/tmp/\n" {
		t.Errorf("after second add: %q", b)
	}
}

// TestStashPushPaths stashes only the listed paths.
func TestStashPushPaths(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("stashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := repo.StashPushPaths(ctx, "only-a", []string{"a.txt"}, false)
	if err != nil {
		t.Fatalf("StashPushPaths: %v", err)
	}
	if !res.ExitOK() {
		t.Fatalf("stash failed: %s", res.OutputOfStderr())
	}
	// a.txt reverted; b.txt keeps the worktree edit.
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "one\n" {
		t.Errorf("a.txt = %q, want reverted", b)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(b) != "keep\n" {
		t.Errorf("b.txt = %q, want worktree edit kept", b)
	}
}
