package svn

import (
	"context"
	"encoding/xml"
	"strings"
)

// BlameLine is one annotated source line — rev + author + content,
// the same shape git's blame produces so the file-history overlay can
// render both backends through one view.
type BlameLine struct {
	Rev    int64
	Author string
	Text   string // the source line
}

type blameXML struct {
	XMLName xml.Name `xml:"blame"`
	Target  struct {
		Entry []struct {
			LineNo int64 `xml:"line-number,attr"`
			Commit struct {
				Rev    int64  `xml:"revision,attr"`
				Author string `xml:"author"`
			} `xml:"commit"`
		} `xml:"entry"`
	} `xml:"target"`
}

// Blame runs `svn blame --xml` on path — per-line rev + author only;
// svn's XML carries no line text. svn refuses to blame a locally
// modified file, so the annotations always describe BASE.
func (w *WC) Blame(ctx context.Context, path string) ([]BlameLine, error) {
	res, err := w.RunOK(ctx, "blame", "--xml", pathArg(path))
	if err != nil {
		return nil, err
	}
	var x blameXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	out := make([]BlameLine, 0, len(x.Target.Entry))
	for _, e := range x.Target.Entry {
		out = append(out, BlameLine{
			Rev:    e.Commit.Rev,
			Author: e.Commit.Author,
		})
	}
	return out, nil
}

// BlameWithText stitches Blame with the BASE content (which is what the
// annotations index — never the edited disk file). Lines come back with
// Text filled; a failed CatBase still yields rev/author rows.
func (w *WC) BlameWithText(ctx context.Context, path string) ([]BlameLine, error) {
	lines, err := w.Blame(ctx, path)
	if err != nil {
		return nil, err
	}
	content, err := w.CatBase(ctx, path)
	if err != nil {
		return lines, nil // annotations still useful without text
	}
	parts := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for i := range lines {
		if i < len(parts) {
			lines[i].Text = parts[i]
		}
	}
	return lines, nil
}
