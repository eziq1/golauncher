# Homebrew 插件

> 状态：实现 v1 · 2026-10-16 · macOS 移植（[macos-port.md](macos-port.md) §10）
>
> 需求：macOS 上和 Scoop（[scoop.md](scoop.md)）、WinGet（[winget.md](winget.md)）对应的软件包管理插件。三者是**完全独立**的插件，不共享代码、不共享抽象。

## 1. 定位

- 内置插件 `builtin.homebrew`（`plugins/homebrew`），manifest 声明 `Platforms: []string{plugin.PlatformDarwin}`，只在 macOS 上加载，Windows 上不注册；glctl 的假环境模拟 Windows，也不包含它（`builtin.NotFaked()` 只供 `glctl i18n check` 检查其翻译）。
- **读操作尽量不启动 brew**：搜索直接读 brew 缓存目录里的 API 索引，边输入边出结果；已安装和可升级列表来自一次 `brew info --json=v2 --installed`（本机 244 个包约 9 秒），在后台运行并缓存到插件缓存目录，界面先显示缓存。
- **写操作**（安装、卸载、升级、全部升级、`brew update`）作为宿主的后台任务运行（[background-tasks.md](background-tasks.md)），队列 `homebrew`，一次一个，实时输出（⌘J），可以停止。
- **没有安装 Homebrew 时**插件照常启动（和 WinGet 的 `ErrUnavailable` 不同）：所有视图和 `brew <词>` 都显示一行"没有安装 Homebrew"，回车打开 https://brew.sh，⇧⌘C 复制官方安装命令；每次呼出启动器时重新找一次 brew，装好后不用重启。

## 2. 功能

| 功能 | 入口 | 实现 |
|------|------|------|
| 搜索 | 命令 **Search Homebrew Packages**；根搜索 `brew <关键词>`（偏好项 `showInRoot`，默认开） | 本地索引，按名称、别名（`rg` → ripgrep）、cask 显示名（"微信 Mac 版"）、命令名、简介模糊匹配；多词查询整体等于别名/显示名时加分（"vs code"） |
| 详情面板 | 列表右侧 | 索引里的字段：名称、类型、版本、tap、许可证、别名、命令、主页、弃用/停用 |
| 详情视图 | **Show Details**（⌘D） | `brew info --json=v2 --formula|--cask <名称>`：依赖、构建依赖、注意事项（caveats）、冲突、应用（`.app`）、最低 macOS、keg-only、已安装的版本；按包缓存，操作后清空 |
| 安装 | 未安装的包的主动作 | `brew install --formula <名称>` / `brew install --cask <token>` |
| 升级 | 可升级时为主动作，⌘U | `brew upgrade --formula|--cask <名称>`（固定版本的 formula 不显示） |
| 卸载 | ⌘X（Destructive，宿主先确认） | `brew uninstall --formula|--cask <名称>` |
| 卸载并删除数据 | cask 的 ⇧⌘X（Destructive） | `brew uninstall --cask --zap <token>` |
| 已安装列表 | 命令 **Installed Homebrew Packages** | 分组：有可用更新 / 应用（Cask）/ Formula / 作为依赖安装 / 刷新 |
| 可升级列表 | 命令 **Outdated Homebrew Packages** | `outdated` 为真的包（和 `brew outdated` 一样不含自动更新的 cask）；第一条是"升级全部 N 个软件包" |
| 全部升级 | 命令 **Upgrade All Homebrew Packages**（Destructive） | `brew upgrade` |
| 更新 | 命令 **Update Homebrew**；找不到索引时搜索视图里也有一行 | `brew update` |
| 打开主页 | ⌘O | 索引或 `brew info` 里的 homepage |
| 复制 | ⇧⌘C 安装命令，⌥⌘C 名称 | — |

## 3. 找到 brew

