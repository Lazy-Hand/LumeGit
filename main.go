// LumeGit: a desktop git client in the IntelliJ IDEA spirit, built on
// MyGo's native UI. Entry point. The commit workspace lives in
// commitview.go (view) and commitws.go (state, background work).
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	upd "github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"lumegit/internal/store"
)

// appCTX lives in commitview.go — the shared process-lifetime context
// for background git work. appCancel ends it at quit. Individual
// windows stop only their own watchers / jobs, not this.
var appCancel context.CancelFunc

// reposStore is the shared recent/pinned repositories list persisted in
// ~/.config/lumegit/repos.json. One store.Manager serves every window;
// it serializes writes so concurrent windows can't corrupt the file.
var (
	reposStore   *store.Manager
	reposStoreMu sync.Mutex
)

// sharedRepos lazily loads the store once per process.
func sharedRepos() *store.Manager {
	reposStoreMu.Lock()
	defer reposStoreMu.Unlock()
	if reposStore == nil {
		reposStore, _ = store.Load()
	}
	return reposStore
}

// windowSeq gives each new window a distinct StateKey suffix so
// positions don't fight.
var windowSeq atomic.Int64

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	appCTX = ctx
	appCancel = cancel

	// Sparkle-style update window: background daily check + "检查更新…"
	// menu item. Dev builds skip; the plugin no-ops when the build can't
	// update itself.
	mygo.Use(updaterui.Plugin)

	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: "LumeGit", Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				upd.MenuItem(),
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Label: "文件", Submenu: []*mygo.MenuItem{
				{Label: "新建窗口", Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) {
					spawnWindow("")
				}},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		spawnWindow("")
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// spawnWindow creates one independent workspace window. A non-empty
// path opens that repo immediately; empty shows the picker.
func spawnWindow(openPath string) {
	a := &app{}
	a.filesPaneW = 280
	a.histListW = 380
	a.histFilesW = 260
	a.shelfListW = 300
	a.shelfFileW = 240
	a.branchListW = 320
	a.commit.repos = sharedRepos()
	if openPath != "" {
		a.pickPath = openPath
	}

	title := "LumeGit"
	if openPath != "" {
		title = fmt.Sprintf("%s — LumeGit", baseName(openPath))
	}
	seq := windowSeq.Add(1)
	stateKey := "main"
	if seq > 1 {
		stateKey = fmt.Sprintf("main-%d", seq)
	}

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     title,
		Width:     1080,
		Height:    720,
		MinWidth:  720,
		MinHeight: 480,
		StateKey:  stateKey,
		Content:   ui.View(a.view),
	})
	a.win = win
	a.winUpdate = win.Update
	a.commit.publish = win.Update

	win.OnClosed(func() {
		// Per-window cleanup only: stop its fs watcher and in-flight git
		// work. appCTX / appCancel belong to the process and outlive any
		// one window; the shared repos store is left alone too.
		a.commit.stopWatch()
		if a.commit.deadCtx != nil {
			a.commit.deadCtx()
		}
	})

	if openPath != "" {
		// Jump straight into the repo: the picker's open path runs the
		// same openRepo flow on this window's goroutine.
		a.openPickedRepo()
	}
}
