# 主题设计方案

> 状态：设计 v1 · 已确认（§12），一、二期已实现（待合并，实现记录见 §13，主题编写指南见 §14）· 2026-10-16 · 对应需求 R50、R51（见 [requirements.md](requirements.md)）

## 1. 现状

- **配置**：配置里只有一个 `theme` 字段，取值 `system`、`light`、`dark`（`internal/config/config.go`）。
- **怎么生效**：`internal/shell/shell.go` 把它交给 MyGo 的 `mygo.Theme.SetSource`；启动器每次绘制时，`internal/ui/launcher.go` 的 `applyTheme` 在 MyGo 自带的 `mui.LightTheme()` 和 `mui.DarkTheme()` 之间切换。
- **背景**：窗口用 Acrylic 材质（`mygo.VibrancyAcrylic`）。有材质时，根背景是完全透明的，看到的就是系统的 Acrylic；没有材质时，是主题的 `Background` 纯色。
- **写死的颜色**：少数几个，例如动作面板的阴影、确认框的遮罩 `RGBA(0,0,0,0.35)`、Toast 的透明度。
- **MyGo 已经提供的**（v0.5.1）：
  - `mui.Theme` 有完整的颜色和尺寸字段：背景、表面、边框、文字、次要文字、强调色及其悬停和按下态、危险、警告、成功、选中、焦点、反色、滚动条，以及圆角、间距、字号、字体；
  - `Window.SetVibrancy` 可以在运行时切换 Mica、Acrylic 或无材质；
  - `Window.SetOpacity` 设置整个窗口的不透明度。

所以要做的不是从零开始，而是：把启动器自己的颜色也收进"主题"，提供一套内置主题，再把材质和透明度做成可调的。

## 2. 目标

1. **一套可选的主题**：内置十几个，浅色、深色都有，设置里能直接预览、一键切换。
2. **可调透明度**：拖动滑块，即时看到效果。
3. **跟随系统**：可以分别指定"浅色模式用哪个、深色模式用哪个"。
4. **能自己做主题**：一个 JSON 文件，可以只改几个颜色；支持导入、导出、热加载。
5. **不牺牲可读性**：每个主题都要过文字对比度检查；系统关闭透明效果或开启高对比度时，自动退回不透明。

不做：插件自定义颜色（插件仍然只能用 `IconTint` 这类语义色，由主题决定实际颜色）、背景图片、动画主题。

## 3. 主题由什么组成

一个主题 = **调色板** + **启动器专用的颜色** + **外观参数**。

### 3.1 调色板（直接对应 `mui.Theme`）

`background`、`surface`、`surfaceHover`、`surfacePressed`、`border`、`text`、`textMuted`、`accent`、`accentHover`、`accentPressed`、`accentText`、`danger`、`warning`、`success`、`selection`、`focus`、`inverse`、`inverseText`、`scrollbar`。

- `accentHover`、`accentPressed`、`surfaceHover`、`surfacePressed` 可以不写，按明暗自动推导（变亮或变暗 6%、12%）。主题作者通常只需要写七八个颜色。

### 3.2 启动器专用的颜色（新增）

| 名称 | 用在哪 | 现在 |
|------|--------|------|
| `rowSelected` / `rowHover` | 结果列表的选中行、悬停行 | 用 `surface` 系列 |
| `searchText` / `searchPlaceholder` | 搜索框 | 用 `text` / `textMuted` |
| `sectionHeader` | 分组标题（Applications、System……） | `textMuted` |
| `divider` | 搜索框下、页脚上的分隔线 | `border` |
| `footer` | 页脚背景（在材质上叠一层） | 透明 |
| `overlay` | 确认框后面的遮罩 | 写死 `RGBA(0,0,0,0.35)` |
| `shadow` | 动作面板、确认框的阴影 | 写死 |
| `tint.<名称>` | 插件图标的语义色（红、橙、黄、绿、蓝、紫、粉……） | 写死在 `icons.go` |

缺省时都有合理的推导值，所以旧主题、简单主题照样能用。

### 3.3 外观参数

| 参数 | 含义 | 默认 |
|------|------|------|
| `material` | 背景材质：`acrylic`（模糊背后的内容）、`mica`（取桌面壁纸的色调，不模糊）、`none`（纯色） | `acrylic` |
| `opacity` | **背景不透明度**，0–100%：材质上面盖一层主题背景色的比例。0% 是纯 Acrylic，100% 是纯色 | 由主题给出，比如深色 70% |
| `radius` | 圆角 | 6 |
| `density` | 紧凑 / 标准 / 宽松（对应 `Spacing` 3 / 4 / 5） | 标准 |
| `fontSize` | 字号 | 系统默认 |

