# Danlu 单服务器 Demo 方案 v1

> 目标：在一台 GPU 服务器上运行一个 `danlu` 程序。电脑和安卓手机通过 **公网 IP + 端口 + 安全入口** 打开网页，就能**提交、排队、查看、取消、重跑**训练任务，并且**能查到每次任务实际跑的是哪一版代码**。
> 长期规划见 [DESIGN.md](DESIGN.md)。Demo 直接用正式版的技术栈，做完后在它上面继续迭代，不会推倒重写。
> 状态：第 9 节的 1–7 步都已完成，在 WSL 里用假 GPU 跑通了端到端测试（`scripts/e2e.sh`）。2026-11-02 用户反馈正在日常使用，暂未遇到问题；第 8 步的完整真机验收记录仍待补齐，改为随使用逐项补验，不阻塞后续开发（见 ROADMAP.md）。
> 多服务器由桌面客户端负责，见 [CLIENT.md](CLIENT.md)。客户端要求服务器端做的改动（设备令牌、`info --json`、游标翻页等）已经合进本文的接口和命令。
> 接下来先迭代后端和面板，客户端暂停新功能。工作项、先后顺序和完成标准见 [ROADMAP.md](ROADMAP.md)。

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

**不做（Demo 范围）**：多服务器、通知、CLI、在浏览器里上传代码、优先级、任务依赖、自动重试、参数扫描、告警、PWA、OpenAPI 代码生成。其中多服务器后来交给了桌面客户端（[CLIENT.md](CLIENT.md)），面板本身仍然不做；通知在 Demo 阶段未实现，后来纳入 S2 并已完成 ntfy 手机推送（见 [ROADMAP.md](ROADMAP.md) 和 [PHASES.md](PHASES.md)）。其他后续工作按路线图推进。

**后续定版**：机器体检和 TOTP 两步验证，见 [ROADMAP.md](ROADMAP.md)。

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
  手机扫码打开面板：
  [二维码]

# 3. 安装成 systemd 用户服务（开机自启、退出 SSH 后继续运行）
$ danlu install-service

# 以后需要时使用
$ danlu info              # 重新显示访问地址和二维码（密码不会再显示）
$ danlu info --no-qr      # 只显示文字信息
$ danlu passwd            # 修改密码
$ danlu entrance --reset  # 换一个新的安全入口
$ danlu port 30001        # 修改端口
$ danlu restart           # 改完配置后重启服务（训练不中断）
```

完整的命令列表（包括设备令牌和升级用的 `replace`）见 [README](../README.md#常用命令)。

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
- **数据不可信时不分卡**：两次查询各有 8 秒超时，超时连子进程一起杀掉。查不到卡上的进程、`nvidia-smi` 出错，或者超过 20 秒没有一次采集完成（驱动卡死时 `nvidia-smi` 可能连信号都杀不掉），`/api/gpus` 都带 `error`，面板提示"暂停分配新任务"，调度器不再分配 GPU；排在前面、不用卡的任务照常启动。
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
| Bash 环境 | 可选，启动时加载服务器用户的 `~/.bashrc`，默认关闭 |

提交前会做校验：工作目录必须存在；GPU 张数不能超过总卡数；指定的卡号必须存在。

### 4.3 调度

- **严格的先来先跑**：按提交顺序检查排在最前面的任务，放得下就启动，放不下就继续等，**后面的任务不能插队**。这样大任务不会一直被小任务挤掉。（demo 先这样做，以后再加优先级和插队。）
- 可用的卡 = 全部的卡 − 正在运行的 danlu 任务占用的卡 − 被外部进程占用的卡。
- "张数 N" 模式取编号最小的 N 张可用卡；"指定卡号" 模式要等这几张卡全部空闲才启动。
- 调度循环在两种情况下触发：每 2 秒定时一次；以及提交、任务结束、取消时立即触发一次。
- 正在排队的任务可以取消，取消后直接从队列里移除。
- 首页排队分组可以手动暂停队列；首页和任务页会显示暂停原因，点“继续队列”恢复。暂停时仍可提交任务，已经占位或运行中的任务继续准备、运行和收尾，尚在排队的任务不再启动。暂停状态存入数据库，重启后保留。
- 任务从本次启动占位到失败收尾不到 `runtime.fail_fast`（默认 2 分钟），且仍有任务排队时，自动暂停队列，`kind` 为 `fail_fast`，并记录触发任务和失败原因。加载环境、写入输出清单或启动进程失败也计入；用户取消、尚未启动就被校验为无效的任务不触发。设为 `"0s"` 可关闭，只能手动恢复。若任务在磁盘暂停期间快速失败，会改为需要人工确认的快速失败暂停。
- 数据目录所在分区的可用空间低于 `runtime.disk_min_gb`（默认 2 GiB）时暂停，`kind` 为 `disk`；达到阈值的两倍后自动恢复。磁盘暂停尚未落库时也立即在内存生效并重试。采集失败或超过 3 分钟未更新时不启动新任务；保护开启时不能手动绕过不足的容量，恢复接口返回 409 和原因。已占位、运行的任务继续，手动与快速失败暂停不因磁盘恢复而解除。

### 4.4 执行

每个任务在 `~/.danlu/run/<id>/` 下生成一个包装脚本 `run.sh`：

```bash
#!/usr/bin/env bash
# 第一件事是写 pid 文件：danlu 在启动途中退出时，重启后靠它把进程认回来
echo $$ > "$DANLU_RUN_DIR/pid.tmp" && mv -f "$DANLU_RUN_DIR/pid.tmp" "$DANLU_RUN_DIR/pid"
# 原子写入退出码：先写临时文件再改名，danlu 不会读到写了一半的内容
finish() { echo "$1" > "$DANLU_RUN_DIR/exitcode.tmp" && mv "$DANLU_RUN_DIR/exitcode.tmp" "$DANLU_RUN_DIR/exitcode"; exit "$1"; }
cd "$DANLU_WORKDIR" || { echo "[danlu] 工作目录不存在"; finish 1; }
source "<conda_base>/etc/profile.d/conda.sh" && conda activate "<env>" \
  || { echo "[danlu] conda activate 失败"; finish 127; }
