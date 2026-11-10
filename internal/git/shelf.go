package git

import (
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

// ShelvePaths writes a patch of the listed files' worktree changes to
// the shelf and reverts those files — IDEA's "Shelve Silently": the
// changes leave the worktree but stay recoverable.
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
	patchPath := filepath.Join(dir, safe+".patch")
	if err := r.CreatePatchFromWorktree(ctx, paths, patchPath); err != nil {
		return err
	}
	// Revert the worktree side for those paths — like stash, the shelf
	// stores a copy and the files go back to HEAD.
	if err := r.RevertPaths(ctx, paths); err != nil {
		// The patch is already saved; report but keep it.
		return fmt.Errorf("shelve: patch saved to %s but revert failed: %w", patchPath, err)
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
		}
	}
	return out, nil
}

// ShelfPatchDiff renders a stored patch as a DiffInfo for one file
// (the old side comes from HEAD, the new side applies the patch
// textually — for the simple case we show HEAD vs HEAD+patch applied
// to a temp worktree? Keep it simple: run `git apply --stat` is not a
// diff; callers use the raw patch text when they need the view).
func (r *Repo) ShelfPatchText(ctx context.Context, name string) ([]byte, error) {
	dir, err := r.shelfDir(ctx)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, name+".patch"))
}
