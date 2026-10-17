# 自安装（self-installer）设计

golauncher 只发布一个 zip（`golauncher.exe` + `glctl.exe`）。不另做安装包，由 `golauncher.exe` 自己完成安装、升级、降级和卸载。

## 1. 需求

1. 第一次打开时，弹出安装界面，安装到系统里。默认装到用户的程序目录。
2. 已经安装过时，再次打开会比较当前版本和已安装版本：
   - 版本号不一致：提示升级或降级。
   - 版本号一致：跳过安装程序。

## 2. 已确定的决策

| 项 | 决定 |
|----|------|
| 安装范围 | 只装给当前用户，不需要管理员权限，没有 UAC |
| 默认目录 | `%LOCALAPPDATA%\Programs\golauncher`（`FOLDERID_UserProgramFiles`），可以在界面里改 |
| 安装内容 | 复制 `golauncher.exe`，以及运行中 exe 旁边的 `glctl.exe`（如果有） |
| 开始菜单 | `%APPDATA%\Microsoft\Windows\Start Menu\Programs\golauncher.lnk`，纯 Go COM（`IShellLinkW` + `IPersistFile`） |
| 桌面快捷方式 | 复选框，默认不勾 |
| 开机自启 | 复选框，默认勾选；写进 `config.json` 的 `launchAtLogin`，由安装后的 exe 启动时自己同步 Run 项（这样 Run 项指向安装后的路径） |
| 卸载项 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\golauncher`：DisplayName、DisplayVersion、Publisher（eziq1）、InstallLocation、DisplayIcon、UninstallString（`"<dir>\golauncher.exe" --uninstall`）、NoModify、NoRepair、EstimatedSize、InstallDate、URLInfoAbout |
| 已安装版本 | 读卸载项的 DisplayVersion；读不到时读已安装 exe 的版本资源（ProductVersion） |
| 何时不进入安装流程 | 版本是 `dev`（未注入版本号的开发构建）、带 `--portable`、环境变量 `GOLAUNCHER_NO_INSTALL=1`、带 `--quit`、运行的就是已安装的 exe |

## 3. 启动时的判断

判断放在 `shell.Run` 的最前面，早于单实例锁。原因：MyGo 的单实例锁按应用名区分，未安装的副本和已安装的副本共用同一把锁，只有在拿锁之前才能分辨“我是谁”。

```
Run(args)
 ├─ --uninstall            → 卸载流程（§6）
 ├─ 跳过条件成立            → 正常启动（仍会检测安装，用于 §5.3）
 └─ Detect()
     ├─ 没有安装（无卸载项，或卸载项指向的 exe 不存在） → 安装界面
     ├─ 运行的就是已安装的 exe（路径不区分大小写比较）   → 清理 *.old，正常启动
     ├─ 版本一致                                      → 启动已安装的 exe（转发 --show/--settings 等参数），本进程退出
     └─ 版本不一致                                    → 升级/降级界面
