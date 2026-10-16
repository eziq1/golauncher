# 自动更新设计

对应需求：R54（见 [requirements.md](requirements.md)）。

> 增加自动升级：自动连接 GitHub 检测新版本，有的话直接下载并升级。

本文先写设计（§1–§10），实现完成后在 §11 记录实现情况和与设计的差异。

## 1. 目标与范围

- golauncher 定期查询 GitHub Releases 的**最新正式版**；比当前版本新，就下载对应架构的 zip，校验后替换已安装的程序，在不打扰用户的时候重启到新版本。
- 只有**已安装的副本**（[self-install.md](self-install.md) 装到安装目录、`State.Self` 为真的那个）会更新自己。便携运行（`--portable`、没有安装或安装的是另一个副本）、开发构建（版本 `dev`，或 `git describe` 带提交数、`-dirty`、裸哈希的版本）只能手动检查，不会下载和安装；设置里写明原因。
- 不更新用户数据。配置、数据库、插件数据都不在安装目录里，更新只替换 `golauncher.exe` 和 `glctl.exe`。
- 从不自动降级。

## 2. 是否使用 MyGo 的 `plugins/updater`

design.md 的 M6 原计划用 MyGo 的 `plugins/updater`。读过它的源码（`mygo@v0.5.1/plugins/updater`、`updater.go`、`docs/updates.md`）后，**决定不用**，自己实现，理由：

| 方面 | `plugins/updater` / `mygo.Updater` | golauncher 的情况 |
|------|-----------------------------------|-------------------|
| 构建方式 | 依赖 `mygo build`：它用 `mygo.config.ts` 里的 `updates` 配置，通过 `-ldflags -X` 注入更新源和公钥，并为每个平台生成 `update-windows-amd64.json` 清单和签名的归档 | 我们用 `go build` + `scripts/build.ps1`，发布产物是 zip + `checksums.txt`（[release.md](release.md)），没有它的清单 |
| 签名 | 必须有 ed25519 私钥，没有私钥就不生成更新文件，`Updater.Enabled()` 为假 | 目前没有签名密钥；需要用户先配置 CI 密钥 |
| 替换方式 | 把整个程序目录的条目改名为 `.<name>.old-<pid>` 再放入新文件，下次启动删掉 | 我们的安装还包括卸载项（`DisplayVersion`）、开始菜单快捷方式、`glctl.exe`；用自安装的逻辑才能一起更新 |
| 安装时机 | 下载完立即替换文件，用户点“重新启动”或下次启动才运行新版本 | 需要在“安全时刻”（启动器隐藏、没有后台任务、AI 没在回答、用户最近没在用）自动重启 |
| 失败处理 | 没有启动健康检查和回滚 | 需要：新版本起不来就回滚，并记住坏版本 |
| 界面 | 自带一个 Sparkle 风格的更新窗口和自己的 14 种语言文字、自己的状态文件 `updater.json` | 我们要在设置 › 关于里显示，文字走自己的 i18n（en、zh-CN），设置写进 `config.json` |

借鉴的部分：检查间隔和失败后提前重试的算法（`nextCheck`）、改名正在运行的 exe（Windows 允许改名但不允许覆盖）、ed25519 签名的思路。

## 3. 检查

