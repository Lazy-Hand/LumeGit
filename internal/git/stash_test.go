package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStashLifecycle: push → list → files → apply → drop.
func TestStashLifecycle(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	// Dirty the worktree: modify a.txt, add untracked b.txt.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nstashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Push with -u.
	res, err := repo.StashPush(ctx, StashPushRequest{Message: "wip work", IncludeUntracked: true})
	if err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	if !res.ExitOK() {
		t.Fatalf("push failed: %s", res.OutputOfStderr())
	}
	// Worktree clean now.
	snap, _ := repo.Snapshot(ctx)
	if len(snap.Files) != 0 {
		t.Fatalf("worktree not clean after stash: %+v", snap.Files)
	}

	// List.
	stashes, err := repo.Stashes(ctx)
	if err != nil {
		t.Fatalf("Stashes: %v", err)
	}
	if len(stashes) != 1 {
		t.Fatalf("stashes = %v", stashes)
	}
	st := stashes[0]
	if st.Name != "stash@{0}" {
		t.Errorf("name = %q", st.Name)
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q", st.Branch)
	}
	if st.Message != "wip work" {
		t.Errorf("message = %q", st.Message)
	}

	// Files.
	files, err := repo.StashFiles(ctx, st.Name)
	if err != nil {
		t.Fatalf("StashFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	var aFile, bFile *StashFile
	for i := range files {
		switch files[i].Path {
		case "a.txt":
			aFile = &files[i]
		case "b.txt":
			bFile = &files[i]
		}
	}
	if aFile == nil || aFile.Status != 'M' || aFile.Untracked {
		t.Errorf("a.txt file = %+v", aFile)
	}
	if bFile == nil || !bFile.Untracked {
		t.Errorf("b.txt file = %+v", bFile)
	}

	// Diff of the tracked file.
	d, err := repo.StashFileDiff(ctx, st.Name, *aFile)
	if err != nil {
		t.Fatalf("StashFileDiff: %v", err)
	}
	if string(d.Old) != "one\n" {
		t.Errorf("old = %q", d.Old)
	}
	if string(d.New) != "one\nstashed\n" {
		t.Errorf("new = %q", d.New)
	}
	// Diff of the untracked file (old side empty).
	d2, err := repo.StashFileDiff(ctx, st.Name, *bFile)
	if err != nil {
		t.Fatalf("untracked diff: %v", err)
	}
	if len(d2.Old) != 0 {
		t.Errorf("untracked old = %q", d2.Old)
	}
	if string(d2.New) != "untracked\n" {
		t.Errorf("untracked new = %q", d2.New)
	}

	// Apply restores both files.
	if _, err := repo.StashApply(ctx, st.Name); err != nil {
		t.Fatalf("StashApply: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\nstashed\n" {
		t.Errorf("a.txt after apply = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Errorf("b.txt not restored: %v", err)
	}
	// Stash entry still there after apply.
	stashes2, _ := repo.Stashes(ctx)
	if len(stashes2) != 1 {
		t.Errorf("stash count after apply = %d", len(stashes2))
	}

	// Drop removes it.
	if _, err := repo.StashDrop(ctx, st.Name); err != nil {
		t.Fatalf("StashDrop: %v", err)
	}
	stashes3, _ := repo.Stashes(ctx)
	if len(stashes3) != 0 {
		t.Errorf("stash count after drop = %d", len(stashes3))
	}
}

// TestStashClearAndShow verifies the advanced ops.
func TestStashClearAndShow(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.StashPush(ctx, StashPushRequest{Message: "to clear"}); err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nagain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.StashPush(ctx, StashPushRequest{Message: "also clear"}); err != nil {
		t.Fatalf("StashPush: %v", err)
	}
	sts, _ := repo.Stashes(ctx)
	if len(sts) != 2 {
		t.Fatalf("stashes = %d", len(sts))
	}

	// StashShow returns the patch text.
	text, err := repo.StashShow(ctx, "stash@{0}")
	if err != nil {
		t.Fatalf("StashShow: %v", err)
	}
	if !strings.Contains(text, "again") {
		t.Errorf("StashShow missing new content: %q", text)
	}

	// StashClear drops everything.
	if _, err := repo.StashClear(ctx); err != nil {
		t.Fatalf("StashClear: %v", err)
	}
	sts2, _ := repo.Stashes(ctx)
	if len(sts2) != 0 {
		t.Errorf("stash count after clear = %d", len(sts2))
	}
}

// TestStashKeepIndex stashes only unstaged changes (--keep-index).
func TestStashKeepIndex(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	// Stage one change, leave another unstaged.
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, []string{"staged.txt"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nunstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --keep-index: only unstaged a.txt changes go in; staged.txt stays.
	if _, err := repo.StashPush(ctx, StashPushRequest{KeepIndex: true}); err != nil {
		t.Fatalf("StashPush --keep-index: %v", err)
	}
	snap, _ := repo.Snapshot(ctx)
	staged := false
	for _, f := range snap.Files {
		if f.Path == "staged.txt" && f.Staged() {
			staged = true
		}
	}
	if !staged {
		t.Error("staged.txt not kept after --keep-index stash")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) == "one\nunstaged\n" {
		t.Error("unstaged change was not stashed")
	}
}
