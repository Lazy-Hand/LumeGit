package git

import (
	"context"
	"testing"
)

// TestConflictsDetailed verifies the per-file list behind IDEA's
// Resolve Conflicts dialog.
func TestConflictsDetailed(t *testing.T) {
	dir := setupConflictRepo(t)
	repo := NewRepo(dir)
	ctx := context.Background()

	list, err := repo.ConflictsDetailed(ctx)
	if err != nil {
		t.Fatalf("ConflictsDetailed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("entries = %+v, want 1", list)
	}
	e := list[0]
	if e.Path != "f.txt" {
		t.Errorf("path = %q", e.Path)
	}
	if !e.HasBase || !e.HasOurs || !e.HasTheir {
		t.Errorf("stages = b:%v o:%v t:%v", e.HasBase, e.HasOurs, e.HasTheir)
	}
	if e.Kind != "修改/修改" {
		t.Errorf("kind = %q", e.Kind)
	}
	if e.Hunks == 0 {
		t.Error("Hunks = 0")
	}
}

// TestResolveAllOurs accepts "ours" for every conflict in one shot.
func TestResolveAllOurs(t *testing.T) {
	dir := setupConflictRepo(t)
	repo := NewRepo(dir)
	ctx := context.Background()

	if err := repo.ResolveAllOurs(ctx); err != nil {
		t.Fatalf("ResolveAllOurs: %v", err)
	}
	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Conflicts()) != 0 {
		t.Fatalf("still conflicted: %+v", snap.Conflicts())
	}
}
