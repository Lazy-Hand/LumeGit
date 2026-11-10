# LumeGit — IDEA 对齐路线图 · P0 阶段

目标：把 IDEA Git 工具窗口中**缺失的核心功能**全部补齐，严格对齐 IDEA 的概念和交互。以下是 P0 五项的实现计划（按依赖顺序）。

---

## 1. 标签（Tags）

IDEA 对应：
- `Git → New Tag…`、Log 右键 → `New Tag…`
- Branches 弹窗里独立的 **Tags** 分组（本地标签列表，含 checkout / push / delete）
- Push 对话框中的 `Push tags` 复选框
- Log 里提交行显示 tag 徽标

实现：
- `internal/git/tag.go`：
  - `Tags(ctx)` — `git tag -l --format=...` 列出本地标签（名称、指向 OID、注释）
  - `CreateTag(ctx, name, target, message, annotated)` — `git tag [-a -m msg] name [target]`
  - `DeleteTag(ctx, name)` — `git tag -d`
  - `PushTag(ctx, remote, name)` / `PushAllTags(ctx, remote)` — `git push <remote> <name>` / `--tags`
- UI：
  - 分支 tab 增加「标签」分组（与 本地/远程 并列）：右键菜单 = 检出 / 推送到远程 / 删除
  - 历史 log 行右键新增「新建标签…」（对选中提交打标）
  - 顶部 Git 菜单入口 + 「新建标签」对话框（名称、目标提交默认 HEAD、附注信息）
  - Push 对话框（见 §3）加 `同时推送标签` 复选框
  - log 行 refs 已会显示 tag 名（`git log --decorate` 已含 tag: 前缀，解析端确认即可）

## 2. 交互式 Rebase

IDEA 对应：Log 行右键 → `Interactively Rebase from Here…`，弹出提交清单，每条可改 action：Pick / Reword / Edit / Squash / Fixup / Drop，支持拖动排序。

实现：
- `internal/git/rebase.go`：
  - `RebasePlan` — 列出 `target..HEAD` 的提交（`git log --reverse`）
  - `StartInteractiveRebase(ctx, onto string, entries []RebaseEntry)` — 用 `GIT_SEQUENCE_EDITOR` 注入改写好的 todo 文件 + `GIT_EDITOR=true` 让 rebase 无编辑跑完；reword 时用 `git rebase` + `--autostash`，或对 reword 项走后处理 `git commit --amend`
  - rebase 中途冲突：复用现有 `Continue/Abort`（`rebase --continue/--abort`）
- UI：
  - Log 右键新增「从此提交交互式变基…」→ 打开 rebase 计划面板（列表 + 每行下拉选 action + 上下移按钮）
  - 确认后执行；冲突时沿用现有冲突解决 overlay，完成后提示

## 3. Push 对话框

IDEA 对应：`Push` 弹出对话框 —— 显示当前分支 → 远程分支、将推送的提交列表、`Force push` 复选框（force-with-lease）、`Push tags`。

实现：
- `internal/git`：
  - `UnpushedCommits(ctx)` — `git log @{u}..HEAD --oneline`（无 upstream 时列全部）
  - `PushRequest` 增加 `Tags bool`
- UI：
  - 点「推送」不再直推：打开 modal，显示 remote、本地分支、ahead 提交列表（最多 N 条）、复选框：强制推送（--force-with-lease）、推送标签
  - 提交并推送按钮同样走此对话框
- Amend 防护合入此项：HEAD 是 amend 产物且远端已含旧提交时，对话框默认勾选/提示需要 force-with-lease（`git status` ahead/behind 分叉即提示）

## 4. Hunk 级部分提交

IDEA 对应：勾选文件后在 diff 窗格逐块勾选要提交的块（checkbox per hunk）。

实现：
- `internal/git`：
  - `DiffHunks(ctx, path)` — `git diff -- path` 解析出 hunk 边界（已有一部分 diff 解析，复用 `DiffInfo`）
  - 提交路径扩展：选中的 hunk 集合 → 构造临时 patch（保留 `@@` 头并按选中行重算）→ `git apply --cached` → commit → `git reset`（保留 index 其他内容）
  - 这是 P0 中最复杂的一项，放最后做；先支持「按 hunk 勾选 tracked 修改」，untracked/新文件仍按整文件
- UI：diff 窗格在每个 hunk 头加 checkbox（仅提交 tab 且文件已勾选时显示）

## 5. Amend 后推送防护

并入 §3：Push 对话框检测 `@{u}...HEAD` 出现分叉（既有 ahead 又有 behind 且历史被改写）→ 强制推送提示 + 只允许 `--force-with-lease`，杜绝裸 `--force`。

---

## 实施顺序

1. **标签**（独立、后端+UI 小）
2. **Push 对话框 + Amend 防护**（复用现有 `Push`，主要是 UI + unpushed log；tag 推送复选框依赖 1 的后端）
3. **交互式 Rebase**（后端驱动 + 计划面板 UI）
4. **Hunk 级部分提交**（最重，最后做）

每一项完成后：gofmt + build + vet + `go test ./...`，并对关键路径补 headless 测试（沿用 `ui.NewTester`）。
