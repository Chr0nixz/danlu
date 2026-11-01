# S0 真机验收清单

逐条走完 [DEMO.md](../../docs/DEMO.md) 第 0 节的 7 条完成标准，再加上只有真机才能验证的几项（[PHASES.md](../../docs/PHASES.md) 第 3 节）。每一条写了怎么做、应该看到什么、要贴回来什么。结果不对的，先别修，把现象和要贴的东西记下来就行。

## 准备

1. **构建**：在开发机上运行 `.\scripts\build.ps1`（ARM 服务器加 `-Arch arm64`）。
2. **部署**：按 [README](../../README.md#部署) 的第 2、3 步上传、`danlu init`、`danlu install-service`。在能找到 `nvidia-smi` 和 conda 的 shell 里执行 `install-service`。
3. **上传工具包**：`scp -r scripts/s0 服务器:~/s0`。
4. **准备 Python 环境**：`jobs/` 里的脚本需要 `torch` 和 `tqdm`，用一个已有的 conda 环境就行，下文记作 `<env>`。
5. **先采集一次**：`bash ~/s0/collect.sh`，把生成的 `s0-report-<主机名>.tar.gz` 贴回来。后面有几条要求做完以后再采集一次。

提交任务时，工作目录都填 `~/s0/jobs`，conda 环境填 `<env>`。

## A. DEMO.md 第 0 节

| # | 怎么做 | 应该看到 | 贴回来 |
|---|---|---|---|
| 1a | 电脑浏览器打开 `danlu init` 打印的完整地址，登录 | 能登录；首页有 GPU 和主机资源（CPU、内存的曲线在走） | 首页截图 |
| 1b | 安卓手机上打开同一个地址，登录 | 同上，页面没有横向滚动 | 截图 |
| 1c | 去掉安全入口直接访问 `https://IP:端口/`、`/login`、`/api/me` | 都是空白的 404 | `curl -k -i https://IP:端口/api/me` 的输出 |
| 1d | 在一个新的无痕窗口进入口后，连续输错 5 次密码 | 第 5 次以后提示锁定，15 分钟内正确密码也登不上 | 提示文字 |
| 2 | 4 卡机器上连续提交 3 个任务，每个 2 卡，命令 `torchrun --nproc_per_node=2 ddp_dummy.py --minutes 5` | 前两个同时在跑，各占 2 张卡；第三个排队；5 分钟后前两个成功，第三个自动开始 | 三个任务都在时的首页截图 |
| 3 | 在 SSH 里手动占一张卡：`CUDA_VISIBLE_DEVICES=3 python -c "import torch; x=torch.ones(1,device='cuda'); input()"`，然后提交一个 4 卡任务和一个 1 卡任务 | GPU 3 显示外部进程（带用户和 pid）；4 卡任务排队、不启动；1 卡任务不会分到 GPU 3 | 首页截图；`collect.sh` 的报告（看 `gpu-procs.txt`） |
| 4 | 提交 `torchrun --nproc_per_node=2 ddp_dummy.py --minutes 20`，等进度条走起来，执行 `danlu restart` | 训练不中断：重启后日志接着往下走，任务还是运行中，pid 不变 | `journalctl --user -u danlu -n 50` 里"接管运行中的任务"那一行；重启前后的任务详情截图 |
| 5 | 提交 4 卡的 `torchrun --nproc_per_node=4 ddp_dummy.py --minutes 20 --mem-gb 4`，跑起来后在面板上取消 | 几秒内状态变成已取消；`nvidia-smi` 里这 4 张卡的进程都没了，显存回到接近 0 | 取消前后各一次 `nvidia-smi`；任务详情里的备注（有没有"已清理 N 个残留进程"） |
| 6 | 打开第 4 条那个任务的详情 | 实时日志里 tqdm 原地刷新、不刷屏；有 W&B 链接（脚本打印的是假链接）；"代码"标签里能看到文件；工作目录是 git 仓库时能看到 commit 和 diff | 日志页和代码页截图 |
| 7 | 在第 6 条的任务上点"重跑"，把 `--lr 3e-4` 改成 `--lr 1e-3`，再提交 | 新任务的命令里是新参数，其他字段和原任务一样 | 新任务详情截图 |

## B. 只有真机才能验证的

| # | 怎么做 | 应该看到 | 贴回来 |
|---|---|---|---|
| B1 | 看 `collect.sh` 报告里的 `smi-query-gpu.txt` 和面板首页 | 每张卡的型号、显存、利用率、温度、功耗和 `nvidia-smi` 一致 | 报告 |
| B2 | 有其他用户或容器在用卡时跑一次 `collect.sh` | `gpu-procs.txt` 里能看到他们的进程；面板上那几张卡标成外部进程 | 报告。没有这种情况就写"没条件" |
| B3 | 第 A5 条取消后，在 SSH 里运行 `bash ~/s0/jobs/kill_worker.sh <id> dry` | 列不出任何进程 | 输出 |
| B4 | 提交 `torchrun --nproc_per_node=2 ddp_dummy.py --minutes 10`，跑起来后 `bash ~/s0/jobs/kill_worker.sh <id>` 杀掉一个 worker | 任务失败；记下面板上显示的原因（预计是"退出码 N"或"进程被外部终止"）；卡上没有残留进程 | 任务详情截图；日志文件 `~/.danlu/logs/<id>.log` |
| B5 | 安卓 Chrome 上从设置页下载 CA 证书并安装，再打开面板 | 不再有证书警告 | 截图；如果装 CA 的步骤卡住了，写下卡在哪一步（S3 要写装 CA 的说明） |
| B6 | （可选）能重启机器的话：跑着一个 `ddp_dummy.py --minutes 60` 时重启 | 开机后不用登录，danlu 就已经在运行（linger）；那个任务显示"服务器重启，任务中断" | 开机后的任务详情截图；`collect.sh` 报告 |

## C. 失败日志样本（给 S1 的失败归类用）

每个任务失败以后，把 `~/.danlu/logs/<id>.log` 和面板上显示的失败原因一起贴回来。日志里有不想外传的路径或数据，替换掉就行，报错的那几行保持原样。

| # | 命令 | 预期 |
|---|---|---|
| C1 | `python fail_cuda_oom.py` | `torch.OutOfMemoryError: CUDA out of memory` |
| C2 | `torchrun --nproc_per_node=2 fail_cuda_oom.py` | 两个 rank 各报一次显存不足 |
| C3 | `python fail_exception.py` | `TypeError`，调用栈有三层 |
| C4 | `torchrun --nproc_per_node=2 fail_nccl.py` | 1–2 分钟后 NCCL watchdog 超时 |
| C5 | 第 B4 条的日志 | `kill -9` 一个 worker 之后 torchrun 的输出 |
| C6 | 平时真实训练里遇到的任何失败 | 越多越好 |

主机内存不足（被 OOM killer 杀掉）这次不造：在共享的服务器上可能误杀别人的进程。S1 会用 cgroup 限额安全地造。

## 最后

再跑一次 `bash ~/s0/collect.sh`，把报告、截图和日志一起贴回来。服务器的 GPU 型号、驱动版本和发行版会写进 DEMO.md 的状态行。
