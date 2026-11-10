package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitLog returns the subject lines of HEAD's history, newest first.
func gitLog(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	writeTestFile(t, dir, name, content)
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-qm", msg)
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestRebasePlan lists commits onto..HEAD oldest-first.
func TestRebasePlan(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	commitFile(t, dir, "b.txt", "b\n", "second")
	commitFile(t, dir, "c.txt", "c\n", "third")

	plan, err := repo.RebasePlan(ctx, "HEAD~2")
	if err != nil {
		t.Fatalf("RebasePlan: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan[0].Subject != "second" || plan[1].Subject != "third" {
		t.Errorf("subjects = %q %q", plan[0].Subject, plan[1].Subject)
	}
	for _, e := range plan {
		if e.Action != RebasePick {
			t.Errorf("default action = %q", e.Action)
		}
	}
}

// TestInteractiveRebaseRewordDrop exercises the scripted editor path:
// reword one commit, drop another.
func TestInteractiveRebaseRewordDrop(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	commitFile(t, dir, "b.txt", "b\n", "second")
	commitFile(t, dir, "c.txt", "c\n", "third")

	plan, err := repo.RebasePlan(ctx, "HEAD~2")
	if err != nil {
		t.Fatalf("RebasePlan: %v", err)
	}
	plan[0].Action = RebaseReword
	plan[0].Message = "renamed second"
	plan[1].Action = RebaseDrop

	res, err := repo.InteractiveRebase(ctx, "HEAD~2", plan)
	if err != nil {
		t.Fatalf("InteractiveRebase: %v", err)
	}
	if !res.ExitOK() {
		t.Fatalf("rebase failed: %s", res.FullLog())
	}
	log := gitLog(t, dir)
	// History now: seed + renamed second (third was dropped).
	if len(log) != 2 || log[0] != "renamed second" || log[1] != "seed" {
		t.Fatalf("log = %v", log)
	}
	// c.txt is gone from the tree AND the worktree.
	if fileExists(filepath.Join(dir, "c.txt")) {
		t.Error("c.txt should be dropped")
	}
}

// TestInteractiveRebaseSquash squashes two commits into one.
func TestInteractiveRebaseSquash(t *testing.T) {
	dir := setupWorkRepo(t)
	ctx := context.Background()
	repo := NewRepo(dir)

	commitFile(t, dir, "b.txt", "b\n", "second")
	commitFile(t, dir, "c.txt", "c\n", "third")

	plan, err := repo.RebasePlan(ctx, "HEAD~2")
	if err != nil {
		t.Fatalf("RebasePlan: %v", err)
	}
	plan[1].Action = RebaseSquash
	plan[1].Message = "combined second+third"

	res, err := repo.InteractiveRebase(ctx, "HEAD~2", plan)
	if err != nil {
		t.Fatalf("InteractiveRebase: %v", err)
	}
	if !res.ExitOK() {
		t.Fatalf("squash failed: %s", res.FullLog())
	}
	log := gitLog(t, dir)
	if len(log) != 2 || log[0] != "combined second+third" {
		t.Fatalf("log = %v", log)
	}
	// Both files survive in the squashed commit's tree.
	if !fileExists(filepath.Join(dir, "b.txt")) || !fileExists(filepath.Join(dir, "c.txt")) {
		t.Error("squashed commit lost files")
	}
}
