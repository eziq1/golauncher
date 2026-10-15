# 系统命令与设置直达插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R31（见 [requirements.md](requirements.md)）

内置插件 `builtin.system`（`plugins/system`）。

## 1. 系统命令

| 命令 | 实现 | 是否需要确认 |
|------|------|--------------|
| Lock（锁屏） | `LockWorkStation` | 否 |
| Sleep（睡眠） | `SetSuspendState(false, …)`（powrprof） | 否 |
| Hibernate（休眠） | `SetSuspendState(true, …)`，只在系统开启了休眠时显示 | 否 |
| Restart（重启） | `ExitWindowsEx(EWX_REBOOT)`，需要先启用关机权限 `SeShutdownPrivilege` | **是** |
| Shut Down（关机） | `ExitWindowsEx(EWX_POWEROFF)` | **是** |
| Sign Out（注销） | `ExitWindowsEx(EWX_LOGOFF)` | **是** |
| Empty Recycle Bin（清空回收站） | `SHEmptyRecycleBinW`。副标题用 `SHQueryRecycleBinW` 显示回收站里的文件数和大小 | **是** |
| Toggle Dark Mode（切换深浅色） | 改注册表 `HKCU\…\Themes\Personalize` 下的 `AppsUseLightTheme` 和 `SystemUsesLightTheme`，再广播 `WM_SETTINGCHANGE("ImmersiveColorSet")`。副标题显示当前模式 | 否 |
| Mute / Unmute（静音） | 发送 `VK_VOLUME_MUTE` 按键，最简单可靠 | 否 |
| Volume Up / Down（音量） | 发送 `VK_VOLUME_UP` / `VK_VOLUME_DOWN` | 否 |
| Turn Off Display（关闭显示器） | `SendMessage(HWND_BROADCAST, WM_SYSCOMMAND, SC_MONITORPOWER, 2)` | 否 |
| Show Desktop（显示桌面） | Shell 的 `ToggleDesktop` | 否 |
| Task Manager、Control Panel 等 | 归入 §2 的经典工具 | 否 |

- 每个命令都有中英文关键词，比如 `lock`/`锁屏`/`sp`，`shutdown`/`关机`/`gj`，并带上图标。
- 需要确认的命令标记为 `Destructive`，由宿主统一弹出确认框。
- 关机、重启、注销执行前，先检查是否有后台任务在运行（R24）。如果有，确认框里会提醒，例如"有 2 个后台任务会被中断"。为此需要宿主提供一个查询运行中任务数的接口：在 SDK 里给 `Host` 加一个只读查询，或者把这个数传进 `Context`。实现时选一种改动最小的方式。

## 2. 设置直达

- **Windows 设置页**：嵌入一份 `ms-settings:` 目录，约 150 条。每条有英文标题、中文标题、关键词（含拼音），以及适用的 Windows 版本（例如有些页面只在 Windows 11 上有）。
  - 例子：`ms-settings:bluetooth`（蓝牙）、`ms-settings:display`（显示）、`ms-settings:sound`（声音）、`ms-settings:network-wifi`（WLAN）、`ms-settings:defaultapps`（默认应用）、`ms-settings:windowsupdate`（Windows 更新）、`ms-settings:privacy`（隐私）、`ms-settings:powersleep`（电源和睡眠）、`ms-settings:appsfeatures`（应用和功能）、`ms-settings:dateandtime`（日期和时间）。
  - 打开方式：`ActionOpen("ms-settings:…")`。
- **经典工具**：
  - 控制面板；
  - 程序和功能 `appwiz.cpl`、网络连接 `ncpa.cpl`、系统属性 `sysdm.cpl`；
  - 设备管理器 `devmgmt.msc`、服务 `services.msc`、磁盘管理 `diskmgmt.msc`、事件查看器 `eventvwr.msc`、任务计划程序 `taskschd.msc`、组策略 `gpedit.msc`（只在专业版及以上显示）；
  - 注册表编辑器、任务管理器、资源监视器、命令提示符（管理员）。

  打开方式：`ActionOpen`，必要时设置 `Verb: runas`。
