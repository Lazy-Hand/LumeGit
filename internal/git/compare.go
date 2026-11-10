package git

import (
	"context"
	"fmt"
	"strings"
)

// CompareFileDiff diffs one path between an arbitrary ref and the
// worktree (IDEA: "Compare with Local" / "Compare with Branch"). ref
// may be a branch, tag or commit; the old side is the ref's blob.
func (r *Repo) CompareFileDiff(ctx context.Context, ref, path string) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     path,
		OldLabel: ref,
		NewLabel: "worktree",
	}
	old, err := r.catFile(ctx, ref+":"+path)
	if err != nil {
		return nil, fmt.Errorf("compare %s:%s: %w", ref, path, err)
	}
	info.Old = old
	wt, err := readWorktreeFile(ctx, r, path)
	if err != nil {
		// File absent in the worktree is a valid side (deleted locally).
		wt = nil
	}
	info.New = wt
	if info.Binary = looksBinary(old) || looksBinary(wt); info.Binary {
		return info, nil
	}
	return info, nil
}

// CompareFiles lists the files that differ between ref and HEAD
// (IDEA: "Compare with Current" on a branch → its changed-files tree).
// Returns name-status pairs, same shape as CommitFiles.
func (r *Repo) CompareFiles(ctx context.Context, ref string) ([]CommitFile, error) {
	res, err := r.Run(ctx, "diff", "--name-status", "-z", ref+"...HEAD")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		// Fall back to two-dot when there is no merge base
		// (unrelated histories).
		res, err = r.Run(ctx, "diff", "--name-status", "-z", ref, "HEAD")
		if err != nil {
			return nil, err
		}
		if !res.ExitOK() {
			return nil, &GitError{Args: []string{"diff", ref + "...HEAD"}, Stderr: res.OutputOfStderr()}
		}
	}
	return parseNameStatus(res.Stdout), nil
}

// CompareCommitsDiff returns the file-level diff of one path between
// two committed refs (IDEA: compare any two commits/branches).
func (r *Repo) CompareCommitsDiff(ctx context.Context, fromRef, toRef, path string) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     path,
		OldLabel: fromRef,
		NewLabel: toRef,
	}
	old, err := r.catFile(ctx, fromRef+":"+path)
	if err != nil {
		return nil, err
	}
	newB, err := r.catFile(ctx, toRef+":"+path)
	if err != nil {
		return nil, err
	}
	info.Old, info.New = old, newB
	info.Binary = looksBinary(old) || looksBinary(newB)
	return info, nil
}

// BranchCompare is IDEA's "Compare with Branch…" modal: for a target
// ref it shows the commits each side has that the other doesn't
// (a..b on the right, b..a on the left) plus the file-level diff of
// merge-base...ref so the user sees what merging would bring in.
type BranchCompare struct {
	Base      string       // merge-base OID (empty when unrelated)
	AOnly     []Commit     // commits on a only — "ours" side
	BOnly     []Commit     // commits on b only — "theirs" side
	Files     []CommitFile // merge-base..ref diff (what merging b into a would change)
	Unrelated bool         // histories share no ancestor
}

// CompareBranches computes the two-sided comparison of branch a vs b.
// a may be "HEAD"; both may be any ref.
func (r *Repo) CompareBranches(ctx context.Context, a, b string) (*BranchCompare, error) {
	out := &BranchCompare{}

	// Merge-base for the file list (three-dot) and to tell unrelated
	// histories apart.
	base, err := r.mergeBase(ctx, a, b)
	if err != nil {
		return nil, err
	}
	out.Base = base
	out.Unrelated = base == ""

	// Unique commits on each side.
	if out.AOnly, err = r.logRange(ctx, b+".."+a, 100); err != nil {
		return nil, err
	}
	if out.BOnly, err = r.logRange(ctx, a+".."+b, 100); err != nil {
		return nil, err
	}

	// File diff: merge-base..b — what merging would bring in.
	if out.Unrelated {
		// No base: diff the two tips directly.
		out.Files, err = r.compareTwoRefs(ctx, a, b)
	} else {
		out.Files, err = r.compareTwoRefs(ctx, base, b)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mergeBase returns the common ancestor, "" when unrelated.
func (r *Repo) mergeBase(ctx context.Context, a, b string) (string, error) {
	res, err := r.Run(ctx, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		// exit 1 with no output = no common ancestor.
		return "", nil
	}
	return strings.TrimSpace(res.Output()), nil
}

// logRange wraps LogFiltered for a rev range.
func (r *Repo) logRange(ctx context.Context, rng string, max int) ([]Commit, error) {
	return r.LogFiltered(ctx, LogOptions{Max: max, Branch: rng})
}

// compareTwoRefs returns the name-status diff from→to (NUL-separated,
// matching parseNameStatus).
func (r *Repo) compareTwoRefs(ctx context.Context, from, to string) ([]CommitFile, error) {
	res, err := r.Run(ctx, "diff", "--name-status", "-z", from, to)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"diff", from, to}, Stderr: res.OutputOfStderr()}
	}
	return parseNameStatus(res.Stdout), nil
}

// ChangedBetween lists paths changed ref→worktree (uncommitted), i.e.
// "what would comparing show" for a whole-file scope.
func (r *Repo) ChangedBetween(ctx context.Context, ref string) ([]string, error) {
	res, err := r.Run(ctx, "diff", "--name-only", ref)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"diff", "--name-only", ref}, Stderr: res.OutputOfStderr()}
	}
	var out []string
	for _, ln := range strings.Split(res.Output(), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out, nil
}
