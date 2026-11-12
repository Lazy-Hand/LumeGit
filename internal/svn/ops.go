package svn

import (
	"context"
	"strconv"
)

// Add schedules a path for addition (--parents to pull in missing dirs).
func (w *WC) Add(ctx context.Context, path string, parents bool) (*RunResult, error) {
	args := []string{"add"}
	if parents {
		args = append(args, "--parents")
	}
	return w.RunOK(ctx, append(args, "--", pathArg(path))...)
}

// Delete schedules a path for deletion; keepLocal leaves the file on disk.
func (w *WC) Delete(ctx context.Context, path string, keepLocal bool) (*RunResult, error) {
	args := []string{"delete"}
	if keepLocal {
		args = append(args, "--keep-local")
	}
	return w.RunOK(ctx, append(args, "--", pathArg(path))...)
}

// Revert restores a path to BASE; recursive covers dirs.
func (w *WC) Revert(ctx context.Context, path string, recursive bool) (*RunResult, error) {
	args := []string{"revert"}
	if recursive {
		args = append(args, "--depth", "infinity")
	}
	return w.RunOK(ctx, append(args, "--", pathArg(path))...)
}

// Resolve marks a conflict settled (--accept answer). AcceptPostpone
// is not valid here — resolve needs a concrete pick.
func (w *WC) Resolve(ctx context.Context, path string, acc Accept) (*RunResult, error) {
	if acc == "" || acc == AcceptPostpone {
		acc = AcceptWorking
	}
	return w.RunOK(ctx, "resolve", "--accept", string(acc), "--", pathArg(path))
}

// Lock takes a server-side write lock on a path.
func (w *WC) Lock(ctx context.Context, path, msg string, force bool) (*RunResult, error) {
	args := []string{"lock"}
	if msg != "" {
		args = append(args, "-m", msg)
	}
	if force {
		args = append(args, "--force")
	}
	return w.RunOK(ctx, append(args, "--", pathArg(path))...)
}

// Unlock releases a lock.
func (w *WC) Unlock(ctx context.Context, path string, force bool) (*RunResult, error) {
	args := []string{"unlock"}
	if force {
		args = append(args, "--force")
	}
	return w.RunOK(ctx, append(args, "--", pathArg(path))...)
}

// Cleanup fixes a locked/interrupted working copy. removeUnversioned
// and removeIgnored map to git-clean semantics.
func (w *WC) Cleanup(ctx context.Context, removeUnversioned, removeIgnored bool) (*RunResult, error) {
	args := []string{"cleanup"}
	if removeUnversioned {
		args = append(args, "--remove-unversioned")
	}
	if removeIgnored {
		args = append(args, "--remove-ignored")
	}
	return w.RunOK(ctx, args...)
}

// Relocate repoints the WC at a new repository URL after a server move.
func (w *WC) Relocate(ctx context.Context, from, to string) (*RunResult, error) {
	return w.RunOK(ctx, "relocate", from, to)
}

// Checkout fetches a new working copy (not a method on WC — the WC
// doesn't exist yet). depth "" = infinity.
func Checkout(ctx context.Context, url, dir, rev, depth string) (*RunResult, error) {
	args := []string{"checkout"}
	if rev != "" {
		args = append(args, "-r", rev)
	}
	if depth != "" && depth != "infinity" {
		args = append(args, "--depth", depth)
	}
	args = append(args, pathArg(url), dir)
	return NewWC(dir).RunOK(ctx, args...)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
