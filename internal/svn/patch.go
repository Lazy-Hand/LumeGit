package svn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Patch applies a unified-diff file to the working copy — `svn patch`.
// dryRun maps to --dry-run; the caller passes false to actually apply.
// svn patch can't create commits — only worktree changes — so there's
// no svn counterpart to `git am`.
func (w *WC) Patch(ctx context.Context, patchFile string, dryRun bool) (*RunResult, error) {
	args := []string{"patch"}
	if dryRun {
		// No — a real apply; dryRun maps to --dry-run only when true.
		args = append(args, "--dry-run")
	}
	args = append(args, pathArg(patchFile))
	return w.Run(ctx, args...)
}

// PatchWorktree writes the worktree's local diffs for paths (nil = all)
// to a .patch in dest — `svn diff` output IS a unified patch.
func (w *WC) PatchWorktree(ctx context.Context, paths []string, dest string) (string, error) {
	args := []string{"diff"}
	for _, p := range paths {
		args = append(args, pathArg(p))
	}
	return w.writePatch(ctx, args, dest, "worktree")
}

// PatchRev writes one revision's changes as a patch — `svn diff -c N`.
func (w *WC) PatchRev(ctx context.Context, rev int64, dest string) (string, error) {
	args := []string{"diff", "-c", itoa(rev)}
	return w.writePatch(ctx, args, dest, fmt.Sprintf("r%d", rev))
}

// writePatch runs the diff args, writes stdout to a timestamped .patch
// in dest, and returns that file's path.
func (w *WC) writePatch(ctx context.Context, args []string, dest, tag string) (string, error) {
	res, err := w.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		return "", &Error{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	name := "patch-" + tag + "-" + time.Now().Format("0102-150405") + ".patch"
	out := filepath.Join(dest, name)
	if err := os.WriteFile(out, res.Stdout, 0o644); err != nil {
		return "", err
	}
	return out, nil
}
