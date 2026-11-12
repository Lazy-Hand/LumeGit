package svn

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ChangedFile is one file-level change between two revs (--summarize)
// or inside one revision (-c).
type ChangedFile struct {
	Path   string // repo-relative
	Status Status // A/M/D
}

type diffXML struct {
	XMLName xml.Name `xml:"diff"`
	Paths   struct {
		Path []struct {
			Item string `xml:"item,attr"`
			Props string `xml:"props,attr"`
			Text string `xml:",chardata"`
		} `xml:"path"`
	} `xml:"paths"`
}

// DiffFiles lists files changed between rev from and to on path.
// "-c N" diffs rN-1→rN inside one revision — the log-row file list.
func (w *WC) DiffFiles(ctx context.Context, from, to int64, path string) ([]ChangedFile, error) {
	args := []string{"diff", "--summarize", "--xml",
		"-r", fmt.Sprintf("%d:%d", from, to)}
	if path != "" {
		args = append(args, "--", path)
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseDiffXML(res.Stdout)
}

// DiffFilesRev lists files one revision changed: -c N == -r N-1:N.
func (w *WC) DiffFilesRev(ctx context.Context, rev int64, path string) ([]ChangedFile, error) {
	args := []string{"diff", "--summarize", "--xml", "-c", fmt.Sprint(rev)}
	if path != "" {
		args = append(args, "--", path)
	}
	res, err := w.RunOK(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseDiffXML(res.Stdout)
}

func parseDiffXML(b []byte) ([]ChangedFile, error) {
	var x diffXML
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, err
	}
	var out []ChangedFile
	for _, p := range x.Paths.Path {
		out = append(out, ChangedFile{
			Path:   strings.TrimSpace(p.Text),
			Status: statusOf(p.Item),
		})
	}
	return out, nil
}

// DiffText is a unified-diff payload for one file between revs — the
// right pane renders it as old/new text, not hunks.
type DiffText struct {
	Path     string
	OldLabel string
	NewLabel string
	Old      string // content at 'from' ("" when added)
	New      string // content at 'to'   ("" when deleted)
}

// Cat fetches a file's content at a rev — the way a diff pane's sides
// are filled without parsing unified-diff output.
func (w *WC) Cat(ctx context.Context, path string, rev int64) (string, error) {
	args := []string{"cat", "-r", fmt.Sprint(rev)}
	if rev > 0 {
		args = append(args, pathArg(path))
	} else {
		args = append(args, path)
	}
	res, err := w.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		// Only "path/rev not found" is an empty side; everything else
		// (auth, network, repo gone) must surface as an error rather
		// than a blank diff that misreads as an added/deleted file.
		if svnCatNotFound(res.OutputOfStderr()) {
			return "", nil
		}
		return "", &Error{Args: args, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return string(res.Stdout), nil
}

// svnCatNotFound reports whether stderr is svn's "path/rev not found"
// family (E150000/E160013/E195012/E200009…) rather than a real failure.
func svnCatNotFound(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, sig := range []string{
		"e150000", "e160013", "e195012", "e200009", "e155010",
		"non-existent", "not found", "no such", "path not found",
		"doesn't exist", "does not exist",
	} {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}

// Peg appends "@" to pin a path's peg revision — the exported form of
// pathArg for callers outside this package (the compare overlay).
func Peg(p string) string { return pathArg(p) }

// CatURL reads a file's content at a repo URL (with optional @rev in
// the URL itself) — the dir-ref side of an svn compare.
func (w *WC) CatURL(ctx context.Context, url string) (string, error) {
	res, err := w.Run(ctx, "cat", url)
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		if svnCatNotFound(res.OutputOfStderr()) {
			return "", nil // path absent on that side → empty side
		}
		return "", &Error{Args: []string{"cat", url}, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return string(res.Stdout), nil
}

// CatBase reads the pristine BASE version of path — the old side of a
// worktree diff. Unversioned or scheduled-add paths have no BASE: "".
func (w *WC) CatBase(ctx context.Context, path string) (string, error) {
	res, err := w.Run(ctx, "cat", "-r", "BASE", pathArg(path))
	if err != nil {
		return "", err
	}
	if !res.ExitOK() {
		if svnCatNotFound(res.OutputOfStderr()) {
			return "", nil
		}
		return "", &Error{Args: []string{"cat", "-r", "BASE", path}, Stderr: strings.TrimSpace(res.OutputOfStderr())}
	}
	return string(res.Stdout), nil
}

// FileDiffWorktree returns BASE vs the on-disk bytes for path — the
// commit pane's "what would I commit" diff. A missing worktree file
// (deleted) reads as "".
func (w *WC) FileDiffWorktree(ctx context.Context, path string) (*DiffText, error) {
	oldT, err := w.CatBase(ctx, path)
	if err != nil {
		return nil, err
	}
	newT := ""
	if b, err := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(path))); err == nil {
		newT = string(b)
	}
	return &DiffText{
		Path:     path,
		OldLabel: "BASE",
		NewLabel: "工作副本",
		Old:      oldT,
		New:      newT,
	}, nil
}

// FileDiffRev returns old/new text for path between from and to — the
// two sides a diff view needs. cat on a missing side yields "".
// to < 0 means the worktree file itself.
func (w *WC) FileDiffRev(ctx context.Context, path string, from, to int64) (*DiffText, error) {
	oldT, err := w.Cat(ctx, path, from)
	if err != nil {
		return nil, err
	}
	var newT string
	if to < 0 {
		if b, rerr := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(path))); rerr == nil {
			newT = string(b)
		}
	} else {
		newT, err = w.Cat(ctx, path, to)
		if err != nil {
			return nil, err
		}
	}
	newLabel := fmt.Sprintf("r%d", to)
	if to < 0 {
		newLabel = "工作副本"
	}
	return &DiffText{
		Path:     path,
		OldLabel: fmt.Sprintf("r%d", from),
		NewLabel: newLabel,
		Old:      oldT,
		New:      newT,
	}, nil
}
