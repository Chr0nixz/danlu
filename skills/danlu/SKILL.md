---
name: danlu
description: 在装了丹炉（danlu）的 GPU 服务器上提交、等待、查看、取消和重跑训练任务，查哪些卡空着、任务为什么排队或失败。用户要在 GPU 服务器上跑训练、评测、扫参数，看任务日志、显存不足或 NCCL 报错，或者提到"丹炉"、"提交任务"、"排队跑"时使用；服务器上有 `danlu` 命令时，即使用户没提丹炉，也要用它跑要 GPU 或要跑很久的命令，而不是 nohup、tmux 或直接 torchrun。Use for submitting GPU training jobs to a danlu server, waiting on them, reading their logs, diagnosing failures (CUDA OOM, NCCL, Python exceptions) and rerunning them. 不用于安装、升级或配置 danlu 本身。
---

# 用丹炉跑 GPU 任务

丹炉是 GPU 服务器上的任务队列和实验记录。经它跑的任务按整卡分配、按队列顺序严格排队，不会抢到别人的卡；每次运行都记下命令、conda 环境、环境变量、git 版本和代码快照，用户在面板上能看到、能对比、能重跑。

所以在装了丹炉的服务器上，训练、评测这类要 GPU 或要跑很久的命令都用 `danlu submit` 提交。直接 `nohup python …`、tmux 里 `torchrun` 起的进程，丹炉只会当成"外部进程"：它占着的卡不会再分给任何任务，队首任务会一直等它，用户也看不到这次运行的记录。几秒钟的检查命令（`nvidia-smi`、`python -c "import torch"`）照常直接跑就行。

## 先确认能用

```bash
danlu gpus        # 能打印卡和队列，就说明连上了
```

- 找不到命令时试 `~/bin/danlu`。经 SSH 执行命令时，非交互 shell 常常没把 `~/bin` 加进 PATH，直接写完整路径最省事。
- 退出码 3（`cli_unreachable`）：服务没在运行、`DANLU_HOME` 指向了别的数据目录，或者当前用户不是运行 danlu 的那个用户（`sudo danlu …` 会被拒）。不要自己去启动、重启或用 sudo，把错误原样告诉用户。
- `cli_server_old`：服务还是旧版本，没有任务接口，需要用户执行 `danlu restart` 或升级。
- 这些命令只在服务器本机可用：它们经数据目录里的 `danlu.sock` 连本机的 danlu，不需要密码或令牌。

## 输出约定

需要读结果时都加 `--json`：

- 成功时 stdout 是一个 JSON 对象，字段和面板接口相同。
- 失败时 stderr 是一行 `{"error": "…", "code": "…", "params": {…}}`，按 `code` 判断，`error` 是给人看的中文说明。
- 命令从不交互，提示和错误只写 stderr。
- `tail` 和 `api` 没有 `--json`：`tail` 输出日志文本，`api` 原样输出接口响应。

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 其他错误：参数校验没过、服务端出错 |
| 2 | 用法错误：参数写错、未知命令 |
| 3 | 连不上服务 |
| 4 | 任务不存在 |
| 10 | 只有 `wait`、`tail -f`：有任务失败 |
| 11 | 只有 `wait`、`tail -f`：没有失败，但有任务被取消 |
| 124 | 只有 `wait`：到了 `--timeout`，还有任务没结束 |

选项一律写在 `--` 之前，`--` 后面的所有内容都属于任务命令。所以 `danlu submit -g 1 -- python eval.py --json` 里的 `--json` 是传给 `eval.py` 的。

`danlu <命令> -h` 打印完整用法。

## 提交

```bash
cd ~/proj
danlu submit -g 4 -e torch2 -n lr3e-4 \
  --key "lr3e-4-$(git rev-parse --short HEAD)" --json \
  -- torchrun --nproc_per_node 4 train.py --lr 3e-4
```

返回任务对象，要关心的字段是 `.id`、`.status`（`queued` 或 `running`）和 `.queue_pos`。

### 代码和工作目录

- 工作目录默认是当前目录，`-C 目录` 指定别的目录。丹炉不上传代码，代码要已经在服务器的这个目录里。
- **代码快照和 git 版本是在任务开始运行时记录的，不是提交时。** 任务排队期间改了工作目录里的代码，它跑的就是改过的版本。所以有任务在排队或刚提交时，不要改这个目录里的代码。要同时试不同的代码，就给每个版本开一个目录（比如 `git worktree add ../proj-exp2`）。
- 提交前先 `git commit`，`git_commit` 才能对应到确切的代码；有未提交改动的话，任务上会记 `git_dirty: true`。

### GPU

