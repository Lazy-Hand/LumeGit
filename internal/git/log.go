package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// SigStatus mirrors `git log --format=%G?`: the letter git reports.
type SigStatus string

const (
	SigNone     SigStatus = "N" // unsigned
	SigGood     SigStatus = "G" // valid signature
	SigBad      SigStatus = "B" // bad signature
	SigUnknown  SigStatus = "U" // signature valid, signer unknown
	SigExpired  SigStatus = "X" // signature expired
	SigCannot   SigStatus = "Y" // expired key
	SigRevoked  SigStatus = "R" // revoked key
	SigError    SigStatus = "E" // verification error
	SigUnsigned SigStatus = ""  // no signature field requested
)

// Commit is one log entry as shown in the history list.
type Commit struct {
	OID      string    // full hash
	ShortOID string    // abbreviated hash for display
	Parents  []string  // parent OIDs (>1 means merge)
	Author   string    // author name
	Email    string    // author email
	When     time.Time // author date
	Subject  string    // first message line
	Refs     []string  // ref names pointing here (branches, tags, HEAD)
	Sig      SigStatus // %G? signature status; "" when not requested
}

// Merge reports whether the commit has more than one parent.
func (c *Commit) Merge() bool { return len(c.Parents) > 1 }

// CommitFile is one file touched by a commit, for the detail pane.
type CommitFile struct {
	Path     string
	OrigPath string // rename source, when reported
	Status   byte   // 'A' 'M' 'D' 'R' 'T' (from --name-status)
}

// StatusKind maps a name-status letter to the workspace FileKind set,
// so the history file list reuses the same glyphs and colors.
func (f CommitFile) StatusKind() FileKind {
	switch f.Status {
	case 'A':
		return KindAdded
	case 'D':
		return KindDeleted
	case 'R':
		return KindRenamed
	case 'T':
		return KindTypechange
	case 'M', 'C':
		return KindModified
	}
	return KindModified
}

// logFields is the NUL-separated record layout parsed by parseLog.
// %x1f is US (field), %x1e is RS (record) — both can never appear in
// real git data, unlike spaces or newlines. %G? gives the signature
// status letter ("" = unsigned).
const logFields = "%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%s%x1f%D%x1f%G?%x1e"

// Log reads the commit history for HEAD, newest first. maxCount <= 0
// means no cap. An unborn branch (no commits yet) returns an empty
// slice, not an error.
// LogOptions filters the history (IDEA's log filter bar).
type LogOptions struct {
	Max    int    // -n
	Skip   int    // --skip: page offset for lazy loading
	Branch string // rev to walk (empty = HEAD); also accepts tag/commit
	Author string // --author substring
	Grep   string // --grep (commit message, case-insensitive)
	Path   string // -- pathspec limiter
	All    bool   // --all: every ref's history
}

func (r *Repo) Log(ctx context.Context, maxCount int) ([]Commit, error) {
	return r.LogFiltered(ctx, LogOptions{Max: maxCount})
}

// LogFiltered is Log with IDEA's filter bar options.
func (r *Repo) LogFiltered(ctx context.Context, o LogOptions) ([]Commit, error) {
	args := []string{"log", "--format=" + logFields}
	if o.Max > 0 {
		args = append(args, "-n", strconv.Itoa(o.Max))
	}
	if o.Skip > 0 {
		args = append(args, "--skip", strconv.Itoa(o.Skip))
	}
	if o.Author != "" {
		args = append(args, "--author="+o.Author)
	}
	if o.Grep != "" {
		args = append(args, "--regexp-ignore-case", "--grep="+o.Grep)
	}
	if o.All {
		args = append(args, "--all")
	} else if o.Branch != "" {
		args = append(args, o.Branch)
	}
	// Always end the revision list with "--": a branch named like a
	// directory ("docs", "test") is otherwise ambiguous and git aborts.
	args = append(args, "--")
	if o.Path != "" {
		args = append(args, o.Path)
	}
	res, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		// "fatal: your current branch ... does not have any commits yet"
		// — an unborn branch is an empty history, not a failure.
		if isUnbornErr(res.OutputOfStderr()) {
			return nil, nil
		}
		return nil, &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return parseLog(res.Stdout), nil
}

func isUnbornErr(stderr string) bool {
	return strings.Contains(stderr, "does not have any commits yet") ||
		strings.Contains(stderr, "bad default revision")
}

