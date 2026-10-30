# Danlu 单服务器 Demo 方案 v1

> 目标：在一台 GPU 服务器上运行一个 `danlu` 程序。电脑和安卓手机通过 **公网 IP + 端口 + 安全入口** 打开网页，就能**提交、排队、查看、取消、重跑**训练任务，并且**能查到每次任务实际跑的是哪一版代码**。
> 长期规划见 [DESIGN.md](DESIGN.md)。Demo 直接用正式版的技术栈，做完后在它上面继续迭代，不会推倒重写。

## 0. 完成标准（全部通过才算 demo 做完）

1. 电脑和安卓手机都能通过 `https://<公网IP>:<端口>/<安全入口>` 登录。**不经过安全入口时，访问任何地址都返回 404**；密码连续输错 5 次，锁定 15 分钟。
2. 在一台 4 卡机器上连续提交 3 个 2 卡任务：前两个同时开始跑，第三个排队；前面有任务结束后，第三个自动开始。
3. 在服务器上手动启动一个占着某张卡的进程，danlu 不会把这张卡分配出去。
4. 执行 `systemctl --user restart danlu` 重启 danlu 后，**正在跑的训练不中断**，网页上的状态也能恢复。
5. 取消一个 4 卡的 torchrun 任务后，所有 worker 进程都退出，显存全部释放。
6. 任务详情页能看到：实时日志（tqdm 进度条不刷屏）、W&B/SwanLab 链接、代码快照、git commit 和 diff。
7. 点"重跑"后，可以先改参数再提交。

## 1. 范围

**要做**：安全入口和登录、HTTPS、GPU 面板、在网页上提交任务、先来先跑的整卡调度、任务执行（含 conda 环境、danlu 重启后接管任务、取消）、实时日志、代码快照、重跑、手机适配。

**不做**：多服务器、通知、CLI、在浏览器里上传代码、优先级、任务依赖、自动重试、参数扫描、告警、PWA、OpenAPI 代码生成。

**有余力再做**：两次任务之间的代码和参数 diff；"用当时的代码重跑"；申请 Let's Encrypt 的 IP 证书；TOTP 两步验证。

## 2. 使用流程

```bash
# 1. 把二进制拷到服务器（在 Windows 上交叉编译好）
scp danlu-linux-amd64 server:~/bin/danlu

# 2. 初始化（交互式，所有问题都有默认值，直接回车即可）
$ danlu init
  面板端口 [随机: 28457]:
  公网 IP [自动检测: 203.0.113.10]:
  用户名 [admin]:
  密码 [回车自动生成]:
  conda 路径 [自动检测: /home/me/miniconda3]:
  ✔ 已写入配置 ~/.danlu/config.toml
  ✔ 已生成本地 CA 和服务器证书（只对 203.0.113.10 有效）
  ──────────────────────────────────────────
  访问地址  https://203.0.113.10:28457/k3x9p2qa7m4z
  用户名    admin
  密码      Xr7#…（只显示这一次）
  ⚠ 请在防火墙 / 云安全组放行 TCP 28457
  ──────────────────────────────────────────

# 3. 安装成 systemd 用户服务（开机自启、退出 SSH 后继续运行）
$ danlu install-service

# 以后需要时使用
$ danlu info              # 重新显示访问地址（密码不会再显示）
$ danlu passwd            # 修改密码
$ danlu entrance --reset  # 换一个新的安全入口
$ danlu port 30001        # 修改端口
```

之后的日常使用：
1. 照旧在本地改代码，用 rsync 推到服务器上。
2. 打开网页，点"新建任务"，选择工作目录，填好 conda 环境、命令和卡数，提交。
3. 在电脑或手机上查看进度和日志，需要时取消或改参数重跑。
4. 想知道某个结果是哪一版代码跑出来的：在任务详情页的"代码"标签里查看，或者在 W&B 上按 `danlu-<id>` 这个标签找回对应任务。

## 3. 访问与安全

这个面板能远程执行任意命令，所以安全措施分四层：

