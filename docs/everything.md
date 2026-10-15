# Everything 文件搜索插件

对应需求：R8（见 [requirements.md](requirements.md)）。

> 调用 Everything 来搜索电脑上的文件。它是 Windows 特有的插件，依赖系统上已经安装的 Everything；检测到没有安装时，就不启用这个插件。

## 1. 方案

### 1.1 和 Everything 通信

直接实现 Everything 的 IPC 协议，用纯 Go 编写（`internal/winapi/everything`），不依赖它的 SDK DLL，也不调用 `es.exe`，同样不需要 cgo。

1. 查找 Everything 的 IPC 窗口：先找窗口类 `EVERYTHING_TASKBAR_NOTIFICATION`，再找 1.5 alpha 版的 `EVERYTHING_TASKBAR_NOTIFICATION_(1.5a)`。
2. 在一个锁定的 OS 线程上创建 message-only 窗口，用来接收回复。
3. 通过 `WM_COPYDATA` 发送 `EVERYTHING_IPC_QUERY2` 请求，请求的字段有：文件名、路径、大小、修改时间。
4. 接收并解析 `EVERYTHING_IPC_LIST2` 回复，解析时做边界检查。
5. 等待回复时会响应取消，也有超时：以 ctx 的截止时间为准，最长 10 秒。

结构体布局取自官方的 `everything_ipc.h`，在 amd64 和 arm64 上都适用。

### 1.2 检测是否安装

依次检查，命中任何一项就算已安装：

1. 正在运行的 Everything：通过 IPC 窗口找到所属进程，拿到 exe 路径。
2. 注册表：`App Paths\Everything.exe`；HKLM、WOW6432Node、HKCU 下的 Uninstall 条目，条件是条目名为 `Everything`，或者 DisplayName 以 Everything 开头且发布者是 voidtools。
3. 常见安装目录：Program Files（含 x86），`%LOCALAPPDATA%\Programs\Everything`，以及 Scoop 的安装目录和 shim。

### 1.3 插件行为（`plugins/everything`，ID `builtin.everything`）

- **没有安装**：`Init` 返回包装了 `plugin.ErrUnavailable` 的错误，并附上原因（"Everything is not installed. Get it from voidtools.com."，原因文本会本地化）。插件不启用，设置页的插件列表显示这个原因，开关不可操作。这个"插件不可用"的机制是为此新加的 SDK 能力，其他依赖外部软件的插件也能用。
- **已安装但没有运行**：启动时在后台用 `-startup` 参数启动一次。搜索时如果仍然没有运行，列表只显示一条"Everything is not running"，并提供"启动 Everything"的动作。
- **命令** `search`（Search Files）：输入文字后调用 Everything 搜索。每条结果显示文件名、所在目录（用户目录缩写成 `~`）和相对修改时间。右侧详情面板显示路径、类型、大小和修改时间。

可用的动作：

| 动作 | 快捷键 |
|------|--------|
| 打开 | `Enter` |
| 在资源管理器中显示 | `Ctrl+Shift+F` |
| 复制路径 | `Ctrl+Shift+C` |
| 复制文件 | `Ctrl+Alt+C` |

偏好设置：

| 偏好项 | 默认值 | 说明 |
|--------|--------|------|
| `maxResults` | 100 | 每次最多显示的结果数，取值范围 1–1000 |
| `matchPath` | false | 是否连同路径一起匹配 |
| `showInRoot` | false | 打开后，根搜索输入 3 个字符以上时附带最多 5 个文件结果；这些结果排在应用和命令之后 |

### 1.4 测试

- 不依赖 Everything 的测试：用合成的二进制数据测试 IPC 编码和解析；用 fixture 后端（`testdata/fixtures/files.json`）测试插件逻辑。`glctl --fixtures` 也会用到这份 fixture。
- 依赖真实 Everything 的测试：没有运行 Everything 时自动跳过，所以在 CI 上不会运行。包括真实搜索、模拟快速打字的压力测试 `TestStressTyping`，以及界面层的 `TestRealFileSearch`。

## 2. 已知限制

- 没有在 1.5 alpha 版和 arm64 上实测过。
- Everything 需要管理员权限才能启动时，自动启动会失败，只显示提示，不会弹出 UAC。
- 结果按名称排序。按修改时间或使用次数排序只需改一行，但前提是用户在 Everything 设置里为对应字段建立了索引，否则会很慢。
- 详情面板里的修改时间用的是固定格式 `2006-01-02 15:04`，因为 Localizer 还没有日期格式化功能。

## 3. 变更记录

- 2026-10-15：首次实现。同时修复了"搜索无结果时程序崩溃"的问题，详见 [reliability.md §1](reliability.md)。
