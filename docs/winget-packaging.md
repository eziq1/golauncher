# 用 winget 安装和更新 golauncher

> 对应需求 R92（GitHub issue #2：希望能用 winget 安装和更新 golauncher）。见 [requirements.md](requirements.md)。
>
> 这一篇讲**把 golauncher 发布成 winget 软件包**。内置的 WinGet 插件（在启动器里搜索、安装别的软件）是另一回事，见 [winget.md](winget.md)。

## 1. 结论

| 项 | 决定 |
|----|------|
| 软件包标识符 | `eziq1.golauncher`（发布者 `eziq1`，名称 `golauncher`；常量 `install.WingetID`） |
| 安装程序类型 | 直接用 Release 里已有的 zip：`InstallerType: zip`，`NestedInstallerType: portable`。不另做安装包，也不改 zip 的结构（自动更新依赖它，[release.md §9](release.md)） |
| 便携程序 | `golauncher.exe`（命令别名 `golauncher`）和 `glctl.exe`（别名 `glctl`），都在 zip 根目录 |
| 架构 | x64（`golauncher_<版本>_windows_amd64.zip`）、arm64（`..._arm64.zip`） |
| SHA-256 | 生成器从 zip 本身算出，再和同一 Release 的 `checksums.txt` 核对，不一致就报错 |
| 清单格式 | 多文件清单，schema **1.12.0**（winget-pkgs 当前用的版本；本机 winget v1.30 preview 校验通过）：版本、默认语言 en-US、安装程序、zh-CN 语言 |
| 最低系统 | `MinimumOSVersion: 10.0.17763.0`（Windows 10 1809，winget 本身能运行的最低版本；golauncher 支持 Windows 10 和 11） |
| 预发布版本 | 不发布到 winget（winget 把 `0.9.0-rc.1` 排在 `0.9.0` 之后），生成器拒绝带后缀的版本号 |
| 提交到 winget-pkgs | **不自动提交**。是否提交、何时提交由维护者决定（§4） |

## 2. 清单

### 2.1 目录结构

和 microsoft/winget-pkgs 的目录一致，可以整个文件夹拷过去：

```
packaging/winget/manifests/
└── e/eziq1/golauncher/0.8.0/
    ├── eziq1.golauncher.yaml                  # 版本清单（DefaultLocale: en-US）
    ├── eziq1.golauncher.installer.yaml        # 安装程序清单：zip + portable，x64、arm64
    ├── eziq1.golauncher.locale.en-US.yaml     # 默认语言：发布者、许可证、简介、标签、主页、发行说明链接
    └── eziq1.golauncher.locale.zh-CN.yaml     # 简体中文：简介、标签
```

仓库里提交的是 v0.8.0 的清单，作为示例和第一次提交的内容。`scripts/release/wingetmanifest` 的单元测试会检查它和生成器的输出一致，改了生成器里的文字要重新生成。

### 2.2 内容要点

- **发布者**写 `eziq1`（和 Apps & features 里自安装的卸载项一致），不写任何人的真实姓名；没有 `Author` 字段。
- 许可证 `MIT`，`LicenseUrl` 指向仓库的 LICENSE；主页、问题反馈、发行说明（`ReleaseNotesUrl`，指向该版本的 Release 页面）、README 链接。
- `Moniker: golauncher`；标签 launcher、raycast、spotlight、productivity、search、clipboard-manager、calculator、plugins（中文清单里是启动器、效率、搜索等）。
- `ReleaseDate` 是 GitHub Release 的发布日期（UTC）。
- `Commands: [golauncher, glctl]`，方便 `winget search --command glctl`。

### 2.3 生成器

```powershell
# 从 GitHub Release 下载两个 Windows zip 和 checksums.txt，日期取 Release 的发布时间
go run ./scripts/release/wingetmanifest -version 0.8.0 -download

# 从本地的 dist/ 读（发布工作流就是这样用的），日期默认是今天（UTC）
go run ./scripts/release/wingetmanifest -version 0.9.0 -dist dist -date 2026-11-01 -out dist/winget

# 校验
winget validate --manifest packaging/winget/manifests/e/eziq1/golauncher/0.8.0
```

生成器检查：版本号是 `MAJOR.MINOR.PATCH`；每个 zip 的 SHA-256 和 `checksums.txt` 一致；zip 根目录有 `golauncher.exe` 和 `glctl.exe`。输出目录默认是 `packaging/winget/manifests`。

### 2.4 校验结果

- `winget validate --manifest packaging/winget/manifests/e/eziq1/golauncher/0.8.0`：**Manifest validation succeeded.**（本机 winget v1.30.140-preview）
- 没有在本机用 `winget install --manifest` 试装：这需要管理员打开 `LocalManifestFiles` 设置（`winget settings --enable LocalManifestFiles`），按要求不改 winget 和系统设置、不在本机装东西。想试装的话，在一台可以改设置的电脑（或 Windows 沙盒）上：

  ```powershell
  winget settings --enable LocalManifestFiles     # 管理员
  winget install --manifest packaging\winget\manifests\e\eziq1\golauncher\0.8.0
  golauncher --show
  winget uninstall eziq1.golauncher
  ```

  winget-pkgs 的 PR 流水线也会在干净的虚拟机里装一遍。

