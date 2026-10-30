# Danlu（丹炉）· 方案草稿 v0.4

> 炼丹师的训练任务管理器：在多台 GPU 服务器上排队、运行和追溯训练任务。**不需要主控节点。**
> 状态：长期规划，带 ❓ 的是待确认项。变更记录见文末。
> **当前先做单服务器 demo，范围见 [DEMO.md](DEMO.md)。**

## 1. 已确认的需求

| 维度 | 结论 |
|---|---|
| 目的 | 先解决自己的痛点，能用最重要 |
| 用户 | 第一阶段只有自己；数据模型预留多用户 |
| 机器 | 多台服务器，**不设主控**。任务要么指定服务器，要么自动分配。**每台都有公网 IP**，人和服务器都在洛杉矶 |
| 现在怎么跑 | SSH + tmux/nohup + conda，`python train.py` / `torchrun` |
| 任务形态 | 单机多卡为主，1 天以内 |
| 代码同步 | 在本地（Windows）改代码，用 rsync/scp 推上去 |
| 数据 | 存在各台服务器的本地磁盘；暂时不需要下载输出 |
| 界面 | **以 GUI 为主**（电脑浏览器和安卓都要能用），另配 CLI |
| 手机 | 以安卓为主 |
| 通知 | 可选。默认用 ntfy（不需要域名），Web Push 以后再说 |
| 指标 | 复用 W&B / SwanLab |
| 当前重点 | **训练任务管理**；监控和告警往后放 |
| 最痛的点 | **找不到某次结果对应哪版代码** |
| 开发方式 | AI 辅助开发，技术栈优先选 AI 擅长的 |

## 2. 不做什么

- 不做跨机分布式训练，一个任务只在一台机器上跑。
- 不做 K8s 或容器编排。
- 不做指标存储和曲线。
- 不做通用 MLOps 平台。
- 不做中心化的全局调度器。

## 3. 参考项目与可借鉴的点

| 项目 | 借鉴什么 |
|---|---|
| gflow | 命令行设计；依赖语法 `--depends-on @`；参数扫描；hold/release；**卡上只要有外部计算进程，就不分配这张卡**；MCP server |
| gpuq | WebUI 的信息结构；REST 接口 + OpenAPI；**fallback_wait**；排队中的任务可以编辑 |
| SkyPilot / dstack | 提交时自动同步工作目录；节点标签 |

## 4. 架构：对等节点，没有主控

```
  电脑浏览器 / 安卓 PWA / Windows CLI
                │  HTTPS：连哪台都行（每台都有公网 IP）
                ▼
  ┌─────────── gpu1: danlu node ───────────┐        ┌─── gpu2: danlu node ───┐
  │ API + GUI                              │  peer  │  完全相同的程序         │
  │ 本机队列 / 调度 / 执行                 │◄──────►│                        │
  │ 本机快照库 / 日志 / SQLite             │  API   └────────────────────────┘
  │ 聚合和转发（无状态）                   │◄──────► ┌── gpu3 ... ──┐
  └────────────────────────────────────────┘         └──────────────┘
```

**四条原则**
1. **每个节点自治**：队列、数据库、代码快照、日志都在本机。一台机器单独部署就是完整可用的产品，这正好就是 M1 的交付物。
2. **节点之间对等**：通过配置里的 `peers` 互相认识。节点之间的请求走 TLS，并用集群密钥认证；证书指纹直接写在配置里，不需要域名。
3. **任意一台都可以当入口**：请求的目标是自己就直接处理，是其他节点就转发过去；列表类的查询会同时发给所有节点，结果合并后返回。**入口节点不保存任何全局状态。** 入口挂了就换一台连，其他节点上正在跑的任务和排着的队都不受影响。
4. **任务一经提交就归属某个节点**，ID 形如 `gpu1-42`，只在这台机器上排队和运行。

```toml
# /etc/danlu/danlu.toml（示例）
node_name    = "gpu1"
listen       = "0.0.0.0:443"         # GUI / API，HTTPS
peer_listen  = "0.0.0.0:7601"        # 节点之间互访；防火墙只放行其他节点的 IP
outputs_root = "/data/runs"
labels       = { gpu = "a100", data = "imagenet,coco" }
cluster_key  = "/etc/danlu/cluster.key"

[tls]
mode = "acme-ip"     # 默认：Let's Encrypt IP 证书，有效期 6 天，自动续期，不需要域名
# mode = "acme-domain"; domain = "gpu1.example.com"   # 有域名时更稳
# mode = "manual"; cert = "…"; key = "…"

[notify]                               # 留空 = 不推送
ntfy_topic = "danlu-<长随机串>"         # 所有节点填同一个

[[peers]]
name = "gpu2"
url  = "https://<gpu2 公网 IP>:7601"
cert_sha256 = "…"
```
加一台新机器：在新机器上装好 danlu，然后在任意一台已有节点上执行 `danlu peer add gpu3 https://… --fingerprint …`，节点信息会同步给其他 peers。

