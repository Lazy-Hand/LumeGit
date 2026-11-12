package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ShelfEntry is one stored patch on the shelf (IDEA's Shelf: patches
// kept outside the git object store, re-applied on demand).
type ShelfEntry struct {
	Name    string    // display name, doubles as the patch file's base
	Path    string    // absolute patch file
	Created time.Time // file mtime
	Paths   []string  // files the patch touches (parsed lazily)
}

// shelfDir is the per-repo shelf location: inside .git so it follows
// the repo and is never committed.
func (r *Repo) shelfDir(ctx context.Context) (string, error) {
	gd, err := r.gitDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(gd, "lumegit-shelf"), nil
}

// Shelf lists the stored patches, newest first.
func (r *Repo) Shelf(ctx context.Context) ([]ShelfEntry, error) {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ShelfEntry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, _ := os.Stat(p)
		se := ShelfEntry{
			Name: strings.TrimSuffix(e.Name(), ".patch"),
			Path: p,
		}
		if info != nil {
			se.Created = info.ModTime()
		}
		out = append(out, se)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// ShelvePaths writes a patch of the listed files' changes to the shelf
// and reverts those files — IDEA's "Shelve Silently": the changes leave
// the worktree but stay recoverable.
//
// Three kinds of path need three diff+revert pairs:
//   - staged-only edits diff via `git diff --cached` and roll back with
//     `restore --staged --worktree` (worktree content = index content);
//   - unstaged/tracked edits diff via `git diff` and roll back with
//     plain `restore`;
//   - untracked files diff via `git diff --no-index /dev/null` and are
//     deleted — apply-side `git apply` recreates them.
// All diffs concatenate into one patch file; `git apply` replays every
// hunk it contains regardless of which command produced it.
func (r *Repo) ShelvePaths(ctx context.Context, name string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("shelve: no paths")
	}
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	safe := strings.Map(func(rn rune) rune {
		switch {
		case rn >= 'a' && rn <= 'z', rn >= 'A' && rn <= 'Z', rn >= '0' && rn <= '9',
			rn == '-', rn == '_', rn == '.':
			return rn
		}
		return '_'
	}, name)
	if safe == "" {
		safe = fmt.Sprintf("shelf-%d", time.Now().Unix())
	}
	// Never clobber an existing entry: the same-second name collision
	// used to overwrite the earlier patch while its worktree changes
	// had already been reverted — silent data loss. Suffix -2, -3…
	patchPath := filepath.Join(dir, safe+".patch")
	for i := 2; ; i++ {
		if _, err := os.Stat(patchPath); os.IsNotExist(err) {
			break
		} else if err != nil {
			return err
		}
		patchPath = filepath.Join(dir, fmt.Sprintf("%s-%d.patch", safe, i))
	}

	// Split paths by which tree holds their change. Stale staged info is
	// fine here — the revert half just no-ops on a path that turned out
	// not to be staged after all.
	snap, err := r.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("shelve: status: %w", err)
	}
	kind := map[string]FileChange{}
	for i := range snap.Files {
		f := snap.Files[i]
		kind[f.Path] = f
	}
	var stagedOnly, worktree, untracked []string
	for _, p := range paths {
		f, ok := kind[p]
		switch {
		case ok && f.Kind == KindUntracked:
			untracked = append(untracked, p)
		case ok && f.Staged() && !f.Unstaged():
			stagedOnly = append(stagedOnly, p)
		default:
			worktree = append(worktree, p)
		}
	}

	// Collect the patch text for each bucket.
	var patch bytes.Buffer
	write := func(res *RunResult, err error, what string) error {
		if err != nil {
			return fmt.Errorf("shelve: %s: %w", what, err)
		}
		if !res.ExitOK() {
			return &GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
		}
		patch.Write(res.Stdout)
		return nil
	}
	if len(worktree) > 0 {
		args := append([]string{"diff", "--binary", "--no-color", "--"}, literalPathspecs(worktree)...)
		res, err := r.Run(ctx, args...)
		if err := write(res, err, "diff worktree"); err != nil {
			return err
		}
	}
	if len(stagedOnly) > 0 {
		args := append([]string{"diff", "--cached", "--binary", "--no-color", "--"}, literalPathspecs(stagedOnly)...)
		res, err := r.Run(ctx, args...)
		if err := write(res, err, "diff index"); err != nil {
			return err
		}
	}
	for _, p := range untracked {
		// --no-index exits 1 on a difference — that's the file's content,
		// not a failure. Passing the repo-relative path keeps headers in
		// the a/p b/p form git apply expects.
		res, err := r.Run(ctx, "diff", "--no-index", "--binary", "--no-color", "--", "/dev/null", p)
		if err != nil {
			return fmt.Errorf("shelve: diff untracked %s: %w", p, err)
		}
		if res.Exit > 1 {
			return &GitError{Args: res.Args, Stderr: res.OutputOfStderr()}
		}
		patch.Write(res.Stdout)
	}
	if len(bytes.TrimSpace(patch.Bytes())) == 0 {
		return fmt.Errorf("没有可搁置的改动")
	}
	if err := os.WriteFile(patchPath, patch.Bytes(), 0o644); err != nil {
		return err
	}

	// Revert each bucket the way its diff was taken.
	if len(worktree) > 0 {
		if err := r.RevertPaths(ctx, worktree); err != nil {
			return fmt.Errorf("shelve: patch saved to %s but revert failed: %w", patchPath, err)
		}
	}
	if len(stagedOnly) > 0 {
		if err := r.RevertStagedPaths(ctx, stagedOnly); err != nil {
			return fmt.Errorf("shelve: patch saved to %s but revert failed: %w", patchPath, err)
		}
	}
	for _, p := range untracked {
		if err := os.Remove(r.Worktree + "/" + p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("shelve: patch saved to %s but delete %s failed: %w", patchPath, p, err)
		}
	}
	return nil
}

// Unshelve applies a stored patch back to the worktree.
func (r *Repo) Unshelve(ctx context.Context, name string) (*RunResult, error) {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return nil, err
	}
	return r.RunOK(ctx, "apply", "--whitespace=nowarn", filepath.Join(dir, name+".patch"))
}

// DropShelf deletes a shelf patch.
func (r *Repo) DropShelf(ctx context.Context, name string) error {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, name+".patch"))
}

// ShelfPatchFiles parses which paths a shelf patch touches.
func (r *Repo) ShelfPatchFiles(ctx context.Context, name string) ([]string, error) {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".patch"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "+++ b/") {
			out = append(out, strings.TrimPrefix(ln, "+++ b/"))
			continue
		}
		// Deleted files show "+++ /dev/null" — take the old name from
		// "--- a/<path>" so the shelf row still lists them; binary
		// patches keep the same header shape so they're covered too.
		if strings.HasPrefix(ln, "+++ /dev/null") {
			continue
		}
		if strings.HasPrefix(ln, "--- a/") {
			out = append(out, strings.TrimPrefix(ln, "--- a/"))
		}
	}
	return dedupStrings(out), nil
}

// ShelfPatchDiff renders a stored patch as a DiffInfo for one file
// (the old side comes from HEAD, the new side applies the patch
// textually — for the simple case we show HEAD vs HEAD+patch applied
// to a temp worktree? Keep it simple: run `git apply --stat` is not a
// diff; callers use the raw patch text when they need the view).
func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (r *Repo) ShelfPatchText(ctx context.Context, name string) ([]byte, error) {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, name+".patch"))
}