bash "$DANLU_RUN_DIR/cmd.sh"          # 用户填写的命令
finish $?
```

- **启动分三步**，慢的事不占着调度锁：
  1. 占位（锁内）：选好卡和端口，任务写成 running、pid 为 0，同时记下开机标识 `boot_id`。写库失败就不启动，下一轮再试；
  2. 准备（锁外）：做快照、加载 `.bashrc`，都有超时。这期间提交、取消、其他任务的收尾都不用等；
  3. 启动（锁内）：这期间被取消了就直接记为已取消，danlu 正在停止就放回队列；否则 fork/exec，毫秒级。
- **启动方式**：`setsid` 新建一个进程组；stdin 指向 `/dev/null`；stdout 和 stderr 合并写入 `logs/<id>.log`。记录 pid，以及 `/proc/<pid>/stat` 里的进程启动时间，用来防止 pid 被复用时认错进程；读不到启动时间时改为核对进程的 `DANLU_JOB_ID`。启动后写库失败时，任务留在内存里接着监控，下一轮重试写库，不会再启动一次。
- **注入的环境变量**：

  | 变量 | 值 | 作用 |
  |---|---|---|
  | `CUDA_VISIBLE_DEVICES` | 分到的卡号 | 限定任务只能看到这几张卡 |
  | `CUDA_DEVICE_ORDER` | `PCI_BUS_ID` | **CUDA 默认按算力给卡编号，可能和 nvidia-smi 的编号对不上**，统一按 PCI 总线顺序 |
  | `PET_MASTER_PORT` | 一个空闲端口 | torchrun 自动读取它作为 `--master_port`，避免多个任务撞端口 |
  | `PYTHONUNBUFFERED=1` | | **否则输出重定向到文件时，Python 的 print 会被缓冲，日志要很久才出现** |
  | `DANLU_JOB_ID`、`DANLU_JOB_NAME` | | 方便在代码里引用 |
  | `DANLU_OUTPUT_DIR` | `<output_root>/<项目>/<任务 id>-<任务名>/`（默认 `~/danlu-outputs`） | 保存模型、checkpoint 等训练产物；danlu 会在其中维护 `danlu.json`，不会清理该目录 |
  | `WANDB_TAGS` | 追加 `danlu-<id>` | 在 W&B 里按标签找回任务 |

- 勾选“Bash 环境”时，danlu 用交互式 Bash 加载服务器用户的 `~/.bashrc`，把其中导出的变量加入任务环境。任务表单里的自定义变量优先于 `.bashrc`，danlu 管理的变量仍由 danlu 注入。文件缺失、加载失败或超过 10 秒时，任务会启动失败。`.bashrc` 里用 `&` 起的后台进程，读完环境后会被结束掉，不会留在服务器上。

- **取消**：向整个进程组，以及所有带 `DANLU_JOB_ID=<id>` 标记的进程发 SIGTERM，等 30 秒（可配置），还没退出就发 SIGKILL。
- **收尾**：任务结束后，扫描 `/proc`，找出仍带着这个任务标记的残留进程（比如 DDP 的孤儿 worker）并全部结束掉，在任务上注明"已清理 N 个残留进程"。收尾时只按标记清理、不按进程组号清理，因为包装脚本退出后，进程组号可能已经被别的进程复用了。
- **重启后接管**：danlu 启动时，检查所有状态为 running 的任务：
  - pid 还在，并且进程启动时间和记录的一致 → 继续监控（改为每 2 秒轮询一次）；
  - 进程已经不在了 → 读取 `exitcode` 文件来确定结果；
  - 进程不在，也没有 `exitcode` 文件 → 标记为失败。开机标识变了，原因写"服务器重启，任务中断"，否则写"进程被外部终止"；
  - pid 为 0（占位以后 danlu 就退出了）→ 有 pid 文件、进程还在就认领；跑过、已经结束的按上面两条收尾；从没启动过的放回队列。
- **环境记录恢复**：服务重启会将中断的环境包采集标为未完成，包括已结束任务，避免永久显示“采集中”并阻止清理。
- **停止服务**：收到 SIGTERM 后，先断开日志 SSE 这类长连接，等正在处理的请求完成（最多 5 秒），再等正在启动的任务落库，最后才关数据库。正在跑的训练不受影响。
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
- 重跑默认使用工作目录里的当前代码。git commit 变化或存在未提交改动时，表单会提示。
- 切换到源任务的快照后，在 `run/<id>/code/` 恢复代码。数据、权重等快照之外的文件通过链接从原目录读取，绝对路径仍指向原处。新任务记录 `code_from` 和需留意的补齐文件，并照常记录这次运行的环境。

### 4.8 对比任务

在任务列表点“对比”并勾选两个任务，或从详情页选同目录的近期任务。对比页显示命令、环境变量、代码、包版本和机器信息。展开文件查看文本差异。快照被清理后，任务记录和环境仍可用于对比。

## 5. 页面（同时适配电脑和手机）

| 页面 | 电脑 | 手机 |
|---|---|---|
| **登录** | 用户名、密码 | 同左 |
| **首页** | 一行状态摘要（运行中、排队、空闲卡数）；GPU 面板每行 4 张；CPU 和内存当前使用率及最近 1 分钟趋势；下面是一个分组列表：运行中、排队、最近完成，每行一个任务 | GPU 每张卡一行；分组列表直接展开，不用标签页切换；CPU 和内存当前值及趋势并排显示，放在任务列表后面 |
| **新建 / 重跑** | 标签在左、输入框在右的表单；Ctrl+Enter 提交 | 上下排列的表单 |
| **任务详情** | 标题和"W&B / 重跑 / 取消"按钮；一排关键信息（状态、GPU、环境、时长、代码版本、时间）；下面三个标签页：日志、详情、代码 | 同左，关键信息排成两列 |
| **终端** | 可在最多 3 个会话间切换；支持调整字号和断线重连 | 同左，增加命令输入框与常用控制键 |
| **全部任务** | 和首页同样的任务行，支持按状态筛选、搜索（快捷键 /）、分页；粘贴绝对路径可反查任务 | 同左，每个任务两行 |
| **任务对比** | 并列显示两次运行，展开文件查看文本差异 | 各区块上下排列，命令分上下两栏 |
| **设置** | 服务信息、外观（跟随系统 / 浅色 / 深色）、下载 CA 证书、修改密码、审计日志 | 同左 |

GPU 卡片显示的内容：卡号、型号、利用率条、显存（已用/总量）、温度、功耗，以及当前占用者（"任务名"链接 / "外部进程 pid" / "空闲"）。
数据刷新：GPU 状态和任务列表每 3 秒轮询一次（TanStack Query）；日志通过 SSE 实时推送。
CPU 和内存由服务端每 2 秒采样一次，首页每 3 秒读取，并保留最近 1 分钟的数据。

视觉规范见 [../web/DESIGN.md](../web/DESIGN.md)：中性灰阶，颜色只用来表示状态（排队灰色虚线圈、运行蓝色转圈、成功绿色对勾、失败红色叉、取消灰色斜杠、外部占卡琥珀色），系统字体，支持深色模式。快捷键：N 新建任务，/ 搜索任务。

## 6. API

```
GET  /<entrance>                       通过安全入口（设置 gate cookie）
POST /api/login                       用户名、密码；启用 TOTP 时还需 code
                                      缺少或错误验证码返回 401 和 need_totp=true