// parseLog splits the US/RS-delimited stream into Commits. Truncated
// trailing records are dropped.
func parseLog(out []byte) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 8 {
			continue
		}
		c := Commit{
			OID:      f[0],
			ShortOID: f[1],
			Author:   f[3],
			Email:    f[4],
			Subject:  f[6],
		}
		if len(f) > 8 {
			c.Sig = SigStatus(f[8])
		}
		if f[2] != "" {
			c.Parents = strings.Fields(f[2])
		}
		if t, err := time.Parse(time.RFC3339, f[5]); err == nil {
			c.When = t
		}
		if f[7] != "" {
			c.Refs = parseRefs(f[7])
		}
		commits = append(commits, c)
	}
	return commits
}

// parseRefs cleans a %D decoration list ("HEAD -> main, tag: v1, dev")
// into displayable names.
func parseRefs(dec string) []string {
	var out []string
	for _, part := range strings.Split(dec, ",") {
		name := strings.TrimSpace(part)
		name = strings.TrimPrefix(name, "HEAD -> ")
		name = strings.TrimPrefix(name, "tag: ")
		if name != "" && name != "HEAD" {
			out = append(out, name)
		}
	}
	return out
}

// CommitMessage returns the commit's full message (%B — subject plus
// body). The log list only stores the subject line, so the amend dialog
// needs this to avoid silently dropping the body on save.
func (r *Repo) CommitMessage(ctx context.Context, oid string) (string, error) {
	res, err := r.RunOK(ctx, "log", "-1", "--format=%B", oid)
	if err != nil {
		return "", err
	}
	// git appends a trailing newline to %B — strip exactly one.
	return strings.TrimSuffix(string(res.Stdout), "\n"), nil
}

// CommitFiles lists the files a commit touched, via
// `git show --name-status`. For merge commits the combined tree makes
// file lists ill-defined; this returns the list against the first
// parent only.
func (r *Repo) CommitFiles(ctx context.Context, oid string) ([]CommitFile, error) {
	res, err := r.RunOK(ctx, "show", "--name-status", "--format=", "-z", "--first-parent", oid)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(res.Stdout), nil
}

// parseNameStatus reads the NUL-separated --name-status stream:
//
//	M\x00path\x00            — ordinary status
//	R100\x00old\x00new\x00   — renames carry a second path token
func parseNameStatus(out []byte) []CommitFile {
	toks := strings.Split(string(out), "\x00")
	var files []CommitFile
	for i := 0; i+1 < len(toks); {
		st := toks[i]
		i++
		if st == "" {
			continue
		}
		f := CommitFile{Status: st[0]}
		if f.Status == 'R' || f.Status == 'C' {
			if i+1 < len(toks) {
				f.OrigPath = toks[i]
				f.Path = toks[i+1]
				i += 2
			}
		} else {
			f.Path = toks[i]
			i++
		}
		if f.Path != "" {
			files = append(files, f)
		}
	}
	return files
}

// CommitFileDiff returns the two-side diff of one file within a commit:
// old side is the commit's first parent (empty for a root or added
// file), new side is the commit itself.
func (r *Repo) CommitFileDiff(ctx context.Context, oid string, f CommitFile) (*DiffInfo, error) {
	parent, err := r.firstParent(ctx, oid)
	if err != nil {
		return nil, err
	}
	info := &DiffInfo{
		Path:     f.Path,
		OldPath:  f.OrigPath,
		Renamed:  f.Status == 'R',
		OldLabel: shortRevLabel(parent) + " (父提交)",
		NewLabel: shortRevLabel(oid),
	}
	// Old side: for renames read the source path at the parent; added
	// files have no old blob (absence is the diff).
	if parent != "" {
		oldPath := f.Path
		if f.OrigPath != "" {
			oldPath = f.OrigPath
		}
		b, err := r.catFile(ctx, parent+":"+oldPath)
		if err != nil {
			return nil, err
		}
		info.Old = b
	}
	// New side: deleted files have no blob at the commit.
	if f.Status != 'D' {
		b, err := r.catFile(ctx, oid+":"+f.Path)
		if err != nil {
			return nil, err
		}
		info.New = b
	}
	info.Binary = looksBinary(info.Old) || looksBinary(info.New)
	return info, nil
}

// firstParent resolves oid^ to a hash; "" means a root commit.
func (r *Repo) firstParent(ctx context.Context, oid string) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", oid+"^")
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		return "", nil // root commit
	}
	return strings.TrimSpace(res.Output()), nil
}

func shortRevLabel(oid string) string {
	if oid == "" {
		return "(根提交)"
	}
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}
