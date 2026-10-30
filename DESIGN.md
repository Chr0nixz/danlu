---
name: 丹炉 Danlu
description: 每台 GPU 服务器自带的兜底控制面板：安静、密集、只用颜色说状态的控制台。
colors:
  bg: "#fafafa"
  surface: "#ffffff"
  subtle: "#f4f4f4"
  raised: "#ffffff"
  hover: "#efefef"
  line: "#e8e8e8"
  line-strong: "#d6d6d6"
  fg: "#171717"
  fg-2: "#4d4d4d"
  fg-3: "#6b6b6b"
  fg-4: "#a8a8a8"
  inv: "#171717"
  inv-fg: "#fafafa"
  run: "#0b6bcb"
  ok: "#18794e"
  fail: "#cd2b31"
  warn: "#a35200"
  brand: "#e5482d"
  bg-dark: "#0a0a0a"
  surface-dark: "#111111"
  subtle-dark: "#171717"
  raised-dark: "#2a2a2a"
  hover-dark: "#1f1f1f"
  line-dark: "#242424"
  line-strong-dark: "#363636"
  fg-dark: "#ededed"
  fg-2-dark: "#b4b4b4"
  fg-3-dark: "#8f8f8f"
  fg-4-dark: "#5a5a5a"
  inv-dark: "#ededed"
  inv-fg-dark: "#0a0a0a"
  run-dark: "#52a8ff"
  ok-dark: "#3dd68c"
  fail-dark: "#ff6369"
  warn-dark: "#f1a10d"
  term-bg: "#0b0b0b"
  term-fg: "#d9d9d9"
typography:
  headline:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", \"Noto Sans CJK SC\", sans-serif"
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: "1.75rem"
    letterSpacing: "-0.025em"
    fontFeature: "\"tnum\""
  title:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", \"Noto Sans CJK SC\", sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    lineHeight: "1.75rem"
    fontFeature: "\"tnum\""
  body:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", \"Noto Sans CJK SC\", sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: "1.25rem"
    fontFeature: "\"tnum\""
  ui:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", \"Noto Sans CJK SC\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: "1.25rem"
    fontFeature: "\"tnum\""
  label:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", \"Noto Sans CJK SC\", sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: "1rem"
    fontFeature: "\"tnum\""
  mono:
    fontFamily: "ui-monospace, \"SFMono-Regular\", \"Cascadia Mono\", \"JetBrains Mono\", Menlo, Consolas, \"Liberation Mono\", \"Roboto Mono\", \"Noto Sans Mono CJK SC\", monospace"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: 1.6
  micro:
    fontFamily: "ui-monospace, \"SFMono-Regular\", \"Cascadia Mono\", \"JetBrains Mono\", Menlo, Consolas, \"Liberation Mono\", \"Roboto Mono\", \"Noto Sans Mono CJK SC\", monospace"
    fontSize: "11px"
    fontWeight: 400
    lineHeight: 1
rounded:
  chip: "4px"
  control: "6px"
  panel: "8px"
  full: "9999px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "5": "20px"
  "6": "24px"
components:
  button-primary:
    backgroundColor: "{colors.inv}"
    textColor: "{colors.inv-fg}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "36px"
  button-primary-sm:
    backgroundColor: "{colors.inv}"
    textColor: "{colors.inv-fg}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "32px"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fg}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "36px"
  button-secondary-hover:
    backgroundColor: "{colors.hover}"
  button-ghost:
    textColor: "{colors.fg-2}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "36px"
  button-ghost-hover:
    backgroundColor: "{colors.hover}"
    textColor: "{colors.fg}"
  button-danger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fail}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "36px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fg}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "36px"
  panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
  group-header:
    backgroundColor: "{colors.subtle}"
    typography: "{typography.ui}"
    padding: "0 16px"
    height: "36px"
  job-row:
    typography: "{typography.body}"
    padding: "10px 16px"
  gpu-panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
    padding: "12px"
  gpu-chip:
    textColor: "{colors.fg-2}"
    typography: "{typography.micro}"
    rounded: "{rounded.chip}"
    height: "18px"
  segmented:
    backgroundColor: "{colors.subtle}"
    rounded: "{rounded.control}"
    height: "32px"
  segmented-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.fg}"
  topbar:
    backgroundColor: "{colors.surface}"
    height: "48px"
  bottom-nav:
    backgroundColor: "{colors.surface}"
    height: "56px"
  terminal:
    backgroundColor: "{colors.term-bg}"
    textColor: "{colors.term-fg}"
    typography: "{typography.mono}"
    rounded: "{rounded.panel}"
    padding: "10px 12px"
