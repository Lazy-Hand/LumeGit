package svn

import (
	"context"
	"fmt"
	"strings"
)

// Accept is svn update/merge's --accept answer for conflicts.
type Accept string

const (
	AcceptPostpone      Accept = "postpone"        // mark conflicted, decide later (default for GUI)
	AcceptWorking       Accept = "working"         // keep local edits
	AcceptBase          Accept = "base"            // throw away local edits
	AcceptMineConflict  Accept = "mine-conflict"   // mine only inside conflicts
	AcceptTheirsConfl   Accept = "theirs-conflict" // theirs inside conflicts
	AcceptMineFull      Accept = "mine-full"       // keep all local edits
	AcceptTheirsFull    Accept = "theirs-full"     // take all incoming
)

// UpdateOptions bounds `svn update`.
type UpdateOptions struct {
	Rev            int64  // -r N; 0 = HEAD
	Depth          string // --depth empty|files|immediates|infinity; "" = keep
	SetDepth       bool   // --set-depth (sticky)
	IgnoreExternals bool  // --ignore-externals
	Accept         Accept // conflict answer; "" = postpone
	Parents        bool   // --parents (pull in missing parents)
	Force          bool   // --force (discard local edits that conflict)
}

// Update runs `svn update`. Output is line-per-item (A/U/D/C/G plus
// "At revision N.") — parsed into UpdateResult.
func (w *WC) Update(ctx context.Context, o UpdateOptions) (*UpdateResult, error) {
	args := []string{"update"}
	if o.Rev > 0 {
		args = append(args, "-r", fmt.Sprint(o.Rev))
	}
	if o.Depth != "" {
		args = append(args, "--depth", o.Depth)
	}
	if o.SetDepth {
		args = append(args, "--set-depth", o.Depth)
	}
	if o.IgnoreExternals {
		args = append(args, "--ignore-externals")
	}
	acc := o.Accept
	if acc == "" {
		acc = AcceptPostpone
	}
	args = append(args, "--accept", string(acc))
	if o.Parents {
		args = append(args, "--parents")
	}
	if o.Force {
		args = append(args, "--force")
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseUpdate(res.Output()), nil
}

// UpdateResult summarizes what update did — IDEA's Update Info panel.
type UpdateResult struct {
	Revision  int64 // "At revision N."
	Updated   []string
	Added     []string
	Deleted   []string
	Conflicted []string // C lines — the caller drives resolve
	Merged    []string // G lines
	Skipped   []string
	Restored  []string
}

func parseUpdate(out string) *UpdateResult {
	r := &UpdateResult{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "At revision ") {
			fmt.Sscanf(ln, "At revision %d.", &r.Revision)
			continue
		}
		if strings.HasPrefix(ln, "Updated to revision ") {
			fmt.Sscanf(ln, "Updated to revision %d.", &r.Revision)
			continue
		}
		if strings.HasPrefix(ln, "Summary of conflicts") || strings.HasPrefix(ln, "Summary of updates") {
			continue
		}
		// Status letter(s) + space + path. SVN prints "C path",
		// "A path", "U path", "D path", "G path", "  path" (tree).
		switch {
		case strings.HasPrefix(ln, "C "):
			r.Conflicted = append(r.Conflicted, strings.TrimSpace(ln[2:]))
		case strings.HasPrefix(ln, "A "):
			r.Added = append(r.Added, strings.TrimSpace(ln[2:]))
		case strings.HasPrefix(ln, "D "):
			r.Deleted = append(r.Deleted, strings.TrimSpace(ln[2:]))
		case strings.HasPrefix(ln, "U "):
			r.Updated = append(r.Updated, strings.TrimSpace(ln[2:]))
		case strings.HasPrefix(ln, "G "):
			r.Merged = append(r.Merged, strings.TrimSpace(ln[2:]))
		case strings.HasPrefix(ln, "Skipped "):
			r.Skipped = append(r.Skipped, strings.TrimSpace(ln[8:]))
		case strings.HasPrefix(ln, "Restored "):
			r.Restored = append(r.Restored, strings.TrimSpace(ln[9:]))
		}
	}
	return r
}
