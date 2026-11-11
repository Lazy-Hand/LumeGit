# Changelog

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
