# 丹炉 Danlu

在一台 GPU 服务器上运行一个 `danlu` 程序，用浏览器（电脑或手机）**提交、排队、查看、取消、重跑**训练任务，并且能查到每次任务实际跑的是哪一版代码。

- 整卡独占、先来先跑；被外部进程占用的卡不会分出去
- 自动激活 conda，自动注入 `CUDA_VISIBLE_DEVICES` 和 torchrun 端口
- danlu 重启时训练不中断；取消时连 torchrun 的 worker 一起结束
- 实时日志（tqdm 进度条原地刷新），自动识别 W&B / SwanLab 链接
- 每次任务启动时保存代码快照（git commit + 未提交的改动 + 文件本身）
- 从 checkpoint 路径找到任务，对比两次运行，也可用旧快照重跑
- 记录运行环境，并为每次任务生成独立输出目录和 `danlu.json` 清单
- 公网访问：随机端口 + 安全入口 + 密码 + HTTPS（本地 CA，不需要域名）
- 界面电脑和手机都能用：首页一屏看清在跑什么、有没有失败、哪张卡空着；跟随系统深浅色；快捷键 `N` 新建任务、`/` 搜索、`Ctrl+Enter` 提交

管理多台服务器用桌面客户端（`desktop/`，目前只有 Windows 版）。它常驻托盘，把各台的空卡和任务汇总在一个窗口里，在任务失败或服务器异常时发系统通知；升级和服务管理也在客户端里做，通过 SSH 执行。服务器之间互不连接，汇总都在客户端完成。用法见下文"桌面客户端"。

设计文档：
- [docs/ROADMAP.md](docs/ROADMAP.md)：当前的开发规划（先做后端和面板，客户端暂停新功能）；
- [docs/PHASES.md](docs/PHASES.md)：把规划拆成 S0–S5 的阶段方案；
- [docs/DEMO.md](docs/DEMO.md)：当前 demo 的范围和接口；
- [docs/DESIGN.md](docs/DESIGN.md)：长期规划，部分已被客户端方案取代；
- [PRODUCT.md](PRODUCT.md)：面板的产品定位；
- [web/DESIGN.md](web/DESIGN.md)：面板的视觉规范；
- [docs/CLIENT.md](docs/CLIENT.md)：桌面客户端方案和里程碑；
- [desktop/PRODUCT.md](desktop/PRODUCT.md)：客户端的产品定位；
- [desktop/DESIGN.md](desktop/DESIGN.md)：客户端的视觉规范。

## 部署

丹炉是**一个静态链接的 Linux 可执行文件**，前端已经打包在里面。服务器上不需要 Docker、Node、Python，也不需要 root。部署分四步：

1. 在开发机上交叉编译；
2. 用 `scp` 把文件传到服务器；
3. 运行 `danlu init` 生成配置；
4. 运行 `danlu install-service` 注册成 systemd **用户服务**。

> 截至 2026-11-02，用户已在真实环境日常使用且暂未反馈异常；完整真机验收记录仍在随使用补齐。自动化端到端测试在 WSL 假 GPU 环境运行（`scripts/e2e.sh`）。

### 服务器要求

| | |
|---|---|
| 系统 | Linux x86_64 或 arm64，带 systemd |
| 必需 | NVIDIA 驱动（`nvidia-smi` 能在 PATH 里找到）、`/bin/bash` |
| 可选 | `git`：用来记录 commit 和未提交的改动，没有的话只保存文件快照<br>conda：用来自动激活环境 |
| 网络 | 防火墙 / 云安全组放行一个 TCP 端口（`init` 时默认随机选一个） |

### 1. 构建

需要 Go 1.26+、Node.js 22.22+ 和 pnpm 12.16.1（版本固定在根目录 `package.json` 的 `packageManager`）。

```powershell
.\scripts\build.ps1                # Windows，生成 dist\danlu-linux-amd64
.\scripts\build.ps1 -Arch arm64    # ARM 服务器
```

