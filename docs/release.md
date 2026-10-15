# 发布流程与 v0.1.0 记录

对应需求：R7（见 [requirements.md](requirements.md)）。

> 好好整理一下，作为 0.1 版本发布到 GitHub。

## 1. 决定

| 项 | 决定 |
|----|------|
| 仓库 | `github.com/eziq1/golauncher`（公开）；Go module 路径与仓库地址一致 |
| 许可证 | MIT |
| README | 英文主 README，加中文 `README.zh-CN.md`；插件开发指南 `docs/plugin-guide.md` 用英文 |
| 版本号 | SemVer。tag 带 `v`（`v0.1.0`），程序内部和 zip 文件名里的版本号不带 `v`（`0.1.0`）；`v0.x` 版本标记为预发布 |
| 产物 | `golauncher_<版本>_windows_{amd64,arm64}.zip`，内含 `golauncher.exe`、`glctl.exe`、LICENSE、两份 README、CHANGELOG；另附 `checksums.txt`（SHA-256） |

## 2. 发布前的整理（已完成）

- module 路径改为 `github.com/eziq1/golauncher`；添加 LICENSE。
- **发布工程**：
  - 版本号通过 ldflags 注入，`glctl version` 可以查看；
  - 新图标，exe 内嵌 manifest 和版本资源；
  - 本地打包脚本 `scripts/build.ps1`；
  - CI `.github/workflows/ci.yml`：gofmt、vet、测试、`-race`、构建、i18n 检查；
  - 发布工作流 `.github/workflows/release.yml`：推送 `v*` tag 后，跑测试、构建两种架构、生成校验和，用 `gh release create` 发布。
- **补齐缺口**：密码类偏好改存凭据管理器；设置可以导出和导入；规范检查（插件 import 边界、禁止写死界面文字）。
- **只读代码审查**，修复了 12 项问题，包括阻塞发布的"粘贴功能完全无效"：
  - 粘贴失败：`SendInput` 的结构体布局错误；
  - 并发调用 `Wait` 导致 panic；
  - SQLite 路径没有转义；
  - 配置文件损坏时程序无法启动；
  - 数据库放在漫游目录；
  - E2E 控制通道默认开启（改为 `GOLAUNCHER_CONTROL=1` 才开启）；
  - 剪贴板历史的全局快捷键改为 `Alt+Shift+V`；
  - 剪贴板读取占用时间过长、超大文本没有上限、按天清理不及时；
  - 动作面板的快捷键标签错误；
  - 搜索出错后仍显示旧结果。
- 文档：英文和中文 README、CHANGELOG、插件开发指南。
- 误提交的 `golauncher.exe`（29MB）已经用 `git filter-branch` 从历史中清除（经你同意），仓库大小约 330KB。

## 3. 发布步骤

1. 在 `CHANGELOG.md` 里把 `[Unreleased]` 下的条目整理成 `## [X.Y.Z] - 日期`，并更新文件底部的比较链接。
2. 本地验证：`gofmt -l .` 输出为空，`go vet ./...`，`go test ./...`，`go test -race ./internal/... ./plugins/...`，`pwsh scripts/build.ps1 -Zip`。
3. 推送 `main`，等 CI 通过（`gh run watch`）。
4. `git tag -a vX.Y.Z -m "golauncher X.Y.Z"`，然后 `git push origin vX.Y.Z`。
5. 等发布工作流跑完，在 Releases 页面检查产物和发布说明。

## 4. v0.1.0 的发布记录

- 2026-10-15：仓库创建，推送了 `main`，CI 全部通过（包括 `-race`）。
- 打 `v0.1.0` tag 那一步被你中断了，但 tag 其实已经推送出去，触发了发布工作流。
- 同时你报告了文件搜索崩溃（R11），我**在生成 Release 之前取消了这次发布运行**。GitHub 上没有发布出任何版本。
- 2026-10-15：经你同意，删除了指向旧提交的远端和本地 `v0.1.0` tag。等 R15（自安装）合入、CHANGELOG 更新后，在新的提交上重新打 `v0.1.0` 发布。
- 2026-10-15：在提交 `7e3262d`（包含自安装、崩溃修复和插件防护）上重新打了 `v0.1.0`。发布工作流全部通过，Release 已发布：<https://github.com/eziq1/golauncher/releases/tag/v0.1.0>。产物是 amd64 和 arm64 两个 zip 加 `checksums.txt`，标记为预发布。

## 5. v0.1.0 的已知问题

- **开发者工具没有关闭**（R21）：MyGo 只有在用 `mygo build` 以生产模式构建时才关闭它的开发者工具。我们用的是 `go build`，所以 v0.1.0 里按 F12、Ctrl+Shift+I、Ctrl+Shift+C 会打开 Inspector 面板。其中 Ctrl+Shift+C 还会抢走"复制路径"这个快捷键。
- 已在 main 上修复：
  - 所有窗口显式设置 `PageOptions.DevTools`：只有版本号为 `dev`（本地未打版本号的构建）或设置了环境变量 `GOLAUNCHER_DEVTOOLS=1` 时才开启。
  - `scripts/build.ps1` 加了 `-tags mygo_noinspector`，发布包里不再包含开发者工具的代码。
- 修复随 v0.2.0 发布。
- **以后升级 MyGo 时要检查**：是否有新的、只在 `mygo build` 下才生效的生产模式行为。

## 6. v0.2.0（2026-10-15）

- **为什么"Release 里没有可下载的包"**：v0.1.0 的两个 zip 和校验文件其实都在，可以正常下载（实测返回 200，大小 13.5MB）。但它被标成了预发布（Pre-release）。GitHub 不会把预发布显示为"Latest"：仓库首页右侧和 `/releases/latest` 都找不到它（实测 `/releases/latest` 被重定向回了 Releases 列表），所以看起来像是没有可下载的包。这是当初"v0.x 一律标为预发布"的规则造成的。
- **调整**：发布工作流改为只有带后缀的版本号（如 `v1.2.0-rc.1`）才标为预发布；v0.x 正式版本作为普通 Release 发布，会显示为 Latest。
- **版本号**：0.2.0。自 0.1.0 以来新增了插件平台（M5，包括外部插件）、Scoop、世界时钟、后台任务、环境感知、Everything 排序等功能，按语义化版本升次版本号。
- **范围**：发布当时的 `main`。单位换算（R28）以及系统命令、终端与 WSL、网络工具（R31）还在各自的分支上开发，留到下一个版本。
- **新图标（R33）**：你在发布前提供了新设计的图标，当时 v0.2.0 还没打 tag，所以直接并入 v0.2.0，不再单独发一个版本。
