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
		// Path absent in the ref is a valid side too (untracked locally):
		// an empty old half, not an error the modal should show.
		if !isCatFileMissing(err) {
			return nil, fmt.Errorf("compare %s:%s: %w", ref, path, err)
		}
		old = nil
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

// isCatFileMissing reports a `cat-file`-style "object/path not found"
// error — the blob simply isn't there, not a real failure.
func isCatFileMissing(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not exist") ||
		strings.Contains(s, "bad object") ||
		strings.Contains(s, "invalid object name") ||
		strings.Contains(s, "bad revision") ||
		strings.Contains(s, "not found")
}

// CompareFiles lists the files that differ between ref and HEAD
// (IDEA: "Compare with Current" on a branch → its changed-files tree).
// Two-dot diff: the rows are the state differences between the two
// tips — the same pair selectFile diffs — so a file the row lists is
// guaranteed to show a diff. (A three-dot merge-base…HEAD list can
// name files whose ref:path and HEAD:path blobs are identical, which
// opened to an empty diff — the "loads then nothing" bug.)
func (r *Repo) CompareFiles(ctx context.Context, ref string) ([]CommitFile, error) {
	res, err := r.Run(ctx, "diff", "--name-status", "-z", ref, "HEAD", "--")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"diff", ref, "HEAD"}, Stderr: res.OutputOfStderr()}
	}
	return parseNameStatus(res.Stdout), nil
}

// CompareFilesWorktree lists files differing between ref and the
// working tree (`git diff ref` — tracked edits only; untracked files
// never appear in a ref↔worktree diff). path non-empty scopes the
// check to one file.
func (r *Repo) CompareFilesWorktree(ctx context.Context, ref, path string) ([]CommitFile, error) {
	args := []string{"diff", "--name-status", "-z", ref, "--"}
	if path != "" {
		args = append(args, path)
	}
	res, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"diff", ref}, Stderr: res.OutputOfStderr()}
	}
	return parseNameStatus(res.Stdout), nil
}

// CompareCommitsDiff returns the file-level diff of one path between
// two committed refs (IDEA: compare any two commits/branches). For a
// renamed file origPath names the pre-rename path — the old side must
// read fromRef:origPath or the blob lookup 404s into an "added file".
// A side whose blob is absent reads as empty (added/deleted file), not
// an error.
func (r *Repo) CompareCommitsDiff(ctx context.Context, fromRef, toRef, path, origPath string) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     path,
		OldLabel: fromRef,
		NewLabel: toRef,
	}
	oldPath := path
	if origPath != "" {
		oldPath = origPath
	}
	old, err := r.catFile(ctx, fromRef+":"+oldPath)
	if err != nil {
		if !isCatFileMissing(err) {
			return nil, err
		}
		old = nil
	}
	newB, err := r.catFile(ctx, toRef+":"+path)
	if err != nil {
		if !isCatFileMissing(err) {
			return nil, err
		}
		newB = nil
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
	Files     []CommitFile // the diverging side's diff (see FilesFrom/To)
	// FilesFrom/FilesTo name the endpoints Files was generated with —
	// selectFile must diff the same pair or a listed file opens blank.
	FilesFrom string
	FilesTo   string
	Unrelated bool // histories share no ancestor
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

	// File diff: the side that actually diverges. When only one side has
	// unique commits (fast-forward / fast-forwardable), the merge-base
	// diff on the *empty* side is empty and the panel reads "没有差异文件"
	// — IDEA still shows the divergence, which lives on the other side.
	switch {
	case out.Unrelated:
		// No base: diff the two tips directly.
		out.Files, err = r.compareTwoRefs(ctx, a, b)
		out.FilesFrom, out.FilesTo = a, b
	case len(out.AOnly) > 0 && len(out.BOnly) == 0:
		// Only "ours" moved ahead — show what merging *b* into *a*
		// would revert, i.e. base→a. IDEA surfaces the same direction.
		out.Files, err = r.compareTwoRefs(ctx, base, a)
		out.FilesFrom, out.FilesTo = base, a
	default:
		// BOnly non-empty or both: what merging b into a brings in.
		out.Files, err = r.compareTwoRefs(ctx, base, b)
		out.FilesFrom, out.FilesTo = base, b
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
	res, err := r.Run(ctx, "diff", "--name-status", "-z", from, to, "--")
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
	res, err := r.Run(ctx, "diff", "--name-only", ref, "--")
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
