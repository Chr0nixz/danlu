# Danlu 架构审阅与修复方案 v1

> 2026-11-01 对整个仓库做的一次架构审阅，范围包括 Go 后端、Web 面板、`packages/core` 和桌面客户端。本文记录发现的问题和建议的修复方案，按优先级排列，状态记在第 3 节的总表里。哪些项排进开发计划、排在什么时候，以 [ROADMAP.md](ROADMAP.md) 为准，第 8 节是建议的顺序。
>
> 文中的行号对应审阅时的工作区：提交 `e2b8c4e`，加上还没提交的主机状态（`/api/system`）改动。第 4、5 节描述的是修复前的代码，链接指向修复前的位置，只作记录；第 6、7 节里还没做的项，行号已经按阶段 A 之后的代码更新过。
>
> v2：阶段 A（第 8 节）已经做完。实现和原方案不一样的地方，在对应小节里用"实现说明"标出。

## 1. 结论

整体架构适合一个中小型项目，不需要换技术栈，也不需要重写。问题集中在两处：

1. **调度的关键路径在异常情况下不够稳。** 外部命令卡住、`nvidia-smi` 卡住、写库失败、启动途中 danlu 退出，这几种情况下，要么整个调度卡住，要么任务状态和实际对不上，要么一个任务被启动两次。这正是 ROADMAP 定版线第 2 项"异常路径"和验收场景 5 要覆盖的内容，最好在真机验证之前修掉。
2. **缺几个接口。** 定版线上的失败归类、ntfy 推送、快速失败暂停、完整配方、对比和复现、自动清理，都要用到统一的状态转换和事件记录、一个存放运行时状态的地方、按内容寻址的快照。先把这几个接口补上，后面的功能就不用各自临时拼一套。不补的话，这才是以后真正会积累下来的技术债。

审阅时做的验证：

| 检查 | 结果 |
|---|---|
| `go test ./cmd/... ./internal/...`（Windows） | 通过 |
| 同一批单测交叉编译成 Linux 版，在 WSL 里运行（包括 `*_linux_test.go`） | 通过 |
| `GOOS=linux go vet ./cmd/... ./internal/...` | 通过 |
| `pnpm typecheck`：`packages/core`、`web`、`desktop` | 通过 |
| `cargo test`（`desktop/src-tauri`） | 4 项通过，1 项按设计跳过 |
| `scripts/e2e.sh`（WSL，假 GPU） | 62 项全部通过 |
| 复现脚本（附录 A） | 第 4 节的 3 个问题全部复现 |

## 2. 保持不变的部分

### 2.1 做得对、要保持的

- 一个静态二进制，前端嵌在里面；纯 Go 的 SQLite，WAL 模式；迁移只追加。
- 包按任务生命周期划分（`scheduler`、`runner`、`procfs`、`snapshot`、`store`）。Linux 专用代码放在 `*_linux.go`，配 `*_other.go` 存根，在 Windows 上也能编译和跑单测。
- 任务和 danlu 进程解耦：`setsid` 启动、输出直接写文件、用 pid 加进程启动时间识别进程、`KillMode=process`，所以 danlu 重启不影响训练。用 `DANLU_JOB_ID` 环境变量找 torchrun 的 worker，比按进程组找可靠。
- 用户输入只通过环境变量传给包装脚本，脚本里不拼接任何用户输入。
- 安全分层清楚：安全入口、会话、`Content-Type` 加 `SameSite=Strict` 防 CSRF、设备令牌只存哈希、无效令牌和没过入口一样返回空 404。
- `@danlu/core` 通过注入 transport，让面板和客户端共用接口定义和日志逻辑；客户端的轮询放在 Rust 层，每台服务器只信任它自己的 CA。
- 跨文件的关键约束写在 `AGENTS.md` 里，e2e 覆盖了主要流程。

### 2.2 不建议引入的

保持标准库 `net/http`、SQLite、单个二进制和 TanStack Query 轮询。不要加 ORM、依赖注入框架、Web 框架、前端状态管理库、消息队列或额外的守护进程，也不要换 Postgres 或者上 OpenAPI 工具链。本文所有改动都是在现有结构里补几个接口和几张表，不引入新的技术栈。唯一可能新增的是第 7.6 节里一个可选的、只在开发时用的 TS 类型生成工具。

## 3. 问题总表

规模：S 约 100 行以内，M 约 100–400 行，L 400 行以上，都包括测试。

| # | 问题 | 级别 | 依据 | 方案 | 规模 | 状态 |
|---|---|---|---|---|---|---|
| 1 | 调度锁里做慢操作，外部命令的超时管不住 | P0 | 复现 | 4.1 | M | 完成 |
| 2 | `nvidia-smi` 卡住后数据过期且不报错；查进程失败时卡显得空闲 | P0 | 复现 | 4.2 | M | 完成 |
| 3 | 面板改密码，会把命令行改过的配置写回旧值 | P0 | 复现 | 4.3 | M | 完成 |
| 4 | 同一个任务可能被启动两次；停止服务时不等调度器 | P1 | 代码 | 5.1 | M，和 1 一起改 | 完成 |
| 5 | 迁移不是原子的；不防降级；升级前不备份 | P1 | 代码 | 5.2 | S | 完成 |
| 6 | HTTP 没有空闲和读写超时；证书只在启动时续期 | P1 | 代码 | 5.3 | M | 完成 |
| 7 | 一组小问题 | P2 | 代码 | 6 | S | 待做 |
| 8 | 统一的状态转换和事件表 | 结构 | 设计 | 7.1 | L | 待做：拆进 PHASES.md 的 S1 第 4.0 节和 S2 第 5.5 节 |
| 9 | 执行层接口和调度器单测 | 结构 | 设计 | 7.2 | M | 完成 |
| 10 | 运行时状态和配置文件分开 | 结构 | 设计 | 7.3 | S，随 3 一起做 | 完成 |
| 11 | 快照按内容寻址 | 结构 | 设计 | 7.4 | L | 待做 |
| 12 | 统一的采样器 | 结构 | 设计 | 7.5 | S，随 2 一起做 | 完成 |
| 13 | API 契约：响应结构体、拆文件、接口测试 | 结构 | 设计 | 7.6 | M | 待做 |
| 14 | CI | 结构 | 设计 | 7.7 | S | 已加入仓库，还没在 GitHub 上跑过 |
| 15 | 前端：共享逻辑收进 core，日志增量渲染 | 结构 | 设计 | 7.8 | M | 待做 |

"依据"一栏：**复现**是用附录 A 的脚本实际跑出来过；**代码**是读代码能确定，但没有专门复现；**设计**不是 bug，是为后面的功能先补上的接口。

## 4. P0：已复现的问题

### 4.1 调度锁里做慢操作，外部命令的超时管不住

**现象**：一个任务勾选了"Bash 环境"，用户的 `.bashrc` 里起了一个后台进程（复现用的是 `sleep 20 &`）。这时再提交另一个任务，请求等了 20.7 秒才返回，而加载 `.bashrc` 的超时是 10 秒。

**原因**：

