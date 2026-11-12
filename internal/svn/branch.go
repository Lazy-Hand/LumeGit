package svn

import (
	"context"
	"encoding/xml"
)

// BranchOps covers trunk/branches/tags — SVN's only "branch" concept is
// a directory convention; operations are svn copy / svn switch.

// Copy creates a branch/tag — `svn copy <src> <dst> -m msg`. src/dst may
// be WC paths or URLs; server-side copies (URL→URL) don't touch the WC.
func (w *WC) Copy(ctx context.Context, src, dst, msg string) (*RunResult, error) {
	return w.RunOK(ctx, "copy", pathArg(src), pathArg(dst), "-m", msg)
}

// CopyRev creates a branch/tag pinned to a revision — `svn copy -r N`.
// A zero rev keeps current HEAD, matching Copy.
func (w *WC) CopyRev(ctx context.Context, src, dst, msg string, rev int64) (*RunResult, error) {
	args := []string{"copy"}
	if rev > 0 {
		args = append(args, "-r", itoa(rev))
	}
	args = append(args, pathArg(src), pathArg(dst), "-m", msg)
	return w.RunOK(ctx, args...)
}

// Switch points the WC at another URL — SVN's "checkout branch".
func (w *WC) Switch(ctx context.Context, url string, o UpdateOptions) (*UpdateResult, error) {
	args := []string{"switch", pathArg(url)}
	if o.Rev > 0 {
		args = append(args, "-r", itoa(o.Rev))
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
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseUpdate(res.Output()), nil
}

// ListURL lists a repository directory — the branch/tag menu reads
// `branches/*` and `tags/*` via this.
func (w *WC) ListURL(ctx context.Context, url, depth string) ([]string, error) {
	if depth == "" {
		depth = "immediates"
	}
	res, err := w.RunOK(ctx, "list", "--xml", "--depth", depth, pathArg(url))
	if err != nil {
		return nil, err
	}
	return parseListXML(res.Stdout)
}

func parseListXML(b []byte) ([]string, error) {
	var x struct {
		Lists []struct {
			Entry []struct {
				Kind string `xml:"kind,attr"`
				Name string `xml:"name"`
			} `xml:"entry"`
		} `xml:"list"`
	}
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, err
	}
	var out []string
	for _, l := range x.Lists {
		for _, e := range l.Entry {
			out = append(out, e.Name)
		}
	}
	return out, nil
}
