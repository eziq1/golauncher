# 主题设计方案

> 状态：设计 v1 · 已确认（§12），一、二期一起做· 2026-10-15 · 对应需求 R50（见 [requirements.md](requirements.md)）

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