POST /api/logout
GET  /api/me                           用户名、版本、api_version（接口版本号，不兼容的改动时加一）
GET  /api/doctor                       机器体检，返回 checks 数组（ok / warn / fail）

GET  /api/gpus                         GPU 状态和占用者；出错时含 error、error_since（unix 秒），恢复后清除
GET  /api/queue                        队列状态（paused、kind、reason、since、job_id）
POST /api/queue/pause                  手动暂停队列（JSON {}，不影响已经占位或运行的任务）
POST /api/queue/resume                 恢复队列（JSON {}）
GET  /api/system                       CPU、内存采样，以及磁盘余量和采集状态
GET  /api/conda-envs                   conda 环境列表
GET  /api/fs/dirs?path=                目录自动补全
GET  /api/workdirs/recent              最近用过的工作目录

GET  /api/jobs?status=&q=&page=&ended_after=  任务列表（ended_after 是 unix 秒，只要之后结束的；失败任务带 fail_kind、error_line 和 traceback_offset）
GET  /api/jobs?order=ended&before=&before_id=  只要已结束的任务，按结束时间倒序；before / before_id 是上一页最后一条的结束时间和 id（游标翻页，多台服务器的历史按时间合并时用）
GET  /api/trace?path=                  从绝对路径反查任务，返回匹配来源（output_dir、danlu_json、guess）
GET  /api/compare?a=&b=                两次任务的命令词、环境变量差异、快照文件差异、包版本及配方
POST /api/jobs                         提交任务
GET  /api/jobs/{id}                    任务详情
GET  /api/jobs/{id}/env                任务记录的环境包列表原文
POST /api/jobs/{id}/cancel             取消任务
POST /api/jobs/{id}/star               标星或取消标星（JSON {"starred":true|false}）
GET  /api/jobs/{id}/log?offset=&limit= 分段读取日志
GET  /api/jobs/{id}/log/stream         实时日志（SSE）
GET  /api/jobs/{id}/snapshot           快照文件清单和 git 信息
GET  /api/jobs/{id}/snapshot/file?path=  快照中单个文件的内容
GET  /api/jobs/{id}/snapshot/diff      git diff
GET  /api/jobs/{id}/snapshot/download  下载 tar.gz

