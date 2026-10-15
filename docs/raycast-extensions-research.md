# 调研：golauncher 能否直接集成 Raycast 扩展

> 日期：2026-10-14
> 调研对象：<https://github.com/raycast/extensions>，以及 <https://github.com/raycast/script-commands>
> 方法：浅克隆两个仓库，对全部 3432 个扩展的 `package.json` 和 `src/**/*.{ts,tsx,js,jsx}` 做关键字统计；阅读 Raycast 官方文档和博客；阅读开源兼容实现 Vicinae 的源码。
> 文中标注【未核实】的内容没有找到一手来源。

---

## 1. 结论摘要

1. **不能"直接"集成。** Raycast 扩展是 React + TypeScript 写的 Node.js 程序，没有声明式数据文件可读。npm 上的 `@raycast/api` 只包含类型声明和 `ray` 命令行工具，**不含运行时实现**；运行时由 Raycast 应用自己提供。要运行这些扩展，必须自己实现一整套 `@raycast/api`，包括一个自定义 React reconciler，并内置 Node 运行时。Vicinae（Linux，C++/Qt，GPL-3.0）就是这么做的，而且它自己的文档也承认"很多扩展跑不起来"。
2. **技术上可行，但工作量大。** 架构可以这样搭：Node sidecar 进程，用 worker_threads 隔离各扩展，加上自定义 reconciler，把渲染树序列化成 JSON，通过 JSON-RPC over stdio 与宿主通信。这和 Raycast 自己的方案、我们 §5.5 规划的外部插件协议都同构。但我们现在的 SDK 是"拉取式"（每次输入宿主调一次 `View()`），Raycast 是"推送式"（React 随时重渲染）。所以 §5.5 的协议要先扩展：插件可以主动推送渲染，回调按 ID 调用。另外还要补齐 Form、Grid、导航栈 push/pop、动态 Toast、accessories 等 UI 能力。
3. **能兼容的规模**（静态扫描，偏乐观）：
   - 3432 个扩展中，只有 976 个（28%）声明支持 Windows。
   - 在这 976 个里，不用 AppleScript/macOS 路径、menu-bar、OAuth、AI、浏览器扩展、选中文本、子进程的有 567 个（约 17%）。
   - 如果再只要求 List/Detail（不用 Form/Grid），只剩 274 个（8%）。
4. **法律上有灰色地带。**
   - 代码层面没问题：仓库和每个扩展的 `license` 字段都是 MIT，`@raycast/api` 和 `@raycast/utils` 的 npm 包也是 MIT，可以运行、修改、再分发。
   - 风险在于直接从 Raycast Store 后端下载扩展包：Raycast 服务条款禁止抓取、自动化获取和逆向。另外不能使用 Raycast 商标。
   - 稳妥做法：从 GitHub 源码自行构建，或者让用户自己提供扩展目录。
5. **推荐路线：**
   - 近期（M5）先把我们自己的外部插件协议做对，并做 **方案 C：兼容 Raycast Script Commands**。脚本元数据是纯注释，工作量约 1–2 人周。不过它的价值有限：880 个脚本里约 47% 明显只能在 macOS 上用，`.ps1` 只有 3 个。
   - 中期如果确有需求，再做 **方案 B：精选子集的兼容层**。MVP 约 8–12 人周，之后需要持续维护。
   - **不建议** 做全量兼容（方案 A）。

---

## 2. 仓库概况

| 项目 | 数据 | 来源 |
|------|------|------|
| 仓库 | `raycast/extensions`，即 Raycast Store 中全部扩展的源码 | README |
| 规模 | `extensions/` 下 **3432** 个扩展目录，共 **10900** 个命令（2026-10-15 的 HEAD） | 本地 `git ls-tree` 统计 |
| 活跃度 | 7.8k stars、7.2k forks、约 2.2 万次提交；每周新增 10–30 个扩展 | GitHub 页面；This Week in Raycast |
| 仓库许可 | MIT（Copyright 2021 Raycast） | 仓库根目录 `LICENSE` |
| 扩展许可 | 3432 个扩展的 `package.json` 全部写 `"license": "MIT"`；其中 432 个另带 `LICENSE` 文件 | 本地统计 |
| 结构 | 每个扩展一个目录，包含 `package.json`（npm 包加 Raycast 字段作为 manifest）、`src/<command>.tsx`（每个命令一个入口文件）、`assets/`（图标）、`CHANGELOG.md`、`README.md` | 官方 manifest 文档 |
| 发布流程 | 开发者用 `ray develop` 在本地调试，`ray publish` 生成 PR，Raycast 团队 review 后合并，再由 Raycast 构建并上架 Store。用户在 Raycast 内一键安装预构建的 bundle，不需要本地 Node 工具链 | README / CONTRIBUTING / 开发者文档 |
| 依赖 | 所有扩展都依赖 `@raycast/api`；2402 个依赖 `@raycast/utils`。常见三方库：node-fetch 367、axios 212、date-fns 141、run-applescript 111。原生模块很少：sharp 12、bplist 7、better-sqlite3 5、canvas 3 | 本地统计 |