- **接口**：`GET https://api.github.com/repos/eziq1/golauncher/releases/latest`。这个接口只返回最新的、非草稿、非预发布的 Release（[release.md §6](release.md)：只有带后缀的 tag 才是预发布），正好是“稳定通道”。
- **请求头**：`Accept: application/vnd.github+json`、`X-GitHub-Api-Version: 2022-11-28`、`User-Agent: golauncher/<版本> (+https://github.com/eziq1/golauncher)`；带上次的 `ETag` 作为 `If-None-Match`。返回 304 时用缓存的上次结果（GitHub 规定 304 不计入限额）。
- **不带 token**。未认证的限额是每个 IP 每小时 60 次，我们一天一次，远低于限额。遇到 403/429 且 `X-RateLimit-Remaining: 0`，或者有 `Retry-After`，记下解除时间（`X-RateLimit-Reset`），在那之前不再请求。
- **超时**：检查整体 15 秒；下载每次连接 30 秒没有数据就断开重试。
- **代理**：先看环境变量（`HTTPS_PROXY` 等，Go 的 `ProxyFromEnvironment`），没有再读 Windows 的系统代理（`WinHttpGetIEProxyConfigForCurrentUser` 里手动设置的代理和例外列表）。自动配置脚本（PAC）不支持，这时直连；记录在 §10。
- **解析**：`tag_name` 必须是 `vMAJOR.MINOR.PATCH`，`draft`、`prerelease` 必须为假；版本号去掉 `v`。用自安装的 `install.CompareVersions` 比较，只有严格更新（`order > 0` 且可排序）才算有新版本。
- **跳过的版本**（用户点了“跳过此版本”）和**坏版本**（回滚过的）不会自动下载；手动检查时仍然显示，并说明。
- **调度**：
  - 启动 30 秒后检查一次；但如果上次成功检查不到 1 小时（例如刚更新重启），就不查，等下一个周期。
  - 之后按设置每天或每周一次，从上次成功检查算起。
  - 失败后 1 小时（不超过间隔）重试；被限流时等到解除时间。
  - 调度用每分钟一次的“节拍”比较墙上时间，而不是一个长定时器：电脑睡眠后醒来，到期的检查马上就会做。
- **手动检查**：设置 › 关于的“检查更新”按钮、托盘菜单“检查更新…”、启动器命令“Check for Updates”（`builtin.update`）。手动检查不受间隔限制（仍然遵守限流）。

## 4. 下载与校验

