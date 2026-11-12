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

	"lumegit/components"
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

	settingsStore   *store.SettingsManager
	settingsStoreMu sync.Mutex
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

// sharedSettings lazily loads the preferences file once per process.
func sharedSettings() *store.SettingsManager {
	settingsStoreMu.Lock()
	defer settingsStoreMu.Unlock()
	if settingsStore == nil {
		settingsStore, _ = store.LoadSettings()
	}
	return settingsStore
}

// windowSeq gives each new window a distinct StateKey suffix so
// positions don't fight.
var windowSeq atomic.Int64

// windows maps each live *mygo.Window to its app so app-menu clicks
// (which receive the window, not the app) can reach the right window's
// settings modal.
var (
	windowsMu sync.Mutex
	windows   = map[*mygo.Window]*app{}
)

func registerWindow(w *mygo.Window, a *app) {
	windowsMu.Lock()
	windows[w] = a
	windowsMu.Unlock()
}

func unregisterWindow(w *mygo.Window) {
	windowsMu.Lock()
	delete(windows, w)
	windowsMu.Unlock()
}

func appForWindow(w *mygo.Window) *app {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	return windows[w]
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	appCTX = ctx
	appCancel = cancel

	// Sparkle-style update window: background daily check + "检查更新…"
	// menu item. Dev builds skip; the plugin no-ops when the build can't
	// update itself.
	// The updater window is forced to simplified Chinese: LumeGit's own UI
	// is Chinese-only, so following the system locale (which App.Locale
	// resolves to "en" while the app declares no localizations) would
	// show an English dialog inside a Chinese app.
	mygo.Use(updaterui.New(upd.Options{Language: "zh-Hans"}))

	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: "LumeGit", Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				upd.MenuItem(),
				mygo.Separator(),
				{Label: "偏好设置…", Accelerator: "CmdOrCtrl+,", Click: func(_ *mygo.MenuItem, w *mygo.Window) {
					if a := appForWindow(w); a != nil {
						a.settingsOpen = true
						a.update(func() {})
					}
				}},
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
// path opens that repo immediately; empty shows the picker. Tests may
// stub it via spawnWindowHook — in headless mode NewWindow would panic.
var spawnWindowHook func(path string)

func spawnWindow(openPath string) {
	if spawnWindowHook != nil {
		spawnWindowHook(openPath)
		return
	}
	a := &app{}
	// Restore persisted UI prefs first; a zero field means "first run or
	// older settings file", in which case the built-in default stands.
	a.loadSettings()
	a.commit.repos = sharedRepos()
	if openPath != "" {
		a.pickPath = openPath
	}

	title := "LumeGit"
	if openPath != "" {
		title = fmt.Sprintf("%s — LumeGit", components.BaseName(openPath))
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
	registerWindow(win, a)

	// Window focus = "user is back" — lazygit/GitHub Desktop pattern:
	// refresh immediately so external changes (a commit made in the
	// terminal while we were unfocused) are already loaded. Quiet, so
	// the banner doesn't flash on every ⌘Tab.
	win.OnFocus(func() {
		a.commit.refreshQuiet(context.Background(), a.updater())
	})

	win.OnClosed(func() {
		// Per-window cleanup only: stop its refs poller and in-flight git
		// work. appCTX / appCancel belong to the process and outlive any
		// one window; the shared repos store is left alone too.
		a.commit.stopPolling()
		if a.commit.deadCtx != nil {
			a.commit.deadCtx()
		}
		// Keep the user's pane/diff/tab choices for next launch.
		a.saveSettings()
		unregisterWindow(win)
	})

	if openPath != "" {
		// Jump straight into the repo: the picker's open path runs the
		// same openRepo flow on this window's goroutine.
		a.openPickedRepo()
	}
}