## 3. 由 winget 安装的副本怎么运行

### 3.1 winget 把便携程序放在哪里

实测本机 winget 装的其他便携软件包（zip + portable）：

| 位置 | 内容 |
|------|------|
| `%LOCALAPPDATA%\Microsoft\WinGet\Packages\<标识符>_<源>\` | zip 解压出来的全部文件。`<源>` 对默认源是 `Microsoft.Winget.Source_8wekyb3d8bbwe`。装到整台电脑（`--scope machine`）时在 `%ProgramFiles%\WinGet\Packages\`（32 位是 `%ProgramFiles(x86)%`） |
| `%LOCALAPPDATA%\Microsoft\WinGet\Links\golauncher.exe`、`glctl.exe` | 指向上面文件的符号链接，这个目录在 PATH 里，所以终端里能直接运行 `golauncher`、`glctl`。不能建符号链接时，winget 改为把软件包目录加进 PATH |
| `HKCU\...\Uninstall\eziq1.golauncher_<源>` | winget 写的卸载项：`WinGetPackageIdentifier`、`WinGetSourceIdentifier`、`WinGetInstallerType=portable`、`InstallLocation`、`UninstallString=winget uninstall --product-code ...` |

winget 的卸载项名字是 `eziq1.golauncher_<源>`，和自安装的 `Uninstall\golauncher` 不冲突。

### 3.2 判断"由 winget 管理"（`install.WingetManaged`）

启动时（`shell.selfInstall`，早于单实例锁）先把运行中的 exe 解析掉符号链接（从 `WinGet\Links` 启动时得到真实路径），满足下面任一条就算由 winget 管理：

1. exe 在某个 `...\WinGet\Packages\eziq1.golauncher_<任意源>\` 文件夹里（或它的子文件夹里）。不看盘符和上一级是哪个根目录，所以用户范围、整机范围、别的源都认；比较不区分大小写；`eziq1.golauncher2_...`、没有 `_<源>` 后缀、上级不是 `WinGet\Packages` 的都不算。
2. 用 `winget install --location <目录>` 装到自定义目录时，路径里没有 `WinGet\Packages`：读 HKCU 和 HKLM 的 `Uninstall` 下以 `eziq1.golauncher_` 开头、`WinGetPackageIdentifier` 是 `eziq1.golauncher` 的项的 `InstallLocation`（`winstall.WingetLocations`），exe 在其中一个目录里就算。

只在 Windows 上判断；macOS 上永远不是（macOS 上对应的是 Homebrew，见 [homebrew-packaging.md §3.2](homebrew-packaging.md)）。

### 3.3 由 winget 管理时的行为

| 方面 | 行为 |
|------|------|
| 自安装 | **不弹安装界面**，也不因为装过另一个版本而提示升级、降级或转去启动已安装的那个，直接运行这个副本（和 `--portable` 一样） |
| 开机自启 | 照常可以在设置里打开：Run 项写成这个副本的路径（winget 的软件包目录不带版本号，`winget upgrade` 后路径不变）。例外同 [self-install.md §5.3](self-install.md)：如果同时还有一个自安装的副本，开机自启归那个副本，这个副本不改 Run 项 |
| 设置 › 关于 | 更新一节写"由 winget 管理：这个副本是用 winget 安装的，由 winget 负责更新。请先退出 golauncher，再在终端里运行 `winget upgrade eziq1.golauncher`"（`settings.updates.disabledWinget`）。没有"自动更新"模式、频率、"下载并安装""跳过此版本"这些控件 |
| 自动更新 | 关闭（`update.DisabledWinget`）：不在后台下载、不安装、不弹"有新版本"的通知 |
| 手动检查 | 设置里的"检查更新"、托盘菜单和"检查更新"命令仍然可以查：有新版本时显示"golauncher X 可用"，提示用 `winget upgrade eziq1.golauncher`（`update.result.availableWinget`） |
| `GOLAUNCHER_NO_UPDATE=1` | 优先：显示"自动更新已被 GOLAUNCHER_NO_UPDATE 关闭" |
| `--uninstall` | 不变：它卸载的是自安装的副本（如果有）。卸载 winget 装的副本用 `winget uninstall eziq1.golauncher` |
| 用户数据 | 和其他副本共用 `%APPDATA%\golauncher`、`%LOCALAPPDATA%\golauncher`；`winget uninstall` 不删数据 |

便携副本和自安装副本的行为不变。

**升级前要先退出 golauncher**：winget 升级便携软件包时要替换 `golauncher.exe`，运行中的 exe 被锁住，winget 会报错。所以提示里写了"先退出"。以后可以考虑在设置里加一个"退出并升级"的按钮（运行 `winget upgrade` 后再启动），这一期不做。

### 3.4 代码

| 位置 | 内容 |
|------|------|
| `internal/install/winget.go` | `WingetID`、`WingetUpgrade`、`WingetManaged`（纯路径判断，测试用假路径） |
| `internal/winapi/install/winget_windows.go` | `WingetLocations`：读注册表里 winget 的卸载项（集成测试要 `GOLAUNCHER_INSTALL_TESTS=1`，用临时的 HKCU 测试键） |
| `internal/shell/installer.go` | `selfInstall`：判断后设 `Options.managedBy`（`update.DisabledWinget`；macOS 上的 Homebrew 见 [homebrew-packaging.md §3](homebrew-packaging.md)），跳过安装流程 |
| `internal/shell/update.go` | `updateDisabled`：`GOLAUNCHER_NO_UPDATE` 优先，其次 `managedBy` |
| `internal/update` | `DisabledWinget`；`Summary` 的 winget 提示 |
| `internal/ui/updates.go` | 关于页的 winget 说明 |

## 4. 发布到 winget 社区仓库

### 4.1 第一次提交（手动）

1. 确认 Release 已经发布，两个 Windows zip 和 `checksums.txt` 都在。
2. 生成并校验清单（§2.3）。v0.8.0 的已经在 `packaging/winget/manifests/e/eziq1/golauncher/0.8.0/`。
3. 在 GitHub 上 fork `microsoft/winget-pkgs`，新建分支，把 `e/eziq1/golauncher/0.8.0/` 整个文件夹放到 fork 的 `manifests/e/eziq1/golauncher/0.8.0/`。
4. 本地可以先跑 winget-pkgs 自带的 `Tools/SandboxTest.ps1 manifests/e/eziq1/golauncher/0.8.0`（在 Windows 沙盒里试装）。
5. 提 PR，标题如 `New package: eziq1.golauncher version 0.8.0`，按模板勾选检查项。流水线会校验清单、下载 zip 核对 SHA-256、在虚拟机里安装并扫描；第一次提交的新软件包还要人工审核，通常几天。
6. 合并后几小时内 `winget search eziq1.golauncher` 就能找到，用户运行 `winget install eziq1.golauncher`。

也可以用微软的 `wingetcreate`（`winget install Microsoft.WingetCreate`）代替第 3 到 5 步：`wingetcreate submit --token <GitHub 令牌> packaging\winget\manifests\e\eziq1\golauncher\0.8.0`，它会自动 fork、建分支、提 PR。令牌需要 `public_repo` 权限。

### 4.2 每次发布时维护者要做的

1. 照常打 tag 发布（[release.md §3](release.md)）。发布工作流在算完 `checksums.txt` 后运行生成器（只对正式版本），运行器上有 winget 时用它校验，把清单作为构建产物 `winget-manifests` 上传。这一步失败不会阻止发布（`continue-on-error`）。
2. 从这次运行的 Artifacts 下载 `winget-manifests`（或本地 `go run ./scripts/release/wingetmanifest -version X.Y.Z -download`）。
3. 按 §4.1 第 3 到 5 步提交新版本的文件夹（更新已有软件包的 PR 一般会自动合并），或者 `wingetcreate update eziq1.golauncher --version X.Y.Z --urls <x64 zip 地址> <arm64 zip 地址> --submit --token <令牌>`（`wingetcreate` 会从上一版清单继承文字、自己下载并算 SHA-256）。
4. 想把仓库里的示例跟上最新版本时，再把新文件夹提交到 `packaging/winget/manifests/`（可选；生成器的测试只检查 0.8.0 那份）。

### 4.3 自动提交（没有做，供你决定）

可以在发布工作流最后加一步，自动向 winget-pkgs 提 PR：

- **wingetcreate**：`wingetcreate update eziq1.golauncher --version $version --urls <两个 zip 的地址> --submit --token ${{ secrets.WINGET_TOKEN }}`。只能在第一次 PR 合并、软件包已经存在之后用。
- **Komac**（社区工具，winget-pkgs 里很多清单就是它生成的）：`komac update eziq1.golauncher --version $version --urls ... --submit --token ...`；也有现成的 GitHub Action（如 `vedantmgoyal9/winget-releaser`），在 `release: published` 时运行。
- 都需要一个 GitHub 个人访问令牌（classic，`public_repo` 权限）存成仓库 Secret（如 `WINGET_TOKEN`），并且令牌所属账号下要有 winget-pkgs 的 fork。令牌只用于向 fork 推分支和开 PR。
- 风险：每次发布都会以令牌所属账号的名义在微软的仓库开 PR；Release 发布后若发现问题需要撤回，要记得关掉 PR。

## 5. 还没做 / 待定

- **向 microsoft/winget-pkgs 提交**：由你决定是否、何时提交（§4.1）。在软件包被接受之前，`winget install eziq1.golauncher` 找不到它。
- 是否加自动提交（§4.3）。
- "退出并升级"按钮（§3.3）。
- 自安装副本和 winget 副本并存时，两者各自运行；没有提示用户删掉其中一个。