`@raycast/api` 的 npm 包（2.8.0，MIT，要求 Node ≥ 22.22.2）只包含以下内容：

- `types/index.d.ts`（约 320KB 的类型声明）
- 基于 oclif 的 `ray` CLI（build/develop/lint/publish），用 esbuild 打包，打包目标是 `node22.22.2`
- `dist/index.js` 只导出 CLI 的 `run`

**List、showToast 这些 API 的实现都不在包里。** 扩展打包后，对 `@raycast/api` 的 `require` 由宿主运行时注入；Vicinae 的做法是 patch `Module.prototype.require`。所以兼容层必须**从零实现** API，可以参考的只有 MIT 许可的类型声明和公开文档。`@raycast/utils`（2.3.2，MIT）是真正的运行时库（useFetch、usePromise、useCachedState 等），它建立在 `@raycast/api` 之上，可以原样复用。

---

## 3. Raycast 扩展的技术模型

### 3.1 运行时架构（Raycast 官方博客）

- Raycast（原生 macOS 应用）自带一个托管的 Node 运行时：自动下载，启动前校验完整性。这个 Node 是一个独立进程。
- 每个扩展运行在 Node 进程内独立的 **worker thread** 里，有各自的 JS 引擎实例、事件循环和堆上限。超限的 worker 会被直接终止，界面显示错误页。
- 通信方式是 **JSON-RPC over 标准文件描述符**。扩展只能发送已注册的消息（如 `render`、`setClipboard`），每个命令实例用会话 ID 区分。
- **自定义 React reconciler** 把组件树转成 JSON 渲染树。每次渲染与上一次做 **JSON Patch** diff，渲染树较大时还会 gzip。宿主（Swift）把 patch 应用到轻量视图模型上，再驱动 AppKit 原生控件。
- **不做沙箱**：进程级沙箱成本高，引擎级沙箱限制多。安全依赖开源加人工审核、worker 隔离和堆上限。

### 3.2 API 面（`@raycast/api`）

| 类别 | 内容 |
|------|------|
| UI 组件 | `List`（Section、Item、accessories、`isShowingDetail`、`searchBarAccessory` 下拉、分页、`isLoading`、`onSearchTextChange`/`filtering`/`throttle`）、`Grid`、`Detail`（Markdown + `Detail.Metadata`）、`Form`（TextField、TextArea、PasswordField、Checkbox、Dropdown、DatePicker、TagPicker、FilePicker、Separator、Description，以及校验）、`ActionPanel`（Section、Submenu）、`MenuBarExtra` |
| Action | `Action.CopyToClipboard`、`Paste`、`OpenInBrowser`、`Open`、`OpenWith`、`ShowInFinder`、`Trash`、`Push`、`SubmitForm`、`CreateQuicklink`、`CreateSnippet`、`ToggleQuickLook`、`PickDate`，以及带任意 `onAction` 闭包的 `Action` |
| 命令式 API | `Clipboard`（copy/paste/read/readText/clear）、`LocalStorage`、`Cache`、`environment`、`getPreferenceValues`、`showToast`（Animated/Success/Failure，可就地更新进度）、`showHUD`、`confirmAlert`、`open`、`closeMainWindow`、`popToRoot`、`launchCommand`、`updateCommandMetadata`、`getSelectedText`、`getSelectedFinderItems`、`getApplications`、`getFrontmostApplication`、`showInFinder`、`trash`、`AI.ask`、`OAuth.PKCEClient`、`BrowserExtension`、`WindowManagement`、`Keyboard`、`Icon`/`Color`/`Image` |
| Hooks | `useNavigation`（push/pop）；`@raycast/utils` 提供 `useFetch`、`usePromise`、`useCachedPromise`、`useCachedState`、`useForm`、`useSQL`、`useExec`、`useAI`、`withAccessToken`/`OAuthService`、`runAppleScript`、`showFailureToast`、`getFavicon` 等 |

