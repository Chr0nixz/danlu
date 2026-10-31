---
name: 丹炉 桌面客户端
description: 常驻托盘的多服务器控制台：平台无关、克制的现代开发者工具，层次靠底色台阶，颜色只说状态。
colors:
  window-dark: "#0a0b0d"
  window-light: "#e8eaee"
  panel-dark: "#121317"
  panel-light: "#ffffff"
  card-dark: "#18191e"
  card-light: "#f6f7f9"
  hover-dark: "#1e2026"
  hover-light: "#eff0f3"
  ink-dark: "#eeeef1"
  ink-light: "#16171b"
  ink-2-dark: "#a3a5ad"
  ink-2-light: "#5b5e66"
  ink-3-dark: "#7c7f88"
  ink-3-light: "#6f727a"
  hairline-dark: "rgb(255 255 255 / 0.06)"
  hairline-light: "rgb(0 0 0 / 0.07)"
  hairline-strong-dark: "rgb(255 255 255 / 0.1)"
  hairline-strong-light: "rgb(0 0 0 / 0.12)"
  run-dark: "#5aa7ff"
  run-light: "#1f6fe0"
  ok-dark: "#4fd18b"
  ok-light: "#15803d"
  fail-dark: "#ff6b6b"
  fail-light: "#dc2626"
  warn-dark: "#f5b14c"
  warn-light: "#b45309"
  terminal-dark: "#0b0c0e"
  terminal-light: "#111215"
  cinnabar: "#e5482d"
typography:
  title:
    fontFamily: "Geist Variable, Microsoft YaHei UI, PingFang SC, system-ui, sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.5
  heading:
    fontFamily: "Geist Variable, Microsoft YaHei UI, PingFang SC, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 600
    lineHeight: 1.5
  body:
    fontFamily: "Geist Variable, Microsoft YaHei UI, PingFang SC, system-ui, sans-serif"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: 1.5
  data:
    fontFamily: "Geist Mono Variable, Microsoft YaHei UI, PingFang SC, ui-monospace, monospace"
    fontSize: "11.5px"
    fontWeight: 400
    lineHeight: 1.5
    fontFeature: "\"tnum\""
  micro:
    fontFamily: "Geist Mono Variable, Microsoft YaHei UI, PingFang SC, ui-monospace, monospace"
    fontSize: "11px"
    fontWeight: 400
    lineHeight: 1.4
    fontFeature: "\"tnum\""
rounded:
  kbd: "4px"
  control: "6px"
  terminal: "8px"
  panel: "10px"
spacing:
  hair: "1px"
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
components:
  button-primary:
    backgroundColor: "{colors.ink-light}"
    textColor: "{colors.panel-light}"
    rounded: "{rounded.control}"
    height: "28px"
    padding: "0 10px"
  button-primary-dark:
    backgroundColor: "{colors.ink-dark}"
    textColor: "{colors.window-dark}"
    rounded: "{rounded.control}"
    height: "28px"
    padding: "0 10px"
  button-secondary:
    backgroundColor: "{colors.panel-light}"
    textColor: "{colors.ink-light}"
    rounded: "{rounded.control}"
    height: "28px"
    padding: "0 10px"
  button-danger:
    backgroundColor: "{colors.panel-light}"
    textColor: "{colors.fail-light}"
    rounded: "{rounded.control}"
    height: "28px"
    padding: "0 10px"
  input:
    backgroundColor: "{colors.panel-light}"
    textColor: "{colors.ink-light}"
    rounded: "{rounded.control}"
    height: "28px"
    padding: "0 8px"
  job-row:
    textColor: "{colors.ink-light}"
    rounded: "{rounded.control}"
    height: "30px"
    padding: "0 8px"
  job-row-selected:
    backgroundColor: "{colors.hover-light}"
    rounded: "{rounded.control}"
    height: "30px"
  nav-item-active:
    backgroundColor: "{colors.panel-light}"
    textColor: "{colors.ink-light}"
    rounded: "{rounded.control}"
    size: "32px"
  panel:
    backgroundColor: "{colors.panel-light}"
    rounded: "{rounded.panel}"
  detail-card:
    backgroundColor: "{colors.card-light}"
    rounded: "{rounded.panel}"
    padding: "10px 12px"
  terminal:
    backgroundColor: "{colors.terminal-dark}"
    textColor: "#d6d7dc"
    rounded: "{rounded.terminal}"