### 和"有主控"方案相比，少了什么、怎么补

| 少了什么 | 影响 | 怎么补 |
|---|---|---|
| 全局队列 | 自动分配只在提交那一刻做决定；之后别的机器空出来了，任务也不会自己挪过去 | ① 分配时不只看当前空闲卡，也看排队情况；② GUI 里可以一键把排队中的任务"移到另一台"；③（M3，可选）空闲节点向邻居认领排队中的自动任务（work stealing）。认领时所有权会做一次明确交接，**同一时刻只有一个所有者，不会重复运行** |
| 集中的历史记录 | 历史分散在各个节点上 | GUI 同时查询所有节点再合并显示。某台机器下线时，它的历史暂时看不到，但输出目录里的 `danlu.json` 一直都在 |
| 集中的账号系统 | 以后做多用户时，每台机器都要有账号 | 单用户阶段：把同一份凭据配置部署到每台机器；到多用户阶段再接 OIDC 或做配置同步 |
| 节点主动连主控（对 NAT 友好） | 入口节点要能访问其他节点 | 每台都有公网 IP，可以直接互访；peer 端口用防火墙限制为只允许其他节点的 IP 访问 |

### 技术栈（以 AI 辅助开发为前提）

| 层 | 选型 | 理由 |
|---|---|---|
| 节点程序 / CLI | **Go**，单个二进制 | AI 写 Go 的质量高，编译器能拦下很多错误；单个静态二进制方便分发，也不受服务器上 conda 环境的影响 |
| Web GUI | **React + TypeScript + Vite + shadcn/ui + TanStack Query** | AI 最熟悉的前端组合；同一套代码同时适配电脑和手机，还能装成 PWA；构建产物嵌入 Go 二进制，每个节点都自带 GUI |
| 接口契约 | **OpenAPI**，Go 服务端和 TS 客户端都从它生成 | AI 改接口时两端不会对不上 |
| 存储 | 每个节点一个 SQLite | 不需要额外部署数据库 |
| 部署 / HTTPS | systemd；**HTTPS 内置在 danlu 里**（用 certmagic，和 Caddy 是同一个库），默认申请 Let's Encrypt 的 IP 证书 | 不需要域名，也不用另装 Caddy；每台机器上仍然只有一个二进制 |

## 5. 核心：任务管理

### 5.1 提交与代码快照

- **CLI**：`danlu submit --node gpu2 -g 4 -e torch2 -n vit-lr3e-4 -- torchrun --nproc_per_node 4 train.py --lr 3e-4`。`--node` 可以写 `auto`，也可以在 `danlu.toml` 里配置默认值。
- **GUI（电脑）**：用 Chrome/Edge 的"选择文件夹"（File System Access API）选中本地项目。提交页会列出每台机器的空闲卡数，可以直接选一台，也可以选"自动"。
- **GUI（手机）**：基于某次任务改参数重跑，或者选择一个已有的快照提交。

快照流程：
1. 客户端扫描目录（遵守 `.gitignore` 和 `.danluignore`），对每个文件算哈希，**只把目标节点上还没有的文件传过去**。超过大小阈值的文件会被拒绝并提示原因。
2. 目标节点把这些文件存成一个不可变快照，同时记录 git commit、分支和未提交的 diff，然后用硬链接组装出运行目录。
3. **在另一台机器上重跑**时，目标节点会直接向源节点拉取缺少的文件，客户端不需要重新上传。

### 5.2 追溯："这个结果是哪版代码跑出来的？"

```
   输出目录 ◄──────► 任务 gpu1-42 ◄──────► W&B / SwanLab run
                        │
                        ▼
              代码快照 + git commit + diff
```

- 每个任务都会注入环境变量 `DANLU_OUTPUT_DIR=<outputs_root>/<project>/<job_id>-<name>/`，并在这个目录里**自动写一份 `danlu.json`**，内容包括任务 id、节点、快照 id、git commit、命令、卡号和时间。
- 注入 `WANDB_TAGS=danlu-gpu1-42`、`WANDB_NAME=<name>`，同时从日志里抓取 run 链接，这样任务和 W&B/SwanLab 上的 run 可以双向跳转。
- GUI 里可以浏览快照代码；**任意两次任务（可以在不同节点上）都能做代码 diff 和参数 diff**；也可以按 commit 或路径搜索。
- CLI：`danlu trace <路径>` 根据输出目录找到对应任务；`danlu checkout <job>` 把那次运行的代码取回本地。

