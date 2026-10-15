# Scoop 插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R22（见 [requirements.md](requirements.md)）
>
> 需求：为 Windows 下的 [Scoop](https://scoop.sh) 开发一个插件，功能包括搜索、安装、卸载、升级软件包；其他功能由我设计。

## 1. 定位

- 内置插件 `builtin.scoop`（`plugins/scoop`），和 Everything 一样依赖系统里已有的软件：**没有安装 Scoop 时插件不启用**，`Init` 返回 `plugin.ErrUnavailable`，设置页显示原因（"Scoop is not installed. Get it from scoop.sh."）。
- 读操作（搜索、已安装列表、可升级列表）**直接读 Scoop 的本地文件**，不调用 `scoop search`，因为那条命令每次都要启动 PowerShell，耗时以秒计，没法做到边输入边出结果。
- 写操作（安装、卸载、升级、清理……）**调用 Scoop 命令行**，在输出视图里实时显示过程，可以取消。

## 2. 功能清单

### 2.1 你要求的四项

| 功能 | 入口 | 实现 |
|------|------|------|
| 搜索软件包 | 命令 **Search Scoop Packages**（视图） | 在本地 bucket 清单索引里按名称、简介、可执行文件名模糊搜索 |
| 安装 | 搜索结果的主动作 `Enter` | `scoop install <bucket>/<name>` |
| 卸载 | 动作 `Ctrl+X`（Destructive，需要确认） | `scoop uninstall <name>` |
| 升级 | 已安装且有新版本时为主动作；也可以用"全部升级" | `scoop update <name>` / `scoop update *` |

### 2.2 补充的功能

| 功能 | 入口 | 说明 |
|------|------|------|
| 已安装列表 | 命令 **Installed Scoop Apps**（视图） | 显示版本、来自哪个 bucket、是否被锁定（hold），以及是否有新版本（"1.2.0 → 1.3.0"）。有新版本的排在前面 |
| 可升级列表 | 命令 **Scoop Updates**（视图），相当于 `scoop status` | 本地计算：已安装版本和 bucket 清单里的最新版本对比；提供"全部升级"动作 |
| 全部升级 | 命令 **Update All Scoop Apps** | 先 `scoop update` 更新 bucket，再 `scoop update *` |
| 更新 bucket | 命令 **Update Scoop Buckets** | `scoop update`。完成后重建索引，搜索结果里能看到新版本 |
| 管理 bucket | 命令 **Scoop Buckets**（视图） | 列出已添加的 bucket（清单数量、最后更新时间），以及 `scoop bucket known` 里的推荐 bucket。可以添加（`scoop bucket add`）和删除（Destructive） |
| 锁定 / 解锁 | 已安装软件的动作 | `scoop hold` / `scoop unhold`：锁定的软件在"全部升级"时会被跳过 |
| 清理旧版本 | 命令 **Clean Up Scoop**（Destructive） | `scoop cleanup *`，再 `scoop cache rm *`。执行前显示可以释放多少空间（本地计算） |
| 详情面板 | 所有列表 | 简介、主页、许可证、版本、bucket、可执行文件、安装目录、安装说明（清单里的 `notes`） |
| 其他动作 | 所有软件包 | 打开主页、复制安装命令、打开安装目录、在资源管理器中显示清单文件 |
| 根搜索直达（可选） | 偏好项 `showInRoot`，默认关闭 | 根搜索输入 `scoop <关键词>` 或 `sc <关键词>` 时，直接列出匹配的软件包 |

## 3. 数据来源（只读本地文件，速度快）

- **Scoop 根目录**：优先用偏好项 `scoopPath`，其次环境变量 `SCOOP`，最后是 `%USERPROFILE%\scoop`；判断依据是目录里有 `apps\scoop\current`。全局安装的软件在 `SCOOP_GLOBAL`（默认 `%ProgramData%\scoop`），只读显示，因为改动需要管理员权限。
- **Bucket 清单**：`<root>\buckets\<bucket>\bucket\*.json`（有些 bucket 把清单放在根目录，两种都兼容），解析其中的 `version`、`description`、`homepage`、`license`、`bin`、`notes`、`architecture`。
- **已安装软件**：
  - `<root>\apps\<name>\current\manifest.json`：取已安装版本；
  - `install.json`：取来源 bucket、架构，以及是否锁定（`hold`）。
- **索引**：后台用 `Host.Go` 构建，构建期间搜索显示"正在建立索引"。结果缓存在 `CacheDir`，以每个 bucket 目录的 `.git\FETCH_HEAD` 或目录修改时间作为失效依据。下次启动时直接读缓存，`main` 和 `extras` 加起来约 3000 个清单，读缓存应在 100ms 以内。
- **刷新时机**：窗口显示时检查 bucket 和 `apps` 目录有没有变化，有变化就后台重建，再调用 `Invalidate`。

## 4. 执行操作（调用 Scoop 命令行）

- **执行方式**：`powershell -NoProfile -ExecutionPolicy Bypass -Command "scoop <args>"`，参数逐个转义。如果有 `pwsh` 就优先用它。进程不显示窗口，加入 Job Object，可以连同子进程一起结束。
- **串行队列**：Scoop 不能并发执行，插件内部维护一个操作队列，同时只运行一个，其余排队。
- **输出视图**：和 Script Commands 的 fullOutput 相同，实时追加输出，通过 `Invalidate` 刷新。显示状态（排队中 / 运行中 / 成功 / 失败及退出码）和耗时，提供"停止"、"复制输出"动作。操作结束后弹 Toast，并刷新已安装列表和索引。
- **确认**：卸载、删除 bucket、清理都是 Destructive，会先弹确认。
- **超时**：安装和升级可能要好几分钟，所以操作不在 `HandleAction` 里等待完成：`HandleAction` 只把操作放进队列并打开输出视图，立即返回，不受 30 秒动作超时的限制。

## 5. 偏好项

| 名称 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `scoopPath` | path | 空 | 手动指定 Scoop 根目录 |
| `showInRoot` | bool | false | 根搜索里用 `scoop` / `sc` 前缀直达 |
| `maxResults` | number | 50 | 搜索结果的最大条数 |
| `updateBucketsFirst` | bool | true | "全部升级"前先执行 `scoop update` |

## 6. 测试

- **Fixture**：`testdata/fixtures/scoop/` 放一个假的 Scoop 根目录，包括两个 bucket（各几个清单，含中文简介）、几个已安装软件（其中一个有新版本、一个被锁定），以及 `apps\scoop\current`。
- **单元测试**（hostfake）：
  - 根目录探测，以及没装 Scoop 时插件不可用；
  - 清单解析，包括 `architecture` 和 `bin` 的多种写法；
  - 索引与缓存失效；
  - 搜索排序，可升级状态的计算；
  - 每个动作生成的命令行（使用假的执行器，只记录要执行的命令）；
  - 队列串行执行、停止、输出流和 `Invalidate`。
- **`glctl`**：`builtin.Fake` 用 `<fixtures>/scoop` 作为根目录，执行器换成假的，只记录命令不真正执行。补充 golden 测试：搜索、已安装列表、可升级列表、安装动作生成的命令。
- **真实环境测试**：本机装有 Scoop（`~\scoop`），只做**只读**测试，验证真实目录能被解析、搜索能出结果。**绝不在测试里真的安装或卸载软件。**

## 7. 已知限制 / 以后

- 全局安装（`-g`）的软件只读显示：修改需要管理员权限，第一版不做提权。
- 不解析 `checkver` 和 `autoupdate`；"有新版本"以本地 bucket 清单为准，所以要先"更新 bucket"才能看到最新版本。
- 很多 Scoop 安装的软件可以通过应用启动器（`builtin.apps`）直接打开；Scoop 插件只负责包管理，不重复提供启动功能。
