# 接入大模型设计方案

> 状态：设计 v1 · 第二期已实现（待合并，§15）· 2026-10-15 · 对应需求 R42、R43、R44、R45（见 [requirements.md](requirements.md)）
>
> v0 是功能清单（§1、§2 保留）；v1 补上了架构、工具层、MCP 服务器、会话、界面、安全、测试和分期。CLI 参数都按本机安装的 Claude Code 和 Codex CLI 的 `--help` 核对过，实现前还要用真实进程验证一遍（§10）。

## 1. 为什么 golauncher 适合接大模型

- **它本来就是"一句话 → 一个动作"的入口**：按 Alt+Space 输入一句话，正好就是向大模型提问的姿势。
- **插件系统就是现成的工具清单**：每个插件的命令和动作都有标题、参数，类型也明确（`ActionDo`）。破坏性的动作已经标了 `Destructive`，宿主会统一弹确认框。
- **`glctl` 已经证明了"程序驱动启动器"这条路**：query / run / session 这几个动词就是给 Agent 用的。AI 工具层（§4）是同一套动词，换成 MCP 协议，并加上确认和权限。
- **有环境感知**（R25）、**推送式渲染**（`Host.Render`）和**后台任务**（R24）：分别用来附带上下文、流式显示回答、跑耗时的多步任务。

## 2. 能做什么

| 编号 | 功能 | 例子 |
|------|------|------|
| F1 | **快速问答** | 输入问题按 `Tab`，或者在结果很差时选"Ask AI"，流式显示回答，可以追问；只在明确触发时调用模型，打字过程中从不调用 |
| F2 | **AI 命令** | 对选中的文本或剪贴板内容执行提示词模板：翻译、润色、总结、解释代码、表格转 Markdown……结果可以粘贴回原窗口；你可以自己写模板（变量 `{selection}`、`{clipboard}`、`{argument}`），每条都是普通命令，可以设别名和快捷键 |
| F3 | **用自然语言操作启动器** | "8080 端口被谁占了""用 winget 装 7zip""打开蓝牙设置""把 WSL 关了"：模型调用插件，破坏性操作照旧确认 |
| F4 | **结合环境感知** | 在 Explorer 里"总结这个文件夹的文档""把这些照片按日期重命名"（先给计划，确认后执行）；发送的内容先列出来 |
| F5 | **Claude 应用反过来使用启动器** | 在 Claude 桌面版或 Claude Code 里，通过 MCP 使用启动器的插件 |
| F6 | **交给 Claude 或 ChatGPT 应用继续** | 长对话一键带到对方应用（深链接，预填问题） |
| F7 | 意图搜索、剪贴板历史按意思搜、自然语言计算 | 以后按需要做 |

## 3. 总体架构

```
            ┌──────────────── golauncher.exe（常驻，GUI） ────────────────┐
            │                                                              │
 你 ──Alt+Space──► 搜索框 / AI 对话视图（插件 builtin.ai，F1 F2 F4 F6）     │
            │           │ 发消息                     ▲ 流式事件            │
            │           ▼                            │                     │
            │      后端 Backend（§6）：Claude Code 常驻进程 / Codex / API    │
            │           │ 工具调用（MCP，或 API 后端在进程内直接调用）      │
            │           ▼                                                  │
            │   AI 工具层 internal/agent（§4）── 权限策略 ── 确认框（§5）   │
            │           │                                                  │
            │           ▼                                                  │
            │   core.Engine / Controller ──► 各插件（应用、WinGet、网络……） │
            │           ▲                                                  │
            │   本机管道（只有当前用户能连，§4.4）                          │
            └───────────┼──────────────────────────────────────────────────┘
                        │
        golauncher.exe mcp（stdio 桥，§4.3） ◄── Claude 桌面版 / Claude Code（F5）
```

要点：

- **工具只在常驻的 golauncher 进程里执行**。这样插件状态是同一份，确认框也能弹在你面前。
- `golauncher mcp` 只是个很薄的桥：它把 MCP 请求转发给常驻进程，自己不加载插件。
- 启动器里的 AI 对话（F1–F4）也走同一套工具层。用 Claude Code 后端时，同样通过这个 MCP 桥；用 API 后端时，在进程内直接调用，省掉一层。

## 4. AI 工具层（`internal/agent`）

### 4.1 为什么用通用工具，不按插件生成

插件的动作挂在**动态的结果项**上，比如"WinGet 搜索结果里的 7-Zip"的"安装"，而不是固定参数的函数。所以工具层只提供几个**通用工具**，它们对应 `glctl` 的动词。模型先搜索或打开命令，拿到条目，再对条目执行动作。