## 4. 透明度怎么做

有两种"透明"，效果差别很大：

| 做法 | 效果 | 结论 |
|------|------|------|
| **背景色叠在材质上**：根背景用主题背景色，alpha = `opacity` | 只有背景变透，文字、图标始终是实心的，清晰 | **主滑块用这个** |
| **整个窗口半透明**（`Window.SetOpacity`） | 文字、图标也一起变淡，背后的内容透出来，可读性差 | 只作为"高级"选项（窗口整体不透明度 70–100%），默认 100% |

细节：

- **选中行、悬停行、页脚**等叠加色也按同一比例跟着变，否则背景透了、选中行还是实心的会很突兀。具体做法：这些颜色在主题里按"不透明时的样子"定义，绘制时乘以一个系数。
- **滑块即时生效**：拖动时直接改启动器窗口的背景（启动器在设置窗口旁边打开预览），松手后保存。
- **退回不透明的情况**：
  - Windows 设置里关闭了"透明效果"（注册表 `EnableTransparency = 0`）；
  - 开启了高对比度；
  - 系统不支持材质（Windows 10 不支持 Mica，部分 Acrylic 也不行）；
  - 节电模式下系统自己关了材质。

  这时用 100% 不透明的背景，设置里的滑块显示为禁用，并说明原因。监听系统设置变化，即时切换。
- **最低可读性保护**：`opacity` 低于 40% 时，文字加一层很淡的同色阴影（可关），防止浅色壁纸上的浅色文字看不清。

## 5. 内置主题

调色板都来自公开的配色方案（多为 MIT 许可），在 `docs/` 和"关于"里注明来源。

| 主题 | 明暗 | 说明 |
|------|------|------|
| **golauncher Light / Dark** | 浅 / 深 | 默认，就是现在的样子（MyGo 默认色），保持兼容 |
| Raycast 风格 Light / Dark | 浅 / 深 | 中性灰，红色强调 |
| GitHub Light / Dark | 浅 / 深 | |
| Nord | 深 | 冷色调 |
| Dracula | 深 | 紫色调 |
| Catppuccin Latte / Mocha | 浅 / 深 | 柔和 |
| Tokyo Night | 深 | |
| Solarized Light / Dark | 浅 / 深 | |
| Gruvbox Dark | 深 | 暖色调 |
| One Dark | 深 | |
| 宣纸（Paper） | 浅 | 米白底、墨色字、朱红强调，偏中文排版风格 |
| 高对比度 Light / Dark | 浅 / 深 | 不透明，对比度 ≥ 7:1 |

- **"跟随系统"**：选"跟随系统"时，再分别选浅色主题和深色主题，默认是 golauncher Light / Dark。
- **强调色**：每个主题自带强调色；可以选"使用 Windows 强调色"，从系统设置读取，并随系统变化。

## 6. 自定义主题

### 6.1 文件格式

放在 `%APPDATA%\golauncher\themes\*.json`：

```json
{
  "$schema": "https://raw.githubusercontent.com/eziq1/golauncher/main/docs/theme.schema.json",
  "name": "Midnight Teal",
  "name.zh-CN": "午夜青",
  "author": "someone",
  "extends": "golauncher-dark",
  "colors": {
    "background": "#0f1a1f",
    "accent": "#2dd4bf",
    "rowSelected": "#1e3a40"
  },
  "material": "acrylic",
  "opacity": 0.65
}
```

- `extends` 指定基于哪个主题（内置或自定义），只写要改的字段。
- 颜色支持 `#rgb`、`#rrggbb`、`#rrggbbaa`、`rgba(...)`。
- 提供 JSON Schema，编辑器里有补全和校验。

### 6.2 加载和校验

- 启动时加载；文件变化时**热加载**（监听目录），编辑主题文件时启动器即时变化，适合调色。
- **校验**：
  - 未知字段给警告；
  - 颜色格式错误时，该字段退回 `extends` 主题的值；
  - 循环 `extends` 报错。