### 3.3 Manifest（`package.json`）

- **扩展级字段**：`name`、`title`、`description`、`icon`、`author`、`categories`、`platforms`（`"macOS"`/`"Windows"`）、`commands`、`preferences`、`tools`、`ai`、`contributors`、`keywords`、`external`。
- **命令级字段**：`name`（对应 `src/<name>.tsx`）、`title`、`subtitle`、`description`、`mode`、`icon`、`keywords`、`interval`（后台定时运行，最小 1m）、`arguments`（最多 3 个，类型为 text/password/dropdown）、`preferences`、`disabledByDefault`。
- **`mode`** 有三种：`view`、`no-view`、`menu-bar`。
- **preference 类型**：textfield、password、checkbox、dropdown、appPicker、file、directory。

全仓统计结果：

| 维度 | 统计 |
|------|------|
| 命令 mode | view 7315 · no-view 3217 · menu-bar 368（329 个扩展带 menu-bar） |
| 命令参数 | 1019 个命令（596 个扩展）有 `arguments`；类型为 text 1138、dropdown 122、password 11 |
| 后台定时 | 380 个命令带 `interval` |
| AI | 314 个扩展声明 `tools`，211 个声明 `ai` |
| 偏好类型 | textfield 2534、checkbox 2205、dropdown 1668、password 1121、directory 196、appPicker 118、file 66 |

---

## 4. Windows 支持现状

- **Raycast for Windows 的状态**：2025-11-20 开始公开 Beta。官方说"数千个扩展里已有数百个能在 Windows 上运行"，并且只上架经过 Windows 测试的扩展。
  - Raycast 2.0 于 2026-08-21 结束 Beta（来自 This Week in Raycast 周报），但没有确认这是否包括 Windows 版。【未核实】
- **平台声明**：用 manifest 的 `platforms` 字段。官方 changelog 说不写时默认 `["macOS"]`，要上 Windows Store 必须显式加上 `"Windows"`。
- **快捷键**：快捷键可以按平台分别写。只写了 `cmd` 修饰键的快捷键在 Windows 上会被忽略。
- **仓库实际分布**（静态统计）：

| platforms | 扩展数 | 占比 |
|-----------|--------|------|
| 未声明（默认 macOS） | 1239 | 36% |
| `["macOS"]` | 1217 | 35% |
| `["macOS","Windows"]` | 935 | 27% |
| `["Windows"]` | 41 | 1% |
| **含 Windows 合计** | **976** | **28%** |

macOS 依赖信号（扫描 `src/`；第一个数是全部扩展，第二个数是其中声明支持 Windows 的扩展）：

| 信号 | 全部 / 声明 Windows |
|------|--------------------|
| `runAppleScript`/`osascript`/AppleScript | 486 / 58 |
| macOS 路径（`/Applications/`、`~/Library` 等） | 390 / 62 |
| Swift/JXA | 121 / 22 |
| `child_process`/`execa`/`spawn` | 760 / 165 |
| `getSelectedText`/`getSelectedFinderItems` | 396 / 107 |
| 任一 macOS 信号（AppleScript/路径/Swift） | 805 / 117 |

说明：

- 声明支持 Windows 的扩展里也有部分 AppleScript 调用。它们通常用 `process.platform` 分支处理，这是正常的。
- 关键字扫描既会误报（注释、分支代码），也会漏报（动态拼接的命令）。这些数字只能当量级参考。

---

## 5. 已有的兼容实现