| 工具 | 作用 | 对应 |
|------|------|------|
| `list_plugins` | 列出可用的插件和命令：标题、说明、前缀（如 `winget`、`time`、`port`）、参数 | `glctl plugin list` |
| `search` | 在根搜索里搜，可以限定插件；返回条目：句柄、标题、副标题、插件，以及动作列表（ID、标题、是否破坏性） | `glctl query` |
| `run_command` | 运行一个命令，可以带参数；视图命令返回视图里的条目，可以再带一个搜索词 | `glctl run` |
| `get_detail` | 取条目的详情（Markdown 和元数据） | 详情面板 |
| `run_action` | 对之前拿到的条目执行动作；表单动作可以带字段值 | Enter / 动作面板 |

- **条目句柄**：工具层给每个返回的条目一个短句柄（如 `i7`），记在会话的表里，10 分钟后失效。`run_action` 只接受这些句柄，模型不能凭空编出一个动作来执行。
- **返回内容**：都转成精简的 JSON，去掉图标、按键等界面信息，并限制条数（默认 20）和长度。
- **服务器说明**：MCP 的 `instructions` 字段里写清楚怎么用：先 `list_plugins` 或 `search`，再 `run_action`；破坏性动作会请用户确认；不要反复重试被拒绝的操作。
- **插件作者不用写任何代码**。以后可以给 SDK 加一个可选的 `ToolProvider`，让插件声明专门的工具（带 JSON Schema），作为增强（§11 第四期）。

### 4.2 动作的风险等级和默认策略

| 等级 | 包括 | 默认 |
|------|------|------|
| 只读 | `list_plugins`、`search`、`get_detail`、视图命令 | 直接执行 |
| 打开 | `Open`（应用、网址、设置页、文件夹）、`CopyText` 等 | 直接执行（可以改成"每次询问"） |
| 改动 | 插件回调动作（安装、结束进程、新建文件……）、表单提交、无界面命令 | **每次询问** |
| 破坏性 | 标了 `Destructive` 的动作和命令（卸载、关机、清空回收站……） | **每次询问，而且不能关掉** |

- 回调动作由插件自己处理，宿主看不出它会做什么，所以一律按"改动"处理，除非插件把它标成只读（SDK 新增 `Action.ReadOnly`，可选）。
- **哪些插件允许 AI 用**：设置里逐个插件开关。剪贴板历史**默认关闭**，即使打开，标了敏感的条目（R37 的 `WriteSecret`、来自密码管理器的内容）也永远不返回。其他内置插件默认打开。第三方插件默认关闭，你在设置里打开。

### 4.3 MCP 服务器（`golauncher mcp`）

- **传输**：stdio（JSON-RPC 2.0，每行一条），这是 Claude 桌面版和 Claude Code 都支持的本地方式。
- **怎么启动**：就是 `golauncher.exe mcp`，同一个程序的子命令，不另装东西。它连到常驻进程的管道（§4.4）：
  - 常驻进程没在运行时，先在后台启动它（不显示窗口），等它就绪；
  - 没有安装或启动失败时，`tools/call` 返回清楚的错误。
- **客户端标识**：记下 MCP `initialize` 里的 `clientInfo`（如 Claude 桌面版、Claude Code），确认框和活动记录里会写"Claude 想要……"。
- **配置助手**：设置 › AI 里有"连接到 Claude 桌面版"按钮。它会：
  1. 先备份 `%APPDATA%\Claude\claude_desktop_config.json`；
  2. 只添加或更新 `mcpServers.golauncher` 这一项；
  3. 显示改了什么，提示你重启 Claude 应用。

  给 Claude Code 的做法是显示一条 `claude mcp add golauncher -- "<路径>\golauncher.exe" mcp` 命令供复制，不替你执行。

### 4.4 本机管道和安全

- **管道**：命名管道 `\\.\pipe\golauncher-<用户 SID>-agent`，ACL 只允许当前用户访问。
- **默认关闭**：设置 › AI ›"允许 AI 应用使用 golauncher"默认**关闭**，打开后管道才监听。
- **不能绕过确认**：同一用户的任何程序都能连这个管道，所以权限只靠 §4.2 的策略和确认框，不靠"信任连接方"。确认框始终由常驻进程弹出，桥和模型都没法跳过它。
- **活动记录**：每次工具调用都记一条：时间、客户端、工具、参数摘要、结果（成功、拒绝、失败）。设置里可以查看和清空，保留 30 天。

## 5. 确认框

