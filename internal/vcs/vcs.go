// Package vcs is the thin VCS-agnostic layer the UI binds to. It
// detects whether a directory is a git worktree or an svn working
// copy and hands back a backend that speaks Snapshot/Diff/Log in the
// shared shape; git and svn each fill it from their own commands.
package vcs

import (
	"context"
	"fmt"
	"os/exec"

	"lumegit/internal/git"
	"lumegit/internal/svn"
)

// Kind names which system owns the working tree.
type Kind string

const (
	KindGit Kind = "git"
	KindSVN Kind = "svn"
)

// Detect inspects dir and returns which VCS manages it. git wins when
// both markers exist (a .svn inside a git worktree is metadata, not
// the live backend). svn is confirmed by `svn info` succeeding — a bare
// .svn dir alone is not a WC svn can read.
func Detect(ctx context.Context, dir string) (Kind, error) {
	if _, err := git.Open(ctx, dir); err == nil {
		return KindGit, nil
	}
	if _, err := svn.DetectSVN(ctx); err != nil {
		return "", fmt.Errorf("不是 git 仓库，且 svn 命令不可用：%w", err)
	}
	if _, err := svn.Open(ctx, dir); err == nil {
		return KindSVN, nil
	}
	return "", fmt.Errorf("不是 git 仓库或 svn 工作副本：%s", dir)
}

// DetectFast does what Detect does without shelling out to svn — the
// picker calls it on every candidate; svn info is only consulted when
// git already failed AND a .svn dir exists, to keep open snappy.
func DetectFast(ctx context.Context, dir string) (Kind, error) {
	if _, err := git.Open(ctx, dir); err == nil {
		return KindGit, nil
	}
	if err := exec.CommandContext(ctx, "svn", "--version", "--quiet").Run(); err != nil {
		return "", fmt.Errorf("不是 git 仓库，且 svn 命令不可用：%w", err)
	}
	if _, err := svn.Open(ctx, dir); err == nil {
		return KindSVN, nil
	}
	return "", fmt.Errorf("不是 git 仓库或 svn 工作副本：%s", dir)
}

// Backend is the VCS handle a workspace binds to — git.Repo or svn.WC,
// plus which kind it is so views can show rev-vs-SHA, update-vs-pull.
type Backend struct {
	Kind Kind
	Git  *git.Repo // non-nil when Kind == KindGit
	SVN  *svn.WC   // non-nil when Kind == KindSVN
	Root string    // absolute working-copy root
}

// Open detects the VCS and returns the matching backend.
func Open(ctx context.Context, dir string) (*Backend, error) {
	k, err := Detect(ctx, dir)
	if err != nil {
		return nil, err
	}
	switch k {
	case KindGit:
		r, err := git.Open(ctx, dir)
		if err != nil {
			return nil, err
		}
		return &Backend{Kind: k, Git: r, Root: r.Worktree}, nil
	case KindSVN:
		w, err := svn.Open(ctx, dir)
		if err != nil {
			return nil, err
		}
		return &Backend{Kind: k, SVN: w, Root: w.Root}, nil
	}
	return nil, fmt.Errorf("unsupported vcs kind %q", k)
}
