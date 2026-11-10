package main

import (
	"context"

	"lumegit/internal/git"
)

// reflogModel backs the "Reflog — HEAD 操作历史" overlay: the action
// journal IDEA exposes via Git → Reflog.
type reflogModel struct {
	open    bool
	entries []git.ReflogEntry
	loading bool
	err     string
	gen     int
}

// openFor loads HEAD's reflog.
func (m *reflogModel) openFor(repoPath string, w updater) {
	m.open = true
	m.gen++
	gen := m.gen
	m.loading = true
	m.err = ""
	go func() {
		repo := git.NewRepo(repoPath)
		list, err := repo.Reflog(context.Background(), "HEAD", 200)
		w.update(func() {
			if gen != m.gen {
				return
			}
			m.loading = false
			if err != nil {
				m.err = err.Error()
				return
			}
			m.entries = list
		})
	}()
}

func (m *reflogModel) close() { m.open = false }
