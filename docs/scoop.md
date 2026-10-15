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

## 8. 实现记录（R22）

> 分支 `worktree-agent-ac84eb4184826588e`，等待主控合并。

### 代码结构（`plugins/scoop`）

| 文件 | 内容 |
|------|------|
| `scoop.go` | 插件、`Options`、清单（7 个命令、4 个偏好项）、`Init`/`Close`、索引刷新、根搜索直达、`HandleAction` |
| `root.go` | 根目录和全局目录的探测 |
| `manifest.go` | 清单解析（宽松） |
| `index.go` | bucket 读取、已安装软件读取、`Index`、缓存 |
| `search.go` | 搜索打分 |
| `version.go` | 版本比较 |
| `queue.go` | 串行操作队列、停止、输出刷新 |
| `runner.go` | `Runner` 接口、PowerShell 执行器、`FakeRunner` |
| `views.go` | 搜索、已安装、可升级、bucket、清理、输出视图，条目、动作和详情 |
| `cleanup.go` | 可释放空间的本地计算 |
| `output.go`、`proc_*.go` | 从 `plugins/scripts` 复制（插件之间不能互相 import）：输出收集、无窗口启动、Job Object、OEM 解码 |

### 与设计的差异和补充

1. **"Clean Up Scoop" 是视图命令，不是 no-view 命令。** 宿主的确认框只显示通用文字，没法在确认前告诉用户能释放多少空间。所以命令打开一个视图：第一条显示总共能释放多少（旧版本 N 个、下载缓存 M 个文件，各占多少），它的动作 "Clean Up" 是 Destructive，按下后照常先确认；下面列出每个有旧版本的软件及其大小。测得本机计算一次要 1.7 秒，所以结果按"同一个索引、1 分钟内、期间没有执行过操作"缓存。
2. **操作结束后不弹 Toast。** SDK 没有在 `HandleAction` 返回之后再弹 Toast 的接口（Script Commands 也遇到同样的问题）。替代做法：输出视图的状态实时更新（排队中 / 运行中 / 完成 / 失败及退出码 / 已停止）；有排队或运行中的操作时，搜索、已安装、可升级、bucket、清理视图顶部都会出现 "Scoop Operations" 分组，可以回到输出视图或直接停止；失败写日志。以后 SDK 有了异步通知，在 `queue.go` 的 `run` 末尾加一行即可。
3. **新版 Scoop 改了文件名。** 实测本机 152 个软件里，47 个用的是 `scoop-install.json` / `scoop-manifest.json`，105 个是旧的 `install.json` / `manifest.json`，两种都支持。
4. **"有新版本"的判定**：bucket 里的版本比已安装的版本**新**才算，版本只是不相同（比如已安装的更新）不算。这样从 `versions` bucket 装了更新的版本时，不会显示成"降级"。`nightly` 永远不算。版本比较参照 Scoop 的 `Compare-Version`：数字段按数值比（任意长度），字母段按不区分大小写的字典序，同一位置数字比字母新，`1.0-beta` 比 `1.0` 旧。
5. **从 URL 安装的软件**（`install.json` 里只有 `url`、没有 `bucket`）不判断更新；"复制安装命令"给出的是 `scoop install <url>`。
6. **搜索结果里的已安装软件**：同名并且来源 bucket 相同（或未知）时按已安装处理，显示已安装软件的状态和动作（升级、锁定、卸载等）。
7. **执行方式**：脚本通过 `-EncodedCommand`（UTF-16LE 的 Base64）传给 PowerShell，而不是 `-Command "scoop ..."`，命令行上不存在任何引号问题。脚本里每个参数都是单引号字面量（`'` 和 PowerShell 同样认作单引号的 `‘’‚‛` 都双写）。另外加了 `-NonInteractive`（遇到提示不会卡住）、输出编码设为 UTF-8、`$ProgressPreference = 'SilentlyContinue'`，用 `try/catch` 保证找不到 `scoop` 时退出码不是 0。子进程的 PATH 最前面是 `<root>\shims`，并设置 `SCOOP=<root>`，即使启动器早于 Scoop 安装、或者用偏好项指定了别的根目录也能找到正确的 scoop。
8. **参数校验**：软件名和 bucket 名必须匹配 `^[A-Za-z0-9][A-Za-z0-9._+@-]*$`，自定义 bucket 的 URL 必须是 `https://`、`http://`、`ssh://` 或 `git@` 开头且不含空白和引号，防止被 scoop 当作选项（比如 `-g`）。全局安装的软件拒绝所有修改操作。不合法的请求返回失败 Toast，不入队。
9. **锁定 / 解锁也走队列和输出视图**，和其他操作保持一致（它们很快，但同样不能和别的 scoop 命令并发）。
10. **`glctl --real` 默认不执行 scoop**：新增 `builtin.DryRun`，`glctl` 在 `--real` 且没有 `--exec` 时设置它，Scoop 插件换成 `FakeRunner`，只把命令写进输出视图（`[not run] scoop install ...`）。为此 `internal/cli/platform.go` 的 `realPlugins` 多了一个参数。
11. **假环境的确定性**：`Options.Sync` 让 `Init` 同步建好索引，并让动作等操作结束再返回，`glctl` 的 golden 因此稳定；真实环境里动作立即返回、索引在后台建立。
12. **视图等待首个索引**：视图最多等 5 秒（宿主给视图 10 秒），超时显示 "Reading Scoop buckets…"，建好后 `Invalidate`；根搜索不等待。
13. **偏好项 `scoopPath`** 在 `Init` 时读取，修改后需要重启（偏好说明里写了）。指定的目录不是 Scoop 根目录时，回退到 `$SCOOP` 和默认位置并记日志，而不是直接判为不可用。
14. **全局目录**：`$SCOOP_GLOBAL` 或 `%ProgramData%\scoop`，里面有 `apps` 才算。假环境用 `<fixtures>/scoop/global`。
15. **缓存**：一个文件 `CacheDir\index.json`，带格式版本号和根目录（根目录不同就丢弃）。每个 bucket 的 stamp 是 `.git\FETCH_HEAD` 和清单目录这两者的修改时间；刷新时只重读 stamp 变了的 bucket，删除的 bucket 去掉。已安装软件每次都重读（本机 152 个约 60 毫秒，在后台），和上次结果相同就不 `Invalidate`。操作结束后、窗口显示时各刷新一次；刷新期间再次触发只补一次。
16. **清单解析很宽松**：允许重复键、非法 UTF-8 和 BOM；字段类型不对时留空而不是丢掉整个清单；`bin` 支持字符串、数组、`[路径, 别名, 参数]` 三种写法，并合并 `architecture` 里各架构的 `bin`（命令名取别名，没有别名取文件名去掉扩展名）；`license` 为对象时取 `identifier`；`description`、`notes` 为数组时拼接。bucket 根目录里没有 `version` 的 JSON（比如配置文件）不算清单。
17. **清单解析并行**：每个 bucket 的文件分给最多 GOMAXPROCS 个 `Host.Go` 任务。
18. **搜索打分**：名称完全相同 > 名称前缀 > 命令名（bin）相同 > 名称包含 > 命令名前缀 > 名称或简介中的单词前缀 > 简介包含 > 名称的子序列；多个词必须都匹配，第一个词的分数加倍。同分时 main、extras、versions 等常用 bucket 优先，再按名称长度和字母序。
19. **动作和快捷键**：

    | 动作 | 快捷键 | 出现在 |
    |------|--------|--------|
    | Install | `Enter` | 未安装 |
    | Update | `Ctrl+U`（有新版本且未锁定时为主动作） | 已安装 |
    | Open Folder | `Ctrl+Shift+F`（已安装且无需升级时为主动作） | 已安装 |
    | Open Homepage | `Ctrl+O` | 全部 |
    | Hold / Unhold Version | `Ctrl+Shift+H` | 已安装（非全局） |
    | Copy Install Command | `Ctrl+Shift+C` | 全部 |
    | Show Manifest in File Explorer | `Ctrl+Shift+M` | 全部 |
    | Uninstall（Destructive） | `Ctrl+X` | 已安装（非全局） |