```

## 4. 版本比较

- 按 SemVer 比较，允许开头的 `v`，忽略 `+build`。
- `git describe` 的后缀 `-N-gHASH[-dirty]` 表示“某个 tag 之后第 N 个提交”，按 tag 之后排序：`0.1.0 < 0.1.0-2-gabc < 0.1.0-3-gdef < 0.1.1`。如果 SemVer 直接比较，`0.1.0-2-gabc` 会被当成预发布版、排在 `0.1.0` 前面，这是错的。
- 没有 tag 时 `build.ps1` 生成 `0.0.0-<hash>`，带 `-dirty` 的版本也一样：不可排序，只算“不同”。
- 无法解析的版本也只算“不同”。
- 字符串完全相同（去掉 `v` 后）才算“一致”。
- 方向未知时，对话框用中性的“替换”措辞。

## 5. 安装 / 升级 / 降级

### 5.1 界面

用一个独立的小窗口（MyGo 原生 UI，不是启动器面板），`internal/ui/installer.go`。

- **全新安装**：欢迎语；安装位置（文本框 + “浏览…”选择文件夹）；“开机时启动”“创建桌面快捷方式”两个复选框。按钮有“安装”“不安装，直接运行”“取消”。
- **已安装、版本不同**：显示“已安装 golauncher X，升级到 Y？”（降级时说“降级到 Y”）。按钮有“升级”或“降级”、“不安装，直接运行”“取消”。
- 安装过程中显示进度文字，按钮禁用。失败时显示错误，可以重试。
- 成功后启动安装好的 exe，然后本进程退出。

“不安装，直接运行”的处理：用 `--portable` 重新启动自己，本进程退出。MyGo 的 `App.Run` 在一个进程里只能跑一次，所以不能在同一个进程里接着正常启动。这个选择不会被记住。

### 5.2 步骤（`internal/install`）

1. **校验目录**：必须是绝对路径，不能是一个文件；不存在就创建。
2. **结束正在运行的实例**：运行 `<自己> --quit`，等它退出。`--quit` 不会进入安装流程，会经单实例锁转给正在运行的实例，不管那个实例是哪个副本、哪个版本。然后最多等 5 秒，直到本会话里除自己以外的 `golauncher.exe` 进程都退出。
   - 超时就报错（“golauncher 仍在运行”），不强杀。
   - 用自己而不是已安装的 exe 发 `--quit`，是因为已安装的 exe 可能缺失或损坏。
3. **复制文件**：
   1. 先写到 `<目标>.new` 并 `Sync`。
   2. 已有的 `<目标>` 改名为 `<目标>.old`，作为备份。
   3. 把 `.new` 改名为 `<目标>`。

   如果源文件和目标文件是同一个文件（用户选的就是当前目录），跳过复制。
4. **快捷方式**：重建开始菜单快捷方式。全新安装时，勾选了才创建桌面快捷方式。升级时保留原来的桌面快捷方式，不新建。
5. **写卸载项**：版本号、目录、大小等。
6. **开机自启**：只在全新安装时写 `config.json`，升级时保留用户的设置。
7. **提交**：删除 `*.old`。删不掉的（例如被占用），留给已安装的 exe 下次启动时清理。

第 3 到第 6 步任何一步失败都会回滚：
- 把 `.old` 改回原名，删掉新复制的文件。
- 删掉本次新建的快捷方式。
- 卸载项写在最后，失败时旧的卸载项保持不变。

### 5.3 开机自启和便携运行

`shell.syncLoginItem` 会把 Run 项写成“当前 exe”。如果已经有安装，而当前运行的不是安装的那个（便携运行、dev 构建等），就不碰 Run 项（`Options.KeepLoginItem`），避免被便携副本抢走。

## 6. 卸载（`golauncher.exe --uninstall`）

1. 弹确认框，附带复选框“同时删除我的设置和数据”（`%APPDATA%\golauncher`、`%LOCALAPPDATA%\golauncher`），默认不勾。
2. 结束正在运行的实例（同 §5.2 第 2 步，排除自己）。
3. 删除开始菜单和桌面快捷方式：只删除目标指向安装目录的那个。
4. 删除开机自启项：`mygo.App.SetOpenAtLogin(false)`，按名字删除。
5. 删除卸载项。
6. 如果勾选了，删除用户数据。
7. 删除程序文件。运行中的 exe 删不掉自己，所以启动一个分离的、无窗口的 `cmd.exe`，逐个删除已知文件（`golauncher.exe`、`glctl.exe`，以及它们的 `.old`、`.new`）。
   - 文件还被占用时，每秒重试一次（`ping -n 2 127.0.0.1` 等待），最多 30 秒。卸载程序最后还要弹“已卸载”提示，用户可能过一会儿才关掉。
   - 删完后用 `rmdir`（不带 `/s`）删除空目录。用户如果装进了一个有其他文件的目录，那些文件不会被删。
   - 不用 `timeout`，因为它在没有控制台的进程里会立刻失败。
8. 提示“已卸载”，本进程退出。

## 7. 代码结构

| 包 | 职责 |
|----|------|
| `internal/install` | 与 UI 无关：参数解析、状态检测、版本比较、安装/升级/卸载的执行和回滚。所有系统操作都通过 `System` 接口完成，文件操作用真实文件系统，测试时用 `t.TempDir()` 和假的 `System` |
| `internal/winapi/install` | `System` 的 Windows 实现：注册表（`x/sys/windows/registry`）、`IShellLinkW` 快捷方式、版本资源、进程枚举和等待、启动进程、延迟删除、已知文件夹 |
| `internal/ui/installer.go` | 安装窗口的视图，通过 `InstallerHost` 选择文件夹、执行安装，以及“直接运行”和“取消” |
| `internal/shell/installer.go` | 胶水：在 `Run` 开头调用，把三者连起来，跑一个只有安装窗口的 MyGo 应用。卸载用 MyGo 的原生消息框（带复选框） |

界面文字都放在 `internal/i18n/locales/en.json` 里，前缀是 `install.`。

## 8. 边界情况

- **卸载项存在，但 exe 被删了**：当作未安装，重新安装会覆盖卸载项。
- **自己就在安装目录里，但没有卸载项**（例如用户手动解压到了那里）：当作未安装，弹安装界面。安装到同一目录时跳过复制，只注册。
- **同时开了两个安装窗口**：没有加锁。复制采用改名的方式，后完成的那个生效。
- **路径里有 `%`**：传给 `cmd /c` 时可能被当成环境变量展开。只影响延迟删除，最坏结果是留下空目录或文件，可以接受。
- **8.3 短路径、链接**：判断“是不是安装的那个 exe”时，先比较路径（`Clean` 后不区分大小写），不相同再用 `os.SameFile` 比较文件本身。
- **改过名的下载文件**（如 `golauncher_0.2.0.exe`）：安装后统一叫 `golauncher.exe`。等待退出时，进程名 `golauncher.exe` 和自己的文件名都算。
- **选择文件夹**：如果用户选的文件夹不叫 `golauncher`，就装到它下面的 `golauncher` 子目录，避免和其他文件混在一起。
- **测试**：单元测试只用临时目录和假的 `System`。Windows 集成测试（真实注册表和快捷方式）要设置 `GOLAUNCHER_INSTALL_TESTS=1` 才运行，用唯一的测试键和临时路径，结束后清理。

## 9. macOS（`golauncher.app`）

macOS 上同样是"程序自己安装自己"，只是安装的对象是整个 `.app` 包，而不是单个 exe。决策、代码和测试都沿用上面的结构：`internal/install` 的 `Installer` 多了 `Bundle` 模式，`internal/winapi/install` 多了 `install_darwin.go`、`update_darwin.go`。Apple Developer ID 签名和公证不在这一期的范围内。

### 9.1 包的形态

`scripts/macos/build.sh --app` 生成 `dist/golauncher.app`：

| 位置 | 内容 |
|------|------|
| `Contents/MacOS/golauncher`、`Contents/MacOS/glctl` | 两个程序（`--arch universal` 时是 x86_64 + arm64 的通用二进制） |
| `Contents/Info.plist` | 由 `install.InfoPlist` 生成（经 `scripts/macos/macbundle` 写出，有单元测试） |
| `Contents/Resources/golauncher.icns` | 用 `iconutil` 从 `assets/icon-{16,32,64,128,256,512}.png` 生成；1024 像素那张由 `sips` 从 512 放大 |
| `Contents/Resources/{en,zh-Hans}.lproj/InfoPlist.strings` | 权限说明文字的英文、中文版本 |
| `Contents/PkgInfo`、`Contents/_CodeSignature/` | `APPL????`；代码签名 |

Info.plist 的要点：

| 键 | 值 | 说明 |
|----|----|------|
| `CFBundleIdentifier` | `io.github.eziq1.golauncher` | 仓库 `github.com/eziq1/golauncher` 的反向域名。TCC 权限、登录项、钥匙串都按它识别应用；**以后不要改**（常量 `install.BundleID`）。glctl 的签名标识是 `io.github.eziq1.golauncher.glctl` |
| `CFBundleShortVersionString`、`CFBundleVersion` | `MAJOR.MINOR.PATCH` | macOS 要求只有数字，所以 `0.7.0-rc.1`、`0.6.0-3-gabc` 写成 `0.7.0`、`0.6.0`；没有版本号时 `0.0.0` |
| `GolauncherVersion` | 完整版本号 | 自安装和自动更新比较版本时读它（`install.PlistVersion`），没有时退回 `CFBundleShortVersionString` |
| `LSUIElement` | `true` | 菜单栏应用：没有 Dock 图标、没有自己的菜单栏（运行时也会 `SetActivationPolicy(Accessory)`） |
| `LSMinimumSystemVersion` | `13.0` | Ventura 起有 `SMAppService`（开机自启） |
| `NSAppleEventsUsageDescription` | 说明向访达、System Events、终端发 Apple 事件的用途 | 第一次控制某个应用时，"自动化"授权弹窗里显示的文字 |
| `NSDesktop/Documents/DownloadsFolderUsageDescription`、`NSRemovableVolumesUsageDescription`、`NSNetworkVolumesUsageDescription` | 文件搜索、打开文件 | 访问受保护的文件夹时的弹窗文字 |
| `NSLocalNetworkUsageDescription` | 网络工具的 Ping、端口检测 | macOS 15 的"本地网络"权限 |
| `NSLocationWhenInUseUsageDescription`、`NSLocationUsageDescription` | 显示当前 Wi-Fi 名称 | macOS 14 起读 SSID 需要定位权限 |
| `NSSystemAdministrationUsageDescription` | 刷新 DNS 缓存需要管理员 | — |
| 其他 | `CFBundleExecutable`、`CFBundleIconFile`、`CFBundlePackageType=APPL`、`NSHighResolutionCapable`、`NSPrincipalClass`、`CFBundleLocalizations=[en, zh-Hans]`、`LSApplicationCategoryType`、版权 | — |

辅助功能（Accessibility，粘贴要用）**没有**对应的 Info.plist 文字，系统用自己的提示。

MyGo 的 `mygo build` 也能打 `.app`，但它在 `cmd/mygo` 的 `package main` 里，要 `mygo.config.ts`（需要 Node/Bun 解析），不能当库用；这里借鉴了它的 Info.plist 写法和"先签内层代码、再签整个包"的顺序，自己写了一个小脚本。

### 9.2 安装位置与"已安装/便携"的判断

| 项 | 决定 |
|----|------|
| 默认位置 | 当前用户能写 `/Applications`（管理员账户能写）就装到 `/Applications`，否则装到 `~/Applications`，不需要输入密码（`install.ChooseAppsDir`）。安装位置指的是**放 `golauncher.app` 的文件夹**；选择别的文件夹时不再套一层 `golauncher` 子目录 |
| 安装记录（相当于卸载项） | `~/Library/Application Support/golauncher/install.json`：`dir`、`version`、`installDate`。没有记录时，在 `/Applications`、`~/Applications` 里找 `CFBundleIdentifier` 是我们的 `golauncher.app`，所以用户自己拖进"应用程序"的也算已安装 |
| 已安装的版本 | 每次读包里的 `Info.plist`（用户可能直接拖了新版本覆盖），读不到再用记录里的版本。二进制格式的 plist 用 `plutil -convert xml1` 转 |
| 判断 | 和 Windows 相同（§3）：运行的就是已安装包里的程序 → 正常启动；版本相同 → 用 `open` 启动已安装的那个，本进程退出；版本不同 → 升级/降级界面；没有安装 → 安装界面 |
| 不进入安装流程 | §2 的条件之外：**不在 `.app` 里的裸二进制**（`dist/golauncher`、`go run`）不安装自己，当作便携运行 |
| App Translocation | 从"下载"文件夹直接打开带隔离属性的应用时，macOS 会把它挂到一个只读的随机路径运行。这时它不是已安装的副本，安装界面会把它复制到"应用程序"文件夹——正好就是"移到应用程序文件夹"。`install.Translocated` 能识别这种路径 |

### 9.3 安装、升级与卸载

安装（`Installer.Install`，`Bundle` 模式）：

1. 校验目录、让正在运行的 golauncher 退出（同 §5.2 第 1、2 步；进程用 `sysctl kern.proc.uid` 按名字 `golauncher` 枚举，排除自己）。
2. **整体替换包**（`install.ReplaceBundle`）：先把运行中的包完整复制到目标文件夹里的 `.golauncher.app.new`（隐藏，访达里看不到），再把已有的 `golauncher.app` 改名为 `.golauncher.app.old`，最后把 `.new` 改名为 `golauncher.app`。两次改名都在同一个文件夹里，是原子的；任何时候 `golauncher.app` 都不会是写了一半的。复制保留权限和包内的相对链接，**不复制扩展属性**，所以新包没有隔离属性。
   - 只能整体替换：代码签名覆盖包里的所有文件，单换一个可执行文件签名就坏了。
   - 运行中的程序所在的包可以被改名、删除，macOS 不像 Windows 那样锁文件。
3. 没有快捷方式（包本身就是应用）。全新安装时把"开机时启动"写进 `config.json`（同 Windows）。
4. 写安装记录。失败就把 `.old` 改回来；成功后删除 `.old`（删不掉的由已安装的程序下次启动时 `install.Cleanup` 清理）。
5. 用 `open -n -a <包> --args …` 启动安装好的应用：由 Launch Services 启动，它不是安装程序的子进程，TCC 把权限记在它自己名下。

卸载：`/Applications/golauncher.app/Contents/MacOS/golauncher --uninstall`（终端里运行；macOS 上用户通常直接把应用拖到废纸篓，那样不会删开机自启和数据）。步骤同 §6：确认框（可勾选"同时删除我的设置和数据"，即 `~/Library/Application Support/golauncher` 和 `~/Library/Caches/golauncher`，日志也在后者里）→ 让其他实例退出 → 关闭开机自启（登录项和 LaunchAgent）→ 删除安装记录和 `update` 文件夹 → 一个分离的 `/bin/sh` 等本进程退出（最多 30 秒）后 `rm -rf` 掉 `golauncher.app`（以及残留的 `.old`、`.new`）。路径作为参数传给脚本，不拼进脚本；拒绝删除 `/`、`/Applications`、家目录。**不删除**钥匙串里的密码类偏好（`golauncher/<插件>/<偏好>`），需要时在"钥匙串访问"里搜 `golauncher` 删除。

### 9.4 签名、Gatekeeper 与 TCC

- **只做 ad-hoc 签名**（`codesign -s -`）：先签 `Contents/MacOS/glctl`（标识 `io.github.eziq1.golauncher.glctl`），再签整个包（标识 `io.github.eziq1.golauncher`），然后 `codesign --verify --strict --deep`。不开 Hardened Runtime（那需要为 Apple 事件等加 entitlements，ad-hoc 签名也得不到它的好处）。
- **Gatekeeper**：从网上下载的 zip 解压出来的应用带隔离属性，第一次打开会被拦下。处理办法（任选其一）：
  - 在访达里按住 Control 点应用（或右键）→"打开"→ 在对话框里再点"打开"；macOS 15 起这一步没有了，要先双击一次，再去"系统设置 › 隐私与安全性"，在页面底部点"仍要打开"。
  - 或者在终端里去掉隔离属性：`xattr -dr com.apple.quarantine ~/Downloads/golauncher.app`。
  之后安装程序复制出来的 `/Applications/golauncher.app` 不带隔离属性，自动更新写入的文件也会主动清除这个属性（[auto-update.md §12](auto-update.md)）。
- **TCC 权限（辅助功能、自动化）按签名身份记录**。ad-hoc 签名没有证书，它的"指定要求"（designated requirement）就是代码哈希（`cdhash`），**每次构建都不同**。所以：
  - 每次升级（包括自动更新）后，"系统设置 › 隐私与安全性 › 辅助功能"里的 golauncher 虽然还显示为打开，但已经不生效：要先用"−"删掉，再重新添加或重新授权。"自动化"的授权会在下次使用时重新弹窗。
  - 包的标识符是固定的，所以列表里不会出现多个 golauncher，只是授权要重给。
  - **让授权在本机重新构建后保留**：在"钥匙串访问 › 证书助理 › 创建证书"里建一个自签名的"代码签名"证书（例如叫 `golauncher local`），然后 `scripts/macos/build.sh --app --sign "golauncher local"`（或设置环境变量 `GOLAUNCHER_CODESIGN_IDENTITY`）。这时指定要求是"标识符 + 这张证书"，换构建不变。这只对自己构建的版本有效；发布版本（CI 构建）仍是 ad-hoc。
  - 根本的解决办法是 Developer ID 签名和公证（docs/macos-port.md §6.3），不在本期范围。
- 钥匙串：签名身份变化后，读取以前存的密码时 macOS 可能弹"允许访问"，点"始终允许"。

### 9.5 开机自启

- 主路径不变：`mygo.App.SetOpenAtLogin`。MyGo 在 macOS 13+ 用 `SMAppService.mainAppService`（纯 Go 的 purego 调用，"系统设置 › 通用 › 登录项"里显示 golauncher），12 用 LaunchAgent。它要求以 `.app` 运行。
- 后备（`winstall.LoginItem`）：如果 `SMAppService` 报错（例如需要用户在"登录项"里批准、或者 ad-hoc 签名的应用被拒绝），就写一个 LaunchAgent `~/Library/LaunchAgents/io.github.eziq1.golauncher.login.plist`，内容是 `open -a <包> --args --background`（只在 Aqua 会话、登录时运行）。它不立即加载（那会马上再启动一个 golauncher），下次登录生效。两种方式都开着时多启动一次也没关系，第二个实例会交给第一个。关闭开机自启时两者都删除。macOS 13+ 加 LaunchAgent 时会显示"已添加后台项目"的通知。
- 同 §5.3：已安装另一个副本时，便携副本不碰登录项。

### 9.6 再次打开、菜单栏图标

- **再次打开**：应用已经在运行时，双击 `golauncher.app` 或执行 `open -a golauncher`，macOS 不会启动第二个进程，而是给运行中的实例发 reopen 事件（`applicationShouldHandleReopen`）。MyGo 把它转成 `App.OnActivate`，shell 把它当作 `--show`：显示启动器。真的启动了第二个进程时（`open -n`、终端直接运行可执行文件），仍由 MyGo 的单实例锁把参数转给第一个（`OnSecondInstance`），各平台一样。
- **菜单栏图标**：Windows 托盘用彩色的 32×32 应用图标；macOS 菜单栏用 `assets/icon-menubar.png`：`icon-small.svg` 里的圆弧和箭头，黑色加透明度、去掉底色，44×36 像素（2 倍下 22×18 点），作为模板图片（`IconIsTemplate`）由系统按浅色/深色菜单栏着色。由 `go run ./scripts/macos/genmenubaricon` 生成。

### 9.7 代码与测试

| 位置 | 内容 |
|------|------|
| `internal/install/bundle.go` | `BundleID` 等常量、`BundleOf`、`ChooseAppsDir`、`Translocated`、`ReplaceBundle`、`CopyTree` |
| `internal/install/plist.go` | `InfoPlist`、`InfoPlistStrings`、`LaunchAgentPlist`、读取 `PlistStrings`/`PlistVersion` |
| `internal/install/ops.go` | `Installer.Bundle` 模式的安装（`installBundle`）、卸载、`Cleanup` |
| `internal/install/quarantine_*.go` | `ClearQuarantine`（只在 macOS 上做事） |
| `internal/winapi/install/install_darwin.go` | `System`：安装记录、在 `/Applications`/`~/Applications` 里查找、`FileVersion`、进程枚举和 `StopInstances`、`open` 启动、延迟删除 |
| `internal/winapi/install/update_darwin.go` | 更新助手的 `HelperSystem`、`scutil --proxy` 系统代理、开机自启的 LaunchAgent 后备 |
| `scripts/macos/build.sh --app`、`scripts/macos/macbundle` | 打包、图标、签名 |
| `internal/shell` | `newInstaller` 用 `Bundle` 模式、登录项走 `winstall.SetLoginItem`、`OnActivate` → `--show`、模板托盘图标 |
| `internal/ui/installer.go` | `InstallerInfo.Bundle`：不提供桌面快捷方式，选的文件夹就是安装位置 |

测试只用临时目录和假的 `System`：包路径的判断、默认位置的选择、Info.plist/InfoPlist.strings/LaunchAgent 的内容（在 Mac 上还过一遍 `plutil -lint`）、`ReplaceBundle`（替换、撤销、提交、可执行位、链接、越界链接被拒绝）、`Bundle` 模式的安装/升级/失败回滚/原地安装/卸载、清理残留、清除隔离属性；darwin 上的 `System`：从安装记录或候选文件夹读安装、别的应用的同名包不算、读版本、`open` 的参数、删除脚本的参数和拒绝的路径、登录项失败时写 LaunchAgent、进程枚举不含自己。**测试从不安装到 `/Applications`、不注册登录项、不替换正在运行的应用。**

### 9.8 还没做

- **安装界面的措辞**：安装窗口已经按平台取消息变体（`id@darwin`），但这几条还没有 macOS 版：`install.welcome`（提到 Windows 和开始菜单）、`install.perUser`（装到 `/Applications` 时并非“只为当前用户”）、`install.errorInvalidDir`（示例是 `C:\Apps\golauncher`）、`install.errorRunning`（“托盘图标”→ 菜单栏图标）、`install.step.shortcuts`（macOS 上没有快捷方式，这一步一闪而过）。留给消息目录的平台措辞工作一起加。
- 设置里没有“卸载”按钮，只能在终端运行 `--uninstall`；拖到废纸篓的卸载方式不会删除登录项、LaunchAgent 和数据。
- DMG（拖到“应用程序”的窗口）没有做，只发 zip。
- 运行在 App Translocation 路径时不会主动提示“移到应用程序文件夹”，只是照常弹安装界面。
