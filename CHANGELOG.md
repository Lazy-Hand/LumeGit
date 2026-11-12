# Changelog

## [0.1.12] - 2026-11-12

- 新增：偏好设置（菜单 LumeGit → 偏好设置…, CmdOrCtrl+,）——差异并排/统一、面板布局重置；Git 身份（本仓库 user.name/user.email）编辑
- 新增：UI 偏好持久化到 `settings.json`（差异模式/分隔比例、五个面板宽度、所在 tab、列表/更改视图），重启保留
- 性能：文件列表虚拟化（ui.List 只渲染可视行），2000 文件面板帧 ~16.6ms → ~0.5ms
- 性能：diff 在后台 worker 预算行级比较（editor.DiffState 预热），选中文件首帧 1.30ms → 0.43ms；大文件自动关闭词级高亮，切换不再卡
- 性能：变更列表分组用反向索引（原 O(文件×列表) 扫描），changelist 分配/过滤不再随仓库大小变慢
- 修复：连续快速切换文件选择跟手（旧 diff 立即作废、不再重绘上一个文件的 DiffView）；刷新同路径 diff 时不再误用旧 DiffState
- 修复：切换文件/提交时残留的 hunk 勾选不再被误提交；加载中隐藏 hunk 勾选条
- 修复：重名文件重复点击不再重置比较/diff 状态造成无谓全量重算
- 重构：可复用 UI 件抽到 `components/`（状态字形/颜色、时间/OID 格式化、graph 提交节点、placeholder、toast 栈、冲突只读列、比较单侧列表）；视图按功能拆分为 historyview/shelfview/branchview/opsdialogs/overlays/conflictview/reflogview/filehistview/diffpane，commitview.go 4707 → ~1600 行
- 修复（此前全量审计批次）：冲突 stage 标记 `%c`→`%d`（"采用当前/对方"此前必失败）、rebase `--only` 修正、push.default 语义、status `-unormal`、shelf 暂存/未跟踪搁置、filehist 懒加载 blame、提交回滚路径引用安全等
- 移除：PLAN_P0–P3 计划文档（已落地的临时文件）

## [0.1.11] - 2026-11-11

- 修复：「与当前分支比较」文件列表三点 → 两点 diff，与点开后看到的差异一致（原来列表能列出两端内容相同的文件，打开是空白）
- 修复：比较弹窗 diff 加 MinHeight，避免 Modal 下塌陷为零高
- 修复：Reflog「查看」现在切到历史 tab 并滚动定位到该提交，自动翻页直到找到
- 文案：「与 'xxx' 比较…」→「与其他分支比较…」

## [0.1.10] - 2026-11-11

- 性能：打开仓库自动启用 `core.fsmonitor`（git ≥2.36）
- 性能：历史提交/提交文件列表虚拟化（ui.List，只渲染可视行）+ log 分页加载（每页 50 + 加载更多）
- 性能：untracked 文件超 2000 时 `git status` 自动降级 `-uno`
- 性能：refs 轮询随窗口 focus 自适应（2s ↔ 10s），diff 选中切换时取消过期后台任务
- 性能：分支 ahead/behind 从 `for-each-ref` 的 `%(upstream:track)` 一次取精确数字
- 性能：打开仓库后台预热历史/搁置/分支缓存

## [0.1.9] - 2026-11-11

- 「分支」tab 移除：header 分支名点击弹出 IDEA 式分支菜单（新建分支/标签/远程/工作树 + 每条分支内嵌切换/合并/变基/比较/重命名/删除）
- 性能：fsnotify 工作区 watch 改为 lazygit 式 refs 指纹轮询（2s）+ 窗口 focus 刷新——大仓库刷新不再 CPU 拉满
- 修复：手动刷新与后台刷新互斥，不再叠加 git status 进程
- 更新弹窗中文化（zh-Hans）

## [0.1.8] - 2026-11-10

- 修复：fsnotify 事件风暴时并发 Snapshot 堆叠导致 fd 耗尽（refreshQuiet 节流）
- 性能：Snapshot 每次少一次 `git rev-parse` 调用

## [0.1.7] - 2026-11-10

- 修复：大仓库下文件 watcher 耗尽 fd（too many open files）——改为只 watch .git 控制文件 + worktree 根目录
- 性能：历史/搁置 tab 并发加载 + Spin 加载指示
- 性能：分支 tab 单 `git for-each-ref` 查询 + 乐观插入新分支

## [0.1.6] - 2026-11-10

- 分支 tab 性能：单 `git for-each-ref` 并发查询 + Spin 加载指示，刷新/建分支即时反馈
- 新建分支乐观插入列表，不再等后台刷新

## [0.1.5] - 2026-11-10

- 修复 v0.1.3/v0.1.4 签名未生效的问题

## [0.1.4] - 2026-11-10

- 修复 v0.1.3 签名未生效的问题

## [0.1.3] - 2026-11-10

- 修复每次更新后需重新授予系统权限：Release 构建改用固定 Apple Development 签名身份，TCC 授权跨更新保留

## [0.1.2] - 2026-11-10

- 多窗口：每个仓库独立窗口，可同时打开多个项目
- 主窗口专职 launcher：仓库列表 + 打开/克隆/新建仓库入口
- 新增「文件 → 新建窗口」(⌘⇧N) 菜单
- picker 中所有「打开」动作统一走新窗口
- 仓库窗口 header 的 ⌄ 菜单可快速跳到其他仓库（新窗口）
- worktree 右键「在新窗口打开此 worktree」
- 内部：store.Manager 共享单例 + 互斥锁，多窗口并发安全

## [0.1.1] - 2026-11-10

- 新应用图标：深色底 + 分支汇合符号，与 LumeShell 同家族风格
- 修复：`git merge --continue` 在无 TTY 环境（CI/后台）下失败

## [0.1.0] - 2026-11-10

- 首个发布：按 IntelliJ IDEA Git 工具窗口对齐 P0–P3 共 24 项功能
  - **P0**：标签、交互式 rebase、push 对话框、hunk 级部分提交、amend 推送防护
  - **P1**：分支重命名、Remotes 管理、Update Project、与分支/工作区比较、patch 创建与应用、真 Shelf
  - **P2**：log 过滤、跳转至提交、未推送高亮、提交图、Shelf UI、子模块、toast 通知
  - **P3**：冲突解决对话框、Blame、分支对比、Reflog、Worktree、提交前检查、Stash 高级、Bisect、LFS、签名验证、克隆现代化、Changelist
- 自更新：GitHub Releases + 签名（mygo updater）