- **在启动器窗口里弹出**：启动器隐藏时会先显示出来。确认框写明谁在请求（"Claude（Claude 桌面版）想要："）、要做的事（动作标题和条目），以及插件给出的说明。关机类动作照旧附带"N 个后台任务会被中断"（R31）。
- **选项**：
  - "允许"；
  - "拒绝"；
  - 对非破坏性的动作，还有"本次会话内都允许这个动作"。
- **超时**：60 秒没有回应算拒绝。拒绝时给模型的结果是"用户拒绝了这个操作"，让它别再试。
- **并发**：同时只显示一个确认框，其他请求排队。
- 这是 core 层的新能力：外部请求的确认。现在的确认流程只服务键盘操作，要在 Controller 里加一个"外部确认"状态，由工具层调用。

## 6. 后端（模型从哪来）

```go
// internal/ai
type Backend interface {
    Name() string
    Available(ctx) error                // 没安装、没登录、版本不对时给出原因
    Open(ctx, Session) (Conversation, error)
}
type Conversation interface {
    Send(ctx, Message) error            // 用户消息（可附带上下文）
    Events() <-chan Event               // 文本片段、工具调用开始和结果、确认请求、结束（含用量）、错误
    Cancel()                            // 停止当前回答
    Close() error
}
```

### 6.1 Claude Code 常驻进程（第一个实现，用你的订阅）

- **命令行**（每个参数的用途）：

  ```
  claude -p --input-format stream-json --output-format stream-json --include-partial-messages
         --session-id <uuid> | --resume <uuid>
         --setting-sources "" --strict-mcp-config --mcp-config <生成的 JSON>
         --tools "" --permission-prompts host
         --append-system-prompt <启动器的说明：语言、身份、怎么用工具>
         [--model <设置里选的模型>]
  ```

  - `--tools ""`：不给 Claude Code 自带的任何工具（读写文件、Bash……），它只能通过 golauncher 的 MCP 工具做事；
  - `--strict-mcp-config` 加 `--mcp-config`：只连 golauncher，不加载你平时给 Claude Code 配的其他 MCP 服务器；
  - `--setting-sources ""`：不加载你的用户、项目设置（包括钩子）。要实测确认钩子和插件确实都不加载；
  - **不能用 `--bare`**：它不读订阅的 OAuth 登录，只认 API Key；
  - `--permission-prompts host`：需要授权时交给宿主（golauncher）回答，接到 §5 的确认框。
- **工作目录**：固定为 `%LOCALAPPDATA%\golauncher\ai\`，和你写代码的会话分开。只用显式的 `--resume <id>`，不用 `--continue`。
- **生命周期**：
  - 打开对话时启动进程，进程放进 Job Object；
  - 窗口隐藏 10 分钟后关掉进程，同一时间最多保留 2 个；
  - 再发消息时用 `--resume` 重启，对话不丢。
- **兼容性**：stream-json 没有正式的版本承诺。
  - 启动时读 `claude --version`，只接受验证过的版本范围；超出范围就提示，仍然可以用，出错时退回"每轮一个进程"（`-p --resume`，§6.2）；
  - 用真实 CLI 录下的事件流做 fixture（内容是虚构的问答），测解析。
- **出错提示**：
  - 没安装：说明如何安装；
  - 没登录：提示在终端运行 `claude` 登录；
  - 额度用完：照原文显示 CLI 的提示。

### 6.2 其他后端（后期）

| 后端 | 方式 | 工具 |
|------|------|------|
| Claude Code 单轮模式 | 每轮 `claude -p --resume <id>`，§6.1 的退路 | 同 §6.1 |
| Codex CLI | 每轮 `codex exec --json`，续聊用 `codex exec resume <id>`，`--sandbox read-only` | 用 `-c` 覆盖配置，挂上 golauncher 的 MCP 服务器（要验证） |
| Anthropic API | Messages API，流式 | 工具层在进程内转成 tool use，不经过 MCP |
| OpenAI 兼容 API | OpenAI、DeepSeek、通义千问、Kimi、智谱、硅基流动等 | 同上（function calling） |
| 本地模型 | Ollama / LM Studio（OpenAI 兼容接口） | 同上；可以作为"只用本地模型"选项 |

API Key 存进 Windows 凭据管理器（`password` 偏好），不写进 `config.json`。

### 6.3 交给桌面应用（F6）

- **"在 Claude 应用中继续"**：用 `claude://` 协议或 `https://claude.ai/new?q=…` 预填问题，没装应用就开浏览器。
- **"在 ChatGPT 中继续"**：用 `https://chatgpt.com/?q=…`。
- 桌面应用是否支持预填参数要实测，不支持时退回网页链接。
- 只带上当前问题（或选中文本），不带整个对话；长度超过 URL 限制时先复制到剪贴板，并提示你粘贴。
- 不做界面自动化（激活对方窗口、模拟粘贴和回车）：对方应用一更新就可能失效，回答也读不回来（R43 的比较）。