```bash
bash scripts/build.sh              # Linux / macOS / WSL；加参数 arm64 构建 ARM 版本
```

### 2. 上传

```bash
ssh 你的服务器 'mkdir -p ~/bin'
scp dist/danlu-linux-amd64 你的服务器:~/bin/danlu
ssh 你的服务器 'chmod +x ~/bin/danlu'      # 从 Windows 传过去的文件没有执行权限
```

### 3. 初始化并安装服务（在服务器上执行）

```bash
~/bin/danlu init               # 交互式，每一项都有默认值，直接回车即可
~/bin/danlu install-service
```

`init` 会询问端口、公网 IP、用户名、密码和 conda 目录，然后在 `~/.danlu/` 里生成配置文件、本地 CA 和服务器证书。如果密码留空，会随机生成一个，**只显示这一次**。完成后会打印访问二维码，手机可直接扫码打开安全入口；不需要二维码时加 `--no-qr`。在脚本里使用时加 `--yes`，用 `--port`、`--public-ip`、`--user`、`--password`、`--conda` 指定各项的值。

`install-service` 会做以下几件事：

- 写入 `~/.config/systemd/user/danlu.service`。其中 `ExecStart` 是二进制的绝对路径，`PATH` 是你执行这条命令时 shell 里的 PATH。所以请在能找到 `nvidia-smi` 和 conda 的那个 shell 里执行；以后 PATH 有变化，重新执行一次即可。
- 自动创建 `~/.local/bin/danlu` 软链接，指向当前安装的程序，并配置 Bash / Zsh 的 PATH。新终端可以直接运行 `danlu info`、`danlu logs`；当前终端执行一次 `export PATH="$HOME/.local/bin:$PATH"` 即可。已有的其他同名程序不会被覆盖。
- 设置 `KillMode=process`：重启或停止 danlu 时，不会连带杀掉正在跑的训练进程。
- 执行 `systemctl --user enable --now danlu`：立即启动服务，并且开机自启。
- 执行 `loginctl enable-linger`：退出 SSH 后服务继续运行。如果这一步因为权限失败，请手动执行 `sudo loginctl enable-linger $USER`。

### 4. 安装 CA，再打开面板

`init` 最后会打印访问地址和二维码，形如 `https://203.0.113.10:28457/k3x9p2qa7m4z`；以后可以用 `danlu info` 重新查看，`danlu info --no-qr` 只显示文字信息。请把**完整地址**存成书签：不经过安全入口访问时，所有页面都返回 404。

面板的证书由 `init` 生成的本地 CA 签发，这个 CA 只能给这台服务器的 IP 签发证书。**先在每台设备上装好 CA，再打开面板登录。** 不要跳过浏览器的证书警告去登录：能拦截连接的人可以冒充面板，拿走你输入的密码，连「设置」页里下载的 CA 也能换成他自己的。

1. 在自己的电脑上通过 SSH 取回 CA。SSH 已经核实过服务器的身份，经它拿到的证书可以直接信任：

   ```bash
   ssh 你的服务器 danlu ca > danlu-ca.crt
   ```

   Windows PowerShell 5 的 `>` 会把文件转成 UTF-16，请改用 `scp 你的服务器:.danlu/tls/ca.crt danlu-ca.crt`（数据目录不是 `~/.danlu` 时换成对应路径）。命令会在终端里显示 CA 的 SHA-256 指纹，`danlu info` 里也有。

2. 安装到受信任的根证书：
   - Windows：双击 `danlu-ca.crt`，选择"当前用户"，放入"受信任的根证书颁发机构"；
   - macOS：用"钥匙串访问"导入到"登录"钥匙串，把信任设为"始终信任"；
   - 手机：把电脑上的这个文件用数据线、AirDrop 或聊天软件的文件传输发到手机上，按「设置 → 安装 CA 证书」里对应平台的步骤安装（步骤在电脑上的面板里就能看到）。不要在手机上直接打开面板下载。

