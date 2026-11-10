package git

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// RevertPaths discards worktree changes for the given paths, restoring
// them to the index (git restore). For untracked files revert makes no
// sense — callers should use DeletePath instead.
//
// DESTRUCTIVE: bytes the user edited and never staged/committed are
// lost. The UI confirms before calling.
func (r *Repo) RevertPaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"restore", "--"}, paths...)
	res, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return nil
}

// RevertStagedPaths restores index entries for paths to HEAD (git
// restore --staged). Same destructive caveat for staged-but-uncommitted
// edits.
func (r *Repo) RevertStagedPaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"restore", "--staged", "--worktree", "--"}, paths...)
	res, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return nil
}

// DeletePath removes an untracked file from the worktree. DESTRUCTIVE —
// the file is gone, not in any index/HEAD.
func (r *Repo) DeletePath(ctx context.Context, path string) error {
	full := r.Worktree + "/" + path
	if err := os.Remove(full); err != nil {
		return fmt.Errorf("删除 %s: %w", path, err)
	}
	return nil
}

// AddToGitignore appends a path to the repo's .gitignore (creating the
// file at the worktree root when absent). The pattern is the path
// itself with a leading "/" — anchored, not a glob.
func (r *Repo) AddToGitignore(ctx context.Context, path string) error {
	_ = ctx
	gi := r.Worktree + "/.gitignore"
	f, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 .gitignore: %w", err)
	}
	defer f.Close()
	// Anchored absolute-from-root pattern matches the file only.
	if _, err := f.WriteString("/" + path + "\n"); err != nil {
		return fmt.Errorf("写入 .gitignore: %w", err)
	}
	return nil
}

// StashPushPaths stashes only the given paths (pathspec-limited push).
// includeUntracked adds untracked files that match the pathspec.
func (r *Repo) StashPushPaths(ctx context.Context, message string, paths []string, includeUntracked bool) (*RunResult, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("stash: no paths")
	}
	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "--include-untracked")
	}
	if message != "" {
		args = append(args, "-m", message)
	}
	args = append(args, "--")
	args = append(args, paths...)
	return r.RunOK(ctx, args...)
}