| 项目 | 平台 / 技术 | 做法 | 兼容程度 |
|------|-------------|------|----------|
| **Vicinae**（vicinaehq/vicinae，GPL-3.0，约 10.3k★，2026-10 仍活跃） | Linux 为主，C++/Qt；仓库标签含 macOS/Windows，完成度未知 | `src/typescript/extension-manager` 是一个由宿主拉起的 Node 进程，每个命令一个 worker_thread，stdout 走 **protobuf** 消息，stderr 输出日志。自写 `react-reconciler` host config，回调登记成 handler ID。用 patch `Module.prototype.require` 把 `@raycast/api` 重定向到 `raycast-api-compat`；这个包大部分是对自家 `@vicinae/api` 的 re-export，未知符号用 Proxy 返回一个"调用即抛错"的桩。`MenuBarExtra` 未支持；`environment.canAccess(AI)` 返回 false。可以从 Raycast Store 一键下载已构建的扩展 | 官方首页说"对很多扩展有不错的支持，简单扩展效果好"；扩展支持页又说"非常初级，多数扩展会因缺 API 或写死的 macOS 逻辑而无法工作"。HN 上有用户反馈具体扩展报运行时错误 |
| **raycast-linux**（ByteAtATime） | Linux，Tauri/Rust + Svelte | 自写 React renderer，再加一套自定义 API | 作者说明依赖 AppleScript、`/Applications`、Swift 二进制或通讯录/日历权限的扩展都无法运行。AlternativeTo 显示已停止维护 |
| **Flare** | Linux | Raycast 兼容启动器 | 已归档，项目自己推荐改用 Vicinae |
| Sunbeam、Script Kit、Ulauncher | — | 各自有独立的插件协议或 API，**不兼容** Raycast 扩展（根据项目文档的一般认知）【未逐一核实】 | — |

结论：

- 目前**没有** Windows 上的开源 Raycast 扩展运行时可以直接拿来用。
- Vicinae 的架构可以参考，但它是 GPL-3.0，不能把代码复制进我们的项目（除非 golauncher 也采用 GPL）。

---

## 6. 与 golauncher 的差距对照表

我们目前的模型（`pkg/plugin`）：

- 插件返回声明式的 `View{Kind: list|detail, Sections, Detail}`，以及 `Item{Title, Subtitle, Icon, Accessory string, Actions, Detail}`。
- 动作是可序列化的 `ActionDo{Open, CopyText, CopyImage, CopyFiles, Paste, Push, RunCommand, Callback}`。
- `Result{Push, Toast, Hide, PopToRoot, Refresh}`。
- Host 提供 Storage/KV、Preferences、Clipboard、Events、I18n。
- 宿主**每次输入都调用 `View()`**，属于拉取模型。
- 外部插件（§5.5）还没实现。