- **对比度检查**：`text`、`searchText` 对背景，`accentText` 对 `accent`，选中行上的文字对 `rowSelected`，都按 WCAG 计算。半透明背景按最坏的情况（下面是纯白或纯黑）合成后再算。低于 4.5:1 时在设置里给出警告，但仍然可以用。
- 出错的主题在设置里标红，并显示原因；正在用的主题出错时，退回它的 `extends` 或默认主题，不会让界面无法使用。

### 6.3 导入、导出和分享

- **导出**：把当前主题（包括你在设置里调的透明度、强调色）导出为一个 JSON 文件。
- **导入**：选文件，或者把 JSON 粘贴进来。
- **"复制为主题"**：基于任意内置主题生成一个可编辑的副本，并打开文件夹。
- 以后可以考虑导入 Raycast 的主题（它的主题是一组颜色，可以转换），先不做。

## 7. 设置界面

设置 › 外观（把现在通用页里的"主题"移过来，单独成一页）：

1. **主题画廊**：每个主题一张缩略图。缩略图用真实的启动器组件画出来（搜索框、三行结果、选中行、页脚），不是图片。单击即应用，悬停时显示名称和作者。
2. **跟随系统**：开关；打开后显示"浅色时"和"深色时"两个下拉框。
3. **背景**：材质（Acrylic / Mica / 纯色）和背景不透明度滑块。系统不支持或关闭了透明效果时禁用，并说明原因。
4. **强调色**：使用主题的强调色 / 使用 Windows 强调色 / 自定义颜色。
5. **布局**：圆角、密度、字号。
6. **高级**：窗口整体不透明度（默认 100%）、低透明度时给文字加阴影。
7. **自定义主题**：打开主题文件夹、导入、导出、复制为主题，以及自定义主题的错误列表。

调整任何一项时，启动器在旁边打开预览，并保持显示，方便对比。

## 8. 实现

### 8.1 代码结构

- **新包 `internal/theme`**：
  - `Theme`：调色板、启动器颜色、外观参数，可以从 JSON 读写；
  - `Builtin()`：内置主题，用 `embed` 嵌入 JSON；
  - `Load(dir)`：读自定义主题，处理 `extends`、校验、对比度检查；
  - `Resolve(cfg, systemDark, systemAccent, transparencyOn)`：根据配置和系统状态，算出这一刻要用的 `*mui.Theme` 和启动器颜色；
  - `Watch(dir)`：热加载。
- **`internal/ui`**：
  - `applyTheme` 改为使用 `Resolve` 的结果；
  - 启动器里写死的颜色（遮罩、阴影、Toast、图标色）都改为读启动器颜色；
  - 设置窗口新增外观页和缩略图组件。
- **`internal/shell`**：
  - 材质变化时调用 `Window.SetVibrancy`，整体不透明度变化时调用 `Window.SetOpacity`；
  - 监听系统的明暗、强调色、透明效果、高对比度变化，变化时重新 `Resolve`。
- **`glctl`**：
  - `glctl theme list` / `show <id>` / `validate <file>` / `export <id>`；
  - `validate` 输出校验结果和对比度，方便 Agent 和主题作者检查。

### 8.2 配置和迁移

```json
"appearance": {
  "theme": "golauncher-dark",
  "followSystem": true,
  "lightTheme": "golauncher-light",
  "darkTheme": "golauncher-dark",
  "material": "acrylic",
  "opacity": null,
  "accent": "theme",
  "radius": null,
  "density": "standard",
  "fontSize": null,
  "windowOpacity": 1
}
```

- `null` 表示"用主题自己的值"。所以换主题时，主题自带的透明度会生效；你手动调过的则一直保留，直到点"恢复主题默认"。
- **迁移**：旧配置的 `theme` 字段转换为：
  - `system` → `followSystem: true`；
  - `light` → `followSystem: false`，主题为 golauncher Light；
  - `dark` → `followSystem: false`，主题为 golauncher Dark。

  按配置版本号做一次迁移（`docs/storage.md` 的迁移机制），旧版本读到新配置时忽略未知字段。

### 8.3 性能

- `Resolve` 只在配置、系统设置或主题文件变化时运行，结果缓存；绘制时只是读取缓存，不在每一帧计算。
- 缩略图画廊用同一套组件小尺寸绘制，十几个主题一次画完，不需要额外的图片资源。

## 9. 测试

- **每个内置主题**：
  - JSON 能解析；
  - 所有必填颜色都有值；
  - 对比度达标（文字 ≥ 4.5:1，高对比度主题 ≥ 7:1），半透明时按最坏情况计算。

  这条测试保证以后新增的主题也不会看不清。