- `-g N` 要 N 张整卡，`-g 0` 是纯 CPU 任务。不写时用面板偏好里的默认值，所以最好明确写出来。
- `--pin 0,2` 指定卡号。只在用户点名要哪几张卡时用：队列严格先进先出，指定的卡被占着时，这个任务会挡住它后面的所有任务。
- 卡都忙时照样提交，任务会排队，等有空卡再开始。不要因为没空卡就绕开丹炉直接跑。

### 环境变量

- 任务不继承你当前 shell 的环境，和在面板上提交一样：只有服务自己的环境，加上 `--var`，再加上可选的 `~/.bashrc`。
- conda 环境用 `-e 名字`，不写就不激活。其他变量用 `--var K=V`，可以写多次。`--bashrc` 或 `--no-bashrc` 决定是否加载 `~/.bashrc`，不写时按面板偏好。
- 不能设置 `CUDA_VISIBLE_DEVICES`、`CUDA_DEVICE_ORDER`、`PET_MASTER_PORT` 和 `DANLU_*`，会被拒绝（`job_env_reserved`）。丹炉已经把 `CUDA_VISIBLE_DEVICES` 设成分到的卡，训练代码里不要再改它，也不要按 nvidia-smi 的编号选卡，否则会跑到别人的卡上。
- 任务里可以用这些变量：
  - `DANLU_OUTPUT_DIR`：这个任务自己的产物目录，checkpoint 和结果写在这里，面板能把产物对应回任务。
  - `DANLU_JOB_ID`：任务编号。
  - `DANLU_GPUS`：分到的物理卡号。
  - `PET_MASTER_PORT`：每个任务各不相同，torchrun 默认就读它，同时跑多个任务时端口不会冲突。

### 命令怎么写

- `--` 后面只有一个参数时，它原样作为 shell 脚本运行，可以写 `&&`、管道和 `cd`，例如 `-- 'cd src && python train.py'`。
- 有多个参数时，丹炉逐个加引号再拼起来，重跑时得到同样的参数。
- 多行脚本用 `--cmd-file 文件`，或者用 `--cmd-file -` 从标准输入读，最多 512 KiB。

### 防重复提交：`--key`

只要是脚本或你自己发起的提交，都带上 `--key`：

- 同一个 key 只会建一个任务。连接断开、命令超时，不知道上一次提交有没有成功时，原样再执行一次就行：如果已经提交过，会返回原来那个任务（退出码 0，文字输出是"已存在 #N"）。
- key 要从实验本身生成，例如 `<实验名>-<参数>-<commit>`。不要每次随机生成，那样重试就会重复提交。
- 同一个 key 配上不同的内容，会被拒绝（退出码 1，`job_submit_key_conflict`，`params.id` 是已有任务的编号）。比较的内容是工作目录、命令、GPU、conda、环境变量、`.bashrc` 和代码来源。扫参数时，每组参数要用不同的 key。
- key 只能用字母、数字和 `._:-`，长度 1 到 100。

### 其他选项

- `-n 名字`：取一个短而有意义的名字，面板列表里显示的就是它。较长的说明，比如为什么做这个实验，写进 `--memo`。
- `--notify` 或 `--no-notify`：任务结束时是否推送到手机。不写时按面板偏好，用户没提就别改。

## 等结果、看进度

```bash
danlu wait 42 --timeout 9m; echo $?    # 0 成功，10 失败，11 取消，124 还没结束
danlu show 42 --json                   # 状态、失败原因、分到的卡、代码版本
danlu tail 42 -n 100                   # 日志最后 100 行
```

**`wait` 会一直阻塞到任务结束。** 很多编程助手的 shell 工具会杀掉运行过久的命令，比如两分钟或十分钟就超时。所以要把 `--timeout` 设得比你的工具时限短，然后循环：收到 124 说明还在跑，用 `danlu tail <id> -n 20` 看一眼进度，再接着 `wait`。如果你的工具能在后台跑命令、结束时通知你，也可以把 `wait` 放到后台。不要用 `show` 反复 sleep 轮询，`wait` 本身就会每 2 秒查一次，服务重启的间隙也会自动重连。

`danlu tail -f` 也会阻塞到任务结束，适合放到后台，不适合在前台用。`tail` 默认把 tqdm 进度条折叠成最后一次的状态，并且在输出不是终端时去掉颜色，读起来和面板上一样；要原始字节用 `--raw`。

`show --json` 里常用的字段：