| Raycast 能力 | golauncher 现状 | 需要的工作 |
|--------------|-----------------|-----------|
| 推送式渲染（React state 变化 → 重渲染 → 推送新树） | 拉取式 `View(ctx, req)` | §5.5 协议新增 `ui/render`（全量树或 JSON Patch）通知；宿主为每个视图维护可变的视图模型。**这是最大的结构性差异** |
| 闭包回调（`onAction`、`onSearchTextChange`、`onSelectionChange`、`onSubmit`） | `ActionCallback` + `Payload` | reconciler 把函数注册成 handler ID，`Action.Do = Callback{Target: handlerID}`；新增宿主到插件的 `searchTextChanged`、`selectionChanged`、`formSubmit` 等事件 |
| `List`（Section/Item） | 有 | 映射即可 |
| `List.Item.accessories`（文本、日期、tag + 颜色、图标、tooltip） | 只有一个 `Accessory string` | 扩展成 `[]Accessory{Text, Date, Tag, Color, Icon, Tooltip}` |
| `isLoading`、分页 `pagination.onLoadMore`、`throttle`、`filtering` | 有 `FilterLocally` | 加入加载态（进度条）和分页触底事件 |
| `searchBarAccessory`（List.Dropdown） | 无 | 搜索栏右侧下拉框控件 |
| `List` 的 `isShowingDetail` + `List.Item.Detail`（Markdown + Metadata） | 有 `ShowDetail` 和 Detail（Markdown/Image/Metadata 只支持 label/value） | Metadata 扩展 Link、TagList、Separator；Markdown 渲染要支持图片（远程 URL 和本地 asset） |
| `Detail` | 有 `ViewDetail` | 加 `navigationTitle`、metadata 侧栏 |
| `Grid` | `ViewKind` 里没有 Grid（§4.3 提到过） | 新增 Grid 布局（columns、aspectRatio、fit、inset、section），MyGo 要做虚拟网格 |
| `Form`（10 种控件 + 校验 + `Action.SubmitForm` + `useForm`） | 未实现（§15 已列为待办） | 新增 Form 视图和各控件；有 1354 个扩展使用 Form，**属于必需** |
| `ActionPanel.Section`、`Submenu`（可以懒加载） | Action 是平铺列表 | 动作面板支持分组和二级菜单 |
| 快捷键 `{modifiers:["cmd","shift"], key}`，可按平台分别写 | `Shortcut string` | 把 `cmd` 映射成 `Ctrl`、`opt` 映射成 `Alt`（Raycast Windows 官方是忽略纯 cmd 快捷键，我们可以选择映射）；处理与内置键的冲突。有 1593 个扩展使用 cmd 修饰键 |
| `useNavigation().push/pop`、`Action.Push` | `ActionPush` 由插件按 view ID 再渲染 | 改成插件端的 React 子树压栈：宿主栈的每一层对应 sidecar 中的一个 root，pop 时通知卸载 |
| `showToast` 返回可变的 Toast（Animated → Success，带 primaryAction） | `Result.Toast` 是一次性的 | 增加 `toast/show|update|hide`、Animated 样式和 Toast 动作。1205 个扩展用了 Animated |
| `showHUD`、`closeMainWindow`、`popToRoot`、`clearSearchBar` | 有 Hide、PopToRoot | 加 HUD（窗口隐藏后的小浮层）和 clearSearchBar |
| `confirmAlert` | 只有 Destructive 确认流程 | 通用的确认对话框 RPC（标题、消息、按钮样式），由插件发起并等待结果 |
| `Clipboard.copy/paste/read` | Host.Clipboard + CopyText/Paste | 基本覆盖；补 `concealed`（不进历史，对应我们的 `Exclude`） |
| `LocalStorage`、`Cache`、`environment.supportPath`、`assetsPath` | Storage KV、DataDir、CacheDir | 映射即可；`Cache` 可以在 sidecar 内用文件实现 |
| `getPreferenceValues` + 7 种 preference | 8 种 PrefType | textfield→text、checkbox→bool、dropdown→select、directory/file→path；**appPicker 缺失**；`required` 偏好未填时要拦截并引导去设置 |
| 命令 `arguments`（最多 3 个，在根搜索内联输入） | `Tab` 进入参数未实现（§15） | 实现根搜索的内联参数输入框（596 个扩展用到） |
| `menu-bar` 命令 | 无 | 映射到托盘菜单，或者直接不支持 |
| `interval` 后台命令 | 无 | 需要调度器和后台 worker，可以排到后面 |
| `launchCommand`、`updateCommandMetadata`、深链 `raycast://` | 无 | 跨命令调用，动态修改命令副标题 |
| `getApplications`、`getFrontmostApplication`、`open(target, app)` | `builtin.apps` 有应用索引 | 暴露成 Host RPC |
| `getSelectedText`、`getSelectedFinderItems` | 无 | Windows 上用 UIA/剪贴板模拟取选中文本，用 Shell COM 取资源管理器选中项；可靠性一般 |
| `OAuth.PKCEClient` / `OAuthService` | 无 | 需要 `golauncher://` 协议回调、token 存凭据管理器；而且很多扩展的 client ID 只登记了 Raycast 的 redirect URI，**即使实现了也可能用不了**（推断） |
| `AI.ask`、`useAI`、tools/ai | 无 | 需要接入 LLM 提供方，暂不支持，`canAccess(AI)=false` |
| `BrowserExtension`、`WindowManagement` | 无 | 不支持 |
| `Icon.*`（数百个内置图标）、`Color`、`Image.Mask`、`tintColor`、深浅色 `source:{light,dark}`、远程 URL 图标 | `Icon{Name, Path}` | 需要一套与 Raycast `Icon` 枚举对应的图标集（自绘或开源图标替换），以及远程图片下载缓存、着色和圆形遮罩 |
| `environment.appearance/theme/textSize/raycastVersion/launchType` | 无 | 由 sidecar 填充 |
| 多实例、崩溃隔离、堆上限 | §5.5 规划了进程级懒启动和回收 | sidecar 内用 worker_threads 的 `resourceLimits` |

**ActionKind 映射**：