## 7. 会话

- **会话记录**：每个对话有一个 ID。对 Claude Code 后端来说，这就是 `--session-id`。
- **存储**：插件存储里记会话列表（ID、后端、标题取第一句、创建和最后使用时间），以及**我们自己的对话记录**（消息、工具步骤的摘要），用来重新显示。CLI 的会话文件是内部格式，不去解析它，只用 `--resume` 恢复模型那边的上下文。
- **"AI 对话历史"**：一个命令，列出最近的对话，可以打开接着聊、删除、清空。保留期在设置里选，默认 30 天。
- **新对话**：Tab 进来默认开新对话；在对话视图里按 `Ctrl+N` 新建。

## 8. 界面

- **入口**：
  - 搜索框有文字时按 `Tab`，把文字作为第一条消息打开 AI 对话。`Tab` 原来用于命令参数（M6 计划），两者不冲突：有参数的命令选中时 `Tab` 进参数，其他时候进 AI；
  - 结果很差（没有结果或最高分低于阈值）时，在最后显示"Ask AI：<文字>"，可以在设置里关；
  - 命令："AI Chat"（新对话）、"AI Chat History"、各条 AI 命令（F2）。
- **对话视图**：一个 Detail 视图，Markdown 显示消息，搜索框就是输入框，Enter 发送。
  - 回答用 `Host.Render` 流式推送；
  - 工具步骤显示成一行，例如 `🔧 查询端口 8080 → node.exe (PID 4242)`；
  - 需要确认时显示确认框（§5）。
- **动作**：复制回答、粘贴到前一个窗口、在 Claude 或 ChatGPT 中继续（F6）、停止（Esc 先停止回答，再按一次关闭视图）、新对话、复制会话 ID。
- **状态**：页脚显示后端、模型、本轮用量（如果后端提供）。
- **热键冲突**：注册 Alt+Space 失败时（例如 ChatGPT 桌面版占用），用通知提示，并打开设置的热键项。这个对所有用户都有用，不限于 AI。

## 9. 隐私

- 默认不发送任何东西，AI 功能整体默认关闭，在设置 › AI 里打开并选后端。
- 附带的上下文（选中文本、剪贴板、Explorer 的文件夹和文件）在发送前列在输入框上方，可以逐个去掉。文件内容只在你明确附带时读取，单个文件有大小上限。
- 剪贴板历史默认不对 AI 开放；敏感条目永远不发（§4.2）。
- 对话记录只存在本机，可以清空；活动记录同样（§4.4）。
- 日志里不写消息内容，只写长度、耗时和错误。

## 10. 测试

- **工具层**：用 glctl 的假环境（fixture 插件数据）测每个工具的输入输出、句柄失效、风险等级、策略、确认（自动允许或拒绝的假确认器）和活动记录。
- **MCP 服务器**：测试里起一个 MCP 客户端，走 stdio，测 `initialize`、`tools/list`、`tools/call` 和错误；`glctl mcp` 在假环境里出 golden。
- **管道**：ACL 只允许当前用户；常驻进程没在运行时的启动和等待。
- **Claude Code 后端**：用一个测试用的假 `claude` 程序（Go 写的 helper），按脚本输出 stream-json 事件：文本片段、工具调用、授权请求、错误、进程崩溃。测重启、`--resume`、取消、闲置关闭、版本检查、退回单轮模式。
- **真实 CLI**：只在设置 `GOLAUNCHER_AI_TESTS=1` 时运行，会消耗订阅额度，默认不跑。实现前先手工验证一遍：
  1. stream-json 双向对话；
  2. `--permission-prompts host` 的授权消息格式；
  3. `--setting-sources ""` 确实不加载钩子和插件；
  4. `--tools ""` 下 MCP 工具可用；
  5. `claude://` 预填问题。

  验证结果只记结论，不记本机的配置。
- **界面**：`ui.NewTester` 测对话视图的流式更新、确认框、Esc 的两段行为。

## 11. 分期

