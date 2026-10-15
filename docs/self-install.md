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