---

# Design System: 丹炉 Danlu

## Overview

**Creative North Star: "值班台"（The Quiet Console）**

丹炉的界面是一块安静、密集的控制台，做工对标 Vercel 控制台、Linear 和 GitHub Actions（类别标准 canon 方向，用户在两轮方向选择后主动选定）。整个画面由中性灰阶构成：页面底、面板、1px 边线、四级文字灰。颜色从不用来装饰，只用来说状态：蓝色是在跑，绿色是成功或空闲，红色是失败，琥珀是外部进程或需要注意，灰色是排队和取消。朱砂色只属于火焰标志。

密度是这个系统的底线。列表和表格优先，一行里同时给出状态图标、#编号、名称、GPU 卡号、最后一行日志、时长、结束时间和 commit；卡片只留给 GPU 这类本身就是"一台设备一块"的数据。所有数字等宽（tabular-nums），刷新时不跳。字体只用系统字体栈，不打包字体，也不从外部 CDN 加载任何资源。

深浅两套主题共用同一套语义变量，默认跟随系统，可以在设置里强制浅色或深色。日志和代码面板在两种主题下都是深色终端。明确拒绝的样子：粗圆角大卡片加橙色按钮的模板面板。

**Key Characteristics:**
- 中性灰阶 + 状态色，颜色即语义
- 状态同时用形状和颜色区分（lucide 图标）
- 反色实心主按钮，朱砂色只给火焰标志
- 系统字体栈，全局 tabular-nums
- 控件圆角 6px，面板圆角 8px，1px 边线，几乎没有阴影
- 日志和代码是深色终端，两种主题都一样
- 信息密度只能升不能降

## Colors

一套冷静的纯中性灰阶，加四个按深浅主题分别调过亮度的状态色；所有颜色都是语义变量（`--bg`、`--fg-2`、`--run` 等），通过 `@theme inline` 映射成 Tailwind 颜色。

### Primary
- **反色墨（Inverted Ink）** (`inv` / `inv-fg`)：主按钮和手机底部导航中间的"新建"块。浅色模式黑底白字，深色模式白底黑字。主动作靠明度反差而不是色相来突出。

### Secondary
- **运行蓝（Run Blue）** (`run`)：运行中的旋转弧、运行任务的进度细条、占卡 GPU 的利用率条、焦点环（70% 混合）、输入框聚焦光晕（15%）、文字选区（28%）。
- **成功绿（OK Green）** (`ok`)：成功对勾圈、GPU "空闲"圆点、摘要里"有空卡"圆点、日志直播中的脉冲点。
- **失败红（Fail Red）** (`fail`)：失败叉圈、失败原因文字、失败计数、危险按钮文字、错误提示框（6% 底、30% 边）。
- **琥珀（Warn Amber）** (`warn`)：外部进程占卡（文字、圆点、利用率条）、正在取消中的运行图标、commit 有未提交改动的小圆点、数据过期提示、警告框。

### Tertiary
- **朱砂（Cinnabar）** (`brand`)：只用于火焰标志（顶栏 24px 圆角方块和 favicon），两种主题下不变。

### Neutral
- **页面底（Page）** (`bg`)：整页背景。
- **面板（Surface）** (`surface`)：面板、顶栏、底部导航、输入框和次按钮底色。
- **衬底（Subtle）** (`subtle`)：分组标题栏、行悬停、分段控件轨道、说明框。
- **浮起（Raised）** (`raised`)：分段控件选中块；深色模式下明显亮于面板。
- **悬停（Hover）** (`hover`)：幽灵按钮和次按钮悬停底、细条轨道、骨架屏。
- **边线（Line）** (`line`)：面板边框、行分隔线、标签页底线。
- **强边线（Line Strong）** (`line-strong`)：输入框、次按钮、GPU 卡号小方块的边框，滚动条。
- **文字四级** (`fg` / `fg-2` / `fg-3` / `fg-4`)：正文与标题 / 次要值 / 标签、编号、时长、占位符 / 分隔斜杠和空闲细条。
- **终端底（Terminal）** (`term-bg` / `term-fg`)：日志和代码面板，两种主题都用；文字选区为运行蓝 35%。ANSI 16 色按深色背景调亮（见 sidecar）。

