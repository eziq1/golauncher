# 用 Homebrew 安装和更新 golauncher

> 对应需求 R98（为本项目增加 brew 的安装方式）。见 [requirements.md](requirements.md)。
>
> 状态：✅ 已合并到 main，尚未发布（由 Homebrew 管理时的检测从下一个版本起才有，0.8.0 没有）；tap 仓库 [eziq1/homebrew-tap](https://github.com/eziq1/homebrew-tap) 已创建，含 v0.8.0 的 cask（2026-10-17，R101）。
>
> 这一篇讲**把 golauncher 发布成 Homebrew cask**。内置的 Homebrew 插件（在启动器里搜索、安装别的软件）是另一回事，见 [homebrew.md](homebrew.md)。Windows 上的对应做法见 [winget-packaging.md](winget-packaging.md)。只支持 macOS，Linux 不是目标平台。

## 1. 结论

| 项 | 决定 |
|----|------|
| 发布渠道 | 自己的 tap：`eziq1/tap`，对应 GitHub 仓库 **`eziq1/homebrew-tap`**（Homebrew 的约定：`brew tap <用户>/<名字>` 找 `github.com/<用户>/homebrew-<名字>`）。不提交到官方的 homebrew/cask（见 §6） |
| 软件包 | cask `golauncher`（常量 `install.CaskToken`），文件 `Casks/golauncher.rb` |
| 安装命令 | `brew install --cask eziq1/tap/golauncher`（会自动 tap）；升级 `brew upgrade --cask golauncher`（`install.HomebrewUpgrade`） |
| 安装内容 | Release 里已有的 macOS zip，不另做安装包：`app "golauncher.app"`，`binary` 把包里的 `Contents/MacOS/glctl` 链接到 `$(brew --prefix)/bin/glctl` |
| 架构 | `on_arm`（`golauncher_<版本>_darwin_arm64.zip`）和 `on_intel`（`..._amd64.zip`），各自的 `url` 和 `sha256` |
| SHA-256 | 生成器从 zip 本身算出，再和同一 Release 的 `checksums.txt` 核对，不一致就报错 |
| 最低系统 | `depends_on macos: ">= :ventura"`，由生成器从 zip 里 Info.plist 的 `LSMinimumSystemVersion`（13.0）换算 |
| 版本检测 | `livecheck`：`url :url` + `strategy :github_latest`（GitHub 的 Latest release，不看预发布） |
| 预发布版本 | 不发布，生成器拒绝带后缀的版本号，`-download` 时也拒绝 GitHub 上标为 pre-release 的 Release |
| 签名 | 应用只有 ad hoc 签名，没有 Developer ID 签名和公证（[release.md](release.md) 的 macOS 任务、`scripts/macos/build.sh`）。cask 不加任何绕过 Gatekeeper 的设置（§4） |
| tap 仓库 | ✅ 已创建 [eziq1/homebrew-tap](https://github.com/eziq1/homebrew-tap)（2026-10-17，含 v0.8.0 的 cask） |
| 自动推送到 tap | 已实现但默认关闭：只有仓库 Secret `HOMEBREW_TAP_TOKEN` 存在时发布流程才推送（§5.3） |

## 2. cask

### 2.1 内容

v0.8.0 的 cask 在 `packaging/homebrew/Casks/golauncher.rb`，作为示例和第一次推送到 tap 的内容。`scripts/release/brewcask` 的单元测试会检查它和生成器的输出一致，改了生成器里的文字要重新生成。

| 段 | 内容 |
|----|------|
| `version` | `0.8.0` |
| `on_arm` / `on_intel` | `sha256` 和 `url "https://github.com/eziq1/golauncher/releases/download/v#{version}/golauncher_#{version}_darwin_arm64.zip"`（Intel 是 `amd64`）。URL 里用 `#{version}` 插值，升级时只有 `version` 和两个 `sha256` 变 |
| `name` / `desc` / `homepage` | `golauncher`；`Raycast-style keyboard launcher with plugins, written in Go`（Homebrew 的规则：不以冠词开头、不写平台、不含软件名、不超过 80 个字符、不以句号结尾）；仓库主页 |
| `livecheck` | 见 §1 |
| `depends_on` | `macos: ">= :ventura"` |
| `app` | `golauncher.app`，装到 appdir（默认 `/Applications`） |
| `binary` | `#{appdir}/golauncher.app/Contents/MacOS/glctl`，终端里可以直接用 `glctl` |
| `zap trash` | `brew uninstall --zap` 删除的用户数据：`~/Library/Application Support/golauncher`（配置、数据库、剪贴板历史、AI 对话、安装记录）、`~/Library/Caches/golauncher`（缓存、日志、更新下载）、`~/Library/LaunchAgents/io.github.eziq1.golauncher.login.plist`（开机自启的后备 launch agent，`install.LaunchAgentLabel`）、`~/Library/Preferences/io.github.eziq1.golauncher.plist` 和 `~/Library/Saved Application State/io.github.eziq1.golauncher.savedState`（macOS 按 bundle ID 保存的偏好和窗口状态，可能不存在） |
| `caveats` | 装完后显示：没有公证，第一次打开会被拦（§4）；它不自己更新，用 `brew upgrade --cask golauncher` 升级，之后退出并重新打开 |

**没有 `license`**：cask 的 DSL 没有这个字段（只有 formula 有），写了 `brew audit` 会报错。许可证（MIT）见仓库。

**没有 `uninstall quit:`**：`brew upgrade` 也会执行 `uninstall` 段。golauncher 自己的 Homebrew 插件可以在后台任务里运行 `brew upgrade`（包括升级 golauncher 本身），而 golauncher 退出时会停止所有后台任务（`tasks.Manager.Close`），如果 cask 让 brew 先让 golauncher 退出，升级会在半路被杀掉。所以 brew 不让它退出：新的 golauncher.app 替换旧的之后，正在运行的进程继续用旧代码，退出并重新打开就是新版本（设置里的说明和 caveats 都这样写）。

**没有 `auto_updates true`**：由 Homebrew 安装的副本关闭了自己的自动更新（§3），`brew upgrade` 负责更新，所以不需要这个声明。

### 2.2 生成器

```sh
# 从 GitHub Release 下载两个 macOS zip 和 checksums.txt
go run ./scripts/release/brewcask -version 0.8.0 -download

# 从本地的 dist/ 读（发布工作流就是这样用的）
go run ./scripts/release/brewcask -version 0.9.0 -dist dist -out dist/homebrew/Casks

# 直接写进 tap 的工作副本
go run ./scripts/release/brewcask -version 0.9.0 -download -out ../homebrew-tap/Casks
```

生成器检查：版本号是 `MAJOR.MINOR.PATCH`；每个 zip 的 SHA-256 和 `checksums.txt` 一致；zip 根目录有 `golauncher.app`，里面有 `Contents/MacOS/golauncher`、`Contents/MacOS/glctl` 和 `Contents/Info.plist`；Info.plist 的版本（`GolauncherVersion`）就是这个版本、`CFBundleIdentifier` 是 `io.github.eziq1.golauncher`；两个 zip 的 `LSMinimumSystemVersion` 相同，并且 Homebrew 有名字（11 big_sur、12 monterey、13 ventura、14 sonoma、15 sequoia、26 tahoe，次版本号必须是 0）。输出 `<out>/golauncher.rb`，`out` 默认是 `packaging/homebrew/Casks`。

### 2.3 校验

- 这台 Windows 电脑上没有 Ruby 和 brew，cask 的格式由生成器的测试检查：逐段的内容、两个插值 URL 展开后就是 Release 的下载地址、`do`/`end` 和 `[`/`]` 配对、两格缩进、没有制表符和行尾空格、`url` 和 `binary` 以外的行没有插值、Ruby 字符串转义、`desc` 的规则。
- 发布工作流的 `homebrew` 任务（macOS 运行器）在一个临时的本地 tap 里运行 `brew style --cask` 和 `brew audit --cask --online`（会真的去下载两个 zip 核对 SHA-256）。这一步失败不影响发布（`continue-on-error`），看任务日志即可。
- 本地在 Mac 上手动校验：

  ```sh
  brew tap-new --no-git golauncher-local/test
  cp packaging/homebrew/Casks/golauncher.rb "$(brew --repository golauncher-local/test)/Casks/"
  brew style --cask golauncher-local/test/golauncher
  brew audit --cask --online golauncher-local/test/golauncher
  brew install --cask golauncher-local/test/golauncher   # 试装
  brew uninstall --cask golauncher && brew untap golauncher-local/test
  ```

## 3. 由 Homebrew 安装的副本怎么运行

### 3.1 Homebrew 把东西放在哪里

| 位置 | 内容 |
|------|------|
| `/Applications/golauncher.app` | 应用本身（`brew install --appdir <目录>` 或 `HOMEBREW_CASK_OPTS="--appdir=..."` 时在别处，常见的是 `~/Applications`） |
| `<前缀>/Caskroom/golauncher/<版本>/` | 每个已安装版本一个文件夹（应用移走后基本是空的）。前缀：Apple 芯片 `/opt/homebrew`，Intel `/usr/local` |
| `<前缀>/Caskroom/golauncher/.metadata/config.json` | 安装时的目录设置：`{"default": {...}, "env": {...}, "explicit": {...}}`，每层里有 `appdir`（已展开的绝对路径）。优先级 explicit（命令行的 `--appdir`）> env（`HOMEBREW_CASK_OPTS`）> default（`/Applications`） |
| `<前缀>/Caskroom/golauncher/.metadata/<版本>/<时间戳>/Casks/golauncher.rb` | 安装时的 cask 文件 |
| `<前缀>/bin/glctl` | 指向 `golauncher.app/Contents/MacOS/glctl` 的符号链接 |

`brew uninstall` 删除应用、链接和整个 `Caskroom/golauncher`。

### 3.2 判断"由 Homebrew 管理"（`install.HomebrewManaged`）

启动时（`shell.selfInstall`，早于单实例锁）先把运行中的程序解析掉符号链接，再按下面的规则判断，**不运行 brew**（启动时不起进程）：

1. 程序在一个名叫 `golauncher.app` 的应用包里（`<目录>/golauncher.app/Contents/MacOS/<程序>`）。改过名的副本（`golauncher 2.app`）不算。
2. 依次看每个前缀（`install.HomebrewPrefixes`：环境变量 `HOMEBREW_PREFIX`，如果有并且是绝对路径——从访达打开的应用一般没有；然后 `/opt/homebrew`、`/usr/local`）下的 `Caskroom/golauncher`：里面至少有一个不以 `.` 开头的版本文件夹（只有 `.metadata` 不算）。
3. 应用包所在的目录等于这个 Caskroom 记录的 appdir（`.metadata/config.json`，按 §3.1 的优先级取第一个绝对路径）。`config.json` 没有或读不了时，`/Applications` 和 `~/Applications` 都算。比较不区分大小写（APFS 默认不区分）。

所以：Homebrew 装在 `/Applications` 的就是被管理的副本；用户自己下载到"下载"文件夹或别的地方运行的副本不是；装过 `golauncher2` 之类别的 cask 也不算。只在 macOS 上判断（`winstall.Bundled`）；纯路径逻辑和读文件在所有平台上测试（`internal/install/homebrew_test.go`，假的前缀、Caskroom 和 config.json）。

没有比对版本号：Caskroom 的版本文件夹是 Homebrew 认为装着的版本，和应用自己的版本可能不同（例如 0.8.0 还没有这个判断，用 brew 装的 0.8.0 可能已经自己更新过），这不影响"由 Homebrew 管理"的结论。

### 3.3 由 Homebrew 管理时的行为

和 winget 的做法一致（[winget-packaging.md §3.3](winget-packaging.md)），共用 `Options.managedBy`：

| 方面 | 行为 |
|------|------|
| 自安装 | **不弹安装界面**，也不因为装过另一个版本而提示升级、降级或转去启动已安装的那个，直接运行这个副本 |
| 开机自启 | 照常可以在设置里打开（应用路径在升级后不变） |
| 设置 › 关于 | 更新一节写"由 Homebrew 管理：这个副本是用 Homebrew 安装的，由 Homebrew 负责更新。请在终端里运行 `brew upgrade --cask golauncher`，然后退出并重新打开 golauncher。"（`settings.updates.disabledHomebrew`）。没有"自动更新"模式、频率、"下载并安装""跳过此版本"这些控件 |
| 自动更新 | 关闭（`update.DisabledHomebrew`）：不在后台下载、不安装、不弹"有新版本"的通知。否则应用会自己替换 `/Applications/golauncher.app`，Homebrew 记录的版本就不对了 |
| 手动检查 | 设置里的"检查更新"、菜单栏菜单和"检查更新"命令仍然可以查：有新版本时提示运行 `brew upgrade --cask golauncher`（`update.result.availableHomebrew`） |
| `GOLAUNCHER_NO_UPDATE=1` | 优先：显示"自动更新已被 GOLAUNCHER_NO_UPDATE 关闭" |
| `--uninstall` | 不变（卸载自安装的副本）。卸载 Homebrew 装的副本用 `brew uninstall --cask golauncher`，连用户数据一起删用 `--zap` |
| 用户数据 | 和其他副本共用 `~/Library/Application Support/golauncher`、`~/Library/Caches/golauncher`；`brew uninstall` 不删数据，`--zap` 才删 |

### 3.4 已经自安装过的用户

自安装的 golauncher 也在 `/Applications/golauncher.app`，`brew install` 会因为"已经有一个应用"而停下。两种办法：

- `brew install --cask --adopt eziq1/tap/golauncher`：版本相同时 Homebrew 接管已有的应用（不重新下载应用本身）；版本不同时先让 golauncher 自己更新到 cask 的版本，或用下一种办法。
- 先删掉旧的：退出 golauncher，`rm -rf /Applications/golauncher.app`（或 `golauncher --uninstall`，不勾选删除数据），再 `brew install --cask eziq1/tap/golauncher`。配置和数据都在 `~/Library`，不受影响。

### 3.5 代码

| 位置 | 内容 |
|------|------|
| `internal/install/homebrew.go` | `CaskToken`、`CaskFullName`、`HomebrewUpgrade`、`HomebrewPrefixes`、`HomebrewManaged` |
| `internal/shell/installer.go` | `packageManager`：winget（Windows）或 Homebrew（macOS），结果放在 `Options.managedBy`，跳过安装流程 |
| `internal/shell/update.go` | `updateDisabled`：`GOLAUNCHER_NO_UPDATE` 优先，其次 `managedBy` |
| `internal/update` | `DisabledHomebrew`；`Summary` 的 Homebrew 提示 |
| `internal/ui/updates.go` | 关于页的 Homebrew 说明 |
| `scripts/release/brewcask` | 生成器 |

## 4. Gatekeeper

golauncher.app 只有 ad hoc 签名，没有 Developer ID 签名，也没有经过 Apple 公证。Homebrew 下载的文件会带上 `com.apple.quarantine` 属性，所以：

- **第一次打开会被拦下**（"无法打开 golauncher，因为 Apple 无法检查其是否包含恶意软件"）。先打开一次，再到 **系统设置 › 隐私与安全性**，在页面下方点 **"仍要打开"**（macOS 15 起，按住 Control 点按 › 打开 这条老办法不再能绕过）。
- 或者在终端里去掉隔离属性：`xattr -dr com.apple.quarantine /Applications/golauncher.app`。这是用户自己的决定，cask 和文档都不替用户做。
- **升级后可能还要再允许一次**：Homebrew 升级时保留隔离属性，只在签名身份不变时才沿用用户之前的允许；ad hoc 签名没有身份，所以可能每次升级都要重复一次。辅助功能权限同样会在每次更新后失效（README 的 macOS 一节）。
- Homebrew 已经弃用 `brew install --no-quarantine`，文档里不推荐它；cask 里也没有任何绕过安全检查的设置。

根本的解决办法是 Developer ID 签名和公证（需要 Apple Developer Program 账号），见 [macos-port.md](macos-port.md) 的第三期。做了之后，删掉 caveats 里的这段说明。

## 5. 发布到 tap

### 5.1 第一次：创建 tap 仓库（✅ 2026-10-17 已完成）

**已创建**：公开仓库 [eziq1/homebrew-tap](https://github.com/eziq1/homebrew-tap)，内容是 `Casks/golauncher.rb`（v0.8.0，SHA-256 已和 v0.8.0 重建后 Release 的 `checksums.txt` 核对一致，LF 换行）和一个写着安装命令的 README，第一个提交是 `golauncher 0.8.0`（作者 eziq1 + noreply 邮箱）。还没在 Mac 上实际跑过 `brew install`（下面第 3 步）。

当时的步骤（以后要重建时参考）：

1. 在 GitHub 上新建**公开**仓库 `eziq1/homebrew-tap`（名字必须以 `homebrew-` 开头）。不需要任何别的设置。
2. 在本地：

   ```sh
   git clone https://github.com/eziq1/homebrew-tap.git
   cd homebrew-tap
   mkdir -p Casks
   cp ../golauncher/packaging/homebrew/Casks/golauncher.rb Casks/
   # 可选：一个 README，写安装命令
   printf '# eziq1/tap\n\n```sh\nbrew install --cask eziq1/tap/golauncher\n```\n' > README.md
   git add Casks/golauncher.rb README.md
   git commit -m "golauncher 0.8.0"
   git push origin main
   ```

   或者在 Mac 上用 `brew tap-new eziq1/tap`（会在本地生成带 GitHub Actions 测试工作流的骨架，在 `$(brew --repository eziq1/tap)`），把 cask 放进 `Casks/`，再 `git remote add origin https://github.com/eziq1/homebrew-tap.git && git push -u origin main`。
3. 验证：`brew install --cask eziq1/tap/golauncher`（自动执行 `brew tap eziq1/tap`），`glctl version`，打开应用后在 设置 › 关于 看到"由 Homebrew 管理"。

### 5.2 每次发布时

1. 照常打 tag 发布（[release.md §3](release.md)）。发布工作流在算完 `checksums.txt` 后运行生成器（只对正式版本），把 cask 作为构建产物 `homebrew-cask` 上传；之后 `homebrew` 任务在 macOS 上用 `brew style` 和 `brew audit` 检查它。都不会阻止发布。
2. 没有开自动推送时：从这次运行的 Artifacts 下载 `homebrew-cask`（或本地 `go run ./scripts/release/brewcask -version X.Y.Z -download -out ../homebrew-tap/Casks`），在 tap 仓库里提交并推送：`git commit -am "golauncher X.Y.Z" && git push`。
3. 用户的 `brew update` 之后，`brew outdated --cask` 和 `brew upgrade --cask golauncher` 就能看到新版本。
4. 想把仓库里的示例跟上最新版本时，再提交 `packaging/homebrew/Casks/golauncher.rb`（可选；生成器的测试只检查 0.8.0 那份，要一起改测试里的 `v080`）。

### 5.3 自动推送（已实现，默认关闭）

发布工作流的 `homebrew` 任务最后一步"Push to eziq1/homebrew-tap"只在仓库 Secret **`HOMEBREW_TAP_TOKEN`** 存在时运行：克隆 `eziq1/homebrew-tap`，用这次生成的 cask 覆盖 `Casks/golauncher.rb`，有变化就以 `github-actions[bot]` 的名义提交 `golauncher X.Y.Z` 并推送到默认分支。没有这个 Secret 时跳过，什么都不推。预发布版本不运行。

要打开它：

1. 建一个 fine-grained 个人访问令牌：Repository access 只选 `eziq1/homebrew-tap`，Permissions › Contents 选 **Read and write**，其他都不给；设一个过期时间，到期前续。
2. 在 `eziq1/golauncher` 的 Settings › Secrets and variables › Actions 里新建 Secret `HOMEBREW_TAP_TOKEN`，填这个令牌。
3. 下一次正式发布就会自动推送。要关掉，删掉这个 Secret。

风险：令牌能改 tap 仓库的内容；cask 由同一次发布的产物生成并经过 `checksums.txt` 核对，`brew audit` 失败不阻止推送（它只是检查），所以推送前最好看一眼 `homebrew` 任务的日志——或者只在确认过几次 audit 都通过后再打开自动推送。

## 6. 为什么不提交到官方 homebrew/cask

- 官方仓库要求应用有一定知名度（星标、关注者等），并且从 2025 年起要求 cask 里的应用通过 Gatekeeper（Developer ID 签名和公证）；不满足的 cask 会被弃用和禁用。golauncher 现在没有公证（§4）。
- 自己的 tap 不受这些限制，发布节奏也由自己定。将来做了签名和公证，可以再考虑提交到 homebrew/cask，那时安装命令变成 `brew install --cask golauncher`，管理检测（§3.2）不用改（Caskroom 的目录名就是 cask 名）。

## 7. 还没做 / 待定

- ~~创建 `eziq1/homebrew-tap` 仓库并推送第一个 cask~~：✅ 2026-10-17 已完成（§5.1）；还没在 Mac 上实际跑过 `brew install`。
- 是否打开自动推送（§5.3）。
- "退出并升级"按钮：在设置里运行 `brew upgrade --cask golauncher` 后自动重新打开，这一期不做。
- `brew style` / `brew audit` 没有在本机运行过（这台电脑是 Windows），第一次发布后看 `homebrew` 任务的日志；如果它对 `on_arm`/`on_intel` 写法提出建议（另一种写法是 `arch arm: "arm64", intel: "amd64"` 加 `sha256 arm: ..., intel: ...`），改生成器即可。
- Developer ID 签名和公证（§4、§6）。
