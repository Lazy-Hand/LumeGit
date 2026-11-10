package git

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FileKind distinguishes what happened to a file. Git infers renames by
// similarity — they are reported, not stored.
type FileKind int

const (
	KindModified FileKind = iota
	KindAdded             // newly created; staged or untracked
	KindDeleted
	KindRenamed
	KindTypechange // type change: symlink <-> regular file etc.
	KindUntracked
	KindUnmerged
)

func (k FileKind) String() string {
	switch k {
	case KindModified:
		return "modified"
	case KindAdded:
		return "added"
	case KindDeleted:
		return "deleted"
	case KindRenamed:
		return "renamed"
	case KindTypechange:
		return "type change"
	case KindUntracked:
		return "untracked"
	case KindUnmerged:
		return "conflicted"
	}
	return "unknown"
}

// Snapshot is one consistent read of the repository state for the
// commit workspace. Callers treat it as immutable; a new read replaces
// it wholesale.
//
// Consistency model: this is git's own view at execution time, not a
// transaction over HEAD+index+worktree. State-changing operations must
// re-verify against a fresh snapshot before acting.
type Snapshot struct {
	Worktree string // absolute path taken from the Repo

	// HEAD state. Unborn: no commits yet (fresh init/clone), HeadOID empty.
	HeadName string // without refs/heads/: "main"; "(detached)" → Detached
	HeadOID  string
	Detached bool
	Unborn   bool

	// Ahead/Behind vs upstream, -1 when unknown or no upstream set.
	Ahead  int
	Behind int

	// InProgress: "merge", "rebase (...)", "cherry-pick", "revert",
	// "am", "bisect", or "" when clean.
	InProgress string

	// GitDir is the absolute git dir (per-worktree for linked worktrees).
	GitDir string

	// Files lists tracked changes and untracked files, in git's
	// order. There is no guaranteed order — sorts before display.
	Files []FileChange
}

// Find returns the change record for path, or nil.
func (s *Snapshot) Find(path string) *FileChange {
	for i := range s.Files {
		if s.Files[i].Path == path {
			return &s.Files[i]
		}
	}
	return nil
}

// Staged returns the files whose index side differs from HEAD.
func (s *Snapshot) Staged() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Staged() {
			out = append(out, f)
		}
	}
	return out
}

// Unstaged returns the files whose worktree side differs from the
// index, plus untracked files (which are by definition unstaged).
func (s *Snapshot) Unstaged() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Unstaged() {
			out = append(out, f)
		}
	}
	return out
}

// Conflicts returns files with unmerged index entries.
func (s *Snapshot) Conflicts() []FileChange {
	var out []FileChange
	for _, f := range s.Files {
		if f.Conflict() {
			out = append(out, f)
		}
	}
	return out
}

// FileChange describes one file's status across the three trees
// (HEAD / index / worktree), exactly as git reported it. Staged vs
// unstaged is derived only from the X/Y letters; a file whose staged
// content was edited back to HEAD's shows only the residual letters,
// so "index has content differing from worktree" is never invented
// here.
type FileChange struct {
	Path     string // target path (for renames: new path)
	OrigPath string // set on KindRenamed
	Kind     FileKind

	// IndexState ('X') and WorktreeState ('Y'): 'M','A','D','R','C','T',
	// 'U', or ' ' for no change (git prints '.', normalized here).
	// Unmerged records set IndexState='U'.
	IndexState    byte
	WorktreeState byte

	RenameScore int // 0-100 for KindRenamed
}

// Staged: the index side differs from HEAD.
func (f FileChange) Staged() bool {
	if f.Kind == KindUntracked {
		return false
	}
	switch f.IndexState {
	case 'M', 'A', 'D', 'R', 'C', 'T':
		return true
	}
	return false
}

// Unstaged: the worktree side differs from the index.
func (f FileChange) Unstaged() bool {
	if f.Kind == KindUntracked {
		return true
	}
	switch f.WorktreeState {
	case 'M', 'D', 'C', 'T':
		return true
	}
	return false
}

// Conflict: the path has unmerged index entries.
func (f FileChange) Conflict() bool { return f.Kind == KindUnmerged }

// branchInfo carries the "# branch.*" header fields.
type branchInfo struct {
	OID    string // "(initial)" when unborn
	Head   string // branch name without refs/heads/
	Ahead  int    // -1 unknown
	Behind int    // -1 unknown
	HasAB  bool   // branch.ab present
}