### Named Rules
**The Color-Is-Status Rule.** 颜色只表达状态或注意事项。没有状态含义的地方一律用灰阶；不加装饰色、不加渐变、不给分区上色。

**The One Flame Rule.** 朱砂色 `brand` 只出现在火焰标志上。主按钮用反色墨，绝不用朱砂或任何橙色。

**The Paired Theme Rule.** 新增颜色必须作为语义变量同时定义浅色和深色两个值，并同时写进 `prefers-color-scheme: dark` 块和 `[data-theme="dark"]` 块；组件里只引用变量，不写裸色值（终端面板除外）。

## Typography

**Display Font:** 无（这个系统没有展示字级）
**Body Font:** 系统无衬线栈（ui-sans-serif, system-ui, Segoe UI, PingFang SC, Microsoft YaHei UI …）
**Label/Mono Font:** 系统等宽栈（ui-monospace, SFMono-Regular, Cascadia Mono, Menlo, Consolas …）

**Character:** 不打包任何字体，中文走系统黑体，读起来就是操作系统本身的控制台。字级很窄，层级主要靠字重（400 / 500 / 600）和四级文字灰拉开，而不是靠字号。

### Hierarchy
- **Headline** (600, 20px, 28px, -0.025em)：任务详情页的任务名，全站唯一的大标题。
- **Title** (600, 18px, 28px)：页面标题（任务、新建任务、设置、登录）。
- **Body** (400–500, 14px, 20px)：任务名（500）、输入框文字、中号按钮、标签页。
- **UI** (400–600, 13px, 20px)：系统的主力字级（`text-ui`）。导航、分组标题、摘要、GPU 卡号、表单标签、小按钮、提示框。
- **Label** (400, 12px, 16px)：说明文字、时长、相对时间、GPU 指标、最后一行日志。
- **Mono** (400, 12.5px, 1.6)：终端面板、路径、命令、环境变量、commit（12px）、最后一行日志（12px）。
- **Micro** (11px)：GPU 卡号小方块（等宽）、快捷键提示、手机底部导航标签；证书指纹用 11.5px 等宽。

### Named Rules
**The Tabular Rule.** 全局 `font-variant-numeric: tabular-nums`。任何会刷新的数字（利用率、显存、时长、计数）都必须等宽对齐，不能在刷新时抖动。

**The System Stack Rule.** 只用 `--font-sans` 和 `--font-mono` 两个系统栈。不引入网络字体，不打包字体文件。

**The Narrow Ramp Rule.** 字号不超过 20px。需要强调时先用字重和文字灰，而不是加大字号。

## Layout

内容区最大宽 1200px 居中；内边距手机 16px、≥640px 24px；页面纵向 16px / 24px。页面内区块间距 16px（≥640px 时 20px）。间距用 4px 基准的 Tailwind 刻度，常用 4 / 6 / 8 / 10 / 12 / 16 / 20 / 24px。

- **顶栏**：粘性，高 48px，面板底加底边线。依次是火焰标志、"丹炉"、斜杠、主机名、下划线式导航（总览 / 任务 / 设置），右侧主按钮"新建任务"带 `N` 快捷键提示。
- **首页**：状态摘要一行（运行 / 排队 / 24 小时内失败 / 空闲卡数 / 实时）→ GPU 面板一排（≥1024px 四列，≥640px 两列）→ 一个面板里的分组任务列表（运行中、排队、最近完成），分组标题栏高 36px、衬底色。
- **任务行**：padding 10px × 16px（手机 12px）。≥768px 时是固定列：名称列 240px（≥1024px 280px）、弹性的卡号 + 细条 + 最后一行日志、时长 5.5rem、结束时间 4.5rem、commit 4.5rem、32px 的取消按钮位。手机上折成两行，信息不删，只把 tqdm 进度改写成短格式。
- **表单**：≥768px 时左侧 200px 标签 + 说明，右侧控件；手机上下排列。
- **手机（<640px）**：GPU 每张卡一行放进同一个面板；任务分组直接展开；底部固定导航高 56px，三格"总览 / 新建 / 任务"，新建在正中间并用反色块；设置是顶栏右侧的齿轮图标；页面底部留出导航和安全区的高度。触控的取消按钮放大到 44px。
- **断点**：640px（sm）、768px（md）、1024px（lg）。

