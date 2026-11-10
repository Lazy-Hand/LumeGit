package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupRemotePair builds a bare "remote" and a local clone with one
// commit each, returning (remote dir, clone dir). Uses file:// so no
// network or credentials are needed.
func setupRemotePair(t *testing.T) (remote, local string) {
	t.Helper()
	base := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v in %s: %v: %s", args, dir, err, out)
		}
	}
	// Bare remote.
	remote = filepath.Join(base, "remote.git")
	if err := os.Mkdir(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	run(remote, "init", "-q", "--bare", "-b", "main")
	// Seed a commit via a scratch clone.
	seed := filepath.Join(base, "seed")
	run(base, "clone", "-q", remote, seed)
	run(seed, "config", "user.email", "t@t")
	run(seed, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", ".")
	run(seed, "commit", "-qm", "seed")
	run(seed, "push", "-q", "origin", "main")
	// Working clone.
	local = filepath.Join(base, "local")
	run(base, "clone", "-q", remote, local)
	run(local, "config", "user.email", "t@t")
	run(local, "config", "user.name", "tester")
	return remote, local
}

// TestUpstreamAndPush verifies ahead/behind counting and a real push.
func TestUpstreamAndPush(t *testing.T) {
	remote, local := setupRemotePair(t)
	ctx := context.Background()
	repo := NewRepo(local)

	// Fresh clone: upstream exists, in sync.
	up, err := repo.Upstream(ctx)
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	if !up.HasUpstream || up.Ahead != 0 || up.Behind != 0 {
		t.Fatalf("upstream = %+v, want in-sync", up)
	}

	// Commit locally → ahead 1.
	if err := os.WriteFile(filepath.Join(local, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = local
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-qm", "local work")
	cmd.Dir = local
	_ = cmd.Run()

	up, err = repo.Upstream(ctx)
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	if up.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", up.Ahead)
	}

	// Push → back in sync.
	res := repo.Push(ctx, PushRequest{})
	if res.Err != nil {
		t.Fatalf("Push: %v\n%s", res.Err, res.Output)
	}
	up, _ = repo.Upstream(ctx)
	if up.Ahead != 0 {
		t.Errorf("ahead after push = %d", up.Ahead)
	}
	_ = remote
}

// TestFetchAndPull verifies fetch reports remote commits and pull
// integrates them.
func TestFetchAndPull(t *testing.T) {
	remote, local := setupRemotePair(t)
	base := filepath.Dir(remote)
	ctx := context.Background()
	repo := NewRepo(local)

	// Push an extra commit from a second clone.
	other := filepath.Join(base, "other")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run(base, "clone", "-q", remote, other)
	run(other, "config", "user.email", "o@o")
	run(other, "config", "user.name", "other")
	if err := os.WriteFile(filepath.Join(other, "c.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(other, "add", ".")
	run(other, "commit", "-qm", "other work")
	run(other, "push", "-q", "origin", "main")

	// Before fetch: behind 0 (stale).
	up, _ := repo.Upstream(ctx)
	if up.Behind != 0 {
		t.Logf("behind before fetch = %d (expected 0 — stale)", up.Behind)
	}

	// Fetch → behind 1.
	res := repo.Fetch(ctx)
	if res.Err != nil {
		t.Fatalf("Fetch: %v\n%s", res.Err, res.Output)
	}
	up, _ = repo.Upstream(ctx)
	if up.Behind != 1 {
		t.Errorf("behind after fetch = %d, want 1", up.Behind)
	}

	// Pull → file arrives, in sync.
	res = repo.Pull(ctx, PullRequest{})
	if res.Err != nil {
		t.Fatalf("Pull: %v\n%s", res.Err, res.Output)
	}
	if _, err := os.Stat(filepath.Join(local, "c.txt")); err != nil {
		t.Errorf("pulled file missing: %v", err)
	}
	up, _ = repo.Upstream(ctx)
	if up.Behind != 0 || up.Ahead != 0 {
		t.Errorf("after pull upstream = %+v, want in-sync", up)
	}
}

// TestPushNoUpstream verifies -u push on a branch with no tracking.
func TestPushNoUpstream(t *testing.T) {
	remote, local := setupRemotePair(t)
	ctx := context.Background()
	repo := NewRepo(local)

	// New branch, no upstream.
	cmd := exec.Command("git", "checkout", "-qb", "feature")
	cmd.Dir = local
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("checkout: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(local, "f.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = local
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-qm", "feat")
	cmd.Dir = local
	_ = cmd.Run()

	up, _ := repo.Upstream(ctx)
	if up.HasUpstream {
		t.Fatalf("new branch unexpectedly has upstream %+v", up)
	}

	res := repo.Push(ctx, PushRequest{})
	if res.Err != nil {
		t.Fatalf("Push -u: %v\n%s", res.Err, res.Output)
	}
	up, _ = repo.Upstream(ctx)
	if !up.HasUpstream {
		t.Error("upstream not set after -u push")
	}
	if up.Name != "origin/feature" {
		t.Errorf("upstream name = %q, want origin/feature", up.Name)
	}
	_ = remote
}
