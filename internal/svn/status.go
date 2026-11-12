package svn

import (
	"context"
	"encoding/xml"
)

// Item is one entry from `svn status --xml`. ItemKind is the file/dir
// state; TextStatus is the working-copy status letter; PropStatus the
// property status; ReposStatus what's on the server (with --show-updates).
type Item struct {
	Path        string
	TextStatus  Status // ' ' normal, M, A, D, ?, !, C, K, L, S, X, R, O, ~, I
	PropStatus  Status // ' ' normal, M, C
	TreeConflict bool
	Switched    bool
	Locked      bool // wc has a lock (K)
	Copied      bool // scheduled addition with history
	WCRev       int64 // revision this item sits on (mixed-rev WC)
	// Server-side, when queried with ShowUpdates:
	ReposTextStatus Status // what changed on the server
	ReposPropStatus Status
	ReposLocked     bool // locked on the server by someone else
	OOD             bool // out-of-date — an update would touch it
}

// Status is a single-letter svn status code.
type Status byte

// Kind collapses an Item into the coarse bucket the UI colors by —
// IDEA/TortoiseSVN's groups (blue M, purple A, dark-red D/!, yellow C…).
type Kind int

const (
	KindNormal Kind = iota
	KindModified
	KindAdded
	KindDeleted
	KindMissing // ! — deleted without svn rm
	KindConflicted
	KindTreeConflicted
	KindUnversioned // ?
	KindIgnored     // I
	KindObstructed  // ~
	KindReplaced    // R
	KindExternal    // X
	KindIncomplete  // svn couldn't finish an op
	KindOutOfDate   // server-side change pending
	KindLocked      // K/L
)

// Kind returns the display bucket for an item.
func (it *Item) Kind() Kind {
	if it.TreeConflict {
		return KindTreeConflicted
	}
	if it.OOD {
		return KindOutOfDate
	}
	switch it.TextStatus {
	case 'M':
		return KindModified
	case 'A':
		return KindAdded
	case 'D':
		return KindDeleted
	case '!':
		return KindMissing
	case 'C':
		return KindConflicted
	case '?':
		return KindUnversioned
	case 'I':
		return KindIgnored
	case '~':
		return KindObstructed
	case 'R':
		return KindReplaced
	case 'X':
		return KindExternal
	case 'K', 'L':
		return KindLocked
	default:
		return KindNormal
	}
}

// Changed reports whether the item is a local change worth listing in
// the commit pane (unversioned/ignored/out-of-date excluded).
func (it *Item) Changed() bool {
	switch it.Kind() {
	case KindModified, KindAdded, KindDeleted, KindMissing,
		KindConflicted, KindTreeConflicted, KindObstructed, KindReplaced:
		return true
	}
	return it.PropStatus == 'M' || it.PropStatus == 'C'
}

type statusXML struct {
	XMLName xml.Name `xml:"status"`
	Target  struct {
		Path  string `xml:"path,attr"`
		Entry []struct {
			Path     string `xml:"path,attr"`
			WCStatus struct {
				Item      string `xml:"item,attr"`
				Revision  int64  `xml:"revision,attr"`
				Props     string `xml:"props,attr"`
				Copied    bool   `xml:"copied,attr"`
				Switched  bool   `xml:"switched,attr"`
				TreeConfl bool   `xml:"tree-conflicted,attr"`
				Lock      struct {
					Token string `xml:"token,attr"`
				} `xml:"lock"`
			} `xml:"wc-status"`
			// repos-status is a SIBLING of wc-status, not a child —
			// svn prints it after wc-status inside the same entry.
			ReposStatus struct {
				Item  string `xml:"item,attr"`
				Props string `xml:"props,attr"`
				Lock  struct {
					Owner string `xml:"owner,attr"`
				} `xml:"lock"`
			} `xml:"repos-status"`
		} `xml:"entry"`
		Against struct {
			Rev int64 `xml:"revision,attr"`
		} `xml:"against"`
	} `xml:"target"`
}