---

# Design System: 丹炉 桌面客户端

> 只管 `desktop/`。面板（`web/`）的视觉系统在 `web/DESIGN.md`，两者互不相干。取值来自 `desktop/src/index.css` 和组件代码，那里是唯一来源。

## Overview

**Creative North Star: "炉前控制台"**

一块安静的操作台，开着就在 IDE 旁边，只在出事时用颜色叫你。做工参照 Linear、Raycast，但更收敛：没有渐变、没有毛玻璃、没有彩色主按钮。结构完全由中性灰的底色台阶和发丝细边撑起来，四层：窗口底、内容面板、详情卡片、日志终端。颜色只有两种用途：任务和卡的状态，以及火焰标志的朱砂。

它在 Windows 和以后的 macOS 上长得一样。窗口用纯色，不用云母、亚克力或半透明；标题栏自己画，平台差异只剩窗口按钮的位置。信息密度优先：任务一行 30px，数字一律等宽对齐，空卡条直接把每张卡的利用率、占用者和 5 分钟曲线摊开。主题跟随系统，深浅两套同样完整，日志终端在两种主题下都是深色。

**Key Characteristics:**
- 四层底色台阶 + 发丝细边，投影只托住浮起的面板
- 颜色只说状态：运行蓝、成功绿、失败红、外部占卡琥珀；朱砂只给火焰
- 反色墨主按钮，选中项是底色加一圈内描边
- Geist / Geist Mono 随安装包打包，编号、数字、命令、路径、日志用等宽
- 热力色块的空卡条，失败未读的红点，托盘图标三态
- 动效克制：转圈、呼吸、状态闪一下、详情淡入；系统"减少动态效果"时全关

## Colors

中性灰阶加四个状态色，深浅两套成对出现；朱砂色只出现在火焰标志上。

### Primary
- **反色墨 Inverted Ink**（浅色 `ink-light`，深色 `ink-dark`）：正文和主按钮。主按钮在浅色模式是黑底白字，深色模式是白底黑字。它是唯一的"强调"，不用任何彩色代替。

### Secondary
- **运行蓝 Run Blue**（`run-*`）：运行中的状态图标、进度条、GPU 热力色块的底色（按利用率 4%–34% 混进卡片底色）、利用率小曲线。
- **成功绿 OK Green**（`ok-*`）：成功的状态和在线圆点。
- **失败红 Fail Red**（`fail-*`）：失败的状态、未读红点、连不上的服务器、危险按钮的文字、托盘浮窗里未读失败的淡红底条（8% 混入）。
- **外部琥珀 External Amber**（`warn-*`）：被外部进程占用的卡（14% 底色加 60% 描边）、"有未提交的改动"、被挡住的排队任务。

### Tertiary
- **朱砂 Cinnabar**（`cinnabar`）：只用于火焰标志（标题栏、托盘浮窗、应用和托盘图标）。

### Neutral
- **窗口底 Window**（`window-*`）：最外层，标题栏和左侧导航直接画在它上面。
- **内容面板 Panel**（`panel-*`）：总览、历史、右侧详情、服务器卡片、设置分组。
- **详情卡片 Card**（`card-*`）：面板里的第二层，任务的关键信息、命令历史、空闲的 GPU 格子。
- **悬停 Hover**（`hover-*`）：悬停、选中行、分段选择器的底。
- **文字三级**：主文字 `ink-*`，次文字 `ink-2-*`，弱文字 `ink-3-*`（时间、卡号、说明）。
- **发丝细边 Hairline**（`hairline-*`，加强版 `hairline-strong-*`）：面板描边、分隔线、按钮和输入框的边。
- **日志终端 Terminal**（`terminal-*`）：两种主题下都是深色。