| Raycast Action | golauncher ActionKind |
|----------------|-----------------------|
| `CopyToClipboard` | `CopyText`（`content` 是文件或 HTML 时要扩展） |
| `Paste` | `Paste` |
| `OpenInBrowser`、`Open` | `Open` |
| `ShowInFinder` | `Open{Reveal:true}` |
| `Push` | `Push` |
| 自定义 `onAction` | `Callback` |

缺少的 ActionKind：`OpenWith`（指定应用）、`Trash`、`SubmitForm`、`CreateQuicklink`/`CreateSnippet`、`ToggleQuickLook`、`PickDate`。

---

## 7. 可选方案与工作量

人周按 1 名熟悉本项目的工程师估算，量级参考，误差 ±50%。

### 方案 A：全量兼容层

**目标**：尽可能运行 Store 中所有声明支持 Windows 的扩展。

**内容**：

- Node sidecar
- 完整 `@raycast/api` 实现
- Form/Grid/MenuBar（托盘）/arguments/interval/OAuth/AI/HUD/图标集
- 扩展安装、更新和商店界面
- 持续跟进 Raycast API 版本（每月都有变化）

**工作量**：约 6–9 人月起步，加上长期维护。

**能覆盖的上限**：约 976 个 Windows 扩展的 70–85%（推测）。

**结论**：不推荐。投入产出比低，还要长期追着上游走。

### 方案 B：精选子集兼容（推荐的远期选项）

**目标**：只支持"纯 Node + 网络 API + List/Detail/Form/Grid + 基础命令式 API"的扩展，并维护一份白名单（已测试的 N 个扩展）。

| 组件 | 工作量 |
|------|--------|
| §5.5 协议扩展（推送渲染、回调 ID、事件、Toast 更新、确认框） | 1.5–2 人周 |
| sidecar：Node 加载器、`require` 重定向、worker 隔离、自定义 reconciler 和 JSON Patch | 2–3 人周 |
| `@raycast/api` 核心实现（List/Detail/ActionPanel/Action.*/Toast/HUD/Clipboard/LocalStorage/Cache/environment/preferences/open/navigation） | 3–4 人周 |
| 宿主 UI：accessories、动作分组/子菜单、加载态、分页、搜索栏下拉、动态 Toast | 2–3 人周 |
| Form 视图（同时补齐 §15 的待办） | 2–3 人周 |
| Grid 视图 | 1.5–2 人周 |
| 命令 arguments 内联输入（同时补齐 §15 的待办） | 1 人周 |
| 图标集映射、远程图片缓存 | 1 人周 |
| 扩展获取：从 GitHub 源码用 esbuild 构建，或手动导入目录；兼容性测试集 | 1.5–2 人周 |

**MVP（不含 Form/Grid）**：8–12 人周。

**完整子集**：14–20 人周。

**能覆盖的上限**（静态估计）：

- 不用 Form/Grid：约 274 个
- 支持 Form/Grid：约 567 个

两者都只是声明 Windows 且没有明显 macOS、子进程、OAuth、AI 等依赖的扩展，实际能跑通的会少于这个数，需要逐个验证。

### 方案 C：只兼容 Script Commands（推荐的近期选项）

`raycast/script-commands`（MIT）的情况：

- 共 880 个脚本：.sh 602、.applescript 174、.py 39、.swift 19、.js 17、.rb 14、.ps1 3。
- 元数据写在注释里：`@raycast.schemaVersion`、`title`、`mode`（silent 594、compact 96、fullOutput 88、inline 83）、`packageName`、`icon`、`argument1..3`、`refreshTime`、`platform`（macos/windows）。
- 只有 3 个脚本声明 `platform windows`。
- 用关键字估计，约 416 个（47%）明显依赖 macOS（osascript、pbcopy、`/Applications`、AppleScript/Swift 文件）。

**实现**：

- 一个 Go 内置插件，扫描用户指定的脚本目录，解析注释头，生成 `Command`。
- 执行时按 shebang 或扩展名选择解释器：`.ps1` 用 pwsh，`.py` 用 python，`.sh` 用 Git Bash/WSL（可选）。
- 输出按 mode 映射：silent 映射为 Toast/HUD，compact 映射为 Toast（取最后一行），fullOutput 映射为 Detail 视图，inline 映射为根搜索条目，并按 refreshTime 刷新。
- 参数复用 arguments 能力。