| 字段 | 含义 |
|---|---|
| `status` | `queued` / `running` / `succeeded` / `failed` / `cancelled` |
| `queue_pos`, `wait_reason` | 排在第几位，为什么还没开始 |
| `held` | 只在为 `true` 时出现：排队任务被暂缓了，留在原位但不会开始 |
| `gpus_assigned` | 分到的物理卡号 |
| `exit_code`, `fail_kind`, `fail_detail`, `fail_reason` | 失败的分类、细分和说明，见下一节 |
| `error_line` | 日志里识别出的报错行 |
| `last_line` | 最后一行训练输出 |
| `git_commit`, `git_dirty`, `snapshot_ok` | 实际跑的代码版本，在开始运行时记录 |
| `output_dir` | `DANLU_OUTPUT_DIR` 的实际路径 |
| `started_at`, `ended_at` | Unix 秒 |

列表：`danlu jobs --status running,queued`，`--workdir .` 只看当前项目的任务，`-q 文本` 按名字、命令或备注搜索，`--limit N` 控制条数，加 `--json` 拿结构化结果。

### 为什么还在排队

先看 `wait_reason`，或者运行 `danlu gpus`：它列出每张卡被谁占着（丹炉任务、外部进程或 GPU 预约），最后一行说明队首任务在等什么。

- 卡被别的任务占着：正常现象，等着就行。
- 外部进程或来源不明的显存占用：有人在丹炉之外用卡。告诉用户是哪张卡、哪个进程，不要去 kill 它。
- `wait_reason` 是"已暂缓"（`held: true`）：任务被按住了，不会开始，也不挡后面的任务。可能是用户暂缓的，问过用户再 `danlu release <id>`。
- 显示"队列已暂停"：可能是用户手动暂停，可能是有任务刚启动就失败触发了自动暂停，也可能是磁盘空间不足。暂停期间什么任务都不会开始。把原因告诉用户，由用户决定是否继续。
- GPU 数据暂不可信（nvidia-smi 出错或超时）：恢复之前不会启动需要 GPU 的任务。

## 失败了怎么办

先看 `danlu show <id> --json` 里的 `fail_kind`、`fail_reason` 和 `error_line`，再用 `danlu tail <id> -n 200` 看完整的报错上下文。

| `fail_kind` | 含义 | 通常怎么做 |
|---|---|---|
| `cuda_oom` | CUDA 显存不足 | 减小 batch size、开梯度累积或激活检查点，或者多要几张卡 |
| `host_oom` | 主机内存不足 | 减少 dataloader worker 数，避免把整个数据集读进内存 |
| `exception` | Python 异常 | 从 traceback 定位，修好代码后提交新任务 |
| `command` | 命令这一层出错，没进到 Python。`fail_detail` 细分：`not_found` 命令没找到（退出码 127），`not_executable` 不能执行（126），`conda` 找不到 conda 或 `conda activate` 失败，`workdir` 进不了工作目录，空串是 bash 或 python 报文件不存在、没有权限 | 多半是 `-e` 的环境不对、没选环境或路径写错，看 `error_line`；改好环境、命令或路径后提交新任务，原样 `rerun` 还会失败 |
| `nccl` | NCCL 通信超时或出错 | 往前翻日志，找第一个出错的 rank。常常与代码无关，可以 `rerun` |
| `reboot` | 服务器重启，任务中断 | 与代码无关，直接 `rerun` |
| `killed` | 进程被外部终止，没留下退出码 | 可能是有人手动杀了进程，先问用户再重跑 |
| `signal` | 进程被信号终止 | 同上 |
| `start` | 任务没能启动 | 加载 `.bashrc` 出错、恢复代码快照失败、产物目录写不进去等，看 `fail_reason` |
| `exit` | 退出码非 0，没有更具体的分类 | 看日志末尾和 `error_line`，按程序自己的输出判断 |

## 重跑

```bash
danlu rerun 42 --key rerun-42-a --json
```

- `rerun` 按原任务的工作目录、命令、conda、环境变量和 GPU 要求提交一个新任务，名字自动加上 `-r2`、`-r3`。
- 用哪份代码：默认情况下，`reboot`、`killed`、`signal`、`nccl` 这类与代码无关的失败，用原任务的代码快照重跑；其他情况用工作目录里现在的代码。`--snapshot` 或 `--current-code` 可以强制指定，输出里会写明用的是哪份。
- 要改参数，就用 `submit` 提交一个新任务，`rerun` 只做原样复制。

## 调整排队中的任务

队列严格按顺序：队首放不下时，后面的任务都不开始。顺序可以调，但只对还在排队的任务有效，已经开始的任务不受影响。

```bash
danlu move 45 top        # 置顶；也可以 up、down、bottom。输出"#45 现在排第 1 位"
danlu hold 46 47         # 暂缓：留在原来的位置，调度时跳过，也不挡后面的任务
danlu release 46 47      # 恢复，按原来的位置参与调度
```