### 5.3 调度

**节点内调度**（每个节点只管自己的卡）：
- 整卡独占，通过 `CUDA_VISIBLE_DEVICES` 分配。只要卡上有外部计算进程，这张卡就不可用。
- 先按优先级、再按提交顺序排队。排在前面的大任务放不下时，默认后面的任务不能插队；可以给单个任务打上"允许插队"标记。GUI 里可以拖动排序。
- 支持依赖（同一节点内用 `--after`；跨节点的依赖放到 M3）、hold/release、`--retries N`（每次重试是一个独立的 attempt），以及 fallback_wait（任务启动后很快就退出时，自动暂停本节点的队列）。

**自动分配**（`--node auto`，在提交那一刻由入口节点决定）：
1. 过滤：节点在线、标签满足要求（例如 `data=imagenet`）、所需 conda 环境存在、总卡数不少于 N。
2. 如果有节点**现在就能启动**这个任务（空闲卡 ≥ N，而且前面没有挡着的任务），就选放下之后剩余空闲卡最少的那台（best-fit）。
3. 如果都不能马上启动，就选"前面排队任务需要的总卡数 ÷ 节点总卡数"最小的那台。
4. GUI 和 CLI 会显示分配理由，例如"→ gpu2：当前空闲 4 卡"。

### 5.4 执行

- 用 `setsid` 启动独立的进程组，日志写文件，再由一层 wrapper 记录退出码。**节点程序重启后会根据 pid 和进程启动时间重新接管正在运行的任务。**
- conda 环境由节点定期扫描并上报，GUI 里可以直接下拉选择。
- 为每个任务分配一个空闲端口，通过 `PET_MASTER_PORT` 注入，避免多个 torchrun 任务端口冲突。
- 取消任务时先对整个进程组发 SIGTERM，宽限期过后再发 SIGKILL。任务结束后检查残留显存，有残留就把这张卡标记为"脏卡"。

### 5.5 GUI 页面

| 页面 | 内容 |
|---|---|
| 总览 | 每台机器一组 GPU 卡片，下面是所有节点合并后的运行中任务和排队列表 |
| 提交 | 选择文件夹或快照、命令（带历史补全）、卡数、**节点（含"自动"）**、conda 环境、优先级、依赖 |
| 任务详情 | 实时日志、进度、W&B/SwanLab 链接、快照代码、`danlu.json`；取消、重跑、改参数重跑、**移到另一台** |
| 历史 | 跨节点合并显示，可以筛选；选两条做对比（Monaco diff） |
| 节点 | peers 列表、在线状态、标签、conda 环境、版本；添加节点 |

手机端优先保证四件事：看进度、看日志、取消、重跑。

### 5.6 通知（可选，排在任务管理之后）

- **默认渠道：ntfy**。节点往 `ntfy.sh/<随机 topic>` 发一个 HTTP POST，手机装 ntfy App 订阅这个 topic 即可。**不需要域名，也不依赖我们自己的 HTTPS**，实现只要几十行代码。topic 用很长的随机串，消息里只写任务名和状态，不放敏感信息。
- **Web Push（可选，M3 之后再做）**：在洛杉矶，安卓 Chrome 能正常收到推送。但这要求 HTTPS，而且多个节点之间要共享 VAPID 密钥和订阅列表，在无主控架构下比较麻烦，所以往后排。
- 每个节点自己发通知，所有节点用同一份通知配置。
- 第一批事件：成功、失败、队列被 fallback_wait 暂停、peer 掉线。
- 第二批事件：OOM、卡死、NaN、磁盘快满、脏卡。
- 不想要通知就把配置留空，其他功能不受影响。

### 5.7 安全（每台都暴露在公网，这一节不能省）

