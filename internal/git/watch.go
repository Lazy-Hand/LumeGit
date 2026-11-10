package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch runs a filesystem watch on the worktree until ctx is cancelled.
// onChange fires (on the watcher's own goroutine) after a quiet period
// once worktree files or the index change — the caller re-snapshots.
//
// What we watch and why:
//
//   - worktree/** catches edits, creates, deletes, renames, chmod
//   - .git/index catches stage/unstage (git rewrites the whole index
//     file; watching the file itself, not the directory, skips object
//     writes under .git/objects)
//   - .git/HEAD and .git/MERGE_HEAD/etc catch branch switches and
//     sequencer state (MERGE_HEAD appearing means a merge started)
//
// Debounce: one save generates a burst of events (create temp, rename,
// chmod). We wait 200 ms of quiet before firing — enough to collapse
// an editor's atomic save or a `git checkout` into one refresh.
func (r *Repo) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		w.Close()
		return err
	}
	// Watch the worktree recursively, .git/index itself, and the
	// control files that mark repo state. Watching the whole .git would
	// flood us with object/pack writes we don't care about.
	watchDirs := map[string]bool{}
	add := func(p string) {
		if watchDirs[p] {
			return
		}
		watchDirs[p] = true
		_ = w.Add(p) // errors ignored: a vanished dir is a no-op
	}
	// IDEA watches the git metadata (index/HEAD/refs/sequencer markers)
	// plus the worktree *root only* — it does not recurse into every
	// subdirectory. Recursion was the fd-eater: a repo with node_modules/
	// build/ target/ has thousands of dirs, each costing one fsnotify fd
	// and easily exhausting the 256-fd macOS soft limit. Top-level files
	// still fire (root add covers them); deeper edits surface on the
	// next git status check triggered by .git activity or manual refresh.
	add(r.Worktree)
	// Index + HEAD + sequencer markers: one file each, not the dir.
	for _, name := range []string{
		"index", "HEAD", "MERGE_HEAD", "CHERRY_PICK_HEAD",
		"REVERT_HEAD", "ORIG_HEAD", "FETCH_HEAD",
	} {
		p := filepath.Join(gitDir, name)
		if fileExists(p) {
			add(p)
		}
	}
	// Also watch .git/refs (but only its top level — refs/heads, refs/tags,
	// refs/remotes; deeper would again multiply fds).
	refsDir := filepath.Join(gitDir, "refs")
	if fileExists(refsDir) {
		add(refsDir)
		for _, sub := range []string{"heads", "tags", "remotes"} {
			p := filepath.Join(refsDir, sub)
			if fileExists(p) {
				add(p)
			}
		}
	}

	go func() {
		defer w.Close()
		const quiet = 200 * time.Millisecond
		timer := time.NewTimer(quiet)
		if !timer.Stop() {
			<-timer.C
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
				// Watcher errors are non-fatal; keep watching.
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if !watchRelevant(ev, r.Worktree, gitDir) {
					continue
				}
				// Reset the quiet timer — a burst counts once.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(quiet)
			case <-timer.C:
				onChange()
			}
		}
	}()
	return nil
}

// watchRelevant reports whether one fsnotify event should count toward
// a refresh. We skip noise under .git/objects (pack writes, object
// files) and editor swap/backup files in the worktree.
func watchRelevant(ev fsnotify.Event, worktree, gitDir string) bool {
	// Chmod-only events are metadata noise (Spotlight, AV scans).
	if ev.Op&^(fsnotify.Chmod) == 0 {
		return false
	}
	name := ev.Name
	// .git/objects is the bulk of index noise: any object write during
	// a commit triggers a burst we don't care about (the index event
	// itself covers the change).
	if strings.HasPrefix(name, gitDir+string(os.PathSeparator)) {
		rel := name[len(gitDir)+1:]
		// Relevant: index, HEAD, refs, sequencer markers. Not objects,
		// logs, packed-refs churn, lock files.
		switch {
		case strings.HasPrefix(rel, "objects"+string(os.PathSeparator)):
			return false
		case strings.HasPrefix(rel, "logs"+string(os.PathSeparator)):
			return false
		case strings.HasSuffix(rel, ".lock"):
			// A lock write means an in-progress update; the follow-up
			// rename lands the real change, which we will catch.
			return false
		case rel == "index" || rel == "HEAD" || rel == "MERGE_HEAD" ||
			rel == "CHERRY_PICK_HEAD" || rel == "REVERT_HEAD" ||
			rel == "ORIG_HEAD" || rel == "FETCH_HEAD" ||
			strings.HasPrefix(rel, "refs"+string(os.PathSeparator)):
			return true
		default:
			return false
		}
	}
	// Worktree side: skip swap/backup names editors use.
	base := filepath.Base(name)
	switch {
	case strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".swp"),
		strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".swx"),
		strings.HasSuffix(base, "~"),
		strings.HasPrefix(base, ".#"),
		strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#"):
		return false
	}
	return strings.HasPrefix(name, worktree+string(os.PathSeparator))
}

// gitDir returns the absolute .git dir for the repo (per-worktree for
// linked worktrees).
func (r *Repo) gitDir(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Output()), nil
}
