package git

import (
	"context"
	"strings"
)

// ConflictEntry is one file with unmerged index entries, plus enough
// context to drive IDEA's "Resolve Conflicts" dialog (which side it
// came from, how many unmerged hunks it carries).
type ConflictEntry struct {
	Path string

	// Stage presence (from `git ls-files -u`):
	//   stage1 = base/ancestor, stage2 = ours, stage3 = theirs.
	// Both-added files have no stage1; delete/modify conflicts miss
	// stage2 or stage3.
	HasBase  bool
	HasOurs  bool
	HasTheir bool

	// Hunks counts the unmerged records for this path — a proxy for
	// conflict size (IDEA shows "N conflicts" per file).
	Hunks int

	// Kind is a short Chinese label for the conflict shape:
	//   "修改/修改" "添加/添加" "删除/修改" "修改/删除" "删除/删除"
	Kind string
}

// ConflictsDetailed lists every unmerged path with its stage shape —
// the data behind IDEA's Resolve Conflicts dialog.
func (r *Repo) ConflictsDetailed(ctx context.Context) ([]ConflictEntry, error) {
	res, err := r.Run(ctx, "ls-files", "-u", "-z")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"ls-files", "-u"}, Stderr: res.OutputOfStderr()}
	}
	byPath := map[string]*ConflictEntry{}
	var order []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if rec == "" {
			continue
		}
		// "100644 <oid> <stage>\t<path>"
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		head, path := rec[:tab], rec[tab+1:]
		f := strings.Fields(head)
		if len(f) < 3 {
			continue
		}
		stage := f[2]
		e := byPath[path]
		if e == nil {
			e = &ConflictEntry{Path: path}
			byPath[path] = e
			order = append(order, path)
		}
		switch stage {
		case "1":
			e.HasBase = true
		case "2":
			e.HasOurs = true
		case "3":
			e.HasTheir = true
		}
		e.Hunks++
	}
	out := make([]ConflictEntry, 0, len(order))
	for _, p := range order {
		e := byPath[p]
		e.Kind = conflictKindLabel(e)
		out = append(out, *e)
	}
	return out, nil
}

// conflictKindLabel maps stage presence to IDEA-style "X/Y" wording.
func conflictKindLabel(e *ConflictEntry) string {
	ours := sideName(e.HasOurs)
	theirs := sideName(e.HasTheir)
	// Both present without base → add/add; with base → modify/modify.
	if e.HasOurs && e.HasTheir {
		if e.HasBase {
			return "修改/修改"
		}
		return "添加/添加"
	}
	if !e.HasOurs && !e.HasTheir {
		return "删除/删除"
	}
	return ours + "/" + theirs
}

func sideName(present bool) string {
	if present {
		return "修改"
	}
	return "删除"
}

// ResolveAllOurs resolves every conflict by taking the "ours" side
// (IDEA's "Accept Yours" applied to the whole list).
func (r *Repo) ResolveAllOurs(ctx context.Context) error {
	return r.resolveAll(ctx, "--ours")
}

// ResolveAllTheirs takes "theirs" for every conflict.
func (r *Repo) ResolveAllTheirs(ctx context.Context) error {
	return r.resolveAll(ctx, "--theirs")
}

// resolveAll checks out the given side for every unmerged path then
// stages them all.
func (r *Repo) resolveAll(ctx context.Context, flag string) error {
	list, err := r.ConflictsDetailed(ctx)
	if err != nil {
		return err
	}
	for i := range list {
		if err := r.ResolveConflict(ctx, list[i].Path, conflictSideFlag(flag), nil); err != nil {
			return err
		}
	}
	return nil
}

func conflictSideFlag(flag string) ConflictResolution {
	if flag == "--theirs" {
		return ResolveTheirs
	}
	return ResolveOurs
}
