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
