package git

import (
	"context"
	"fmt"
	"strings"
)

// Tree is one of the three compare sources for a file diff.
type Tree string

const (
	TreeHead     Tree = "head"
	TreeIndex    Tree = "index"
	TreeWorktree Tree = "worktree"
)

// DiffInfo is one file's diff, ready for the UI's DiffView. Old/New are
// full file texts in canonical git form produced by git itself — we do
// no byte surgery of our own, so line endings and clean filters match
// what git computes.
type DiffInfo struct {
	Path    string
	OldPath string // rename source, when git reported one

	// Old/New byte content of the two sides. For a working-tree side
	// this is the worktree bytes; for HEAD/index it is the stored
	// blob. Binary files set Binary and do not fill texts.
	Old    []byte
	New    []byte
	Binary bool

	// OldLabel/NewLabel describe the sides for the UI, e.g.
	// "HEAD", "index", "worktree".
	OldLabel string
	NewLabel string

	// Renamed reports git reported this change as a rename.
	Renamed bool
}

// FileDiffRequest names one file and its two compare sides.
type FileDiffRequest struct {
	Path string
	From Tree // old side
	To   Tree // new side
}

// treeIsSnapshotSide reports whether a Tree (unlike TreeWorktree) is
// read from committed/stored data for a given path.
func (t Tree) label() string {
	switch t {
	case TreeHead:
		return "HEAD"
	case TreeIndex:
		return "index"
	case TreeWorktree:
		return "worktree"
	}
	return string(t)
}

// FileDiff resolves one file's two-side diff.
//
//   - HEAD/index sides read via `git cat-file blob <prefix><path>`
//     (rename: HEAD:<origPath>) — canonical stored content; git's clean
//     filters are intentionally NOT applied (the stored form is what we
//     show), while the worktree side is raw disk bytes, i.e. what an
//     editor shows.
//   - Binary detection goes through `git diff --numstat` per side pair
//     ("-" "-" means binary). Untracked files are invisible to plain
//     diff, so their worktree bytes get Go's side of the check instead.
func (r *Repo) FileDiff(ctx context.Context, req FileDiffRequest) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     req.Path,
		OldLabel: req.From.label(),
		NewLabel: req.To.label(),
	}

	if req.From == TreeWorktree && req.To == TreeWorktree {
		return nil, fmt.Errorf("FileDiff: both sides worktree")
	}

	// Binary check + rename detection in one git call. --numstat on the
	// specific paths prints "add\tdele\tpath" with "-" "-" for binary; a
	// rename shows "old => new".
	numArgs := numstatArgs(req)
	num, numErr := r.Run(ctx, numArgs...)
	if numErr != nil {
		return nil, fmt.Errorf("numstat %s: %w", req.Path, numErr)
	}
	if !num.ExitOK() {
		// An unborn branch has no HEAD; "bad revision" means the file
		// simply isn't tracked anywhere, which our Go-side binary sniff
		// handles below. Other failures stay fatal.
		if !strings.Contains(num.OutputOfStderr(), "bad revision") &&
			!strings.Contains(num.OutputOfStderr(), "ambiguous argument") {
			return nil, &GitError{Args: numArgs, Stderr: num.OutputOfStderr()}
		}
		num.Stdout = nil
	}
	if line := firstNumstatLine(num.Stdout); line != nil {
		info.Binary = line.binary
	} else {
		// No numstat line at all: untracked or index-absent path — git
		// shows nothing for untracked in any diff form. Sniff the
		// worktree bytes ourselves; the UI shows a binary marker.
		if b, rerr := readWorktreeFile(ctx, r, req.Path); rerr == nil {
			info.Binary = looksBinary(b)
		}
	}
	// Renames: only relevant when HEAD→index or HEAD→worktree with
	// rename detection enabled at status level; we trust the snapshot's
	// FileChange instead of re-running a rename here (cheap, explicit).

	// Resolve old side.
	oldPath := req.Path
	// The caller may have learned from the snapshot that this is a
	// rename against HEAD; FileDiff keeps it neutral (see FileDiffRename).
	if req.From != TreeWorktree {
		rev := treeRev(req.From) + oldPath
		b, err := r.catFile(ctx, rev)
		if err != nil {
			return nil, err
		}
		info.Old = b
	}
	if req.To != TreeWorktree {
		rev := treeRev(req.To) + req.Path
		b, err := r.catFile(ctx, rev)
		if err != nil {
			return nil, err
		}
		info.New = b
	}
	if req.From == TreeWorktree {
		b, err := readWorktreeFile(ctx, r, req.Path)
		if err != nil {
			return nil, err
		}
		info.Old = b
	}
	if req.To == TreeWorktree {
		b, err := readWorktreeFile(ctx, r, req.Path)
		if err != nil {
			return nil, err
		}
		info.New = b
	}
	return info, nil
}

