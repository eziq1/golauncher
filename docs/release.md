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
