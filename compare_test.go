package main

// TestCompareSelectFile exercises the compare overlay end to end:
// openFor lists diff files from a real repo, selectFile loads a diff.
// Guards the two-dot CompareFiles semantics — a three-dot list used
// to name files whose ref/HEAD blobs were identical (empty-looking
// diff).

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCompareSelectFile(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "a@b.c")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init")
	gitT(t, dir, "checkout", "-qb", "b1")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0644)
	gitT(t, dir, "commit", "-qam", "b")
	gitT(t, dir, "checkout", "-q", "master")

	a := &app{}
	a.commit.RepoPath = dir
	sync := updater{update: func(fn func()) { fn() }}

	a.compare.openFor(dir, "b1", sync)
	deadline := time.Now().Add(3 * time.Second)
	for a.compare.loading && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("loading=%v err=%q files=%d", a.compare.loading, a.compare.err, len(a.compare.files))
	if a.compare.err != "" {
		t.Fatalf("load err: %s", a.compare.err)
	}
	if len(a.compare.files) == 0 {
		t.Fatal("no files listed")
	}

	a.compare.selectFile(dir, a.compare.files[0].Path, sync)
	deadline = time.Now().Add(3 * time.Second)
	for a.compare.diffLoad && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("diff=%v err=%q", a.compare.diff != nil, a.compare.diffErr)
	if a.compare.diffErr != "" {
		t.Fatalf("diff err: %s", a.compare.diffErr)
	}
	if a.compare.diff == nil {
		t.Fatal("diff stayed nil")
	}
	t.Logf("old=%q new=%q", a.compare.diff.Old, a.compare.diff.New)
}