20. **Bucket 视图**：已添加的 bucket 显示清单数量、更新时间、git 远程地址；动作有更新全部 bucket、打开目录、删除（Destructive）。推荐 bucket 来自 `apps\scoop\current\buckets.json`（即 `scoop bucket known`），去掉已添加的；另有一条 "Add Custom Bucket"，用命令参数输入名称和 URL。
21. **图标**：条目用图标名 `package`，宿主的内置图标里还没有，会回退成应用图标；`folder`、`trash` 已有。可以在 `internal/ui/icons.go` 里加一个 `package` 图标（不在本次范围内）。

### 测试

- `plugins/scoop/scoop_test.go`（hostfake + `FakeRunner`）：根目录探测、没装 Scoop 时不可用、清单的各种写法、版本比较、fixture 索引（新旧文件名、锁定、URL 安装、全局、根目录清单、非清单 JSON）、缓存命中与失效（stamp 不变时用缓存，FETCH_HEAD 变化只重读那个 bucket，新清单、删除 bucket、已安装软件变化）、搜索排序（含中文、多词、`maxResults`）、条目和动作、每个动作生成的命令（包括"全部升级"受 `updateBucketsFirst` 控制、自定义 bucket 的参数）、非法请求被拒、队列串行（同时最多一个）、停止排队中和运行中的操作、失败的操作（后续步骤不执行）、`Close` 停止运行中的操作、输出视图和 `Invalidate("output")`、窗口显示时刷新、根搜索直达、清理空间计算。
- `runner_windows_test.go`：真实的 PowerShell 执行器，但 `<临时根目录>\shims\scoop.ps1` 是一个假的脚本（打印参数、退出码 3；或者一直 sleep 用来测停止）。验证参数原样到达（含 `'`、空格、`$(...)`、`*`）、UTF-8 输出、`SCOOP` 环境变量、退出码、Stop 结束进程树。用的参数都不是 Scoop 命令，即使万一调用到真的 scoop 也只会报错。
- `real_test.go`：本机装有 Scoop 时只读地解析真实目录、搜索，并打印下面的实测数据；没有 Scoop 时跳过；断言没有启动任何命令。
- `internal/cli`：golden `run_scoop_search`、`run_scoop_search_chinese`、`run_scoop_installed`、`run_scoop_updates`、`session_scoop_install`（输出视图里能看到 `scoop install main/lazygit`）；`TestSessionScoopUninstallConfirms` 验证卸载先确认。`plugin_list`（没有 fixture 时 Scoop 显示为不可用）和 `session_confirm_done`（根列表多了 Scoop 的命令）已更新。
- Fixture `testdata/fixtures/scoop`：bucket `main`（6 个）、`extras`（3 个，含中文简介）、`local`（清单放在根目录，外加一个不是清单的 JSON）；已安装 git（有新版本）、nodejs（有新版本但锁定）、7zip（新文件名）、vscode、handmade（从 URL 安装）、一个没有 `current` 的半成品目录；全局安装的 vcredist2022；`apps\scoop\current\buckets.json`。

