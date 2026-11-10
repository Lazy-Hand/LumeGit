package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Hunk is one changed region of a file diff, in unified-diff terms.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Header             string // full "@@ ... @@" line (with function context)
	Lines              []byte // the hunk body: ' ', '-', '+' prefixed lines
}

// FileHunks splits a worktree-vs-index diff for path into hunks —
// the granularity IDEA's per-chunk checkboxes operate on.
// Returns nil for new/deleted/binary files (whole-file only).
func (r *Repo) FileHunks(ctx context.Context, path string) ([]Hunk, error) {
	res, err := r.Run(ctx, "diff", "--no-color", "-U3", "--", path)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"diff", "--", path}, Stderr: res.OutputOfStderr()}
	}
	return parseHunks(res.Stdout), nil
}

// parseHunks scans a unified diff for one file and returns its hunks.
// Header/file lines are skipped; everything from "@@" to the next "@@"
// or EOF is one hunk's body.
func parseHunks(out []byte) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		ln := sc.Bytes()
		switch {
		case bytes.HasPrefix(ln, []byte("@@")):
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			cur = &Hunk{Header: string(ln)}
			fmt.Sscanf(string(ln), "@@ -%d,%d +%d,%d @@",
				&cur.OldStart, &cur.OldCount, &cur.NewStart, &cur.NewCount)
			// Single-line forms "-5 +8" carry no count.
			if !strings.Contains(string(ln), ",") {
				var o, n int
				fmt.Sscanf(string(ln), "@@ -%d +%d @@", &o, &n)
				cur.OldStart, cur.OldCount = o, 1
				cur.NewStart, cur.NewCount = n, 1
			}
		case cur != nil:
			cur.Lines = append(append(cur.Lines, ln...), '\n')
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// SelectedPatch builds a `git apply --cached`-ready patch covering
// only the selected hunks of path. unselected are left out entirely;
// git recomputes offsets when applying, so header counts stay as-is.
func SelectedPatch(path string, old []byte, hunks []Hunk, chosen []bool) ([]byte, error) {
	var body bytes.Buffer
	body.WriteString("diff --git a/" + path + " b/" + path + "\n")
	body.WriteString("--- a/" + path + "\n")
	body.WriteString("+++ b/" + path + "\n")
	n := 0
	for i, h := range hunks {
		if i >= len(chosen) || !chosen[i] {
			continue
		}
		// Recompute @@ counts to reflect only this hunk. The new-file
		// line numbers shift by lines already applied in earlier chosen
		// hunks; with apply --cached's fuzz the raw numbers are fine
		// for non-adjacent selections, but exact counts keep context
		// matching honest.
		body.WriteString(h.Header)
		body.WriteString("\n")
		body.Write(h.Lines)
		n++
	}
	if n == 0 {
		return nil, fmt.Errorf("no hunks selected")
	}
	return body.Bytes(), nil
}

// ApplyToIndex applies a patch to the index only (git apply --cached).
// The worktree is untouched: staged set gains exactly the patch's lines.
func (r *Repo) ApplyToIndex(ctx context.Context, patch []byte) error {
	res, err := r.RunStdin(ctx, patch, "apply", "--cached", "--whitespace=nowarn", "-")
	if err != nil {
		return err
	}
	if !res.ExitOK() {
		return &GitError{Args: []string{"apply", "--cached"}, Stderr: res.OutputOfStderr()}
	}
	return nil
}

// StageHunks stages only the chosen hunks of path: apply the partial
// patch to the index, leaving the worktree as-is. After it, `git diff`
// (index→worktree) shows exactly the unchosen hunks.
func (r *Repo) StageHunks(ctx context.Context, path string, chosen []bool) error {
	hunks, err := r.FileHunks(ctx, path)
	if err != nil {
		return err
	}
	if len(hunks) == 0 {
		return fmt.Errorf("%s: no hunks (new/deleted/binary file?)", path)
	}
	patch, err := SelectedPatch(path, nil, hunks, chosen)
	if err != nil {
		return err
	}
	return r.ApplyToIndex(ctx, patch)
}

// hunkID is a stable string form for UI bookkeeping.
func (h Hunk) ID() string {
	return strconv.Itoa(h.OldStart) + ":" + strconv.Itoa(h.NewStart)
}
