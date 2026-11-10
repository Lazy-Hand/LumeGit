package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BisectStatus reports the in-progress bisect's state (IDEA's Git →
// Bisect panel).
type BisectStatus struct {
	Active      bool   // a bisect is in progress
	CurrentOID  string // the commit bisect has checked out now
	CurrentSub  string // its subject
	Steps       int    // good/bad marks so far
	FirstBadOID string // set once found (bisect ends)
	Done        bool   // first bad commit identified
}

// BisectStart kicks off a bisect between a known-good and a known-bad
// rev — `git bisect start` + `bad` + `good` (IDEA's start dialog).
func (r *Repo) BisectStart(ctx context.Context, good, bad string) (*RunResult, error) {
	if good == "" || bad == "" {
		return nil, fmt.Errorf("good and bad refs required")
	}
	if res, err := r.RunOK(ctx, "bisect", "start"); err != nil {
		return res, err
	}
	if res, err := r.RunOK(ctx, "bisect", "bad", bad); err != nil {
		return res, err
	}
	return r.RunOK(ctx, "bisect", "good", good)
}

// BisectMark records the current commit's verdict — good or bad. Git
// may check out the next candidate or finish the bisect (its output
// names the first bad commit). A non-zero exit is still returned as a
// RunResult so callers can read stderr (e.g. marking after the search
// already finished).
func (r *Repo) BisectMark(ctx context.Context, good bool) (*RunResult, error) {
	mark := "bad"
	if good {
		mark = "good"
	}
	return r.Run(ctx, "bisect", mark)
}

// BisectSkip lets the user defer a commit that can't be tested (IDEA's
// "Skip" button on a candidate).
func (r *Repo) BisectSkip(ctx context.Context) (*RunResult, error) {
	return r.RunOK(ctx, "bisect", "skip")
}

// BisectReset restores the original HEAD (`git bisect reset`).
func (r *Repo) BisectReset(ctx context.Context) (*RunResult, error) {
	return r.RunOK(ctx, "bisect", "reset")
}

// BisectStatus reads the current bisect state. Inactive repos return a
// zero-valued status with Active=false (not an error).
func (r *Repo) BisectStatus(ctx context.Context) (*BisectStatus, error) {
	st := &BisectStatus{}

	// Read BISECT_LOG to detect a live session and the first-bad marker.
	gd, err := r.gitDir(ctx)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(gd, "BISECT_LOG"))
	if err != nil {
		// No log = no bisect in flight.
		return st, nil
	}
	st.Active = true

	// Parse the log: "git bisect good/bad <oid>" lines count as steps;
	// the final "# first bad commit: <oid>" comment ends the search.
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "git bisect good"), strings.HasPrefix(ln, "git bisect bad"):
			st.Steps++
		case strings.HasPrefix(ln, "# first bad commit:"):
			st.Done = true
			st.FirstBadOID = strings.TrimSpace(strings.TrimPrefix(ln, "# first bad commit:"))
		}
	}

	// Current candidate: BISECT_HEAD is set while mid-bisect.
	if res, err := r.Run(ctx, "rev-parse", "--verify", "BISECT_HEAD"); err == nil && res.ExitOK() {
		st.CurrentOID = strings.TrimSpace(res.Output())
		if res2, err := r.Run(ctx, "log", "-1", "--format=%s", st.CurrentOID); err == nil && res2.ExitOK() {
			st.CurrentSub = strings.TrimSpace(res2.Output())
		}
	}
	return st, nil
}
