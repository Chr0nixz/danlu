#!/usr/bin/env bash
# S0：模拟一个 worker 被外部杀掉。按 DANLU_JOB_ID 找到这个任务的进程，kill -9 其中的一个 python worker。
#   bash kill_worker.sh <任务 id> [dry]   # 加 dry 只列出进程，不杀
set -uo pipefail
ID=${1:?用法: bash kill_worker.sh <任务 id> [dry]}

echo "pid | ppid | sid | 命令"
victim=
for d in /proc/[0-9]*; do
  pid=${d#/proc/}
  tr '\0' '\n' <"$d/environ" 2>/dev/null | grep -qx "DANLU_JOB_ID=$ID" || continue
  line=$(ps -o pid=,ppid=,sid=,args= -p "$pid" 2>/dev/null) || continue
  echo "$line" | cut -c1-140
  # 挑最后一个直接跑训练脚本的 python（不是 torchrun 本身）
  case $line in
    *torchrun*|*torch.distributed.run*) ;;
    *python*) victim=$pid ;;
  esac
done

if [ -z "$victim" ]; then
  echo "没找到任务 #$ID 的 python worker"
  exit 1
fi
if [ "${2:-}" = dry ]; then
  echo "会杀掉 pid $victim（dry，没有执行）"
  exit 0
fi
kill -9 "$victim" && echo "已 kill -9 pid $victim"
