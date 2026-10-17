# 接入大模型设计方案

> 状态：设计 v1 · 第二期已实现（§15）· MCP 期已实现（待合并，§16）· 2026-10-15 · 对应需求 R42、R43、R44、R45、R47（见 [requirements.md](requirements.md)）
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

- **`plugin.FallbackProvider`**：根搜索没有结果、或最好的匹配分（不含使用频率和加成）低于 `core.FallbackThreshold`（0.5）时，宿主才调用它，把条目放在最后的"备选命令"分组；这些条目不记入"最近使用"。AI 插件用它提供"问 AI：<文字>"，偏好项可以关掉。几个插件都有备选条目时的顺序见 §17。
- **`plugin.TabProvider`**：根搜索里有文字时按 `Tab`，如果选中的条目有参数，`Tab` 照旧进入参数（M5 的命令参数）；否则运行第一个启用插件给出的 Tab 动作。AI 插件始终给出"打开新对话并发送这段文字"；AI 没打开时先显示打开提示，打开后自动发送。
- **全窗口的 `ViewDetail`**：Markdown 占满整个窗口，内容增长时跟随到底（用户往上滚时不跟随）；视图的第一个条目不显示，只提供动作；搜索框就是输入框，回调的 `ActionDo.Query` 带着输入的文字，`Result.ClearText` 清空它。这种视图里 `Up`/`Down`、`Ctrl+N`/`Ctrl+P` 不移动选中项，所以 `Ctrl+N` 可以给"新对话"用。
- **`View.Escape`**：视图可以接管 `Esc`。AI 对话只在回答进行中设置"停止"，所以第一次 `Esc` 停止回答，第二次返回。
- **`Result.Preferences`**：插件设置自己的偏好项（不能设置密码类），首次使用提示里的"打开 AI"按钮就是这样把总开关打开的。
- 这些机制暂时只对进程内插件可用，外部插件协议还没有对应的消息。

### 15.5 界面

- 对话视图：显示"你"和"Claude"的消息，回答流式显示（末尾有光标），工具步骤显示成一行（"搜索了网页"）；底部一行显示后端、模型和上一次回答的用量。
- 动作：发送（Enter）、停止（回答中；Esc）、复制回答（`Ctrl+Shift+C`）、粘贴回答到前一个窗口（`Ctrl+Shift+V`）、在 Claude 中继续、在 ChatGPT 中继续、新对话（`Ctrl+N`）、AI 对话历史、复制会话 ID。
- 交给应用继续：只带当前问题（输入框里的文字，没有就用最后一个问题）；链接超过 8000 个字符（Claude 桌面版超过 14000 个字符）时，把问题复制到剪贴板、打开空白对话，并用通知提示粘贴。
- 设置 › 插件 › AI：使用 AI（总开关，默认关）、模型（默认、Sonnet、Opus、Haiku）、允许联网搜索（默认关；R56 起默认开，见 §17）、对话保留时间（默认 30 天）、没有匹配结果时显示"问 AI"（默认开）。
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

## 16. 实现记录（MCP 期：把插件开放给 AI，2026-10-15，R47）

R47：AI 对话已经可用，接着把启动器的插件开放给 AI。这一期实现了 §4（工具层、风险等级、插件开关、活动记录）、§4.3/§4.4（`golauncher mcp` 和命名管道）、§5（外部确认框），并让启动器里的 AI 对话用上这些工具。

### 16.1 实测验证（§10 第 4 项，以及 MCP 相关的参数）

用本机的 Claude Code 2.1.296 跑了几次极短的提问，只用只读工具，MCP 服务器是 `glctl mcp`（假环境，所有动作只记录，确认一律拒绝），没有在本机做任何改动。只记结论：

1. **`--safe-mode` 会把 `--mcp-config` 的服务器也关掉**：`init` 里 `tools` 和 `mcp_servers` 都是空的，模型回答"没有这个工具"。去掉 `--safe-mode` 后，`--tools ""` 加 `--strict-mcp-config` 加 `--mcp-config` 正常：`tools` 正好是 5 个 `mcp__golauncher__*`，`mcp_servers` 里 golauncher 为 `connected`。所以 `--tools ""` 只关 Claude Code 自带的工具，不影响 MCP 工具。
2. **去掉 `--safe-mode` 之后怎么保持隔离**（最小的替代）：
   - `--setting-sources ""`：不读用户、项目设置，所以你自己装的插件不会启用（调试日志：只有 3 个内置插件，"Registered 0 hooks"），设置里的钩子也不加载；
   - `--strict-mcp-config`：只有 golauncher 这一个 MCP 服务器（claude.ai 的连接器也不在 `mcp_servers` 里）；
   - `--disable-slash-commands`：技能和斜杠命令仍是 0 个；
   - `--tools ""`：没有读写文件、终端等工具；
   - 新增环境变量 **`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`**：不加它时 `init` 里有 `memory_paths`（自动记忆开着），加上后没有了；
   - 工作目录里放一个带"暗号"的 CLAUDE.md，回答里没有出现（项目的 CLAUDE.md 不读）。
   - 和 `--safe-mode` 相比的差别：多了一个内置的代理定义（只能经 Task 工具使用，而 Task 已被 `--tools ""` 关掉）；用户级 `~/.claude/CLAUDE.md` 本机没有，没法直接验证（见 16.7）。
   - 所以：**开着"允许 AI 使用启动器插件"时不加 `--safe-mode`，改加上面的环境变量；关着时照旧加 `--safe-mode`。**
3. **预先批准**：`--allowedTools mcp__golauncher__*`（通配符）有效，配合 `--permission-prompts none`，调用工具时没有任何授权请求，`permission_denials` 为空。确认由启动器自己的工具层负责。
4. **事件格式**（下面的值是虚构的示例）：
   - 开始：`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_…","name":"mcp__golauncher__search","input":{},"caller":{"type":"direct"}}}}`，接着若干 `input_json_delta`（`partial_json` 拼起来是参数）；
   - 完整的工具调用：`{"type":"assistant","message":{"id":"msg_…","content":[{"type":"tool_use","id":"toolu_…","name":"mcp__golauncher__search","input":{"query":"…"}}]}}`（`message.id` 和流式的相同）；
   - 结果：`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_…","content":[{"type":"text","text":"<工具返回的 JSON>"}],"is_error":true}]}, "tool_use_result":…}`；成功时没有 `is_error` 字段。
5. **客户端标识**：Claude Code 的 `clientInfo.name` 是 `claude-code`（活动记录里显示为"Claude Code"）。Claude 桌面版按 `claude-ai` 识别（没有在本机实测）。
6. **拒绝**：工具层返回 `isError: true` 和"用户拒绝了"的说明后，模型如实告诉用户没有执行，没有重试。
7. 完整链路的真实测试 `TestRealCLITools`（`GOLAUNCHER_AI_TESTS=1`）：`claudecode` 后端加 `glctl mcp` 工具服务器，模型调用 `search`，收到工具步骤和结果，通过。

### 16.2 结构

| 部分 | 位置 | 说明 |
|------|------|------|
| 工具层 | `internal/agent` | 5 个工具、会话和句柄表、风险等级和策略、插件开关、活动记录、MCP 服务器、管道服务、`golauncher mcp` 桥、"AI 活动记录"命令（内部插件 `builtin.agent`） |
| 命名管道 | `internal/winapi/pipe` | `\\.\pipe\golauncher-<SID>-agent`；DACL 受保护，只有当前用户一条 ACE，所有者是当前用户；拒绝远程客户端；第一个实例用 `FILE_FLAG_FIRST_PIPE_INSTANCE`；重叠 I/O，读写可以同时进行 |
| 外部确认 | `internal/core/external.go` | `Controller.ConfirmExternal`，队列、超时、按键；`Snapshot.Confirm` 带 `External`、`AllowSession`、`Danger` |
| 引擎接口 | `internal/core/agent.go` | `Engine.Search`（不带本次显示的上下文、最近使用和备选项）、`CommandEntry`、`OnInvalidate`、`CloseViews`、`FormValues`、`ArgumentValues`、`MatchEntry` |
| 配置 | `config.Config.Agent` | `external`、`askOpen`、`plugins`（按插件覆盖默认值） |
| Claude 桌面版配置 | `internal/agent/desktop` | 找配置文件（含 MSIX 版的虚拟化目录）、备份、只改 `mcpServers.golauncher`、原子写入；其他内容逐字节保留 |
| AI 对话 | `plugins/ai`、`internal/ai/claudecode` | 偏好"允许 AI 使用启动器插件"、工具步骤、系统提示、`--mcp-config` |
| 界面 | `internal/ui` | 确认框（三个按钮）、设置 › 插件 › AI 下的"AI 工具"一节 |
| 桌面 | `internal/shell` | `golauncher mcp` 子命令、`--background`、启动时监听管道、确认时显示窗口 |
| glctl | `glctl mcp` | 假环境上的 MCP 服务器，`--confirm deny|allow|session`、`--ask-open`、`--trace` |