**The Density Floor Rule.** 信息密度只能升不能降。这是用户明确的回归判据：任何改版如果让一屏可读的任务、GPU 或字段变少，就是退步。

## Elevation & Depth

系统是平的。层次靠背景色阶（页面底 → 面板 → 衬底 / 浮起）和 1px 边线表达，静止的面板没有阴影。阴影只出现在三种真正浮起来的东西上，而且都很轻。

### Shadow Vocabulary
- **分段选中块** (`box-shadow: 0 1px 2px rgb(0 0 0 / 0.08)`)：分段控件里被选中的一格。
- **浮层菜单** (`box-shadow: 0 8px 24px rgb(0 0 0 / 0.12)`)：工作目录自动补全下拉。
- **终端浮钮** (`box-shadow: 0 4px 12px rgb(0 0 0 / 0.4)`)：日志面板里"回到底部"按钮。
- **标签页底线** (`box-shadow: inset 0 -1px 0 var(--line)`)：下划线标签页的轨道线，不是投影。

### Named Rules
**The Flat-At-Rest Rule.** 面板、卡片、列表静止时只有边线没有阴影。只有覆盖在其他内容之上的浮层才允许投影。

## Shapes

圆角克制而分级：控件 6px（按钮、输入框、分段控件轨道、提示框、标志方块），面板 8px（面板、GPU 面板、终端、代码浏览器），小方块 4px（GPU 卡号、分段选中块、终端工具栏按钮、快捷键提示），圆点和细条全圆。所有容器用 1px 实线边；只有空状态的说明框用虚线边。进度细条高 4px。

**The Two Radii Rule.** 控件 6px、面板 8px，不出现更大的圆角。

## Components

### Buttons
干净、低调，靠明度说话。
- **Shape:** 控件圆角 (6px)；中号高 36px、左右 14px、14px 字；小号高 32px、左右 10px、13px 字；图标按钮 36 / 32px 见方，图标 16px。
- **Primary:** 反色墨实心 (`inv` 底、`inv-fg` 字)，500 字重。悬停透明度 85%，按下 75%。
- **Hover / Focus:** 背景、边线、文字、透明度 150ms 过渡；键盘焦点是 2px 运行蓝（70%）外框，偏移 2px。禁用为 45% 透明度。加载时左侧换成旋转图标。
- **Secondary:** 面板底、强边线、主文字色，悬停变 `hover` 底。
- **Ghost:** 无底无边，`fg-2` 文字，悬停 `hover` 底并变 `fg`。
- **Danger:** 次按钮外形、失败红文字，悬停边线和底色带 8% 的失败红。

### Chips
- **GPU 卡号：** 18px 高的小方块，4px 圆角，强边线，11px 等宽 `fg-2`，多张卡紧贴并排（间距 2px）。
- **代码版本标签：** 分支和 commit 用中性小标签；有未提交改动时变琥珀（8% 底、30% 边）。
- **Commit：** 7 位短哈希，12px 等宽 `fg-2`；有未提交改动时后面跟 6px 琥珀圆点。

### Cards / Containers
- **Corner Style:** 面板圆角 (8px)。
- **Background:** `surface`；分组标题栏 `subtle`。
- **Shadow Strategy:** 无（见 Flat-At-Rest Rule）。
- **Border:** 1px `line`；内部行之间也用 1px `line` 分隔。
- **Internal Padding:** GPU 面板 12px；列表行 10px × 16px；表单行 16px × 20px。

### Inputs / Fields
- **Style:** 36px 高，面板底，1px 强边线，6px 圆角，左右 12px，14px 字；占位符 `fg-3`。路径、命令、环境变量输入用 12.5px 等宽。
- **Focus:** 悬停边线变 `fg-4`；聚焦边线变 `fg-3`，外加 3px 运行蓝 15% 光晕。150ms 过渡。
- **Error / Disabled:** 禁用 50% 透明度；错误用失败提示框在字段下方说明。

### Navigation
- **桌面顶栏导航：** 13px，未选中 `fg-3`，悬停 `fg`；选中 500 字重 `fg`，下方 2px `fg` 色下划线压住顶栏底边。
- **页内标签页：** 同一套下划线语言，高 40px、14px。
- **分段控件：** 32px 高的衬底轨道，选中格是浮起色 + 轻阴影；用于筛选和小范围切换，选项里可以带状态图标。
- **手机底部导航：** 见 Layout。图标 20px，标签 11px / 500；选中 `fg`，未选中 `fg-3`。