- **`extends`**：继承、覆盖、循环、缺失的基主题。
- **解析颜色**：各种格式和错误格式。
- **`Resolve`**：
  - 跟随系统时的明暗切换；
  - Windows 强调色；
  - 关闭透明效果、高对比度时退回不透明；
  - `null` 和手动值的优先级。
- **配置迁移**：旧的三种取值。
- **热加载**：写文件 → 收到更新；写坏的文件 → 保持上一个有效版本并报错。
- **界面**：`ui.NewTester` 检查背景透明度、选中行的颜色确实来自主题；设置页的滑块和画廊。
- **glctl**：`theme list`、`validate` 的 golden。
- 不读写本机真实的主题文件夹和系统设置；系统状态在测试里注入假的值。

## 10. 分期

| 阶段 | 内容 |
|------|------|
| **一** | `internal/theme`、启动器颜色收进主题、内置主题、跟随系统的浅色/深色选择、材质和背景不透明度滑块、退回不透明的规则、外观设置页（画廊、滑块）、配置迁移、对比度测试 |
| **二** | 自定义主题 JSON（`extends`、校验、热加载、JSON Schema）、导入导出、复制为主题、Windows 强调色、窗口整体不透明度、圆角/密度/字号、`glctl theme` |
| **三**（以后） | 导入 Raycast 主题、主题分享、更多内置主题 |

第一期就能满足"有一套可选的主题，还可以设置透明度"。

## 11. 需要你决定

1. 内置主题的清单（§5）是否合适？有没有想要的、不想要的？
2. 透明度的主滑块按"只有背景变透、文字保持清晰"来做（§4），"整个窗口半透明"只作为高级选项。可以吗？
3. 外观单独成一页，可以吗？
4. 先做第一期，还是一、二期一起做？

## 12. 决定（2026-10-15，R51）

1. 内置主题清单（§5）同意。
2. 透明度：主滑块只让背景变透；整窗半透明放在高级选项里（§4）。
3. 外观单独成一页（§7）。
4. **第一期和第二期一起做**。第三期（导入 Raycast 主题、主题分享）以后再说。

## 13. 实现记录（2026-10-16，R51）

一期和二期全部完成。三期（导入 Raycast 主题、主题分享）没有做。

### 13.1 代码

| 位置 | 内容 |
|------|------|
| `internal/theme` | `File`（文件格式）、`Theme`（应用了 `extends` 之后的主题）、颜色表和推导规则（`theme.go`）；颜色解析和 WCAG 计算（`color.go`、`contrast.go`）；内置主题（`builtin/*.json`，`embed`）和用户主题、热加载、`Validate`（`library.go`）；`Resolve` 和带缓存的 `Manager`（`resolve.go`）；导出（`export.go`）；JSON Schema 生成（`schema.go`） |
| `docs/theme.schema.json` | 由 `theme.Schema()` 生成，`TestSchema` 保证与代码一致（`go test ./internal/theme -run TestSchema -update` 重新生成） |
| `internal/config` | `Appearance`（§8.2）、迁移、`LegacyTheme` |
| `internal/app` | `Env.Themes`（`theme.Manager`），用户主题目录默认 `%APPDATA%\golauncher\themes`，`Options.ThemesDir` 可以改，`Fake` 时没有 |
| `internal/ui` | 启动器每帧 `Resolve`（命中缓存）后用主题画；`appearance.go` 是外观设置页、画廊缩略图、自定义主题 |
| `internal/shell` | `appearance.go`：MyGo 的明暗模式、材质（`SetVibrancy`）、整窗不透明度（`SetOpacity`）、监听系统设置（`Theme.OnUpdated`）、轮询主题文件夹、在设置旁边显示启动器预览 |
| `internal/cli/theme.go` | `glctl theme list | show | validate | export`，golden 在 `internal/cli/testdata/golden/theme_*.json` |

### 13.2 与设计不同的地方、补充的决定

