// Package components holds the reusable display pieces shared by the
// app's panes: status glyphs/colors, time and path formatting, and the
// small stateless row helpers. Nothing in here may depend on the main
// package's app or model types — that is what keeps it reusable.
package components

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lobehub/lobe-mygo/pkg/display"

	"lumegit/internal/git"
	"lumegit/internal/vcs"
)

// StatusTagColor maps file status to a Tag color.
func StatusTagColor(k git.FileKind) display.TagColor {
	switch k {
	case git.KindUnmerged, git.KindDeleted:
		return display.TagError
	case git.KindUntracked, git.KindAdded:
		return display.TagSuccess
	case git.KindRenamed:
		return display.TagInfo
	case git.KindModified, git.KindTypechange:
		return display.TagWarning
	default:
		return display.TagGray
	}
}

// StatusGlyph uses IDEA's per-type letters.
func StatusGlyph(k git.FileKind) string {
	switch k {
	case git.KindModified:
		return "M"
	case git.KindAdded:
		return "A"
	case git.KindDeleted:
		return "D"
	case git.KindRenamed:
		return "R"
	case git.KindTypechange:
		return "T"
	case git.KindUntracked:
		return "U"
	case git.KindUnmerged:
		return "C"
	// svn-only kinds (same enum, extended range in vcs)
	case vcs.KindMissing:
		return "!"
	case vcs.KindObstructed:
		return "~"
	case vcs.KindReplaced:
		return "R"
	case vcs.KindExternal:
		return "X"
	case vcs.KindIgnored:
		return "I"
	case vcs.KindOutOfDate:
		return "*"
	case vcs.KindLocked:
		return "L"
	}
	return "?"
}

// StashFileKind maps a stash entry's status letter to a FileKind so the
// same tag/glyph helpers can render it.
func StashFileKind(f *git.StashFile) git.FileKind {
	if f.Untracked {
		return git.KindUntracked
	}
	switch f.Status {
	case 'A':
		return git.KindAdded
	case 'D':
		return git.KindDeleted
	case 'R':
		return git.KindRenamed
	case 'T':
		return git.KindTypechange
	}
	return git.KindModified
}

// ShortOID abbreviates a full hash for display.
func ShortOID(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// RelTime formats a commit time like IDEA's log ("5 分钟前", "昨天").
func RelTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return t.Format("1月2日")
	}
	return t.Format("2006-01-02")
}

// AheadBehind formats "+2 −1"; empty when neither is known.
func AheadBehind(ahead, behind int) string {
	if ahead < 0 && behind < 0 {
		return ""
	}
	var parts []string
	if ahead > 0 {
		parts = append(parts, "+"+fmt.Sprint(ahead))
	}
	if behind > 0 {
		parts = append(parts, "−"+fmt.Sprint(behind))
	}
	return strings.Join(parts, " ")
}

// CheckedCount renders "n of total" over the snapshot, skipping
// conflicts and ignored rows; the predicate says whether a path counts.
func CheckedCount(snap *vcs.Snapshot, isChecked func(path string) bool) string {
	if snap == nil {
		return "0 of 0"
	}
	total, n := 0, 0
	for _, f := range snap.Files {
		if f.Conflict() || f.Kind == vcs.KindIgnored {
			continue // ignored 行只展示、不参与提交计数
		}
		total++
		if isChecked != nil && isChecked(f.Path) {
			n++
		}
	}
	return fmt.Sprintf("%d of %d", n, total)
}

// BaseName is filepath.Base, tolerant of an empty path.
func BaseName(p string) string {
	if p == "" {
		return ""
	}
	if i := strings.LastIndexByte(p, os.PathSeparator); i >= 0 {
		return p[i+1:]
	}
	return p
}
