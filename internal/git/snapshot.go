package git

import (
	"context"
	"strconv"
	"strings"
)

// inProgressStates maps sequencer files git names to display labels.
// Detected via `git status`'s own report, not by probing files, so it
// stays correct across git versions.
var inProgressStates = map[string]string{
	"cherry_pick": "cherry-pick",
	"revert":      "revert",
	"rebase":      "rebase",
	"merge":       "merge",
	"rebase_i":    "rebase (interactive)",
	"rebase_m":    "rebase (merge)",
	"rebase_am":   "rebase (am)",
	"bisect":      "bisect",
	"am":          "am",
	"am/rebase":   "am/rebase",
}

// Snapshot captures one consistent read of the repository state for the
// commit workspace. Callers treat it as immutable; a new read replaces
// it wholesale.
//
// Consistency model: this is a snapshot of git's own view at execution
// time, not a transaction over HEAD+index+worktree. Operations that
// change state must re-verify against a fresh snapshot before acting.
func (r *Repo) Snapshot(ctx context.Context) (*Snapshot, error) {
	// -uall lists untracked files individually; -uno reports only the
	// directory. PreferNoUntracked picks the cheap mode for repos whose
	// untracked set is large enough that enumerating it dominates the
	// refresh cost.
	untracked := "-uall"
	if r.PreferNoUntracked {
		untracked = "-uno"
	}
	res, err := r.RunOK(ctx, "status", "--porcelain=v2", "-z", "--branch", untracked)
	if err != nil {
		return nil, err
	}
	bi, files, err := parseStatus(res.Stdout)
	if err != nil {
		return nil, err
	}

	s := &Snapshot{
		Worktree: r.Worktree,
		Ahead:    bi.Ahead,
		Behind:   bi.Behind,
		Files:    files,
	}
	switch bi.OID {
	case "", "(initial)":
		s.Unborn = true
	default:
		s.HeadOID = bi.OID
	}
	switch bi.Head {
	case "(detached)":
		s.Detached = true
	default:
		s.HeadName = bi.Head
	}
	if !bi.HasAB {
		s.Ahead, s.Behind = -1, -1
	}

	// Which operations are mid-flight. The plain (non-porcelain) long
	// status prints an "You have ..." hint we can detect cheaply, but
	// probe the well-known control files instead: porcelain v2 may not
	// include the in-progress notice with -z.
	s.InProgress = detectInProgress(ctx, r)
	return s, nil
}

// detectInProgress checks gitdir control files for operations that
// change what a commit means. One git invocation — `--path-format=absolute`
// already covers the old `--git-dir` case (relative paths get anchored
// to the worktree, absolute stays as-is).
func detectInProgress(ctx context.Context, r *Repo) string {
	res, err := r.RunOK(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(res.Output())
	has := func(name string) bool { return fileExists(gitDir + "/" + name) }

	switch {
	case has("MERGE_HEAD"):
		return "merge"
	case has("REVERT_HEAD"):
		return "revert"
	case has("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case has("BISECT_LOG"):
		return "bisect"
	case has("rebase-merge"):
		return "rebase (interactive)"
	case has("rebase-apply"):
		// rebase-apply serves both `am` and `git rebase --am`.
		if has("rebase-apply/rebasing") {
			return "rebase (am)"
		}
		return "am"
	}
	return ""
}

// EnableFSMonitor turns on git's builtin filesystem monitor
// (core.fsmonitor=true), which lets `git status` reuse a daemon's file
// cache instead of stat()ing every file. Fork measured 600ms→50ms on a
// typical repo and 5000ms→1000ms on Chromium. Git <2.36 lacks the
// feature; older versions just fail the config write and we ignore it.
// Idempotent: safe to call on every open.
func (r *Repo) EnableFSMonitor(ctx context.Context) {
	maj, min, err := r.gitVersion(ctx)
	if err != nil || maj < 2 || (maj == 2 && min < 36) {
		return
	}
	_, _ = r.Run(ctx, "config", "core.fsmonitor", "true")
}

// HeadResolution returns the symbolic name and OID of HEAD. Used by the
// pre-commit re-check. Unborn HEAD returns "" for name.
func (r *Repo) HeadResolution(ctx context.Context) (name, oid string, err error) {
	res, err := r.RunOK(ctx, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		// Unborn HEAD: symbolic-full-name fails.
		res2, err2 := r.RunOK(ctx, "rev-parse", "HEAD")
		if err2 != nil {
			return "", "", &GitError{Args: []string{"rev-parse", "HEAD"}, Stderr: res2.OutputOfStderr()}
		}
		return "", strings.TrimSpace(res2.Output()), nil
	}
	name = strings.TrimSpace(res.Output())
	res3, err := r.RunOK(ctx, "rev-parse", "HEAD")
	if err != nil {
		return name, "", err
	}
	return name, strings.TrimSpace(res3.Output()), nil
}

// IsAncestor reports whether c1 is an ancestor of c2.
func (r *Repo) IsAncestor(ctx context.Context, c1, c2 string) (bool, error) {
	res, err := r.RunOK(ctx, "merge-base", "--is-ancestor", c1, c2)
	if err != nil {
		ge, ok := err.(*GitError)
		if ok && res != nil && res.Exit == 1 {
			return false, nil
		}
		_ = ge
		return false, err
	}
	return res.ExitOK(), nil
}

// stashFormatNumber returns git's minor version for feature gating.
func (r *Repo) gitVersion(ctx context.Context) (int, int, error) {
	res, err := r.RunOK(ctx, "version")
	if err != nil {
		return 0, 0, err
	}
	// "git version 2.39.2"
	fields := strings.Fields(strings.TrimSpace(res.Output()))
	if len(fields) < 3 {
		return 0, 0, err
	}
	parts := strings.SplitN(fields[2], ".", 3)
	maj, _ := strconv.Atoi(parts[0])
	min := 0
	if len(parts) > 1 {
		min, _ = strconv.Atoi(parts[1])
	}
	return maj, min, nil
}
