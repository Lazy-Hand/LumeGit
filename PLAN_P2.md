# LumeGit — IDEA 对齐路线图 · P2 阶段

P0（标签、交互式 rebase、push 对话框、hunk 部分提交、amend 防护）与
P1（重命名、remotes、update project、比较、patch、真 shelf）已完成。
P2 补齐 IDEA 的「体验增强」项。仍严格对齐其概念与交互。

## 实施项（按依赖顺序）

### 1. Log 过滤（分支/作者/文本）
IDEA：Log 顶部过滤条 — Branch/User/Date/Paths。
- `internal/git`：`Log(ctx, opts)` 扩 `LogOptions{Branch, Author, Grep, Path, Max}` → `git log` 参数化
- UI：历史 tab 顶栏加过滤行（分支输入、作者输入、文本搜索、路径过滤）+「全部」重置

### 2. Log 搜索 + Go to Hash/Branch/Tag
IDEA：Log 中 `Cmd+F` 定位提交；`Git → Go to Hash/Branch/Tag`。
- UI：历史 tab 加「跳转…」→ 输入 hash/分支/tag → 高亮选中该行（已有 selectedID 机制）
- 搜索用 `git log --format=%H` 全量列出后本地匹配（先按显示文本 contains）

### 3. 未推送提交高亮
IDEA：log 里 ahead 的提交显示不同颜色（绿点/粗体）。
- `internal/git`：`UnpushedSet(ctx)` — `git log @{u}..HEAD --format=%H -z` 的 oid 集合
- historyModel 加载时同时取；UI 对 oid 在集合里的行加「未推送」tag/强调色

### 4. Log 图形化分支泳道
IDEA：每条提交左侧的彩色 graph 线。
- `internal/git`：`Log` 请求 `git log --graph --format=...` 不可行（graph 与机器格式冲突）；改为每条提交取 `Parents`（已有），前端画连接线：对每行计算 lane 分配（merge 分出、fork 合并）——v1 简化为「每行显示 ● 和到父提交的连线」的 ASCII/列宽 graph，或用 Canvas 绘制彩色 lane
- UI：提交行左侧 24px graph 列

### 5. Detached HEAD 状态处理
IDEA：detached 状态在分支栏显示「DETACHED」并提供「新建分支从当前提交」。
- `Snapshot` 已可分离 HEAD（headName 返回 ""）；header 分支显示改为 `DETACHED @ abc1234`，点击打开「从此提交新建分支」

### 6. Submodule 支持
IDEA：检测到 `.gitmodules` 时分支栏/文件树显示 submodule 节点（含状态、跳转）。
- `internal/git`：`Submodules(ctx)` — 解析 `.gitmodules` + `git submodule status` → 路径、commit、dirty
- UI：分支 tab 或提交 tab 显示 submodule 行（状态 tag），右键 init/update

### 7. 通知/toast 系统
IDEA：右下角气泡通知（推送成功、变基完成、冲突出现等）。
- UI 层统一 toast 队列：`app.toasts []toast`，操作完成/失败时 push，定时淡出（MyGo 需有动画/定时器或简单 overlay 列表）

每项完成后 gofmt + build + vet + `go test ./...`。
