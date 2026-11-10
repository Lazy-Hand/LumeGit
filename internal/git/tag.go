package git

import (
	"context"
	"strings"
)

// Tag is one local tag ref.
type Tag struct {
	Name      string // short name ("v1.0")
	OID       string // object the tag points at (commit for lightweight; tag object for annotated)
	Annotated bool   // -a tag (has its own object + message)
	Message   string // annotation subject, if annotated
}

// Tags lists local tags (IDEA shows them as their own group in the
// branches popup). Remote tags are only fetched, not listed locally —
// like IDEA, the list is the local view.
func (r *Repo) Tags(ctx context.Context) ([]Tag, error) {
	res, err := r.Run(ctx, "tag", "--list",
		"--format=%(refname:short)%1f%(objectname:short)%1f%(objecttype)%1f%(subject)%1e")
	if err != nil {
		return nil, err
	}
	if !res.ExitOK() {
		return nil, &GitError{Args: []string{"tag", "--list"}, Stderr: res.OutputOfStderr()}
	}
	var tags []Tag
	for _, rec := range strings.Split(res.Output(), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 3 {
			continue
		}
		t := Tag{Name: f[0], OID: f[1]}
		if f[2] == "tag" {
			// Annotated tags point at a tag object; %(subject) is the
			// annotation's first line.
			t.Annotated = true
			if len(f) > 3 {
				t.Message = f[3]
			}
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// CreateTag creates a tag at target (defaults to HEAD). message makes
// it annotated (-a -m), matching IDEA's "Annotated" checkbox.
func (r *Repo) CreateTag(ctx context.Context, name, target, message string) (*RunResult, error) {
	args := []string{"tag"}
	if message != "" {
		args = append(args, "-a", "-m", message)
	}
	args = append(args, name)
	if target != "" {
		args = append(args, target)
	}
	return r.RunOK(ctx, args...)
}

// DeleteTag removes a local tag (git tag -d).
func (r *Repo) DeleteTag(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "tag", "-d", name)
}

// PushTag pushes one tag to a remote (git push <remote> <name>).
func (r *Repo) PushTag(ctx context.Context, remote, name string) (*RunResult, error) {
	if remote == "" {
		remote = "origin"
	}
	return r.RunOK(ctx, "push", remote, "refs/tags/"+name)
}

// CheckoutTag checks out a tag (detached HEAD), matching IDEA's
// "Checkout" on a tag.
func (r *Repo) CheckoutTag(ctx context.Context, name string) (*RunResult, error) {
	return r.RunOK(ctx, "switch", "--detach", "tags/"+name)
}
