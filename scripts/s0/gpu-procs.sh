#!/usr/bin/env bash
# 列出每张卡上的全部进程（CUDA 进程和图形进程），以及每个进程启动时看到的选卡变量。
# 用来查"分配给任务的卡和实际占用对不上"：在 GPU 服务器上用跑 danlu 的用户运行
#   bash gpu-procs.sh
#
# 只读。/proc/<pid>/environ 是进程启动那一刻的环境，程序启动后自己改的 os.environ 看不到。
set -uo pipefail

smi() { timeout 20 nvidia-smi "$@"; }

# PCI 总线号去掉域号再比：域号有 4 位和 8 位两种写法
bus_key() { local b; b=$(printf '%s' "$1" | tr -d ' ' | tr 'A-F' 'a-f'); echo "${b#*:}"; }

# 每行"总线号 卡号 UUID"
cards=$(smi --query-gpu=index,pci.bus_id,uuid --format=csv,noheader | while IFS=, read -r idx bus uuid; do
  echo "$(bus_key "$bus") ${idx// /} ${uuid// /}"
done)
index_of() { printf '%s\n' "$cards" | awk -v k="$(bus_key "$1")" '$1 == k { print $2; exit }'; }
# 把 GPU-xxxx 形式的 CUDA_VISIBLE_DEVICES 换算成 nvidia-smi 的卡号，对不上的写 ?
uuids_to_index() {
  printf '%s\n' "$1" | tr ',' '\n' | while read -r u; do
    printf '%s\n' "$cards" | awk -v u="$u" '$3 == u { print $2; found = 1; exit } END { if (!found) print "?" }'
  done | paste -sd, -
}

# describe <pid>：用户、DANLU_RUN_DIR、选卡和 EGL/OpenGL 相关变量、命令行
describe() {
  local pid=$1 env user cmd
  if [ ! -d "/proc/$pid" ]; then
    echo "（本机 /proc 里没有这个 pid，可能在别的容器或 pid 命名空间里）"
    return
  fi
  user=$(ps -o user= -p "$pid" 2>/dev/null)
  cmd=$(ps -o args= -p "$pid" 2>/dev/null | cut -c1-160)
  if ! env=$(tr '\0' '\n' 2>/dev/null <"/proc/$pid/environ"); then
    echo "$user | （environ 读不到，不是本用户的进程）| $cmd"
    return
  fi
  val() { printf '%s\n' "$env" | grep -m1 "^$1=" | cut -d= -f2-; }
  has() { printf '%s\n' "$env" | grep -q "^$1="; }
  local run cvd order gl
  run=$(val DANLU_RUN_DIR)
  if has CUDA_VISIBLE_DEVICES; then cvd=$(val CUDA_VISIBLE_DEVICES); cvd=${cvd:-（空）}; else cvd=未设置; fi
  case $cvd in GPU-*) cvd="$cvd（即卡 $(uuids_to_index "$cvd")）" ;; esac
  order=$(val CUDA_DEVICE_ORDER)
  gl=$(printf '%s\n' "$env" | grep -E '^(MUJOCO_GL|MUJOCO_EGL_DEVICE_ID|PYOPENGL_PLATFORM|EGL_DEVICE_ID|RAY_ADDRESS)=' | tr '\n' ' ')
  echo "$user | ${run:--} | CUDA_VISIBLE_DEVICES=$cvd CUDA_DEVICE_ORDER=${order:-未设置} ${gl}| $cmd"
}

echo "卡 | pid | 类型 | 显存 | 用户 | DANLU_RUN_DIR | 选卡变量 | 命令"
echo "（类型：C 是 CUDA，G 是图形/EGL 渲染，C+G 两者都有。DANLU_RUN_DIR 是 <数据目录>/run/<任务号>，没有就是 danlu 认不出的外部进程）"
smi -q -d PIDS | awk '
  function flush() { if (pid != "") print bus, pid, type, mem; pid = "" }
  /^GPU / { flush(); bus = $2; next }
  /Process ID/ { flush(); pid = $NF; type = "?"; mem = "?"; next }
  /^[ \t]+Type[ \t]/ { type = $NF; next }
  /Used GPU Memory/ { sub(/^[^:]*:[ \t]*/, ""); mem = $1; next }
  END { flush() }
' | while read -r bus pid type mem; do
  index=$(index_of "$bus")
  echo "GPU ${index:-?} | $pid | $type | ${mem} MiB | $(describe "$pid")"
done

# RL 框架常用 Ray。ray.init() 会优先连本机已有的集群（ray start 起的），那样 worker 不是任务启动的，
# 带的是集群自己的环境变量和选卡，danlu 也认不出它们
echo
echo "Ray 进程："
for pid in $(pgrep -f 'raylet|gcs_server' 2>/dev/null); do
  echo "$pid | $(describe "$pid")"
done
