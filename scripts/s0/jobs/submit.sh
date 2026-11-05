#!/usr/bin/env bash
# 在验收服务器上提交一批受控任务。每个任务申请一张 GPU。
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "用法：bash submit.sh <morning|quick-fail> <conda 环境> [danlu 路径]" >&2
  exit 2
fi
SCENARIO=$1
CONDA_ENV=$2
DANLU=${3:-$HOME/bin/danlu}
WORKDIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
LONG_MINUTES=${DANLU_S0_LONG_MINUTES:-60}
case "$SCENARIO" in
  morning)
    if [[ ! "$LONG_MINUTES" =~ ^[1-9][0-9]*$ ]]; then
      echo "DANLU_S0_LONG_MINUTES 必须是正整数分钟数" >&2
      exit 2
    fi
    ;;
  quick-fail) ;;
  *)
    echo "场景须为 morning 或 quick-fail" >&2
    exit 2
    ;;
esac

source "$WORKDIR/../api.sh"
trap s0_close_api EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
s0_open_api s0-submit
active=$(s0_api 'jobs?status=queued,running&size=1' | python3 -c 'import json,sys; print(json.load(sys.stdin)["total"])')
if [ "$active" != 0 ]; then
  echo "请等现有任务全部结束后再提交验收批次" >&2
  exit 1
fi
paused=$(s0_api queue | python3 -c 'import json,sys; print(json.load(sys.stdin)["paused"])')
case "$SCENARIO:$paused" in
  morning:False|quick-fail:True) ;;
  morning:*) echo "请先继续队列，再提交早上看手机的批次" >&2; exit 1 ;;
  *) echo "请先手动暂停队列，等十个任务都提交后再继续" >&2; exit 1 ;;
esac

submit() {
  local body
  body=$(python3 -c 'import json,sys
workdir,env,name,cmd=sys.argv[1:]
print(json.dumps(dict(workdir=workdir,conda_env=env,name=name,cmd=cmd,gpus=1)))' "$WORKDIR" "$CONDA_ENV" "$1" "$2")
  s0_api jobs -H 'Content-Type: application/json' --data-binary "$body" |
    python3 -c 'import json,sys; j=json.load(sys.stdin); print("#"+str(j["id"]), j["name"])'
}

if [ "$SCENARIO" = morning ]; then
  cards=$(s0_api gpus | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(0 if d.get("error") else sum(not g.get("job") and not g.get("external") for g in d["gpus"]))')
  if [ "$cards" -lt 2 ]; then
    echo "这个场景需要至少两张 GPU，才能同时看到长任务和失败结果" >&2
    exit 1
  fi
  for i in {1..4}; do
    submit "验收-成功-$i" 'python ddp_dummy.py --minutes 1 --mem-gb 0.25'
  done
  submit "验收-长跑" "python ddp_dummy.py --minutes $LONG_MINUTES --mem-gb 0.25"
  submit "验收-显存不足" 'python fail_cuda_oom.py --blocks 10000'
else
  submit "验收-参数拼错" 'python ddp_dummy.py --lrr 3e-4'
  for i in {1..9}; do
    submit "验收-排队-$i" 'python ddp_dummy.py --minutes 1 --mem-gb 0.25'
  done
  echo "已提交十个任务，现在可在面板上继续队列。"
fi