### 16.3 工具层

- **工具**：`list_plugins`（插件、命令、参数、风险）、`search`（根搜索，可限定插件；或带 `view` 句柄在某个列表里搜）、`run_command`（视图命令返回列表，无界面命令按"改动"确认）、`get_detail`（Markdown 和元数据）、`run_action`（条目的动作，默认第一个；表单句柄加 `values` 提交）。参数用 JSON Schema 描述，未知参数报错。
- **结果**：精简 JSON，不含图标、快捷键、关键词；默认 20 条（最多 50），多出的只给 `more` 数量；标题、副标题截到 200 字，Markdown 截到 4000 字。每个动作带 `risk`。
- **句柄**：`i…`（条目）、`v…`（视图，用于再搜索）、`f…`（表单），记在会话表里，10 分钟后失效；`run_action` 只接受句柄。启动器里的对话按对话 ID 保留会话（Claude Code 进程重启后桥会重连，句柄和"本次会话内允许"都还在）；外部客户端每个连接一个会话，断开时关闭打开过的视图。
- **视图加载**：视图渲染后如果插件很快调用了 `Invalidate`（后台加载），再渲染一次（最多 3 次、3 秒），这样很快到达的结果也能返回。
- **风险等级**（§4.2）：`Push`、`ShowTask` 只读；`Open`、复制是"打开"（`runas` 算改动）；回调、无界面命令、`RunProcess`、`Paste`、表单提交是"改动"；`StyleDestructive` 和 `Destructive` 命令是破坏性。回调可以用新的 `Action.ReadOnly` 标成只读（WinGet 的"刷新"已标）。
- **策略**：只读直接执行；"打开"默认直接执行，设置成"每次询问"后询问；改动每次询问，可以选"本次会话内都允许"（按插件 + 动作 + 回调名记住）；破坏性每次询问，不提供"本次会话内都允许"。
- **插件开关**：AI 对话插件自己和"AI 活动记录"永远不开放；其余默认：内置插件开、剪贴板历史关、独立进程的插件关；设置里逐个覆盖。关掉或不可用的插件也不开放。
- **敏感条目**：SDK 新增 `Item.Sensitive`；剪贴板历史把来自密码管理器（内置名单加"忽略的应用"列表）的条目标成敏感，工具层永远不返回；标了 `Exclude`（`WriteSecret`）的内容本来就不记录。表单里的密码字段也不返回、不接受。
- **外部访问**：没有对话令牌的连接是 AI 应用，"允许 AI 应用使用 golauncher"关着时每次调用都返回 `refused` 和打开方法（每次调用时检查，开关立即生效）。
- **活动记录**：每次调用一条（时间、客户端、工具、简短摘要、结果、错误说明），写在数据目录的 `agent\activity.jsonl`，保留 30 天（打开、写入、查看时清理，最多 5000 条）。摘要只有搜索词、命令名、动作和条目标题，不记对话内容和表单值。"AI 活动记录"命令可以查看和清空，设置页显示条数并能清空。

### 16.4 MCP 和管道

- **MCP**：JSON-RPC 2.0，每行一条；支持 `initialize`（协议版本 2025-11-25、2025-06-18、2025-03-26、2024-11-05，不认识的版本回最新的）、`ping`、`tools/list`、`tools/call`、`notifications/cancelled`；`initialize` 之前调用工具返回 -32002，解析错误 -32700，未知方法 -32601，未知工具或参数不是对象 -32602。工具失败（拒绝、句柄失效等）是 `isError: true` 的结果，不是协议错误。`instructions` 写明用法。
- **管道**：常驻进程启动后就监听（不只是在打开外部访问时，见 16.6）。每个连接第一行是桥发来的 `{"golauncher":"hello","token":…,"session":…}`，10 秒内没有就断开；令牌等于本进程的随机令牌（常量时间比较）时算启动器的对话。
- **`golauncher mcp`**：在自安装和单实例之前处理，不显示窗口；连不上管道时用 `--background --portable` 在后台启动启动器（尽量脱离调用方的 Job，避免关掉 Claude 时连带结束启动器），最多等 30 秒；仍然连不上时自己回答 MCP，`tools/call` 返回原因。
- **对话的配置**：Claude Code 的 `--mcp-config` 文件写在 `%LOCALAPPDATA%\golauncher\ai\mcp-<对话ID>.json`（0600），内容是本程序的 `mcp` 子命令，环境变量带令牌（`GOLAUNCHER_AGENT_TOKEN`）和对话 ID（`GOLAUNCHER_AGENT_SESSION`）；对话关闭时删除。

### 16.5 实际的命令行（开着"允许 AI 使用启动器插件"）

```
claude -p --input-format stream-json --output-format stream-json --include-partial-messages --verbose
       --session-id <uuid> | --resume <uuid>
       --setting-sources "" --strict-mcp-config --disable-slash-commands
       --tools ""   （或 --tools WebSearch,WebFetch）
       --mcp-config <mcp-<id>.json>
       --allowedTools mcp__golauncher__*   （或 mcp__golauncher__*,WebSearch,WebFetch）
       --permission-prompts none
       --append-system-prompt <启动器的说明，含工具用法>  [--model <模型>]
环境：CLAUDE_CODE_DISABLE_AUTO_MEMORY=1
```

关着时和 §15.2 完全一样（含 `--safe-mode`）。从不使用 `--bare`、`--continue` 或任何绕过授权的参数，测试里检查。

### 16.6 界面

- **确认框**：标题"Claude 桌面版想要："，下面是"安装“7-Zip”（WinGet）"，关机类附带后台任务警告，最后一行"60 秒内不回应将自动拒绝"。按钮：拒绝（Esc）、本次会话内都允许（Ctrl+Enter，破坏性动作没有）、允许（Enter；破坏性为红色）。同时只显示一个，其他排队；超时或请求被取消算拒绝。外部客户端的请求在窗口隐藏时像按热键一样显示窗口（回到根搜索，确认框在上面）；启动器里对话的请求显示窗口但不回到根，确认框盖在对话视图上，流程不断开。
- **对话里的工具步骤**：每步一行，例如 `🔧 搜索“port 8080” → 1 个结果`、`⏳ 等待操作…`（可能在等你确认）、`✅ 安装“7-Zip”`、`⛔ 已拒绝：…`、`❌ …`；步骤随对话保存，重新打开时照样显示。
- **设置 › 插件 › AI › AI 工具**：说明、"允许 AI 应用使用 golauncher"、"打开应用、链接和文件夹，复制"（直接执行／每次询问）、"允许 AI 使用的插件"（逐个开关）、"连接到 Claude 桌面版"（显示连接状态；点了之后显示改了哪个文件、备份在哪、原来和现在的内容，并提醒重启 Claude 桌面版，外部访问没开时再提醒打开）、Claude Code 的命令（可选中，"复制"按钮，只显示不执行）、活动记录（条数、查看、清空）。
- 根搜索和命令列表里多了"AI 活动记录"命令。

### 16.7 和设计不同的地方、遗留问题

- **`--safe-mode` 不能和 MCP 一起用**（16.1）：开着工具时改用 `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` 加原有参数。用户级 `~/.claude/CLAUDE.md` 是否会被读取没法在本机验证（本机没有这个文件，也不应该为测试去创建）；`--setting-sources ""` 不含 user 来源，按说明不会读，但没有实测。需要的话可以在设置里关掉"允许 AI 使用启动器插件"，回到 `--safe-mode`。
- **管道一直监听**，不是 §4.4 写的"打开外部访问后才监听"：启动器里的对话也走这条管道。外部访问关着时，没有令牌的调用一律拒绝；ACL 只允许当前用户。
- **仍然没有 `--permission-prompts host`**：工具预先批准，确认完全在启动器的工具层里做；其他工具一律被 `--permission-prompts none` 拒绝。
- "设置 › AI"仍是插件页（设置 › 插件 › AI）下的一节。
- 句柄 10 分钟从发出时算起，使用不会延长。
- Claude 桌面版的 `clientInfo` 名称、"连接到 Claude 桌面版"之后桌面版实际调用工具的效果，都没有在本机实测（不能改本机的真实配置）；配置助手只在临时目录里测试过。
- `golauncher mcp` 启动启动器、真实 GUI 里确认框的显示，靠单元测试（管道、桥、`ui.NewTester`）覆盖，没有在本机启动第二个 GUI 实例实测（本机正在运行的启动器会接管同名单实例锁）。

