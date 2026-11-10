package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCompareBranches runs the two-sided compare on a fixture with
// diverged branches.
func TestCompareBranches(t *testing.T) {
	// Build: main@A + branch side@B diverged.
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.email", "t@t")
	run("config", "user.name", "tester")
	write("shared.txt", "base\n")
	run("add", ".")
	run("commit", "-qm", "base")

	run("switch", "-qc", "side")
	write("side-only.txt", "side\n")
	run("add", ".")
	run("commit", "-qm", "side commit")

	run("switch", "-q", "main")
	write("main-only.txt", "main\n")
	run("add", ".")
	run("commit", "-qm", "main commit")

	repo := NewRepo(dir)
	ctx := context.Background()
	cmp, err := repo.CompareBranches(ctx, "main", "side")
	if err != nil {
		t.Fatalf("CompareBranches: %v", err)
	}
	if cmp.Unrelated {
		t.Error("marked unrelated")
	}
	if cmp.Base == "" {
		t.Error("no merge-base")
	}
	if len(cmp.AOnly) != 1 || cmp.AOnly[0].Subject != "main commit" {
		t.Errorf("AOnly = %+v", cmp.AOnly)
	}
	if len(cmp.BOnly) != 1 || cmp.BOnly[0].Subject != "side commit" {
		t.Errorf("BOnly = %+v", cmp.BOnly)
	}
	if len(cmp.Files) != 1 || cmp.Files[0].Path != "side-only.txt" {
		t.Errorf("Files = %+v", cmp.Files)
	}
}