// Status runs `svn status --xml` on the WC root. showUpdates adds the
// server-side repos-status (what update would bring) — IDEA's Incoming.
// Ignored paths don't appear by default; Ignored() fetches them.
func (w *WC) Status(ctx context.Context, showUpdates bool) ([]Item, error) {
	args := []string{"status", "--xml"}
	if showUpdates {
		args = append(args, "-u")
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	var x statusXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	var out []Item
	for _, e := range x.Target.Entry {
		ws := e.WCStatus
		rs := e.ReposStatus
		it := Item{
			Path:          e.Path,
			TextStatus:    statusOf(ws.Item),
			PropStatus:    statusOf(ws.Props),
			WCRev:         ws.Revision,
			Copied:        ws.Copied,
			Switched:      ws.Switched,
			TreeConflict:  ws.TreeConfl,
			Locked:        ws.Lock.Token != "" || ws.Item == "locked",
			ReposTextStatus: statusOf(rs.Item),
			ReposPropStatus: statusOf(rs.Props),
			ReposLocked:     rs.Lock.Owner != "",
			OOD:             rs.Item != "" && rs.Item != "none" && rs.Item != "normal",
		}
		out = append(out, it)
	}
	return out, nil
}

// Ignored lists paths svn ignores (`svn status --no-ignore`): the
// `I` rows the default status output drops. AddIgnore'd files land
// here right away — before the prop commit even happens.
func (w *WC) Ignored(ctx context.Context) ([]Item, error) {
	_, ignored, err := w.StatusAll(ctx, false)
	return ignored, err
}

// StatusAll runs ONE `svn status --xml --no-ignore` tree walk and splits
// the entries into changed vs ignored. Status+Ignored used to walk the
// working copy twice per refresh; the merged pass halves the fs crawl
// (status dominates refresh cost on big WCs).
func (w *WC) StatusAll(ctx context.Context, showUpdates bool) (changed, ignored []Item, err error) {
	args := []string{"status", "--xml", "--no-ignore"}
	if showUpdates {
		args = append(args, "-u")
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, nil, err
	}
	var x statusXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, nil, err
	}
	for _, e := range x.Target.Entry {
		ws := e.WCStatus
		rs := e.ReposStatus
		it := Item{
			Path:            e.Path,
			TextStatus:      statusOf(ws.Item),
			PropStatus:      statusOf(ws.Props),
			WCRev:           ws.Revision,
			Copied:          ws.Copied,
			Switched:        ws.Switched,
			TreeConflict:    ws.TreeConfl,
			Locked:          ws.Lock.Token != "" || ws.Item == "locked",
			ReposTextStatus: statusOf(rs.Item),
			ReposPropStatus: statusOf(rs.Props),
			ReposLocked:     rs.Lock.Owner != "",
			OOD:             rs.Item != "" && rs.Item != "none" && rs.Item != "normal",
		}
		if it.TextStatus == 'I' {
			ignored = append(ignored, it)
			continue
		}
		changed = append(changed, it)
	}
	return changed, ignored, nil
}

func statusOf(s string) Status {
	if s == "" {
		return ' '
	}
	switch s {
	case "none", "normal", "unversioned", "modified", "added", "deleted",
		"conflicted", "missing", "obstructed", "replaced", "external",
		"ignored", "incomplete", "locked":
		return statusByte(s)
	}
	return Status(s[0])
}

func statusByte(s string) Status {
	switch s {
	case "none", "normal":
		return ' '
	case "modified":
		return 'M'
	case "added":
		return 'A'
	case "deleted":
		return 'D'
	case "conflicted":
		return 'C'
	case "missing":
		return '!'
	case "obstructed":
		return '~'
	case "replaced":
		return 'R'
	case "external":
		return 'X'
	case "ignored":
		return 'I'
	case "locked":
		return 'L'
	default:
		return '?'
	}
}