| 层 | 做法 | 防的是什么 |
|---|---|---|
| **端口** | `init` 时随机生成一个 20000–60000 之间的端口，也可以自己指定 | 减少常见端口上的扫描 |
| **安全入口** | 一段随机路径，如 `/k3x9p2qa7m4z`，类似 1Panel / 宝塔的同名功能 | 扫描器和随便访问的人只能看到 404，**认不出这是什么服务** |
| **登录** | 用户名 + 密码（bcrypt 哈希存储）；登录失败按 IP 限速 | **真正的防线**；能挡住暴力破解 |
| **HTTPS** | 默认用本地 CA 签发证书 | 公网上传输的密码、会话和入口路径都会被加密 |

### 3.1 安全入口的工作方式

```
GET /k3x9p2qa7m4z  ──►  设置 gate cookie（HMAC 签名，HttpOnly，有效期和会话一样是 7 天）──► 302 跳到 /
其他任何路径，如果没有有效的 gate cookie  ──►  404，响应体为空，和普通的 404 一样
有 gate cookie，但没有登录          ──►  页面跳转到 /login；/api/* 返回 401
登录成功                           ──►  设置会话 cookie（随机 32 字节，存在服务端，HttpOnly + Secure + SameSite=Strict，7 天有效）
```

- 前端的静态资源（JS/CSS）同样需要先过入口才能访问，所以扫描器拿不到任何能识别出 danlu 的特征。
- 防暴力破解：同一个 IP 连续失败 5 次，锁定 15 分钟；每次失败都写入审计日志。
- 防 CSRF：会话 cookie 设了 SameSite=Strict，而且所有写操作都要求 `Content-Type: application/json`。
- **入口只负责"藏起来"，并不保证安全**。如果入口路径泄露了，还有密码、HTTPS 和限速三层防护。

### 3.2 HTTPS：本地 CA（默认，不需要域名）

- `init` 时生成一个 **"Danlu Local CA"**，再用它给服务器签发证书。这个 CA 带有 **NameConstraints，只能为这台服务器的 IP 签证书**。这样即使 CA 私钥泄露，攻击者也无法用它冒充其他网站。
- **不安装 CA**：浏览器第一次访问时会提示"连接不是私密连接"，点"继续前往"即可。传输仍然是加密的，只是无法防御主动的中间人攻击。
- **安装 CA（推荐，只需做一次）**：登录后在"设置"页下载 `danlu-ca.crt`。
  - 安卓：设置 → 安全 → 加密与凭据 → 安装证书 → CA 证书；
  - Windows：双击证书，安装到"受信任的根证书颁发机构"。
  - 装好之后不再有任何警告，并且能防中间人攻击。
- 其他模式（在配置文件里切换）：
  - `file`：使用你自己的证书，比如以后有了域名；
  - `off`：只用 HTTP，**仅限本地测试**，启动时会打出醒目的警告。
- 公网 IP 变了之后，执行 `danlu cert --regen` 重新生成 CA 和服务器证书。因为 CA 被限定只能给旧 IP 签证书，**各设备上要删掉旧 CA、装上新的**。

### 3.3 其他

- **以普通用户身份运行**，不需要 root：端口大于 1024，任务直接用你自己的账号、conda 环境和文件权限执行。
- 审计日志会记录这些事件：登录成功和失败、提交任务、取消任务、修改设置。

## 4. 功能设计

### 4.1 GPU 面板

- 每 2 秒执行一次 `nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw --format=csv,noheader,nounits`。
- 同时执行 `nvidia-smi --query-compute-apps=pid,gpu_uuid,used_memory --format=csv,noheader,nounits`，再读取每个进程 `/proc/<pid>/environ` 里的 `DANLU_JOB_ID`：
  - 属于某个正在运行、并且分到了这张卡的 danlu 任务 → 这张卡显示为**该任务占用**；
  - 其他情况（没有标记、任务已结束、跑到了没分给它的卡上）→ 显示为**外部进程**（附上 pid 和进程所属用户），调度时不分配这张卡。
  - 为什么不用进程组：torchrun 会把每个 worker 放进**新的 session**，进程组追踪不到它们；环境变量会一路继承下去，靠它识别最可靠。
- **假 GPU 模式**：设置 `DANLU_FAKE_GPUS=4` 后会模拟 4 张卡，利用率和显存随机波动；设置 `DANLU_FAKE_BUSY=2` 可以模拟第 2 号卡被外部进程占用。用于在 WSL 或没有显卡的机器上测试。

