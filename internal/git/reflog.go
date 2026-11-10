package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// ReflogEntry is one line of `git reflog` for a ref (HEAD by default).
// IDEA's Reflog panel shows the action the user took and the commit
// it left the ref pointing at.
type ReflogEntry struct {
	Index   int       // %gd → "HEAD@{n}" n
	OID     string    // new head after the action
	Short   string    // abbreviated OID
	Action  string    // "commit" | "checkout" | "rebase" | "merge" | ...
	Message string    // the rest after "action:"
	When    time.Time // committer time of the reflog entry
	Who     string    // committer name
}

// Reflog lists the actions recorded on ref ("HEAD" by default), newest
// first — IDEA's Git → Reflog panel.
func (r *Repo) Reflog(ctx context.Context, ref string, max int) ([]ReflogEntry, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if max <= 0 {
		max = 200
	}
	// Fields: %gd selector  %H oid  %gs subject ("action: msg")
	//         %ct time      %gn name
	res, err := r.Run(ctx, "reflog", "show", "--date=unix",
		"--format=%gd%x1f%H%x1f%gs%x1f%ct%x1f%gn",
		"-n", strconv.Itoa(max), ref)
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"reflog", "show"}, Stderr: res.OutputOfStderr()}
	}
	var out []ReflogEntry
	for _, ln := range strings.Split(res.Output(), "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\x1f")
		if len(f) < 5 {
			continue
		}
		e := ReflogEntry{OID: f[1], Who: f[4]}
		// Index from "HEAD@{n}" / "main@{n}".
		if at := strings.Index(f[0], "@{"); at >= 0 {
			if end := strings.Index(f[0][at:], "}"); end > 0 {
				e.Index, _ = strconv.Atoi(f[0][at+2 : at+end])
			}
		}
		e.Short = e.OID
		if len(e.Short) > 7 {
			e.Short = e.Short[:7]
		}
		// %gs is "action: rest" — split on the first colon.
		if i := strings.Index(f[2], ":"); i >= 0 {
			e.Action = strings.TrimSpace(f[2][:i])
			e.Message = strings.TrimSpace(f[2][i+1:])
		} else {
			e.Action = f[2]
		}
		if ts, err := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64); err == nil {
			e.When = time.Unix(ts, 0)
		}
		out = append(out, e)
	}
	return out, nil
}