### 16.8 测试

- `internal/agent`：用 glctl 的假环境（fixture 数据）测每个工具、句柄（编造的、别的会话的、过期的、对话重连后仍有效）、风险等级、每个等级的策略（直接执行、询问、拒绝、本次会话内允许、破坏性不能免问、"打开"改成询问）、插件开关和默认值、敏感剪贴板条目、外部访问开关、客户端名称、活动记录和 30 天清理；MCP 服务器用测试里的客户端走管道流（初始化、版本协商、工具列表、调用、各种错误）；真实命名管道上的服务（对话令牌、外部客户端被拒、没有 hello 的连接被断开）；桥（启动器没运行时启动并等待、启动失败时自己回答、hello 行）。
- `internal/winapi/pipe`：回显、并发客户端、关闭、找不到管道、占用、对端关闭、超时、ACL（受保护的 DACL 只有当前用户一条 ACE、所有者）、句柄泄漏。
- `internal/core` / `internal/ui`（`ui.NewTester`）：外部确认显示窗口的回调、队列、三个按键、破坏性不提供会话内允许、超时、取消、中文。设置页的"AI 工具"一节（开关写入配置、连接 Claude 桌面版只改测试文件、Claude Code 命令）。
- `internal/ai/claudecode`：假 `claude` 读取 `--mcp-config`，启动其中的服务器（测试用的假 MCP 服务器），做握手并调用 `search`，按真实格式输出工具步骤；检查命令行（有工具时没有 `--safe-mode`、有 `--mcp-config` 和 `--allowedTools`，没有时照旧）、配置文件内容和删除、错误结果。真实 CLI 的 `TestRealCLITools` 只在 `GOLAUNCHER_AI_TESTS=1` 时运行。
- `plugins/ai`：工具步骤的显示（中英文）、保存和重新打开、拒绝和错误、偏好关掉时的系统提示。
- `glctl mcp` 金样：`mcp_deny`、`mcp_allow`。

## 17. 修订（2026-10-16，R56）

- **联网搜索默认打开**：偏好 `webSearch`（"允许联网搜索"）的默认值从 `false` 改为 `true`。从没改过这一项的用户，新开的对话就能搜索网页（`--tools WebSearch,WebFetch`，§15.2），系统提示里也会加上"可以搜索网页"那一句（`systemPrompt` 本来就按这个偏好决定）；在设置里明确关掉过的（配置里存了 `false`）保持关闭。联网工具只访问网络，不涉及本机，仍然不弹任何授权。测试：`plugins/ai` 的 `TestWebSearchPreference`（默认开、明确关掉时关），`plugin_list` golden 的默认值。
- **"问 AI"排在网页搜索前面**：主搜索没有好结果时，"备选命令"一节先列"问 AI：…"，再列"用 Google/Bing 搜索…"（以前反过来）。做法不是调整插件的注册顺序，而是 SDK 新增一个可选接口 `plugin.FallbackOrderer`（`FallbackOrder() int`）：宿主按它从小到大排列各插件的备选条目，相同的（没实现这个接口的都是 0）按注册顺序。网页搜索是"什么都能接"的兜底，`builtin.quicklinks` 返回 `plugin.FallbackOrderLast`（100），排在所有其他备选条目之后，以后新增的插件的备选条目也会排在它前面。打开了 AI 时，没有结果按 `Enter` 现在运行"问 AI"；AI 没打开（默认）或关掉了"问 AI"偏好时，"问 AI"不出现，只有网页搜索，和以前一样。`Tab` 不受影响，仍然是 AI（quicklinks 没有 `TabProvider`）。测试：`internal/core` 的 `TestFallbackOrder`（网页搜索的插件注册在前，仍然排在后面），`session_ai_fallback` golden 里两条的顺序。
### 修订：对话闲置 3 分钟释放进程（R58、R59，2026-10-16）

- **问题**（R58）：每个对话常驻一个 `claude.exe`，它又启动一个 `golauncher mcp` 桥，所以两个对话就多出两个 golauncher 进程。原来只在"窗口隐藏满 10 分钟"后释放，而每次显示窗口都会重新计时，常用时几乎不释放。
- **修改**（R59，你只选了这一条）：
  - 每个对话有自己的闲置计时器，发消息时停止计时，回答结束时重新开始；
  - 满 3 分钟且不在回答中，就释放这个对话的 `claude.exe`，不管窗口是否显示。进程在 Job Object 里，它启动的桥进程也一起结束；
  - 之后再发消息时，用 `--resume` 接上原来的会话，第一句会慢几秒。
  - 原来的"窗口隐藏 10 分钟"规则保留：它还会把不再显示的对话从内存里移走。
- **没做的**：关闭对话后 2 分钟释放、启动器内的对话改用 HTTP 方式的 MCP（不再启动桥进程）。所以对话进行中，仍然是一个 `claude.exe` 加一个桥。
- **测试**：`TestChatIdleRelease` 验证窗口显示时，对话闲置后会释放进程，下一条消息后又会重新计时。
## 18. 接入 ChatGPT 和 Antigravity（R69，调研结论）

按本机安装的版本（Codex CLI 0.46、Gemini CLI 0.34、Antigravity 1.107）的 `--help` 核对：

| | Claude（已做） | ChatGPT | Antigravity |
|---|---|---|---|
| 官方 CLI（用订阅） | Claude Code | Codex CLI（ChatGPT 账号登录） | 没有无界面的对话 CLI；`antigravity chat` 只在 IDE 窗口里开对话 |
| 无界面、流式 | `claude -p --output-format stream-json` | `codex exec --json` | — |
| 常驻多轮 | stream-json 双向 | `codex app-server`（实验性的 JSON-RPC，VS Code 插件用的就是它） | — |
| 续聊 | `--resume <id>` | `codex exec resume <id>` | — |
| MCP | `--mcp-config` | `-c mcp_servers.golauncher.*` | `antigravity --add-mcp`：Antigravity 作为客户端使用 golauncher（同 Claude 桌面版） |
| 只读 | `--tools ""` | `--sandbox read-only` | — |

- **复用**：工具层、MCP、确认框、活动记录、对话视图、会话存储都不变；`plugins/ai` 的 `backend.Backend` 接口本来就是为多后端设计的（§6），每家写一个后端，设置里选。
- **差异**：
  - 事件格式各写一个解析器，并锁定已验证的版本范围；
  - 隔离用户配置要逐家实测（Codex 的登录文件和 `config.toml` 在同一个 `CODEX_HOME`）；
  - Codex 第一版可以每轮一个进程（`exec` 加 `resume`），之后再评估 `app-server`。
- **Antigravity 的做法**：
  - 设置里加"连接到 Antigravity"按钮（`--add-mcp`）；
  - 加"在 Antigravity 中继续"（`antigravity chat`）；
  - 启动器里用 Gemini 回答时，走 **Gemini CLI**：`-p`、`-o stream-json`、MCP、`--approval-mode plan` 只读、Google 账号登录。它的 `--resume` 只接受 `latest` 或序号，不接受会话 ID，需要每个对话用单独的工作目录，要实测。
## 19. 多 CLI 抽象层（R70，方案已确认；第一期见 §20）

**结论**：保留 `backend.Backend`/`Conversation` 作为对话插件的唯一接口，下面拆出共用的"Agent 运行层"和每家的"适配器"；适配器里加一个 **ACP（Agent Client Protocol）通用适配器**。本机 Gemini CLI 0.34 原生支持 `--acp`；Claude Code、Codex 没有内置，可通过 Zed 的 `claude-code-acp`、`codex-acp` 适配器接入（未在本机验证）。

```
对话插件（不变）
   │  backend.Backend / Conversation（事件扩充：工具步骤、授权请求、用量）
   ▼
Agent 运行层（新，共用）
   ├─ 进程管理：Job Object、用到才启动、闲置 3 分钟释放（R59）、最多 N 个、崩溃重启、版本门槛、退回每轮一个进程
   ├─ 隔离：独立工作目录，屏蔽用户自己的配置
   ├─ MCP 注入：golauncher 的工具（R47，含令牌）
   ├─ 会话对应：对话 ID ↔ CLI 会话 ID（持久）
   ├─ 授权桥接：CLI 的授权请求 → 宿主确认框（§5）
   └─ 错误分类：没安装、没登录、额度用完、其他
   ▼
适配器（每家一个）
   ├─ Claude 原生（现有 stream-json，§6.1、§15、§16）
   ├─ ACP 通用：initialize、session/new（带 mcpServers）、session/prompt、session/update、
   │   session/request_permission（接确认框）、session/cancel、session/load（可选能力）
   │   └─ 只需"命令 + 参数"：Gemini `gemini --acp`；Codex/Claude 也可经 Zed 适配器
   └─ Codex 原生（按需）：`codex exec --json` / `app-server`
```

