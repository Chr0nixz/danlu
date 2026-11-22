#!/usr/bin/env bash
# 严格模式可行性探测：用 cgroup 设备控制（systemd 的 DevicePolicy/DeviceAllow）把进程限制在指定的卡上，
# 看 CUDA、nvidia-smi 和 EGL 渲染在限制下怎么表现，据此决定丹炉的严格模式怎么做。
#
# 在 GPU 服务器上用跑 danlu 的用户运行，中途会要 sudo 密码：
#   bash ~/s0/strict-probe.sh          # 自动挑编号最大的两张空闲卡放行
#   bash ~/s0/strict-probe.sh 6,7      # 指定放行的卡（nvidia-smi 编号）
# 报告同时存到当前目录的 strict-probe-<时间>.txt，整份发回来。
#
# 不改任何系统配置：每一步都是 systemd-run 起的临时服务，以当前用户身份运行，结束自动回收，最多跑 90 秒。
# 只在放行的卡上建 CUDA 上下文、分配 1 MiB 显存、初始化 EGL（各占几百 MB，马上释放），所以放行的卡要空闲；
# 其余卡只枚举、不建上下文，不影响上面正在跑的任务。设备限制没生效时自动跳过建上下文和 EGL 初始化。
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
report="strict-probe-$(date +%Y%m%d-%H%M%S).txt"
exec > >(tee "$report") 2>&1

section() { printf '\n== %s ==\n' "$1"; }
die() {
  echo "无法继续：$*"
  exit 1
}
# PCI 总线号去掉域号再比：域号有 4 位和 8 位两种写法
bus_key() {
  local b
  b=$(printf '%s' "$1" | tr -d ' ' | tr 'A-F' 'a-f')
  echo "${b#*:}"
}
join() {
  local IFS=,
  echo "$*"
}

section 环境
echo "内核 $(uname -r)"
command -v systemd-run >/dev/null || die "没有 systemd-run"
systemctl --version | head -1
echo "cgroup 文件系统：$(stat -fc %T /sys/fs/cgroup 2>/dev/null)（cgroup2fs 是 v2，tmpfs 是 v1 或混合模式）"
SMI=$(command -v nvidia-smi) || die "没有 nvidia-smi"
PY=$(command -v python3) || die "没有 python3"
[ -f "$here/strict_probe.py" ] || die "找不到 $here/strict_probe.py，把整个 s0 目录一起上传"
smi() { timeout 20 "$SMI" "$@"; }
echo "驱动 $(smi --query-gpu=driver_version --format=csv,noheader | head -1)"
echo "python3：$PY"

