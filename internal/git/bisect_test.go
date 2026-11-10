package git

import (
	"context"
	"testing"
)

// TestBisectLifecycle runs start → mark → status → reset.
func TestBisectLifecycle(t *testing.T) {
	repo, _ := setupTempRepo(t)
	ctx := context.Background()

	// Status when inactive.
	st, err := repo.BisectStatus(ctx)
	if err != nil {
		t.Fatalf("BisectStatus: %v", err)
	}
	if st.Active {
		t.Error("inactive repo reported active")
	}

	// Start between the oldest commit (good) and HEAD (bad).
	// For a single-commit repo this is degenerate; still exercises
	// the plumbing.
	list, err := repo.Log(ctx, 100)
	if err != nil || len(list) == 0 {
		t.Fatalf("Log: %v", err)
	}
	good := list[len(list)-1].OID
	bad := list[0].OID
	if good == bad && len(list) > 1 {
		good = list[1].OID
	}
	if good == bad {
		// Need ≥2 commits — extend with another.
		commitFile(t, repo.Worktree, "bisect-step.txt", "step\n", "bisect step")
		list, _ = repo.Log(ctx, 100)
		good = list[len(list)-1].OID
		bad = list[0].OID
	}

	res, err := repo.BisectStart(ctx, good, bad)
	if err != nil || !res.ExitOK() {
		t.Fatalf("BisectStart: %v %s", err, res.OutputOfStderr())
	}

	st, err = repo.BisectStatus(ctx)
	if err != nil {
		t.Fatalf("BisectStatus: %v", err)
	}
	if !st.Active {
		t.Error("bisect should be active after start")
	}

	// Mark current — may or may not finish; non-zero exit is tolerated
	// (already-determined bisects fail the next mark).
	res, err = repo.BisectMark(ctx, true)
	if err != nil {
		t.Fatalf("BisectMark: %v", err)
	}
	_ = res

	// Reset restores HEAD.
	res, err = repo.BisectReset(ctx)
	if err != nil || !res.ExitOK() {
		t.Fatalf("BisectReset: %v %s", err, res.OutputOfStderr())
	}
	st, _ = repo.BisectStatus(ctx)
	if st.Active && !st.Done {
		t.Error("still active after reset")
	}
}