### 状态图标（Signature）
所有状态都用 16px 的 lucide 图标，形状和颜色同时区分，色盲也能读：
- 排队：灰色虚线圈 (`fg-3`)
- 运行：蓝色旋转弧 (`run`，匀速 1.6s 一圈)；正在取消时变琥珀
- 成功：绿色对勾圈 (`ok`)
- 失败：红色叉圈 (`fail`)
- 取消：灰色斜杠圈 (`fg-3`)
- 外部进程占卡：琥珀色 6px 圆点 + 琥珀文字

作为分组标题时（如"最近完成"）图标可以退成中性灰。

### 任务行（Signature）
Linear 式的单行：状态图标、#编号（`fg-3`）、任务名（14px / 500）、GPU 卡号、运行任务的 40px 进度细条、最后一行日志（12px 等宽，失败时是红色失败原因）、时长、结束时间、commit。悬停整行变衬底色；运行中的行在悬停时才显示取消按钮（触屏常显）。**状态变化时整行背景闪一次**：1.2s ease-out，前 15% 保持新状态颜色 14% 的底色再淡出；成功闪绿、失败闪红、取消闪灰、其余闪蓝。

### GPU 面板（Signature）
桌面每张卡一块面板：第一行 "GPU n"（13px / 600）、型号（混装时才显示）、温度和功耗；中间利用率和显存两组"标签 + 数值 + 4px 细条"；底部隔一条边线写占卡者（运行中的任务链接、琥珀色外部进程，或绿色"空闲"）。细条颜色跟随占卡状态：任务蓝、外部琥珀、空闲 `fg-4`。手机端改为每卡一行。

### 终端面板（Signature）
日志和代码在两种主题下都是深色终端（`term-bg`），8px 圆角，40px 高的工具栏（白 10% 分隔线，按钮悬停白 10% 底）。文字 12.5px 等宽、行高 1.6、tab 宽 4。直播时左上角绿色脉冲点。ANSI 颜色用为深色背景调亮的一套（sidecar 里有完整取值）。

### 快捷键
`N` 新建任务（顶栏按钮内提示），`/` 聚焦任务搜索（搜索框内提示，`Esc` 清空），`Ctrl/⌘ + Enter` 提交新任务。快捷键提示是 20px 高、11px、当前色 25% 边框的小框，只在 ≥1024px 显示。

## Do's and Don'ts

### Do:
- **Do** 只通过语义变量取色（`bg`、`surface`、`line`、`fg-2`、`run`…），新颜色同时定义深浅两套值。
- **Do** 用反色墨 (`inv` / `inv-fg`) 做页面上唯一的主按钮。
- **Do** 让每个任务状态同时有形状和颜色：虚线圈、旋转弧、对勾圈、叉圈、斜杠圈。
- **Do** 所有数字等宽；会刷新的数值给固定列宽右对齐。
- **Do** 状态变化时让该行按新状态的颜色闪一次（1.2s）；运行图标匀速 1.6s 一圈；其他交互只有 150ms 的颜色过渡。尊重 `prefers-reduced-motion`。
- **Do** 日志、代码、命令、路径、commit 用系统等宽栈；日志和代码放进深色终端面板，两种主题都一样。
- **Do** 在手机上保留同样多的信息：改排版、改短格式，不删字段。
- **Do** 主题默认跟随系统，允许用 `<html data-theme>` 手动覆盖，并在首屏前由内联脚本提前应用以免闪烁。

### Don't:
- **Don't** 让信息密度比现在低：不加大行高、不把列表换成大卡片、不把字段藏进展开层。
- **Don't** 用朱砂色或任何橙色做按钮、链接、强调或背景；朱砂只属于火焰标志。
- **Don't** 用没有状态含义的颜色装饰分区、图标或标题。
- **Don't** 引入网络字体、打包字体或任何外部 CDN 资源。
- **Don't** 使用大于 8px 的圆角或粗圆角大卡片加橙色按钮的模板面板样式。
- **Don't** 给静止的面板加阴影；阴影只属于浮层。
- **Don't** 让日志或代码面板跟随浅色主题变白。
