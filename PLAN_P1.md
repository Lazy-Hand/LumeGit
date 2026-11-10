# LumeGit — IDEA 对齐路线图 · P1 阶段

P0（标签、交互式 rebase、push 对话框、hunk 部分提交、amend 防护）已完成。
P1 补齐 IDEA 的常用工作流，仍严格对齐其概念与操作。

## 实施项（按依赖顺序）

### 1. 分支重命名
IDEA：分支右键 → Rename。后端 `RenameBranch` 已有，仅接 UI（右键菜单 + 行内输入或对话框）。

### 2. Git Remotes 管理
IDEA：`Git → Manage Remotes…` 对话框（列表 + 添加/编辑/删除 remote 名称与 URL）。
- `internal/git/remote.go`：`Remotes`（`git remote -v`）、`AddRemote`、`RemoveRemote`、`SetRemoteURL`
- UI：分支 tab 顶栏加「远程…」按钮 → modal 管理；Push 对话框后续可用 remote 名（v1 仍固定 origin）

### 3. Update Project 拉取对话框
IDEA：`Git → Update Project…`（Ctrl+T）— 选择 merge / rebase，可勾「清理前先 stash」。
- `PullRequest` 已有 `Rebase`；补 `stash` 选项（dirty 时 stash → pull → pop）
- UI：「拉取」按钮改为打开对话框（上游分支显示、merge/rebase 单选、stash 复选）

### 4. 与分支比较 / 与工作树比较
IDEA：分支右键 → `Compare with Current`（分支文件树 diff）；log 文件右键 → `Compare with Local`。
- `internal/git`：`BranchFileDiff(ctx, branch, path)` — `git diff branch -- path` 或直接两侧 cat-file
- UI：分支右键「与当前分支比较」→ 打开对比 diff（文件列表或选中文件）；历史文件行右键「与工作树比较」→ HEAD:file vs worktree

### 5. Create Patch / Apply Patch
IDEA：文件右键/提交右键 → `Create Patch…`（导出为 .patch）；`Git → Apply Patch…`（选文件应用）。
- `internal/git/patch.go`：`CreatePatch(ctx, paths|commit, dest)` — `git format-patch`（提交）或 `git diff > file`（工作区改动）；`ApplyPatch(ctx, file)` — `git apply`（工作区）/`git am`（带提交信息）
- UI：changelist 文件右键「创建补丁…」、历史提交右键「创建补丁…」、工具栏「应用补丁…」

### 6. 真 Shelf（补丁式搁置）
IDEA：Shelf 是 patch 文件（存在 `.idea/shelf/` 或 git 无关位置），可改名、跨提交应用、不占用 git stash。
- `internal/store/shelf.go`：shelf 元数据（名称、时间、patch 文件路径，存仓库 `.git/lumegit-shelf/`）
- `internal/git`：`ShelveAsPatch(ctx, paths, name)` — 生成 patch 存盘 + 回滚工作区对应改动；`Unshelve(ctx, name)` — `git apply` 还原
- UI：现有「搁置」tab 分两组：`搁置架（patch）` + `Stash`；文件右键「搁置到 Shelf…」（存 patch）与「Stash」分开

每项完成后 gofmt + build + vet + `go test ./...`，关键路径补 headless/真实 repo 测试。
