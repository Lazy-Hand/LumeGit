package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBranchesAndOps covers listing, checkout, rename, delete, merge.
func TestBranchesAndOps(t *testing.T) {
	remote, local := setupRemotePair(t)
	ctx := context.Background()
	repo := NewRepo(local)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = local
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	// Fresh clone: one local branch (main), tracking origin/main.
	local_, remote_, err := repo.Branches(ctx)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(local_) != 1 || local_[0].Name != "main" || !local_[0].Current {
		t.Fatalf("local = %+v", local_)
	}
	if local_[0].Upstream != "origin/main" || !local_[0].HasTrack {
		t.Errorf("upstream = %+v", local_[0])
	}
	foundOrigin := false
	for _, rb := range remote_ {
		if rb.Name == "origin/main" && rb.Remote {
			foundOrigin = true
		}
	}
	if !foundOrigin {
		t.Errorf("origin/main missing from remote list: %+v", remote_)
	}

	// New branch + switch.
	if _, err := repo.Checkout(ctx, "feature", true); err != nil {
		t.Fatalf("Checkout -c: %v", err)
	}
	cur, _ := repo.CurrentBranch(ctx)
	if cur != "feature" {
		t.Errorf("current = %q", cur)
	}

	// Rename.
	if _, err := repo.RenameBranch(ctx, "feature", "feat-renamed"); err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	local2, _, _ := repo.Branches(ctx)
	names := map[string]bool{}
	for _, b := range local2 {
		names[b.Name] = true
	}
	if !names["feat-renamed"] || names["feature"] {
		t.Errorf("after rename = %v", names)
	}

	// Switch back to main, then delete feat-renamed.
	if _, err := repo.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteBranch(ctx, "feat-renamed", false); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}

	// Merge: make a side branch with a commit, merge into main.
	run("switch", "-c", "side")
	if err := os.WriteFile(filepath.Join(local, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "side work")
	run("switch", "main")
	if _, err := repo.MergeIntoCurrent(ctx, "side"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "side.txt")); err != nil {
		t.Errorf("merged file missing: %v", err)
	}
	_ = remote
}