GET  /api/settings/ca.crt              下载 CA 证书
GET  /api/settings/qr.svg              完整安全入口地址的二维码（需登录，禁止缓存）
GET  /api/settings                    服务配置摘要（含 terminal_enabled、totp_enabled）和 notify（enabled、last_sent_at、last_error；不含 topic 和令牌）
POST /api/settings/password            修改密码
GET  /api/terminal                     列出终端会话
POST /api/terminal                     打开终端（JSON {"job_id"?: number}；可在任务目录和 conda 环境中启动）
GET  /api/terminal/{id}/ws?offset=     终端 WebSocket（二进制输入输出；文本帧传 resize、offset 控制消息；offset 是已收到的输出末尾，只补发之后的部分；新连接接替旧连接，旧连接以 4001 关闭）
POST /api/terminal/{id}/close          关闭终端会话
POST /api/notify/test                 发一条测试推送；未开启或正在测试返回 409，发送失败返回 502
GET  /api/audit?page=                  审计日志
GET  /api/tokens                       设备令牌列表（不含令牌本身）
POST /api/tokens/{id}/revoke           吊销设备令牌，立即生效
```

桌面客户端不走 cookie，而是带 `Authorization: Bearer <设备令牌>`：令牌有效就跳过安全入口和登录检查，无效则和没过入口一样返回空 404。令牌只能在服务器上用 `danlu token create` 创建（能 SSH 登录才说明是本人），面板上只能查看和吊销。

`GET /api/system` 的 `disks` 数组包含 `id`（设备号）、`path`、`paths`（同一分区上被监测的路径）、`total_bytes`、`available_bytes`（普通用户实际可用）、`data`、`warn` 和可选的 `error`。`disks_updated_at` 是采样时间；整个磁盘采集失败或过期时增加 `disks_error`，不影响 CPU/内存数据。

任务对象增加 `starred` 和 `pruned_at`（未清理为 null）。列表支持 `starred=true`。清理中的任务不能改标星，返回 409；已清理任务的日志、SSE、日志下载及快照接口返回 410 和原因，环境包接口仍可用。

`POST /api/jobs` 可用 `code_from` 指定源快照任务，提交的工作目录必须与源任务一致。任务列表支持 `workdir` 精确筛选，容许末尾斜杠。对比的 `env` 和 `packages` 中 `a` / `b` 为 null 表示该侧不存在。快照或包不可用时返回 `code.error` 或 `packages_error`，其他区块仍可用。

`GET /api/doctor` 和 `danlu doctor --json` 调用同一套检查，覆盖 GPU/驱动、conda、systemd、linger、磁盘、证书、端口、时间同步和版本。`danlu doctor` 有 fail 项时退出码为 1；`--json` 跑完即返回 0，结论都在 JSON 里。TOTP 通过 `danlu totp enable|disable` 管理；登录验证码错误和重放都会计入 IP 登录限速，密码正确、还没填验证码的那一步不计入；设备令牌不受影响。

排队任务视图增加 `wait_reason`，说明队首在等空闲卡、指定卡、外部进程或可信的 GPU/磁盘数据；其他排队任务说明在等前面的任务。面板列表和详情页会显示，启动后清除。

手机推送通过 `danlu notify setup` 配置，生成随机 topic、打印订阅地址和二维码；重复运行保留订阅，`--new-topic` 主动轮换，`--disable` 关闭。自建服务用 `--server`，已有 topic 用 `--topic`，令牌通过 `--token-stdin` 读取。改完重启服务；`danlu notify test` 使用磁盘上的配置发测试，面板测试使用当前服务进程的配置。

通知消费持久化事件，30 秒窗口内的任务失败合并，自动队列暂停即时发送。告警只在出现和恢复时记录，覆盖磁盘低余量、持续 60 秒的 GPU 错误及恢复、训练 GPU 的外部进程和被外部进程阻塞的队首。消息使用结构化原因，不带日志内容、文件路径或安全入口。首次启用/更换 topic 不补发历史；游标、待发批次和发送认领保存在 `settings`，发送失败重试三次后记录并继续。强制中断导致回执未知的发送不在重启时重发，最后错误明确说明结果未知；HTTP 回执丢失时无法实现严格的外部恰好一次投递。

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
username      = "admin"      # 密码哈希不在这里，存在数据库的 settings 表里（danlu passwd 修改）
session_ttl   = "168h"
max_failures  = 5
lockout       = "15m"

[runtime]
conda_base         = "/home/me/miniconda3"
output_root        = "~/danlu-outputs"
kill_grace         = "30s"
fail_fast          = "2m"     # 启动后不足这个时长就失败且仍有任务排队时暂停；0s 关闭
disk_warn_gb       = 20       # GiB，0 关闭低余量提示
disk_min_gb        = 2        # GiB，0 关闭调度保护；磁盘暂停在两倍余量时恢复
snapshot_max_file_mb  = 1
snapshot_max_total_mb = 50

[retention]
keep_days          = 90      # 0 关闭按时间清理
max_total_gb       = 50      # GiB，0 关闭按大小清理；两项均为 0 时关闭自动清理

[notify]
ntfy              = ""      # 留空关闭；danlu notify setup 生成随机 topic
# token           = ""      # 自建服务需要鉴权时填写，不通过 API 暴露

[terminal]
enabled = true               # 网页终端；关闭后终端 API 返回 404，面板隐藏入口
```

