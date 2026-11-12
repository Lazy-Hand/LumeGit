package svn

import (
	"context"
	"fmt"
	"strings"
)

// CommitOptions for `svn commit`.
type CommitOptions struct {
	Message        string
	Paths          []string // --targets; "" = whole WC
	Changelist     string   // --changelist
	KeepChangelists bool    // --keep-changelists
	NoUnlock       bool    // --no-unlock
	Depth          string   // --depth empty|files|immediates|infinity
}

// CommitResult is svn commit's output parsed — new rev + the items sent.
type CommitResult struct {
	Rev      int64 // "Committed revision N."
	Added    []string
	Deleted  []string
	Modified []string // Sending/Transmitting lines
	Skipped  []string
	Failed   bool
	Reason   string
}

// Commit runs `svn commit -F <msg> [--targets …]`.
// Message goes via stdin (-F -) so quoting/non-ASCII survives untouched.
func (w *WC) Commit(ctx context.Context, o CommitOptions) (*CommitResult, error) {
	args := []string{"commit", "-F", "-"}
	if o.Changelist != "" {
		args = append(args, "--changelist", o.Changelist)
	}
	if o.KeepChangelists {
		args = append(args, "--keep-changelists")
	}
	if o.NoUnlock {
		args = append(args, "--no-unlock")
	}
	if o.Depth != "" {
		args = append(args, "--depth", o.Depth)
	}
	if len(o.Paths) > 0 {
		args = append(args, "--")
		for _, p := range o.Paths {
			args = append(args, pathArg(p))
		}
	}
	res, err := w.RunStdin(ctx, []byte(o.Message), args...)
	if err != nil {
		return nil, err
	}
	cr := parseCommit(res.Output(), res.OutputOfStderr())
	if !res.ExitOK() {
		cr.Failed = true
		if cr.Reason == "" {
			cr.Reason = strings.TrimSpace(res.OutputOfStderr())
		}
		return cr, &Error{Args: args, Stderr: cr.Reason}
	}
	return cr, nil
}

func parseCommit(out, stderr string) *CommitResult {
	cr := &CommitResult{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "Committed revision "):
			fmt.Sscanf(ln, "Committed revision %d.", &cr.Rev)
		case strings.HasPrefix(ln, "Adding"):
			cr.Added = append(cr.Added, strings.TrimSpace(ln[len("Adding"):]))
		case strings.HasPrefix(ln, "Deleting"):
			cr.Deleted = append(cr.Deleted, strings.TrimSpace(ln[len("Deleting"):]))
		case strings.HasPrefix(ln, "Sending"), strings.HasPrefix(ln, "Transmitting"):
			cr.Modified = append(cr.Modified, strings.TrimSpace(ln[len("Sending"):]))
		case strings.HasPrefix(ln, "Skipped"):
			cr.Skipped = append(cr.Skipped, strings.TrimSpace(ln[len("Skipped"):]))
		}
	}
	if cr.Rev == 0 {
		if s := strings.TrimSpace(stderr); s != "" {
			cr.Reason = s
		}
	}
	return cr
}