1. **配置版本号不变**（仍是 1）。`appearance` 只是新增字段：旧版本读到它会忽略；如果把版本号加到 2，旧版本反而会把整个配置当成"更新的版本"移到 `config.json.bad`，和"旧版本读到新配置时忽略未知字段"矛盾。迁移仍在 `config.migrate()` 里：文件里没有 `appearance` 时按旧的 `theme` 字段转换。`theme` 字段继续写出（`system`/`light`/`dark`，按当前外观推算），旧版本仍能保持明暗。见 [storage.md §2.2](storage.md)。
2. **`material`、`density` 也可以是 `null`**（§8.2 的例子里是具体值）。`null` 一律表示"用主题的值"，"恢复主题默认"把 `material`、`opacity`、`accent`、`radius`、`density`、`fontSize` 都还原。新增 `textShadow`（默认 `true`）。
3. **主题文件多了几个字段**：`dark`（明暗，可继承）、`highContrast`（按 7:1 检查，并且总是不透明）、`systemAccent`（配置选"主题的强调色"时用 Windows 强调色）、`source`、`license`、`name.<语言>`。名字和作者不继承：没有名字的主题显示它的 ID（文件名）。
4. **golauncher Light / Dark 跟随 Windows 强调色**（`systemAccent: true`）：以前 MyGo 的默认主题就是这样，所以默认外观完全不变。其他内置主题用自己的强调色，除非在设置里选"Windows"。
5. **新增的启动器颜色**：`rowSelectedText`（选中行上的文字，对比度检查要用）、`control` / `controlHover`（图标底色、键帽、小按钮）。`tint.<名称>` 对应 SDK 里现有的四种语义色 `success`、`danger`、`accent`、`muted`；其他名字的 tint 也会保留，留给以后的 SDK。
6. **选中行默认是强调色**（`accent` 90% 不透明，文字用 `accentText`）。§3.2 的表里写"现在用 surface 系列"不准确，实际一直是强调色；golauncher 主题保持不变。其他内置主题大多用低调的选中行（`rowSelected` 是一个接近背景的颜色，文字是正文色），这时图标保留各自的语义色。
7. **推导规则**：省略的颜色从它的"基础色"推导（如 `accentHover` ← `accent`、`divider` ← `border`）。继承时，如果子主题改了基础色、没改派生色，派生色重新推导（改了强调色，悬停色也跟着变）。强调色的派生（悬停、按下、选中文字、焦点环、强调色上的文字）沿用 MyGo 跟随系统强调色时的公式，这样默认主题在 Windows 强调色下与以前一模一样；`surfaceHover` / `surfacePressed` 按设计变亮或变暗 6% / 12%。配置里指定的强调色（Windows 或自定义）替换调色板里的强调色系列，但主题自己写明的启动器颜色（比如自己的选中行颜色）保留。
8. **叠加色的比例**：选中行、悬停行、页脚在主题自己的不透明度下就是写的样子；用户把背景不透明度调成 `o` 时，它们的 alpha 乘以 `k = (0.5 + 0.5·o) / (0.5 + 0.5·o主题)`（不超过 1）；背景不透明时 `k = 1`。这样 golauncher（`o主题 = 0`）在默认设置下与以前一样，其他主题背景变透时选中行也一起变淡，但不会消失。
9. **对比度的"最坏情况"**：只按"背景色叠在纯白或纯黑上"算的话，背景不透明度低于一半左右时所有主题都不及格（golauncher 是 0%）。实际上 Windows 的 Acrylic / Mica 自己有一层不透明度很高的底色（亮度层），所以模型是：纯白或纯黑 → 材质底色（浅色 `#f3f3f3`、深色 `#202020`，80% 不透明，比 Windows 实际的更透，偏保守）→ 主题背景色（按背景不透明度）→ 文字。检查五项：`text`、`searchText` 对背景（≥ 4.5:1，高对比度主题 ≥ 7:1），`textMuted` 对背景（≥ 3:1，高对比度 ≥ 4.5:1，这一项是新增的），`accentText` 对 `accent`，`rowSelectedText` 对 `rowSelected`（≥ 4.5:1 / 7:1）。
10. **golauncher Light / Dark 的已知例外**：为了"默认外观完全不变"，它们沿用 MyGo 的颜色，有几项达不到 4.5:1：Dark 的白字对 `#3b82f6` 是 3.67:1，选中行 3.98:1；Light 的选中行 4.42:1，次要文字在 0% 不透明度、背后是黑色时 2.71:1。测试里用 `legacyContrast` 白名单只放过这几项；其他内置主题、以后新增的主题必须全部通过。是否调整默认主题的颜色见 §13.6。
11. **设置页的对比度警告只提示"调整造成的"问题**（当前不透明度、强调色下不及格，而主题自己的默认值是及格的）；主题本身就不及格的情况，用户主题在"主题问题"列表里列出，内置主题就是上面的已知例外。否则默认主题一打开设置就有警告。
12. **文字阴影**：MyGo v0.5.1 没有文字阴影。实现为"文字衬底"：背景不透明度低于 40% 时，结果行、分组标题、页脚文字下面加一层背景色 35% 的 `TextBackground`。设置里叫"文字衬底"（Backdrop under text），可以关。
13. **搜索框占位文字的颜色**：MyGo 用整帧主题的 `TextMuted` 画占位文字。主题的 `searchPlaceholder` 和 `textMuted` 不同时，启动器不用输入框自带的占位文字，而是在输入框上叠一段文字（`AttachTo`，不接收鼠标）。
14. **热加载用轮询**：每 500 ms 列一次主题文件夹（文件名、大小、修改时间）。MyGo 的 `WatchFiles` 要在 App 运行时才能用，主题包要能单独测试；文件夹里只有几个小文件，一次轮询就是一次 `ReadDir`。写坏的文件保留上一个可用的版本，并在设置的"主题问题"里显示原因；继承它的主题也基于上一个可用的版本。
15. **设置窗口本身不套主题**，只跟着主题的明暗（MyGo 的浅色或深色），避免设置页因为主题配色看不清。
16. **预览**：拖动滑块时用 `Manager.SetPreview` 临时替换外观，启动器用 `ShowInactive` 显示在设置窗口旁边（右边放不下就放左边），不抢焦点；松开鼠标才写配置。键盘调整每一步直接保存。关闭设置时，预览中的启动器一起隐藏。
17. **主题 ID 就是文件名**（不含 `.json`）。和内置主题同名的文件报错。导入、复制时自动避开重名（加 `-2`、`-3`）。
18. **导出**：导出的是不带 `extends` 的完整主题文件，只写主题自己设定的颜色（派生色留给加载时重新推导，改了强调色，悬停色也会跟着变）；"导出当前主题"还带上当前的材质、不透明度、圆角、密度、字号和强调色。
19. **跟随系统时**，"浅色模式时"只列浅色主题，"深色模式时"只列深色主题；画廊里点浅色主题设为浅色模式的主题，点深色主题设为深色模式的主题。这样 MyGo 的明暗（标题栏、材质的明暗）总是和主题一致。
20. **Raycast 风格**是 golauncher 自己的中性灰配色，只是风格类似，名称写作 "Raycast-style"，与 Raycast 无关。

