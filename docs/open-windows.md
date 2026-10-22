# 应用已在运行时切换到它的窗口（R121，GitHub #6）

## 1. 需求

[GitHub issue #6](https://github.com/eziq1/golauncher/issues/6)（nemowon）：

1. 呼出 golauncher，输入 `code`，第一行是 VS Code，按 Enter：打开一个 VS Code 窗口。
2. 再呼出，输入 `code`，按 Enter：**现在**又开了第二个新窗口。
3. **期望**：列出已经打开的 VS Code 窗口，按最近使用排序；选中（悬停或高亮）的窗口显示一张较大的预览图；按 Enter 切换到这个窗口。

## 2. 现状

- `builtin.apps`（[plugins/apps/apps.go](../plugins/apps/apps.go)）的主操作永远是 `ActionOpen`：通过开始菜单快捷方式或 `shell:AppsFolder\<ID>` 启动。它不知道这个应用是否在运行。VS Code 这类应用每启动一次就开一个新窗口。
- 已经有的、可以复用的部分：
  - `process.Windows()`（[internal/winapi/process/process_windows.go](../internal/winapi/process/process_windows.go)）：按任务栏规则枚举顶层窗口（可见、无所有者、非工具窗口、有标题、未被 DWM 隐藏），UWP 从 `ApplicationFrameWindow` 的 `CoreWindow` 子窗口找真正的 PID，不发消息，未响应的窗口也不会卡住。`EnumWindows` 按 Z 序返回，**Z 序就是最近激活的顺序**，可以直接当“最近使用”排序。
  - `process.SwitchTo(hwnd)`：最小化的先还原，再 `SetForegroundWindow`。进程管理器的“切换到窗口”（`Ctrl+Enter`）已经在用。
  - 插件 SDK：`ActionPush` 推入一个列表视图，`View.ShowDetail` 在列表右侧显示选中项的 `Detail`，`Detail.Image` 可以是一张图片文件。也就是说“窗口列表 + 右侧大预览”不需要改 SDK。
- macOS：`process.Windows()` 只按应用返回（没有屏幕录制权限拿不到窗口标题），`SwitchTo` 激活整个应用。而且 macOS 上打开已经运行的 `.app` 本来就是激活它，不会开新窗口，问题主要在 Windows。

## 3. 方案

### 3.1 哪些窗口属于这个应用

和任务栏分组的规则一致，按顺序：

1. 窗口的 AppUserModelID（`SHGetPropertyStoreForWindow` 读 `PKEY_AppUserModel_ID`），没有就取进程的显式 AppUserModelID，和 `shell.App.ID` 比较。打包应用、设了 AUMID 的桌面应用（Chrome 的 PWA、Edge 的应用窗口等）靠这一条区分开，不会被并到浏览器下面。
2. 否则按进程映像路径和 `shell.App.Path` 比较（不区分大小写）。VS Code（`Code.exe`）、记事本、资源管理器（`explorer.exe` 的文件夹窗口）都走这一条。
3. 快捷方式指向启动器程序的应用（Squirrel 的 `Update.exe --processStart`、某些 JetBrains 启动脚本）第一期不处理：匹配不到就当作没在运行，行为和现在一样。

排除 golauncher 自己的窗口。窗口枚举只在用户按 Enter（或打开窗口列表）时做一次，约 1～5 ms，不影响输入时的搜索速度；根搜索结果里也不显示“正在运行”的标记（避免每次按键都枚举，第二期可以考虑缓存后加一个小圆点）。

### 3.2 Enter 的行为（需要你决定，见 §5）

建议：

| 这个应用的窗口数 | Enter | 说明 |
|---|---|---|
| 0 | 启动（和现在一样） | |
| 1 | 直接切换到这个窗口 | 和 macOS、PowerToys Run 的习惯一致，少按一次键 |
| ≥ 2 | 推入“窗口列表”视图 | 按最近使用排序，第一行（最近用的那个）默认选中，再按 Enter 切换；右侧显示预览 |

另外每个应用都加一个“新建窗口”操作（`Alt+Enter`，也在 `Ctrl+K` 里），总是启动一个新实例；窗口列表最后一行也是“新建窗口”。原来的“以管理员身份运行”“打开文件位置”等操作不变。

可以加一个设置“应用已在运行时按 Enter：切换到窗口 / 总是启动新的”，默认“切换到窗口”，给依赖旧行为的人一条退路。

### 3.3 窗口列表视图

- 每行：应用图标、窗口标题（`SubtitleSensitive`/`Sensitive` 处理：窗口标题可能含文档名，不交给 AI）、右边显示“最小化”或所在显示器。
- 支持输入过滤（`FilterLocally`，标题和拼音）。
- 操作：Enter 切换；`Ctrl+W` 关闭窗口（`process.CloseWindows`，发 `WM_CLOSE`，应用自己会提示保存）；“新建窗口”。
- 切换后隐藏启动器（`Result{Hide: true}`，和进程管理器一样）。

### 3.4 预览图

两种做法：

| 做法 | 效果 | 代价 |
|---|---|---|
| **A. 截图成 PNG**：`PrintWindow(hwnd, PW_RENDERFULLCONTENT)` 画到内存位图，缩到约 480 px 宽存进缓存目录，作为 `Detail.Image` | 静态图，打开列表那一刻的样子。`PW_RENDERFULLCONTENT` 能画出 Chromium/Electron（VS Code、Chrome）等 GPU 渲染的窗口 | 只在插件里改；每个窗口约 10～40 ms，在后台逐个截，截好一张 `Invalidate` 一次，列表本身立即显示。**最小化的窗口截不到**，显示图标 + 标题 + 元数据 |
| **B. DWM 实时缩略图**：`DwmRegisterThumbnail` 把目标窗口的实时画面合成到启动器窗口的预览区域（Alt+Tab 和任务栏缩略图的做法） | 实时画面，最小化的窗口也有（显示最小化前的最后一帧） | 要在宿主层加一种新的 Detail（“窗口缩略图”，带 HWND），MyGo 要给出预览区域在窗口里的位置，跟着滚动、缩放、主题切换更新，切换选中项时注销旧的。只能用于 Windows |

建议**第一期做 A**（插件内完成、风险小），第二期再做 B。管理员权限运行的窗口在普通权限下 `PrintWindow` 可能失败，同样退回图标。

截图文件放在 `CacheDir()/thumbs/`，以 HWND 命名，列表关闭（`ViewClosed`）时删除；不写进日志，不交给 AI（窗口内容可能敏感）。

### 3.5 代码位置和复用

- `internal/winapi/process`：`Window` 增加 `Path`（进程映像路径）和 `AUMID`；新增 `Thumbnail(hwnd, width) ([]byte, error)`（Windows 实现，其他平台 `ErrUnsupported`）。进程管理器、以后的“窗口切换器”（R30 第一梯队里还没做的那个）和这里共用同一套枚举，不另写一份。
- `plugins/apps`：通过一个 `Windows` 接口（真实实现 / fixture）拿窗口，方便 `glctl` 和单元测试；新增窗口列表视图和“新建窗口”操作，补中英文文案。
- 测试：fixture 覆盖 0/1/多个窗口、AUMID 优先于路径、PWA 不并入浏览器、golauncher 自己被排除；真机测试只读（枚举不报错、截一张自己的窗口）。

### 3.6 macOS

第一期保持现状（打开已运行的 `.app` 就是激活它）。按窗口列出要用辅助功能 API（`AXUIElementCreateApplication(pid)` → `kAXWindowsAttribute`，`AXRaise` 切换），已有辅助功能权限可以复用；截图需要屏幕录制权限（ScreenCaptureKit），放到以后。

## 4. 分期

1. **第一期（Windows）**：§3.1 匹配、§3.2 Enter 行为和“新建窗口”、§3.3 窗口列表、§3.4 A 静态截图。
2. **第二期**：DWM 实时缩略图；根搜索里“正在运行”的标记；macOS 按窗口列出。
3. 独立的“窗口切换器”插件（搜索所有打开的窗口）可以在同一套代码上很快做出来，按需排期。

## 5. 待你决定

1. Enter 的行为：按 §3.2 建议（1 个窗口直接切换、多个显示列表），还是按 issue 原文“总是先显示列表”？
2. 预览：第一期只做静态截图（A），还是直接做实时缩略图（B）？
3. 是否需要“总是启动新的”这个设置？
4. 是否在 issue #6 下回复方案？