### Named Rules
**The Status-Only Color Rule.** 颜色只表示状态。选中项、主按钮、导航高亮都用中性色；想强调就用字重、底色台阶或细边。

**The One Flame Rule.** 朱砂只出现在火焰标志上，从不用于按钮、链接、背景或强调。

**The Paired Theme Rule.** 每个颜色都有深浅两个值，一起改。弱文字在两套主题下都要在面板底上达到 4.5:1。

## Typography

**Body Font:** Geist Variable（中文回落到微软雅黑 / 苹方）
**Mono Font:** Geist Mono Variable，数字用 `tabular-nums`

**Character:** 一对同源的几何无衬线和等宽：界面文字安静、紧凑，数据和日志用等宽，读起来像终端但不显得生硬。等宽只给真正的数据，不当装饰。

### Hierarchy
- **Title**（600，15px，1.5）：右侧详情里的任务名、提交表单的标题。
- **Heading**（600，13px，1.5）：页面名（历史、服务器、设置）、卡片和分组标题、对话框标题。
- **Body**（400，12.5px，1.5）：界面正文、按钮、任务名（列表里加粗到 500）。
- **Data**（等宽，11.5px）：任务编号 `gpu1-42`、命令、路径、进度百分比、时长、显存。
- **Micro**（等宽，10.5–11px）：GPU 卡号、显存、快捷键提示、筛选计数。

### Named Rules
**The Data-Is-Mono Rule.** 编号、数字、命令、路径、日志一律等宽，并且数字等宽对齐，列才能竖着读。

## Layout

固定结构：自绘标题栏（38px）→ 左侧 40px 窄导航（总览、历史、服务器，设置贴底）→ 主体。主体四周留 8px，面板之间 8px 间距。总览和历史是一块铺满的内容面板，选中任务或新建任务时右侧出现约 540px（最多 46%）的第二块面板。服务器和设置页是居中的单列（最宽 860px / 720px）。

密度：任务一行 30px，行间 1px；面板内边距 10–16px；控件高 28px（筛选芯片 24px）。空卡条每台服务器一行，格子最小 118px，自动换行。最小窗口 1000×620：列表按自己的宽度取舍列（容器查询），窄到 640px 以下先收起项目列，540px 以下再收起卡号列，任务名至少留 96px。

## Elevation & Depth

以底色台阶为主，投影为辅。内容面板有一圈发丝细边加很淡的投影（浅色 `0 1px 2px` + `0 4px 16px`，4% 黑；深色只有 `0 1px 2px` 30% 黑）；详情卡片只靠底色和细边，不加投影。真正浮起来的东西才有明显投影：下拉菜单、对话框、提示条（浅色 `0 8px 28px` 14% 黑，深色再加一圈白 6% 的描边）。按钮不做高光，不做立体。

### Named Rules
**The Steps-Not-Shadows Rule.** 区分层次先用底色台阶和细边；投影只给浮在内容上面的东西。

## Shapes

圆角按层级递减：内容面板和详情卡片 10px，日志终端 8px，按钮、输入框、筛选项、任务行、GPU 格子 6px，键帽 4px；托盘浮窗用系统窗口圆角。状态图标是 lucide 线性图标（转圈的弧、虚线圈、对勾、叉、斜杠圈），一致的描边粗细。没有胶囊形大按钮，没有彩色侧边条。

## Components

### Buttons
- **Shape:** 6px 圆角，高 28px，左右 10px，图标 14px。
- **Primary:** 反色墨实心（浅色黑底白字 / 深色白底黑字），悬停降到 90% 不透明。每屏最多一个，例如"新建任务""提交到 gpu1"。
- **Secondary:** 面板底 + 加强细边，悬停换成悬停底色。
- **Danger:** 和次按钮同形，只把文字染成失败红；必须配确认对话框。
- **Ghost:** 无边无底，次文字色，悬停出底色。用于关闭、"更多"、W&B 链接。
- **快捷键提示:** 按钮里带一个 Kbd（`N`、`C`、`Ctrl+Enter`）。