`FindBrew` 依次看 `/opt/homebrew/bin/brew`（Apple 芯片）、`/usr/local/bin/brew`（Intel）、`/home/linuxbrew/.linuxbrew/bin/brew`，再找 PATH。从访达启动时 PATH 很短，启动时 `internal/shell` 已经从登录 shell 补过 PATH（§5.3），自定义前缀的安装也能找到。两个前缀都有时用 `/opt/homebrew`。

缓存目录（API 索引所在）：`$HOMEBREW_CACHE`，否则 `brew --cache`（约 0.1 秒，只问一次），否则 `~/Library/Caches/Homebrew`。

## 4. 索引格式

brew 下载的 API 文件都是 JWS：`{"payload": "<JSON 文本>", "signature": …}`。签名由 brew 在下载时校验，插件只取 payload。按修改时间取最新的一种：

| 格式 | 文件 | 说明 |
|------|------|------|
| internal | `api/internal/packages.<bottle tag>.jws.json` | 新版 brew（本机 7.0.9）：一个文件，`formulae`、`casks` 是以名称为键的对象，`formula_aliases` 是别名表；formula 带 `executables`；弃用/停用是带日期的 `deprecate_args`/`disable_args`，按当前日期判断是否已生效。约 15 MB，读取约 0.6 秒 |
| public | `api/formula.jws.json`、`api/cask.jws.json` | 旧版 brew：两个数组，字段和 `brew info --json=v2` 相同 |
| names | `api/formula_names.txt`、`api/cask_names.txt` | 只有名称，作为最后的退路 |

呼出启动器时只 stat 这些文件，没变就不重读；读失败时保留上一次的索引。其他 tap（`brew tap user/repo`）的包不在 API 索引里，搜不到，但已安装列表里有（操作时用 `user/repo/name` 全名）。

## 5. 环境变量

- 只读命令（`info`、`--cache`）：`HOMEBREW_NO_AUTO_UPDATE=1`（不触发自动更新，也不下载 API 文件）、`HOMEBREW_NO_ENV_HINTS=1`、`HOMEBREW_NO_COLOR=1`、`HOMEBREW_NO_EMOJI=1`；只解析 stdout，失败时显示 stderr 的最后一行。
- 操作：`HOMEBREW_NO_ENV_HINTS=1`、`HOMEBREW_NO_EMOJI=1`；偏好项 **Update Homebrew before installing**（`autoUpdate`，默认开，和终端里的行为一致）关掉时加 `HOMEBREW_NO_AUTO_UPDATE=1`。

名称在拼命令前用正则校验（字母数字开头，可带 `user/tap/` 前缀），不会被当成选项；参数直接作为 argv 传给 brew，不经过 shell。

## 6. 测试

- `plugins/homebrew/testdata`：从本机 Homebrew 7.0.9 裁剪的真实数据——internal 格式的缓存目录（8 个 formula、5 个 cask）、public 格式的两个文件、`brew info --json=v2 --installed` 和两个包的 `brew info` 输出。
- 单元测试覆盖三种索引格式的解析和选择、许可证表达式、弃用日期、搜索排序、已安装/详情解析、`FindBrew`、各视图、操作的命令行/环境变量/队列、非法名称、没有 brew 时的状态、en 与 zh-CN 键一致。命令只交给假的任务运行器，**从不安装或卸载任何东西**。
- `GOLAUNCHER_HOMEBREW_TESTS=1 go test -run RealHomebrew -v ./plugins/homebrew/`：只读本机的 Homebrew（索引、`brew --cache`、`brew info`），记录耗时；如果创建了任务就失败。

## 7. 还没做

- 需要 sudo 的 cask（`.pkg` 安装器）在后台任务里没有终端，会失败（"a terminal is required"）。可以复制安装命令到终端运行；以后可以加 `SUDO_ASKPASS` 图形密码框或"在终端中运行"。
- 固定/取消固定版本（`brew pin`）、tap 管理、服务（`brew services`）、`brew cleanup`、`brew autoremove`。
- 第三方 tap 的包不能搜索（需要读各 tap 的 git 仓库）。