- 调度循环拿着 `m.mu` 调用 `schedule()`（[scheduler.go:209](../internal/scheduler/scheduler.go#L209)）。`start()` 在锁里依次做快照（最多 10 秒，[scheduler.go:396](../internal/scheduler/scheduler.go#L396)）和启动（[scheduler.go:412](../internal/scheduler/scheduler.go#L412)），启动里还包括加载 `.bashrc`。`Submit`、`Cancel` 和任务收尾都要这把锁，这段时间里全部排队。同一轮要启动几个任务时，等待时间还要乘上任务数。
- 加载 `.bashrc` 用的是 `exec.CommandContext`（[bashenv_linux.go:46](../internal/runner/bashenv_linux.go#L46)），没有设 `WaitDelay`。后台进程继承了输出管道，`cmd.Run()` 要等管道关闭才返回；超时只会杀掉 bash 本身，杀不到后台进程。如果起的是一个常驻进程，调度会一直卡下去：新任务不启动，取消没反应，结束的任务也不收尾。
- 快照里的 `git`、采集 GPU 的 `nvidia-smi`、列 conda 环境的 `conda` 也是同样的写法，只是不太容易遇到。

**临时规避**：修好之前，先确认 `.bashrc` 里没有起后台进程，再勾选"Bash 环境"。

**方案**：

1. **外部命令统一加护栏。** 新增一个很小的包 `internal/execx`，所有外部命令都通过它创建。命令在自己的进程组里运行，超时时杀掉整个进程组；命令退出后如果还有子进程占着管道，最多再等 2 秒就返回。

   ```go
   //go:build linux

   // Command 和 exec.CommandContext 一样，另外加两道护栏：
   // 命令在自己的进程组里运行，ctx 结束时连它起的子进程一起杀掉；
   // 命令退出后如果还有子进程占着输出管道，最多再等 WaitDelay 就返回。
   func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
   	cmd := exec.CommandContext(ctx, name, args...)
   	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
   	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
   	cmd.WaitDelay = 2 * time.Second
   	return cmd
   }

   // KillGroup 结束命令留下的整个进程组，比如 .bashrc 里起的后台进程。
   func KillGroup(cmd *exec.Cmd) {
   	if cmd.Process != nil {
   		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
   	}
   }
   ```

   `execx_other.go` 里只设 `WaitDelay`，`KillGroup` 留空。加载 `.bashrc` 时，`Run()` 返回后调用 `KillGroup`，不留下后台进程。`errors.Is(err, exec.ErrWaitDelay)` 表示命令本身成功了、只是有子进程没关管道，这时照常解析输出。`snapshot.git`、`gpu.SMI.Query` 和 `api.listCondaEnvs` 都换成 `execx.Command`。

   **实现说明**：最终用的是 `Setsid` 而不是 `Setpgid`。在终端里手动运行 `danlu serve` 时（`dev-wsl.sh`、e2e），一个处在后台进程组、又能打开 `/dev/tty` 的 `bash -i` 会给自己发 SIGTTIN 停住，加载 `.bashrc` 必然超时；新会话没有控制终端，就不会这样。会话首进程的 pid 同时也是进程组号，`KillGroup` 照样有效。另外加了 `execx.Output`：执行命令、把 `ErrWaitDelay` 当作成功、返回前结束整个进程组。

2. **启动拆成三步，慢的事放到锁外做。**

   | 步骤 | 在哪里 | 做什么 |
   |---|---|---|
   | 占位 | 锁内，`schedule()` | 选好卡和端口，把任务写成 `running`，`pid = 0`，同时写入 `started_at`、`gpus_assigned`、`master_port`。写库失败就不启动，这一轮停下，下一轮再试（这一步同时解决第 5.1 节的问题）。写成功后记进内存里的 `m.starting`，起一个 goroutine 做第二步 |
   | 准备 | 锁外，`launch()` | 做快照、加载 `.bashrc`。两者都有超时，而且超时真的能生效 |
   | 启动 | 锁内，`launch()` | 先看这期间有没有被取消、danlu 是不是正在停止；都没有就 fork/exec（毫秒级），记下 pid 和启动时间，落库 |

   配套的改动：

   - `checkRunning` 跳过 `m.starting` 里的任务，否则 `pid = 0` 会被当成进程已经不在了。
   - `Cancel` 遇到正在启动的任务时，只在 `m.starting` 里做标记，并把 `cancel_requested` 写进库。第三步看到标记就直接记为已取消，不再启动。
   - `runner.Start` 拆成 `LoadEnv`（加载 `.bashrc`）和 `Start(spec, env)` 两步，第二步只剩写脚本和 fork/exec。
   - 占位以后任务在库里就是 `running`，所以卡的占用、端口去重、`/api/gpus` 里显示的占用者都不用改。界面上，任务在快照的那几秒里会显示"运行中"、还没有输出，这可以接受。
   - 接口不变，`packages/core`、面板和客户端都不用改。

**测试**：

- 单测（依赖第 7.2 节的执行层接口）：假的 `LoadEnv` 阻塞住时，`Submit` 和 `Cancel` 立即返回；正在启动时取消，任务记为已取消，而且没有启动进程。
- e2e：把附录 A 的场景 A 加进 `scripts/e2e.sh`：`.bashrc` 里起后台进程，另一个提交在 1 秒内返回，后台进程被清理掉。

**改动范围**：新增 `internal/execx`；`internal/runner`、`internal/scheduler`，以及 `internal/snapshot`、`internal/gpu`、`internal/api` 里调用外部命令的地方。

### 4.2 `nvidia-smi` 卡住后数据过期且不报错；查进程失败时卡显得空闲

**现象**：用一个会卡住的假 `nvidia-smi` 复现：正常采集两轮以后卡住。15 秒后 `/api/gpus` 返回的还是旧数据，`error` 为空；这时提交的 1 卡任务照样被分到了 GPU 0。

**原因**：

- 采集循环卡在 `refresh` 里（[gpu.go:221](../internal/gpu/gpu.go#L221)），`Latest()` 一直返回最后一次成功的结果，不带错误；调度器拿数据时也忽略了时间戳（[scheduler.go:306](../internal/scheduler/scheduler.go#L306)）。驱动出问题时，`nvidia-smi` 可能卡在不可中断的状态（D 状态），发什么信号都杀不掉。所以光靠超时不够，读数据的时候必须判断数据过没过期。
- 查 GPU 上的进程失败时，代码只返回卡，不带进程，也不报错（[gpu.go:73](../internal/gpu/gpu.go#L73)）。这样一来，被外部进程占着的卡在调度器看来是空的，违反了"有外部进程的卡不分配"这条规则。两次查询共用一个 10 秒超时，第一次慢一点，第二次就可能超时；在没开 persistence mode 的机器上，`nvidia-smi` 调一次就可能要几秒。

**临时规避**：真机验证时留意首页"GPU 数据 N 秒没有更新"的提示。出现这个提示时，调度器还在按旧数据分卡。

**方案**：

1. 采集改用第 7.5 节的通用采样器：超过 15 秒没有成功采集，`Latest()` 就返回错误"GPU 数据已经 N 秒没有更新（nvidia-smi 可能卡住了）"。采集循环是同步的，卡住时不会再叠加新的 `nvidia-smi` 进程。

   **实现说明**：过期时间定为 20 秒，按"最近一次采集完成"计时，不管成功失败。两次查询各 8 秒超时，一轮最长十几秒，15 秒会把慢但能用的 `nvidia-smi` 误报成卡住；按"最近一次成功"计时的话，持续"查不到进程"会在 20 秒后被改报成"数据过期"，原因就看不出来了。能被信号杀掉的卡住，8 秒超时后就报"nvidia-smi 执行失败"；杀不掉的（D 状态）靠过期判断。
2. 查进程失败时，照常返回卡的数据，同时带上错误 `ErrProcsUnknown`（"查不到 GPU 上的进程，没法判断哪些卡被外部占用"）。
3. 两次查询各用各的超时（各 8 秒），都通过 `execx` 执行。
4. 调度器不用改：`Latest()` 返回错误时，它本来就不分配 GPU（`gpuKnown`），不用卡的任务照常启动。面板也不用改：[GpuGrid.tsx:40](../web/src/components/GpuGrid.tsx#L40) 已经会显示"GPU 信息读取失败，暂停分配新任务：…"。客户端的 `fleet.rs` 读的也是同一个 `error` 字段，会照常发"nvidia-smi 报错"的通知。

**测试**：

- 单测：假的数据源一直阻塞，超过过期时间后 `Latest()` 返回错误；数据过期或进程未知时，GPU 任务保持排队，不用卡的任务照常启动。
- e2e：把附录 A 的场景 B 加进去，断言 `/api/gpus` 带 `error`，1 卡任务保持排队。

**改动范围**：`internal/gpu`，新增 `internal/sample`（第 7.5 节）。

### 4.3 面板改密码，会把命令行改过的配置写回旧值

**现象**：先执行 `danlu port 30001`（提示"重启服务后生效"），还没重启就在面板上改了密码，`config.toml` 里的端口又变回了旧值 17692。`danlu entrance --reset` 和 `danlu cert --regen --public-ip` 改的值也会这样被覆盖，而且没有任何提示。最坏的情况是换 IP：`cert --regen --public-ip` 已经生成了只认新 IP 的 CA，配置却被改回旧 IP，下次重启时 `EnsureServer` 发现 CA 不允许给旧 IP 签证书，服务直接起不来，报错里还提示去执行 `cert --regen`。

**原因**：面板改密码时，把服务启动时读进内存的整份配置写回文件（[api.go:665](../internal/api/api.go#L665)）。配置文件同时被服务进程和命令行写，谁后写谁赢。

**临时规避**：用命令行改过配置后，先重启服务，再在面板上改密码。

**方案**：密码哈希不再放在配置文件里，改存数据库。这样服务进程就再也不写 `config.toml` 了。

1. 追加一次迁移，新建 `settings` 表（第 7.3 节）。
2. `danlu serve` 启动时，如果库里还没有密码哈希，就从 `config.toml` 的 `auth.password_hash` 导入一次，兼容现有的安装。两边都没有时拒绝启动，提示先运行 `danlu passwd`。
3. `Auth.CheckPassword` 每次从库里读哈希。只有登录和改密码时用到，读一行的开销可以忽略。读失败就拒绝登录。
4. 面板改密码、`danlu passwd`、`danlu init` 都只写库。顺带的好处是 `danlu passwd` 立即生效，不用再重启服务。`danlu passwd` 同时删掉配置文件里的旧哈希，免得同时存在两份。`Validate()` 不再要求配置里有密码哈希。
5. 其他改配置的命令（`port`、`entrance`、`cert`）照旧要重启才生效，但改的值不会再被覆盖。

如果想先止血：`changePassword` 改成先从磁盘重新读一遍配置，只改密码哈希再写回，5 行左右就够。但 TOTP（定版线第 15 项）、队列暂停（第 11 项）也需要放运行时状态的地方，所以建议直接做 `settings` 表。

**测试**：

- 接口测试：通过接口改密码后，`config.toml` 的内容一个字节都不变。
- e2e：把附录 A 的场景 C 加进去；再加一条：服务运行时执行 `danlu passwd --password-stdin`，不重启也能用新密码登录。

**改动范围**：`internal/store`（迁移、读写 settings）、`internal/auth`、`internal/api`、`internal/config`、`cmd/danlu`（`init`、`passwd`、`serve`）。

## 5. P1：从代码里确认的风险

### 5.1 同一个任务可能被启动两次；停止服务时不等调度器

**原因**：

- `start()` 先启动进程，再把状态写进库（[scheduler.go:412](../internal/scheduler/scheduler.go#L412)–[421](../internal/scheduler/scheduler.go#L421)）。写库失败只记一条日志，任务在库里还是排队状态，下一轮（最多 2 秒后）会再启动一次：同一个任务 id、同一个日志文件、同一个输出目录，两份训练互相覆盖。磁盘快满时就可能写库失败，这正是验收场景 5 的内容。
- 收到 SIGTERM 后，`ListenAndServe` 立即返回，`cmdServe` 跟着返回，程序直接退出（[main.go:295](../cmd/danlu/main.go#L295)–[321](../cmd/danlu/main.go#L321)）。所以 `Shutdown` 的 5 秒宽限实际上不起作用：正在处理的请求会被截断；调度循环可能正好停在"进程已经起来、还没落库"的那一刻，重启后这个任务又会被启动一次。`defer st.Close()` 也可能在调度循环还在用数据库时执行。升级（`replace`）和 `danlu restart` 都会走这条路。
- 启动时间为 0 时，`procfs.Alive` 不核对进程身份（[procfs.go:67](../internal/procfs/procfs.go#L67)）。进程刚启动就读不到 `/proc/<pid>/stat` 时会出现这种情况，之后就失去了对 pid 复用的防护。

**方案**：

1. **先落库再启动**：就是第 4.1 节的"占位"那一步。
2. **包装脚本写 pid 文件**：`run.sh` 第一行写 `echo $$ > "$DANLU_RUN_DIR/pid"`；`prepare()` 启动前删掉旧的 pid 文件，和现在删 `exitcode` 一样。有了它，启动途中 danlu 退出时，也能把进程认回来。
3. **按下表收尾**。`Recover` 在启动时按这张表处理；运行中遇到 `pid = 0`、又不在 `m.starting` 里的任务，也按这张表处理：

   | 库里的记录 | 实际情况 | 处理 |
   |---|---|---|
   | `running`，pid > 0 | 进程在，启动时间一致 | 接管（和现在一样） |
   | `running`，pid > 0 | 进程不在了 | 有 `exitcode` 就按退出码收尾；没有的话，开机标识变了记为"服务器重启，任务中断"，没变记为"进程被外部终止" |
   | `running`，pid = 0 | 有 pid 文件，进程在，而且带着本任务的 `DANLU_JOB_ID` | 认领：记下 pid 和启动时间，继续监控 |
   | `running`，pid = 0 | 有 `exitcode` | 已经跑完了，按退出码收尾 |
   | `running`，pid = 0 | 没有 pid 文件 | 从没启动过：放回队列，清空 `gpus_assigned`、`started_at` 和 `master_port` |

   "开机标识"指 `/proc/sys/kernel/random/boot_id`，在占位时记进 `jobs` 表的新列 `boot_id`。这样验收场景 5 里的"服务器断电重启"能给出明确的原因，不用再从现象去猜。
4. **核对身份**：启动时间为 0 时，`Alive` 改为核对 `/proc/<pid>/environ` 里的 `DANLU_JOB_ID`，不再直接当作是同一个进程。
5. **停止顺序**：

   ```go
   // 请求的 context 都从 base 派生：停止时先取消它，日志 SSE 这类长连接会马上退出
   base, cancelBase := context.WithCancel(context.Background())
   httpSrv.BaseContext = func(net.Listener) context.Context { return base }
   errc := make(chan error, 1)
   go func() { errc <- listen(httpSrv) }()
   select {
   case err := <-errc: // 端口被占用之类的启动错误
   	return err
   case <-ctx.Done():
   }
   cancelBase()
   sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
   defer cancel()
   _ = httpSrv.Shutdown(sctx) // 等正在处理的请求（比如提交任务）完成
   mgr.Wait()                  // 等调度循环和正在启动的任务都落库，最后才关数据库
   ```

   `launch()` 里快照用的 context 从调度器的 context 派生。停止时快照立即放弃，任务放回队列，不会只启动一半。

**实现说明**：

- **启动后写库失败**不靠 pid 文件补救，而是记进内存里的 `pending`：下一轮 `checkRunning` 用内存里的 pid 继续监控，同时重试写库；`Cancel` 也以内存里的为准。收尾、认领时写库失败也走同一条路。只有 danlu 在写进库之前就退出了，重启后才按上表从 pid 文件认领。
- **认领**先读 pid 文件；没有的话（包装脚本还没来得及写），再按 `DANLU_JOB_ID` 找会话首进程。表里"pid = 0、有 pid 文件但进程已经不在、也没有退出码"的情况按"进程不在了"那一行收尾，不放回队列。
- **停止顺序**：不另加 `mgr.Wait()`，`Manager.Run` 退出前自己等正在启动的任务落库。`cmdServe` 在关数据库之前等 `Run` 返回。

**测试**：单测（第 7.2 节）：占位时写库失败，任务不启动；启动后写库失败，下一轮认领进程，而不是再启动一次；`Recover` 按上表每一行构造输入。

**改动范围**：`internal/scheduler`、`internal/runner`（包装脚本、pid 文件、认领）、`internal/procfs`、`internal/store`（`boot_id` 列）、`cmd/danlu`（停止顺序）。和第 4.1 节是同一组函数，一起改。

### 5.2 迁移不是原子的；不防降级；升级前不备份

**原因**：[store.go:111](../internal/store/store.go#L111) 里，迁移语句和 `PRAGMA user_version` 分两次执行，也没有放在事务里。迁移里有好几条语句时，中途失败会留下一个改了一半的库，而 `user_version` 没变，之后每次启动都在同一个地方报错（比如"table already exists"），服务起不来。库的版本比程序新时（升级后又换回旧程序）也不会拦住。定版线第 2 项要求的"升级前自动备份数据库"也还没有做。

**方案**：

```go
func (s *Store) migrate(backupDir string) error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("数据库是新版本的 danlu 建的（版本 %d，这个程序只认识到 %d），请换回新版本", v, len(migrations))
	}
	if v > 0 && v < len(migrations) {
		// 要升级时才备份：VACUUM INTO 生成一份一致的副本，WAL 里还没合并的内容也在里面
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			return err
		}
		dst := filepath.Join(backupDir, fmt.Sprintf("danlu-v%d-%s.db", v, time.Now().Format("20060102-150405")))
		if _, err := s.db.Exec("VACUUM INTO ?", dst); err != nil {
			return fmt.Errorf("升级前备份数据库失败: %w", err)
		}
	}
	for i := v; i < len(migrations); i++ {
		// 迁移和版本号放在同一个事务里：要么都生效，要么都不生效
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("第 %d 次迁移失败: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
```

备份放在 `~/.danlu/backup/`，只保留最近 5 份。`danlu replace` 替换前把旧程序留一份 `<路径>.prev`。回滚就是换回旧程序，再用升级前的备份替换数据库：先在 README 里写清楚步骤，以后需要时再加一个 `danlu rollback` 命令。

**实现说明**：备份文件名是 `danlu-<时间>-v<旧版本>.db`，时间放在前面，按文件名排序就是按时间排序。`.prev` 优先用硬链接，不用复制。

**测试**：store 单测：迁移中途失败后，`user_version` 和表结构都不变；库的版本比程序新时，`Open` 返回错误；从旧版本升级时生成了备份文件。

**改动范围**：`internal/store`、`internal/config`（备份目录）、`cmd/danlu`（`replace`）。

### 5.3 HTTP 没有空闲和读写超时；证书只在启动时续期

**原因**：

- [main.go:288](../cmd/danlu/main.go#L288) 只设了 `ReadHeaderTimeout`。`IdleTimeout` 和 `ReadTimeout` 都是 0，空闲的长连接永远不会关闭，而面板是直接开在公网上的。
- 服务器证书只在启动时检查（[main.go:306](../cmd/danlu/main.go#L306)），只有剩不到 30 天时才重签。如果上次启动时证书还剩 31 天，之后一个月没重启，证书就会在运行中过期。`cert --regen` 和 `file` 模式下换证书，也都要重启才生效。

**方案**：

1. 超时：

   ```go
   httpSrv := &http.Server{
   	ReadHeaderTimeout: 10 * time.Second,
   	ReadTimeout:       30 * time.Second, // 请求体最多 1MB
   	WriteTimeout:      30 * time.Second, // 日志 SSE 每次写之前续期，下载时清掉，见下文
   	IdleTimeout:       2 * time.Minute,
   	MaxHeaderBytes:    64 << 10,
   }
   ```

   日志 SSE 每次写之前用 `http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))` 续期。这样客户端不读数据时，处理函数最多卡 30 秒，不会一直挂着。下载日志和快照时用 `SetWriteDeadline(time.Time{})` 清掉写超时，手机网络慢也能下完。
2. 证书：`tlsutil` 新增一个证书管理器，把证书放在内存里，通过 `tls.Config.GetCertificate` 在每次握手时给出当前的那张；启动改用 `ListenAndServeTLS("", "")`。管理器每天检查一次：`local-ca` 模式下剩不到 30 天就用本地 CA 重签；证书文件变了就重新加载，所以 `cert --regen` 和 `file` 模式换证书都不用重启。以后做 DuckDNS（ROADMAP 第 7.1 节）时，按 SNI 选证书的逻辑也加在 `GetCertificate` 里。

   **实现说明**：`tlsutil.CertManager` 每分钟比较一次证书和私钥文件的内容，变了就重新加载，所以 `cert --regen` 一分钟内生效；重签每天一次。两个文件对不上（比如正写到一半）时继续用原来的，下一分钟再试。

**测试**：tlsutil 单测：用一张快过期的证书启动管理器，检查以后换成了新证书；证书文件变了以后，`GetCertificate` 返回新证书。

**改动范围**：`cmd/danlu`、`internal/tlsutil`、`internal/api`（SSE 和下载的写超时）。

## 6. P2：小问题

这些都是 S 规模，可以单独改，也可以在相关的改动里顺手改。

1. **登录限速只按 IP 计数，bcrypt 校验没有并发上限**（[auth.go:242](../internal/auth/auth.go#L242)）。拿到安全入口的人可以换着 IP 撞密码；并发的登录请求每个都要算一次 bcrypt（cost 12），会把 CPU 占满。加一个全局的失败计数，所有 IP 加起来每分钟超过一定次数就统一放慢；bcrypt 校验加一个并发上限，用信号量限制在 2 个。
2. **`RecordFailure` 先读后写，不是原子的**（[store.go:538](../internal/store/store.go#L538)），并发的失败会少记。改成一条 UPSERT：`count = count + 1`，用 `RETURNING` 拿到新值，再判断要不要锁定。
3. **下载不支持断点续传。** 日志和快照的下载用的是 `io.Copy`，不带 `Content-Length`，也不支持 Range。改用 `http.ServeContent`，手机网络断了能接着下。
4. **配置文件里写错的键会被静默忽略**（[config.go:140](../internal/config/config.go#L140)）。`toml.DecodeFile` 返回的 `MetaData.Undecoded()` 不为空时，在启动日志里给出警告。
5. **`Validate` 不检查时长和次数**：`kill_grace`、`session_ttl`、`lockout` 要大于 0，`max_failures` 至少为 1。
6. **证书、私钥和 `secret.key` 不是原子写入。** 现在直接 `os.WriteFile`，写到一半断电文件就坏了。统一改成先写临时文件再改名，和 `config.Save` 一样。
7. **systemd unit 里的值没有转义**（[main.go:721](../cmd/danlu/main.go#L721)）。`PATH` 里的 `%` 会被 systemd 当成占位符，路径里有空格会被拆开。`%` 写成 `%%`，`Environment=` 的值加上引号。
8. **重跑表单的"代码已变化"提示白跑了一次 diff。** 它调用的 `snapshot.GitHead`（[snapshot.go:281](../internal/snapshot/snapshot.go#L281)）为了一个 dirty 标志跑了完整的 `git diff HEAD`，结果直接丢掉。只跑 `rev-parse` 和 `status --porcelain` 就够了。
9. **任务列表每次请求都给每个任务读日志尾部**（[api.go:357](../internal/api/api.go#L357)）：最后一行读 8KB，失败任务再读 32KB；客户端每 3 秒拉一次最近 24 小时的已结束任务，最多 100 个。现在的量没问题；任务多了以后，按文件大小和修改时间缓存结果。
10. **`useJobLog` 在 `setRaw` 的更新函数里调用了 `setStart`**（[useJobLog.ts:48](../packages/core/src/useJobLog.ts#L48)）。更新函数里不应该有副作用，StrictMode 下它会执行两次。和第 7.8 节的增量渲染一起改。

## 7. 结构性改进：先补上的接口

### 7.1 统一的状态转换和事件表

**现状**：

- 任务状态在 `finalize`、`failQueued`、`start` 的失败分支和 `Cancel` 里各改各的。
- `FailReason` 里原因和备注混在一起，"已清理 N 个残留进程"连成功的任务上也会出现（[scheduler.go:385](../internal/scheduler/scheduler.go#L385)）。
- 没有地方记录"发生了什么"。想做推送，就只能像客户端的 `fleet.rs` 那样，从轮询到的快照里倒推：失败水位、外部进程出现在你的卡上、队首被挡住（[fleet.rs:317](../desktop/src-tauri/src/fleet.rs#L317)）。客户端第一次连上一台服务器时，水位用的是客户端自己的时钟，拿去和服务器的 `ended_at` 比（[fleet.rs:441](../desktop/src-tauri/src/fleet.rs#L441)），两边时钟不一致时会漏报或多报。
- 如果 ntfy 再用 Go 照着写一遍，就会有两套规则相同的通知逻辑，而且会越改越不一样。

**方案**：

1. `jobs` 表加几列：`fail_kind`（失败类别，枚举）、`notes`（备注，比如清理了几个残留进程，不再塞进 `fail_reason`）、`boot_id`（第 5.1 节）。`fail_reason` 只放给人看的原因。
2. 新建 `events` 表：

   ```sql
   CREATE TABLE events (
   	id INTEGER PRIMARY KEY AUTOINCREMENT,
   	ts INTEGER NOT NULL,
   	kind TEXT NOT NULL,
   	key TEXT NOT NULL DEFAULT '',    -- 告警类事件用它区分是哪件事
   	job_id INTEGER,
   	data TEXT NOT NULL DEFAULT '{}'  -- JSON
   );
   CREATE INDEX events_ts ON events(ts);
   ```

   | kind | 什么时候写 | data 里有什么 |
   |---|---|---|
   | `job_started` | 进程启动成功 | GPU、pid |
   | `job_finished` | 成功、失败、取消 | 状态、`fail_kind`、原因、报错行、退出码、运行时长 |
   | `job_requeued` | 启动途中 danlu 退出，任务放回队列 | 原因 |
   | `queue_paused`、`queue_resumed` | 快速失败暂停（定版线第 11 项）、手动继续 | 原因 |
   | `alert_raised`、`alert_cleared` | 某种情况出现或消失。`key` 区分是哪件事：`gpu_error`、`external:<卡>:<pid>`、`blocked:<任务>`、`disk_low:<挂载点>` | 给人看的说明 |

3. 调度器里所有改状态的地方都改为调用同一个函数 `transition(j, to, reason)`，它在同一个事务里写任务和对应的事件。原来的 `save()` 只留给不改状态的字段，比如 `tracker_url`。
4. 告警类事件只在状态变化的那一刻写：调度循环每 2 秒比较一次当前情况和上一次的，出现时写 `alert_raised`，消失时写 `alert_cleared`。danlu 启动时，从每个 `key` 的最后一条事件恢复出"现在有哪些告警"，所以重启不会重复报。
5. 调度器顺便记下队首在等什么，比如"等 2 张空闲卡（现在空闲 1 张）"、"GPU 3 被外部进程占着（alice，pid 1234）"、"GPU 数据不可用"。它通过任务视图的 `wait_reason` 字段给面板显示；队首被外部进程挡住时，写一条 `alert_raised`。

**谁来用**：

- **ntfy**（定版线第 4 项）：一个 goroutine 从上次读到的位置往后读事件，游标存在 `settings` 里。30 秒内的多个失败合成一条，告警出现时报一次。"同一件事只报一次、一批失败合成一条"都在这里实现。
- **失败归类**（第 3 项）：`finalize` 根据退出码、信号、日志结尾和 `boot_id` 填 `fail_kind`。分类规则集中在 `logs` 包的一个函数里，可以单独测。
- **快速失败暂停**（第 11 项）：根据 `job_finished` 事件判断"启动后很快就失败"，然后写 `queue_paused`。
- **理想线的 `/api/events?after=<id>`**：直接读这张表。客户端恢复开发时，用它代替 `fleet.rs` 自己的推算，水位换成事件 id，就没有时钟不一致的问题了。这一步等客户端恢复以后再做，现在不改客户端。
- 事件按自动清理（第 7 项）的规则保留，比如保留 90 天。

**改动范围**：`internal/store`（迁移、事务写入、读事件）、`internal/scheduler`、`internal/api`（`wait_reason`）。规模 L，可以分两次做：先做 `transition` 和 `job_*` 事件，告警事件和 ntfy 一起做。

### 7.2 执行层接口和调度器单测

**现状**：`Manager` 直接调用 `runner.Terminate`、`runner.KillAll`、`procfs.Alive` 这些包级函数，依赖的也是具体的 `*runner.Runner` 和 `*gpu.Monitor`，所以状态机只能靠 WSL 里的 e2e 来测。[scheduler_test.go](../internal/scheduler/scheduler_test.go) 只测了选卡的 `Allocate`。而第 4、5 节和第 7.1 节要改的正是这个状态机。

**方案**：在 `scheduler` 包里定义调度器需要的两个小接口，生产环境用现有的实现，测试用假的：

```go
// Executor 是调度器和操作系统之间的那一层：启动、查存活、发信号、读退出码。
type Executor interface {
	LoadEnv(inheritBashrc bool) ([]string, error)
	Start(s runner.Spec, env []string) (pid int, start uint64, err error)
	Alive(pid int, start uint64) bool
	Adopt(jobID int64) (pid int, start uint64, ok bool) // 读 pid 文件并核对 DANLU_JOB_ID
	Terminate(jobID int64, pgid int)
	KillAll(jobID int64, pgid int) int
	ExitCode(jobID int64) (int, bool)
}

// GPUSource 就是 GPU 采样器的 Latest，测试里可以直接给出过期或者报错的数据。
type GPUSource interface {
	Latest() ([]gpu.GPU, time.Time, error)
}
```

数据库不用假的：测试里用临时目录下的真实 SQLite，速度很快。要模拟写库失败时，包一层，让指定的某次 `SaveJob` 失败。时间通过一个 `now func() time.Time` 注入，测取消的宽限期时不用真的等。

**要覆盖的情况**：

- 严格先来先跑：队首放不下时，后面的任务不启动；指定卡号的任务等到卡空出来。
- 取消排队中的任务、正在启动的任务、运行中的任务；超过宽限期后强杀。
- 启动失败；占位时写库失败；启动后写库失败（下一轮认领，不会再启动一次）。
- `Recover` 的每一种情况（第 5.1 节的表）。
- GPU 数据过期或进程未知时不分卡，不用卡的任务照常启动。
- 第 7.1 节做完以后：每次状态变化都有对应的事件。

**改动范围**：`internal/scheduler`（接口和测试）、`internal/runner`（实现 `Executor`）。规模 M。建议在改第 4.1 节之前做：先把现有行为测住，再动结构。

**实现说明**：实际的 `Executor` 比上面多几个方法：`LoadEnv` 带 `ctx`（停止服务时立即放弃）；`Alive` 带任务 id（启动时间为 0 时核对 `DANLU_JOB_ID`，第 5.1 节第 4 条）；另有 `Launched`（pid 文件在不在）、`ReadExitCode`、`LogPath`、`BootID`。存储也抽成了小接口 `JobStore`，测试里包一层模拟写库失败。快照通过 `Manager.snap` 注入，测试里不真的打包目录。时间没有注入，宽限期的测试用 1 毫秒。测试在 [manager_test.go](../internal/scheduler/manager_test.go)，在 `-race` 下连跑 10 次稳定。

### 7.3 运行时状态和配置文件分开

**规则**：`config.toml` 只放手写的、静态的、重启才生效的配置，服务进程从不写它；运行中会变的状态放进数据库的 `settings` 表。

```sql
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
```

| key | 用在哪里 |
|---|---|
| `auth.password_hash` | 第 4.3 节 |
| `auth.totp_secret` | 定版线第 15 项 |
| `queue.paused`、`queue.paused_reason` | 定版线第 11 项 |
| `ntfy.cursor` | 定版线第 4 项：推送读到了哪条事件 |

和单个任务相关的状态，比如自动清理要用的"标星"，还是加在 `jobs` 表里。命令行里改配置的命令（`port`、`entrance`、`cert`）照旧要重启；以后需要的话，可以加一个 SIGHUP 重新加载配置。

### 7.4 快照按内容寻址

**现状**：每个任务存一个完整的 tar.gz。读快照里的一个文件，要从头解压到那个文件为止（[snapshot.go:316](../internal/snapshot/snapshot.go#L316)），代码浏览器每点一个文件就解压一遍。扫参数的 10 个任务，就存 10 份一样的代码。定版线第 7 项（自动清理）、第 9 项（按哈希去重存储）和第 10 项（对比和"用当时的代码重跑"）在 tar.gz 上都不好做。

**方案**：

```
~/.danlu/objects/ab/abcdef…    文件内容的 sha256，每个文件单独 gzip 压缩
~/.danlu/snapshots/<id>.json   清单：每个文件的 path、hash、size、mode；符号链接记下目标；git 信息；跳过的文件
~/.danlu/snapshots/<id>.diff   未提交的改动（不变）
```

- **生成**：读文件的同时算哈希。对象已经存在就不再写；不存在就先写临时文件再改名。大小上限、跳过规则和超时都和现在一样。
- **读单个文件**：按哈希直接打开对应的对象。
- **下载**：按清单现场打包成 tar.gz，边打包边返回，接口不变。
- **对比**（第 10 项）：两份清单一比，就知道哪些文件新增、删除、改了；改了的文件再取出两个版本做文本 diff。
- **用当时的代码重跑**（第 10 项）：按清单把文件还原到运行目录。现在符号链接直接跳过，还原出来的目录会缺 `data -> /mnt/data` 这类链接，所以清单里要记下链接的目标。
- **清理**（第 7 项）：先删掉过期任务的清单，再删掉没有任何清单引用的对象。为了不和正在生成的快照冲突，只删一天以前的对象；生成快照时复用了哪个对象，就更新它的修改时间。
- **兼容旧快照**：读的时候两种格式都认，有 `.json` 清单的按新格式读，没有的按旧的 tar.gz 读。不做批量转换，旧快照按清理规则自然过期。
- 第 9 项的 conda 包列表也可以存成同样的对象，相同的环境只存一份。

**改动范围**：`internal/snapshot`、`internal/api`（快照的几个接口，只改内部实现，返回格式不变）。规模 L，在做第 7、9、10 项之前做。

### 7.5 统一的采样器

`gpu.Monitor` 和新加的 `system.Monitor` 基本是同一段代码：定时采集、加锁缓存、返回最新的结果。磁盘余量（定版线第 6 项）会是第三份。新增 `internal/sample`：

```go
// Sampler 定时采集并缓存最新结果。超过 Stale 没有成功采集，Latest 就返回错误，
// 调度器和接口据此不再相信这份数据，而不是一直用旧值。
type Sampler[T any] struct {
	Every, Stale time.Duration
	Query        func(context.Context) (T, error)
	Clone        func(T) T // 返回副本；GPU 列表要连 Procs 一起复制

	mu  sync.RWMutex
	val T
	err error
	ok  time.Time // 最近一次成功的时间
}

// Run 每隔 Every 调用一次 Query。调用是同步的，Query 卡住时不会叠加新的调用。
func (s *Sampler[T]) Run(ctx context.Context) { /* … */ }

func (s *Sampler[T]) Latest() (T, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.val
	if s.Clone != nil {
		v = s.Clone(v)
	}
	if !s.ok.IsZero() && time.Since(s.ok) > s.Stale {
		return v, s.ok, fmt.Errorf("已经 %d 秒没有新数据", int(time.Since(s.ok).Seconds()))
	}
	return v, s.ok, s.err
}
```

GPU、主机资源、磁盘余量都用它。主机资源的 1 分钟历史留在 `system` 包里，在 `Query` 外面包一层维护。

**实现说明**（[sample.go](../internal/sample/sample.go)）：过期按"最近一次采集完成"计时，不按"最近一次成功"，原因见第 4.2 节；第一次采集就卡住时，从 `Run` 开始的时间算起。多了 `Name`、`Hint` 两个字段，用来拼出错误文字。`Query` 出错时也缓存它返回的值，所以查不到进程时，卡的数据照样能显示。`gpu.Monitor` 和 `system.Monitor` 现在都是 `sample.Sampler` 的类型别名。

### 7.6 API 契约

**现状**：大部分响应是 `map[string]any`（[api.go](../internal/api/api.go)），TS 类型手写在 [types.ts](../packages/core/src/types.ts) 里，客户端的 Rust 层用的是 `serde_json::Value`，两边对不上只能在运行时发现。`internal/api` 一个测试也没有，而接口正是 web、desktop 和 `packages/core` 共同依赖的部分。

**方案**：

1. 每个接口定义响应结构体，比如：

   ```go
   type gpusResp struct {
   	GPUs      []gpuView `json:"gpus"`
   	UpdatedAt int64     `json:"updated_at"`
   	Error     string    `json:"error,omitempty"`
   }
   ```

   JSON 的字段名和 `omitempty` 要和现在完全一致。这一步不改接口，不用动 `APIVersion`。
2. `api.go` 按领域拆成几个文件：`server.go`（路由和工具函数）、`jobs.go`、`logs.go`、`snapshot.go`、`settings.go`、`fs.go`、`gpus.go`。网页终端（定版线第 12 项）以后单独放一个文件。
3. 加 `httptest` 接口测试，用临时目录里的真实数据库、假 GPU 和第 7.2 节的假执行层，覆盖：
   - 安全入口、会话和 CSRF 检查的组合；
   - 任务列表的筛选和游标翻页；
   - 日志的 tail 和分段读取，包括 UTF-8 字符边界；
   - SSE 在任务结束后发送 `end`；
   - 快照文件的路径处理；
   - 改密码不动配置文件；
   - 错误响应的格式。

   关键接口的响应各存一份 JSON 快照做比对，字段被意外改掉时测试会失败。
4. 可选：用 `tygo` 从 Go 结构体生成 `packages/core` 的类型，CI 里检查生成的文件是不是最新的。它只在开发时用，不进运行时。不想加的话，有结构体加接口测试也够用。

### 7.7 CI

仓库现在没有 CI。"改 `/api` 时 `web/` 和 `desktop/` 都要能编译"全靠自觉，e2e 也只能在 WSL 里手动跑。

[.github/workflows/ci.yml](../.github/workflows/ci.yml) 已经加进仓库（还没提交）。push 到 main 和所有 PR 时触发：

- **ubuntu**：`go test`（`*_linux_test.go` 也会跑）、`go vet`、三个 TS 包的 `typecheck`、`scripts/build.sh`、`scripts/e2e.sh`。
- **windows**：在 `desktop/src-tauri` 里跑 `cargo fmt --check`、`cargo clippy --all-targets -- -D warnings`、`cargo test`。不用先建 `desktop/dist`：只有 `tauri build` 打开 `custom-protocol` 时，`generate_context!` 才要求 `frontendDist` 存在。

用到的 Action 版本（checkout v7、setup-go v7、setup-node v7、pnpm/action-setup v6、rust-cache v2）核对过都已发布；pnpm 固定为 11.19.0，和本地一致，读写的锁文件格式是 9.0。各步骤在本地都跑过（Go 单测和 vet、Linux 单测在 WSL 里、e2e、构建）。还要做的：推到 GitHub 跑一次，缓存和 runner 环境只能在那里确认。

### 7.8 前端

1. **共享逻辑收进 `@danlu/core`。** web 和 desktop 各写了一份环境变量的解析，而且已经不一致了：web（[NewJob.tsx:15](../web/src/pages/NewJob.tsx#L15)）保留 `=` 后面的空格，并在本地校验变量名；desktop（[SubmitPanel.tsx:22](../desktop/src/components/SubmitPanel.tsx#L22)）会去掉值两边的空格，变量名留给服务器去校验。`KEY= value` 这样的输入，两边提交上去的值不一样。
   - 把 `parseEnvText`、`formatEnvText`、`rerunName` 和"任务转成提交草稿"收进 core，统一成 web 现在的做法。
   - 第 10 项会给提交增加字段，到时只用改一处。
   - 客户端暂停期间，desktop 只换成调用 core 的函数，不改交互。
2. **日志增量渲染。** web 的 [LogViewer.tsx:14](../web/src/components/LogViewer.tsx#L14) 和 desktop 的 [LogPane.tsx:18](../desktop/src/components/LogPane.tsx#L18) 每收到一段 SSE，都把整个缓冲（最多 300 万字符）重新做一遍 `\r` 折叠和 ANSI 转换。在手机上跟着一个跑了几个小时、tqdm 刷得很快的训练看日志，会越来越卡，影响 ROADMAP 第 8 节"手机上打开不觉得慢"的验收。在 core 里加一个增量渲染器：
   - 整个日志用同一个 `AnsiUp` 实例，颜色状态跨行保留；
   - 完整的行只转换一次，追加到列表末尾；
   - 只有最后一行（还没换行，可能被 `\r` 覆盖）每次重新渲染；
   - 按行数而不是字符数限制保留多少内容。

   面板先换上；客户端恢复开发后再换，在那之前 core 保留现有的 `logToHtml`。

## 8. 实施顺序

**阶段 A：真机验证之前。** 放在定版线第 1 项之前，也可以当作第 2 项"异常路径"的第一步。

1. CI（7.7）：先让后面每一步改动都有自动检查。
2. 执行层接口和调度器单测（7.2）：先把现有行为测住。
3. 调度器的启动路径（4.1 和 5.1）：是同一组函数，一起改。
4. GPU 采集（4.2 和 7.5）。
5. 密码存进数据库（4.3 和 7.3）。
6. 迁移和备份（5.2），HTTP 超时和证书（5.3）。

阶段 A 的完成标准：

- 附录 A 的三个场景写进 `scripts/e2e.sh`，并且通过；
- 调度器单测覆盖第 7.2 节列出的情况；
- CI 通过；
- 第 3 节总表里第 1–6 项的状态改成"完成"。

建议在 ROADMAP 定版线第 1 项之前加一行"架构修复：REVIEW.md 阶段 A"。

**阶段 A 的结果（2026-11-01）**：

| 完成标准 | 结果 |
|---|---|
| 附录 A 的三个场景进 e2e | 完成：e2e 新增"Bash 环境"、"配置和密码"、"GPU 数据不可信时暂停分卡（假 nvidia-smi）"三节，另加"停止服务"（SIGTERM 正常退出）和 `replace` 留 `.prev`。共 82 项全部通过（原来 62 项） |
| 调度器单测 | 完成：第 7.2 节列的情况都有，事件那一条等第 7.1 节 |
| CI 通过 | 未完成：workflow 在本地逐步跑过，还没推到 GitHub |
| 总表第 1–6 项 | 完成，第 9、10、12 项也一起完成 |

附录 A 的复现脚本对新程序的输出见附录末尾。阶段 A 也覆盖了定版线第 2 项"异常路径"的一部分（服务器重启的原因、`nvidia-smi` 卡住、升级前备份、启动途中被打断），PHASES.md 第 4.1 节已经按实际做法更新。

**阶段 B：做定版线第 3、4、11 项之前**：状态转换和事件表（7.1）。已经并进 PHASES.md 的 S1：`transition` 和 `job_*` 事件在 S1 开头做（第 4.0 节），告警事件和队首等待原因留到 S2 和推送一起做（第 5.5 节）。

**阶段 C：做定版线第 7、9、10 项之前**：快照按内容寻址（7.4）。

**随做随改**：API 契约（7.6）在改 `internal/api` 时顺带做；前端（7.8）在做日志相关的功能或者手机性能验收之前做；第 6 节的小问题在相关改动里顺手改。

## 附录 A：复现脚本

把下面的脚本存成 `repro.sh`，在 WSL 或 Linux 里运行，参数是 Linux 版的 danlu（不需要先构建前端）：

```bash
# Windows 上交叉编译
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/danlu-repro ./cmd/danlu
# WSL 里，在仓库根目录运行
bash repro.sh dist/danlu-repro
```

脚本用 17691、17692 两个端口，`HOME` 和 `DANLU_HOME` 都指向临时目录，不会碰真实的 `~/.bashrc` 和 `~/.danlu`，退出时会清理它起的所有进程。审阅时的输出：

```
== A：任务加载 .bashrc 时，调度锁一直被占着 ==
  提交另一个任务 Y 等了 20.7 秒（加载 .bashrc 的超时是 10 秒）
  任务 X：succeeded
== B：nvidia-smi 卡住后，GPU 数据停在旧值、没有报错，调度照常 ==
  /api/gpus：数据已经 15 秒没更新，error=None
  GPU 数据过期期间提交的 1 卡任务：running GPU [0]
== C：面板改密码，会把命令行刚改过的配置写回旧值 ==
  命令行 danlu port 30001 之后： port = 30001
  面板改密码之后：         port = 17692
```

```bash
#!/usr/bin/env bash
# 复现架构审阅里的几个问题。用法：bash repro.sh <danlu 二进制>
set -u
BIN_SRC=$1
T=$(mktemp -d /tmp/danlu-repro.XXXX)
BIN=$T/danlu
cp "$BIN_SRC" "$BIN" && chmod +x "$BIN"
ORIG_HOME=$HOME ORIG_PATH=$PATH
PIDS=()
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done
  [ -f "$T/sleepers" ] && xargs -r kill 2>/dev/null <"$T/sleepers"
}
trap cleanup EXIT

json() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
api() { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
setup() { # 名字 端口
  export DANLU_HOME=$T/$1
  "$BIN" init --yes --tls off --port "$2" --password testpass123 --public-ip 127.0.0.1 >/dev/null 2>&1 || { echo "init 失败"; exit 1; }
  ENTRANCE=$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')
  BASE=http://127.0.0.1:$2
  JAR=$T/$1.jar
}
start() {
  "$BIN" serve >>"$T/$1.log" 2>&1 &
  PIDS+=($!)
  for _ in $(seq 1 40); do curl -s -o /dev/null "$BASE/" && break; sleep 0.25; done
  curl -s -c "$JAR" -o /dev/null "$BASE/$ENTRANCE"
  api -o /dev/null -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login"
}
W=$T/work
mkdir -p "$W"

echo "== A：任务加载 .bashrc 时，调度锁一直被占着 =="
export HOME=$T/homeA DANLU_FAKE_GPUS=2
mkdir -p "$HOME"
# .bashrc 里起一个后台进程，它继承了 danlu 读环境变量用的管道
echo "sleep 20 & echo \$! >> $T/sleepers" >"$HOME/.bashrc"
setup a 17691
start a
X=$(api -X POST "$BASE/api/jobs" -d "{\"name\":\"X\",\"workdir\":\"$W\",\"cmd\":\"sleep 1\",\"gpus\":0,\"inherit_bashrc\":true}" | json "d['id']")
sleep 1
t1=$(date +%s.%N)
api -o /dev/null -X POST "$BASE/api/jobs" -d "{\"name\":\"Y\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}"
t2=$(date +%s.%N)
echo "  提交另一个任务 Y 等了 $(python3 -c "print(round($t2-$t1,1))") 秒（加载 .bashrc 的超时是 10 秒）"
sleep 2
echo "  任务 X：$(api "$BASE/api/jobs/$X" | json "d['status'] + ' ' + d['fail_reason']")"
kill "${PIDS[-1]}"
export HOME=$ORIG_HOME
unset DANLU_FAKE_GPUS

echo "== B：nvidia-smi 卡住后，GPU 数据停在旧值、没有报错，调度照常 =="
FB=$T/fakebin
mkdir -p "$FB"
cat >"$FB/nvidia-smi" <<EOF
#!/bin/bash
# 前两轮采集正常，之后卡住：被杀掉后，子进程还占着输出管道（效果和驱动卡死时一样，都等不到返回）
n=\$(cat $T/nvsmi.n 2>/dev/null || echo 0); echo \$((n+1)) >$T/nvsmi.n
if [ "\$n" -ge 4 ]; then sleep 300 & echo \$! >>$T/sleepers; wait; fi
case "\$1" in
  --query-gpu=*) echo "0, GPU-aaaa, Fake A100, 10, 100, 40960, 40, 60.5"; echo "1, GPU-bbbb, Fake A100, 0, 0, 40960, 35, 50.0" ;;
esac
EOF
chmod +x "$FB/nvidia-smi"
export PATH=$FB:$ORIG_PATH
setup b 17692
start b
sleep 16
G=$(api "$BASE/api/gpus")
echo "  /api/gpus：数据已经 $(( $(date +%s) - $(echo "$G" | json "d['updated_at']") )) 秒没更新，error=$(echo "$G" | json "d.get('error')")"
Z=$(api -X POST "$BASE/api/jobs" -d "{\"name\":\"Z\",\"workdir\":\"$W\",\"cmd\":\"sleep 5\",\"gpus\":1}" | json "d['id']")
sleep 2
echo "  GPU 数据过期期间提交的 1 卡任务：$(api "$BASE/api/jobs/$Z" | json "d['status'] + ' GPU ' + str(d['gpus_assigned'])")"

echo "== C：面板改密码，会把命令行刚改过的配置写回旧值 =="
"$BIN" port 30001 >/dev/null
echo "  命令行 danlu port 30001 之后：$(grep -E '^ *port' "$DANLU_HOME/config.toml" | tr -s ' ')"
api -o /dev/null -X POST "$BASE/api/settings/password" -d '{"old":"testpass123","new":"newpass1234"}'
echo "  面板改密码之后：        $(grep -E '^ *port' "$DANLU_HOME/config.toml" | tr -s ' ')"
export PATH=$ORIG_PATH
```

阶段 A 之后，同一个脚本对新程序的输出：

```
== A：任务加载 .bashrc 时，调度锁一直被占着 ==
  提交另一个任务 Y 等了 0.0 秒（加载 .bashrc 的超时是 10 秒）
  任务 X：running
== B：nvidia-smi 卡住后，GPU 数据停在旧值、没有报错，调度照常 ==
  /api/gpus：数据已经 5 秒没更新，error=nvidia-smi 执行失败: signal: killed
  GPU 数据过期期间提交的 1 卡任务：queued GPU []
== C：面板改密码，会把命令行刚改过的配置写回旧值 ==
  命令行 danlu port 30001 之后： port = 30001
  面板改密码之后：         port = 30001
```

场景 A 里 X 还是 running，是因为脚本只等了 2 秒：加载 `.bashrc` 要等后台进程占着的管道，最多 2 秒（`WaitDelay`），之后才启动 `sleep 1`。

---
变更记录

- v1（2026-11-01）：初稿。
- v2（2026-11-01）：阶段 A 做完。更新总表状态，各节补上"实现说明"，第 7.7 节改为已加入仓库，第 8 节记下完成情况，附录 A 补上修复后的输出。
