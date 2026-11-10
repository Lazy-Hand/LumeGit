package git

import (
	"context"
	"path/filepath"
	"testing"
)

// TestWorktrees runs the list/add/remove cycle.
func TestWorktrees(t *testing.T) {
	repo, dir := setupTempRepo(t)
	ctx := context.Background()

	list, err := repo.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(list) != 1 || !list[0].IsMain {
		t.Fatalf("expected main only: %+v", list)
	}
	if list[0].Path == "" || list[0].OID == "" {
		t.Errorf("main worktree missing fields: %+v", list[0])
	}

	// Add a linked worktree.
	wtPath := filepath.Join(t.TempDir(), "wt-side")
	res, err := repo.AddWorktree(ctx, wtPath, "", "side-wt")
	if err != nil || !res.ExitOK() {
		t.Fatalf("AddWorktree: %v %s", err, res.OutputOfStderr())
	}
	list, err = repo.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("worktrees = %+v", list)
	}
	var linked *Worktree
	for i := range list {
		if !list[i].IsMain {
			linked = &list[i]
		}
	}
	if linked == nil || linked.Branch != "side-wt" {
		t.Errorf("linked = %+v", linked)
	}

	// Remove it.
	res, err = repo.RemoveWorktree(ctx, wtPath, false)
	if err != nil || !res.ExitOK() {
		t.Fatalf("RemoveWorktree: %v %s", err, res.OutputOfStderr())
	}
	list, err = repo.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("after remove = %+v", list)
	}
	_ = dir
}