### 13.3 内置主题的来源

颜色值来自下列公开配色方案（都是 MIT 许可），每个主题文件的 `source`、`license` 字段也写明了。为了通过对比度检查，少数颜色做了调整：Nord、Dracula、Tokyo Night、One Dark 的次要文字取原方案中两个颜色之间的值；Solarized Light 的正文用 base02（原方案是 base00，对比度不够）；Solarized 的强调色上的文字用比 base03 更深的颜色。

| 主题 | 来源 |
|------|------|
| golauncher Light / Dark | MyGo 默认主题（github.com/egoist/mygo） |
| Raycast-style Light / Dark | golauncher 自己的配色 |
| GitHub Light / Dark | GitHub Primer primitives（github.com/primer/primitives） |
| Nord | Arctic Ice Studio（nordtheme.com） |
| Dracula | Zeno Rocha（draculatheme.com） |
| Catppuccin Latte / Mocha | catppuccin.com |
| Tokyo Night | enkia（github.com/enkia/tokyo-night-vscode-theme） |
| Solarized Light / Dark | Ethan Schoonover（ethanschoonover.com/solarized） |
| Gruvbox Dark | Pavel Pertsev（github.com/morhetz/gruvbox） |
| One Dark | Atom（github.com/atom/atom） |
| 宣纸 / Paper、高对比度 Light / Dark | golauncher 自己的配色 |

### 13.4 性能

在一台桌面开发机上测得（`go test -bench`）：

| 项目 | 结果 |
|------|------|
| `Resolve`（不走缓存，Nord + Windows 强调色） | 9.0 µs，36 次分配 |
| `Manager.Resolve`（命中缓存，启动器每帧调用一次） | 34 ns，0 次分配 |
| 画廊 18 个主题的解析 | 约 0.16 ms，只在主题、系统设置或语言变化时做一次 |
| 外观设置页整帧（无头渲染，900×2400，含 18 个缩略图） | 0.44 ms |