// parseStatus decodes `git status --porcelain=v2 -z --branch` output.
//
// Verified against git's actual output: records are NUL-separated;
// within a record fields are space-separated and the path is the LAST
// space-separated field and may contain spaces. Rename records ("2")
// carry "<X><score> <path>" as the last field, then one extra NUL
// token for origPath.
func parseStatus(out []byte) (bi branchInfo, files []FileChange, err error) {
	bi.Ahead, bi.Behind = -1, -1
	if len(out) > 0 && out[len(out)-1] != 0 {
		return bi, nil, fmt.Errorf("status: output not NUL-terminated")
	}
	var recs [][]byte
	if len(out) > 0 {
		recs = bytes.Split(out[:len(out)-1], []byte{0})
	}
	i := 0
	for i < len(recs) {
		line := recs[i]
		i++
		if len(line) == 0 {
			continue
		}
		switch c := line[0]; c {
		case '#':
			parseBranchHeader(string(line), &bi)
		case '1':
			// 1 XY sub mH mI mW hH hI <path>
			parts := strings.SplitN(string(line), " ", 9)
			if len(parts) != 9 {
				return bi, files, fmt.Errorf("status: short 1-record %q", line)
			}
			xy := parts[1]
			files = append(files, FileChange{
				Path:          parts[8],
				IndexState:    xyByte(xy, 0),
				WorktreeState: xyByte(xy, 1),
				Kind:          classify(xyByte(xy, 0), xyByte(xy, 1)),
			})
		case '2':
			// 2 XY sub mH mI mW hH hI X<score> <path> \0 <origPath>
			parts := strings.SplitN(string(line), " ", 9)
			if len(parts) != 9 {
				return bi, files, fmt.Errorf("status: short 2-record %q", line)
			}
			xy := parts[1]
			last := parts[8]
			sp := strings.IndexByte(last, ' ')
			if sp < 1 {
				return bi, files, fmt.Errorf("status: 2-record missing score %q", last)
			}
			scoreField := last[:sp] // e.g. "R100"
			score := 0
			if len(scoreField) > 1 {
				score, err = strconv.Atoi(scoreField[1:])
				if err != nil {
					return bi, files, fmt.Errorf("status: bad rename score %q", scoreField)
				}
			}
			// Path is in this same NUL token (may contain spaces);
			// origPath is the next NUL token.
			path := last[sp+1:]
			if i >= len(recs) {
				return bi, files, fmt.Errorf("status: 2-record without origPath")
			}
			orig := string(recs[i])
			i++
			files = append(files, FileChange{
				Path:          path,
				OrigPath:      orig,
				RenameScore:   score,
				IndexState:    xyByte(xy, 0),
				WorktreeState: xyByte(xy, 1),
				Kind:          KindRenamed,
			})
		case 'u':
			// u XY sub m1 m2 m3 mW h1 h2 h3 <path>
			parts := strings.SplitN(string(line), " ", 11)
			if len(parts) != 11 {
				return bi, files, fmt.Errorf("status: short u-record %q", line)
			}
			xy := parts[1]
			files = append(files, FileChange{
				Path:          parts[10],
				Kind:          KindUnmerged,
				IndexState:    'U',
				WorktreeState: xyByte(xy, 1),
			})
		case '?':
			if len(line) < 2 || line[1] != ' ' {
				return bi, files, fmt.Errorf("status: bad untracked record %q", line)
			}
			files = append(files, FileChange{
				Path:          string(line[2:]),
				Kind:          KindUntracked,
				IndexState:    ' ',
				WorktreeState: '?',
			})
		case '!': // ignored; we never request these
		default:
			return bi, files, fmt.Errorf("status: unknown record type %q", line)
		}
	}
	return bi, files, nil
}

// parseBranchHeader handles one "# ..." line.
func parseBranchHeader(line string, bi *branchInfo) {
	body := strings.TrimPrefix(line, "# ")
	switch {
	case strings.HasPrefix(body, "branch.oid "):
		bi.OID = strings.TrimSpace(strings.TrimPrefix(body, "branch.oid "))
	case strings.HasPrefix(body, "branch.head "):
		bi.Head = strings.TrimSpace(strings.TrimPrefix(body, "branch.head "))
	case strings.HasPrefix(body, "branch.upstream "):
		// name only; ahead/behind come from branch.ab
	case strings.HasPrefix(body, "branch.ab "):
		bi.HasAB = true
		for _, f := range strings.Fields(strings.TrimPrefix(body, "branch.ab ")) {
			switch {
			case strings.HasPrefix(f, "+"):
				bi.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f, "+"))
			case strings.HasPrefix(f, "-"):
				bi.Behind, _ = strconv.Atoi(strings.TrimPrefix(f, "-"))
			}
		}
	}
}

// xyByte normalizes git's "." (no change) placeholder to ' '.
func xyByte(xy string, i int) byte {
	if len(xy) <= i || xy[i] == '.' {
		return ' '
	}
	return xy[i]
}

// classify maps X/Y to a FileKind; renames arrive via the "2" record.
func classify(x, y byte) FileKind {
	if x == 'U' || y == 'U' {
		return KindUnmerged
	}
	switch x {
	case 'A':
		return KindAdded
	case 'D':
		return KindDeleted
	case 'T':
		return KindTypechange
	}
	switch y {
	case 'A':
		return KindAdded
	case 'D':
		return KindDeleted
	case 'T':
		return KindTypechange
	}
	return KindModified
}
