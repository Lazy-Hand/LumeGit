package svn

import (
	"context"
	"encoding/xml"
	"strings"
)

// Info is `svn info --xml` on a working-copy path.
type Info struct {
	Path        string // absolute path queried
	Revision    int64  // BASE revision of that path
	URL         string // this path's URL in the repo
	Root        string // repository root URL
	UUID        string
	NodeKind    string // file | dir
	Depth       string // infinity|empty|files|immediates|unknown
	LastRev     int64
	LastAuthor  string
	LastDate    string
	RelativeURL string // ^/branches/foo — the layout a branch menu needs
}

type infoXML struct {
	XMLName xml.Name `xml:"info"`
	Entry   struct {
		Path     string `xml:"path,attr"`
		Rev      int64  `xml:"revision,attr"`
		Kind     string `xml:"kind,attr"`
		URL      string `xml:"url"`
		RelURL   string `xml:"relative-url"`
		Repo     struct {
			Root string `xml:"root"`
			UUID string `xml:"uuid"`
		} `xml:"repository"`
		WC struct {
			Depth string `xml:"depth,attr"`
		} `xml:"wc-info"`
		Commit struct {
			Rev    int64  `xml:"revision,attr"`
			Author string `xml:"author"`
			Date   string `xml:"date"`
		} `xml:"commit"`
	} `xml:"entry"`
}

// Info queries svn info on the WC root.
func (w *WC) Info(ctx context.Context) (*Info, error) {
	return w.InfoPath(ctx, "")
}

// InfoPath queries svn info on path relative to the WC root.
func (w *WC) InfoPath(ctx context.Context, path string) (*Info, error) {
	args := []string{"info", "--xml"}
	if path != "" {
		args = append(args, pathArg(path))
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	var x infoXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	return &Info{
		Path:        x.Entry.Path,
		Revision:    x.Entry.Rev,
		URL:         x.Entry.URL,
		Root:        x.Entry.Repo.Root,
		UUID:        x.Entry.Repo.UUID,
		NodeKind:    x.Entry.Kind,
		Depth:       x.Entry.WC.Depth,
		LastRev:     x.Entry.Commit.Rev,
		LastAuthor:  x.Entry.Commit.Author,
		LastDate:    x.Entry.Commit.Date,
		RelativeURL: strings.TrimSpace(x.Entry.RelURL),
	}, nil
}

// InfoURL queries a repository URL directly (no working copy needed).
// A WC is still the call site — svn wants a cwd to probe config — so
// NewWC("/") works as a throwaway.
func InfoURL(ctx context.Context, url string) (*Info, error) {
	res, err := NewWC("/").RunOK(ctx, "info", "--xml", pathArg(url))
	if err != nil {
		return nil, err
	}
	var x infoXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	return &Info{
		Path:        x.Entry.Path,
		Revision:    x.Entry.Rev,
		URL:         x.Entry.URL,
		Root:        x.Entry.Repo.Root,
		UUID:        x.Entry.Repo.UUID,
		NodeKind:    x.Entry.Kind,
		LastRev:     x.Entry.Commit.Rev,
		LastAuthor:  x.Entry.Commit.Author,
		LastDate:    x.Entry.Commit.Date,
		RelativeURL: strings.TrimSpace(x.Entry.RelURL),
	}, nil
}
