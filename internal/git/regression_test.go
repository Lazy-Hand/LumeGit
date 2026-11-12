package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stagedNames lists index-vs-HEAD paths (empty = clean index).
func stagedNames(t *testing.T, r *Repo) []string {
	t.Helper()
	res, err := r.RunOK(context.Background(), "diff", "--cached", "--name-only", "--no-renames")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(res.Output())
}

// A worktree rename (delete a + add b) selected as both paths must
// commit: rename detection used to hide a from the verify step.
func TestCommitRenameSelection(t *testing.T) {
	dir := setupWorkRepo(t)
	r := NewRepo(dir)
	if err := os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	out, err := r.Commit(context.Background(), CommitRequest{
		Paths: []string{"a.txt"}, Untracked: []string{"b.txt"}, Message: "rename",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !out.ExitOK {
		t.Fatalf("commit failed: %+v", out)
	}
}

// A rejected commit (pre-commit hook) must hand the index back clean,
// otherwise every later commit is blocked by ErrStagedChangesPending.
func TestCommitHookFailureRollsBackIndex(t *testing.T) {
	dir := setupWorkRepo(t)
	r := NewRepo(dir)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, "a.txt", "one\ntwo\n")
	writeTestFile(t, dir, "new.txt", "n\n")
	req := CommitRequest{Paths: []string{"a.txt"}, Untracked: []string{"new.txt"}, Message: "x"}

	out, err := r.Commit(context.Background(), req)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if out.ExitOK {
		t.Fatal("hook should have rejected the commit")
	}
	if s := stagedNames(t, r); len(s) != 0 {
		t.Fatalf("index not rolled back: %v", s)
	}
	// Worktree untouched.
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("worktree changed: %q", b)
	}
	// Retry once the hook is gone succeeds instead of hitting
	// ErrStagedChangesPending.
	_ = os.Remove(hook)
	out, err = r.Commit(context.Background(), req)
	if err != nil || !out.ExitOK {
		t.Fatalf("retry: err=%v out=%+v", err, out)
	}
}

// The very first commit of a fresh repo (unborn HEAD) must work.
func TestCommitUnbornFirstCommit(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main", ".")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "tester")
	writeTestFile(t, dir, "a.txt", "x\n")
	out, err := NewRepo(dir).Commit(context.Background(), CommitRequest{
		Untracked: []string{"a.txt"}, Message: "first",
	})
	if err != nil || !out.ExitOK {
		t.Fatalf("first commit: err=%v out=%+v", err, out)
	}
}

// Failure on an unborn branch rolls back with rm --cached.
func TestCommitUnbornRollback(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main", ".")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "tester")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, "a.txt", "x\n")
	r := NewRepo(dir)
	if out, err := r.Commit(context.Background(), CommitRequest{Untracked: []string{"a.txt"}, Message: "x"}); err != nil || out.ExitOK {
		t.Fatalf("expected hook rejection: err=%v out=%+v", err, out)
	}
	res, _ := r.Run(context.Background(), "ls-files")
	if strings.TrimSpace(res.Output()) != "" {
		t.Fatalf("index not rolled back: %q", res.Output())
	}
}

// An untracked file named like an option must stay a pathspec.
func TestCommitUntrackedDashName(t *testing.T) {
	dir := setupWorkRepo(t)
	writeTestFile(t, dir, "-A", "dash\n")
	writeTestFile(t, dir, "other.txt", "o\n")
	r := NewRepo(dir)
	out, err := r.Commit(context.Background(), CommitRequest{Untracked: []string{"-A"}, Message: "dash"})
	if err != nil || !out.ExitOK {
		t.Fatalf("commit: err=%v out=%+v", err, out)
	}
	res, _ := r.Run(context.Background(), "show", "--name-only", "--format=", "HEAD")
	if got := strings.TrimSpace(res.Output()); got != "-A" {
		t.Fatalf("committed %q, want only -A", got)
	}
}

func partialRepo(t *testing.T) (string, *Repo, []string) {
	dir := setupWorkRepo(t)
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line"+itoa(i))
	}
	commitFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n", "add f")
	lines[2] = "CHANGED-top"
	lines[17] = "CHANGED-bottom"
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")
	return dir, NewRepo(dir), lines
}