### 4.2 提交任务

| 字段 | 说明 |
|---|---|
| 任务名 | 必填，默认取工作目录名加上时间 |
| 工作目录 | 服务器上的绝对路径，输入框有自动补全（列出子目录），并记住最近用过的 10 个 |
| conda 环境 | 下拉选择；列表来自 `conda env list --json` 的扫描结果，每 5 分钟刷新一次；也可以选"不使用 conda" |
| 命令 | 多行文本，作为 bash 脚本执行，可以写多条命令 |
| GPU | 两种写法二选一："张数 N"（默认），或者"指定卡号"，如 `0,1` |
| 环境变量 | 可选，`KEY=VALUE` 键值对 |

提交前会做校验：工作目录必须存在；GPU 张数不能超过总卡数；指定的卡号必须存在。

### 4.3 调度

- **严格的先来先跑**：按提交顺序检查排在最前面的任务，放得下就启动，放不下就继续等，**后面的任务不能插队**。这样大任务不会一直被小任务挤掉。（demo 先这样做，以后再加优先级和插队。）
- 可用的卡 = 全部的卡 − 正在运行的 danlu 任务占用的卡 − 被外部进程占用的卡。
- "张数 N" 模式取编号最小的 N 张可用卡；"指定卡号" 模式要等这几张卡全部空闲才启动。
- 调度循环在两种情况下触发：每 2 秒定时一次；以及提交、任务结束、取消时立即触发一次。
- 正在排队的任务可以取消，取消后直接从队列里移除。

### 4.4 执行

每个任务在 `~/.danlu/run/<id>/` 下生成一个包装脚本 `run.sh`：

```bash
#!/usr/bin/env bash
# 原子写入退出码：先写临时文件再改名，danlu 不会读到写了一半的内容
finish() { echo "$1" > "$DANLU_RUN_DIR/exitcode.tmp" && mv "$DANLU_RUN_DIR/exitcode.tmp" "$DANLU_RUN_DIR/exitcode"; exit "$1"; }
cd "$DANLU_WORKDIR" || { echo "[danlu] 工作目录不存在"; finish 1; }
source "<conda_base>/etc/profile.d/conda.sh" && conda activate "<env>" \
  || { echo "[danlu] conda activate 失败"; finish 127; }
bash "$DANLU_RUN_DIR/cmd.sh"          # 用户填写的命令
finish $?
```

- **启动方式**：`setsid` 新建一个进程组；stdin 指向 `/dev/null`；stdout 和 stderr 合并写入 `logs/<id>.log`。记录 pid，以及 `/proc/<pid>/stat` 里的进程启动时间，用来防止 pid 被复用时认错进程。
- **注入的环境变量**：

  | 变量 | 值 | 作用 |
  |---|---|---|
  | `CUDA_VISIBLE_DEVICES` | 分到的卡号 | 限定任务只能看到这几张卡 |
  | `CUDA_DEVICE_ORDER` | `PCI_BUS_ID` | **CUDA 默认按算力给卡编号，可能和 nvidia-smi 的编号对不上**，统一按 PCI 总线顺序 |
  | `PET_MASTER_PORT` | 一个空闲端口 | torchrun 自动读取它作为 `--master_port`，避免多个任务撞端口 |
  | `PYTHONUNBUFFERED=1` | | **否则输出重定向到文件时，Python 的 print 会被缓冲，日志要很久才出现** |
  | `DANLU_JOB_ID`、`DANLU_JOB_NAME` | | 方便在代码里引用 |
  | `WANDB_TAGS` | 追加 `danlu-<id>` | 在 W&B 里按标签找回任务 |

- **取消**：向整个进程组，以及所有带 `DANLU_JOB_ID=<id>` 标记的进程发 SIGTERM，等 30 秒（可配置），还没退出就发 SIGKILL。
- **收尾**：任务结束后，扫描 `/proc`，找出仍带着这个任务标记的残留进程（比如 DDP 的孤儿 worker）并全部结束掉，在任务上注明"已清理 N 个残留进程"。收尾时只按标记清理、不按进程组号清理，因为包装脚本退出后，进程组号可能已经被别的进程复用了。
- **重启后接管**：danlu 启动时，检查所有状态为 running 的任务：
  - pid 还在，并且进程启动时间和记录的一致 → 继续监控（改为每 2 秒轮询一次）；
  - 进程已经不在了 → 读取 `exitcode` 文件来确定结果；
  - 进程不在，也没有 `exitcode` 文件 → 标记为失败，原因写"进程被外部终止"。