// FileDiffRename is FileDiff for a rename: the old side reads from
// OrigPath while the new side reads from Path.
//
// Side semantics for a staged rename (index holds delete(a),
// add(new)): Where the old content lives depends on which side we are
// reading — index vs HEAD:
//
//   - HEAD side: blob at HEAD:OrigPath (the file existed there).
//   - index side: the index itself no longer has OrigPath — the staged
//     diff for a rename at index level comes from comparing the
//     index's NEW path against HEAD's OrigPath. The index-side blob of
//     origPath is intentionally absent, so requesting it is a caller
//     error; we surface it instead of guessing.
func (r *Repo) FileDiffRename(ctx context.Context, path, origPath string, from, to Tree) (*DiffInfo, error) {
	info := &DiffInfo{
		Path:     path,
		OldPath:  origPath,
		Renamed:  true,
		OldLabel: from.label(),
		NewLabel: to.label(),
	}
	// Old side.
	switch from {
	case TreeWorktree:
		// A rename出现在worktree尚未staged时,old在磁盘上不存在了。
		info.Old = nil
	default:
		if from == TreeIndex {
			return nil, fmt.Errorf("FileDiffRename: index has no entry for original path %q", origPath)
		}
		b, err := r.catFile(ctx, treeRev(from)+origPath)
		if err != nil {
			return nil, err
		}
		info.Old = b
	}
	// New side.
	switch to {
	case TreeWorktree:
		b, err := readWorktreeFile(ctx, r, path)
		if err != nil {
			return nil, err
		}
		info.New = b
	default:
		b, err := r.catFile(ctx, treeRev(to)+path)
		if err != nil {
			return nil, err
		}
		info.New = b
	}
	return info, nil
}

func treeRev(t Tree) string {
	switch t {
	case TreeHead:
		return "HEAD:"
	case TreeIndex:
		// ":<stage>:<path>" — verified valid: ":0:path" resolves the
		// stage-0 index entry (git cat-file -p :0:plain.txt → ok).
		// treeRev returns the FULL colon-terminated prefix; callers
		// append the path directly.
		return ":0:"
	}
	return string(t) // deliberately invalid for TreeWorktree callers
}

// catFile reads one canonical blob. Missing paths (added/deleted files
// on a side) return nil bytes, not an error — the absence is the diff.
//
// Note: no "--" here — cat-file's rev argument (":0:path", "HEAD:path")
// must not look like a pathspec; with "--" git treats the rev as a file
// name and fails. Paths in revs are unambiguous because they follow the
// colon-prefix; a pathspec-escape hatch is not needed for blob lookups.
func (r *Repo) catFile(ctx context.Context, rev string) ([]byte, error) {
	res, err := r.Run(ctx, "cat-file", "-p", rev)
	if err != nil {
		return nil, err
	}
	if res.ExitOK() {
		return res.Stdout, nil
	}
	// With LC_ALL=C (pinned in the runner) the message is stable.
	stderr := res.OutputOfStderr()
	if strings.Contains(stderr, "not in") ||
		strings.Contains(stderr, "Neither") ||
		strings.Contains(stderr, "bad revision") ||
		strings.Contains(stderr, "unknown revision") ||
		strings.Contains(stderr, "invalid object name") ||
		strings.Contains(stderr, "ambiguous argument") {
		// Missing path OR missing rev (unborn HEAD, empty index): the
		// absence is the diff's empty side, not an error.
		return nil, nil
	}
	return nil, &GitError{Args: []string{"cat-file", rev}, Stderr: res.OutputOfStderr()}
}

type numstatLine struct {
	binary bool
}

// firstNumstatLine picks the single line we asked about. Renames make
// numstat print "old => new"; we only care which line is binary.
func firstNumstatLine(out []byte) *numstatLine {
	for _, ln := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(strings.TrimPrefix(ln, ":"), "\t", 3)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "-" && fields[1] == "-" {
			return &numstatLine{binary: true}
		}
		return &numstatLine{}
	}
	return nil
}

// numstatArgs builds the diff invocation for binary detection per side
// pair. git refuses several 3-way combos; each maps to the documented
// form:
//
//	head→index    git diff --numstat --cached
//	index→work    git diff --numstat
//	head→work     git diff --numstat HEAD
func numstatArgs(req FileDiffRequest) []string {
	var base []string
	switch {
	case req.From == TreeHead && req.To == TreeIndex:
		base = []string{"diff", "--numstat", "--cached"}
	case req.From == TreeIndex && req.To == TreeWorktree:
		base = []string{"diff", "--numstat"}
	case req.From == TreeHead && req.To == TreeWorktree:
		// Revision requested must come before the pathspec separator.
		base = []string{"diff", "--numstat", "HEAD"}
	default:
		// Same side twice can't happen (guarded in FileDiff); an
		// unknown pair degrades to the working-tree diff.
		base = []string{"diff", "--numstat"}
	}
	return append(base, "--", req.Path)
}