**分期**：
1. 抽出运行层，Claude 改造成第一个适配器（行为不变，现有测试兜底），设置里加"后端"选择；
2. ACP 通用适配器，先接 Gemini，并支持"自定义 Agent"（名称、命令、参数）；
3. Codex：实测后在原生接法和 ACP 之间二选一。

Antigravity 的"连接到 Antigravity"（`--add-mcp`）和"在 Antigravity 中继续"（`antigravity chat`）与此独立，可以并行做。

**风险**：ACP 和各家 CLI 都在快速变化，要锁定验证过的版本、只用协议核心；会话续接是可选能力；用 Zed 适配器需要 Node。

## 20. 实现记录（第一期：Agent 运行层，2026-10-16，R71）

§19 的第一期：把 Claude Code 后端里和具体 CLI 无关的部分抽成共用的运行层，Claude Code 改成它的第一个适配器；设置里加"后端"选择。**用户看到的行为不变。**

### 20.1 结构

| 部分 | 位置 | 说明 |
|------|------|------|
| 后端契约 | `plugins/ai/backend` | 不变的 `Backend`/`Conversation`；事件新增 `EventPermission`（`Event.Permission`：工具、参数、选项、`Respond`），错误新增 `ErrUnavailable` |
| 运行层 | `internal/ai/agentrt` | `Runtime` 实现 `backend.Backend`；`adapter.go`（适配器接口）、`runtime.go`（查找、版本、LRU、工作目录、环境）、`conversation.go`（一轮的重试和退路、停止、释放、关闭）、`process.go`（进程、行队列）、`tools.go`（MCP 工具服务器）、`sessions.go`（会话 ID 映射）、`version.go` |
| Claude Code 适配器 | `internal/ai/claudecode` | `claudecode.go`（`Info`、命令行、编解码器）、`stream.go`（stream-json 解析，原样保留）、`tools.go`（`--tools`/`--allowedTools`/`--mcp-config` 和配置文件格式）；`Backend` 就是 `agentrt.Runtime` 加这个适配器，`Options`、`ToolServer` 是运行层类型的别名 |
| 后端注册表 | `internal/builtin/aibackends.go` | `aiBackends()`：按 ID 列出后端（现在只有 `claude-code`），第一个是默认；第二期在这里加 |
| 插件 | `plugins/ai/backends.go` | `Options.Backends`（`BackendOption{ID, Backend}`）；偏好 `backend`；`chooser` 按偏好转发到选中的后端 |

### 20.2 适配器接口

```go
type Adapter interface {
	Info() Info                            // 怎么找、版本范围、要去掉的环境变量、能力
	Command(l Launch) (Command, error)     // 命令行和要加的环境变量
	MCPConfig(s MCPServer) ([]byte, error) // 要写成文件的 MCP 配置；nil 表示用别的方式传（Launch.Tools）
	NewCodec(l Launch) Codec               // 一个进程的协议状态
	ExitOutcome(text string) Outcome       // 进程没结束这一轮就退出时，按 stderr 末尾分类
}

type Info struct {
	ID, Name                string   // "claudecode"（日志、内部错误）、"Claude Code"（显示）
	Commands, InstallPaths  []string // PATH 上找的命令名、常见安装位置
	VersionArgs             []string
	ParseVersion            func(string) string // nil：取第一个 x.y.z
	MinVersion              string
	TestedMajor             int
	EnvDrop                 []string
	WorkDir                 string // 工作目录下的子目录（Claude 为空）
	PerTurnOnly, NoPerTurn  bool   // 只有单轮模式（Codex exec）/ 没有单轮模式（ACP）
	AssignsSessionID        bool   // 会话 ID 由 CLI 分配（ACP、Codex），运行层记映射
}

// Launch：Path、Dir、Persistent、SessionID、Resume、Model、WebSearch、SystemPrompt、
//         Tools *MCPServer、MCPConfig（写好的文件路径）、Prompt（单轮的问题）

type Codec interface {
	Message(text string) ([]byte, error) // 常驻进程：一条用户消息
	Prompt(text string) []byte           // 单轮进程：写进 stdin 后关闭
	Interrupt() []byte                   // 常驻进程：停止回答；nil 表示直接结束进程
	NewDecoder(io TurnIO) Decoder        // TurnIO：Emit（事件）、Reply（往 stdin 写，回答授权请求）
}
type Handshaker interface { // 可选：第一条消息前的握手（ACP 的 initialize、session/new|load）
	Handshake(ctx context.Context, c Conn) (sessionID string, err error)
}
type Decoder interface {
	Line(line []byte) (end, garbage bool) // 一行输出；end：这一轮结束；garbage：根本不是协议
	Outcome(cancelled bool) Outcome       // Done（含用量）/ Cancelled / Failed / LostSession / SessionInUse
	Progress() Progress                   // Output、Started、Emitted、SessionID
}
```

### 20.3 运行层负责的事（和 §15、§16、§17 的行为一致）

- 查找 CLI（`Options.Path`、`Info.Commands`、`Info.InstallPaths`）和版本门槛（低于 `MinVersion` 报"版本太旧"，主版本不是 `TestedMajor` 时标为未验证并用单轮模式）。
- 进程：Job Object、不弹控制台；第一次发送时才启动；常驻和单轮两种模式及其退路（第一轮就退出或输出不是协议 → 单轮；崩溃后下一轮重启；会话丢失时用同一 ID 新开；会话被占用时改为续接）；最多 `MaxProcesses` 个常驻进程，超出时释放最久没用的空闲对话；停止回答时常驻进程发 `Interrupt`，5 秒内没结束就杀掉，单轮进程直接杀掉；`Release` 是闲置释放的钩子（3 分钟和窗口隐藏 10 分钟的计时仍在插件里，R59）。
- 工作目录 `%LOCALAPPDATA%\golauncher\ai`（加 `Info.WorkDir`），去掉 `Info.EnvDrop` 里的环境变量。
- MCP：由 `ToolServer` 算出这个对话的服务器（令牌、`GOLAUNCHER_AGENT_SESSION`）；适配器要文件时写 `mcp-<对话ID>.json`（0600），对话关闭时删除。
- 会话 ID：`AssignsSessionID` 的 CLI 把"对话 ID → CLI 会话 ID"记在工作目录的 `sessions.json`，续接时用它；Claude Code 的会话 ID 就是对话 ID，不写这个文件。
- 错误种类：没安装、版本太旧、"没有回答就结束了"、进程退出码；CLI 自己的错误（没登录、额度）由适配器的解码器分类。
- 日志：只记长度、耗时、错误种类和进程号，日志带 `backend=<Info.ID>`。

### 20.4 "后端"设置

- 设置 › 插件 › AI 的第二项"后端"（`backend`，下拉框），选项来自注册表，标题是后端的产品名（不翻译）；默认 `claude-code`，保存在插件偏好里。
- 对话按偏好选后端；换了后端，已有的对话下次发送时关掉旧的对话、在新后端上重新打开。
- 偏好里的 ID 没有对应的后端（比如别的版本写的配置）：对话里显示"在 设置 › 插件 › AI 里选的后端在这个版本的 golauncher 里没有"，下面附上 ID。
- `glctl` 的假环境同样只有一个选项，所以 `plugin_list` 金样只多了这一项偏好，其他金样不变。

### 20.5 ACP 和 Codex 怎么接（第二期，没有实现）

| | ACP（`gemini --acp`，或 Zed 的 `claude-code-acp`、`codex-acp`） | Codex CLI（`codex exec --json`） |
|---|---|---|
| 模式 | `NoPerTurn`：只有常驻，出问题时重启进程 | `PerTurnOnly`：每轮一个进程 |
| 会话 | `AssignsSessionID`：`Handshake` 发 `initialize`，再发 `session/load`（续接且对方支持）或 `session/new`，返回对方给的会话 ID | `AssignsSessionID`：解码器从 `thread.started` 报 `Progress.SessionID`；续接用 `codex exec resume <id>` |
| MCP | `MCPConfig` 返回 nil，`Handshake` 把 `Launch.Tools` 放进 `session/new` 的 `mcpServers` | `MCPConfig` 返回 nil，`Command` 用 `-c mcp_servers.golauncher.*` |
| 消息 | `Message`：`session/prompt` 请求；它的响应就是这一轮的结束（`stopReason`） | `Prompt`：问题从 stdin 读 |
| 事件 | `session/update`：`agent_message_chunk` → 文本，`tool_call` → 工具步骤，`tool_call_update` → 结果 | `item.*` → 文本和工具步骤，`turn.completed` → 用量和结束 |
| 授权 | `session/request_permission` → `EventPermission`，`Respond` 经 `TurnIO.Reply` 回 JSON-RPC 响应 | `--sandbox read-only`，不问 |
| 停止 | `Interrupt`：`session/cancel` 通知 | 结束进程 |

