---
name: 丹炉 Danlu
description: 每台 GPU 服务器自带的控制面板，现阶段的主力界面：安静、密集、只用颜色说状态的控制台。
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
- **Micro** (11px)：GPU 卡号小方块（等宽）、快捷键提示、手机底部导航标签、终端会话标签上的开启时间、手机摘要里的"24h"；证书指纹用 11.5px 等宽。不出现比 11px 更小的字。

### Named Rules
**The Tabular Rule.** 全局 `font-variant-numeric: tabular-nums`。任何会刷新的数字（利用率、显存、时长、计数）都必须等宽对齐，不能在刷新时抖动。

**The System Stack Rule.** 只用 `--font-sans` 和 `--font-mono` 两个系统栈。不引入网络字体，不打包字体文件。

**The Narrow Ramp Rule.** 字号不超过 20px。需要强调时先用字重和文字灰，而不是加大字号。

## Layout

内容区最大宽 1200px 居中；内边距手机 16px、≥640px 24px；页面纵向 16px / 24px。页面内区块间距 16px（≥640px 时 20px）。间距用 4px 基准的 Tailwind 刻度，常用 4 / 6 / 8 / 10 / 12 / 16 / 20 / 24px。

- **顶栏**：粘性，高 48px，面板底加底边线。依次是火焰标志、"丹炉"、斜杠、主机名、下划线式导航（总览 / 任务 / 设置），右侧主按钮"新建任务"带 `N` 快捷键提示。
- **首页**：状态摘要一行（运行 / 排队 / 24 小时内失败 / 空闲卡数 / 实时；手机上"24 小时内"写成 11px 的"24h"，摘要仍放得下一行）→ GPU 面板一排（≥1024px 四列，≥640px 两列）→ 一个面板里的分组任务列表（运行中、排队、最近完成），分组标题栏高 36px、衬底色 → 主机资源面板（CPU 和内存左右两栏：当前值、细条、最近 1 分钟曲线）。主机资源在手机和电脑上都排在任务列表后面：首屏要回答的是任务跑完没有、跑到哪了、卡空不空，CPU 曲线不在其中，放在 GPU 下面会把失败的任务挤出首屏。页面有一个读屏可见的 h1"总览"，GPU 区是一个带读屏标题的 section。
- **任务行**：padding 10px × 16px（手机 12px）。≥768px 时是固定列：名称列 240px（≥1280px 280px；首页因为多了复制图标是 208px / 248px，1280px 以下收窄，把宽度让给失败原因和最新输出）、弹性的卡号 + 细条 + 最后一行日志、时长 5.5rem、结束时间 4.5rem、commit 4.5rem、32px 的取消按钮位；任务页在列表上方加一条 32px 高的列头（任务 / 卡 · 最新输出 / 耗时 / 结束于 / 代码），列宽和行一致。手机上折成两行，信息不删：第一行是名字和时间（已结束的任务写"耗时 · 多久前结束"），名字放不下时折成两行（不截断：消融实验靠名字后缀区分），标星的任务在名字后跟一个 12px 的实心星；第二行是卡号、说明文字和 commit；tqdm 进度改写成短格式，说明文字放不下时最多折两行，不截掉 loss 或失败原因。手机上行尾只留一个 44px 的操作，见任务行组件。
- **表单**：≥768px 时左侧 200px 标签 + 说明，右侧控件；手机上下排列。
- **手机（<640px）**：GPU 每张卡一行放进同一个面板；任务分组直接展开；主机资源面板和电脑上一样排在任务列表后面；底部固定导航高 56px，三格"总览 / 新建 / 任务"，新建在正中间并用反色块；设置是顶栏右侧的齿轮图标，开启网页终端时齿轮左边多一个终端图标，两者都一点就到；页面底部留出导航和安全区的高度。手机上可点的目标都撑到 44px：任务行的操作、详情页操作栏、确认框按钮、更多操作菜单的每一项、分组标题里的"暂停队列"；最近用过的工作目录小标签撑到 36px。
- **断点**：640px（sm）、768px（md）、1024px（lg）、1280px（xl，只用于任务行名称列加宽）。