3. 重新打开浏览器，访问书签里的地址或扫描二维码。浏览器不再提示证书警告，就可以登录了；**仍然提示证书不受信任时不要继续**，先检查 CA 是否装好，再排查网络。

装好后可以在「设置」页核对：页面上显示的 CA 指纹应该和 `danlu info` 一致。重新生成 CA（`danlu cert --regen`）后，各设备要删掉旧 CA，按上面的步骤重新安装。

HTTPS 有三种模式，在 `~/.danlu/config.toml` 的 `[tls] mode` 里设置：

- `local-ca`：默认，使用上面说的本地 CA；
- `file`：使用自己的证书，比如有了域名以后，同时要填写 `cert_file` 和 `key_file`；
- `off`：只用于本地测试，密码会以明文传输。

### 升级

在开发机上构建并上传，文件名用 `danlu.new`：

```bash
scp dist/danlu-linux-amd64 你的服务器:~/danlu.new
```

然后在服务器上进入上传目录，执行：

```bash
chmod +x danlu.new   # 上传后没有执行权限时需要
./danlu.new update
```

`update` 从正在运行的 danlu 用户服务中自动找到原程序，用新程序原子替换它并重启服务，不用填写原安装路径，也不依赖 PATH 或当前终端的 `DANLU_HOME`。升级还会自动配置 `danlu` 命令的 PATH，配置和数据目录沿用服务原来的设置。

服务没有运行、需要手动指定原程序位置时，使用兼容保留的 `replace`：

```bash
./danlu.new replace ~/bin/danlu
```

`replace` 让新程序把自己改名成 `~/bin/danlu`，然后重启服务，效果和 `mv` 之后执行 `systemctl --user restart danlu` 一样。不能直接覆盖 `~/bin/danlu`：程序正在运行时，覆盖会报 `Text file busy`，所以要先传成一个新文件名。

重启时训练任务不会中断，新进程启动后会重新接管它们；配置和数据都不受影响。桌面客户端「服务器」页的升级仍使用 `replace`。

`v0.2.0` 候选版先升级一台，确认版本、数据库迁移备份和运行任务接管，再升级其余目标机。所有目标机完成 [真机验收清单](scripts/s0/CHECKLIST.md) 后记录两周试用的起点。

升级时留了两份后路：`replace` 把旧程序留成 `~/bin/danlu.prev`；新版本第一次启动、要升级数据库表结构时，先把旧库备份到 `~/.danlu/backup/danlu-<时间>-v<旧版本>.db`，只保留最近 5 份。新版本有问题时这样回滚：

```bash
systemctl --user stop danlu
mv ~/bin/danlu.prev ~/bin/danlu
cp ~/.danlu/backup/danlu-<时间>-v<旧版本>.db ~/.danlu/danlu.db   # 只有这次升级改过表结构、留下了备份时才需要
rm -f ~/.danlu/danlu.db-wal ~/.danlu/danlu.db-shm
systemctl --user start danlu
```

换回备份的数据库，会丢掉升级以后提交的任务记录；正在跑的训练不受影响，旧程序启动后照样接管。该换数据库却没换的话，旧程序发现库是新版本建的，会拒绝启动并提示换回新版本（带这个检查的版本才会，更早的版本不检查）。

### 卸载

```bash
systemctl --user disable --now danlu
rm ~/.config/systemd/user/danlu.service ~/bin/danlu
rm -rf ~/.danlu      # 会删除所有任务记录、日志和代码快照，按需执行
```

### 常用命令