**工作量**：1–2 人周，其中参数输入框和方案 B 共享。

**价值**：对 Windows 用户来说，现成能用的 Raycast 脚本不多。它真正的价值是**给用户一个零门槛的"写个 PowerShell/Python 脚本就成为命令"的扩展方式**，并且元数据格式与 Raycast、Vicinae 通用。

### 方案 D：不集成

继续只做我们自己的 Go/JSON-RPC 插件生态，可以参考 Raycast 的 API 设计。成本为 0，但放弃了"迁移现成扩展"这一卖点。

| 方案 | 工作量 | 覆盖 | 风险 | 推荐度 |
|------|--------|------|------|--------|
| A 全量兼容 | 6–9 人月 + 长期维护 | 最高 | 高（API 跟进、macOS 依赖、法律） | ✗ |
| B 精选子集 | MVP 8–12 人周；完整 14–20 人周 | 约 270–570 个（上限） | 中 | △ 远期 |
| C Script Commands | 1–2 人周 | 少量现成脚本，但有通用价值 | 低 | ✓ 近期 |
| D 不集成 | 0 | 0 | 无 | 默认 |

---

## 8. 推荐路线

与里程碑对齐如下。

1. **M5（外部插件）阶段**：设计 JSON-RPC 协议时直接预留方案 B 需要的能力，避免以后返工。
   - 插件可以主动推送 `ui/render`（全量或 JSON Patch），不只是被动的 `view` 请求。
   - 回调 handler ID，以及搜索、选择、表单提交事件。
   - 可更新的 Toast、`confirm` 请求和应答、HUD。
   - 视图栈由插件端 push/pop 驱动。
   - 这样 Go/Python 外部插件和将来的 Raycast sidecar 走同一套协议。
2. **M5 期间，或 M5 之后马上**：
   - 实现 §15 的两个待办：Form 视图和命令参数内联输入。这两项不管做不做 Raycast 兼容都需要。
   - 实现方案 C（Script Commands 兼容），以 `.ps1`/`.py` 为主。
3. **M6 之后（可选）**：做方案 B 的技术验证（spike），大约 2 人周。
   - 用 Node 22 sidecar 跑 3–5 个声明支持 Windows 的纯 List 扩展，例如简单的 API 查询类扩展，验证 reconciler、协议和 UI 映射。
   - 根据结果决定是否投入 MVP。
   - 需要先确定扩展来源（GitHub 源码自建，或用户导入）和 Node 分发方式。
4. **始终**：不使用 "Raycast" 商标做宣传。在设置页写明"兼容 Raycast 扩展 API 的子集"之类的事实性描述，并展示每个扩展自己的许可。

---

## 9. 风险

### 9.1 技术风险

- **API 漂移**：`@raycast/api` 已经到 2.x，并且持续新增能力（AI tools、MCP、skills 等）。兼容层需要长期跟进，Vicinae 的经验也说明缺 API 是最常见的失败原因。
- **Node 运行时分发**：
  - 自带 node.exe 会让安装包增加约 30MB（压缩后，【未核实】精确值），常驻内存还会突破 §11 的 80MB 目标。sidecar 必须懒启动，空闲后回收。
  - 依赖用户自己安装的 Node 会带来版本不可控的问题（Raycast 要求 ≥22）。
  - Bun/Deno 对 `worker_threads`、`require` patch 和原生模块的兼容性需要验证。
  - 少数扩展带原生模块（sharp、better-sqlite3、canvas），需要 Windows 预编译二进制。
- **平台假设**：即使声明了 Windows，扩展仍可能用 `child_process` 调用 Unix 命令（165 个）、写死 `~/Library` 路径，或依赖 `getSelectedText`（Windows 上实现不可靠）。
- **性能**：React 加 JSON 序列化的往返延迟要控制在每次输入 16ms 以内（§11），需要 diff/patch 和节流。
- **UI 保真**：Raycast 的 Markdown 渲染、图标集、Grid 和 accessories 都要在 MyGo 原生 UI 中重新实现，体验很难做到一致。

### 9.2 安全风险

