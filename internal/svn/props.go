package svn

import (
	"context"
	"encoding/xml"
	"path"
	"strings"
)

// IgnoreEntry is one directory's svn:ignore patterns — svn scatters
// ignore rules across directory properties instead of a gitignore file,
// so the view lists (dir → patterns) pairs.
type IgnoreEntry struct {
	Dir      string   // WC-relative dir holding the property ("" = root)
	Patterns []string // svn:ignore globs
}

type propgetXML struct {
	XMLName xml.Name `xml:"properties"`
	Target  []struct {
		Path     string `xml:"path,attr"`
		Property []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:",chardata"`
		} `xml:"property"`
	} `xml:"target"`
}

// Ignores runs `svn propget svn:ignore -R --xml` — every directory's
// ignore list in one call. svn reports "property not found" on none —
// that maps to an empty result, not an error.
func (w *WC) Ignores(ctx context.Context) ([]IgnoreEntry, error) {
	res, err := w.Run(ctx, "propget", "svn:ignore", "-R", "--xml")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		// No ignore props anywhere (or an old svn without --xml) → empty.
		return nil, nil
	}
	var x propgetXML
	if err := xml.Unmarshal(res.Stdout, &x); err != nil {
		return nil, err
	}
	var out []IgnoreEntry
	for _, t := range x.Target {
		for _, p := range t.Property {
			if p.Name != "svn:ignore" {
				continue
			}
			dir := t.Path
			if dir == "." {
				dir = ""
			}
			var pats []string
			for _, line := range strings.Split(strings.TrimSpace(p.Value), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					pats = append(pats, line)
				}
			}
			out = append(out, IgnoreEntry{Dir: dir, Patterns: pats})
		}
	}
	return out, nil
}

// PropGet reads one property on path; "" when the prop isn't set.
func (w *WC) PropGet(ctx context.Context, prop, p string) (string, error) {
	res, err := w.Run(ctx, "propget", prop, pathArg(p))
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		return "", nil
	}
	return strings.TrimRight(string(res.Stdout), "\n"), nil
}

// PropSet writes one property value on path.
func (w *WC) PropSet(ctx context.Context, prop, value, p string) (*RunResult, error) {
	return w.RunOK(ctx, "propset", prop, value, pathArg(p))
}

// AddIgnore appends path's basename to its parent dir's svn:ignore —
// the svn counterpart of "add to .gitignore". The property lives on
// the DIRECTORY, so ignoring a/b/c sets svn:ignore "c" on a/b.
func (w *WC) AddIgnore(ctx context.Context, p string) (*RunResult, error) {
	dir, name := path.Split(strings.TrimSuffix(p, "/"))
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	cur, err := w.PropGet(ctx, "svn:ignore", dir)
	if err != nil {
		return nil, err
	}
	// Skip if the pattern (or a glob covering it) is already listed.
	for _, line := range strings.Split(cur, "\n") {
		if strings.TrimSpace(line) == name {
			return nil, nil
		}
	}
	next := name
	if strings.TrimSpace(cur) != "" {
		next = strings.TrimRight(cur, "\n") + "\n" + name
	}
	return w.PropSet(ctx, "svn:ignore", next+"\n", dir)
}

// Unignore removes the svn:ignore pattern that makes path show as
// ignored: checks the path's parent dir (and each ancestor) for a
// pattern matching the basename or the dir-relative remainder, and
// deletes the first hit. Returns nil when nothing matched — the path
// may be ignored by a global config svn can't edit from here.
func (w *WC) Unignore(ctx context.Context, p string) error {
	p = strings.TrimSuffix(p, "/")
	dir, rest := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	for {
		d := dir
		if d == "" {
			d = "."
		}
		cur, err := w.PropGet(ctx, "svn:ignore", d)
		if err != nil {
			return err
		}
		var keep []string
		matched := false
		for _, line := range strings.Split(cur, "\n") {
			t := strings.TrimSpace(line)
			if t == "" {
				continue
			}
			// A pattern matches if it equals the basename/remainder or
			// a glob svn would expand to it ("*.log" covers "x.log").
			if !matched && (t == rest || globMatch(t, rest)) {
				matched = true
				continue
			}
			keep = append(keep, t)
		}
		if matched {
			res, err := w.PropSet(ctx, "svn:ignore", strings.Join(keep, "\n")+"\n", d)
			if err == nil && res != nil && !res.ExitOK() {
				return &Error{Args: res.Args, Stderr: res.OutputOfStderr()}
			}
			return err
		}
		if dir == "" {
			return nil // reached the root — nothing to remove
		}
		var parent string
		parent, rest = path.Split(strings.TrimSuffix(dir, "/"))
		dir = strings.TrimSuffix(parent, "/")
	}
}

// globMatch is the minimal fnmatch svn:ignore understands: '*' spans
// any run (no path separator concept in a basename pattern), '?' one
// char, '[...]' a class — or the literal string when no metachars.
func globMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