默认主题下，启动器的 6 张快照（英文、伪本地化、简体中文 × 浅色、深色）与改动前逐像素比较，完全相同。

### 13.5 测试

- `internal/theme`：每个内置主题能解析、无警告、所有颜色齐全、对比度达标（`TestBuiltinThemes`，以后新增的主题自动受检）；golauncher 主题等于 MyGo 的默认主题；颜色格式；`extends` 的继承、覆盖、派生色重新推导、循环、缺失的基主题、缺少必填颜色、与内置主题重名、错误类型、各种警告；热加载（写入、写坏后保留上一个版本、修好、删除）；`Resolve`（跟随系统、Windows 强调色、自定义强调色、关闭透明效果 / 高对比度 / 不支持材质 / 纯色时不透明、`null` 和手动值的优先级、恢复主题默认）；`Manager` 的缓存和预览；导出后再加载不变；JSON Schema 与代码一致。
- `internal/config`：旧 `theme` 三种取值的迁移；部分 `appearance` 的补全和范围限制；`null` 的往返；`Clone`；`LegacyTheme`。
- `internal/ui`（`ui.NewTester`，系统状态全部注入）：背景颜色和不透明度、选中行颜色来自主题（不透明、半透明、30%），关闭透明效果和高对比度时变不透明；跟随系统切换主题；Windows 强调色；图标 tint；设置页的画廊点击、跟随系统开关、滑块按下预览 / 松开保存、禁用原因、恢复默认、密度、强调色；粘贴 JSON 导入、文件导入、坏文件、导出、复制为主题、主题问题列表；主题文件热加载后启动器重画；中文界面布局。
- `internal/cli`：`glctl theme list`、`show`、`export`、`validate`（正常、有警告、出错、文本格式）的 golden。
- 测试从不读写本机的主题文件夹和系统设置。

### 13.6 未决问题

1. golauncher Light / Dark 有几项对比度低于 4.5:1（§13.2 第 10 条）。要不要把默认主题的强调色调深一点（比如 Dark 用 `#2563eb`）？这会改变现在的外观。
2. 对比度模型里材质底色的不透明度（80%）是保守估计，不是从 Windows 读出来的。
3. MyGo 以后如果支持文字阴影、占位文字颜色、文件监视不依赖 App，可以替换 §13.2 第 12、13、14 条的做法。

## 14. 主题编写指南

### 14.1 放在哪里

主题是 `%APPDATA%\golauncher\themes` 里的 JSON 文件，文件名（不含 `.json`）就是主题的 ID。保存后启动器立即变化（半秒内），不用重启。设置 › 外观 › 自定义主题里可以打开这个文件夹，也可以导入文件、粘贴 JSON、把当前主题导出为文件，或者把任意主题"复制为主题"作为起点。

### 14.2 最小的主题

只写和基础主题不同的地方：

```json
{
  "$schema": "https://raw.githubusercontent.com/eziq1/golauncher/main/docs/theme.schema.json",
  "name": "Midnight Teal",
  "name.zh-CN": "午夜青",
  "author": "you",
  "extends": "golauncher-dark",
  "colors": {
    "background": "#0f1a1f",
    "accent": "#2dd4bf",
    "accentText": "#0f1a1f"
  },
  "opacity": 0.7
}
```

`$schema` 让 VS Code 等编辑器提供补全和校验。

### 14.3 字段

| 字段 | 含义 |
|------|------|
| `name`、`name.<语言>` | 设置里显示的名字，如 `name.zh-CN` |
| `author`、`source`、`license` | 作者、配色来源、许可 |
| `extends` | 基于哪个主题：内置主题的 ID（`glctl theme list` 列出），或另一个主题文件的文件名 |
| `dark` | 是否深色主题（继承）。跟随系统时，深色主题只出现在"深色模式时" |
| `highContrast` | 高对比度主题：按 7:1 检查，总是不透明 |
| `systemAccent` | 设置里选"主题的强调色"时，使用 Windows 强调色 |
| `colors` | 颜色，见下 |
| `material` | `acrylic`（模糊背后的内容）、`mica`（取壁纸色调）、`none`（纯色） |
| `opacity` | 背景不透明度 0–1：0 只显示材质，1 是纯色 |
| `radius` | 圆角 0–16，默认 6 |
| `density` | `compact`、`standard`、`comfortable` |
| `fontSize` | 字号 10–24，省略为系统默认 |

