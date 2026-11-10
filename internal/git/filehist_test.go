package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFileLogAndBlame exercises --follow log and --porcelain blame
// against a real repo with a rename.
func TestFileLogAndBlame(t *testing.T) {
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

	// Second commit modifies a.txt, then rename it.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "expand a")
	run("mv", "a.txt", "renamed.txt")
	run("commit", "-qm", "rename")

	// FileLog follows the rename: all 3 commits should appear.
	commits, err := repo.FileLog(ctx, "renamed.txt", 0)
	if err != nil {
		t.Fatalf("FileLog: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("FileLog got %d commits (rename not followed): %v", len(commits), commits)
	}
	if commits[0].Subject != "rename" || commits[2].Subject != "seed" {
		t.Errorf("subjects = %q … %q", commits[0].Subject, commits[2].Subject)
	}

	// Blame attributes each line to its commit.
	lines, err := repo.Blame(ctx, "HEAD", "renamed.txt")
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("blame got %d lines", len(lines))
	}
	// "one" was introduced in "seed" (commit idx 2); "two" in "expand a"
	// (commit idx 1). Blame attributes lines to the commit that wrote them.
	if lines[0].OID != commits[2].OID {
		t.Errorf("line 0 oid = %s, want %s (seed)", lines[0].OID, commits[2].OID)
	}
	if lines[1].OID != commits[1].OID {
		t.Errorf("line 1 oid = %s, want %s (expand a)", lines[1].OID, commits[1].OID)
	}
	for i, ln := range lines {
		if ln.Author != "tester" {
			t.Errorf("line %d author = %q", i, ln.Author)
		}
		// Filename comes from the blamed commit's path (a.txt, before
		// the rename to renamed.txt).
		if ln.Filename != "a.txt" {
			t.Errorf("line %d filename = %q, want a.txt", i, ln.Filename)
		}
	}
	if lines[0].Text != "one" || lines[1].Text != "two" {
		t.Errorf("texts = %q %q", lines[0].Text, lines[1].Text)
	}
	if lines[0].LineNo != 1 || lines[1].LineNo != 2 {
		t.Errorf("lineNos = %d %d", lines[0].LineNo, lines[1].LineNo)
	}
}
