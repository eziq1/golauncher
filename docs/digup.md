# 集成 DigUp：按内容搜索本机文件（R135）

对应需求：R134（调研）、R135（集成文档）。见 [requirements.md](requirements.md)。

> 调研 https://github.com/ARahim3/DigUp ，看看有没有 API 接口，这样我可以集成它。
> 我电脑已经安装了 DigUp beta，你写一份集成文档。

## 1. DigUp 是什么

[DigUp](https://github.com/ARahim3/DigUp)（MIT）用 Google DeepMind 的 EmbeddingGemma 2 在本机按**内容的含义**搜索文件：图片按画面内容，PDF 和文档精确到页，录音和视频精确到时刻，还能搜代码。模型在本机运行（llama.cpp，Windows/Linux 上有 Vulkan 时用 GPU），不联网。Mac 版是正式版；Windows 和 Linux 版是 beta（本文按 0.7.0-beta.3 实测）。

它和 golauncher 已有的文件搜索互补：Everything 插件按**文件名**找，DigUp 按**内容**找（“海滩上的狗”找到照片，“合同里的付款条款”找到 PDF 的那一页）。

## 2. 有没有 API

**没有正式的对外 API**：没有 HTTP 服务、插件接口，也没有文档化的 SDK。但它的命令行工具 `digup` 有两种可用的方式：

| 方式 | 说明 | 适合 |
|---|---|---|
| `digup search <词> --json` | 一次性：启动、加载模型、搜索、退出 | 不适合边打字边搜（每次都要加载模型） |
| **`digup search-service`** | **常驻进程**，标准输入输出逐行 JSON；源码注释写明是给“不能链接 DigUpKit 的外壳程序”用的，DigUp 自己的 Windows/Linux 界面（Tauri）就这样调用它 | ✅ 采用 |

注意：这是 DigUp 的**内部协议**，作者没有承诺兼容，升级可能改动（§7）。

## 3. 本机安装布局（Windows，0.7.0-beta.3 实测）

| 东西 | 位置 |
|---|---|
| 程序 | `%LOCALAPPDATA%\DigUp\DigUp.exe`（按用户安装，无需管理员） |
| 搜索进程 | `%LOCALAPPDATA%\DigUp\helper\digup.exe`（旁边是 Swift 运行时、llama.cpp、PDFium、ffmpeg 的 DLL） |
| 索引 | `%LOCALAPPDATA%\DigUp\index\index.sqlite`（SQLite WAL；`previews\` 是缩略图缓存） |
| 模型 | `%LOCALAPPDATA%\DigUp\Models\`：`embeddinggemma-2-Q8_0.gguf`（文本，296 MB）、`mmproj-embeddinggemma-2-Q8_0.gguf`（图像和音频，529 MB） |
| 设置 | `%APPDATA%\com.abdurrahim.DigUp\settings.json`：`folders`（要搜的文件夹，字符串数组）、`skipped`、`skipTypes`、`codeFolders`、`codeSkipped`、`processorOnly` 等 |
| 版本 | 注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\*` 中 `DisplayName = DigUp` 的 `DisplayVersion` |

DigUp 运行时有两个 `digup.exe` 子进程：`search-service`（**不带参数**启动，用默认索引，文件夹之后用 `folders` 请求传入）和 `index-service --backfill <各文件夹>`（建索引）。

`digup` 找模型的顺序：环境变量 `DIGUP_MODELS`（设置了就只在那里找），否则 macOS 的 `~/Library/Application Support/DigUp/Models` 或 Hugging Face 缓存。**在 Windows 上必须设 `DIGUP_MODELS=%LOCALAPPDATA%\DigUp\Models`**，DigUp 的界面也是这样传的。

## 4. `search-service` 协议（实测）

启动：`digup.exe search-service [--index DIR] [文件夹...] [--code-root DIR]... [--exclude DIR]... [--exclude-ext EXT]... [--encoder-idle 秒]`，标准错误是日志。

一行一个 JSON。请求带 `id`，回复带同一个 `id`；**没有 `id` 的行是事件**。失败的回复是 `{"id":…,"ok":false,"error":"…"}`（未知操作也是这样）。

| 请求 | 回复 | 说明 |
|---|---|---|
| （启动后） | 事件 `{"event":"ready","index":"…"}` | 约 20 ms |
| `{"op":"folders","roots":[…],"skipped":[…],"types":[…],"code_roots":[…],"code_skipped":[…]}` | `{"ok":true}` | **必须先发**：结果只来自这些文件夹，不发就一条结果都没有（实测）。各项可省；没有 `code_roots` 会关掉代码索引 |
| `{"op":"prewarm"}` | `{"ok":true,"encoder":"ready","vectors":n}` | 加载查询模型和向量，实测约 630 ms；启动器打开时发 |
| `{"op":"embed_ahead","q":"…","code":false}` | `{"ok":true}` | 预先编码并缓存查询词；边打字边发，停顿时的语义搜索就不用再编码 |
| `{"op":"search","q":"…","meaning":true,"kinds":[…],"limit":n,"code":false,"prefix_last":true}` | `{"hits":[…],"strong":n,"meaning":bool,"ms":{"embed":…,"search":…}}` | `meaning:false` 只按关键词（不加载模型）；`kinds` 取 `screenshot`、`image`、`pdf`、`doc`、`audio`、`video`；`strong` 是“强匹配”的条数，其后是较弱的结果；`prefix_last` 让最后一个词按前缀匹配（边打字时用） |
| `{"op":"text","segment":id,"code":false}` | `{"text":"…"}` | 命中片段的原文，用于预览 |
| `{"op":"render","path":"…","kind":"pdf\|video\|image","loc":n,"pixels":n}` | `{"image":"路径"}` | 生成 JPEG 缩略图（PDF 第 `loc` 页、视频第 `loc` 秒的帧、浏览器显示不了的图片如 HEIC），放在 `<索引>\previews\` |
| `{"op":"status"}` | `{"encoder":"stopped\|starting\|ready\|unavailable","vectors":n,"files":n,"searchable":n,"pending":n,"unfinished":n,"code_vectors":n}` | |
| `{"op":"summaries","code":false}` | `{"roots":[{"path","missing","unreadable","files","indexed","pending","failed","skipped","skipped_reasons","unfinished","kinds"}]}` | 每个文件夹的索引进度 |
| `{"op":"reload"}` | `{"ok":true,"vectors":n,"code_vectors":n}` | 索引变化后重新读取 |
| `{"op":"quit"}` | `{"ok":true}`，然后退出 | |

事件还有 `{"event":"encoder","state":"ready","seconds":0.6,"source":"google/embeddinggemma-2@… llama.cpp-vectors1 …"}`（查询模型就绪）。

**结果 `hits` 的字段**（实测）：`path`、`kind`、`segment`（`page`、`page_image`、帧、音频窗口、段落等）、`segment_id`、`score`、`modified`、`size`、`info`（如 PDF 的 `pages`、`scanned_pages`、`read_to`）、`loc`（PDF 的页码，音视频的秒数）、`loc_end`、`focus`、`excerpt`（摘录）、`z`、`cosine`、`keyword_rank`（有才出现）。

**实测耗时**（约 1 万个向量的索引，本机）：关键词搜索 17 ms；语义搜索 18–85 ms（编码约 10 ms）；首次 `prewarm` 630 ms。

**实测内存**：冷启动私有内存 4 MB；只做关键词搜索 5 MB；`prewarm` 之后 265 MB（模型的文本部分和全部向量）。`--encoder-idle` 秒（默认 300）没有查询就释放模型和向量，关键词搜索照常；空闲再翻倍时间，进程自己退出。

**生命周期**：标准输入关闭时，进程在两次请求之间退出。在 Windows 上它不监视父进程（`ParentWatch` 只在 macOS 和 Linux 上），由外壳负责结束它。

## 5. 集成方案

新增内置插件 **`builtin.digup`**（只在检测到 DigUp 时出现）。

### 5.1 检测与启动

- 检测：`%LOCALAPPDATA%\DigUp\helper\digup.exe` 存在，或从注册表的 `InstallLocation` 找。没有就不显示任何条目，设置页说明“需要安装 DigUp”并给出下载链接。
- 启动：第一次需要时启动 `digup.exe search-service --encoder-idle 60`，环境变量 `DIGUP_MODELS=<安装目录>\Models`；放进 golauncher 已有的作业对象（`proc.NewKillOnCloseJob`，R122 抽出的那个），golauncher 退出或崩溃它都会结束；`CREATE_NO_WINDOW`；标准错误写到 golauncher 的日志目录 `digup.log`。
- 启动后立刻发 `folders`：文件夹**跟随 DigUp 自己的设置**（读 `settings.json` 的 `folders`、`skipped`、`skipTypes`，有代码文件夹时也带上）；设置文件变化时（启动器显示时比较修改时间）重发。
- 进程挂了：下一次请求时重启一次；再挂就在这次会话里停用并提示，不反复重启（和 R122 钩子进程的做法一致）。
- 复用：`internal/extproc` 管外部插件进程（按需启动、崩溃后退避重启、频繁崩溃就停用、卡住就结束、空闲停止、作业对象），但它说的是 JSON-RPC 2.0，DigUp 是自己的 `{"id","op"}` 格式。做法：把 extproc 里与协议无关的进程管理抽成共用的一层，extproc 和 DigUp 插件都用它，只是各自的编解码不同；不另写一套进程管理。

### 5.2 搜索

- **前缀 `dig `**（可在设置里改）：后面的文字交给 DigUp。
  - 打字时：每次按键发 `embed_ahead`，同时发 `meaning:false, prefix_last:true` 的关键词搜索，立即显示结果；
  - 停顿约 250 ms：发 `meaning:true` 的语义搜索，替换结果；
  - 启动器显示、并且上次用过 DigUp 时，发 `prewarm`，第一次语义搜索就不必等模型。
- **类型筛选**：`dig pdf: 付款条款`、`dig 图片: 海滩` 之类的小前缀映射到 `kinds`；`dig code: …` 映射到 `code:true`（DigUp 的代码搜索）。
- **根搜索**：不直接混入（语义搜索要加载 265 MB 的模型）。没有其他结果时，给一个后备条目“用 DigUp 搜索 ‘…’”（`Transient`），回车进入 DigUp 视图。
- 结果条目：图标按 `kind`；标题是文件名；副标题是“第 N 页”/“03:25”/摘录的开头；`strong` 之后的结果放进“较弱的匹配”分组。路径和摘录可能含隐私：`SubtitleSensitive`，`Detail` 里的摘录标 `Sensitive`，不交给 AI。
- 预览：`ShowDetail`；PDF、视频、HEIC 等用 `render` 生成缩略图（`pixels` 取预览区宽度），文档用 `text` 显示原文。

### 5.3 打开

| 类型 | Enter | 说明 |
|---|---|---|
| PDF | 在第 `loc` 页打开 | 默认 PDF 程序是 Edge/Chrome/Firefox 时用 `file:///…#page=N`；SumatraPDF 用 `-page N`；Acrobat 用 `/A "page=N"`；其他程序从头打开 |
| 视频、录音 | 在第 `loc` 秒打开 | VLC `--start-time=`、mpv `--start=`、MPC-HC `/start <毫秒>`；其他程序从头打开 |
| 其他 | 用默认程序打开 | |
| 代码 | 在编辑器里跳到对应行 | 第二期，复用 DigUp 的编辑器名单思路 |

其余操作：在资源管理器中显示（`Ctrl+Shift+F`）、复制路径（`Ctrl+Shift+C`）、复制文件（`ActionCopyFiles`）、“在 DigUp 中打开”（启动 `DigUp.exe`）。

### 5.4 设置

设置 › 插件 › DigUp：启用开关、前缀、查询模型空闲释放时间（默认 60 秒）、显示索引状态（`status`/`summaries`：已索引、待处理、失败的文件数）。

## 6. 已知风险与对策

1. **启动第二个 `search-service` 会清空 DigUp 的缩略图缓存**：它启动时会删掉并重建 `<索引>\previews\`，而 DigUp 自己的搜索进程正在用这个文件夹。实测时为此改用索引副本。对策：
   - 向上游提一个 PR，加 `--previews DIR` 参数（或启动时不删别人在用的文件），golauncher 把缩略图放到自己的缓存目录；
   - 在上游合并之前，golauncher **只在 DigUp 没有运行时**启动自己的搜索进程；DigUp 在运行时，显示“DigUp 正在运行，按 Ctrl+Shift+Space 用它搜索”，回车切到 DigUp 的面板。这一条需要你决定（§8）。
2. **索引在变化**：DigUp 的 `index-service` 在写索引（SQLite WAL，多个读者没有问题）。golauncher 的搜索进程不知道索引变了：每次启动器显示时，若 `index.sqlite` 或 `-wal` 的修改时间变了，就发 `reload`。
3. **内存**：语义搜索时多一个约 265 MB 的进程；DigUp 自己的界面也在用时是两份。`--encoder-idle 60` 让它在一分钟不用后释放，关键词搜索仍然只占几 MB。
4. **协议不稳定**：DigUp 升级可能改动。对策：读 `DisplayVersion` 记日志；每个回复都检查字段，缺了就降级（比如只显示路径）；未知操作的错误不当作崩溃；集成测试用固定的假进程，真机测试单独开启（§9）。
5. **模型不在**：DigUp 还没下载完模型时，`encoder` 是 `unavailable`，只能关键词搜索；界面提示“DigUp 的模型还没准备好”。
6. **平台**：只在 Windows 上实测。Linux 的安装位置（`~/.local/share/digup`、AppImage/deb）和 Mac 版是否带 `digup` 命令行工具都没确认，第一期只做 Windows。

## 7. 版本兼容

- 第一期按 0.7.0-beta.3 的协议实现。
- 启动后用 `status` 的字段确认能用；`search` 回复里没有 `hits` 时，记日志并停用这次会话。
- 每次 golauncher 启动读一次 `DisplayVersion`，和上次不同时在日志里记下来，方便排查。

## 8. 需要你决定

1. 前缀用 `dig `，还是别的（如 `du `、`找 `）？
2. DigUp 正在运行时怎么办：**A.** 仍然启动 golauncher 自己的搜索进程（会清掉 DigUp 的缩略图缓存，DigUp 面板里的缩略图要重新生成）；**B.** 不启动，提示并切到 DigUp 的面板（建议，直到上游加了 `--previews`）。
3. 是否由我向 DigUp 上游提 `--previews DIR` 的 PR（公开仓库，需要你同意）？
4. 根搜索里要不要“用 DigUp 搜索”的后备条目？

## 9. 测试

- **假进程**：用测试二进制自身扮演 `digup search-service`（和插件测试、钩子进程测试一样的 `TestMain` 做法），回放固定的回复：`folders` 后才有结果、关键词与语义两阶段、`strong` 分组、错误回复、进程中途退出后重启一次再停用。
- **glctl golden**：`dig 海滩` 的结果列表、PDF 条目的“第 N 页”、打开动作的参数（`#page=N`、`--start-time=`）。
- **真机测试**（`GOLAUNCHER_DIGUP_TESTS=1`）：把 `%LOCALAPPDATA%\DigUp\index` **复制到临时目录**再启动搜索进程（不碰真实索引和缩略图缓存），跑 `status`、关键词搜索和语义搜索，只断言字段和耗时，不记录路径。

## 10. 分期

| 期 | 内容 |
|---|---|
| **第一期（Windows）** | 检测、启动和生命周期；`dig ` 前缀的两阶段搜索；类型筛选；结果、预览（`render`、`text`）；按页、按时刻打开；设置页和索引状态；§6 的对策 |
| 第二期 | 代码搜索在编辑器里跳到行；根搜索后备条目；Linux；macOS（先确认 Mac 版能否调用 `digup`） |
| 上游 | `--previews DIR`（§6.1）；如果作者愿意，再提一个稳定的协议版本号 |
