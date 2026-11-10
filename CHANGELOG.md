# Changelog

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
