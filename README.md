# 丹炉 Danlu

在一台 GPU 服务器上运行一个 `danlu` 程序，用浏览器（电脑或手机）**提交、排队、查看、取消、重跑**训练任务，并且能查到每次任务实际跑的是哪一版代码。

- 整卡独占、先来先跑；被外部进程占用的卡不会分出去
- 自动激活 conda，自动注入 `CUDA_VISIBLE_DEVICES` 和 torchrun 端口
- danlu 重启时训练不中断；取消时连 torchrun 的 worker 一起结束
- 实时日志（tqdm 进度条原地刷新），自动识别 W&B / SwanLab 链接
- 每次任务启动时保存代码快照（git commit + 未提交的改动 + 文件本身）
- 公网访问：随机端口 + 安全入口 + 密码 + HTTPS（本地 CA，不需要域名）
- 界面电脑和手机都能用：首页一屏看清在跑什么、有没有失败、哪张卡空着；跟随系统深浅色；快捷键 `N` 新建任务、`/` 搜索、`Ctrl+Enter` 提交

设计文档：[docs/DEMO.md](docs/DEMO.md)（当前 demo）、[docs/DESIGN.md](docs/DESIGN.md)（长期规划）、[PRODUCT.md](PRODUCT.md)（产品定位）、[DESIGN.md](DESIGN.md)（界面视觉规范）。

## 部署到服务器

```powershell
# 在 Windows 上构建（需要 Go、Node、pnpm），生成 dist/danlu-linux-amd64
.\scripts\build.ps1
scp dist\danlu-linux-amd64 你的服务器:~/bin/danlu
```

```bash
# 在服务器上（不需要 root）
danlu init               # 交互式：端口、公网 IP、用户名、密码、conda 目录，都有默认值
danlu install-service    # 安装为 systemd 用户服务，开机自启、退出 SSH 后继续运行
```

`init` 最后会打印访问地址，形如 `https://203.0.113.10:28457/k3x9p2qa7m4z`。记得在防火墙 / 云安全组放行这个端口，并把**完整地址**存成书签：不经过安全入口时，所有页面都返回 404。

第一次打开时浏览器会提示证书不受信任，点"继续"即可。想去掉警告，可以登录后到「设置」页下载 CA 证书，在手机和电脑上各安装一次。

### 常用命令

| 命令 | 作用 |
|---|---|
| `danlu info` | 显示访问地址、CA 指纹 |
| `danlu passwd` | 修改密码（会注销所有已登录的设备） |
| `danlu entrance --reset` | 换一个新的安全入口 |
| `danlu port 30001` | 修改端口 |
| `danlu cert --regen` | 公网 IP 变化后重新生成证书（需要重装 CA） |
| `systemctl --user restart danlu` | 修改配置后重启（不影响正在跑的训练） |
| `journalctl --user -u danlu -f` | 查看服务日志 |

数据都在 `~/.danlu/`：`config.toml`、`danlu.db`、`logs/`、`snapshots/`、`tls/`。

## 本地开发

```bash
cd web && pnpm install && pnpm build     # 前端构建到 internal/webui/dist，嵌入二进制
go test ./cmd/... ./internal/...          # 单元测试（Windows 上也能跑）
```

在 WSL 或 Linux 里用假 GPU 测试：

```bash
bash scripts/e2e.sh      # 端到端测试：入口/登录/调度/取消/重启接管/日志/快照/限速
bash scripts/dev-wsl.sh  # 启动演示环境（4 张假 GPU，第 3 张模拟被外部占用），打开打印出的地址
```

假 GPU 由环境变量控制：`DANLU_FAKE_GPUS=4` 表示模拟 4 张卡，`DANLU_FAKE_BUSY=3` 表示第 3 号卡被外部进程占用。