- ⚠ **systemd 的坑**：systemd 停止服务时，默认会杀掉这个服务 cgroup 里的所有进程，**训练进程也在其中**。所以 unit 文件里必须写 `KillMode=process`。`install-service` 生成 unit 文件时会自动加上这一行，还会执行 `loginctl enable-linger`，保证退出 SSH 后服务继续运行。
- **结束状态的判定**：退出码为 0 → succeeded；不为 0 → failed；用户取消的 → cancelled（以用户取消为准，不看退出码）。

### 4.5 日志

- 打开任务时，先加载日志最后 256KB；之后通过 SSE 接收新增的内容。页面上有"加载更早内容"和"下载完整日志"两个按钮。
- **在前端处理 `\r`**：每一行只保留最后一个 `\r` 后面的内容，所以 tqdm 进度条会在同一行原地刷新，不会刷屏。ANSI 颜色用 `ansi_up` 渲染。
- 跟随模式：页面停在底部时自动滚动；用户往上翻看时暂停滚动，并显示"回到底部"按钮。
- **链接识别**：服务端在读取日志时，用正则匹配 `https://wandb.ai/…/runs/…` 和 `https://swanlab.cn/…`，找到第一个匹配后就存下来。
- 任务列表里显示每个任务的**最后一行日志**（跳过 danlu 自己打印的开始/结束提示）：运行中的任务看进度，结束的任务通常就是最终指标，在手机上扫一眼就够。

### 4.6 代码快照

在**任务启动的前一刻**生成快照，所以记录的就是实际运行的那份代码：

1. 判断工作目录是不是 git 仓库：
   - **是**：记录 `git rev-parse HEAD`、分支名、`git status --porcelain`，以及 `git diff HEAD`（跳过二进制文件）。要打包的文件列表用 `git ls-files -co --exclude-standard` 得到，即已跟踪的文件，加上未跟踪但没被 `.gitignore` 忽略的文件。
   - **不是**：遍历整个目录，跳过 `.git`、`__pycache__`、`wandb`、`swanlog`、`node_modules`、`.ipynb_checkpoints` 这些目录。
2. 过滤：跳过大于 1MB 的单个文件；累计超过 50MB 就停止，并在快照里标记"已截断"。两个阈值都可以配置。
3. 打包成 `snapshots/<id>.tar.gz`，同时写入 `meta.json`，内容包括：文件清单、git 信息、被跳过的文件及原因。
4. 详情页的"代码"标签里可以：浏览文件树、查看文件内容（带语法高亮）、查看 git diff、下载 tar.gz。
5. 快照**不会阻塞任务启动太久**：普通代码仓库只需要不到 1 秒；超过 10 秒会放弃快照，在任务里注明"快照超时"，然后照常启动任务。

### 4.7 重跑

- 点"重跑"会打开提交表单，并预填原任务的名称（自动加上 `-r2` 这样的后缀）、工作目录、conda 环境、命令、GPU 设置和环境变量。改完后提交，新任务会记下 `rerun_of = <原任务 id>`。
- **重跑使用的是工作目录里当前的代码**。如果当前 git commit 和原任务的快照不一致，或者当前有未提交的改动，表单上会给出提示。
- "用当时的代码重跑"放在"有余力再做"里。

## 5. 页面（同时适配电脑和手机）

| 页面 | 电脑 | 手机 |
|---|---|---|
| **登录** | 用户名、密码 | 同左 |
| **首页** | 一行状态摘要（运行中、排队、空闲卡数）；GPU 面板每行 4 张；下面是一个分组列表：运行中、排队、最近完成，每行一个任务 | GPU 每张卡一行；分组列表直接展开，不用标签页切换 |
| **新建 / 重跑** | 标签在左、输入框在右的表单；Ctrl+Enter 提交 | 上下排列的表单 |
| **任务详情** | 标题和"W&B / 重跑 / 取消"按钮；一排关键信息（状态、GPU、环境、时长、代码版本、时间）；下面三个标签页：日志、详情、代码 | 同左，关键信息排成两列 |
| **全部任务** | 和首页同样的任务行，支持按状态筛选、搜索（快捷键 /）、分页 | 同左，每个任务两行 |
| **设置** | 服务信息、外观（跟随系统 / 浅色 / 深色）、下载 CA 证书、修改密码、审计日志 | 同左 |

