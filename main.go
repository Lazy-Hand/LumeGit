// LumeGit: a desktop git client in the IntelliJ IDEA spirit, built on
// MyGo's native UI. Entry point. The commit workspace lives in
// commitview.go (view) and commitws.go (state, background work).
package main

import (
	"context"
	"log"

	"github.com/egoist/mygo"
	upd "github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"lumegit/internal/store"
)

// app is the application state (declared fully in commitview.go).

// shouldStopApp cancels background git work at quit. Assigned in main.
var appCancel context.CancelFunc

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	appCTX = ctx
	appCancel = cancel

	a := &app{}
	a.filesPaneW = 280
	a.histListW = 380
	a.histFilesW = 260
	a.shelfListW = 300
	a.shelfFileW = 240
	a.branchListW = 320
	// Recent/pinned repository list; a failed load just starts empty.
	a.commit.repos, _ = store.Load()

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
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "LumeGit",
			Width:     1080,
			Height:    720,
			MinWidth:  720,
			MinHeight: 480,
			// Opens where the user left it last time.
			StateKey: "main",
			Content:  ui.View(a.view),
		})
		a.win = win
		a.winUpdate = win.Update
		// The model publishes through the same channel: background
		// git jobs post their results onto the main thread here.
		a.commit.publish = win.Update
		// Stop in-flight background git work when the window closes.
		off := win.OnClosed(func() {
			if appCancel != nil {
				appCancel()
			}
		})
		_ = off
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