| 命令 | 作用 |
|---|---|
| `danlu info` | 显示访问地址、CA 指纹和二维码；`--no-qr` 关闭二维码，`--json` 输出给桌面客户端读取 |
| `danlu token create --name 电脑名` | 给桌面客户端领一个设备令牌（客户端添加服务器时会自动执行） |
| `danlu token list` / `danlu token revoke <id>` | 查看、吊销设备令牌，吊销立即生效；面板「设置」页也能吊销 |
| `danlu passwd` | 修改密码，立即生效，不用重启（会注销面板上所有已登录的设备，桌面客户端的令牌不受影响） |
| `danlu entrance --reset` | 换一个新的安全入口 |
| `danlu port 30001` | 修改端口 |
| `danlu cert --regen` | 公网 IP 变化后重新生成证书（需要重装 CA） |
| `danlu ca` | 把本地 CA 证书打印到标准输出，首次接入时经 SSH 取回：`ssh 你的服务器 danlu ca > danlu-ca.crt` |
| `danlu restart` | 修改配置后重启服务（不影响正在跑的训练），等同于 `systemctl --user restart danlu` |
| `danlu logs -n 200` | 查看服务日志的最后若干行 |
| `danlu prune --dry-run` | 预览保留策略会清掉哪些任务文件 |
| `danlu prune --older-than 72h` | 只清理结束至少 72 小时的未标星任务文件；加 `--dry-run` 先预览，`--json` 输出清单 |
| `danlu notify setup` | 配置手机推送，打印随机订阅地址和二维码；重复运行保留原订阅，`--new-topic` 轮换 |
| `danlu notify test` | 按当前配置发一条测试推送；设置页也有测试按钮 |
| `danlu notify setup --disable` | 关闭推送，重启服务后生效 |
| `danlu trace /path/to/checkpoint` | 从服务器上的产物路径反查任务，并打印面板链接 |
| `danlu doctor` / `danlu doctor --json` | 检查 GPU、conda、systemd、磁盘、证书、端口和时间同步；有 fail 项时退出码为 1。`--json` 输出供工具读取，跑完即返回 0，结论看每项的 `level` |
| `danlu totp enable` / `danlu totp disable` | 在服务器终端启用或关闭登录两步验证；启用时扫描二维码并确认一次验证码 |
| `./danlu.new update` | 升级：自动定位运行中的旧程序，用当前新程序替换并重启服务 |
| `danlu.new replace ~/bin/danlu` | 升级：用新程序原子替换旧程序并重启服务（桌面客户端升级时用的就是它） |
| `journalctl --user -u danlu -f` | 查看服务日志 |

数据都在 `~/.danlu/`，可以用环境变量 `DANLU_HOME` 改到别处：

- `config.toml`：配置文件，只有命令行会改它，服务自己从不写；改完要重启服务才生效；
- `secret.key`：会话签名用的密钥；
- `danlu.db`：任务记录和登录密码的哈希，SQLite 格式；
- `backup/`：升级前自动备份的数据库；
- `logs/`：每个任务的日志；
- `snapshots/`：代码快照；
- `run/`：每个任务的包装脚本、pid 和退出码；
- `envs/`：按 SHA-256 去重保存的环境包清单；
- `tls/`：本地 CA 和服务器证书。服务器证书快到期时，服务在运行中自己续签；`danlu cert --regen` 换的证书一分钟内生效，改了公网 IP 的话还是要重启，访问地址才会更新。

启用两步验证后，`danlu.db` 还会保存验证器密钥和已使用过的验证码时间窗。密钥只通过服务器本地 CLI 管理，不会从 API 返回。

构建脚本默认从 `git describe --tags --always --dirty` 读取版本：精确 tag 构建显示 tag，其他构建显示 `dev-<描述>`。也可通过 `VERSION` 环境变量指定版本。

最后一个候选版提交同时有候选和正式 tag 时，正式构建显式指定 `VERSION=v0.2.0`。部署后核对 `danlu version` 和 `/api/me`。

任务产物默认写入 `~/danlu-outputs/<项目>/<任务 id>-<任务名>/`。`runtime.output_root` 可在 `config.toml` 中改位置；训练命令可通过 `DANLU_OUTPUT_DIR` 写 checkpoint 等文件。danlu 会维护该目录中的 `danlu.json`，不会清理训练产物。

