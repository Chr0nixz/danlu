---
version: 1
slug: "web-src"
primary_target: "src"
related_targets: []
---

# 丹炉 Web 界面（web/src）

范围：整个单页应用，包括总览、任务列表、任务详情、新建任务、设置和登录。模式：Operate。

用户要做的事：一眼看出哪些任务在跑、跑到哪了、有没有挂、哪张卡空着；一步进入任务详情，查看日志、代码版本和 W&B 链接，取消或重跑；在电脑或手机上提交新任务。手机和电脑同样重要。

定位：每台服务器自带的兜底控制面板；多台服务器的统一控制交给桌面客户端（desktop/）。所以效率第一：信息密、加载快、点击少，不做装饰。

约束：信息密度不能比现在低；页面不从外部 CDN 加载资源，也不额外打包字体；文案直白，只在空状态里少量用炼丹的说法。

## Direction contract

THESIS: 一块安静、密集的控制台，做工对标 Vercel 控制台、Linear 和 GitHub Actions。中性灰阶，颜色只用来表示状态；列表和表格优先，卡片只给 GPU 这类数据。不要现在这种粗圆角大卡片加橙色按钮的模板面板。

OWN-WORLD: 中性灰阶。浅色：页面 #fafafa，面板 #fff，1px 边线 #e8e8e8。深色：#0a0a0a、#111、#242424。主按钮反色实心（浅色模式黑底白字，深色模式白底黑字）；朱砂色只用在火焰标志上。状态用 lucide 图标，形状和颜色同时区分：排队是灰色虚线圈，运行是蓝色旋转的弧，成功是绿色对勾圈，失败是红色叉圈，取消是灰色斜杠圈；外部进程占卡用琥珀色。字体只用系统字体栈（系统无衬线 + 系统等宽，中文走系统黑体），数字一律等宽（tabular-nums）。控件圆角 6px，面板圆角 8px。

STORY: 打开就能读出在跑什么、跑到哪（最后一行日志和已运行时长）、哪张卡空着。点一下进入详情，看日志、代码版本和 W&B，取消或重跑。

FIRST VIEWPORT:
- 桌面：
  - 顶栏高 48px：标志、主机名、下划线式导航，右侧是主按钮"新建任务"。
  - 顶栏下面一句状态摘要。
  - 一排 GPU 面板，每张卡一个，紧凑：卡号和型号、利用率数字加细条、显存、温度和功耗，以及占卡的是哪个任务或哪个外部进程。
  - 下面是 Linear 式的分组任务列表，分运行中、排队、最近完成三组。每行依次是状态图标、#编号、名称、GPU 标记、最后一行日志、时长、commit。
- 手机：GPU 每张卡一行；任务分组直接展开，不用标签页切换。"新建任务"在底部导航的中间。
- 动效：状态变化时该行背景闪一次；运行图标匀速旋转；其他地方只有 150ms 的颜色过渡。

FORM: 类别标准（canon），用户在两轮方向选择后主动选择。参照 Vercel 控制台、Linear、GitHub Actions。seed key a397c6d2。

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
