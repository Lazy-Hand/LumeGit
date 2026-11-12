package vcs

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"lumegit/internal/git"
	"lumegit/internal/svn"
)

// Snapshot is the UI's view of the working copy — git's three-tree
// status or svn's two-side (BASE↔worktree) one, flattened into the
// same row set the commit pane renders.
//
// For git this is a thin wrap over git.Snapshot: the index/worktree
// split is real. For svn it comes from `svn status`: TextStatus plays
// the worktree-side role and the "index" side is always ' ' because
// SVN has no staging area — every changed file is one unit the commit
// button either includes or doesn't.
type Snapshot struct {
	Kind Kind
	Root string

	// Head label for the title: git's branch name, svn's URL tail
	// ("trunk"/"branches/foo"/revision).
	Head   string
	Rev    string // display rev: git short-OID or svn "r123"
	Detail string // ahead/behind or out-of-date summary

	// Files is the changed-file list the commit pane renders.
	Files []FileChange

	// Git is the raw git snapshot when Kind==KindGit — git-only state
	// (index split, detached HEAD, in-progress op, upstream counts)
	// stays available to views that use it; nil on svn.
	Git *git.Snapshot
}

// FileChange is one row in the commit pane. It embeds git.FileChange so
// the shared git/vcs views keep working unchanged — git rows fill it
// verbatim, svn rows put the single status letter in WorktreeState and
// carry an svn-only Kind in the extended FileKind range.
type FileChange = git.FileChange

// FileKind is git.FileKind widened with the SVN-specific kinds. Being
// an alias (not a new type) means every helper that takes git.FileKind
// — statusGlyph, statusColor, kindRank — keeps compiling for both
// backends; the svn-only constants sit above git's range.
type FileKind = git.FileKind

const (
	KindModified   = git.KindModified
	KindAdded      = git.KindAdded
	KindDeleted    = git.KindDeleted
	KindRenamed    = git.KindRenamed
	KindTypeChange = git.KindTypechange
	KindUntracked  = git.KindUntracked
	KindUnmerged   = git.KindUnmerged

	// SVN-only kinds start above git's range.
	KindMissing    FileKind = 100 + iota // svn !
	KindObstructed                       // svn ~
	KindReplaced                         // svn R
	KindExternal                         // svn X
	KindIgnored                          // svn I
	KindOutOfDate                        // svn --show-updates server-side flag
	KindLocked                           // svn K/L
	KindNormal                           // no change (svn status rows carry one anyway)
)

// Find returns the change record for path, or nil — same shape as
// git.Snapshot.Find so callers don't branch on Kind.
func (s *Snapshot) Find(path string) *FileChange {
	for i := range s.Files {
		if s.Files[i].Path == path {
			return &s.Files[i]
		}
	}
	return nil
}

// Staged: git's index-side changes. SVN has no index — always empty.
func (s *Snapshot) Staged() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Staged() {
			out = append(out, f)
		}
	}
	return out
}

// Unstaged: worktree-side changes; on svn this is the whole list.
func (s *Snapshot) Unstaged() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Unstaged() {
			out = append(out, f)
		}
	}
	return out
}

// Conflicts: git unmerged entries or svn's C/tree-conflict rows.
func (s *Snapshot) Conflicts() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Conflict() {
			out = append(out, f)
		}
	}
	return out
}

// Snapshot reads the working copy's current state through the backend.
func (b *Backend) Snapshot(ctx context.Context) (*Snapshot, error) {
	switch b.Kind {
	case KindGit:
		s, err := b.Git.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		return gitSnapshot(s), nil
	case KindSVN:
		// One status walk serves both lists — Status+Ignored each ran
		// their own `svn status` before.
		items, ignored, err := b.SVN.StatusAll(ctx, false)
		if err != nil {
			return nil, err
		}
		info, _ := b.SVN.Info(ctx)
		return svnSnapshot(b.Root, items, ignored, info), nil
	}
	return nil, fmt.Errorf("unsupported vcs kind %q", b.Kind)
}