任务启动后 2 分钟内失败（包括加载环境或启动进程失败），且还有任务排队时，队列会自动暂停。首页和任务页会显示原因，点“继续队列”恢复；已经占位或运行中的任务继续，重启 danlu 后仍保留暂停。`config.toml` 的 `[runtime]` 中可用 `fail_fast = "45s"` 调整阈值，设为 `"0s"` 关闭，修改后执行 `danlu restart` 生效。取消任务不会触发自动暂停。

首页会显示数据目录、输出目录和最近工作目录所在分区的可用空间，同一分区只列一次。`[runtime] disk_warn_gb` 默认 20，低于它时显示告警；`disk_min_gb` 默认 2，数据目录所在分区低于它时暂停启动新任务，恢复到两倍阈值后自动继续。单位是 GiB，设为 0 可分别关闭告警或暂停保护。数据目录的磁盘采集失败或数据过期时也不启动新任务；某个工作目录所在的网络存储卡住时，只把那一项标为采集失败（每个目录最多等 5 秒），不影响数据目录和调度；磁盘恢复只解除磁盘暂停，手动暂停与快速失败仍需人工继续。开启 ntfy 后，余量跌破告警线时会推送一次，恢复后才能再次触发。

自动清理在服务启动时和之后每 6 小时运行：`[retention] keep_days` 默认 90，`max_total_gb` 默认 50，分别按结束时间和丹炉管理文件总量清理，按最旧任务优先。设为 0 关闭对应规则，两项都为 0 时关闭自动清理。任务行或详情页点星标可保留日志和快照，任务页可只看标星。清理保留任务记录、仍被任何任务引用的环境包清单和所有训练产物；只删除未标星的已结束任务的日志、快照与运行目录，并标明已清理。用快照重跑的任务在 `~/.danlu/run/<id>/code/` 里运行，写在相对路径下的文件（比如 `checkpoints/`）也算训练产物：清理时只删恢复出来的代码和指回原工作目录的链接，这些文件留下，也不计入 `max_total_gb`。运行任务和仍在采集环境包的任务不清理。`--older-than` 会替代配置的两条任务清理规则，仅按指定时长筛选。

## 手机推送

在服务器上运行 `danlu notify setup`，用 ntfy App 扫描终端中的二维码订阅，再运行 `danlu restart`。设置页显示是否开启、最近发送时间和最后的错误，可点“发一条测试”确认连通。

默认使用 ntfy.sh，也可以用 `danlu notify setup --server https://ntfy.example.com --topic training` 接入自建服务；需要令牌时加 `--token-stdin` 从标准输入读取。设置保存在 `[notify] ntfy` 和 `token` 中，topic 留空关闭。重复运行 setup 会保留原订阅，`--new-topic` 才会生成新 topic；订阅地址本身相当于密钥，不要公开。带鉴权的订阅还需在 ntfy App 中登录对应服务器。

推送包括任务失败、自动队列暂停、磁盘余量不足、持续 60 秒的 GPU 采集错误及恢复、外部进程占卡、队首被外部进程阻塞和日志静默。提交时可勾选「结束时推送」，排队或运行中也可从详情修改。成功和取消按开关推送，失败照常提醒。30 秒窗口内的结束提醒合并发送，快速失败使用一条带原因的队列暂停通知。

`[notify] queue_drained` 默认开启，队列排空且本批至少有两个结束任务时，发送一条跑完汇总。同批仍在合并窗口内的结束提醒并入汇总。点通知打开原批次的任务列表。设为 `false` 并重启即可关闭汇总。消息包含主机名、任务编号、名称、用时和结构化原因，链接需要手机已访问过安全入口。

`[runtime] silence_alert` 默认 `"30m"`。任务日志持续无新输出时提示，阈值为下限与已观察到的最长输出间隔三倍中的较大值。恢复输出时面板清除提示，手机只在告警出现时收到消息。设为 `"0s"` 并重启即可关闭。