| 阶段 | 内容 | 完成后你能做什么 |
|------|------|------------------|
| **一：工具层 + MCP** | `internal/agent`（通用工具、句柄、风险等级、策略、活动记录）、外部确认框、命名管道、`golauncher mcp`、设置 › AI（总开关、插件开关、策略）、Claude 桌面版配置助手、热键冲突提示 | 在 Claude 桌面版或 Claude Code 里使用启动器的插件（F5） |
| **二：启动器里的 AI 对话** | `builtin.ai` 插件、Claude Code 常驻后端（含单轮退路）、对话视图、会话和历史、Tab 和"Ask AI"入口、交给 Claude 或 ChatGPT 应用继续 | 在启动器里问答，并用自然语言操作插件（F1、F3、F6） |
| **三：AI 命令和更多后端** | 提示词模板、读取选中文本、环境感知的上下文附带、Codex 后端、API 后端（Anthropic、OpenAI 兼容、Ollama） | F2、F4，不用 Claude 订阅也能用 |
| **四：增强** | SDK 的 `ToolProvider` 和 `Action.ReadOnly`、意图搜索、剪贴板和计算器增强、MCP 客户端 | F7 |

第一期不依赖任何模型，最容易测试，而且第二期直接复用它，所以放在最前面。

## 12. 风险

- **stream-json 格式变动**：锁定验证过的版本范围，退回单轮模式，用 fixture 测试（§6.1）。
- **CLI 读到你的个人配置**：用 `--setting-sources ""`、`--strict-mcp-config`、`--tools ""` 隔离，并实测确认（§10）。
- **模型误操作**：靠风险等级、确认框和不能编造的句柄防止，并有活动记录可查。
- **订阅额度**：用量计入你的订阅；页脚显示用量，界面不在后台偷偷调用。
- **服务条款**：只通过官方 CLI、用你自己的登录，供你本人使用；不提取或转存登录凭据。

## 13. 需要你决定

1. 分期顺序：先做第一期（MCP），还是先做第二期（启动器里的对话）？建议先做第一期。
2. 默认策略：§4.2 的表格可以吗？"打开"类默认直接执行，剪贴板历史默认不开放。
3. 入口：`Tab` 和"Ask AI"都要，还是只要其中一个？
4. 第二期之后，还需要 Codex 或 API 后端吗？要的话，优先哪家？
5. 对话记录的保留期：默认 30 天可以吗？

## 14. 决定（2026-10-15，R45）

1. **先做第二期**：先在启动器里调用 Claude。
2. **第二期不接启动器的工具**：§4（工具层、风险等级、插件开关、活动记录）、§4.3/§4.4（MCP 和管道）、§5 的外部确认，都和 MCP 一起放到以后。第二期里 Claude 只回答问题，不操作插件：
   - 仍然用 `--tools ""`，不挂 MCP；
   - 是否允许它联网搜索（WebSearch、WebFetch，不涉及本机）做成一个偏好项。

   F3（用自然语言操作启动器）随 MCP 一起做。
3. §4.2 的风险等级和默认策略**同意**，到 MCP 那期按表实现。
4. **入口**：`Tab` 和"Ask AI"都要。
5. **第二期之后先暂停**：Codex、API 后端、AI 命令等以后需要时再加。
6. **对话记录保留 30 天**。

### 14.1 第二期的范围（修订）

| 包括 | 不包括（以后） |
|------|----------------|
| `builtin.ai` 插件；设置 › AI（总开关、模型、是否允许联网搜索、保留期） | 工具层、MCP、外部确认（§4、§5） |
| Claude Code 常驻后端（stream-json），出问题时退回单轮模式（§6.1） | Codex、API 后端（§6.2） |
| 对话视图：流式显示、追问、停止、复制、粘贴到前一个窗口 | AI 命令、读取选中文本（F2） |
| 会话和"AI 对话历史"，保留 30 天（§7） | 环境感知的上下文附带（F4） |
| `Tab` 进入 AI、结果很差时显示"Ask AI"（§8） | |
| 在 Claude 应用或 ChatGPT 中继续（§6.3） | |
| 热键注册失败的提示（§8） | |
| 实现前用真实 CLI 验证 §10 里的 1、3、5 三项（2、4 和工具有关，留给 MCP 那期） | |

## 15. 实现记录（第二期，2026-10-15，R45）

### 15.1 实测验证（§10 的 1、3、5）

用本机的 Claude Code 2.1.296 跑了几次极短的提问（"Reply with OK" 这一类），只记结论；下面的事件都是虚构的示例值。