网页终端使用 Linux PTY，同一时间最多 3 个会话。WebSocket 只接受同源连接，且需要已登录的面板会话；断线后 shell 保留 15 分钟，每个会话缓存最近 256 KiB 输出，重连时只补发没收到的部分；同一会话只保留最新的连接，服务器每 20 秒 ping 一次，尽快发现手机锁屏或换网络后断掉的连接。任务详情的"打开终端"会使用任务工作目录和 conda 环境。审计日志记录会话打开/关闭和工作目录，不记录输入输出内容。danlu 重启会关闭终端进程，不影响训练任务。

`danlu init` 和 `danlu info` 默认在终端打印带安全入口的访问二维码，`--no-qr` 可关闭。设置页的"在手机上打开"通过 `GET /api/settings/qr.svg` 显示同一地址；接口要求登录并返回 `Cache-Control: private, no-store`。本地 CA 区域根据浏览器平台预选 Android、iOS、macOS、Windows 或其他平台的安装步骤，也可手动切换，并显示 SHA-256 指纹供导入前核对。

```
~/.danlu/
  config.toml   secret.key（HMAC 密钥）
  danlu.db                           # SQLite
  backup/danlu-<时间>-v<旧版本>.db   # 升级表结构前的备份，保留最近 5 份
  tls/ca.crt ca.key server.crt server.key
  logs/<id>.log
  envs/<sha256>.txt                  # 环境包清单，按内容哈希去重
  snapshots/<id>.tar.gz <id>.meta.json <id>.diff   # 代码、清单和 git 信息、未提交的改动
  run/<id>/run.sh cmd.sh pid exitcode               # pid 是包装脚本第一行写的，启动途中 danlu 退出时靠它认领
~/danlu-outputs/<项目>/<id>-<任务名>/danlu.json # 默认输出目录；记录任务、代码、GPU 和最终状态
```