运行层已经支持这些钩子（`Handshaker`、`TurnIO.Reply`、会话映射、`PerTurnOnly`/`NoPerTurn`），用测试里的假 CLI（测试程序自己按一个简单的行协议回答）验证过，所以第二期只需要写适配器、注册，并在插件里接确认框。

### 20.6 怎么证明行为不变

- **命令行**：重构前先加了 `TestArgvTable`，把 2 种 CLI（`claude.exe`、npm 的 `claude.cmd`）× 常驻/单轮 × 新开/续接 × 工具（关、开、开但没有工具服务器）× 联网 × 模型 × 系统提示（无、两行）共 192 种组合的命令行和额外环境变量录进 `testdata/argv.golden.json`，在重构前的代码上提交；重构后逐字节一致。
- **原有测试**：`internal/ai/claudecode` 的 `claudecode_test.go`、`real_test.go`、假 `claude`、假 MCP 服务器一行没改；测试里用到的内部名字（`parseVersion`、`compareVersions`、`toolConfigName`、`liveCount`、`env`）在 `export_test.go` 里接到运行层。`plugins/ai` 的原有测试也没改。
- **金样**：只有 `plugin_list` 多了"后端"偏好。
- `go test -race` 覆盖 `agentrt`、`claudecode`、`plugins/ai`、`builtin`；真实 CLI 的 `TestRealCLI`、`TestRealCLITools`（`GOLAUNCHER_AI_TESTS=1`）在完成后跑过一次，通过。

### 20.7 和方案不同的地方、遗留问题

- **授权桥接**：§19 把它画在运行层里。实际上运行层只提供通道（`TurnIO.Reply`），请求作为 `EventPermission` 交给插件回答；插件现在没有界面，一律拒绝（`Respond("")`）。第二期接 §5 的确认框，并要定超时和"停止回答时自动拒绝"由谁负责（建议适配器）。Claude Code 不会发这种事件（工具预先批准，确认在启动器自己的工具层里）。
- **会话映射**的文件不会随删除对话而清理（插件删除对话时没有通知后端）；换后端时，新后端看不到旧后端的上下文（Claude Code 的会话 ID 就是对话 ID，换回来还能接上）。
- 日志文字变成通用的（例如"agent turn ended"，带 `backend=claudecode`），内容规则不变；内部错误仍以 `claudecode:` 开头。
- 运行层的"预览命令行"`Runtime.Command` 是为测试导出的，不启动任何进程。

## 21. 实现记录（第二期：Codex 和 Gemini CLI，2026-10-16，R72）

§19 的第二、三期一起做：Gemini CLI 走 **ACP 通用适配器**，Codex 走**原生适配器**；第一期遗留的授权桥接、会话映射清理按 R72 的默认决定处理。设置 › 插件 › AI ›"后端"多了"Codex (ChatGPT)"和"Gemini CLI"。

**不依赖 Node.js**：golauncher 自己不引入 Node（不用 Zed 的 `claude-code-acp`、`codex-acp`）。Claude Code 和 Codex 都是原生程序（Codex 一律运行原生的 `codex.exe`，即使它是通过 npm 装的，也不经过 Node 的启动脚本）；**只有 Gemini CLI 本身是 Node.js 程序**，用户装 Gemini CLI 时已经装了 Node，golauncher 直接运行它的 `gemini.cmd`（或 `gemini.exe`），不经过 PowerShell 脚本。

### 21.1 结构

| 部分 | 位置 | 说明 |
|------|------|------|
| ACP 通用适配器 | `internal/ai/acp` | `acp.go`（`Config`、`NewAdapter`、`Custom`、错误分类）、`codec.go`（JSON-RPC、握手、`session/prompt`、`session/cancel`）、`decode.go`（`session/update` 和授权请求的解码）；只要"命令 + 参数"，以后可以从设置里加自定义 Agent |
| Gemini CLI | `internal/ai/gemini` | `gemini.go`（查找、命令行、要去掉的环境变量）、`settings.go`（golauncher 自己的系统设置文件）、`tools.go`（从参数推断 golauncher 工具名） |
| Codex | `internal/ai/codex` | `codex.go`（命令行、版本解析）、`candidates.go`（只找原生 `codex.exe`）、`stream.go`（`--json` 事件解码、错误分类） |
| 运行层新增 | `internal/ai/agentrt` | `Info.Candidates`（多个安装时用版本最高的）、`Info.LoginArgs`（检查登录）、`Launch.Version`/`ChatID`、`Command.Files`（启动前写进工作目录的文件，预览不写）、`permissions.go`（授权桥接）、`ResumeLoser` 和 `EventNotice`（接不上旧会话时告诉用户）、`Runtime.Forget`、Handshake 返回的 `*backend.Error` 保留种类 |
| 后端契约新增 | `plugins/ai/backend` | `EventNotice`（`NoticeNewSession`）；`PermissionRequest.Kind`、`Done`、`Destructive()`、`Option()`；`PermissionAsker`/`PermissionAsk`/`PermissionDecision`；`Forgetter` |
| 插件 | `plugins/ai` | `backends.go`（每个对话的后端、每个后端自己的模型偏好、`forget`）、`chatbackend.go`（授权询问、按后端的文字、记录、页脚）、`history.go`（删除和清空时清理映射，列表显示后端） |
| 工具层 | `internal/agent` | `ConfirmChatPermission`（同一个确认框，记进活动记录）；桥的 hello 多了 `agent`（`GOLAUNCHER_AGENT_NAME`），确认框写"Gemini CLI（AI 对话）" |
| 接线 | `internal/app`、`internal/builtin/aibackends.go` | 常驻进程建好工具层后，把 `PermissionAsker` 交给 AI 插件；注册表加 Codex、Gemini |

### 21.2 授权桥接（共用层）

- **运行层**（`agentrt/permissions.go`）：适配器的解码器发出 `EventPermission` 时，运行层把 `Respond` 包一层：只算第一次；**60 秒**（`PermissionTimeout`）没回答就拒绝；用户**停止回答**时先拒绝所有待回答的请求，再发中断（ACP 要求先回答再结束）；这一轮结束时还没回答的也拒绝。回答后关闭 `Done`，确认框随之关掉。
- **插件**：收到请求就调用宿主的 `PermissionAsker`，显示在和 MCP 工具相同的确认框里：
  - 标题"<后端名>（AI 对话）想要："，内容是 Agent 自己的描述（如"Fetching content from: …"），另起一行写工具名；
  - 选项映射：允许 → `allow_once`；"本次会话内都允许"只在 Agent 提供 `allow_always` **而且**不是破坏性（ACP 的 kind 不是 `edit`/`delete`/`move`/`execute`）时出现；拒绝 → `reject_once`（没有就回 `cancelled`）；
  - 没有宿主（测试、glctl）时一律拒绝。
- **工具层**：`ConfirmChatPermission` 走 `core.ConfirmExternal`（60 秒超时、排队、`InPlace` 显示在对话上方），并在活动记录里记一条：工具、Agent 的描述、结果（允许或拒绝）、`permission request: allow|deny`。
- Claude Code 和 Codex 不会发授权请求：它们能用的工具都预先批准了，改动东西的操作由 golauncher 自己的工具层确认（§5）。

### 21.3 每个对话固定后端、清理映射

- `Session.BackendID`（新字段）记下对话开始时的后端；旧记录没有这个字段，按 Claude Code 处理。新对话在发出第一条消息时取当时的偏好。
- 换了"后端"偏好只影响新对话；已有对话继续用原来的后端（和 R71 不同：那时会把对话搬到新后端）。
- 对话的后端在这个版本里没有时：打开对话就在末尾说明"这个对话是用 X 开始的，这个版本里没有它"，发消息时也这样报错。偏好里的后端没有时，仍是 R71 的说明。
- "AI 对话历史"的副标题是"时间 · 后端名"。
- 删除、清空对话，以及按保留期清理时，插件调用后端的 `Forget`：运行层从 `sessions.json` 里删掉"对话 ID → CLI 会话 ID"。CLI 自己的会话文件不动（它们属于 CLI，按 CLI 自己的规则清理）。
- CLI 接不上旧会话时（ACP 不支持 `session/load`、加载失败；Codex 找不到会话），运行层新开会话，并在这一轮开头发 `EventNotice`，对话里显示"X 无法接上之前的对话，这次回答从新的对话开始"。Claude Code 的行为不变（它的会话 ID 就是对话 ID）。
- 每个有自己模型的后端多一个"模型"偏好（`model.codex`、`model.gemini`），默认是 CLI 的默认模型；原来的"模型"偏好只用于 Claude Code。

