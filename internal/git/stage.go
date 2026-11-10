package git

import (
	"context"
	"fmt"
	"strings"
)

// Stage adds paths to the index. Untracked files need `add` the same as
// tracked ones; git resolves intent from the path itself.
func (r *Repo) Stage(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	res, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return nil
}

// Unstage removes paths from the index back to HEAD, keeping worktree
// bytes. Equivalent to IDEA's "Unstage" / git's `reset HEAD --`.
// On an unborn branch (no HEAD) we remove the path from the index
// entirely with `rm --cached`, which is the only meaningful unstage.
func (r *Repo) Unstage(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// Unborn branch has no HEAD to reset against.
	headless, err := r.unborn(ctx)
	if err != nil {
		return err
	}
	var args []string
	if headless {
		args = append([]string{"rm", "--cached", "-r", "--"}, paths...)
	} else {
		args = append([]string{"reset", "-q", "HEAD", "--"}, paths...)
	}
	res, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return nil
}

// unborn reports whether HEAD resolves to a commit.
func (r *Repo) unborn(ctx context.Context) (bool, error) {
	res, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return false, err
	}
	return !res.ExitOK(), nil
}

// CommitStaged commits whatever the index currently holds — the Git
// staging-mode path. Unlike Commit (changelist mode), this does NOT
// stage anything itself: the user built the index explicitly, we only
// verify it isn't empty and let `git commit` do the rest.
func (r *Repo) CommitStaged(ctx context.Context, message string, amend bool) (*CommitOutcome, error) {
	// Refuse on conflicts/in-progress; an empty index is a no-op error.
	snap, err := r.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit staged: %w", err)
	}
	if snap.InProgress != "" {
		return nil, ErrConflictInProgress
	}
	for _, f := range snap.Files {
		if f.Conflict() {
			return nil, ErrConflictInProgress
		}
	}
	stagedFiles := snap.Staged()
	if len(stagedFiles) == 0 && !amend {
		return nil, fmt.Errorf("暂存区为空 — 先暂存要提交的文件")
	}
	args := []string{"commit", "-m", message}
	if amend {
		args = append(args, "--amend")
	}
	res, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	outcome := &CommitOutcome{
		Exit:    res.Exit,
		Output:  res.Output(),
		ExitOK:  res.ExitOK(),
		HookOut: res.OutputOfStderr(),
	}
	if !res.ExitOK() {
		outcome.Error = &GitError{Args: args, Stderr: res.OutputOfStderr()}
	}
	return outcome, nil
}