- 扩展是任意 Node 代码，以当前用户权限运行，可以读写文件、访问网络、执行进程。Raycast 自己也不做沙箱，依赖源码审核。我们 §14 已经决定外部插件不签名、不提示，**这个决定对第三方 JS 生态不够**。
- 建议：
  - 首次运行时提示"该扩展可执行任意代码"。
  - 只允许用户主动导入的扩展运行。
  - 在 UI 中显示扩展的来源和许可证。
  - password 偏好继续存放在凭据管理器，只在启动时注入。
  - worker 设置堆上限，并支持超时终止。

### 9.3 法律风险

- **代码许可**：扩展代码（MIT）、`@raycast/api` 类型和 CLI（MIT）、`@raycast/utils`（MIT）都允许使用、修改和再分发，只需保留版权声明。
  - 注意：Vicinae 是 GPL-3.0，不能复制它的代码，只能参考思路。
- **服务条款**：Raycast 服务条款（第 VI 节）禁止对其服务和软件进行复制、修改、逆向，以及数据挖掘、机器人和抓取。
  - 直接调用 Raycast Store 后端下载预构建 bundle（Vicinae 的做法）可能违反条款。【未核实具体接口，也未核实 Raycast 的态度】
  - 我们自己实现 API（依据公开文档和 MIT 类型）不涉及逆向 Raycast 应用，风险较低。
  - 扩展来源建议使用 GitHub 上的 MIT 源码，或者让用户自行导入。
- **商标**：不能使用 Raycast 的名称和 Logo 做产品宣传或命名，只做"兼容某 API"的事实性描述。
- **第三方服务**：很多扩展的 API key、OAuth client 是给 Raycast 申请的。在其他宿主上使用可能违反那些服务的条款，或者根本用不了（例如 redirect URI 只登记了 Raycast）。

---

## 10. 参考链接

- Raycast 扩展仓库：<https://github.com/raycast/extensions>
- Script Commands 仓库：<https://github.com/raycast/script-commands>
- Manifest 文档：<https://developers.raycast.com/information/manifest>
- 术语与 FAQ（自定义 reconciler 的说明）：<https://developers.raycast.com/information/terminology>，<https://developers.raycast.com/misc/faq>
- 开发者 changelog（`platforms` 字段与 Windows 快捷键）：<https://developers.raycast.com/misc/changelog>
- 官方博客《How the Raycast API and extensions work》：<https://raycast.com/blog/how-raycast-api-extensions-work>
- Raycast for Windows 发布：<https://raycast.com/blog/raycast-for-windows>
- Windows 手册：<https://manual.raycast.com/windows/build-your-own-extensions>，<https://manual.raycast.com/windows/extension-store>
- Windows Beta 报道：<https://alternativeto.net/news/2025/11/raycast-for-windows-launches-public-beta-with-native-search-and-extension-support/>
- This Week in Raycast 周报存档：<https://buttondown.com/thisweekinraycast/archive/>
- Raycast 服务条款：<https://www.raycast.com/terms-of-service>
- Raycast 扩展审核指南：<https://manual.raycast.com/extensions-guidelines>
- npm `@raycast/api`（2.8.0，MIT）：<https://registry.npmjs.org/@raycast/api/latest>；`@raycast/utils`（2.3.2，MIT）：<https://registry.npmjs.org/@raycast/utils/latest>
- Vicinae：<https://github.com/vicinaehq/vicinae>
  - 文档：<https://docs.vicinae.com/>，<https://docs.vicinae.com/extensions/raycast>
  - 关键源码：`src/typescript/extension-manager/src/{patch-require.ts,reconciler.ts,worker.tsx}`、`src/typescript/raycast-api-compat/src/{index.ts,unsupported.ts}`
- raycast-linux：<https://git.joshthomas.dev/mirrors/raycast-linux>；HN 讨论：<https://hn.svelte.dev/item/44551762>
- Vicinae 的 HN 讨论：<https://brianlovin.com/hn/45188116>

### 附：统计方法

- 对 `raycast/extensions` 做 `git clone --filter=blob:none --depth 1`，用稀疏检出拿到 `extensions/*/package.json` 和 `extensions/*/src/**/*.{ts,tsx,js,jsx}`（HEAD 时间 2026-10-15T04:13Z）。用 Python 统计 manifest 字段，再对源码做正则匹配。
- 对 `raycast/script-commands` 浅克隆，按扩展名和元数据注释统计。
- 正则匹配只能给出量级，会有误报和漏报。