- **分组**：根搜索里分为"System"（系统命令）和"Settings"（设置页）两组。都是普通的根条目，由宿主做模糊匹配，不需要前缀。

## 3. 测试（安全第一）

- **所有会改变系统状态的调用都可以替换**：锁屏、睡眠、关机、清空回收站、改注册表、发送按键，都放在一个 `System` 接口后面。**测试一律使用假的实现，绝不真的执行**。
- 只读的调用可以在真机上测：查询回收站大小、读取当前的深浅色模式、检测是否开启了休眠。
- 设置目录的测试：每条都有中英文标题；URI 格式正确；关键词能搜到，比如 `蓝牙`、`bt`、`wifi`、`gx`（更新）。
- `glctl` golden：搜索 `lock`、`关机`、`蓝牙`；执行"清空回收站"会弹出确认。

## 4. 实现记录（2026-10-15）

代码：`plugins/system`（`system.go` 条目与动作、`backend.go` 的 `System` 接口和 `Fake`、`settings.go` 与 `settings.json` 设置目录和经典工具、`locales/en.json`），Win32 调用在 `internal/winapi/system`（非 Windows 平台返回 `errors.ErrUnsupported`）。

**系统命令**（13 条，分组 System）

- 锁屏的标题用 "Lock"（和 Windows 开始菜单一致），不叫 "Lock Screen"：设置目录里有同名的锁屏界面设置页，而且这样输入 `lock` 时它排第一。
- 关键词在 `en.json` 里，中英文混写，并为常用命令加了拼音首字母（`sp`、`sm`、`xm`、`cq`、`gj`、`zx`、`jy`、`hsz`）。插件会把中文关键词转成带空格的拼音（"关机" → "guan ji"），宿主据此匹配 `guanji`、`guan`。"重启"的"重"是多音字，go-pinyin 默认读 zhong，所以手动加了 `chong qi`。
- 休眠只在 `IsPwrHibernateAllowed` 为真时显示。
- 睡眠、休眠、关闭显示器：动作立即返回 `Hide`，500ms 后在后台（`Host.Go`）执行。这样启动器先隐藏、Enter 键已松开，显示器不会马上被唤醒；`SetSuspendState` 会一直阻塞到唤醒，也不会占用动作的 30s 超时。插件关闭时取消还没执行的命令。后台执行失败用 `Host.Toast` 提示。
- 重启、关机、注销用 `ExitWindowsEx`，不加 `EWX_FORCE`，应用仍可阻止。重启、关机、睡眠、休眠之前先启用 `SeShutdownPrivilege`。
- 显示桌面没有走 `System` 接口，而是 `ActionOpen("explorer.exe", "shell:::{3080F90D-D7AD-11D9-BD98-0000947B0257}")`，也就是 Shell 的 ToggleDesktop，不需要 COM。
- 音量用 `keybd_event` 发送 `VK_VOLUME_*`。音量加减每次按 5 下（每下 2%，共 10%），而且不隐藏窗口，可以连按 Enter。静音读不到当前状态（需要 Core Audio COM），所以标题是 "Toggle Mute"。
- 深浅色：读写 `AppsUseLightTheme` 和 `SystemUsesLightTheme`，再用 `SendMessageTimeout` 广播 `WM_SETTINGCHANGE("ImmersiveColorSet")`。
- 回收站：`SHQueryRecycleBinW` 和 `SHEmptyRecycleBinW(SHERB_NOCONFIRMATION)`（启动器已经确认过了）。回收站已经是空的时候不调用，直接提示。
- 副标题里的状态（回收站内容、当前深浅色）在 `Init` 和每次 `window-shown` 时在后台读取，有变化就 `Invalidate`，`RootItems` 只读缓存。