### Chips
- **筛选:** 24px 高的文字按钮，没选值时是次文字，选了值时出现悬停底色和一圈内描边；下拉菜单项左边是对勾。
- **分段选择:** 悬停底色的槽里放按钮，选中的是面板底加一圈细描边。

### Cards / Containers
- **Corner Style:** 10px。
- **Background:** 内容面板用 `panel-*`，面板里的信息块用 `card-*`。
- **Shadow Strategy:** 见 Elevation & Depth。
- **Border:** 发丝细边。
- **Internal Padding:** 10–16px。

### Inputs / Fields
- **Style:** 28px 高，面板底，加强细边，6px 圆角；命令、路径、环境变量用等宽。
- **Focus:** 细边加深到弱文字色，不用彩色光晕；键盘焦点统一是 2px 弱文字色描边。
- **Error / Disabled:** 错误写在字段下面或表单底部，用失败红文字；禁用降到 45–50% 不透明。

### Navigation
- **标题栏:** 左边火焰标志和"丹炉"，中间筛选框（`Ctrl K` / `/` 聚焦），Windows 右边是 46px 宽的窗口按钮，关闭按钮悬停是系统红；空白处拖动窗口。
- **左侧导航:** 32px 的图标按钮，当前页是面板底加一圈细描边，其余是弱文字色。

### 任务行
30px 单行：状态图标、等宽编号、项目、任务名（500）、卡号、进度或状态、时间、未读红点。运行中的显示 4px 细进度条和百分比；选中是悬停底色加一圈内描边；状态变化时整行用新状态色（22% 混入）闪一下，1.2 秒淡出。

### GPU 热力色块
每张卡一个格子：卡号、占用者（任务编号或外部进程用户）、利用率、5 分钟小曲线、显存。底色按利用率把运行蓝混进卡片底色；外部进程占用的整格换成琥珀并描边。离线服务器整排降到 45% 不透明，名字下面写"5m 前"。新数据从右边推入。

### 日志终端
深色终端，8px 圆角，32px 工具栏：左边"实时"呼吸圆点（结束后变灰写"已结束"），右边搜索（`Ctrl F`，命中用高亮 API 标出，当前命中是实心琥珀）、加载更早、自动换行、下载。离开底部时右下角出现白色"回到底部"。

### 托盘浮窗
340×480，从上到下：未读失败的淡红底条、各台的迷你热力块和空卡数、运行中的任务和进度条，底部是"打开主窗口"主按钮和全局快捷键。弹出时淡入并从 98% 放大到 100%，只看不操作。

## Do's and Don'ts

### Do:
- **Do** 用底色台阶（窗口底 → 面板 → 卡片 → 终端）和发丝细边分层。
- **Do** 让编号、数字、命令、路径、日志用 Geist Mono 并等宽对齐数字。
- **Do** 每个颜色同时给深浅两个值，弱文字在面板底上保持 4.5:1 以上。
- **Do** 给每个高频操作配快捷键，并在按钮上用 Kbd 标出来。
- **Do** 在系统开启"减少动态效果"时关掉所有动画和过渡。

### Don't:
- **Don't** 用彩色做选中、主按钮或导航高亮；颜色只说状态。
- **Don't** 在火焰标志以外的地方用朱砂或任何橙色。
- **Don't** 用云母、亚克力、毛玻璃或半透明窗口。
- **Don't** 给详情卡片、按钮加投影或高光。
- **Don't** 为了好看降低信息密度：任务保持一行，空卡条保持展开。
- **Don't** 给取消任务这类不能撤回的操作配快捷键；必须点按钮并确认。