**The Density Floor Rule.** 信息密度只能升不能降。这是用户明确的回归判据：任何改版如果让一屏可读的任务、GPU 或字段变少，就是退步。

### 首页速度

定版标准是在 Chrome 的 Fast 4G 节流下，首页 2 秒内能看到 GPU 和任务（ROADMAP.md 第 8 节）。2026-11-05 在 `dev-5fbb10b` 上测了一次，条件如下：
- **服务。** 开发机（i7-14700HX）的 WSL 里起本地 CA 模式的服务，和真机一样走 TLS 和 HTTP/2。4 张假卡中 1 张被外部进程占用，2 个任务在跑、1 个排队，另有 8 个已结束的任务，其中 2 个失败。
- **浏览器和节流。** Chrome 156 无头模式，390×844 手机视口。通过 CDP 套用 DevTools 的 Fast 4G 预设：延迟 165 ms，下行约 8.1 Mbps，上行约 1.35 Mbps。
- **怎么算。** 计时从开始导航算起，到 GPU 区和任务列表都渲染出数据为止，取检测到之后的下一帧。冷启动用全新的浏览器上下文：缓存为空，连接新建。热缓存是在同一上下文里再打开一次。每组 7 次，取中位数，括号里是最慢的一次。

| 情况 | 首次绘制 | GPU | 任务 | 两者都出现 | 传输 |
|---|---|---|---|---|---|
| 冷启动 | 576 ms | 759 ms | 802 ms | **802 ms**（824） | 143 KB |
| 热缓存 | 220 ms | 404 ms | 416 ms | **416 ms**（430） | 18 KB |
| 冷启动，CPU 降速 4× | 724 ms | 952 ms | 1081 ms | **1081 ms**（1277） | 143 KB |
| 热缓存，CPU 降速 4× | 292 ms | 497 ms | 570 ms | **570 ms**（597） | 18 KB |

冷启动分三段，一段接一段：
1. HTML，约 190 ms；
2. 首屏的 JS 和 CSS，gzip 后共 126 KB，其中主包 78 KB，约 330 ms；
3. 首页的 8 个接口，经 HTTP/2 并发，约 200 ms。

**几点说明：**
- DevTools 的节流只给每个请求加延迟，不模拟 TCP 和 TLS 握手。真实 4G 上首次连接大约还要多 2 个往返，约 330 ms。
- 4× CPU 降速只是近似中端手机，不是真机。

把这两点都算上，冷启动仍在 1.5 秒以内，不用再做优化。以后改版时，如果首屏包或首页的接口数明显变多，按同样的条件再测一次。

## Elevation & Depth

系统是平的。层次靠背景色阶（页面底 → 面板 → 衬底 / 浮起）和 1px 边线表达，静止的面板没有阴影。阴影只出现在真正浮起来的东西上，而且都很轻。

### Shadow Vocabulary
- **分段选中块** (`box-shadow: 0 1px 2px rgb(0 0 0 / 0.08)`)：分段控件里被选中的一格。
- **浮层菜单** (`box-shadow: 0 8px 24px rgb(0 0 0 / 0.12)`)：工作目录自动补全下拉、详情页的更多操作菜单、确认框（确认框下面另有 40% 黑色遮罩）。
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
- **Commit：** 7 位短哈希，12px 等宽 `fg-2`；有未提交改动时后面跟 6px 琥珀圆点（读屏读"有未提交的改动"）。任务行和详情页里的 commit 都是链接，直达这个任务的代码快照（`/jobs/:id?tab=code`），悬停变 `fg` 加下划线。链接的点击区向四周撑出一圈（左右各 8px、上下各 10px，36px 高），不改变排版也不越出任务行，免得在手机上点成整行。详情页的"代码版本"标签后面用琥珀字写明"有未提交改动"，触屏上看不到悬停提示也能知道圆点的意思。