- **不管要不要推送，HTTPS 都必须有。** 这个面板能远程执行任意命令。如果走明文 HTTP，密码和 token 在公网上传输时都是明文，一旦被截获，服务器就等于交给了别人。另外，PWA 安装和"选择文件夹提交"这两个功能，浏览器也只允许在 HTTPS 下使用。
- **不需要域名。** Let's Encrypt 从 2026 年 1 月起正式签发 IP 地址证书，有效期 6 天，必须自动续期。danlu 内置 certmagic，自动完成申请和续期；验证走 443 端口上的 TLS-ALPN，不用开放 80 端口。这项功能还比较新，社区里有人报告过问题，所以保留两条退路：一是用域名（免费的 DuckDNS 子域名，或者买一个约 10 美元/年的），二是手动指定证书。
- **登录**：首次启动时强制设置密码并启用 TOTP；登录失败要限速，次数过多就锁定；CLI token 可以随时吊销；所有操作都记入审计日志。
- **节点之间**：防火墙只允许其他节点的 IP 访问 peer 端口；对方证书的指纹固定写在配置里，同时用集群密钥认证；转发请求时带上原始用户身份，记进目标节点的审计日志。
- **对外只开三个端口**：443（GUI/API）、peer 端口（只对其他节点开放）、SSH。

## 6. 数据模型（每个节点各存一份）

```
users(id, name, password_hash, totp_secret)              -- 单用户阶段由配置文件下发
tokens(id, user_id, hash, revoked, last_used_at)
snapshots(id, owner_id, project, manifest_hash, git_commit, git_branch, git_diff, size, created_at)
blobs(hash, size)
jobs(id, seq, owner_id, project, name, tags, status, priority, queue_pos, allow_backfill,
     cmd, env_json, conda_env, snapshot_id, gpus_req, depends_on, retries_max,
     fallback_wait_s, time_limit_s, assigned_by[manual|auto], assign_reason,
     moved_from, output_dir, tracker_url, progress, created_at)
attempts(id, job_id, n, gpus, master_port, pid, pid_starttime,
         status, exit_code, fail_reason, started_at, ended_at, log_path)
events(id, ts, user_id, job_id, type, payload)
```

全局任务 ID 的格式是 `<node_name>-<seq>`。任务状态：`queued | held → running → succeeded | failed | cancelled | moved`。

## 7. 代码仓库结构

```
danlu/
  api/openapi.yaml       # 对外 API 和 peer API
  cmd/danlu/             # 单一入口：node 守护进程 + 客户端子命令
  internal/node/         # 本机队列、调度、执行、NVML、conda、快照库
  internal/federation/   # peers、聚合查询、请求转发、自动分配、任务迁移
  internal/client/       # CLI 和快照上传
  web/                   # React + Vite
  deploy/                # systemd unit、防火墙规则示例
```

## 8. 里程碑

**M1 · 单节点完整版**（一台服务器上就是完整产品）
- 用 CLI 提交并生成快照、多卡排队、conda 环境、日志、取消、重跑、重启后接管任务、`danlu.json`
- GUI：总览、任务详情、历史、快照代码浏览
- 登录 + TOTP、内置 HTTPS（IP 证书）

**M2 · 多节点联邦 + 完善 GUI 管理**
- peers、聚合查询、请求转发；`--node` 和自动分配；跨节点迁移任务和重跑
- GUI 选文件夹提交、改参数重跑、拖动排序、hold/release、依赖、重试、fallback_wait
- 代码 diff 和参数 diff；W&B 双向关联；第一批推送事件

**M3 · 稳定性**
- OOM、卡死、NaN、磁盘、脏卡告警；参数扫描；进度解析；跨节点依赖；work stealing（可选）

**M4 · 多用户与 AI 集成**
- 账号同步或 OIDC、配额；MCP server

## 9. 待确认 ❓（括号里是当前的默认做法，没有意见就按默认来）

1. 各台服务器上的数据集一样吗？（默认：支持给节点打标签，自动分配时可以按 `data=` 标签筛选；不打标签就不筛选）
2. GUI 用电脑浏览器 + 安卓 PWA，可以吗？（默认：可以）
3. 名字就叫 Danlu，可以吗？（默认：可以）
4. 一共几台机器？每台几张卡、什么型号？（只影响测试方案和默认参数）

---
变更记录
- v0.4：确认所有机器都有公网 IP（人和服务器都在洛杉矶）；HTTPS 内置到 danlu，默认使用 Let's Encrypt IP 证书，不需要域名；通知改为可选，默认用 ntfy；按公网暴露的情况加强安全措施。
- v0.3：去掉主控节点，改成对等节点架构；任务提交时指定节点或自动分配；补充"少了什么、怎么补"的对比；里程碑 M1 改为单节点完整版。
- v0.2：命名为 Danlu；采用 server + agent 多机架构；技术栈定为 Go + React/TS + OpenAPI；GUI 作为主界面；代码追溯定为第一优先级。
- v0.1：初稿（单机）。
