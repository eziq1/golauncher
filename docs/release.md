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
- **发布经过**：发布前按 R34 做了隐私检查，并改写了 v0.1.0 之后的历史；强制推送后 CI 暴露出一个偶发失败（R36），修复后才打 tag。
- **结果**：2026-10-15 在提交 `c677aec` 上打了 `v0.2.0`，发布工作流全部通过。Release 为正式版，`/releases/latest` 指向它。产物：amd64、arm64 两个 zip 和 `checksums.txt`。<https://github.com/eziq1/golauncher/releases/tag/v0.2.0>
- **没有进入 v0.2.0、留到下一版的**：单位换算（R28）、系统命令、终端与 WSL、网络工具（R31）、简体中文界面（R35）。这些分支都基于改写前的历史，要逐个重放到新的 `main` 上并先脱敏，不能直接合并。

## 7. v0.3.0（2026-10-15）

- **范围**（R37）：五个分支——单位换算（R28）、系统命令与设置直达、终端与 WSL、网络工具（R31）、简体中文界面（R35）。
- **整合方式**：这些分支都基于 R34 改写前的历史，不能直接合并。每个分支只取它自己的提交（`git log <分支> --not <分叉点>`），用 `cherry-pick -n` 重放到新的 `main` 上，解决冲突后压成一个提交。冲突主要是 README 的功能列表、`builtin.go` 的插件列表、`icons.go`、`cli_test.go` 的 golden 用例表，都是两边保留。golden 重新生成后检查过：除了因结果数上限被新条目挤掉的低分项，没有删除原有结果。
- **提交前脱敏**：终端分支的文档列出了本机真实的 Windows Terminal 配置和 WSL 发行版名称，网络分支的文档写了系统版本号、fixture 用了真实的网卡型号。都改成了只写结论或虚构的例子。
- **整合时修的问题**：网络工具复制 Wi-Fi 密码时，密码会进入 Windows 的剪贴板历史（Win+V）和云剪贴板。SDK 加了 `Clipboard.WriteSecret`（外部插件用 `host/clipboard.writeText` 的 `secret: true`），写入时附带 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory = 0`、`CanUploadToCloudClipboard = 0`。
- **中文补齐**：中文分支开发时还没有后面三个插件和计算器的新词条，整合时补了计算器的新词条、系统、终端、网络插件的 `zh-CN.json`，以及核心的两条确认框文案。`glctl i18n check` 无错误。
- **版本号**：0.3.0。新增功能，按语义化版本升次版本号。`plugin.Clipboard` 多了一个方法，自己实现这个接口的代码需要补上（0.x 允许不兼容改动，已写进 CHANGELOG）。- **结果**：2026-10-15 在提交 `d80d1f4` 上打了 `v0.3.0`。第一次推送后 CI 有 3 个 golden 失败：终端插件会检查配置图标文件是否真的存在，CI 机器装了 Git（`C:\Program Files\Git`），本机没有，所以输出不同。修复是让 glctl 的假环境只承认 fixture 目录里的文件。之后 CI 和发布工作流都通过了。Release 为正式版，`/releases/latest` 指向它。产物：amd64（17.7MB）、arm64（16.2MB）两个 zip 和 `checksums.txt`。<https://github.com/eziq1/golauncher/releases/tag/v0.3.0>

## 8. v0.4.0（2026-10-16，R53）

- **范围**：发布当时的 `main`。自 v0.3.0 以来合并的功能：
  - WinGet 插件（R39）；
  - 按应用的另一种语言名称搜索，“微信”能找到 WeChat（R40）；
  - 世界时钟的 3 万多个城市（R38）和内存优化（R41）；
  - 启动器里的 AI 对话（R45）和 AI 调用插件（R47）；
  - 计时器与提醒（R48）；
  - 快速链接与网页搜索（R49）；
  - 主题（R50、R51）。
- **发布前修的问题**：
  - 世界时钟在 CI 的 `-race` 下超时；
  - 时钟插件关闭时的竞态；
  - 计时器测试的时序问题；
  - 主题“文字衬底”默认开启，导致选中行的文字后面有方框（你在真机上发现）。
- **版本号**：0.4.0。新增功能，升次版本号。配置新增 `appearance` 对象，旧版读到时会忽略，并继续使用旧的 `theme` 字段。
- **结果**：2026-10-16 打了 `v0.4.0` 标签，CI 和发布工作流都通过。Release 为正式版，`/releases/latest` 指向它。产物：amd64（20.9MB）、arm64（19.2MB）两个 zip 和 `checksums.txt`。<https://github.com/eziq1/golauncher/releases/tag/v0.4.0>

## 9. 自动更新与签名（R54）

- 从带自动更新的版本起，已安装的 golauncher 会读 `/releases/latest` 并下载 `golauncher_<版本>_windows_<架构>.zip`，用同一个 Release 的 `checksums.txt` 校验（[auto-update.md](auto-update.md)）。所以**产物的文件名、zip 的根目录结构（`golauncher.exe`、`glctl.exe` 在根目录）和 `checksums.txt` 的格式都不能改**，改了旧版本就更新不了。
- 正式版本才会被自动安装；带后缀的 tag（预发布）不会出现在 `/releases/latest`。
- 可选的 ed25519 签名：仓库变量 `UPDATE_PUBLIC_KEY` 有值时，工作流把公钥写进程序，用 Secret `UPDATE_SIGNING_KEY` 对 `checksums.txt` 签名、验证，并上传 `checksums.txt.sig`；只配了公钥没有私钥会让发布失败。配置步骤见 [auto-update.md §9](auto-update.md)。
- 发布后可以用 `glctl update check --real` 和 `glctl update download --dry-run --real` 确认新 Release 能被检查到、校验通过（只读，不安装）。

## 10. v0.5.0（2026-10-16，R61）

- **范围**：发布当时的 `main`。自 v0.4.0 以来：
  - 自动升级（R54）；
  - 进程管理第一期（R52、R55、R60）；
  - AI 对话闲置 3 分钟释放进程（R58、R59）；
  - AI 默认联网，"Ask AI"排在网页搜索前（R56）；
  - Crimson 主题（原"Raycast-style"），默认主题调深到对比度达标（R56）。
- **发布前修的问题**：
  - 自动升级测试辅助代码的数据竞争；
  - 自动升级的限流测试在 UTC 9 点后必然失败（客户端和服务用了不同的时钟）。
- **版本号**：0.5.0。新增功能，升次版本号。配置新增 `updates` 对象，旧版读到时会忽略。
- **自动升级从这一版开始**：0.4.0 及更早的版本没有自动升级，需要手动装一次 0.5.0；之后的版本会自动更新。第一次真机自动升级会发生在下一次发布时，届时看 `%LOCALAPPDATA%\golauncher\logs\update.log`。
- **签名**：仓库没有设置 `UPDATE_PUBLIC_KEY`，这一版只靠 `checksums.txt` 的 SHA-256 校验（见 §9）。- **结果**：2026-10-16 打了 `v0.5.0` 标签，CI 和发布工作流都通过。Release 为正式版，`/releases/latest` 指向它。产物：amd64（21.7MB）、arm64（20.0MB）两个 zip 和 `checksums.txt`。`glctl update check --real` 能发现 0.5.0。<https://github.com/eziq1/golauncher/releases/tag/v0.5.0>