1. **stream-json 双向对话**：一个进程里可以连续多轮。
   - 输入每行一个 JSON：`{"type":"user","message":{"role":"user","content":"…"}}`。
   - `-p` 加 `--output-format stream-json` **必须同时加 `--verbose`**，否则 CLI 直接报错退出。
   - 每一轮都会先输出一行 `{"type":"system","subtype":"init","session_id":"…","model":"claude-sonnet-5-5","tools":[],"mcp_servers":[],"claude_code_version":"2.1.296",…}`（不是只在启动时输出一次），然后是 `system/status`、`rate_limit_event`。
   - 增量文本：`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}},…}`；只有 `text_delta` 是回答，其他 delta（思考等）要忽略；工具调用是 `content_block_start` 里 `content_block.type=="tool_use"`。
   - 完整消息 `{"type":"assistant","message":{"id":"msg_…","content":[{"type":"text","text":"Hello"}],…}}` 和增量重复，`message.id` 与 `message_start` 的相同，按它去重。
   - 一轮的结尾：`{"type":"result","subtype":"success","is_error":false,"result":"Hello","session_id":"…","usage":{"input_tokens":…,"cache_creation_input_tokens":…,"cache_read_input_tokens":…,"output_tokens":…},"modelUsage":{…},…}`。
   - **停止回答**：写入 `{"type":"control_request","request_id":"r1","request":{"subtype":"interrupt"}}`，CLI 回 `control_response`，这一轮以 `subtype:"error_during_execution"`、`is_error:true`、`terminal_reason:"aborted_streaming"` 的 result 结束；进程不退出，可以接着问。
   - **跨进程 `--resume <id>`**：新进程能接上之前的上下文。`--resume` 一个不存在的会话：result 里 `errors:["No conversation found with session ID: …"]`，进程退出码 1。
   - 错误的形状：模型不存在时有一条合成的 assistant 消息（`"error":"model_not_found"`，model 为 `<synthetic>`），result 为 `is_error:true` 加原文。没登录、额度用完无法在本机复现，按同样的形状（`authentication_failed`、`rate_limit`/`billing_error` 和原文里的关键字）识别。
   - 单轮模式（退路）：同样的参数去掉 `--input-format`，问题从标准输入写入后关闭，输出同样的事件流，结束后进程退出。
   - 以 `/` 开头的消息会被当作斜杠命令（回答"/x isn't available in this environment"）；**在前面加一个空格**就按普通问题处理。
2. **隔离（§10 第 3 项）**：只加 `--setting-sources ""` **不够**。
   - 它确实不加载用户、项目设置（包括设置里的钩子、`env`、启用的插件），也不读项目里的 CLAUDE.md（在工作目录放了一个带"暗号"的 CLAUDE.md，回答里没有出现；调试日志显示"project memory is off"）。
   - 但 `init` 里仍有用户目录下的技能（skills）和斜杠命令，**自动记忆（auto memory）也是开着的**（给出了记忆目录）。
   - 加上 **`--safe-mode`** 后：已安装的插件和它们的钩子不加载、不扫描技能目录、自动记忆关闭、claude.ai 的连接器（MCP）关闭；再加 **`--disable-slash-commands`**，技能和斜杠命令都是 0 个。
   - **OAuth 订阅登录在这些参数下照常可用**（`apiKeySource:"none"`，正常回答）。`--bare` 不能用（它只认 API Key），这一点不变。
   - 本机没有用户级的 `~/.claude/CLAUDE.md`，没法直接验证它；`--safe-mode` 的说明里写明会关闭 CLAUDE.md，`--setting-sources ""` 也不含 user 来源，所以两者一起使用。
   - `--tools WebSearch,WebFetch` 加 `--allowedTools WebSearch,WebFetch` 和 `--permission-prompts none`：联网工具直接可用，不弹任何授权；其他需要授权的操作一律自动拒绝。
3. **`claude://` 预填（§10 第 5 项）**：**没有在屏幕上打开**，只检查了注册和应用包。
   - 协议由 Claude 桌面版（MSIX 包）注册，`HKCR\claude` 带 `URL Protocol`，命令行是 `Claude.exe "%1"`。
   - 应用里有 `claude://claude.ai/new?q=…` 的处理：把窗口导航到 `https://claude.ai/new?q=…`（q 截到 14336 个字符）；只接受白名单里的参数；**q 以 `/` 开头的链接会被拒绝**。
   - 结论：装了桌面版时用 `claude://claude.ai/new?q=…`，否则（以及问题以 `/` 开头时）用 `https://claude.ai/new?q=…`。是否真的预填进输入框、是否自动发送，**未经目视确认**。ChatGPT 用 `https://chatgpt.com/?q=…`，同样未目视确认。

