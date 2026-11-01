#!/usr/bin/env bash
# S0 真机信息采集：在 GPU 服务器上运行，生成 s0-report-<主机名>.tar.gz，检查过内容后贴回来。
#   bash collect.sh [danlu 路径，默认 ~/bin/danlu]
#
# 只读，不改系统配置。唯一的写操作是临时建一个设备令牌去请求本机的接口，脚本退出时吊销。
# 打包前会把公网 IP、安全入口和令牌替换成占位符。
set -uo pipefail

# 连 127.0.0.1 不走代理：有些机器上设了 http_proxy / all_proxy，带着它请求本机会连不上
export no_proxy=127.0.0.1,localhost NO_PROXY=127.0.0.1,localhost

DANLU=${1:-$HOME/bin/danlu}
HOST=$(hostname -s 2>/dev/null || hostname)
OUT=$(mktemp -d "/tmp/s0-report-$HOST.XXXX")
TOKEN_ID=

cleanup() {
  [ -n "$TOKEN_ID" ] && "$DANLU" token revoke "$TOKEN_ID" >/dev/null 2>&1
}
trap cleanup EXIT

# run <文件名> <命令…>：把命令、退出码和输出一起记下来，失败也继续
run() {
  local f=$OUT/$1; shift
  { echo "\$ $*"; "$@" 2>&1; echo "[退出码 $?]"; } >"$f"
}
# 带超时的版本：nvidia-smi 卡住时不让脚本也卡住
runt() {
  local f=$1; shift
  run "$f" timeout 20 "$@"
}

echo "采集到 $OUT"

# ---------- 系统 ----------
run os.txt sh -c 'cat /etc/os-release; echo; uname -a; echo; cat /proc/sys/kernel/random/boot_id; echo; uptime; echo; nproc; free -b'
run systemd.txt sh -c 'systemctl --user show danlu -p KillMode -p Restart -p ActiveState -p SubState -p MainPID -p ExecStart -p Environment; echo; loginctl show-user "$USER" -p Linger; echo; systemctl --user cat danlu'
run cgroup.txt sh -c 'cat /proc/self/cgroup; echo; p=$(systemctl --user show danlu -p ControlGroup --value); echo "ControlGroup=$p"; f=/sys/fs/cgroup$p/memory.events; echo "$f"; cat "$f"'
run journal.txt journalctl --user -u danlu -n 500 --no-pager

# ---------- GPU ----------
# 和 internal/gpu/gpu.go 里的两条查询完全一致
runt smi-full.txt nvidia-smi
runt smi-query-gpu.txt nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw --format=csv,noheader,nounits
runt smi-compute-apps.txt nvidia-smi --query-compute-apps=pid,gpu_uuid,used_memory --format=csv,noheader,nounits
runt smi-driver.txt nvidia-smi --query-gpu=index,name,driver_version,persistence_mode,pci.bus_id --format=csv
runt smi-time.txt sh -c 'time nvidia-smi --query-gpu=index --format=csv,noheader >/dev/null'

# compute-apps 里的每个 pid：属于谁、命令是什么、danlu 能不能读到它的 environ（认不认得出是哪个任务）
{
  echo "pid | 用户 | environ 可读 | DANLU_JOB_ID | 命令"
  timeout 20 nvidia-smi --query-compute-apps=pid --format=csv,noheader,nounits 2>/dev/null | while read -r pid; do
    pid=${pid// /}
    [ -z "$pid" ] && continue
    if [ ! -d "/proc/$pid" ]; then
      echo "$pid | （本机 /proc 里没有这个 pid，可能在容器的 pid 命名空间里）"
      continue
    fi
    user=$(ps -o user= -p "$pid" 2>/dev/null)
    cmd=$(ps -o args= -p "$pid" 2>/dev/null | cut -c1-120)
    if env=$(tr '\0' '\n' <"/proc/$pid/environ" 2>/dev/null); then
      readable=是
      job=$(echo "$env" | sed -n 's/^DANLU_JOB_ID=//p')
    else
      readable=否 job=
    fi
    echo "$pid | $user | $readable | ${job:--} | $cmd"
  done
} >"$OUT/gpu-procs.txt" 2>&1

# ---------- conda ----------
run conda.txt sh -c 'command -v conda; conda --version; conda info --base; conda env list'

# ---------- danlu ----------
run danlu-version.txt "$DANLU" version
run danlu-info.txt "$DANLU" info --json
run danlu-config.txt sh -c "sed -E 's/(password_hash *= *).*/\\1\"<已去掉>\"/' \"\${DANLU_HOME:-\$HOME/.danlu}/config.toml\""
run danlu-dirs.txt sh -c 'd=${DANLU_HOME:-$HOME/.danlu}; ls -la "$d" "$d/backup" 2>&1; du -sh "$d"/* 2>&1; df -h "$d"'

# 用临时设备令牌请求本机接口。证书里总带着 127.0.0.1，所以直接连回环地址
INFO=$("$DANLU" info --json 2>/dev/null)
BASE_URL=$(echo "$INFO" | sed -n 's/.*"base_url": *"\([^"]*\)".*/\1/p')
URL=$(echo "$INFO" | sed -n 's/.*"url": *"\([^"]*\)".*/\1/p')
PUBLIC_HOST=$(echo "$BASE_URL" | sed -E 's#^[a-z]+://\[?([^]/]*)\]?:[0-9]+$#\1#')
PORT=${BASE_URL##*:}
SCHEME=${BASE_URL%%://*}
ENTRANCE=${URL##*/}
if TOK_JSON=$("$DANLU" token create --name "s0-collect" --json 2>/dev/null); then
  TOKEN_ID=$(echo "$TOK_JSON" | sed -n 's/.*"id": *\([0-9]*\).*/\1/p')
  TOKEN=$(echo "$TOK_JSON" | sed -n 's/.*"token": *"\([^"]*\)".*/\1/p')
  CA=$OUT/.ca.crt
  cp "${DANLU_HOME:-$HOME/.danlu}/tls/ca.crt" "$CA" 2>/dev/null
  for ep in me gpus system jobs; do
    curl -s --max-time 10 ${CA:+--cacert "$CA"} -H "Authorization: Bearer $TOKEN" \
      "$SCHEME://127.0.0.1:$PORT/api/$ep" >"$OUT/api-$ep.json" 2>&1 || echo "请求失败" >>"$OUT/api-$ep.json"
  done
  rm -f "$CA"
else
  echo "创建设备令牌失败，跳过接口采集" >"$OUT/api-skipped.txt"
fi

# ---------- 脱敏、打包 ----------
for f in "$OUT"/*; do
  [ -f "$f" ] || continue
  [ -n "$PUBLIC_HOST" ] && sed -i "s/$(printf '%s' "$PUBLIC_HOST" | sed 's/[.[\*^$/]/\\&/g')/<公网IP>/g" "$f"
  [ -n "$ENTRANCE" ] && sed -i "s/$ENTRANCE/<安全入口>/g" "$f"
  [ -n "${TOKEN:-}" ] && sed -i "s/$TOKEN/<令牌>/g" "$f"
  # CA 证书是公开的，但太长，看报告时碍事
  sed -i '/"ca_pem"/s/: *".*"/: "<已去掉>"/' "$f"
done

TAR=$PWD/s0-report-$HOST.tar.gz
tar -czf "$TAR" -C "$(dirname "$OUT")" "$(basename "$OUT")"
echo
echo "已生成 $TAR"
echo "先看一眼里面有没有不想外传的东西（命令行、路径、用户名）：tar -tzf $TAR；内容在 $OUT"
