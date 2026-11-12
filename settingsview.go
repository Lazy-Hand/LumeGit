package main

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/theme"

	"lumegit/internal/git"
)

// settingsView is the 偏好设置 modal (LumeGit → 偏好设置…, CmdOrCtrl+,).
// It edits the live app fields directly — a.saveSettings persists them
// when the window closes — plus a "reset layout" that restores the
// built-in defaults immediately.
func (a *app) settingsView(c *ui.Context, t *ui.Theme) {
	if !a.gitIdentityLoaded {
		a.loadGitIdentity()
	}
	feedback.Modal(c, &a.settingsOpen, feedback.ModalProps{
		Title:    "偏好设置",
		Width:    480,
		OkText:   "完成",
		NoFooter: false,
	}, func() {
		ui.Column(c).Gap(14).Children(func() {
			// --- Git 身份 ---
			a.gitIdentitySection(c, t)
			// --- 差异查看 ---
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "差异查看").FontSize(12).Bold()
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "默认布局").FontSize(12).TextColor(t.TextMuted).Width(90)
					action.Segmented(c, &a.diffMode, []string{"并排", "统一"}, action.SegmentedProps{Size: theme.Small})
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "词级高亮").FontSize(12).TextColor(t.TextMuted).Width(90)
					inline := a.diffInlineOn()
					choice.Checkbox(c, &inline, "大文件内自动关闭以保持流畅", choice.CheckboxProps{Size: 13}).
						Disabled(true)
				})
			})
			// --- 布局 ---
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "布局").FontSize(12).Bold()
				ui.Text(c, "面板宽度和差异分隔比例在拖动时自动保存。").
					FontSize(11).TextColor(t.TextMuted)
				action.Button(c, "重置面板布局", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
					OnClick(func() {
						a.filesPaneW, a.histListW, a.histFilesW = 280, 380, 260
						a.shelfListW, a.shelfFileW = 300, 240
						a.diffSplit = 0 // 0 = DiffState's own half default
						a.saveSettings()
						a.notify("success", "面板布局已重置")
					})
			})
		})
	})
}

// diffInlineOn reports whether word-level inline highlight is enabled.
// There is no switch today — huge diffs auto-degrade via newDiffState —
// so this surfaces as a read-only note until a real toggle exists.
func (a *app) diffInlineOn() bool { return true }

// gitIdentitySection edits the repo's commit author (user.name /
// user.email). The values are loaded lazily the first time the modal
// opens, written on 保存 — empty means "unset here, inherit the global
// gitconfig", which is why the global pair is shown alongside as the
// placeholder hint.
func (a *app) gitIdentitySection(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Git 身份").FontSize(12).Bold()
			ui.Text(c, "（本仓库）").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, "").Grow(1)
			action.Button(c, "管理远程…", action.ButtonProps{Variant: action.Text, Size: theme.Small}).
				OnClick(func() {
					a.settingsOpen = false
					a.branches.remoteOpen = true
				})
		})
		if a.gitIdentityErr != "" {
			feedback.Alert(c, a.gitIdentityErr, feedback.AlertProps{Type: feedback.Error, Variant: feedback.Soft})
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "用户名").FontSize(12).TextColor(t.TextMuted).Width(90)
			field.Input(c, &a.gitName, field.InputProps{
				Placeholder: orStr(a.gitGlobalName, "未设置"), Size: theme.Small,
			}).Grow(1)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "邮箱").FontSize(12).TextColor(t.TextMuted).Width(90)
			field.Input(c, &a.gitEmail, field.InputProps{
				Placeholder: orStr(a.gitGlobalEmail, "未设置"), Size: theme.Small,
			}).Grow(1)
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			action.Button(c, "保存身份", action.ButtonProps{Variant: action.Default, Size: theme.Small}).
				OnClick(func() { a.saveGitIdentity() })
		})
	})
}

// loadGitIdentity fills the modal fields: local override into the
// inputs, the resolved global pair into the read-only placeholders.
// Empty repo (picker screen) leaves them blank — there's nothing to
// read or write without a worktree.
func (a *app) loadGitIdentity() {
	a.gitIdentityLoaded = true
	if a.commit.RepoPath == "" {
		return
	}
	repo := git.NewRepo(a.commit.RepoPath)
	ctx := context.Background()
	if local, err := repo.Identity(ctx, true); err == nil {
		a.gitName, a.gitEmail = local.Name, local.Email
	}
	// Effective (non-local) values minus the local ones ≈ global; git has
	// no "--global only" read that also shows the unset case, so read the
	// global scope directly.
	if global, err := repo.Identity(ctx, false); err == nil {
		a.gitGlobalName, a.gitGlobalEmail = global.Name, global.Email
	}
}

// saveGitIdentity writes the edited pair at repo scope and reports the
// outcome in the modal itself.
func (a *app) saveGitIdentity() {
	if a.commit.RepoPath == "" {
		a.gitIdentityErr = "没有打开的仓库"
		return
	}
	repo := git.NewRepo(a.commit.RepoPath)
	err := repo.SetIdentity(context.Background(), git.Identity{
		Name:  strings.TrimSpace(a.gitName),
		Email: strings.TrimSpace(a.gitEmail),
	})
	if err != nil {
		a.gitIdentityErr = err.Error()
		return
	}
	a.gitIdentityErr = ""
	a.notify("success", "已保存 Git 身份")
}

// orStr returns def when s is empty.
func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
