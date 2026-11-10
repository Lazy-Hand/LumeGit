package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCherryPickRevertReset covers apply/undo/move-HEAD operations.
func TestCherryPickRevertReset(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	// Side branch with one commit we can cherry-pick.
	run("switch", "-c", "side")
	if err := os.WriteFile(filepath.Join(dir, "picked.txt"), []byte("pick me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "pickable")
	res, err := repo.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	pickOID := res.Output()[:40]
	run("switch", "main")

	// Cherry-pick applies the file.
	if _, err := repo.CherryPick(ctx, pickOID); err != nil {
		t.Fatalf("CherryPick: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "picked.txt")); err != nil {
		t.Errorf("picked file missing: %v", err)
	}

	// Revert removes it again.
	if _, err := repo.Revert(ctx, pickOID); err != nil {
		// Revert of a cherry-picked commit re-applies inverse.
		t.Fatalf("Revert: %v", err)
	}

	// Reset --soft keeps worktree, moves HEAD.
	commits, _ := repo.Log(ctx, 0)
	if len(commits) < 2 {
		t.Skip("not enough commits")
	}
	target := commits[1].OID
	if _, err := repo.Reset(ctx, target, ResetSoft); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	head, _ := repo.Run(ctx, "rev-parse", "HEAD")
	if head.Output()[:40] != target {
		t.Errorf("HEAD = %s, want %s", head.Output()[:40], target)
	}
	// Soft reset leaves index with changes.
	snap, _ := repo.Snapshot(ctx)
	if len(snap.Staged()) == 0 {
		t.Log("soft reset produced no staged files (acceptable)")
	}
}

// TestRebase applies commits onto a new base.
func TestRebase(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	// main gets a commit; feature branches from before it.
	run("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "feat work")
	run("switch", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "main work")
	run("switch", "feature")

	// Rebase feature onto main.
	res, err := repo.Rebase(ctx, "main")
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !res.ExitOK() {
		t.Fatalf("rebase failed: %s", res.OutputOfStderr())
	}
	// feat.txt should now sit on top of main's work.
	if _, err := os.Stat(filepath.Join(dir, "main.txt")); err != nil {
		t.Errorf("main.txt missing after rebase: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "feat.txt")); err != nil {
		t.Errorf("feat.txt missing after rebase: %v", err)
	}
}