- 这三个命令都接 `--json`：`move` 返回移动后的任务对象，`hold`、`release` 返回 `{"action","items":[{"id","name","changed"}…]}`，`changed: false` 表示它本来就是那个状态。
- `hold`、`release` 接多个编号时，要么全部生效，要么一个也不改。有任务已经开始或结束时整批失败（退出码 1，`job_already_started` 或 `job_not_queued`，`params.id` 是那个任务）；编号不存在时退出码 4。
- 只调整你自己这次提交的任务。别人的任务挪到后面、暂缓，都会让人家等更久，先问用户。
- 暂缓的任务不算"还没跑完"：其他任务都结束、只剩暂缓的时，丹炉照样发"队列已跑完"的推送。
- 要改排队任务的命令或参数，在面板上打开它点"修改"（编号和位置不变）；命令行没有对应命令，或者取消后用 `submit` 重新提交。

## 扫参数

```bash
rev=$(git rev-parse --short HEAD)
ids=()
for lr in 1e-4 3e-4 1e-3; do
  out=$(danlu submit -g 1 -e torch2 -n "lr$lr" --key "sweep-lr$lr-$rev" --json \
          -- python train.py --lr "$lr") || exit 1
  ids+=("$(jq -r .id <<<"$out")")
done
danlu wait "${ids[@]}" --timeout 9m
```

- 一次全部提交就行，排不下的会排队，按先后顺序开始。
- 这些任务共用一个工作目录。在它们全部开始运行之前，不要改那里的代码，原因见上面的"代码和工作目录"。
- `wait` 接多个编号时，要等到全部结束才返回。只要有一个失败，退出码就是 10；加 `--json` 能拿到每个任务的最终状态，从中找出是哪个失败了。
- 任务启动后 2 分钟内就失败、后面还有没暂缓的任务在排队时，丹炉会自动暂停队列，免得一个 bug 把整批任务都跑失败。这时 `wait` 会一直等到超时，`danlu gpus` 显示"队列已暂停"。先取消你自己那些还在排队、会碰到同一个 bug 的任务，修好代码重新提交，再告诉用户队列暂停了。只是参数写错时，用户也可以在面板的暂停提示里点"修改排队中的任务"，批量替换命令里的文字。继续队列由用户在面板上点，或者经用户同意后执行 `danlu api POST /api/queue/resume`。
- 服务器上没有 `jq` 时，用 `python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'` 代替。

## 从本地经 SSH 调用

助手在本地电脑上、训练在服务器上时：

```bash
rsync -a ./ gpu1:~/proj/         # 丹炉不同步代码；带上 .git，任务才能记下 git 版本
ssh gpu1 'cd ~/proj && ~/bin/danlu submit -g 4 -e torch2 --key exp7-a1b2c3 --json --cmd-file -' <<'EOF'
torchrun --nproc_per_node 4 train.py --lr 3e-4 --out "$DANLU_OUTPUT_DIR"
EOF
```

- 远端 shell 会把命令再解析一遍，引号容易出错。复杂命令用 `--cmd-file -` 加带引号的 heredoc（`<<'EOF'`），任务命令原样送到服务器，`$DANLU_OUTPUT_DIR` 也要等到任务运行时才展开。
- `ssh` 透传远端命令的退出码。255 是 ssh 自己出错（连不上或中途断开），提交可能已经发出去了，也可能没有，用同一个 `--key` 原样重试即可。
- 等待同样受工具时限约束：`ssh gpu1 '~/bin/danlu wait 42 --timeout 9m'`，收到 124 就循环。

## 边界

服务器通常是几个人共用的。下面这些操作会影响到别人，先问用户：

- 只取消、重跑、挪动和暂缓你自己在这次工作中提交的任务。别人的任务，或者你不确定是谁提交的，先问。取消一律用 `danlu cancel <id>`，不要直接 kill 训练进程：它会先发 SIGTERM，宽限期后强制结束，并且连 torchrun 另起会话的 worker 一起收拾干净。
- `danlu api` 只用 `GET` 来查询。非 GET 请求会改设置、暂停或继续队列、改预约，只在用户明确要求时才用。
- 不要运行运维命令：`init`、`cert`、`passwd`、`entrance`、`port`、`prune`、`replace`、`update`、`restart`、`strict`、`totp`、`token`。它们一条就能让所有设备连不上面板，或者删掉文件。
- 不要绕开队列。卡都忙、队列暂停时，把情况告诉用户，不要改用 nohup 直接跑。

汇报时给出任务编号，用户在面板的 `/jobs/<id>` 上能看到这个任务的日志和代码版本。