### Cards / Containers
- **Corner Style:** 面板圆角 (8px)。
- **Background:** `surface`；分组标题栏 `subtle`。
- **Shadow Strategy:** 无（见 Flat-At-Rest Rule）。
- **Border:** 1px `line`；内部行之间也用 1px `line` 分隔。
- **Internal Padding:** GPU 面板 12px；列表行 10px × 16px；表单行 16px × 20px。

### Inputs / Fields
- **Style:** 36px 高，面板底，1px 强边线，6px 圆角，左右 12px，14px 字；占位符 `fg-3`。路径、命令、环境变量输入用 12.5px 等宽。
- **Focus:** 悬停边线变 `fg-4`；聚焦边线变 `fg-3`，外加 3px 运行蓝 15% 光晕。150ms 过渡。
- **Error / Disabled:** 禁用 50% 透明度。字段错误在字段正下方用 12px 失败红文字加 14px 警示图标说明，字段边线变失败红 60%，提交时焦点移到第一个出错的字段；不用浏览器自带的必填气泡（登录页也一样）。只有服务端返回的错误放在表单底部的失败提示框里。
- **工作目录补全：** 聚焦时不展开，输入或按 ↓ 才展开子目录列表，Esc 收起；校验失败时焦点落在这里，展开的列表不能盖住错误说明。
- **单选卡片：** 少数几个、每项需要两行说明的单选（重跑时的代码来源）用并排的卡片，和指定 GPU 的卡片同一套样子：6px 圆角、强边线，选中时 `fg` 边线加 1px `fg` 描边、衬底色和对勾；第一行 13px / 600 标题，第二行 12px `fg-3` 说明，最多两行。底下是原生单选框，方向键和禁用交给浏览器，键盘焦点框画在卡片上。
- **Labels:** 每个控件都用 `<label for>` 关联标签，说明文字用 `aria-describedby` 关联；组合控件（分段按钮 + 步进器）是以标签命名的 group。

### Navigation
- **桌面顶栏导航：** 13px，未选中 `fg-3`，悬停 `fg`；选中 500 字重 `fg`，下方 2px `fg` 色下划线压住顶栏底边。
- **页内标签页：** 同一套下划线语言，高 40px、14px。
- **分段控件：** 32px 高的衬底轨道，选中格是浮起色 + 轻阴影 + 1px `line` 描边（浅色主题下白块叠在浅灰上，没有描边分不清）；用于筛选和小范围切换，选项里可以带状态图标。语义上是单选组，标签页是 tablist，两者都只有选中项能 Tab 到，左右方向键和 Home / End 切换。
- **手机底部导航：** 见 Layout。图标 20px，标签 11px / 500；选中 `fg`，未选中 `fg-3`。
- **更多操作菜单：** 一行放不下、又不常用的操作收进一个 44px 的 `⋯` 次按钮。展开的菜单右对齐、面板底、1px `line` 边、6px 圆角、浮层阴影，每项 44px 高、14px `fg-2` 字、16px `fg-3` 图标，悬停和键盘焦点都是 `hover` 底。打开时焦点落在第一项，↑ ↓ Home End 移动，Esc 关闭并把焦点还给按钮，点外面、Tab 离开或点了某一项都会关闭。目前只用在手机的任务详情页。

### 状态图标（Signature）
所有状态都用 16px 的 lucide 图标，形状和颜色同时区分，色盲也能读：
- 排队：灰色虚线圈 (`fg-3`)
- 运行：蓝色旋转弧 (`run`，匀速 1.6s 一圈)；正在取消时变琥珀
- 成功：绿色对勾圈 (`ok`)
- 失败：红色叉圈 (`fail`)
- 取消：灰色斜杠圈 (`fg-3`)
- 外部进程占卡：琥珀色 6px 圆点 + 琥珀文字

作为分组标题时（如"最近完成"）图标可以退成中性灰。图标默认带读屏能读出的状态名（`role="img"`）；旁边已经写着状态文字时（摘要、分组标题、筛选、状态标签）标成装饰，免得读两遍。任务状态变化时，除了整行闪一下，还通过全局唯一的读屏播报区（`role="status"`，polite）读一句"任务 #5 名字：成功"，同一轮里的几条合成一句。