- **选择产物**：`golauncher_<版本>_windows_<GOARCH>.zip`，按名字精确匹配；同一个 Release 里必须有 `checksums.txt`，否则不下载。
- **位置**：`%LOCALAPPDATA%\golauncher\update\downloads\<版本>\`，只有当前用户可写。先下到 `<名字>.part`，断点续传（`Range`，服务器回 206 就接着写，回 200 就从头写），最多重试 3 次（间隔 2、4 秒）。大小以 API 给的 `size` 为准，超过 100MB 拒绝。
- **校验**：
  1. 下载 `checksums.txt`（最大 64KB），解析 `<sha256>  <文件名>` 行，找到 zip 那一行；没有这一行就拒绝。
  2. 计算 zip 的 SHA-256，不一致就删掉文件、报错，不安装。
  3. 解压时只取根目录下的 `golauncher.exe` 和 `glctl.exe`（必须有 `golauncher.exe`），拒绝路径穿越，限制解压大小。
  4. 读解压出来的 `golauncher.exe` 的版本资源（ProductVersion），必须等于要安装的版本，防止 Release 里放错包。
- **诚实地说**：程序**没有代码签名**。完整性依靠 HTTPS（GitHub 的证书）和同一个 Release 里的 `checksums.txt`。这能防住传输损坏和中间人，但防不住“有人拿到了仓库的发布权限，同时替换 zip 和 `checksums.txt`”。
- **可选的签名校验（ed25519）**：
  - 发布工作流在配置了密钥时，用私钥对 `checksums.txt` 签名，产物多一个 `checksums.txt.sig`（base64 编码的 64 字节 ed25519 签名）。
  - 公钥在构建时通过 `-ldflags -X github.com/eziq1/golauncher/internal/update.PublicKey=<base64>` 写进程序。
  - **程序里有公钥时，签名是必需的**：没有 `.sig` 或者签名不对，一律拒绝。只做“有就验”没有意义，攻击者删掉 `.sig` 就绕过了。没有公钥的旧版本（包括第一个带自动更新的版本）只校验 SHA-256。
  - 私钥只放在 GitHub 的 Secrets 里，不进仓库；我不生成、不提交任何私钥。配置步骤见 §9。
- **以后**：Authenticode 代码签名（需要证书），以及把 SmartScreen 信誉也考虑进去。

## 5. 安装

### 5.1 原则

复用自安装（`install.Installer.Install`，升级模式 `Upgrade: true`）：先写 `.new`、把旧文件改名为 `.old`、再改名放入，失败回滚；重建开始菜单快捷方式；最后写卸载项（版本号变成新版本）；保留用户的桌面快捷方式和开机自启设置。

Windows 不能覆盖正在运行的 exe，而 `Install` 会先让正在运行的 golauncher 退出。所以安装不能在 golauncher 进程里做，而是交给一个**更新助手进程**。

### 5.2 更新助手

- 助手是**当前（旧）版本自己的一个副本**：`update\helper\golauncher-update.exe`，用 `--apply-update <plan.json>` 启动。用旧版本做助手，是因为旧版本已知能正常运行，监督和回滚的逻辑也就可靠；如果用新版本做助手，新版本本身坏了就没人回滚了。
- 文件名不叫 `golauncher.exe`，所以“结束其他 golauncher 进程”时不会算到它。
- `--apply-update` 在 `shell.Run` 最前面处理，早于自安装和单实例锁。

### 5.3 步骤

主程序（旧版本）：

1. 新版本已下载、校验、解压完毕（“已就绪”）。
2. 等到安全时刻（§6），或者用户点了“立即重启”。
3. 把自己复制成助手，写 `plan.json`（安装目录、旧版本、新版本、新文件所在目录、备份目录、自己的 PID），启动助手。
4. 正常退出（不再询问后台任务，因为安全时刻已经保证没有）。

助手：

1. 等旧进程退出，最多 30 秒；超时就放弃，什么都不改。
2. **备份**：把安装目录里的 `golauncher.exe`、`glctl.exe` 复制到 `update\previous\`（只保留上一个版本）。
3. **安装**：`Installer{Self: 新的 golauncher.exe, Version: 新版本}.Install(Upgrade)`。失败时 `Install` 自己会回滚文件；助手重新启动旧版本，记录错误。同一个版本安装失败 3 次后不再自动尝试。
4. **启动新版本**：`golauncher.exe --background`，环境变量 `GOLAUNCHER_UPDATE_HEALTH=<标记文件路径>`。
5. **健康检查**：新版本启动完成（窗口、托盘、快捷键都建好）并稳定运行 10 秒后，写标记文件（然后从自己的环境变量里删掉这个变量，免得传给它启动的程序）。助手等最多 90 秒：
   - 标记出现：成功。
   - 新进程在标记出现前退出（崩溃），或者超时：失败。
6. **失败就回滚**：结束新进程（如果还在），用备份重新安装旧版本（同样走 `Install`，卸载项改回旧版本），把新版本记为坏版本，启动旧版本。
7. 结果写到 `update\result.json`，助手退出。

新版本启动后读 `result.json`：如果是更新到自己的版本，就显示通知“已更新到 vX.Y.Z”，点击打开 Release 页面（“更新内容”）。旧版本回滚后启动，读到回滚结果，就显示“无法更新到 vX.Y.Z，已恢复 vA.B.C”。

### 5.4 清理

- 主程序启动时，如果没有更新在进行，删除 `update\helper\` 和除“已就绪”版本以外的下载目录。
- `update\previous\` 保留上一个版本，供以后排查，下次更新时覆盖。
- 卸载时总是删除 `update` 文件夹（它不是用户数据）；勾选“同时删除我的设置和数据”时本来就会删除整个 `%LOCALAPPDATA%\golauncher`。安装目录里的 `.old`、`.new` 已在卸载列表里。

## 6. 安全时刻

自动安装只在下面的条件**全部**满足时进行（每分钟检查一次）：

| 条件 | 原因 |
|------|------|
| 启动器面板隐藏 | 用户没在用 |
| 设置窗口隐藏 | 同上 |
| 没有正在运行或排队的后台任务（`Tasks.Active() == 0`） | 重启会中断它们 |
| 没有 AI 回答正在生成（AI 插件的 `Busy()`） | 同上 |
| 距离用户最后一次打开或关闭启动器、设置已过 3 分钟 | 用户刚用完可能马上又要用 |

“立即重启”按钮跳过最后一条，但仍然要求没有后台任务和 AI 回答；不满足时提示原因。

## 7. 设置与界面

### 7.1 配置（`config.json`）

```json
"updates": { "mode": "auto", "frequency": "daily" }
```

- `mode`：`auto`（自动下载并安装，默认）/ `notify`（只通知）/ `off`（不自动检查）。
- `frequency`：`daily`（默认）/ `weekly`。
- 缺省或空字符串取默认值，所以不升 `schemaVersion`（[storage.md §2.2](storage.md)：只增加字段不升版本号）。

运行状态（上次检查时间、ETag、缓存的 Release、跳过的版本、坏版本、已就绪的版本）不放进 `config.json`（它可能漫游、会被导出），而是放在 `%LOCALAPPDATA%\golauncher\update\state.json`。

### 7.2 设置 › 关于

- 版本号；“更新”小节：
  - 状态：已是最新（上次检查时间）/ 正在检查 / 正在下载 vX（百分比）/ vX 已就绪，将在空闲时安装 + “立即重启”/ 有新版本 vX + “下载并安装”“跳过此版本”/ 检查失败（原因）。
  - “检查更新”按钮，“更新内容”链接（Release 页面）。
  - “自动更新”下拉框（自动下载并安装 / 只通知 / 关闭），“检查频率”下拉框（每天 / 每周）。
  - 说明：只从 GitHub Releases 获取正式版本。
  - 便携副本和开发构建：说明“这个副本不是安装版（便携运行或开发构建），不会自动更新；请从 GitHub 下载新版本”，只保留“检查更新”。

### 7.3 其他入口

- 托盘菜单“检查更新…”：结果用通知告诉用户（已是最新 / 有新版本 / 失败）。
- 启动器命令“Check for Updates”（宿主插件 `builtin.update`，无视图命令）：同步检查，结果显示为提示。
- 只通知模式下发现新版本：通知一次（每个版本一次），点击打开设置 › 关于。

## 8. glctl

- `glctl update check [--real] [--current VER] [--arch ARCH] [--latest VER]`：检查，输出 JSON（默认，`--json` 也接受；`--format text` 输出文字）。默认用假的 GitHub（本地 `httptest` 服务器，`internal/update/fakegithub`，最新版本由 `--latest` 指定，默认 1.0.0），不访问网络；`--real` 才查真实的 GitHub（只读）。
- `glctl update download --dry-run [...]`：下载并校验（SHA-256、可选签名、解压、版本资源除外），不安装，结束后删除临时文件。
- `glctl update sign <file>`：发布工作流用，从环境变量 `GOLAUNCHER_UPDATE_SIGNING_KEY` 读私钥（base64 的 32 字节种子），写出 `<file>.sig`；`glctl update verify <file>` 用程序里的公钥（或 `--key`）验证。
- `glctl update keygen`：生成一对新密钥打印到标准输出，供你自己保存（不写文件）。

## 9. 发布工作流与签名配置（需要你操作）

签名是可选的。不配置时一切照旧，只校验 SHA-256。要启用：

1. 本地运行 `glctl update keygen`，得到私钥（种子）和公钥，私钥存进密码管理器。
2. 在 GitHub 仓库 Settings › Secrets and variables › Actions：
   - Secret `UPDATE_SIGNING_KEY` = 私钥；
   - Variable `UPDATE_PUBLIC_KEY` = 公钥。
3. 之后的发布：工作流把公钥写进程序，对 `checksums.txt` 签名、用公钥验证一遍，上传 `checksums.txt.sig`。只配置了公钥没有私钥时，工作流失败，避免发出“要求签名却没有签名”的版本把用户卡住。
4. **私钥丢了**，带这个公钥的版本就再也收不到自动更新，只能手动下载安装。**私钥泄露**，攻击者能发布让程序接受的更新（前提是他还要能往这个仓库发布 Release）。

## 10. 已知限制

- 系统代理只支持手动设置的代理，不支持 PAC 脚本和“自动检测设置”。
- 只有当前用户安装（`%LOCALAPPDATA%\Programs`）；不需要管理员权限，所以不会有 UAC。
- 新版本“启动后稳定 10 秒”才算健康；之后才出现的崩溃不会自动回滚，需要手动重新安装旧版本（`update\previous\` 里有备份）。
- 增量更新（只下载差异）不做：一个 zip 不到 20MB。
- 环境变量 `GOLAUNCHER_NO_UPDATE=1` 关闭自动更新（测试、E2E 用）；`GOLAUNCHER_UPDATE_API=<url>` 把 API 地址换成别的（只接受 HTTPS 或本机 HTTP），用于在真机上用假服务器演练整个流程。

## 11. 实现记录（2026-10-16）

### 11.1 代码结构

| 位置 | 内容 |
|------|------|
| `internal/update/github.go` | `Client.Latest`（ETag、User-Agent、API 版本头、限流识别 `RateLimitError`、只接受 HTTPS 或本机 HTTP、重定向不降级）、`Release`/`Asset`、`ParseRelease` |
| `internal/update/verify.go` | 产物选择 `SelectAssets`、`ParseChecksums`、`VerifyFile`、ed25519 的 `Sign`/`VerifySignature`/`GenerateKey`、构建时注入的 `PublicKey`、`Updatable`（是否发布版本）、`Newer` |
| `internal/update/download.go`、`prepare.go` | 小文件下载、带断点续传和重试的归档下载、`Prepare`（校验和 → 可选签名 → 下载 → SHA-256 → 解压 `golauncher.exe`/`glctl.exe`）、`Extract` |
| `internal/update/schedule.go` | 设置 `ParseSettings`、`NextCheck`（调度）、`SafeMoment`（安全时刻） |
| `internal/update/service.go`、`state.go` | `Service`：节拍 `Tick`、手动 `Check`、`InstallNow`、`Skip`、状态 `Status`、事件 `Notify`；`state.json`、`result.json` |
| `internal/update/apply.go` | 更新助手 `Helper.Apply`（等旧进程退出 → 备份 → `install.Installer.Install(Upgrade)` → 启动 → 健康检查 → 失败回滚）、`Plan`、健康标记 |
| `internal/update/plugin.go` | 宿主插件 `builtin.update` 的“Check for Updates”命令、`Summary`（检查结果的文字） |
| `internal/update/proxy.go` | 解析 Windows 代理设置（`SystemProxy`） |
| `internal/update/fakegithub` | 假的 GitHub（API + 下载，支持 ETag/304、限流、503、断流、Range），测试和 `glctl update` 共用 |
| `internal/winapi/install/update_windows.go` | `HelperSystem`（等待进程、带环境变量启动）、`StartDetached`、读取系统代理 |
| `internal/shell/update.go` | 桌面胶水：创建服务、安全时刻的判断（主线程上读窗口状态）、通知、托盘“检查更新…”、重启进入助手、`--apply-update` 助手入口、健康标记 |
| `internal/ui/updates.go` | 设置 › 关于的“更新”小节 |
| `internal/cli/update.go` | `glctl update check/download/sign/verify/keygen` |
| `plugins/ai/ai.go` | `Busy()`：有回答正在生成 |

### 11.2 决定和与设计的差异

- **没有用宿主的后台任务系统显示下载进度**。后台任务是按插件归属、会在退出时询问、“安全时刻”本身又要求没有后台任务；自动下载如果算一个任务，会出现在任务列表里、让退出多一次询问。下载进度改为显示在设置 › 关于里。
- **手动检查不发通知，调用方自己显示结果**：托盘用 Windows 通知，命令用启动器提示，设置页显示在页面上。只有“只通知”模式下的自动检查会主动通知（每个版本一次）。
- **下载失败的退避**：设计里没写。自动下载失败（网络、校验不通过、版本资源不符）后，同一个版本 1 小时后才再试，避免每分钟重下 20MB。
- **安装失败计数**：助手报告“安装失败”（不是“启动失败”）时计一次，同一个版本 3 次后不再自动安装；“启动失败”回滚过的版本直接记为坏版本。两种情况手动“下载并安装”仍然可以。
- **卸载总是删除 `update` 文件夹**（`install.Paths.UpdateDir`）。
- **`glctl update` 的假 GitHub 没有用 fixtures 目录**，而是内置生成（版本由 `--latest` 指定），zip 是确定的，golden 稳定。
- **第一个带自动更新的版本**之前的版本（≤ 0.4.0）没有这套代码，需要手动升级一次。

### 11.3 测试

- `internal/update`（全部离线，`httptest` 假 GitHub）：
  - Release 解析（GitHub 原样的 JSON）、版本与预发布/草稿、`Updatable`/`Newer`（含不降级、`dev`、`git describe` 版本）；
  - 按架构选产物、缺少产物或 `checksums.txt`；校验和解析；
  - ETag/304、限流（403 + `X-RateLimit-*`、429 + `Retry-After`、普通 403、404）、超时、拒绝 HTTP；
  - 下载：校验通过、zip 被换（不匹配）、`checksums.txt` 缺行或缺失、签名（正确、别的密钥、校验和被改、签名被删、程序没有公钥）、503 重试、断流后 `Range` 续传、超大小、zip 路径穿越；
  - 调度（假时钟）：启动延迟、重启后不立即查、每天/每周、失败后 1 小时重试、限流、时钟回拨；安全时刻的各个条件；
  - 服务（假时钟 + 假 GitHub）：自动下载并在安全时刻重启（检查 plan、助手是当前程序的副本）、ETag、每周、失败重试、限流（手动检查也不发请求）、只通知（每个版本通知一次）、关闭、便携/开发构建、跳过、坏下载不重复下载、版本资源不符、助手结果（更新成功通知一次、回滚记坏版本、失败 3 次、助手死掉）、清理残留；
  - 助手（**测试里现编的假 exe**：`testdata/fakeapp` 用 `-X main.mode=healthy|crash|hang` 编出三种“新版本”；临时安装目录 + 假的 `install.System`，**不碰真实安装和注册表**）：成功更新（文件、卸载项版本、备份、启动参数和环境变量）、新版本崩溃回滚、新版本卡住超时回滚（进程被结束）、安装失败、旧进程不退出。
- `internal/ui`：关于页的各个状态、按钮、“只通知”“关闭”、便携副本说明、中文布局（`checkLayout`）。
- `internal/cli`：golden `update_check*`、`update_download_dry_run`；签名/验证/生成密钥；`TestUpdateRealGitHub` 要设置 `GOLAUNCHER_UPDATE_REAL_TESTS=1` 才查询真实的 GitHub（只读）。
- `internal/winapi/install`：`HelperSystem` 的启动、环境变量、结束、等待（`GOLAUNCHER_INSTALL_TESTS=1` 时运行，只启动 `cmd.exe`）；系统代理对本机地址直连。
- `internal/install`：卸载删除 `update` 文件夹。`internal/config`：`updates` 的默认值、旧文件、导出导入。
- 手动验证：`glctl update check --real` 和 `glctl update download --dry-run --real` 对真实的 v0.4.0 Release 成功（找到 amd64 zip，SHA-256 与 `checksums.txt` 一致，zip 根目录有两个 exe）；用临时生成的一对密钥（只在临时目录，未提交）跑了 `build.ps1 -UpdatePublicKey` → `glctl update sign` → `verify`，换一把密钥签名则验证失败。
- 没有在真机上跑完整的“下载 → 退出 → 助手安装 → 重启 → 回滚”：那会改真实的卸载项、开始菜单和正在运行的 golauncher。这部分逻辑由上面的助手测试覆盖；第一次真实验证要等下一个版本发布后，在已安装的上一版本上观察（`%LOCALAPPDATA%\golauncher\logs\update.log`）。也可以用 `GOLAUNCHER_UPDATE_API` 指向本机的假服务器演练。
