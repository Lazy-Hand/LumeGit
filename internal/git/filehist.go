package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FileLog returns the commit history for one path, following renames.
func (r *Repo) FileLog(ctx context.Context, path string, maxCount int) ([]Commit, error) {
	args := []string{"log", "--follow", "--format=" + logFields}
	if maxCount > 0 {
		args = append(args, "-n", strconv.Itoa(maxCount))
	}
	args = append(args, "--", path)
	res, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		if isUnbornErr(res.OutputOfStderr()) {
			return nil, nil
		}
		return nil, &GitError{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return parseLog(res.Stdout), nil
}

// BlameLine is one source line with its attribution.
type BlameLine struct {
	OID      string
	ShortOID string
	Author   string
	When     time.Time
	LineNo   int    // final line number (1-based)
	OrigLine int    // line in the blamed commit
	Filename string // source path at that commit (differs on rename)
	Text     string // line content (without trailing newline)
	Boundary bool   // commit is at history's edge
}

// Blame runs git blame --porcelain on path at HEAD (or rev). Grouped
// result lines share a commit block; each still gets its own BlameLine.
// -M and -C follow lines across intra-file moves and file renames —
// the same default IDEA uses for its Annotate view.
func (r *Repo) Blame(ctx context.Context, rev, path string) ([]BlameLine, error) {
	if rev == "" {
		rev = "HEAD"
	}
	res, err := r.Run(ctx, "blame", "--porcelain", "-M", "-C", rev, "--", path)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"blame", rev, "--", path}, Stderr: res.OutputOfStderr()}
	}
	return parseBlame(res.Stdout), nil
}

// blameMeta is per-commit attribution, cached by OID because
// --porcelain only prints headers on a commit's first line in a group.
type blameMeta struct {
	Author   string
	When     time.Time
	Filename string
	Boundary bool
}

// parseBlame decodes --porcelain: each line record opens with
// "<oid> <orig> <final> <count>", optional header lines (author,
// author-time, filename, boundary, …) printed ONLY the first time a
// commit appears in the output, then a "\t<content>" line.
func parseBlame(out []byte) []BlameLine {
	var lines []BlameLine
	metas := map[string]*blameMeta{}
	var cur *blameMeta // metadata block being filled for the last header
	var curOID string

	for _, raw := range strings.Split(string(out), "\n") {
		if raw == "" {
			continue
		}
		// Content line starts with a tab.
		if strings.HasPrefix(raw, "\t") {
			if len(lines) > 0 {
				lines[len(lines)-1].Text = raw[1:]
			}
			continue
		}
		// Header: <oid> <orig-line> <final-line> [<group-size>]
		f := strings.Fields(raw)
		if len(f) >= 3 && isHexOID(f[0]) && isAllDigits(f[1]) && isAllDigits(f[2]) {
			curOID = f[0]
			cur = metas[curOID]
			if cur == nil {
				cur = &blameMeta{}
				metas[curOID] = cur
			}
			orig, _ := strconv.Atoi(f[1])
			final, _ := strconv.Atoi(f[2])
			lines = append(lines, BlameLine{
				OID: curOID, ShortOID: shortRevLabel(curOID),
				LineNo: final, OrigLine: orig,
			})
			continue
		}
		// Attribute line: key [value] — attaches to the current commit.
		if cur == nil {
			continue
		}
		key, val, _ := strings.Cut(raw, " ")
		switch key {
		case "author":
			cur.Author = val
		case "author-time":
			if ts, err := strconv.ParseInt(val, 10, 64); err == nil {
				cur.When = time.Unix(ts, 0)
			}
		case "filename":
			cur.Filename = val
		case "boundary":
			cur.Boundary = true
		}
	}
	// Fill each line's attribution from its commit's cached metadata.
	for i := range lines {
		if m := metas[lines[i].OID]; m != nil {
			lines[i].Author = m.Author
			lines[i].When = m.When
			lines[i].Filename = m.Filename
			lines[i].Boundary = m.Boundary
		}
	}
	return lines
}

func isHexOID(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, ch := range s {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return len(s) > 0
}

// FileAtCommit reads a file's full content at a revision.
func (r *Repo) FileAtCommit(ctx context.Context, rev, path string) ([]byte, error) {
	b, err := r.catFile(ctx, rev+":"+path)
	if err != nil {
		return nil, fmt.Errorf("read %s:%s: %w", rev, path, err)
	}
	return b, nil
}