### 21.4 Gemini CLI（ACP）

- **查找**：PATH 上的 `gemini.exe`，再是 PATH 上的 `gemini.cmd`，最后 `%APPDATA%\npm\gemini.cmd`。只找到 `gemini.ps1` 或没有扩展名的脚本时报"没有安装"并说明原因。**Gemini CLI 本身是 Node.js 程序**，`gemini.cmd` 会调用 Node；golauncher 不经过 PowerShell。版本门槛 0.34.0（`--acp` 取代了 `--experimental-acp`）。
- **命令行**：`gemini --acp --approval-mode default -e none --allowed-mcp-server-names golauncher [-m <模型>]`。模型名只接受 `[A-Za-z0-9._-]`（`.cmd` 的命令行经过 cmd.exe）。
- **隔离**（设置的优先级：系统设置 > 工作区 > 用户）：环境变量 `GEMINI_CLI_SYSTEM_SETTINGS_PATH` 指向工作目录里 golauncher 写的系统设置文件（`Command.Files`，按内容命名，联网和不联网的对话互不覆盖）。已有的管理员系统设置文件会先读进来，再把 golauncher 的设置合并在上面；`system-defaults.json` 仍指向原来的。内容：
  - `tools.core` 为空（联网时只有 `google_web_search`、`web_fetch`）：**不注册任何本地工具**（读写文件、Shell、记忆、子 Agent……），`tools.exclude` 再列一遍；`tools.discoveryCommand`、`callCommand` 为空；
  - `mcp.allowed` 只有 `golauncher`（命令行也传 `--allowed-mcp-server-names`）：用户 `settings.json` 和工作区里的 MCP 服务器都不启动；golauncher 的工具因此也被预先允许（不重复确认，确认在工具层）；
  - `-e none`：不加载扩展；`hooksConfig`、`skills`、`ide`、检查点、子 Agent、实验功能都关掉；
  - `context.fileName` 设成一个永远不存在的文件名：不加载 `~/.gemini/GEMINI.md` 和项目里的记忆文件；
  - 审批模式固定 `default`，`security.disableYoloMode`；不自动更新。
  - 登录不受影响：仍用 `~/.gemini` 里的 Google 登录（不改 HOME）。
  - 去掉父进程是 Gemini CLI 时的环境变量（`GEMINI_CLI`、IDE 连接、`GEMINI_SYSTEM_MD` 等），保留用户自己的 API Key、`GEMINI_MODEL` 等选择。
- **ACP 流程**：
  1. `initialize`：`protocolVersion: 1`，`clientCapabilities` 里 `fs.readTextFile`、`fs.writeTextFile`、`terminal` 都是 false（不给 Agent 读写文件、开终端的通道），`clientInfo` 是 golauncher；
  2. 续接且对方声明 `loadSession` → `session/load`（`cwd` 是固定的工作目录，`mcpServers` 是 golauncher 的服务器）；加载失败或对方不支持 → `session/new`，并在对话里提示"接不上之前的对话"。Gemini 在 `session/load` 的响应前后都会回放历史（`session/update`），握手读到 300 毫秒没有输出为止（最多 5 秒）再结束，回放的内容不会混进新的回答；
  3. 新会话 → `session/new`，返回的会话 ID 记进 `sessions.json`；
  4. 每条消息是一个 `session/prompt` 请求，它的响应（`stopReason`）就是这一轮的结束；新会话的第一条消息把系统提示作为单独的第一个文本块（ACP 没有系统提示字段）；
  5. 停止：先拒绝待回答的授权请求，再发 `session/cancel` 通知。
- **事件映射**（例子是编的）：

  | Agent 发来 | golauncher |
  |---|---|
  | `agent_message_chunk {"type":"text","text":"你好"}` | 文本 |
  | `agent_thought_chunk`、`user_message_chunk`、`plan`、`available_commands_update`、模式变化 | 忽略 |
  | `tool_call {"toolCallId":"t1","title":"{\"query\":\"Visual Studio Code\"}","kind":"other"}` | 工具步骤 `mcp__golauncher__search`，参数就是标题里的 JSON |
  | `tool_call {"title":"Searching the web for: \"天气\"","kind":"search"}` | `WebSearch` |
  | `kind: "fetch"`（"Fetching content from: …"） | `WebFetch` |
  | `tool_call_update`（`completed`/`failed`） | 工具结果（文本内容拼起来，失败标为出错） |
  | `session/request_permission` | 工具步骤 + `EventPermission`（§21.2） |
  | `fs/*`、`terminal/*` 等其他请求 | JSON-RPC 错误 -32601 |
  | 响应 `end_turn`/`max_tokens`/`max_turn_requests` | 完成；`cancelled` → 已停止；`refusal` → 失败 |
  | 错误里有 "Session not found" | 会话丢失 → 新开 |

  Gemini 0.34 的 `tool_call` 只给标题（MCP 工具的标题就是参数的 JSON），不给工具名。因为它只能用 golauncher 这一个 MCP 服务器，`gemini/tools.go` 按参数推断工具名（有 `command` → `run_command`，有 `action`/`values` → `run_action`，有 `query` → `search`，没有参数 → `list_plugins`，只有 `handle` 时看结果）；失败时 Gemini 的错误文字里有工具名。
- **授权**：Gemini 对 `web_fetch` 会请求授权（kind `fetch`，选项是"Always Allow…"`allow_always`、"Allow"`allow_once`、"Reject"`reject_once`），网页搜索不问；golauncher 的工具不问（`mcp.allowed`）。
- **防斜杠命令**：Gemini 会把以 `/` 或 `$` 开头的消息当成它自己的命令（`/memory add`、`/init`、`/extensions install` 会改文件或装东西），所以这样的消息前面加一个零宽空格。
- **错误**：429、"quota"、"rate limit"、"resource exhausted" → 额度用完；-32000、"Authentication required"、"API key not valid" → 没登录；"no longer supported" 一类 → 原样显示（重新登录也没用）。
- **还会影响 Gemini 的用户配置**：`~/.gemini/policies` 里的策略文件、遥测和隐私设置、默认模型名。会话存在 `~/.gemini/tmp/<工作目录的哈希>/chats`，按 Gemini 自己的保留期（默认 30 天）过期，过期后续接会提示新开。Gemini 0.34 不报用量，页脚只显示模型。

### 21.5 Codex

- **只运行原生 `codex.exe`**（`Info.Candidates`，按顺序，去重，只返回 `.exe`）：
  1. PATH 上的 `codex.exe`（GitHub Releases、winget 等独立安装）；
  2. Codex 应用装的 `%LOCALAPPDATA%\OpenAI\Codex\bin\codex.exe` 和 `bin\*\codex.exe`；
  3. npm 安装：在有 `codex.cmd`/`codex.ps1`/`codex` 启动脚本的 PATH 目录和 `%APPDATA%\npm` 下，找包里自带的原生程序 `node_modules\@openai\codex\vendor\<三元组>\codex\codex.exe`（arm64 先找 `aarch64-pc-windows-msvc`，再找 `x86_64-pc-windows-msvc`），以及新版 npm 包可能的 `@openai\codex-win32-x64|arm64\vendor\…` 布局（这两种布局本机没有，未验证）。
  - 只找到 Node 的启动脚本、找不到原生程序时报"没有安装"，说明 golauncher 需要原生的 `codex.exe`，**不会去运行 Node**。设置里配置的路径如果是启动脚本，也换成旁边的原生程序。
  - 找到多个时，运行层对每个跑 `--version`，用版本最高的（版本号如 `codex-cli 0.162.0-alpha.17.2` 取 `0.162.0`）。版本门槛 0.130.0（有 `--ignore-user-config` 的最早验证版本）。之后 `codex login status` 退出码不为 0 → "没有登录"。