### 任务行（Signature）
Linear 式的单行：状态图标、#编号（`fg-3`）、任务名（14px / 500）、GPU 卡号、运行任务的 40px 进度细条、最后一行日志（12px 等宽）、时长、结束时间、commit。失败的行写"退出码 · 报错行"：服务端从日志末尾找最后一条像报错的行（Python 异常、torchrun 的 `[rankN]:` 异常、Killed、段错误），跳过 torchrun 的 ChildFailedError 汇总；找不到时用最后一行输出。整行可点：名字是链接，用 `::after` 铺满整行，commit 和取消按钮浮在上面单独可点；键盘焦点框画在整行上。悬停整行变衬底色；运行中的行在悬停时才显示取消按钮（触屏常显）。**状态变化时整行背景闪一次**：1.2s ease-out，前 15% 保持新状态颜色 14% 的底色再淡出；成功闪绿、失败闪红、取消闪灰、其余闪蓝。

首页每个任务行右侧常显中性的复制图标，与标星、取消操作并列；电脑上点击区 32px，从名称列调配宽度，保留卡号和最新输出列的空间。手机上行尾只留一个 44px 的操作，不额外撑高任务行：运行和排队的行是取消，已结束的行是复制（只在首页）；标星不在手机的行上点，只显示星标，标星和取消标星在详情页的更多操作里。复制只预填新建表单，保留原名，提交后是独立任务，不记录“重跑自”。复制页不显示原任务的失败说明或代码比较；现有重跑入口继续使用自己的名称后缀和来源关系。

### 任务详情操作栏
每种状态只有一个主操作：运行和排队的任务是"取消任务"（危险按钮，按钮上写全"取消任务"，不写"取消"），已结束的任务是"重跑"（反色墨），失败的写成"改参数重跑"；正在取消的任务没有主操作，重跑退成次按钮。电脑上所有操作都露在外面（标星、终端、W&B / SwanLab、和…对比，运行中再加重跑），主操作排在最右。手机上一行只放主操作和实验记录链接，都是 44px 高，其余（标星、改名、重跑、终端、和…对比）收进行尾的更多操作菜单；标星的任务在标题后跟一个 16px 的实心星。

改名就地进行：电脑上任务名后面跟一个 32px 的幽灵铅笔按钮（`fg-3`），手机上在更多操作里。点开后标题行换成一个输入框加"保存"（反色墨）和"取消"，手机上三者都是 44px 高、排在一行；文字先全选，Enter 保存，Esc 或"取消"放弃，名字没变就直接退出，出错时留在原地显示原因。已经启动过的任务在输入框下方用 12px `fg-3` 写明输出目录和 DANLU_JOB_NAME 不跟着改。不用确认框：改名可以随时改回来。

### 失败说明
任务详情页状态栏下方的失败红框，第一行就是报错行（12.5px 等宽），退出码已经在状态栏里，不再重复；被外部终止、启动失败这类退出码以外的原因另起一行。接着按服务端的失败分类（`fail_kind`）给一句下一步，用主文字色：显存不足建议减小 batch size 或多申请一张卡，主机内存不足建议减少 num_workers，NCCL 出错建议先原样重跑，Python 异常建议按报错行改代码，服务器重启和被外部终止建议用快照原样重跑，启动失败建议检查工作目录、conda 和 ~/.bashrc；只给建议，不替用户断定原因，没法归类的（只有退出码）不写。红框末尾是"看日志末尾"，切到日志标签并滚到日志面板。清理残留进程等运行备注单独用中性提示显示，成功和取消的任务也能看到。重跑页标题下方用失败红写出原任务为什么失败。

### 重跑的代码来源
重跑页第一行是"代码版本"：两张单选卡片，"现在的工作目录"和"任务 #N 的快照"，第二行各自写出分支、7 位 commit 和"有未提交改动"（琥珀），选之前就看得出两边是否一样；快照不可用时那张卡片禁用并写明原因。默认值跟着失败分类走：服务器重启、被外部终止、收到信号、NCCL 出错这类和代码无关的失败默认用快照原样重跑，其余默认用工作目录现在的代码（多半刚改过）。选了工作目录且代码和快照不同时，表单上方仍有琥珀提示列出差别。