服务进程从不写 `config.toml`，只有命令行（`init`、`port`、`entrance`、`cert`）写它，改完重启生效。运行中会变的状态放在数据库的 `settings` 表里。旧版本放在 `[auth] password_hash` 的哈希，新版本第一次启动时导入一次，`danlu passwd` 会把它从配置里删掉。

```
jobs(id, name, workdir, conda_env, cmd, env_json, inherit_bashrc,
     gpus_req, gpus_pinned, gpus_assigned, status,
     master_port, pid, pid_starttime, exit_code, fail_reason, fail_kind, notes, ext_seen, oom_base,
     traceback_offset, recipe_json, output_dir, starred, pruned_at, pruning, code_from, cancel_requested, tracker_url,
     git_commit, git_branch, git_dirty, snapshot_ok, snapshot_note, rerun_of,
     created_at, started_at, ended_at)
sessions(token_hash, created_at, expires_at, ip, user_agent)
audit(id, ts, ip, action, detail)
login_failures(ip, count, locked_until)
recent_workdirs(path, last_used_at)
device_tokens(id, name, token_hash, created_at, last_used_at, last_ip)   -- 第 2 次迁移，同时加了 jobs(ended_at) 索引
-- 第 3 次迁移：给 jobs 增加 inherit_bashrc，已有任务默认关闭
-- 第 4 次迁移：给 jobs 增加 boot_id（占位时的开机标识）
settings(key, value, updated_at)   -- 第 5 次迁移：运行时状态，含 auth.password_hash、auth.totp_secret、queue.state
events(id, ts, kind, key, job_id, data, dedupe_key) -- 第 6 次迁移：状态变化事件，与任务状态在同一事务写入
-- 第 7 次迁移：给 jobs 增加失败分类、外部占卡、OOM 基线和 traceback 偏移
-- 第 8 次迁移：给 jobs 增加 recipe_json，记录环境包哈希、机器信息或采集错误
-- 第 9 次迁移：给 jobs 增加 output_dir
-- 第 10 次迁移：给 jobs 增加 starred、pruned_at、pruning（清理认领标记，不通过 API 暴露）
-- 第 11 次迁移：给 jobs 增加 code_from 和待恢复快照的索引
totp_used_steps(step)              -- 第 12 次迁移：防止相同 TOTP 时间窗的验证码重放
```

`jobs.notes` 与 `fail_reason` 分开：前者存残留进程清理等备注，后者只存失败原因。迁移会把旧记录中混在 `fail_reason` 里的清理说明移到 `notes`。`fail_kind` 保存失败分类；`ext_seen` 表示任务运行时是否观察到所用 GPU 上有其他进程；`oom_base` 保存启动前的 cgroup `oom_kill` 计数；`traceback_offset` 是日志末尾 4 MiB 内首个 traceback 的字节偏移；`recipe_json` 记录环境包哈希、机器信息或采集错误；`output_dir` 记录任务输出目录。`events` 用递增 id 作游标；job 事件由调度器和任务状态在同一事务写入，去重键保证重试不会重复。队列状态与 `queue_paused`、`queue_resumed` 事件同事务写入，重复暂停/恢复不重复记事件。快速失败的任务状态、结束事件、暂停状态和暂停事件一起提交；写库失败时内存中的暂停立即生效并重试，未保存成功前不能继续队列。`alert_raised`、`alert_cleared` 按 key 的最后一条事件恢复告警状态，只在边沿写入；`ntfy.cursor`、`ntfy.state`、`ntfy.last_sent_at`、`ntfy.last_error` 保存通知消费及发送状态，不新增迁移。