- **命令行**（每轮一个进程，问题从 stdin 读）：

  ```
  codex.exe exec --json --color never --skip-git-repo-check --ignore-user-config --ignore-rules --sandbox read-only
    -c approval_policy="never" -c skills.include_instructions=false -c include_permissions_instructions=false
    -c include_apps_instructions=false -c include_collaboration_mode_instructions=false -c project_doc_max_bytes=0
    -c check_for_update_on_startup=false
    -c features.<名字>=false（shell_tool、unified_exec、apps、plugins、hooks、multi_agent、memories、browser_use、
       computer_use、image_generation、view_image、tool_suggest、shell_snapshot……每个一条）
    [-c features.code_mode_host=false]（没有启动器工具时）
    -c web_search="live"|"disabled"  [-m <模型>]  [-c developer_instructions=<系统提示>]
    [-c mcp_servers.golauncher.command=… .args=[…] .env.<名字>=…（排序）
     -c mcp_servers.golauncher.default_tools_approval_mode="approve"]
    [resume <会话 ID>] -
  ```

  - `--ignore-user-config`：不读 `$CODEX_HOME/config.toml`（用户的 MCP 服务器、`notify` 程序、钩子、插件、模型……），**登录仍用 `CODEX_HOME` 里的 `auth.json`**。用一个编造的 `CODEX_HOME` 实测过：配置里的 MCP 服务器不加 `--ignore-user-config` 会启动，加了就不启动；golauncher 自己的 `-c mcp_servers.golauncher.*` 照样启动。`-c mcp_servers={}` **不能**清掉用户的服务器（会合并）。
  - 功能开关用 `-c features.X=false` 而不是 `--disable X`：`--disable` 遇到这个版本不认识的名字会直接报错，`-c` 会忽略，同一份列表在 0.130 和 0.162 上都能用。**关掉 `shell_tool`，Codex 没有运行命令的工具**；沙箱仍是只读。
  - 0.162 的模型都是"code mode"：所有工具（包括 MCP）都通过 `codex-code-mode-host.exe` 里运行的 JavaScript 调用。有启动器工具时必须保留它，否则模型调不到工具；调用仍以 `mcp_tool_call` 事件出现。这个 JS 沙箱本身没法验证；关掉 Shell 后它能碰到的只有 golauncher 的工具和网页搜索。
  - `default_tools_approval_mode="approve"` 只预先批准 golauncher 的工具（改动东西的操作由工具层确认，§5），其他一概不批准；`approval_policy="never"` 下 Codex 不会发授权请求。
  - 去掉父进程是 Codex 时的环境变量（`CODEX_SANDBOX`、`CODEX_THREAD_ID` 等），保留 `CODEX_HOME`。
- **事件映射**（例子是编的）：

  | Codex `--json` | golauncher |
  |---|---|
  | `{"type":"thread.started","thread_id":"…"}` | 会话 ID（记进 `sessions.json`，下一轮 `resume <id>`） |
  | `item.*` 的 `agent_message {"text":"…"}` | 文本（只发新增的部分；多条消息之间空一行） |
  | `item.started` 的 `mcp_tool_call {"server":"golauncher","tool":"search","arguments":{"query":"Visual Studio Code"}}` | 工具步骤 `mcp__golauncher__search` |
  | `item.completed` 的 `mcp_tool_call {"result":{"content":[{"type":"text","text":"{…}"}]}}` | 工具结果（失败或有 `error` 时标为出错） |
  | `web_search {"query":"…"}` | `WebSearch` |
  | `command_execution`、`file_change`（不应该出现） | 显示成"Shell"、"FileChange"步骤 |
  | `reasoning`、`todo_list` | 忽略 |
  | `error` 事件里有 "Reconnecting… n/5"、"; retrying" | 忽略（临时的） |
  | `turn.completed {"usage":{"input_tokens":…,"output_tokens":…}}` | 完成，用量（缓存的输入已包含在 `input_tokens` 里，推理的输出已包含在 `output_tokens` 里） |
  | `turn.failed {"error":{"message":"…"}}` | 失败（分类见下） |

- **错误**：401 / "Not logged in" / "authentication" → 没登录；"usage limit"、"rate limit"、"quota"、独立的 429 → 额度用完；"requires a newer version of Codex" → 版本太旧；"model is not supported when using Codex with a ChatGPT account" → 原样显示；续接时 stderr 上的 "no rollout found for thread id" → 会话丢失：运行层新开会话并提示（为此运行层在进程退出时也处理 `LostSession`）。
- **还会影响 Codex 的用户配置**：`$CODEX_HOME/AGENTS.md` 总会作为用户说明发给模型（没有配置项能关）；会话写在 `$CODEX_HOME/sessions`，golauncher 的对话会出现在用户的 Codex 历史里；环境上下文里有工作目录、日期和时区。
- **`codex app-server`**（以后的常驻模式，没有实现）：JSON-RPC（`thread/start`、`thread/resume`、turn、中断、流式增量），有真正的授权请求（可以接 `EventPermission`），桌面应用和 VS Code 用的就是它；但标着实验性，帮助里没有 `--ignore-user-config`，隔离只能靠配置覆盖，还要避开共享的守护进程（`app-server daemon`）。适合作为以后的单独适配器，需要重新做隔离验证。

### 21.6 测试和验证

- **假 CLI**：
  - Codex：`codex/testdata/fakecodex`（测试里编译成 `codex.exe`），按问题输出编造的 JSONL：普通回答、多条消息、MCP 工具调用和失败、网页搜索、没登录、额度、临时重试后成功、续接时会话丢失、崩溃、慢回答（停止）、非协议的行；记录每次的命令行。测试覆盖多轮和续接（换一个后端实例后映射仍在）、`Forget`、事件映射、错误、停止、崩溃、登录检查、版本太旧、多个安装选最高版本、只有 Node 启动脚本时的报错（`candidates_test.go` 用假的目录布局，含 arm64 顺序）。
  - ACP：测试程序自己当假的 ACP Agent（`ACP_FAKE=1`）：`initialize`、`session/new`、`session/load`（响应前后都回放历史，不能混进回答）、不支持 `loadSession` 和加载失败时提示新开、多轮、流式片段、工具调用（JSON 标题和网页搜索）、授权允许 / 拒绝 / 超时 / 等待时停止（先回答拒绝，再收到 `session/cancel`）、流式时停止、不支持的 `fs` 请求回 -32601、`session/new` 鉴权失败 → 没登录、429 → 额度、中途崩溃后下一轮重启并加载会话、非协议的行、`Forget`；检查 Agent 收到的 JSON（协议版本、没有能力、带环境变量的 `mcpServers`、`cwd`）。
  - 运行层（`agentrt/bridge_test.go`）：授权超时和停止时自动拒绝、`Done`、接不上会话的提示、`Forget`、多个安装选最高版本、登录检查、`Command.Files`（预览不写文件）。
  - 插件（`backends_test.go`）：每个对话固定后端、换偏好只影响新对话、历史里的后端名、旧记录按 Claude Code、后端不存在时的说明、每个后端自己的模型偏好、删除 / 清空 / 过期清理时 `Forget`、提示、授权询问（允许、本次会话、拒绝、破坏性不给"本次会话"、没有宿主时拒绝、被运行层回答后关掉确认框）。工具层：`ConfirmChatPermission` 的确认和活动记录、"Gemini CLI（AI 对话）"的名字。
- **命令行金样**：`codex/testdata/argv.golden.json`（续接 × 工具关/开/没有工具服务器 × 联网 × 模型 × 系统提示，48 种）、`gemini/testdata/argv.golden.json`（`gemini.cmd`、`gemini.exe` 两种 × 同样的组合，64 种，含环境变量和设置文件内容），钉住每个隔离参数。Claude Code 的 `argv.golden.json` 一字未变。
- **真实 CLI**（`GOLAUNCHER_AI_TESTS=1`，只用很短的问题和只读的搜索，工具服务器是 `glctl mcp` 的假环境）：
  - **Codex 0.162**（Codex 应用装的）：`TestRealCLI` 通过（第二轮用 `resume` 续接，记得第一轮的内容，没有新开提示）；`TestRealCLITools` 通过（`mcp__golauncher__search`，参数含 "Visual Studio Code"，结果含 `"handle"`）。npm 装的 0.46 太旧：服务器拒绝它能用的所有模型，所以门槛定在 0.130；0.130 能启动，但它的默认模型也被这个账号拒绝，选最高版本时会用 0.162。
  - **Gemini CLI 0.34**：`gemini --version` 正常，但这个 Google 账号在 `session/new` 被服务器拒绝（"这个客户端不再支持个人版 Gemini Code Assist，请迁移到 Antigravity"），所以**真实的对话和工具调用没法验证**。用测试自己设置里的假 API Key 跑了只到 `session/new` 的隔离测试 `TestRealIsolation`：golauncher 的工具服务器启动了，工作区里的 MCP 服务器和工具发现命令没有启动（对照：不加隔离时会启动）。还观察到：`initialize` 约 7 秒（Node 启动），`session/load` 的历史回放在响应前后 1 毫秒内到达。