// A hunk selection made against an older version of the file must be
// refused, not applied to whatever hunk now sits at that index.
func TestCommitPartialStaleSelection(t *testing.T) {
	dir, r, lines := partialRepo(t)
	seen, err := r.FileHunks(context.Background(), "f.txt")
	if err != nil || len(seen) != 2 {
		t.Fatalf("hunks: %v %d", err, len(seen))
	}
	// File changes after the user picked hunks.
	lines[9] = "CHANGED-middle"
	writeTestFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")

	_, err = r.Commit(context.Background(), CommitRequest{
		Paths:        []string{"f.txt"},
		Partial:      map[string][]bool{"f.txt": {true, false}},
		PartialHunks: map[string][]Hunk{"f.txt": seen},
		Message:      "partial",
	})
	if !errors.Is(err, ErrPartialStale) {
		t.Fatalf("err = %v, want ErrPartialStale", err)
	}
	if s := stagedNames(t, r); len(s) != 0 {
		t.Fatalf("index not rolled back: %v", s)
	}
}

// A matching snapshot of hunks goes through.
func TestCommitPartialFreshSelection(t *testing.T) {
	_, r, _ := partialRepo(t)
	seen, _ := r.FileHunks(context.Background(), "f.txt")
	out, err := r.Commit(context.Background(), CommitRequest{
		Paths:        []string{"f.txt"},
		Partial:      map[string][]bool{"f.txt": {true, false}},
		PartialHunks: map[string][]Hunk{"f.txt": seen},
		Message:      "partial",
	})
	if err != nil || !out.ExitOK {
		t.Fatalf("commit: err=%v out=%+v", err, out)
	}
	show, _ := r.Run(context.Background(), "show", "HEAD:f.txt")
	if !strings.Contains(show.Output(), "CHANGED-top") || strings.Contains(show.Output(), "CHANGED-bottom") {
		t.Fatalf("wrong content committed:\n%s", show.Output())
	}
}

