package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupConflictRepo builds a repo mid-merge-conflict on f.txt.
func setupConflictRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		// Merge is expected to fail; don't skip on it.
		_ = cmd.Run()
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.email", "t@t")
	run("config", "user.name", "tester")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f.txt", "line1\nshared\nline3\n")
	run("add", ".")
	run("commit", "-qm", "base")
	run("switch", "-c", "side")
	write("f.txt", "line1\nside-edit\nline3\n")
	run("commit", "-qam", "side")
	run("switch", "main")
	write("f.txt", "line1\nmain-edit\nline3\n")
	run("commit", "-qam", "main")
	run("merge", "side") // conflict expected
	return dir
}

// TestConflictSidesAndResolve covers the full resolve flow.
func TestConflictSidesAndResolve(t *testing.T) {
	dir := setupConflictRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.InProgress == "" {
		t.Fatal("expected in-progress merge")
	}
	conflicts := snap.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Path != "f.txt" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	prog := snap.Progress()
	if prog.Kind != "merge" {
		t.Errorf("progress kind = %q", prog.Kind)
	}
	if prog.CanContinue {
		t.Error("CanContinue with unresolved conflicts")
	}

	// Three sides present.
	sides, err := repo.ConflictSides(ctx, "f.txt")
	if err != nil {
		t.Fatalf("ConflictSides: %v", err)
	}
	if string(sides.Base) != "line1\nshared\nline3\n" {
		t.Errorf("base = %q", sides.Base)
	}
	if string(sides.Ours) != "line1\nmain-edit\nline3\n" {
		t.Errorf("ours = %q", sides.Ours)
	}
	if string(sides.Their) != "line1\nside-edit\nline3\n" {
		t.Errorf("theirs = %q", sides.Their)
	}

	// Resolve with ours.
	if err := repo.ResolveConflict(ctx, "f.txt", ResolveOurs, nil); err != nil {
		t.Fatalf("ResolveConflict ours: %v", err)
	}
	snap2, _ := repo.Snapshot(ctx)
	if len(snap2.Conflicts()) != 0 {
		t.Errorf("conflicts after resolve = %+v", snap2.Conflicts())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(b) != "line1\nmain-edit\nline3\n" {
		t.Errorf("worktree after ours = %q", b)
	}
	if !snap2.Progress().CanContinue {
		t.Error("CanContinue false after all resolved")
	}

	// Continue finishes the merge.
	if _, err := repo.Continue(ctx, "merge"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	snap3, _ := repo.Snapshot(ctx)
	if snap3.InProgress != "" {
		t.Errorf("still in progress: %q", snap3.InProgress)
	}
}

// TestResolveMerge writes a user-merged result.
func TestResolveMerge(t *testing.T) {
	dir := setupConflictRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	merged := []byte("line1\nhand-picked\nline3\n")
	if err := repo.ResolveConflict(ctx, "f.txt", ResolveMerge, merged); err != nil {
		t.Fatalf("ResolveMerge: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(b) != string(merged) {
		t.Errorf("worktree = %q, want merged", b)
	}
	snap, _ := repo.Snapshot(ctx)
	if len(snap.Conflicts()) != 0 {
		t.Error("conflict remains after merge resolve")
	}
}