section 卡
# 每张卡：nvidia-smi 编号、总线号、已用显存、/dev/nvidiaN 的 N（不一定等于编号）、对应的 DRM 节点
IDX=() BUS=() USED=() MINOR=() NODES=()
while IFS=, read -r idx bus used; do
  idx=${idx// /} key=$(bus_key "$bus") used=${used// /} minor=? nodes=""
  for info in /proc/driver/nvidia/gpus/*/information; do
    [ "$(bus_key "$(basename "$(dirname "$info")")")" = "$key" ] || continue
    minor=$(awk -F: '/Device Minor/ { gsub(/[ \t]/, "", $2); print $2 }' "$info")
  done
  for node in /sys/class/drm/*; do
    name=$(basename "$node")
    [[ $name =~ ^(card|renderD)[0-9]+$ ]] || continue
    [ "$(bus_key "$(basename "$(readlink -f "$node/device")")")" = "$key" ] && nodes+="/dev/dri/$name "
  done
  nodes=${nodes% }
  IDX+=("$idx") BUS+=("$key") USED+=("$used") MINOR+=("${minor:-?}") NODES+=("$nodes")
  echo "GPU $idx  总线 $key  已用 $used MiB  /dev/nvidia${minor:-?}  ${nodes:-（没有 DRM 节点）}"
done < <(smi --query-gpu=index,pci.bus_id,memory.used --format=csv,noheader,nounits)
n=${#IDX[@]}
[ "$n" -ge 2 ] || die "至少要两张卡：一张放行、一张拦住"

if [ $# -ge 1 ]; then
  IFS=, read -ra pick <<<"$1"
else
  pick=()
  for ((i = n - 1; i >= 0 && ${#pick[@]} < 2; i--)); do
    [ "${USED[$i]}" -lt 500 ] 2>/dev/null && pick=("${IDX[$i]}" ${pick[@]+"${pick[@]}"})
  done
  [ ${#pick[@]} -ge 1 ] || die "没找到空闲的卡（已用显存低于 500 MiB），请指定放行的卡，比如：bash $0 6,7"
fi
in_pick() {
  local x
  for x in "${pick[@]}"; do [ "$x" = "$1" ] && return 0; done
  return 1
}
for x in "${pick[@]}"; do
  printf '%s\n' "${IDX[@]}" | grep -qx "$x" || die "没有 GPU $x"
done
[ ${#pick[@]} -lt "$n" ] || die "至少留一张卡拦住，不然看不出限制的效果"

# 放行清单：基础伪设备（DevicePolicy=closed 自带）、终端、驱动公共节点、放行的卡和它们的 DRM 节点
allow=("char-pts rw")
for f in /dev/nvidiactl /dev/nvidia-uvm /dev/nvidia-uvm-tools /dev/nvidia-modeset /dev/nvidia-caps/*; do
  [ -e "$f" ] && allow+=("$f rw")
done
allow_dev=(/dev/nvidiactl) deny_dev=() allow_bus=() deny_bus=()
for ((i = 0; i < n; i++)); do
  if in_pick "${IDX[$i]}"; then
    [ "${MINOR[$i]}" != "?" ] || die "认不出 GPU ${IDX[$i]} 的 /dev/nvidiaN"
    allow+=("/dev/nvidia${MINOR[$i]} rw")
    for node in ${NODES[$i]}; do allow+=("$node rw"); done
    allow_dev+=("/dev/nvidia${MINOR[$i]}") allow_bus+=("${BUS[$i]}")
  else
    [ "${MINOR[$i]}" != "?" ] && deny_dev+=("/dev/nvidia${MINOR[$i]}")
    deny_bus+=("${BUS[$i]}")
  fi
done
props=(-p DevicePolicy=closed)
for a in "${allow[@]}"; do props+=(-p "DeviceAllow=$a"); done
phys=$(join "${pick[@]}")
rel=$(seq -s, 0 $((${#pick[@]} - 1)))
echo
echo "放行 GPU $phys，其余 $((n - ${#pick[@]})) 张拦住。放行的设备："
printf '  %s\n' "${allow[@]}"

results=$(mktemp)
trap 'rm -f "$results"' EXIT
# 被测程序最后一行是 "RESULT {json}"，收进结果文件，其余照常输出
collect() {
  local line
  while IFS= read -r line; do
    case $line in
    "RESULT "*) printf '%s\n' "${line#RESULT }" >>"$results" ;;
    *) printf '%s\n' "$line" ;;
    esac
  done
}
probe() { "$PY" "$here/strict_probe.py" "$@"; }

section "不受限（对照，只枚举）"
env -u CUDA_VISIBLE_DEVICES CUDA_DEVICE_ORDER=PCI_BUS_ID "$PY" "$here/strict_probe.py" cuda cuda_base | collect
env -u CUDA_VISIBLE_DEVICES "$PY" "$here/strict_probe.py" egl egl_base | collect

# run_strict <CUDA_VISIBLE_DEVICES，- 表示不设> <命令…>：在只放行上面那些设备的临时服务里，以当前用户运行
run_strict() {
  local cvd=$1
  shift
  local env=(--setenv="PATH=$PATH" --setenv="HOME=$HOME" --setenv=CUDA_DEVICE_ORDER=PCI_BUS_ID)
  [ "$cvd" = - ] || env+=(--setenv="CUDA_VISIBLE_DEVICES=$cvd")
  [ -n "${LD_LIBRARY_PATH:-}" ] && env+=(--setenv="LD_LIBRARY_PATH=$LD_LIBRARY_PATH")
  sudo systemd-run --quiet --wait --pipe --collect --uid="$(id -un)" --gid="$(id -gn)" \
    --slice="user-$(id -u).slice" -p RuntimeMaxSec=90 "${props[@]}" "${env[@]}" -- "$@"
}

section sudo
echo "接下来用 sudo 起临时服务（以 $(id -un) 身份运行，只放行上面的设备）"
sudo -v || die "sudo 失败"

section "受限：直接打开设备文件"
run_strict - "$PY" "$here/strict_probe.py" open open "$(join "${allow_dev[@]}")" "$(join ${deny_dev[@]+"${deny_dev[@]}"})" | collect
ctx=ctx init=init
if ! "$PY" -c '
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1])]
opens = [r for r in rows if r["label"] == "open"]
sys.exit(0 if opens and all(v != "ok" for v in opens[-1]["denied"].values()) else 1)' "$results"; then
  echo "设备限制没有生效或没跑成：跳过建上下文和 EGL 初始化，免得占到正在用的卡"
  ctx="" init=""
fi

section "受限：nvidia-smi"
out=$(run_strict - "$SMI" -L 2>&1)
code=$?
printf '%s\n' "$out"
printf '{"kind":"smi","label":"smi","code":%d,"gpus":%d}\n' "$code" "$(printf '%s\n' "$out" | grep -c '^GPU ')" >>"$results"
run_strict - "$SMI" --query-gpu=index,pci.bus_id,memory.used --format=csv,noheader 2>&1

section "受限：CUDA，不设 CUDA_VISIBLE_DEVICES"
run_strict - "$PY" "$here/strict_probe.py" cuda cuda_plain $ctx | collect
section "受限：CUDA，按物理编号设 CUDA_VISIBLE_DEVICES=$phys"
run_strict "$phys" "$PY" "$here/strict_probe.py" cuda cuda_physical | collect
section "受限：CUDA，按 0 起的编号设 CUDA_VISIBLE_DEVICES=$rel"
run_strict "$rel" "$PY" "$here/strict_probe.py" cuda cuda_relative | collect

section "受限：EGL"
run_strict - "$PY" "$here/strict_probe.py" egl egl_strict $init | collect

section 原始结果
cat "$results"
probe summary "$results" "$(join "${allow_bus[@]}")" "$(join "${deny_bus[@]}")"
echo
echo "报告已存到 $(pwd)/$report"