// WrapGit adapts a raw git.Snapshot — the many call sites that run a
// git op and then re-snapshot get the shared UI shape for free. Nil-safe.
func WrapGit(s *git.Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	return gitSnapshot(s)
}

func gitSnapshot(s *git.Snapshot) *Snapshot {
	out := &Snapshot{
		Kind:   KindGit,
		Root:   s.Worktree,
		Head:   s.HeadName,
		Rev:    shortOID(s.HeadOID),
		Detail: aheadBehind(s.Ahead, s.Behind),
		Git:    s,
	}
	out.Files = append(out.Files, s.Files...)
	return out
}

// isDirAt reports whether the WC-relative path is a directory on disk.
// A deleted/missing dir fails the stat and stays in the list.
func isDirAt(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}

func svnSnapshot(root string, items []svn.Item, ignored []svn.Item, info *svn.Info) *Snapshot {
	out := &Snapshot{Kind: KindSVN, Root: root}
	if info != nil {
		out.Head = headLabel(info.RelativeURL, info.URL)
		out.Rev = fmt.Sprintf("r%d", info.Revision)
	}
	var ood int
	for _, it := range items {
		if it.OOD {
			ood++
		}
		// Directories whose only change is a property (svn:mergeinfo,
		// svn:ignore…) have no text diff but ARE the committable unit —
		// dropping them left svn:ignore/mergeinfo with no UI row, so the
		// change could never be selected or committed. Keep the row.
		if it.TextStatus == ' ' && it.PropStatus == ' ' &&
			isDirAt(root, it.Path) {
			continue
		}
		fk := svnKind(it.Kind())
		ws := byte(' ')
		if fk != KindNormal {
			ws = byte(it.TextStatus)
		}
		out.Files = append(out.Files, FileChange{
			Path:          it.Path,
			Kind:          fk,
			IndexState:    ' ',
			WorktreeState: ws,
		})
	}
	// Ignored paths show at the bottom as their own rows — the "已忽略"
	// group. They aren't committable; the UI filters them out of commit
	// scope but keeps them visible so 加入忽略 的效果可见。
	for _, it := range ignored {
		if !isDirAt(root, it.Path) {
			out.Files = append(out.Files, FileChange{
				Path:          it.Path,
				Kind:          KindIgnored,
				IndexState:    ' ',
				WorktreeState: 'I',
			})
		}
	}
	if ood > 0 {
		out.Detail = fmt.Sprintf("%d 个远程更新待取", ood)
	}
	return out
}

func headLabel(rel, full string) string {
	label := ""
	if rel != "" {
		// "^/branches/foo" → "branches/foo"
		label = strings.TrimPrefix(rel, "^/")
	} else {
		// Fall back to the URL tail — "…/trunk" → "trunk".
		label = full
		for i := len(full) - 1; i >= 0; i-- {
			if full[i] == '/' {
				label = full[i+1:]
				break
			}
		}
	}
	// svn percent-encodes non-ASCII in URL fields — decode for display.
	if dec, err := url.PathUnescape(label); err == nil {
		label = dec
	}
	return label
}

func shortOID(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

func aheadBehind(a, b int) string {
	switch {
	case a > 0 && b > 0:
		return fmt.Sprintf("↑%d ↓%d", a, b)
	case a > 0:
		return fmt.Sprintf("↑%d", a)
	case b > 0:
		return fmt.Sprintf("↓%d", b)
	}
	return ""
}

func svnKind(k svn.Kind) git.FileKind {
	switch k {
	case svn.KindModified:
		return KindModified
	case svn.KindAdded:
		return KindAdded
	case svn.KindDeleted:
		return KindDeleted
	case svn.KindMissing:
		return KindMissing
	case svn.KindConflicted, svn.KindTreeConflicted:
		return KindUnmerged
	case svn.KindUnversioned:
		return KindUntracked
	case svn.KindObstructed:
		return KindObstructed
	case svn.KindReplaced:
		return KindReplaced
	case svn.KindExternal:
		return KindExternal
	case svn.KindIgnored:
		return KindIgnored
	case svn.KindOutOfDate:
		return KindOutOfDate
	case svn.KindLocked:
		return KindLocked
	}
	return KindModified
}
