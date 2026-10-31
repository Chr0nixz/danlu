# 丹炉 Danlu

在一台 GPU 服务器上运行一个 `danlu` 程序，用浏览器（电脑或手机）**提交、排队、查看、取消、重跑**训练任务，并且能查到每次任务实际跑的是哪一版代码。

- 整卡独占、先来先跑；被外部进程占用的卡不会分出去
- 自动激活 conda，自动注入 `CUDA_VISIBLE_DEVICES` 和 torchrun 端口
- danlu 重启时训练不中断；取消时连 torchrun 的 worker 一起结束
- 实时日志（tqdm 进度条原地刷新），自动识别 W&B / SwanLab 链接
- 每次任务启动时保存代码快照（git commit + 未提交的改动 + 文件本身）
- 公网访问：随机端口 + 安全入口 + 密码 + HTTPS（本地 CA，不需要域名）
- 界面电脑和手机都能用：首页一屏看清在跑什么、有没有失败、哪张卡空着；跟随系统深浅色；快捷键 `N` 新建任务、`/` 搜索、`Ctrl+Enter` 提交

管理多台服务器用桌面客户端（`desktop/`，目前只有 Windows 版）。它常驻托盘，把各台的空卡和任务汇总在一个窗口里，在任务失败或服务器异常时发系统通知；升级和服务管理也在客户端里做，通过 SSH 执行。服务器之间互不连接，汇总都在客户端完成。用法见下文"桌面客户端"。

设计文档：
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

> 目前只在 WSL 里用假 GPU 完整测试过（`scripts/e2e.sh`），还没有在真实的 GPU 服务器上跑过。

### 服务器要求

| | |
|---|---|
| 系统 | Linux x86_64 或 arm64，带 systemd |
| 必需 | NVIDIA 驱动（`nvidia-smi` 能在 PATH 里找到）、`/bin/bash` |
| 可选 | `git`：用来记录 commit 和未提交的改动，没有的话只保存文件快照<br>conda：用来自动激活环境 |
| 网络 | 防火墙 / 云安全组放行一个 TCP 端口（`init` 时默认随机选一个） |

### 1. 构建

需要 Go 1.26+、Node 和 pnpm。

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

`init` 会询问端口、公网 IP、用户名、密码和 conda 目录，然后在 `~/.danlu/` 里生成配置文件、本地 CA 和服务器证书。如果密码留空，会随机生成一个，**只显示这一次**。在脚本里使用时加 `--yes`，用 `--port`、`--public-ip`、`--user`、`--password`、`--conda` 指定各项的值。

`install-service` 会做以下几件事：

- 写入 `~/.config/systemd/user/danlu.service`。其中 `ExecStart` 是二进制的绝对路径，`PATH` 是你执行这条命令时 shell 里的 PATH。所以请在能找到 `nvidia-smi` 和 conda 的那个 shell 里执行；以后 PATH 有变化，重新执行一次即可。
- 设置 `KillMode=process`：重启或停止 danlu 时，不会连带杀掉正在跑的训练进程。
- 执行 `systemctl --user enable --now danlu`：立即启动服务，并且开机自启。
- 执行 `loginctl enable-linger`：退出 SSH 后服务继续运行。如果这一步因为权限失败，请手动执行 `sudo loginctl enable-linger $USER`。

### 4. 打开面板

`init` 最后会打印访问地址，形如 `https://203.0.113.10:28457/k3x9p2qa7m4z`；以后可以用 `danlu info` 重新查看。请把**完整地址**存成书签：不经过安全入口访问时，所有页面都返回 404。

第一次打开时，浏览器会提示证书不受信任，点"继续"即可。想去掉这个警告，可以登录后在「设置」页下载 CA 证书，在手机和电脑上各安装一次。这个 CA 只能给这台服务器的 IP 签发证书。

HTTPS 有三种模式，在 `~/.danlu/config.toml` 的 `[tls] mode` 里设置：

- `local-ca`：默认，使用上面说的本地 CA；
- `file`：使用自己的证书，比如有了域名以后，同时要填写 `cert_file` 和 `key_file`；
- `off`：只用于本地测试，密码会以明文传输。

### 升级

```bash
scp dist/danlu-linux-amd64 你的服务器:~/bin/danlu.new
ssh 你的服务器 'chmod +x ~/bin/danlu.new && ~/bin/danlu.new replace ~/bin/danlu'
```

`replace` 让新程序把自己改名成 `~/bin/danlu`，然后重启服务，效果和 `mv` 之后执行 `systemctl --user restart danlu` 一样。不能直接覆盖 `~/bin/danlu`：程序正在运行时，覆盖会报 `Text file busy`，所以要先传成一个新文件名。

重启时训练任务不会中断，新进程启动后会重新接管它们；配置和数据都不受影响。桌面客户端「服务器」页的升级走的也是这条命令。

### 卸载

```bash
systemctl --user disable --now danlu
rm ~/.config/systemd/user/danlu.service ~/bin/danlu
rm -rf ~/.danlu      # 会删除所有任务记录、日志和代码快照，按需执行
```

### 常用命令

| 命令 | 作用 |
|---|---|
| `danlu info` | 显示访问地址、CA 指纹；加 `--json` 输出给桌面客户端读取 |
| `danlu token create --name 电脑名` | 给桌面客户端领一个设备令牌（客户端添加服务器时会自动执行） |
| `danlu token list` / `danlu token revoke <id>` | 查看、吊销设备令牌，吊销立即生效；面板「设置」页也能吊销 |
| `danlu passwd` | 修改密码（会注销面板上所有已登录的设备，桌面客户端的令牌不受影响） |
| `danlu entrance --reset` | 换一个新的安全入口 |
| `danlu port 30001` | 修改端口 |
| `danlu cert --regen` | 公网 IP 变化后重新生成证书（需要重装 CA） |
| `danlu restart` | 修改配置后重启服务（不影响正在跑的训练），等同于 `systemctl --user restart danlu` |
| `danlu logs -n 200` | 查看服务日志的最后若干行 |
| `danlu.new replace ~/bin/danlu` | 升级：用新程序原子替换旧程序并重启服务（桌面客户端升级时用的就是它） |
| `journalctl --user -u danlu -f` | 查看服务日志 |

数据都在 `~/.danlu/`，可以用环境变量 `DANLU_HOME` 改到别处：

- `config.toml`：配置文件；
- `secret.key`：会话签名用的密钥；
- `danlu.db`：任务记录，SQLite 格式；
- `logs/`：每个任务的日志；
- `snapshots/`：代码快照；
- `run/`：每个任务的包装脚本和退出码；
- `tls/`：本地 CA 和服务器证书。

## 本地开发

```bash
pnpm install                              # 在仓库根目录安装前端依赖（pnpm workspace）
cd web && pnpm build                      # 前端构建到 internal/webui/dist，嵌入二进制
go test ./cmd/... ./internal/...          # 单元测试（Windows 上也能跑）
```

在 WSL 或 Linux 里用假 GPU 测试：

```bash
bash scripts/e2e.sh      # 端到端测试：入口/登录/调度/取消/重启接管/日志/快照/限速/设备令牌/游标翻页/replace 升级
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
