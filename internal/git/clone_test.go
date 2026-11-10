package git

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestInitRepo verifies `git init` produces an openable repo.
func TestInitRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	res, err := InitRepo(context.Background(), dir)
	if err != nil || !res.ExitOK() {
		t.Fatalf("InitRepo: %v %s", err, res.OutputOfStderr())
	}
	// The new repo opens as a valid Repo.
	if _, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Output(); err != nil {
		t.Fatalf("not a repo: %v", err)
	}
}