### 确认框
只用于不可恢复的操作（目前是取消任务）。原生 `<dialog>`：面板底、1px `line` 边、8px 圆角、浮层阴影、40% 黑色遮罩；宽 420px，手机上贴着底部（离底 16px 加安全区），拇指够得到。标题 14px / 600 写成问句（"取消任务 #5？"），正文写清后果：运行中的任务写已运行多久、进程会被结束且不能恢复、日志和代码快照会保留；排队的任务写排在第几位。底部是衬底色按钮栏，左边是不造成损失的按钮（"继续运行" / "保留排队"），焦点默认落在它上面；右边是危险按钮。Esc 或点遮罩关闭，出错时留在框里显示原因。不用浏览器的 `confirm` 和 `alert`。

### GPU 面板（Signature）
桌面每张卡一块面板：第一行 "GPU n"（13px / 600）、型号（混装时才显示）、温度和功耗；中间利用率和显存两组"标签 + 数值 + 4px 细条"；底部隔一条边线写占卡者（运行中的任务链接、琥珀色外部进程，或绿色"空闲"）。细条颜色跟随占卡状态：任务蓝、外部琥珀、空闲 `fg-4`。手机端改为每卡一行。

### 终端面板（Signature）
日志和代码在两种主题下都是深色终端（`term-bg`），8px 圆角，40px 高的工具栏（白 10% 分隔线，按钮悬停白 10% 底）。文字 12.5px 等宽、行高 1.6、tab 宽 4。直播时左上角绿色脉冲点。ANSI 颜色用为深色背景调亮的一套（sidecar 里有完整取值）。日志框从自己的位置铺到视口底部（手机上让出底部导航和安全区），最新几行始终在首屏里；最小高度手机 280px、电脑 320px。为了让手机上的日志框不落到下限，详情页手机上的操作栏只占一行，关键信息条是三列两行。自动换行只在放不下时断开长串，不把 "100/100" 这类数字拆开。代码浏览器左侧有行号（`#7a7a7a`，在终端底上 4.6:1，不可选中），和 traceback 里的行号对得上。电脑上左侧是文件树（目录按钮带 `aria-expanded`，选中的文件带 `aria-current`）；手机上文件树换成一个 44px 的系统下拉选择，列出完整路径，代码紧接在下面，并去掉代码区顶上重复的文件名条，打开代码标签页就能看到代码。

### 快捷键
`N` 新建任务（顶栏按钮内提示），`/` 搜索任务（在任务页聚焦搜索框，在其他页面先跳到任务页；搜索框内提示，`Esc` 清空），`Ctrl/⌘ + Enter` 提交新任务。快捷键提示是 20px 高、11px、当前色 25% 边框的小框，只在 ≥1024px 显示。搜索框有内容时，提示换成清空按钮，不用浏览器自带的蓝色清除图标。

## Do's and Don'ts

### Do:
- **Do** 只通过语义变量取色（`bg`、`surface`、`line`、`fg-2`、`run`…），新颜色同时定义深浅两套值。
- **Do** 用反色墨 (`inv` / `inv-fg`) 做页面上唯一的主按钮。
- **Do** 让每个任务状态同时有形状和颜色：虚线圈、旋转弧、对勾圈、叉圈、斜杠圈。
- **Do** 所有数字等宽；会刷新的数值给固定列宽右对齐。
- **Do** 状态变化时让该行按新状态的颜色闪一次（1.2s）；运行图标匀速 1.6s 一圈；细条数值变化用 500ms 的 `transform: scaleX` 过渡（不动 width，免得十几根细条每次轮询都触发重新布局）；其他交互只有 150ms 的颜色过渡。尊重 `prefers-reduced-motion`：关掉旋转、过渡和平滑滚动，只保留整行闪一下（只是底色淡出，没有位移，而且是这一行状态变了的唯一提示）。
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