// A partial pick for a path outside the selection is refused up front
// and leaves the index untouched.
func TestCommitPartialUnselectedPath(t *testing.T) {
	dir, r, _ := partialRepo(t)
	writeTestFile(t, dir, "a.txt", "one\ntwo\n")
	_, err := r.Commit(context.Background(), CommitRequest{
		Paths:   []string{"a.txt"},
		Partial: map[string][]bool{"f.txt": {true, false}},
		Message: "x",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if s := stagedNames(t, r); len(s) != 0 {
		t.Fatalf("index dirty: %v", s)
	}
}

// A branch named like a directory must not make log/compare ambiguous.
func TestRefNamedLikeDirectory(t *testing.T) {
	dir := setupWorkRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, dir, "docs/x.md", "x\n", "docs")
	runGit(t, dir, "branch", "docs")
	commitFile(t, dir, "a.txt", "changed\n", "after")
	r := NewRepo(dir)
	ctx := context.Background()

	if cs, err := r.LogFiltered(ctx, LogOptions{Branch: "docs"}); err != nil || len(cs) != 2 {
		t.Fatalf("LogFiltered: %v, %d commits", err, len(cs))
	}
	if fs, err := r.CompareFiles(ctx, "docs"); err != nil || len(fs) != 1 {
		t.Fatalf("CompareFiles: %v %+v", err, fs)
	}
	if bc, err := r.CompareBranches(ctx, "HEAD", "docs"); err != nil || len(bc.Files) != 1 {
		t.Fatalf("CompareBranches: %v %+v", err, bc)
	}
	if _, err := r.ChangedBetween(ctx, "docs"); err != nil {
		t.Fatalf("ChangedBetween: %v", err)
	}
	if plan, err := r.RebasePlan(ctx, "docs"); err != nil || len(plan) != 1 {
		t.Fatalf("RebasePlan: %v %+v", err, plan)
	}
}

// Two children sharing a parent: the second lane must end at the
// parent instead of dangling to the bottom of the list.
func TestBuildGraphSiblingsJoin(t *testing.T) {
	cs := []Commit{
		{OID: "D", Parents: []string{"A"}},
		{OID: "C", Parents: []string{"A"}},
		{OID: "A", Parents: []string{"R"}},
		{OID: "R"},
	}
	g := BuildGraph(cs)
	if len(g[2].Joins) != 1 || g[2].Joins[0] != 1 {
		t.Fatalf("A row joins = %v, want [1]", g[2].Joins)
	}
	for _, row := range g[2:] {
		if len(row.Lanes) > 1 && row.Lanes[1] != 0 {
			t.Fatalf("lane 1 still active after the join: %+v", row)
		}
	}
}

func TestParseLFSTrack(t *testing.T) {
	out := "Listing tracked patterns\n    *.psd (.gitattributes)\n    assets/* (.gitattributes)\n" +
		"Listing excluded patterns\n    *.tmp (.gitattributes)\n"
	got := parseLFSTrack(out).Patterns
	if strings.Join(got, ",") != "*.psd,assets/*" {
		t.Fatalf("patterns = %v", got)
	}
}

func TestAddToGitignoreNoTrailingNewline(t *testing.T) {
	dir := setupWorkRepo(t)
	writeTestFile(t, dir, ".gitignore", "*.log")
	if err := NewRepo(dir).AddToGitignore(context.Background(), "build"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != "*.log\n/build\n" {
		t.Fatalf(".gitignore = %q", b)
	}
}

func TestBisectStatusCurrentCommit(t *testing.T) {
	dir := setupWorkRepo(t)
	commitFile(t, dir, "b.txt", "b\n", "c2")
	commitFile(t, dir, "c.txt", "c\n", "c3")
	commitFile(t, dir, "d.txt", "d\n", "c4")
	r := NewRepo(dir)
	ctx := context.Background()
	if _, err := r.BisectStart(ctx, "HEAD~3", "HEAD"); err != nil {
		t.Fatal(err)
	}
	defer r.BisectReset(ctx)
	st, err := r.BisectStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active || st.CurrentOID == "" || st.CurrentSub == "" {
		t.Fatalf("status = %+v", st)
	}
}

// remoteHead reads a branch tip from the bare remote ("" if missing).
func remoteHead(t *testing.T, remote, branch string) string {
	t.Helper()
	res, err := NewRepo(remote).Run(context.Background(), "rev-parse", "-q", "--verify", "refs/heads/"+branch)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(res.Output())
}

// The upstream lives on a remote not called origin, under a different
// branch name: push must update exactly that branch.
func TestPushHonorsUpstreamRemoteAndName(t *testing.T) {
	remote, local := setupRemotePair(t)
	ctx := context.Background()
	runGit(t, local, "remote", "rename", "origin", "up")
	runGit(t, local, "switch", "-q", "-c", "feat", "--track", "up/main")
	// push.default=simple (git's own default) refuses to push feat→main:
	// the names differ. Real users working triangular setups configure
	// upstream/current; mirror that so the test exercises the refspec
	// path, not git's refusal.
	runGit(t, local, "config", "push.default", "upstream")
	commitFile(t, local, "b.txt", "b\n", "feat work")
	r := NewRepo(local)

	if res := r.Push(ctx, PushRequest{}); res.Err != nil {
		t.Fatalf("Push: %v\n%s", res.Err, res.Output)
	}
	if got, want := remoteHead(t, remote, "main"), headOID(t, r); got != want {
		t.Fatalf("remote main = %q, want %q", got, want)
	}
	if remoteHead(t, remote, "feat") != "" {
		t.Fatal("push created a stray remote 'feat' branch")
	}

	// Push-up-to targets the same upstream branch.
	commitFile(t, local, "c.txt", "c\n", "more")
	commitFile(t, local, "d.txt", "d\n", "more2")
	res, _ := r.Run(ctx, "rev-parse", "HEAD~1")
	mid := strings.TrimSpace(res.Output())
	if res, err := r.PushUpTo(ctx, mid, ""); err != nil || !res.ExitOK() {
		t.Fatalf("PushUpTo: %v %s", err, res.OutputOfStderr())
	}
	if got := remoteHead(t, remote, "main"); got != mid {
		t.Fatalf("remote main = %q, want %q", got, mid)
	}
}

// No upstream and the only remote isn't named origin: push -u there.
func TestPushNoUpstreamSingleNonOriginRemote(t *testing.T) {
	remote, local := setupRemotePair(t)
	runGit(t, local, "remote", "rename", "origin", "github")
	runGit(t, local, "switch", "-q", "-c", "topic")
	commitFile(t, local, "t.txt", "t\n", "topic")
	r := NewRepo(local)
	if res := r.Push(context.Background(), PushRequest{}); res.Err != nil {
		t.Fatalf("Push: %v\n%s", res.Err, res.Output)
	}
	if remoteHead(t, remote, "topic") == "" {
		t.Fatal("topic not pushed")
	}
	if up, _ := r.Upstream(context.Background()); up.Name != "github/topic" {
		t.Fatalf("upstream = %q", up.Name)
	}
}

// Several remotes, none origin, no upstream: refuse instead of guessing.
func TestPushAmbiguousRemote(t *testing.T) {
	remote, local := setupRemotePair(t)
	runGit(t, local, "remote", "rename", "origin", "a")
	runGit(t, local, "remote", "add", "b", remote)
	runGit(t, local, "switch", "-q", "-c", "topic")
	if res := NewRepo(local).Push(context.Background(), PushRequest{}); res.Err == nil {
		t.Fatal("expected an error for ambiguous remote")
	}
}