### 实测（本机，2026-10-15，文件系统缓存是热的）

| 项目 | 结果 |
|------|------|
| 规模 | 10 个 bucket，5977 个清单，152 个已安装软件（3 个有新版本） |
| 无缓存建索引（并行解析全部清单 + 读已安装 + 写缓存） | 189 ms（单 goroutine 解析 391 ms） |
| 缓存文件 | 2.0 MB |
| 读缓存（只解码） | 5.8 ms |
| 从缓存启动（解码 + 检查 stamp + 读已安装软件） | 67 ms，其中读已安装软件约 60 ms |
| 搜索延迟 | 平均 0.6 ms/次（7 个查询各 20 次，含 `x` 这种匹配很多的） |
| 清理空间计算 | 数十个旧版本共数 GB，另有下载缓存；用时 1.75 s |
| `glctl run builtin.scoop/search --text python --real` 整个进程 | 约 115 ms（有缓存时） |

`glctl --real` 的搜索、已安装、可升级三个视图都核对过：可升级列表有 3 个，和 bucket 里的版本一致。整个过程没有执行任何 scoop 命令（`--real` 不带 `--exec` 时只记录）。

### 合并时注意

- `internal/builtin/builtin.go`：`All` 和 `Fake` 各加了一个插件，新增 `DryRun`、`realScoop`、`fakeScoop`。
- `internal/cli/cli.go`、`platform.go`：`realPlugins(extra, exec)` 多了 `exec` 参数。
- golden：`plugin_list`、`session_confirm_done` 已更新；新增 5 个 golden。其他分支如果也往根列表里加命令，这两个 golden 需要重新生成。