### 15.2 实际的命令行

```
claude -p --input-format stream-json --output-format stream-json --include-partial-messages --verbose
       --session-id <uuid> | --resume <uuid>
       --setting-sources "" --safe-mode --strict-mcp-config --disable-slash-commands
       --tools ""   （或 --tools WebSearch,WebFetch --allowedTools WebSearch,WebFetch）
       --permission-prompts none
       --append-system-prompt <启动器的说明>  [--model <模型>]
```

工作目录 `%LOCALAPPDATA%\golauncher\ai\`；去掉环境变量 `CLAUDECODE`、`CLAUDE_CODE_ENTRYPOINT`、`CLAUDE_CODE_SSE_PORT`（从 Claude Code 里启动 golauncher 时会带上）。和 §6.1 相比：多了 `--verbose`、`--safe-mode`、`--disable-slash-commands`、`--permission-prompts none`，没有 `--mcp-config`（第二期不挂 MCP）。

### 15.3 结构

| 部分 | 位置 | 说明 |
|------|------|------|
| 后端契约 | `plugins/ai/backend` | `Backend`、`Conversation`（`Send` 返回这一轮的事件通道、`Cancel`、`Release`、`Close`）、`Event`、带种类的 `Error`；`Fake` 供测试和 glctl 使用 |
| Claude Code 后端 | `internal/ai/claudecode` | 进程、stream-json 解析、版本检查、退路、进程数上限；插件不能 import 内部包，由 `internal/builtin` 注入 |
| 进程 | `internal/winapi/proc` | 新增 `StdinPipe` 和单独的 `Stderr`，仍然放进 Job Object、不弹控制台窗口 |
| 插件 | `plugins/ai` | 命令、对话视图、会话存储、历史、交给应用继续 |
| 宿主的通用机制 | `pkg/plugin`、`internal/core`、`internal/ui` | 见 15.4 |
| 协议检测 | `internal/winapi/shell.ProtocolRegistered` | 判断有没有装 Claude 桌面版 |

- **生命周期**：第一次发送时才启动进程；窗口隐藏 10 分钟后 `Release`（关掉进程，下次发送用 `--resume` 重启）；同时最多 2 个常驻进程，超出时释放最久没用的空闲对话，正在回答的不动。
- **版本**：低于 2.1.0 报"版本太旧"；主版本不是 2 时照常可用，但标为"未验证的版本"，直接用单轮模式。
- **退路**：常驻进程第一轮就退出、或输出不是 JSON 时，这个对话改用单轮模式并重试这一轮；`--resume` 找不到会话时用同一个 ID 新开；`--session-id` 已被占用时改用 `--resume`。
- **出错提示**：没安装（说明安装和登录）、没登录（提示在终端运行 `claude` 登录）、额度用完（附 CLI 原文）、其他（附 CLI 原文和 stderr 末尾）。
- **会话**：插件存储里有会话列表（ID、标题取第一句、创建和最后使用时间、消息数）和每个对话的消息；不解析 CLI 的会话文件。打开插件、打开历史时删除超过保留期（默认 30 天，可选 7/30/90/365 天）的对话。删除对话只删我们的记录，CLI 自己的会话文件不动（它的格式是内部的）。
- **系统提示**（`--append-system-prompt`）：说明身份（golauncher 里的助手）、用界面语言回答、简洁、用 Markdown、不能访问本机；允许联网时加一句可以搜索网页。这是给模型看的，不翻译。
- **日志**：只记长度、耗时和错误种类，不记消息内容（§9）。

### 15.4 通用机制（不是 AI 专用）

- **`plugin.FallbackProvider`**：根搜索没有结果、或最好的匹配分（不含使用频率和加成）低于 `core.FallbackThreshold`（0.5）时，宿主才调用它，把条目放在最后的"备选命令"分组；这些条目不记入"最近使用"。AI 插件用它提供"问 AI：<文字>"，偏好项可以关掉。
- **`plugin.TabProvider`**：根搜索里有文字时按 `Tab`，如果选中的条目有参数，`Tab` 照旧进入参数（M5 的命令参数）；否则运行第一个启用插件给出的 Tab 动作。AI 插件始终给出"打开新对话并发送这段文字"；AI 没打开时先显示打开提示，打开后自动发送。
- **全窗口的 `ViewDetail`**：Markdown 占满整个窗口，内容增长时跟随到底（用户往上滚时不跟随）；视图的第一个条目不显示，只提供动作；搜索框就是输入框，回调的 `ActionDo.Query` 带着输入的文字，`Result.ClearText` 清空它。这种视图里 `Up`/`Down`、`Ctrl+N`/`Ctrl+P` 不移动选中项，所以 `Ctrl+N` 可以给"新对话"用。
- **`View.Escape`**：视图可以接管 `Esc`。AI 对话只在回答进行中设置"停止"，所以第一次 `Esc` 停止回答，第二次返回。
- **`Result.Preferences`**：插件设置自己的偏好项（不能设置密码类），首次使用提示里的"打开 AI"按钮就是这样把总开关打开的。
- 这些机制暂时只对进程内插件可用，外部插件协议还没有对应的消息。

### 15.5 界面

- 对话视图：显示"你"和"Claude"的消息，回答流式显示（末尾有光标），工具步骤显示成一行（"搜索了网页"）；底部一行显示后端、模型和上一次回答的用量。
- 动作：发送（Enter）、停止（回答中；Esc）、复制回答（`Ctrl+Shift+C`）、粘贴回答到前一个窗口（`Ctrl+Shift+V`）、在 Claude 中继续、在 ChatGPT 中继续、新对话（`Ctrl+N`）、AI 对话历史、复制会话 ID。
- 交给应用继续：只带当前问题（输入框里的文字，没有就用最后一个问题）；链接超过 8000 个字符（Claude 桌面版超过 14000 个字符）时，把问题复制到剪贴板、打开空白对话，并用通知提示粘贴。
- 设置 › 插件 › AI：使用 AI（总开关，默认关）、模型（默认、Sonnet、Opus、Haiku）、允许联网搜索（默认关）、对话保留时间（默认 30 天）、没有匹配结果时显示"问 AI"（默认开）。
- 热键冲突：注册失败时的通知和设置页提示原来就有（会打开设置的热键项），这次在文字里点明 ChatGPT 应用和 PowerToys Run 也用 Alt+Space。

### 15.6 测试

- `internal/ai/claudecode`：测试时用 Go 编译一个假的 `claude`（`testdata/fakeclaude`），按脚本输出 stream-json。覆盖多轮同进程、精确的命令行参数（并确认没有 `--bare`、`--continue` 和任何绕过授权的参数）、Release 后 `--resume`、常驻和单轮模式的停止、闲置释放、回答中崩溃后重启、最多 2 个进程、版本检查、没安装、错误种类、乱码输出和启动即崩溃时退回单轮模式、会话丢失和会话被占用的重试、开头的 `/`。真实 CLI 的测试只在 `GOLAUNCHER_AI_TESTS=1` 时运行（实现完成后跑过一次：三轮对话加跨进程恢复，通过）。
- `plugins/ai`：会话存储和保留期清理、恢复会话、历史的删除和清空、首次使用提示和打开、出错说明、窗口隐藏后释放、交给应用的链接和剪贴板退路。
- `internal/core`：备选条目（阈值、分组、不记入最近使用）、Tab（无文字不动作、有参数的条目照旧进参数、否则运行 Tab 动作）、全窗口详情视图的输入和清空、`View.Escape` 两段行为、`Result.Preferences`。
- `internal/ui`（`ui.NewTester`）：流式更新、`Esc` 先停止再关闭、Tab 进入、"问 AI"条目、AI 关闭时的提示、追问和复制回答。
- `glctl` 金样：`session_ai_off`、`session_ai_chat`、`session_ai_fallback`、`session_ai_history`、`zh_session_ai_off`（假后端，回答"You asked: …"，会话 ID 按顺序编号，结果可复现）。

### 15.7 和设计不同的地方、遗留问题

- 隔离参数比 §6.1 多（见 15.1、15.2）；第二期不挂 MCP，也就没有 `--mcp-config` 和 `--permission-prompts host`。
- "设置 › AI"就是插件页（设置 › 插件 › AI），由偏好项自动生成，没有单独的页面。
- 没有做"Claude Code 路径"偏好项（后端支持 `Options.Path`，目前自动查找 PATH、`%USERPROFILE%\.local\bin\claude.exe`、`%APPDATA%\npm\claude.cmd`）。
- 用 npm 装的 `claude.cmd` 要经过 cmd.exe，系统提示里的换行会换成空格，`%VAR%` 这样的文字可能被展开；原生安装的 `claude.exe` 不受影响。
- 没登录和额度用完的错误格式没有在本机复现，是按关键字识别的；"会话已被占用"的原文也是宽松匹配。
- `claude://` 和 ChatGPT 链接的预填效果没有目视确认（15.1 第 3 项）。
- 删除对话不会删除 CLI 自己保存的会话文件。