首次开启或更换 topic 不补发历史事件。待发批次和消费游标持久化，正常重启不重复推送。网络发送首次失败后重试三次，仍失败就记录错误并继续；若发送中被强制中断、无法确认是否送达，重启会标明“结果未知”并跳过重发。支持序列更新的 ntfy 客户端可将同一请求的重试显示为一条通知；网络丢失回执时无法保证外部服务只收到一次请求。协议见 [ntfy 官方发布文档](https://docs.ntfy.sh/publish/)。

## 本地开发

```bash
pnpm install                              # 在仓库根目录安装前端依赖（pnpm workspace）
cd web && pnpm build                      # 前端构建到 internal/webui/dist，嵌入二进制
go test ./cmd/... ./internal/...          # 单元测试（Windows 上也能跑）
```

在 WSL 或 Linux 里用假 GPU 测试：

```bash
bash scripts/e2e.sh      # 端到端测试：入口/登录/调度/队列暂停与快速失败保护/Bash 环境/取消/重启接管/日志/快照/设备令牌/游标翻页/改密码/限速/update 与 replace 升级/正常停止/nvidia-smi 出错
bash scripts/dev-wsl.sh  # 启动演示环境（4 张假 GPU，第 3 张模拟被外部占用），打开打印出的地址
```

假 GPU 由环境变量控制：`DANLU_FAKE_GPUS=4` 表示模拟 4 张卡，`DANLU_FAKE_BUSY=3` 表示第 3 号卡被外部进程占用。

### 桌面客户端（`desktop/`）

另外需要 Rust（stable）。Windows 上还需要 WebView2，Windows 11 自带。

```bash
cd desktop && pnpm tauri dev              # 启动客户端（开发模式）
cd desktop/src-tauri && cargo test        # Rust 层的单元测试
```

在客户端的「服务器」页添加服务器，填 `~/.ssh/config` 里的主机名或 `user@host`。添加之前有三个前提：

- 服务器上已经按上面的步骤装好 danlu。客户端目前还不能部署新服务器，这项功能在 C3。
- 能免密登录（密钥或 ssh-agent），因为客户端用的是系统自带的 ssh。
- 第一次连接时已经在终端里确认过主机密钥。

客户端会自动读取地址和 CA 证书，并在服务器上领一个设备令牌，不用输面板密码。数据存放位置：

- 服务器列表：`%APPDATA%\dev.danlu.desktop\servers.json`；
- 偏好、已读的失败和通知进度：同目录下的 `state.json`；
- 设备令牌：Windows 凭据管理器。

客户端常驻托盘：关闭窗口只是缩回托盘，退出在托盘图标的右键菜单里。托盘图标灰色表示空闲，朱砂色表示有任务在跑，带红点表示有没看过的失败或服务器异常；单击图标或按 `Ctrl+Alt+D`（可在设置里改）弹出迷你总览。

主窗口的快捷键：

- `N` 新建任务；
- `C` 复制当前任务；
- `/` 或 `Ctrl+K` 筛选；
- `↑` `↓` `Enter` `Esc` 在任务列表里移动、打开和关闭详情；
- `Ctrl+F` 搜索日志。

在「服务器」页升级时，客户端上传仓库 `dist/` 里构建好的 `danlu-linux-<架构>`，所以要先运行 `.\scripts\build.ps1`（ARM 服务器加 `-Arch arm64`）。安装包目前还不带服务器程序，这要等 C3 的发布流程。

升级时先升一台。客户端确认新版本已经在服务、升级前在跑的任务都已被接管，才会出现"升级其余 N 台"。服务器的接口版本（`api_version`）和客户端不一致时，「服务器」页会提示升级。

## 许可证

本项目采用 GNU GPL-3.0，详见 [LICENSE](LICENSE)。
