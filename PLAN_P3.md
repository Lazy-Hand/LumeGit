# LumeGit — IDEA 对齐路线图 · P3 阶段

P0（tags / 交互式 rebase / push 对话框 / hunk 部分提交 / amend 防护）、
P1（重命名 / remotes / update project / 比较 / patch / 真 shelf）、
P2（log 过滤 / 跳转 / 未推送高亮 / 提交图 / shelf UI / 子模块 / toast）已完成。
P3 补齐 IDEA Git 工具窗口中**仍然缺失**的高价值功能，仍严格对齐其概念与交互。

## 实施项（按依赖顺序）

### 1. Resolve Conflicts 对话框（列表式总览）
IDEA：`Git → Resolve Conflicts…` — 汇总所有冲突文件，列示「ours / theirs / merged」
三状态，可逐文件选择「Accept Yours / Accept Theirs / Merge…」批量处理。
- `internal/git`：新增 `ConflictList(ctx)` — `git ls-files -u` 聚合 unmerged 路径 +
  每文件冲突标记数量；`ResolveConflictsAccept(ctx, path, side)` — `git checkout --ours/--theirs -- path && git add`
- UI：提交 tab 顶部冲突时显示「解决冲突…」按钮 → Modal 列表（图标 + 路径 + 状态），
  行内按钮「取我们的」「取他们的」「手动合并」（手动合并复用现有 `conflictOverlay`），
  全部解决后允许「继续合并/变基」。

### 2. Blame / Annotate
IDEA：编辑器槽右键 → `Annotate with Git Blame`；Log 里文件右键 → `Annotate`。
- `internal/git/blame.go`（已有 `Blame` 方法？确认并扩展）：`git blame --porcelain` 解析
  作者、提交、时间、原始行号 → `[]BlameLine{OID, Author, Date, OrigLine, Content}`；
  追加 `-C -M` 支持重命名/复制跟踪（IDEA 默认开启）。
- UI：文件历史 tab / diff 面板加「注释」开关，左侧显示 blame 列（作者缩写 + 时间 +
  短 oid），点击跳到该提交的 log 行。

### 3. 分支对比（Compare Branches）
IDEA：分支右键 → `Compare with 'xxx'` — 双向列出「独有提交」+ 共同祖先以来的文件差异。
- `internal/git/compare.go` 扩展：`BranchCompare(ctx, a, b)` — `git log a..b` 和 `b..a`
  各自列出独有提交 + `git diff a...b --name-status` 文件清单（三点 = 自 merge-base 起）。
- UI：分支 tab 右键「与分支比较…」→ Modal 左右两栏「a 独有 / b 独有」提交列表 +
  底部文件差异，复用 `compareModel` 布局但支持双 ref。

### 4. 工作树 Worktree 管理
IDEA：`Git → Manage Worktrees`（2023.1+）— 列出 `git worktree list`，支持 add / remove /
  在另一 worktree 打开。
- `internal/git/worktree.go`：`Worktrees(ctx)` — `git worktree list --porcelain` 解析
  path / HEAD / branch / bare / detached；`AddWorktree(ctx, path, ref, newBranch)`；
  `RemoveWorktree(ctx, path, force)`。
- UI：分支 tab 新增「工作树」分组，行内显示路径 + 分支 + 状态；右键「在此 worktree
  打开仓库」（切 repo path）/「移除」；新建对话框（路径选择器 + 基于哪个 ref + 可选新分支名）。

### 5. Bisect 二分查找
IDEA：`Git → Bisect`（2023.3+ 集成）— start / good / bad / reset，实时显示剩余步数与
  当前候选提交。
- `internal/git/bisect.go`：`BisectStart(ctx, good, bad)` / `BisectMark(ctx, good bool)` /
  `BisectReset(ctx)` / `BisectStatus(ctx)` — 解析 `git bisect log` / `git bisect visualize`
  输出剩余估算与当前 ref。
- UI：新「二分」面板或历史 tab 右键「开始二分」：显示「好/坏」按钮 + 进度（已测 X 个、
  剩余 ~log₂N 步）+ 当前检出的提交，完成时高亮首个坏提交。

### 6. 提交前检查（Pre-commit Checks）与提交模板
IDEA：Commit 面板右侧「Checks」区：TODO 检查 / 代码清理 / Git hooks 勾选 + `git commit
  -n` 开关；提交模板可加载 `.gitmessage`。
- `internal/git`：`PrecommitChecks(ctx)` — 汇总 staged 文件的 TODO/FIXME 计数、
  `git diff --cached --check`（空白错误）、`.git/hooks/pre-commit` 是否存在；
  `CommitRequest` 增 `NoVerify bool` → `git commit -n`；`CommitTemplate(ctx)` 读
  `commit.template` / `.gitmessage`。