最后一行日志和失败时的报错行不存数据库，列任务时从日志文件末尾读出来。traceback 偏移在任务结束时从日志末尾 4 MiB 中找出并记录，避免每次列任务都扫描日志。快照文件按任务编号放在 `snapshots/` 下，表里只记 `snapshot_ok`。迁移只能往后追加，按 `PRAGMA user_version` 执行。

清理在启动时和之后每 6 小时运行，清理未标星的已结束任务的 `logs/<id>.log`、三种快照文件和 `run/<id>/`，只删除没有任何任务引用的环境包文件。包采集中和任务运行时不做环境包 GC；已清理任务的环境包引用仍然保留。按总量清理时从最旧的任务开始，运行中、标星和仍在采集包的任务不作为候选，因此总量上限不保证可以达到。训练输出不计入总量，也不删除；输出路径与待删路径重叠时整项跳过并报错。文件操作受数据目录句柄约束，管理子目录是符号链接时拒绝清理，任务内的符号链接只删除链接本身。Linux 文件锁阻止服务和 CLI 同时清理，数据库认领标记避免标星竞态，并支持恢复中断的清理。

`danlu prune --dry-run [--json]` 预览配置规则；`--older-than 72h` 只按指定结束时长清理任务，不额外应用总量规则，`0s` 表示所有已结束任务。预览不删任务文件或写清理标记；实际清理后任务记录保留，补上 `pruned_at`，面板显示清理时间。

## 8. 技术实现与项目结构

| 部分 | 选型 |
|---|---|
| 后端 | Go 1.26，路由直接用标准库 `net/http`，**纯 Go 不用 cgo**，可以在 Windows 上交叉编译出 Linux 版本 |
| 数据库 | `modernc.org/sqlite`（纯 Go 实现） |
| 密码 | `golang.org/x/crypto/bcrypt` |
| 证书 | 标准库 `crypto/x509` |
| 前端 | React 19 + Vite + TypeScript + Tailwind v4 + react-router + TanStack Query + highlight.js；接口类型、日志处理（tqdm、ansi_up）放在和桌面客户端共享的 `packages/core` |
| 打包 | `go:embed` 把前端打进二进制，最终只有一个文件 |

```
D:\Projects\danlu\
  cmd/danlu/main.go        # 所有子命令：init / serve / info / token / passwd / totp / doctor / entrance / port / cert /
                           #   restart / logs / replace / install-service；systemd unit 也在这里生成（带 KillMode=process）
  internal/config/         # 读写配置，init 交互流程
  internal/auth/           # 安全入口、登录、会话、设备令牌、限速、审计
  internal/tlsutil/        # 本地 CA 和服务器证书
  internal/gpu/            # 解析 nvidia-smi，假 GPU 模式
  internal/system/         # 从 /proc/stat 和 /proc/meminfo 采集 CPU 与内存
  internal/procfs/         # 读 /proc：进程启动时间、环境变量里的任务标记
  internal/store/          # SQLite 和数据库迁移
  internal/trace/          # 从输出目录、任务清单或命令路径反查任务
  internal/doctor/         # CLI 与设置页共用的服务器体检
  internal/totp/           # RFC 6238 登录验证码
  internal/scheduler/      # 先来先跑的调度
  internal/runner/         # 包装脚本、进程组、接管、取消
  internal/snapshot/       # 代码快照、git 信息
  internal/logs/           # 日志分段读取、SSE、链接识别
  internal/api/            # HTTP 路由和处理函数
  internal/webui/          # 嵌入前端构建产物
  web/                     # 面板前端
  packages/core/           # 面板和客户端共享的 TS 代码
  desktop/                 # 桌面客户端（Tauri）
  scripts/                 # build.ps1 / build.sh（构建前端 + 交叉编译）、e2e.sh、dev-wsl.sh
  docs/DEMO.md  docs/DESIGN.md  docs/CLIENT.md  docs/ROADMAP.md
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
