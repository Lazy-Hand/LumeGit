package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestShelfRoundTrip: shelve removes worktree change, unshelve restores.
func TestShelfRoundTrip(t *testing.T) {
	dir := setupWorkRepo(t)
	repo := NewRepo(dir)
	ctx := context.Background()

	writeTestFile(t, dir, "a.txt", "one\nCHANGED\n")
	if err := repo.ShelvePaths(ctx, "wip-a", []string{"a.txt"}); err != nil {
		t.Fatalf("ShelvePaths: %v", err)
	}
	// Worktree reverted.
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) == "one\nCHANGED\n" {
		t.Error("worktree not reverted")
	}
	// Shelf has the entry.
	entries, err := repo.Shelf(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Shelf = %+v err=%v", entries, err)
	}
	if entries[0].Name != "wip-a" {
		t.Errorf("entry = %+v", entries[0])
	}
	files, _ := repo.ShelfPatchFiles(ctx, "wip-a")
	if len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("patch files = %v", files)
	}
	// Unshelve.
	res, err := repo.Unshelve(ctx, "wip-a")
	if err != nil || !res.ExitOK() {
		t.Fatalf("Unshelve: %v %v", err, res)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "one\nCHANGED\n" {
		t.Errorf("after unshelve = %q", data)
	}
	// Drop.
	if err := repo.DropShelf(ctx, "wip-a"); err != nil {
		t.Fatalf("DropShelf: %v", err)
	}
	entries, _ = repo.Shelf(ctx)
	if len(entries) != 0 {
		t.Errorf("after drop = %+v", entries)
	}
}