- UI：提交面板底部展开「检查」抽屉：勾选「跳过 git hooks」（amend 时灰掉）+
  「从模板加载」按钮；检查项失败时黄色警告但不阻断（IDEA 行为）。

### 7. 已暂存/未暂存分组的「Changelist」多选提交
IDEA：同一文件的不同 hunk 可分到不同 changelist，分别提交。我们已有 partial-commit
  hunk 勾选，扩展到跨文件：对多个文件各选 hunks，组成一个「changelist」一次提交。
- `internal/store/changelists.go` 已有 changelist 存储 — 接入：UI 允许把文件拖到命名
  changelist，提交面板按 changelist 分组展示；提交按钮按所选 changelist 过滤 paths +
  Partial hunks。
- `CommitRequest.Partial` 已支持 `map[string][]bool` — 扩展为每个文件独立 hunk 集合。

### 8. Git LFS 指示与锁
IDEA：LFS 文件在文件树带 `LFS` 徽标，`Git → LFS → Locks` 管理锁定。
- `internal/git/lfs.go`：`LFSTracked(ctx)` — `git lfs track` / `.gitattributes` 解析；
  `LFSLocks(ctx)` — `git lfs locks --json`；`LFSLock(ctx, path)` / `LFSUnlock(ctx, path, force)`；
  `HasLFS(ctx)` — `git lfs version` 探测。
- UI：文件行尾加 LFS tag（仅当被 LFS 追踪时）；分支 tab 或工具栏入口 →
  LFS Locks 对话框（路径 + 持有人 + 时间 + 解锁按钮）；未安装 git-lfs 时优雅降级
  （对话框内提示）。

### 9. Sign Commits（GPG/SSH 签名指示与验证）
IDEA：Log 行显示 GPG 验证徽标（✓ verified / ✗ failed / ? unknown）。
- `internal/git`：`Log` 请求加 `%G?` 占位符 → `Commit{Signature SigStatus}`；
  附 `SignShow(ctx, oid)` — `git verify-commit` 输出详情。
- UI：log 行右侧小图标（仅签名提交显示）；点击弹详情 Modal。

### 10. 克隆 / 初始化入口的现代化
IDEA：欢迎页 `Get from VCS` 对话框（URL + 目录 + shallow/submodule 选项）。
- `internal/git/clone.go` 已有 `Clone` — 扩展 `CloneRequest{Depth, Branch, NoCheckout,
  RecurseSubmodules}`；新增 `InitRepo(ctx, path)` — `git init`。
- UI：启动 `pickView` 三分支：「打开本地」「克隆」「新建」；克隆对话框含 URL、
  目标目录、深度、分支、--recurse-submodules 开关，进度条走 `git clone --progress`
  行解析。

### 11. Stash 高级操作
IDEA：stash 右键 → `Apply` / `Pop` / `Branch` / `Drop` / `Clear`；`Stash…` 对话框
  支持 `Keep index` / `Include untracked` / `Message`。
- `internal/git`：补 `StashClear(ctx)`、`StashShow(ctx, name)` — `git stash show -p` 完整 diff；
  `StashPush` 扩 `KeepIndex` 选项。
- UI：stash 行右键补「清空全部 stash」（确认后 `StashClear`）；
  「从这里建分支」已存在；搁置对话框加 `保留已暂存` 复选框。

### 12. Reflog 浏览
IDEA：`Git → Reflog` — 列出 HEAD 历史动作（checkout/rebase/commit/reset），
  可回滚到任一点。
- `internal/git/reflog.go`：`Reflog(ctx, ref, max)` — `git reflog show --format=...` 解析
  `{Index, OID, Action, Message}`。
- UI：新「Reflog」对话框（工具栏/Git 菜单入口）：表格（#、动作、消息、时间、短 oid），
  行右键「重置到此」/「检出」。

---

## 验收标准

每项完成后：
1. `gofmt -w . && go build ./... && go vet ./...`
2. `go test ./... -count=1` 全绿
3. UI 在真实仓库（非仅 testdata）验证一次交互

## 实施顺序建议

按「IDEA 概念对齐完整度 / 用户感知价值 / 实现成本」排序：

**第一批（先做，IDEA 用户最敏感）**
1. Resolve Conflicts 对话框 — merge/rebase 冲突时的总览缺失最明显
2. Blame — 文件右键最高频
3. 分支对比 — 分支管理的日常
4. Reflog — 回滚救命的兜底

**第二批**
5. Worktree — 多分支并行开发
6. 提交前检查 + 模板 — 提交面板完整度
7. Stash 高级操作 — 小但便宜

**第三批（更重、可选）**
8. Bisect — 调试利器但用得少
9. LFS — 取决于项目是否用
10. Sign commits — 签名项目才需要
11. 克隆入口现代化 — 首次体验
12. Changelist — 概念复杂，IDEA 用户也未必全用

每项独立成 PR，互不阻塞。
