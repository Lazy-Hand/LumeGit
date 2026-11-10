package git

import (
	"context"
	"strings"
)

// Remote is one configured git remote (name + fetch/push URLs).
type Remote struct {
	Name     string
	FetchURL string
	PushURL  string // may equal FetchURL when no separate pushurl is set
}

// Remotes lists the configured remotes (git remote -v pairs).
func (r *Repo) Remotes(ctx context.Context) ([]Remote, error) {
	res, err := r.Run(ctx, "remote", "-v")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"remote", "-v"}, Stderr: res.OutputOfStderr()}
	}
	byName := map[string]*Remote{}
	var order []string
	for _, ln := range strings.Split(res.Output(), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// "origin\thttps://… (fetch)"
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		name, url, kind := f[0], f[1], f[2]
		rm := byName[name]
		if rm == nil {
			rm = &Remote{Name: name}
			byName[name] = rm
			order = append(order, name)
		}
		if strings.Contains(kind, "fetch") {
			rm.FetchURL = url
		} else {
			rm.PushURL = url
		}
	}
	out := make([]Remote, 0, len(order))
	for _, n := range order {
		rm := byName[n]
		if rm.PushURL == "" {
			rm.PushURL = rm.FetchURL
		}
		out = append(out, *rm)
	}
	return out, nil
}

// AddRemote registers a remote (git remote add).
func (r *Repo) AddRemote(ctx context.Context, name, url string) (*RunResult, error) {
	return r.RunOK(ctx, "remote", "add", name, url)
}

// RemoveRemote unregisters a remote (git remote remove).
func (r *Repo) RemoveRemote(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "remote", "remove", name)
}

// SetRemoteURL changes a remote's fetch URL (git remote set-url).
// IDEA's Edit Remote also touches the push URL when it's the same.
func (r *Repo) SetRemoteURL(ctx context.Context, name, url string) (*RunResult, error) {
	return r.RunOK(ctx, "remote", "set-url", name, url)
}
