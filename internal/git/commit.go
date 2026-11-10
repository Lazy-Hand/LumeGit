package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrStagedChangesPending reports that the index already holds staged
// content, so a changelist-mode selective commit must not proceed
// automatically (design decision: first version refuses and points the
// user at staging mode rather than silently touching their index).
var ErrStagedChangesPending = errors.New("staged changes exist; changelist commits are blocked to preserve the index")

// ErrConflictInProgress reports unresolved conflicts or a sequencer
// operation mid-flight; a plain commit is unsafe there.
var ErrConflictInProgress = errors.New("in-progress operation or conflicts; resolve before committing")

// ErrNotCleanState reports that repo state changed between the
// snapshot the user reviewed and the commit attempt.
type ErrNotCleanState struct {
	What string
}

func (e *ErrNotCleanState) Error() string {
	return "repository changed before commit: " + e.What
}

// CommitRequest is a reviewed, explicit commit: exact paths, exact
// untracked inclusions, and the message.
type CommitRequest struct {
	Paths     []string // tracked pathspecs to commit from the worktree
	Untracked []string // untracked files the user explicitly checked
	// Partial maps a tracked path to the hunk selection the user made in
	// the diff view — chosen[i]==true stages hunk i. Files here are
	// staged via `git apply --cached` of a partial patch instead of the
	// blanket `git add`, so unselected hunks stay unstaged.
	Partial  map[string][]bool
	Message  string
	Amend    bool
	NoVerify bool // -n: skip pre-commit/commit-msg hooks (IDEA's "Skip Git hooks")
}

// Commit performs a changelist-mode selective commit.
//
// Safety protocol (per the design notes):
//
//  1. Fresh snapshot; refuse on staged content, conflicts, in-progress
//     operations, or unborn corner cases the flow doesn't handle yet.
//  2. Stage only the requested paths, from the CURRENT worktree bytes.
//  3. Re-verify against the PRISTINE index owned by this request and
//     the实施 snapshotted pre-commit tree: nothing else slipped into
//     the index while staging.
//  4. Run plain git commit — hooks run, credentials stay git's own.
//  5. Report the ACTUAL resulting state; hooks may have altered files
//     or the index, so the UI re-snapshots after the commit.
func (r *Repo) Commit(ctx context.Context, req CommitRequest) (*CommitOutcome, error) {
	if len(req.Paths) == 0 && len(req.Untracked) == 0 {
		return nil, fmt.Errorf("commit: nothing selected")
	}
	if err := r.checkPreCommitState(ctx); err != nil {
		return nil, err
	}

	// Snapshot HEAD for the post-staging integrity check.
	beforeName, beforeOID, err := r.HeadResolution(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit: resolve HEAD: %w", err)
	}

	// Split the path list: partial-hunk files get a patch apply, the
	// rest a blanket add.
	var fullPaths []string
	for _, p := range req.Paths {
		if _, ok := req.Partial[p]; !ok {
			fullPaths = append(fullPaths, p)
		}
	}
	if len(req.Untracked) > 0 || len(fullPaths) > 0 {
		stageArgs := []string{"add"}
		for _, u := range req.Untracked {
			stageArgs = append(stageArgs, u)
		}
		stageArgs = append(stageArgs, "--")
		stageArgs = append(stageArgs, fullPaths...)
		if res, serr := r.RunOK(ctx, stageArgs...); serr != nil {
			return nil, fmt.Errorf("commit: stage selection: %w", serr)
		} else if !res.ExitOK() {
			return nil, &GitError{Args: stageArgs, Stderr: res.OutputOfStderr()}
		}
	}
	// Partial selections: stage only the chosen hunks of each file.
	for path, chosen := range req.Partial {
		if perr := r.StageHunks(ctx, path, chosen); perr != nil {
			return nil, fmt.Errorf("commit: stage hunks of %s: %w", path, perr)
		}
	}

	// Verify: index diff vs HEAD must mention exactly the staged set —
	// nothing else slipped into the index while staging.
	expected := map[string]bool{}
	for _, p := range req.Untracked {
		expected[p] = true
	}
	for _, p := range req.Paths {
		expected[p] = true
	}
	res2, err := r.RunOK(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, fmt.Errorf("commit: verify staged set: %w", err)
	}
	staged := strings.FieldsFunc(string(res2.Stdout), func(r rune) bool { return r == 0 })
	stagedSet := map[string]bool{}
	for _, p := range staged {
		if !expected[p] {
			return nil, fmt.Errorf("commit: unexpected path in index: %q", p)
		}
		stagedSet[p] = true
	}
	// Every selected path must have reached the index — a silent no-op
	// from a bad pathspec is worse than failing.
	for want := range expected {
		if !stagedSet[want] {
			return nil, fmt.Errorf("commit: selected path %q did not reach the index", want)
		}
	}

	// HEAD must not have moved during staging (concurrent app guard;
	// not a lock — this narrows the race, it cannot eliminate it).
	afterName, afterOID, err := r.HeadResolution(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit: re-resolve HEAD: %w", err)
	}
	if beforeOID != afterOID || beforeName != afterName {
		return nil, &ErrNotCleanState{What: "HEAD moved during staging"}
	}

	// The commit itself. Default runs hooks; -n opts out like
	// IDEA's "Skip Git hooks" checkbox.
	commitArgs := []string{"commit", "-m", req.Message}
	if req.NoVerify {
		commitArgs = append(commitArgs, "--no-verify")
	}
	if req.Amend {
		// Amend in changelist mode is only offered when the index held
		// no staged content before (we blocked that case above), so a
		// plain amend adds nothing extra.
		commitArgs = append(commitArgs, "--amend")
	}
	cRes, err := r.Run(ctx, commitArgs...)
	if err != nil {
		return nil, err
	}
	outcome := &CommitOutcome{
		Exit:    cRes.Exit,
		Output:  cRes.Output(),
		ExitOK:  cRes.ExitOK(),
		HookOut: cRes.OutputOfStderr(),
	}
	if !cRes.ExitOK() {
		outcome.Error = &GitError{Args: commitArgs, Stderr: cRes.OutputOfStderr()}
	}
	return outcome, nil
}

// CommitOutcome reports what actually happened, including when the
// commit command failed.
type CommitOutcome struct {
	ExitOK  bool
	Exit    int
	Output  string // stdout, e.g. the "[main abc123] ..." summary line
	HookOut string // stderr: hook messages, rejection reasons
	Error   error
}