GPU 卡片显示的内容：卡号、型号、利用率条、显存（已用/总量）、温度、功耗，以及当前占用者（"任务名"链接 / "外部进程 pid" / "空闲"）。
数据刷新：GPU 状态和任务列表每 3 秒轮询一次（TanStack Query）；日志通过 SSE 实时推送。

视觉规范见 [../web/DESIGN.md](../web/DESIGN.md)：中性灰阶，颜色只用来表示状态（排队灰色虚线圈、运行蓝色转圈、成功绿色对勾、失败红色叉、取消灰色斜杠、外部占卡琥珀色），系统字体，支持深色模式。快捷键：N 新建任务，/ 搜索任务。

## 6. API

```
GET  /<entrance>                       通过安全入口（设置 gate cookie）
POST /api/login        POST /api/logout        GET /api/me

GET  /api/gpus                         GPU 状态和占用者
GET  /api/conda-envs                   conda 环境列表
GET  /api/fs/dirs?path=                目录自动补全
GET  /api/workdirs/recent              最近用过的工作目录

GET  /api/jobs?status=&q=&page=&ended_after=  任务列表（ended_after 是 unix 秒，只要之后结束的；失败任务带 error_line 报错行）
POST /api/jobs                         提交任务
GET  /api/jobs/{id}                    任务详情
POST /api/jobs/{id}/cancel             取消任务
GET  /api/jobs/{id}/log?offset=&limit= 分段读取日志
GET  /api/jobs/{id}/log/stream         实时日志（SSE）
GET  /api/jobs/{id}/snapshot           快照文件清单和 git 信息
GET  /api/jobs/{id}/snapshot/file?path=  快照中单个文件的内容
GET  /api/jobs/{id}/snapshot/diff      git diff
GET  /api/jobs/{id}/snapshot/download  下载 tar.gz

GET  /api/settings/ca.crt              下载 CA 证书
POST /api/settings/password            修改密码
GET  /api/audit?page=                  审计日志
```

## 7. 配置与数据

```toml
# ~/.danlu/config.toml
[server]
port      = 28457
entrance  = "k3x9p2qa7m4z"
public_ip = "203.0.113.10"

[tls]
mode = "local-ca"            # local-ca | file | off
# cert_file = ""; key_file = ""

[auth]
username      = "admin"
password_hash = "$2a$12$…"
session_ttl   = "168h"
max_failures  = 5
lockout       = "15m"

[runtime]
conda_base         = "/home/me/miniconda3"
kill_grace         = "30s"
snapshot_max_file_mb  = 1
snapshot_max_total_mb = 50
```

```
~/.danlu/
  config.toml   secret.key（HMAC 密钥）
  danlu.db                           # SQLite
  tls/ca.crt ca.key server.crt server.key
  logs/<id>.log
  snapshots/<id>.tar.gz
  run/<id>/run.sh cmd.sh exitcode
```

```
jobs(id, name, workdir, conda_env, cmd, env_json,
     gpus_req, gpus_pinned, gpus_assigned, status,
     master_port, pid, pid_starttime, exit_code, fail_reason, tracker_url, last_line,
     git_commit, git_branch, git_dirty, snapshot_path, snapshot_note, rerun_of,
     created_at, started_at, ended_at)
sessions(token_hash, created_at, expires_at, ip, user_agent)
audit(id, ts, ip, action, detail)
login_failures(ip, count, locked_until)
recent_workdirs(path, last_used_at)
```

## 8. 技术实现与项目结构

| 部分 | 选型 |
|---|---|
| 后端 | Go 1.25，路由直接用标准库 `net/http`，**纯 Go 不用 cgo**，可以在 Windows 上交叉编译出 Linux 版本 |
| 数据库 | `modernc.org/sqlite`（纯 Go 实现） |
| 密码 | `golang.org/x/crypto/bcrypt` |
| 证书 | 标准库 `crypto/x509` |
| 前端 | React + Vite + TypeScript + Tailwind + shadcn/ui + TanStack Query + ansi_up + highlight.js |
| 打包 | `go:embed` 把前端打进二进制，最终只有一个文件 |

