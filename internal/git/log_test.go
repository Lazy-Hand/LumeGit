package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParseLog feeds the exact US/RS record stream the real git emits
// and checks field extraction.
func TestParseLog(t *testing.T) {
	rec := "abc123def4567890abc123def4567890abc12345" +
		"\x1fabc123d" +
		"\x1f" + // no parents (root)
		"\x1fTester" +
		"\x1ft@example.com" +
		"\x1f2026-11-09T16:18:06+08:00" +
		"\x1ffirst commit" +
		"\x1fHEAD -> main, tag: v1.0" +
		"\x1e\n"
	commits := parseLog([]byte(rec))
	if len(commits) != 1 {
		t.Fatalf("parseLog got %d commits", len(commits))
	}
	c := commits[0]
	if c.OID != "abc123def4567890abc123def4567890abc12345" {
		t.Errorf("OID = %q", c.OID)
	}
	if c.ShortOID != "abc123d" {
		t.Errorf("ShortOID = %q", c.ShortOID)
	}
	if len(c.Parents) != 0 {
		t.Errorf("Parents = %v", c.Parents)
	}
	if c.Author != "Tester" || c.Email != "t@example.com" {
		t.Errorf("author = %q <%q>", c.Author, c.Email)
	}
	if c.Subject != "first commit" {
		t.Errorf("Subject = %q", c.Subject)
	}
	if c.When.IsZero() {
		t.Error("When not parsed")
	}
	want := []string{"main", "v1.0"}
	if len(c.Refs) != len(want) {
		t.Fatalf("Refs = %v", c.Refs)
	}
	for i, r := range want {
		if c.Refs[i] != r {
			t.Errorf("Refs[%d] = %q, want %q", i, c.Refs[i], r)
		}
	}
	if c.Merge() {
		t.Error("root commit reported as merge")
	}
}

// TestParseNameStatus checks ordinary and rename records decode the
// right path tokens.
func TestParseNameStatus(t *testing.T) {
	out := "M\x00src/main.go\x00A\x00new.txt\x00R100\x00old.go\x00renamed.go\x00D\x00gone.go\x00"
	files := parseNameStatus([]byte(out))
	want := []CommitFile{
		{Status: 'M', Path: "src/main.go"},
		{Status: 'A', Path: "new.txt"},
		{Status: 'R', OrigPath: "old.go", Path: "renamed.go"},
		{Status: 'D', Path: "gone.go"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files: %+v", len(files), files)
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %+v, want %+v", i, files[i], w)
		}
	}
}

// TestLogAndDiffAgainstRealRepo runs the whole chain against a temp
// repo: init, two commits, rename, then Log + CommitFiles +
// CommitFileDiff.
func TestLogAndDiffAgainstRealRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.email", "t@t")
	run("config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "first")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "second")
	run("mv", "a.txt", "renamed.txt")
	run("commit", "-qm", "rename a")

	ctx := context.Background()
	repo := NewRepo(dir)

	commits, err := repo.Log(ctx, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("Log got %d commits", len(commits))
	}
	if commits[0].Subject != "rename a" {
		t.Errorf("newest subject = %q", commits[0].Subject)
	}
	if len(commits[0].Parents) != 1 || commits[0].Parents[0] != commits[1].OID {
		t.Errorf("newest parents = %v, want [%s]", commits[0].Parents, commits[1].OID)
	}
	// Root commit has no parent.
	if len(commits[2].Parents) != 0 {
		t.Errorf("root parents = %v", commits[2].Parents)
	}

	// File list of the rename commit.
	files, err := repo.CommitFiles(ctx, commits[0].OID)
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if len(files) != 1 || files[0].Status != 'R' || files[0].OrigPath != "a.txt" || files[0].Path != "renamed.txt" {
		t.Fatalf("rename files = %+v", files)
	}

	// Diff of the modified file in "second".
	files2, err := repo.CommitFiles(ctx, commits[1].OID)
	if err != nil {
		t.Fatalf("CommitFiles second: %v", err)
	}
	if len(files2) != 1 || files2[0].Status != 'M' {
		t.Fatalf("second files = %+v", files2)
	}
	d, err := repo.CommitFileDiff(ctx, commits[1].OID, files2[0])
	if err != nil {
		t.Fatalf("CommitFileDiff: %v", err)
	}
	if string(d.Old) != "line1\n" {
		t.Errorf("old = %q", d.Old)
	}
	if string(d.New) != "line1\nline2\n" {
		t.Errorf("new = %q", d.New)
	}
	if d.Binary {
		t.Error("text file flagged binary")
	}

	// Root commit: added file has an empty old side.
	files3, err := repo.CommitFiles(ctx, commits[2].OID)
	if err != nil {
		t.Fatalf("CommitFiles root: %v", err)
	}
	d3, err := repo.CommitFileDiff(ctx, commits[2].OID, files3[0])
	if err != nil {
		t.Fatalf("CommitFileDiff root: %v", err)
	}
	if len(d3.Old) != 0 {
		t.Errorf("root old = %q, want empty", d3.Old)
	}
	if string(d3.New) != "line1\n" {
		t.Errorf("root new = %q", d3.New)
	}
}