### 21.7 和方案不同的地方、遗留问题

- **Codex 不用 Zed 的 `codex-acp`**（需要 Node），走原生 `codex exec --json`；**Gemini 是唯一需要 Node 的后端**（Gemini CLI 自己就是 Node 程序）。
- **Codex 的版本门槛**比 R69 调研时本机的 0.46 高：0.46 没有 `--ignore-user-config`，而且这个账号已经用不了它。实际用的是 Codex 应用自带、会自动更新的 `codex.exe`。
- **Codex 的模型偏好只有"默认"**：能用的模型随账号和 CLI 版本变化，不写死；Gemini 列了 2.5 Pro / Flash / Flash-Lite。
- **授权桥接放在运行层**（超时、停止、轮次结束都由运行层拒绝），插件只负责问用户——和 §20.7 的建议一致。
- **R71 的"换后端会搬对话"改成每个对话固定后端**（R72 的决定 5）。
- **Gemini 的工具名靠推断**（0.34 的 ACP 不给工具名），以后 Gemini 在 `tool_call` 里带上 `rawInput`/工具名时可以去掉。
- **还没做**：
  - 真实环境下验证 Gemini 的对话、工具、授权（需要一个还能用 Gemini CLI 的账号，或者新版 Gemini CLI）；
  - Codex 的 `app-server` 常驻模式（见 §21.5）；
  - 设置里添加"自定义 ACP Agent"（名称、命令、参数）：`acp.Custom` 已经有了，缺界面；自定义 Agent 没有 `--version` 时运行层还要一个"不检查版本"的选项；
  - Codex 和 Gemini 自己的会话文件不随删除对话清理（只清 golauncher 的映射）；
  - ACP 的 `clientInfo.version` 现在是 "dev"，接线时可以传 golauncher 的版本。

### 21.8 macOS（2026-10-16，`macos-support` 分支）

Windows 上的行为一字未变（Windows 的查找逻辑原样保留，`gemini/testdata/argv.golden.json` 不变）；以下只说 macOS（以及顺带的 Linux）。PATH 是登录 shell 的（GUI 启动时由 shell 补上，[macos-port.md](macos-port.md) §5.3），此外都会再找 `agentrt.UnixBinDirs`：`/opt/homebrew/bin`、`/usr/local/bin`、`~/.local/bin`、`~/.npm-global/bin`、`~/.nvm/versions/node/*/bin`（nvm 通常只在交互式 shell 里加载，登录 shell 的 PATH 里没有）。

- **Codex**（`codex/candidates.go` 的 `unixCandidates`，`findEnv.OS` 区分平台，测试用编造的环境在任何系统上都能跑 Windows 和 macOS 两套）：仍然**只运行原生程序，不经过 Node**（R73）。按顺序：
  1. PATH 和上面这些目录里的 `codex`，必须可执行、且不是脚本（开头不是 `#!`）——Homebrew 的 cask（`brew install --cask codex`，`/opt/homebrew/bin/codex` 链到 Caskroom 里的原生程序）、GitHub Releases 的二进制都在这里；按链接解析后的真实路径去重；
  2. Codex 应用可能自带的 CLI：`/Applications/Codex.app/Contents/Resources/codex`、`…/Resources/bin/codex`（以及 `~/Applications` 下的）。**没有验证**：本机没有 Codex 应用，不知道 macOS 版是否带 CLI、放在哪；
  3. npm 安装：上面目录里的 `codex` 是脚本时（npm 把 `<prefix>/bin/codex` 链到 `<prefix>/lib/node_modules/@openai/codex/bin/codex.js`），从链接找到包，或按 npm 的 prefix 布局推出包，再在启动脚本自己查找的位置找原生程序（三元组 `aarch64-apple-darwin`，Apple 芯片上再找 `x86_64-apple-darwin`，Intel 上只有后者）：
     - 平台包（0.150 起）：包下嵌套的 `node_modules/@openai/codex-darwin-arm64|x64/vendor/<三元组>/bin/codex`，或提升到旁边的 `@openai/codex-darwin-…/vendor/<三元组>/bin/codex`；
     - 包自己的 `vendor/<三元组>/bin/codex`（0.150 起启动脚本的退路），以及旧版的 `vendor/<三元组>/codex/codex`（0.130 及以前）。
     这些布局对照了 npm 上 `@openai/codex` 0.98、0.130、0.150、0.162、0.163 的 `bin/codex.js`，以及 `@openai/codex@0.163.0-darwin-arm64` 包的文件列表（`vendor/aarch64-apple-darwin/bin/codex`）；本机 npm 装的 0.98 是 `vendor/x86_64-apple-darwin/codex/codex`，`Candidates` 找到了它。
  - 只找到启动脚本时报"没有安装"，消息里说 golauncher 运行原生的 `codex`；设置里配置的路径是脚本或 `codex.js` 时，换成它那个安装里的原生程序（没有就换成原生程序应在的路径，运行层报"没有安装"）。
  - 命令行和 Windows 完全一样（只有 `argv[0]` 不同），所以 Codex 没有 macOS 的金样。
- **Gemini CLI**（`gemini.go` 的 `unixCandidates`）：接受 PATH 和上面目录里**任何可执行的 `gemini`**——npm 的链接（指向 `bundle/gemini.js`，`#!/usr/bin/env node`）或 Homebrew 的包装脚本；Gemini CLI 本来就是 Node 程序，用户已接受。没有"只找到别的启动脚本"的报错。
  - 如果 `gemini` 旁边有 `node`（nvm、自带 Node 的安装）而这个目录不在 PATH 上，新的 `Info.PathEnv` 把它放到 PATH 最前面，版本检查和对话进程都用这个 PATH，`#!/usr/bin/env node` 才找得到 Node。
  - 系统设置文件：macOS 上是 `/Library/Application Support/GeminiCli/settings.json`，`system-defaults.json` 在同一目录；Linux 是 `/etc/gemini-cli/`；环境变量 `GEMINI_CLI_SYSTEM_SETTINGS_PATH`、`GEMINI_CLI_SYSTEM_DEFAULTS_PATH` 照旧优先（对照了 npm 上 Gemini CLI 0.64 打包后的 `getSystemSettingsPath`）。
  - 命令行相同；模型名的白名单在所有平台上都一样（只有 Windows 的 `gemini.cmd` 经过 cmd.exe，macOS 上没有 shell 参与）。金样 `gemini/testdata/argv.darwin.golden.json`（`/opt/homebrew/bin/gemini` × 32 种组合，设置文件内容与 Windows 的相同，`GEMINI_CLI_SYSTEM_DEFAULTS_PATH` 不同）。
- **自定义 ACP Agent**（`acp.Custom`）：`filepath.IsAbs` 本来就按本机规则；macOS 上 `~/` 开头的路径展开成主目录。
- **界面**：`error.notInstalled.codex`、`error.version.codex` 有 `@darwin` 变体（不提 `codex.exe`，改说 `brew install --cask codex`、`npm install -g @openai/codex`）；`chat.backendGone`、`error.chatUnavailable` 的 Ctrl+N 在 macOS 上写 ⌘N。`TestDarwinTexts` 检查 AI 插件里提到 `.exe`、`.cmd`、`%APPDATA%`、`Ctrl+` 的文案都有不含这些词的 macOS 变体。
- **测试**：假 CLI 的进程测试（`agentrt`、`acp`、`codex`）原来只在 Windows 上跑，现在 macOS 上也跑（`proc` 的 Unix 实现，进程组）；`TestCheckCandidates` 在 macOS 上用 `codex` 的布局（Homebrew 目录、Codex.app）。只在 Windows 上跑的：Gemini 的 Windows 查找（反斜杠路径）、真实 CLI 的 `TestRealIsolation`（用 cmd.exe 当探针）。
- **没有验证**：真实的 Codex、Gemini 对话（本机 npm 装的 Codex 是 0.98，低于 0.130 的门槛，而且运行时被系统直接杀掉；Node 本身也坏了）；Codex 应用的 CLI 位置；Homebrew 的 `gemini-cli` 包装脚本的样子（只要求可执行）。
- **顺带发现（Windows，没有改）**：新版 npm 包（0.150 起）的原生程序在 `vendor/<三元组>/bin/codex.exe`，而 Windows 的查找只看 `vendor/<三元组>/codex/codex.exe`，所以 Windows 上用 npm 装的新版 Codex 找不到原生程序（Codex 应用装的不受影响）。按"Windows 行为不变"的要求这次没有改，需要单独决定。