```
D:\Projects\danlu\
  cmd/danlu/main.go        # 子命令：init / serve / info / passwd / entrance / port / cert / install-service
  internal/config/         # 读写配置，init 交互流程
  internal/auth/           # 安全入口、登录、会话、限速、审计
  internal/tlsutil/        # 本地 CA 和服务器证书
  internal/gpu/            # 解析 nvidia-smi，假 GPU 模式
  internal/store/          # SQLite 和数据库迁移
  internal/scheduler/      # 先来先跑的调度
  internal/runner/         # 包装脚本、进程组、接管、取消
  internal/snapshot/       # 代码快照、git 信息
  internal/logs/           # 日志分段读取、SSE、链接识别
  internal/api/            # HTTP 路由和处理函数
  web/                     # React 前端
  deploy/danlu.service     # systemd 用户服务模板（带 KillMode=process）
  scripts/build.ps1        # 构建前端 + 交叉编译 linux/amd64
  docs/DEMO.md  docs/DESIGN.md
```

## 9. 开发步骤（每一步都能单独验证）

| # | 内容 | 怎么验证 |
|---|---|---|
| 1 | 项目骨架：Go 模块加 Vite 前端，前端嵌入二进制，`danlu serve` 能打开页面 | 在 WSL 里运行后能打开页面 |
| 2 | `init` 流程、配置文件、本地 CA、HTTPS、安全入口、登录、限速 | 不带入口访问返回 404；带入口能登录；输错 5 次后被锁定 |
| 3 | GPU 采集（真实 + 假 GPU）、`/api/gpus`、首页 GPU 卡片 | 假 GPU 模式下卡片会刷新；模拟占用的卡显示为"外部进程" |
| 4 | SQLite、提交任务和任务列表的接口、调度循环、提交表单 | 能提交任务，排队顺序正确 |
| 5 | runner：包装脚本、conda 环境、取消、重启后接管 | 用假任务在 WSL 里测试：取消；`kill -9 danlu` 再重启后能接管 |
| 6 | 任务详情、日志 SSE、`\r` 处理、链接识别、重跑 | 用模拟 tqdm 输出的脚本测试 |
| 7 | 代码快照、git 信息、代码浏览和 diff | 在 git 仓库和非 git 目录各提交一次 |
| 8 | `install-service`、交叉编译、部署到真服务器 | 逐条走完第 0 节的 7 条完成标准 |

**测试**：
- **单元测试**：nvidia-smi 输出解析、调度时的分卡逻辑、快照的文件筛选、入口和登录中间件、日志的 `\r` 处理。
- **WSL 集成测试**：假 GPU 模式加上一组假任务脚本（sleep、模拟 tqdm、故意报错退出、生成子进程等），覆盖取消、接管和退出码。
- **真机验证**：用一个很小的 torchrun DDP 脚本跑 2 卡；再用 `python -c "import torch,time; torch.zeros(1).cuda(); time.sleep(600)"` 制造一个外部占卡进程。

预计代码量：Go 约 2000 行，前端约 2000 行。

## 10. 风险与已知限制

| 项 | 说明 |
|---|---|
| 证书警告 | 不安装本地 CA 时，每次打开浏览器都会看到一次警告。装一次 CA 就能解决 |
| 安全入口不是加密 | 入口路径泄露后，就只剩密码 + HTTPS + 限速三层防护，所以密码必须足够强（`init` 时默认随机生成） |
| systemd 杀进程 | 必须设置 `KillMode=process`，否则重启 danlu 时会把训练进程一起杀掉。已在 4.4 节处理 |
| 快照的时间点 | 快照在任务启动时生成。如果任务运行过程中修改了代码，后面才被 import 的模块可能和快照不一致（这种情况很少见） |
| 严格先来先跑 | 排在队首的大任务会挡住后面的小任务。这是有意为之，以后再加插队开关 |
| 单用户 | 所有任务都以运行 danlu 的那个账号执行 |
| nvidia-smi 开销 | 每次调用约 50 到 200 毫秒，按 2 秒一次的频率可以接受 |