**后台任务提醒（R24）**：选的是最小改动，在 SDK 的 `plugin.Action` 上加 `EndsSession bool`（JSON 字段 `endsSession`），不改 `Host` 接口，所以 `hostfake`、`rpcserve`、`extproc` 和协议都不用动，外部插件也能直接用。宿主弹确认框时，从任务管理器的 `Indicator()` 取当前排队和运行中的任务数：有任务就提示 "N background tasks will be interrupted."，没有就提示 "Open apps will be closed. Save your work first."。任务数是在弹框那一刻算的，比插件在 `RootItems` 时读到的更准。顺带改了一处：动作标题和条目标题相同时，确认框标题用 "{{.item}}?"，避免出现 `Shut Down “Shut Down”?`。

**设置直达**

- `settings.json` 有 159 条，每条有英文标题、中文标题、分类、关键词，部分有 `win: "10"`/`"11"`。按 `RtlGetVersion` 的 build 号过滤，22000 及以上算 Windows 11。
- 标题按界面语言选中文或英文，另一种语言放进关键词。只把中文标题转成拼音：如果所有中文关键词都转拼音，拼音首字母几乎能匹配任何两三个字母的查询，噪音太大。
- 分类显示在 Accessory，而不是 Subtitle：宿主会用 Subtitle 做匹配，"Privacy & security" 这样的分类名会让 `vsc` 之类的查询命中一大片。
- 主要动作是打开，次要动作是复制 URI（`Ctrl+Shift+C`）。
- 经典工具 20 个，标题和关键词在 `en.json`。组策略 `gpedit.msc` 和本地用户和组 `lusrmgr.msc` 只在非家庭版显示（`EditionID` 以 `Core` 开头算家庭版）。"命令提示符（管理员）"的主要动作就是 `runas`，其它工具都有"以管理员身份运行"（`Ctrl+Shift+Enter`）。Subtitle 显示实际命令（如 `devmgmt.msc`），所以也能直接搜命令名。

**安全**

- 改变系统状态的调用全部经过 `System` 接口。测试和 `glctl` 的假环境用 `Fake`：Windows 11 专业版、浅色、回收站 3 项 1.5 MB，只记录调用。
- `glctl --real` 不带 `--exec` 时换成 `DryRun()`：只读取真实状态，改动只记录不执行。
- 真机上只测只读调用：回收站查询、深浅色读取、休眠检测、版本号和版本类型。

**测试**

- `internal/winapi/system`：上面的只读调用，以及家庭版判断。
- `plugins/system`：目录检查（URI 格式和唯一性、中英文标题、分类、版本、总数至少 140、设计里列出的 10 个页面都在）；按 Windows 版本和版本类型过滤，休眠是否显示；13 条命令的图标、分组、`Destructive`、`EndsSession` 和回调；每条命令在 `Fake` 上的调用记录和返回结果（隐藏窗口、提示）；失败提示（包括后台执行的）；关闭插件会取消待执行的睡眠；`window-shown` 时刷新；大小格式化；拼音；用真实引擎跑 30 个查询，检查排名（`lock`、`锁屏`、`sp`、`关机`、`gj`、`guanji`、`重启`、`cq`、`蓝牙`、`bt`、`lanya`、`wifi`、`wlan`、`gx`、`更新`、`devmgmt`、`环境变量`、`组策略` 等）。
- `glctl` golden：`query_system_lock`、`query_system_shutdown_zh`、`query_system_bluetooth_zh`，清空回收站的确认和完成（`session_system_empty_bin_ask/done`）。另外还有一个会话测试：有后台任务时，关机的确认框会提示 "1 background task will be interrupted."。
- 已有的 golden（`plugin_list`、`query_vsc`、`query_pinyin`、`query_scripts`、`query_clock`、`session_context_new_folder`）末尾多了 System 和 Settings 的低分匹配，只是新增，原有结果的顺序没变。

**没有做的**

- 设置页不检测硬件，比如没有电池的电脑也会显示节电模式。
- 系统命令是根条目，不是 manifest 命令，所以不能设别名和全局快捷键（设计要求分成 System 和 Settings 两组）。如果以后需要给"锁屏"之类设快捷键，可以再补成命令。
