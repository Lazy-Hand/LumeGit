package svn

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// Revision is one `svn log` entry. Revs are numeric, not SHAs — all UI
// shows rNNNN.
type Revision struct {
	Rev     int64
	Author  string
	Date    time.Time
	Message string
	// ChangedPaths is non-empty when the log was requested with -v:
	// every path the rev touched, with its action letter.
	ChangedPaths []ChangedPath
}

// ChangedPath is one affected path inside a revision.
type ChangedPath struct {
	Action   string // A|M|D|R
	Path     string // repo-relative
	CopyFrom string // for A/R with copy history
	CopyRev  int64
	Kind     string // file|dir
}

type logXML struct {
	XMLName   xml.Name `xml:"log"`
	LogEntry []struct {
		Rev    int64  `xml:"revision,attr"`
		Author string `xml:"author"`
		Date   string `xml:"date"`
		Msg    string `xml:"msg"`
		Paths  struct {
			Path []struct {
				Action string `xml:"action,attr"`
				Kind   string `xml:"kind,attr"`
				From   string `xml:"copyfrom-path,attr"`
				FromR  int64  `xml:"copyfrom-rev,attr"`
				Text   string `xml:",chardata"`
			} `xml:"path"`
		} `xml:"paths"`
	} `xml:"logentry"`
}

// LogOptions bounds a log query.
type LogOptions struct {
	Rev          string // "N", "N:M", "HEAD", "BASE" — "" = HEAD:1
	Limit        int    // -l N, 0 = no cap
	Verbose      bool   // -v: include changed-paths
	StopOnCopy   bool   // --stop-on-copy (branch lifecycle)
	Search       string // --search substring on message/author
	SearchAnd    bool   // --search-and (all must match)
	Target       string // path/URL — "" = WC root
}

// Log runs `svn log --xml`.
func (w *WC) Log(ctx context.Context, o LogOptions) ([]Revision, error) {
	args := []string{"log", "--xml"}
	if o.Verbose {
		args = append(args, "-v")
	}
	if o.Rev != "" {
		args = append(args, "-r", o.Rev)
	}
	if o.Limit > 0 {
		args = append(args, "-l", fmt.Sprint(o.Limit))
	}
	if o.StopOnCopy {
		args = append(args, "--stop-on-copy")
	}
	if o.Search != "" {
		if o.SearchAnd {
			args = append(args, "--search-and", o.Search)
		} else {
			args = append(args, "--search", o.Search)
		}
	}
	if o.Target != "" {
		args = append(args, pathArg(o.Target))
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	var x logXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	var out []Revision
	for _, e := range x.LogEntry {
		r := Revision{
			Rev:     e.Rev,
			Author:  e.Author,
			Message: strings.TrimSpace(e.Msg),
		}
		if t, err := time.Parse("2006-01-02T15:04:05.999999Z", e.Date); err == nil {
			r.Date = t
		}
		for _, p := range e.Paths.Path {
			r.ChangedPaths = append(r.ChangedPaths, ChangedPath{
				Action:   p.Action,
				Path:     strings.TrimSpace(p.Text),
				CopyFrom: p.From,
				CopyRev:  p.FromR,
				Kind:     p.Kind,
			})
		}
		out = append(out, r)
	}
	return out, nil
}