没有 `extends` 的主题必须写全这些颜色：`background`、`surface`、`border`、`text`、`textMuted`、`accent`、`danger`、`warning`、`success`。

### 14.4 颜色

格式：`#rgb`、`#rgba`、`#rrggbb`、`#rrggbbaa`、`rgb(r, g, b)`、`rgba(r, g, b, a)`（`a` 是 0–1，也可以写百分比）、`transparent`。格式写错的颜色会被忽略（用基础主题的值），并给出警告。

| 颜色 | 用在哪 | 省略时 |
|------|--------|--------|
| `background` | 窗口背景，按 `opacity` 叠在材质上 | 必填 |
| `surface`、`surfaceHover`、`surfacePressed` | 按钮、输入框等控件 | 悬停 / 按下：变亮或变暗 6% / 12% |
| `border` | 控件边框 | 必填 |
| `text`、`textMuted` | 正文、次要文字和占位文字 | 必填 |
| `accent`、`accentHover`、`accentPressed`、`accentText` | 强调色、它的悬停 / 按下色、上面的文字 | 从 `accent` 推导；文字在浅色强调色上为黑色，否则为白色 |
| `danger`、`warning`、`success` | 危险、警告、成功 | 必填 |
| `selection`、`focus` | 选中文字、焦点环 | 从 `accent` 推导 |
| `inverse`、`inverseText` | 提示框 | `text`、`background` |
| `scrollbar` | 滚动条 | 半透明黑或白 |
| `rowSelected`、`rowSelectedText` | 选中的结果行和它上面的文字 | `accent` 90%、`accentText` |
| `rowHover` | 鼠标下的结果行 | `text` 5% |
| `searchText`、`searchPlaceholder` | 搜索框的文字、占位文字 | `text`、`textMuted` |
| `sectionHeader` | 分组标题 | `textMuted` |
| `divider` | 分隔线 | `border` |
| `footer` | 页脚背景 | 透明 |
| `overlay` | 确认框后面的遮罩 | 黑色 35% |
| `shadow` | 动作面板、确认框的阴影 | 黑色 25% |
| `control`、`controlHover` | 图标底色、键帽、小按钮 | `text` 8% / 14% |
| `tint.success`、`tint.danger`、`tint.accent`、`tint.muted` | 插件图标的语义色 | `success`、`danger`、`accent`、`textMuted` |

继承时，如果改了基础色（比如 `accent`）而没改由它派生的颜色（比如 `accentHover`），派生的颜色会重新推导。

### 14.5 检查

```powershell
glctl theme validate $env:APPDATA\golauncher\themes\midnight-teal.json --format text
glctl theme show nord --format text      # 一个主题的全部颜色和对比度
glctl theme export nord > my-nord.json    # 内置主题的完整文件，作为起点
```

`validate` 列出错误（文件不能用）、警告（未知字段、写错的颜色、超出范围的值）和五项对比度。对比度按最坏的情况算：半透明背景下面分别是纯白和纯黑（中间隔着材质的底色）。低于标准的会在设置的"主题问题"里提示，但主题照样可以用。

### 14.6 建议

- 先 `extends` 一个明暗相同的内置主题，只改几个颜色。
- 半透明主题的 `opacity` 不要太低（深色 0.7–0.85、浅色 0.8–0.9 比较稳妥），并用 `validate` 看看对比度。
- 选中行可以用强调色（golauncher 的做法），也可以用一个接近背景的颜色配正文色（Nord、GitHub 等的做法）；后者的图标保留各自的语义色。

- **修正（2026-10-16）**：你在真机上发现，golauncher 浅色/深色主题下，选中行的每段文字后面都有一块颜色不对的方框；深色主题的其他行也有。
  - **原因**："文字衬底"默认开启，而这两个主题的默认背景不透明度是 0%，低于 40% 的触发线，于是每段文字后面都画了一块用窗口背景色的衬底，在选中行的蓝色上尤其明显。
  - **为什么之前没发现**："与改动前逐像素一致"的截图测试是在不透明（无材质）的情况下渲染的。
  - **修正**：
    - 文字衬底**默认关闭**；
    - 配置字段改名为 `textBackdrop`，这样早期构建默认写入的 `textShadow: true` 不再生效；
    - 即使开启，选中行上的文字也不画衬底，因为选中行有自己的底色。