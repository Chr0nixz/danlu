#!/usr/bin/env bash
# danlu 端到端测试：假 GPU + 本地 HTTP，在 Linux 或 WSL 里运行。
#   bash scripts/e2e.sh [danlu 二进制路径]
set -uo pipefail

BIN_SRC=${1:-"$(dirname "$0")/../dist/danlu-linux-amd64"}
T=$(mktemp -d /tmp/danlu-e2e.XXXX)
BIN=$T/danlu
cp "$BIN_SRC" "$BIN" && chmod +x "$BIN"
export DANLU_HOME=$T/home DANLU_FAKE_GPUS=4 DANLU_FAKE_BUSY=3
# 清掉代理设置：WSL 的登录 shell 里常设着 http_proxy / all_proxy，
# 带着它们 curl 连 127.0.0.1 也要走代理，代理不通时整套测试会卡在超时上
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
export no_proxy=127.0.0.1,localhost NO_PROXY=127.0.0.1,localhost
PORT=${E2E_PORT:-17699} # 和演示环境（17600）错开
BASE=http://127.0.0.1:$PORT
JAR=$T/cookies
PASS=0 FAIL=0
SERVER_PID=
NTFY_PID=
API_AUTH=()

ok()   { echo "  ✔ $*"; PASS=$((PASS+1)); }
bad()  { echo "  ✘ $*"; FAIL=$((FAIL+1)); }
check() { local desc=$1; shift; if "$@"; then ok "$desc"; else bad "$desc"; fi; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
api()  { curl -s -b "$JAR" -c "$JAR" "${API_AUTH[@]}" -H 'Content-Type: application/json' "$@"; }
code() { curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" "${API_AUTH[@]}" "$@"; }
job()  { api "$BASE/api/jobs/$1" | json "$2"; }
trace_path() { curl -s -b "$JAR" --get --data-urlencode "path=$1" "$BASE/api/trace"; }
manifest() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))" "$1" "$2"; }
event_kinds() {
  python3 -c 'import sqlite3,sys; db=sqlite3.connect("file:"+sys.argv[1]+"?mode=ro",uri=True); print(" ".join(r[0] for r in db.execute("SELECT kind FROM events WHERE job_id=? AND kind NOT IN (\"alert_raised\",\"alert_cleared\") ORDER BY id",(int(sys.argv[2]),))))' "$DANLU_HOME/danlu.db" "$1"
}
submit() { api -X POST "$BASE/api/jobs" -d "$1" | json "d['id']"; }
wait_status() { # id status 超时秒数
  for _ in $(seq 1 $(( ${3:-20} * 2 ))); do
    [ "$(job "$1" "d['status']")" = "$2" ] && return 0; sleep 0.5
  done; return 1
}
# 等进程真正起来（pid 不为 0）：任务一占位就是 running，那时还在做快照、没有进程
wait_started() { # id 超时秒数
  for _ in $(seq 1 $(( ${2:-10} * 2 ))); do
    [ "$(job "$1" "d['pid'] > 0")" = True ] && return 0; sleep 0.5
  done; return 1
}
wait_recipe() { # id 超时秒数
  for _ in $(seq 1 $(( ${2:-20} * 2 ))); do
    [ "$(job "$1" "bool(d.get('recipe') and (d['recipe'].get('env_hash') or d['recipe'].get('env_error')))" )" = True ] && return 0; sleep 0.5
  done; return 1
}
wait_manifest() { # id 超时秒数
  for _ in $(seq 1 $(( ${2:-20} * 2 ))); do
    out=$(job "$1" "d.get('output_dir', '')")
    if [ -n "$out" ] && [ -f "$out/danlu.json" ] \
      && [ "$(manifest "$out/danlu.json" "d['status']")" = succeeded ] \
      && [ "$(manifest "$out/danlu.json" "d['recipe']['env_hash']")" = "$(job "$1" "d['recipe']['env_hash']")" ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}
wait_until() { # 超时秒数 命令…
  local n=$1; shift
  for _ in $(seq 1 $(( n * 2 ))); do "$@" && return 0; sleep 0.5; done; return 1
}
# 服务进程用单独的 HOME，里面的 .bashrc 起了一个后台进程（"Bash 环境"一节用）。
# 参数是只给这个服务进程的 env 参数，比如 -u DANLU_FAKE_GPUS PATH=…
start_server() {
  env "$@" HOME="$T/userhome" "$BIN" serve >>"$T/server.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 40); do
    # 进程退出了（比如端口被占用）就立刻失败，免得请求打到别的服务上
    kill -0 "$SERVER_PID" 2>/dev/null || break
    curl -s -o /dev/null "$BASE/" && return 0
    sleep 0.25
  done
  echo "服务没有启动："; cat "$T/server.log"; exit 1
}
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  [ -n "$NTFY_PID" ] && kill "$NTFY_PID" 2>/dev/null
  [ -f "$T/bashrc-bg.pid" ] && kill "$(cat "$T/bashrc-bg.pid")" 2>/dev/null
}
trap cleanup EXIT

echo "== 初始化 =="
"$BIN" init --yes --tls off --port $PORT --password testpass123 --public-ip 127.0.0.1 --conda "$T/fakeconda" >"$T/init.log" 2>&1 \
  && ok "init 成功" || { bad "init 失败"; cat "$T/init.log"; exit 1; }
check "init 默认打印访问二维码" grep -q '手机扫码打开面板' "$T/init.log"
"$BIN" info --no-qr >"$T/info-no-qr.log" 2>&1
check "info 支持关闭二维码输出" [ "$(grep -c '手机扫码打开面板' "$T/info-no-qr.log" || true)" = 0 ]
ENTRANCE=$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')
[ -n "$ENTRANCE" ] || { bad "读不到安全入口"; exit 1; }
check "快速失败阈值默认为 2 分钟" grep -q 'fail_fast = "2m0s"' "$DANLU_HOME/config.toml"
sed -i 's/kill_grace = .*/kill_grace = "3s"/' "$DANLU_HOME/config.toml"
# 假 conda：activate 时只设置一个环境变量；包清单固定，便于验证哈希去重
mkdir -p "$T/fakeconda/etc/profile.d" "$T/fakeconda/bin"
echo 'conda() { [ "$1" = activate ] && export FAKE_CONDA_ENV="$2"; }' > "$T/fakeconda/etc/profile.d/conda.sh"
cat > "$T/fakeconda/bin/conda" <<'EOF'
#!/bin/sh
if [ "$1" = "list" ]; then
  if [ "$3" = "compare-b" ]; then
    printf 'python=3.12\nnumpy=2.2\nscipy=1.15\n'
  else
    printf 'python=3.12\nnumpy=2.1\n'
  fi
  exit 0
fi
if [ "$1" = "env" ]; then
  printf '{"envs":[]}'
  exit 0
fi
exit 1
EOF
chmod +x "$T/fakeconda/bin/conda"
# 工作目录：一个有未提交改动的 git 仓库
W=$T/work; mkdir -p "$W"
git -C "$W" init -q && echo 'print("v1")' > "$W/train.py" && git -C "$W" add . \
  && git -C "$W" -c user.name=t -c user.email=t@t commit -qm init && echo 'print("v2")' > "$W/train.py"
# 服务进程的 .bashrc：导出一个变量，再起一个占着输出管道的后台进程；
# bashrc-hold 存在时停在这里，让任务停在准备阶段（"单实例"一节用）
mkdir -p "$T/userhome"
cat >"$T/userhome/.bashrc" <<EOF
export E2E_BASHRC=loaded
sleep 30 & echo \$! > "$T/bashrc-bg.pid"
while [ -e "$T/bashrc-hold" ]; do sleep 0.1; done
EOF

start_server

echo "== 安全入口与登录 =="
check "未过入口访问首页返回 404" [ "$(code "$BASE/")" = 404 ]
check "未过入口访问 API 返回 404" [ "$(code "$BASE/api/me")" = 404 ]
check "错误入口返回 404" [ "$(code "$BASE/wrongentrance")" = 404 ]
check "正确入口跳转 302" [ "$(code "$BASE/$ENTRANCE")" = 302 ]
check "过入口后未登录 API 返回 401" [ "$(code "$BASE/api/me")" = 401 ]
check "未登录不能读取手机接入二维码" [ "$(code "$BASE/api/settings/qr.svg")" = 401 ]
check "错误密码返回 401" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"nope"}' "$BASE/api/login")" = 401 ]
check "正确密码登录成功" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login")" = 200 ]
check "登录后访问 API 返回 200" [ "$(code "$BASE/api/me")" = 200 ]
curl -s -D "$T/qr.headers" -b "$JAR" -c "$JAR" -o "$T/qr.svg" "$BASE/api/settings/qr.svg"
check "设置接口返回 SVG 接入二维码" grep -q '<svg' "$T/qr.svg"
check "接入二维码禁止缓存" grep -qi '^Cache-Control: private, no-store' "$T/qr.headers"
check "表单格式的写请求被拒绝" [ "$(code -X POST -d 'x=1' "$BASE/api/jobs")" = 415 ]

echo "== 网页终端 API =="
TERM_ID=$(api -X POST "$BASE/api/terminal" -d '{}' | json "d['id']")
check "创建登录 shell 会话" [ -n "$TERM_ID" ]
check "会话列表包含新终端" [ "$(api "$BASE/api/terminal" | json "any(s['id']=='$TERM_ID' for s in d['sessions'])")" = True ]
check "关闭终端会话" [ "$(api -X POST "$BASE/api/terminal/$TERM_ID/close" -d '{}' | json "d['ok']")" = True ]
check "关闭后会话从列表移除" [ "$(api "$BASE/api/terminal" | json "not any(s['id']=='$TERM_ID' for s in d['sessions'])")" = True ]

echo "== GPU =="
sleep 2
GPUS=$(api "$BASE/api/gpus")
check "看到 4 张假 GPU" [ "$(echo "$GPUS" | json "len(d['gpus'])")" = 4 ]
check "GPU 3 显示为外部进程占用" [ "$(echo "$GPUS" | json "len(d['gpus'][3]['external'])")" = 1 ]
check "磁盘采集返回数据分区和实际可用容量" [ "$(api "$BASE/api/system" | json "any(x['data'] and x['total_bytes']>0 and x['available_bytes']<=x['total_bytes'] for x in d['disks'])")" = True ]

echo "== 调度 =="
A=$(submit "{\"name\":\"A\",\"workdir\":\"$W\",\"cmd\":\"echo hello; python3 train.py; sleep 4\",\"gpus\":2,\"conda_env\":\"myenv\"}")
B=$(submit "{\"name\":\"B\",\"workdir\":\"$W\",\"cmd\":\"echo \$CUDA_VISIBLE_DEVICES; sleep 1\",\"gpus\":1}")
C=$(submit "{\"name\":\"C\",\"workdir\":\"$W\",\"cmd\":\"sleep 1\",\"pinned_gpus\":[3]}")
D=$(submit "{\"name\":\"D\",\"workdir\":\"$W\",\"cmd\":\"echo conda=\$FAKE_CONDA_ENV; echo out=\$DANLU_OUTPUT_DIR\",\"gpus\":2,\"conda_env\":\"myenv\"}")
check "A 开始运行" wait_status "$A" running 10
check "A 分到 GPU 0,1" [ "$(job "$A" "d['gpus_assigned']")" = "[0, 1]" ]
check "B 和 A 同时开始（只要 1 张卡，GPU 2 空闲）" wait_status "$B" succeeded 10
check "B 用的是 GPU 2" [ "$(job "$B" "d['gpus_assigned']")" = "[2]" ]
check "C 指定被外部占用的 GPU 3，保持排队" [ "$(job "$C" "d['status']")" = queued ]
check "D 排在 C 后面，严格先来先跑也在排队" [ "$(job "$D" "d['status']")" = queued ]
check "取消排队中的 C" [ "$(api -X POST "$BASE/api/jobs/$C/cancel" -d '{}' | json "d['status']")" = cancelled ]
check "A 结束后 D 开始并成功" wait_status "$D" succeeded 20
check "A 成功" [ "$(job "$A" "d['status']")" = succeeded ]
check "D 激活了 conda 环境" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$D/log?tail=10000' | grep -q 'conda=myenv'"
check "A 和 D 的环境包记录完成" wait_recipe "$A" 10
check "D 的环境包记录完成" wait_recipe "$D" 10
check "相同包清单使用相同哈希" [ "$(job "$A" "d['recipe']['env_hash']")" = "$(job "$D" "d['recipe']['env_hash']")" ]
OUTDIR=$(job "$D" "d['output_dir']")
check "任务收到独立输出目录" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$D/log?tail=10000' | grep -Fq 'out=$OUTDIR'"
check "机器配方记录主机、驱动、CUDA 和 GPU 型号" [ "$(job "$D" "d['recipe']['machine']['driver_version'] + ' ' + d['recipe']['machine']['cuda_version'] + ' ' + d['recipe']['machine']['gpus'][0]['name']")" = "fake fake Fake GPU 24GB" ]
check "danlu.json 记录结束状态和环境包哈希" wait_manifest "$D" 10
check "环境包接口返回原文" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$D/env' | grep -q '^numpy=2.1$'"
TRACE_OUTPUT=$(trace_path "$OUTDIR/checkpoints/latest.pt")
check "输出目录路径反查到任务 D" [ "$(echo "$TRACE_OUTPUT" | json "[(m['job']['id'], m['source']) for m in d['matches']]")" = "[($D, 'output_dir')]" ]
mkdir -p "$T/manifest-copy/nested"
cp "$OUTDIR/danlu.json" "$T/manifest-copy/danlu.json"
TRACE_MANIFEST=$(trace_path "$T/manifest-copy/nested/latest.pt")
check "向上查找 danlu.json 反查到任务 D" [ "$(echo "$TRACE_MANIFEST" | json "[(m['job']['id'], m['source']) for m in d['matches']]")" = "[($D, 'danlu_json')]" ]
TRACE_GUESS=$(trace_path "$W/train.py")
check "命令路径片段列出可能任务 A" [ "$(echo "$TRACE_GUESS" | json "[(m['job']['id'], m['source']) for m in d['matches']]")" = "[($A, 'guess')]" ]
TRACE_CLI=$(HOME="$T/userhome" "$BIN" trace "$OUTDIR/checkpoints/latest.pt")
check "danlu trace 输出任务和面板链接" bash -c "printf '%s' '$TRACE_CLI' | grep -Fq '$BASE/jobs/$D'"
check "B 的日志里 CUDA_VISIBLE_DEVICES=2" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$B/log?tail=10000' | grep -q '\"data\":\"[^\"]*\\\\n2\\\\n'"

echo "== 改名 =="
check "改名返回去掉首尾空白的新名字" [ "$(api -X POST "$BASE/api/jobs/$D/rename" -d '{"name":"  D-改名  "}' | json "d['name']")" = "D-改名" ]
check "任务详情里是新名字" [ "$(job "$D" "d['name']")" = "D-改名" ]
check "已结束任务改名后输出目录不变" [ "$(job "$D" "d['output_dir']")" = "$OUTDIR" ]
check "按新名字能搜到任务" [ "$(api --get --data-urlencode "q=D-改名" "$BASE/api/jobs" | json "[j['id'] for j in d['jobs']]")" = "[$D]" ]
check "空名字被拒绝" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"name":"  "}' "$BASE/api/jobs/$D/rename")" = 400 ]
check "不存在的任务改名返回 404" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"name":"x"}' "$BASE/api/jobs/999999/rename")" = 404 ]
check "改名记入审计日志" [ "$(api "$BASE/api/audit" | json "any(e['action']=='job_rename' and e['detail']=='#$D D → D-改名' for e in d['entries'])")" = True ]

echo "== 校验 =="
check "工作目录不存在时拒绝" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"workdir":"/nope","cmd":"x","gpus":1}' "$BASE/api/jobs")" = 400 ]
check "卡数超过总数时拒绝" [ "$(code -X POST -H 'Content-Type: application/json' -d "{\"workdir\":\"$W\",\"cmd\":\"x\",\"gpus\":9}" "$BASE/api/jobs")" = 400 ]
check "不允许覆盖 CUDA_VISIBLE_DEVICES" [ "$(code -X POST -H 'Content-Type: application/json' -d "{\"workdir\":\"$W\",\"cmd\":\"x\",\"gpus\":1,\"env\":{\"CUDA_VISIBLE_DEVICES\":\"0\"}}" "$BASE/api/jobs")" = 400 ]

echo "== Bash 环境（.bashrc 里起了后台进程）=="
X=$(submit "{\"name\":\"X\",\"workdir\":\"$W\",\"cmd\":\"echo bashrc=\$E2E_BASHRC; sleep 1\",\"gpus\":0,\"inherit_bashrc\":true}")
sleep 0.5
t0=$(date +%s%N)
Y=$(submit "{\"name\":\"Y\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}")
t1=$(date +%s%N)
check "X 加载 .bashrc 期间，提交别的任务不用等（1 秒内返回）" [ $(( (t1 - t0) / 1000000 )) -lt 1000 ]
check "X 成功" wait_status "$X" succeeded 15
check "X 的环境里有 .bashrc 导出的变量" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$X/log?tail=10000' | grep -q 'bashrc=loaded'"
check ".bashrc 起的后台进程已被清理" bash -c "! kill -0 \$(cat '$T/bashrc-bg.pid') 2>/dev/null"
check "Y 成功" wait_status "$Y" succeeded 10

echo "== 单实例：同一数据目录的第二个 danlu 不碰任务 =="
touch "$T/bashrc-hold"
Z=$(submit "{\"name\":\"Z\",\"workdir\":\"$W\",\"cmd\":\"echo z\",\"gpus\":0,\"inherit_bashrc\":true}")
z_preparing() { [ "$(job "$Z" "(d['status'], d['pid'])")" = "('running', 0)" ]; }
check "Z 停在加载 .bashrc：已占位，还没有进程" wait_until 5 z_preparing
env HOME="$T/userhome" timeout 10 "$BIN" serve >"$T/second.log" 2>&1
SECOND=$?
check "第二个实例立即退出" [ "$SECOND" = 1 ]
check "第二个实例说明数据目录被占用" grep -q '另一个 danlu（pid [0-9]*）正在使用数据目录' "$T/second.log"
check "第二个实例没有把准备中的任务放回队列" z_preparing
rm -f "$T/bashrc-hold"
check "Z 照常启动并成功" wait_status "$Z" succeeded 15
check "原服务仍在运行" kill -0 "$SERVER_PID"
check "Z 只启动了一次" [ "$(grep -c "任务 #$Z Z 已启动" "$T/server.log")" = 1 ]

echo "== 一个输出目录卡住不影响别的任务 =="
M1=$(submit "{\"name\":\"M1\",\"workdir\":\"$W\",\"cmd\":\"sleep 4\",\"gpus\":0}")
check "M1 开始运行" wait_started "$M1" 10
check "M1 的环境包记录完成" wait_recipe "$M1" 10
M1OUT=$(job "$M1" "d['output_dir']")
# 把 danlu.json 换成 FIFO：收尾时读旧清单会一直卡住，模拟没有响应的网络存储
rm -f "$M1OUT/danlu.json" && mkfifo "$M1OUT/danlu.json"
check "M1 结束" wait_status "$M1" succeeded 10
M2=$(submit "{\"name\":\"M2\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}")
check "M1 的清单卡住时 M2 照常启动并成功" wait_status "$M2" succeeded 10
timeout 5 sh -c ': > "$1"' sh "$M1OUT/danlu.json" # 打开写端再关掉，卡住的读取读到 EOF
m1_manifest_done() { [ -f "$M1OUT/danlu.json" ] && [ "$(manifest "$M1OUT/danlu.json" "d['status']")" = succeeded ]; }
check "解除阻塞后 M1 的清单换成普通文件并记下结束状态" wait_until 10 m1_manifest_done

echo "== 退出码与残留进程 =="
E=$(submit "{\"name\":\"E\",\"workdir\":\"$W\",\"cmd\":\"echo bye; exit 3\",\"gpus\":1}")
check "退出码 3 记为失败" wait_status "$E" failed 10
check "记录了退出码 3" [ "$(job "$E" "d['exit_code']")" = 3 ]
F=$(submit "{\"name\":\"F\",\"workdir\":\"$W\",\"cmd\":\"setsid sleep 1000 & sleep 1\",\"gpus\":0}")
check "F 成功结束" wait_status "$F" succeeded 10
check "F 用 setsid 留下的残留进程被清理，记在备注里" [ "$(job "$F" "'已清理' in d['notes']")" = True ]
check "成功任务的失败原因保持为空" [ "$(job "$F" "d['fail_reason']")" = "" ]
sleep 0.5
check "没有遗留 F 的 sleep 进程" bash -c "! grep -l 'DANLU_JOB_ID=$F' /proc/[0-9]*/environ 2>/dev/null | grep -q ."

echo "== 取消（忽略 SIGTERM 的进程要被强杀）=="
G=$(submit "{\"name\":\"G\",\"workdir\":\"$W\",\"cmd\":\"trap '' TERM; python3 -c 'import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(1000)'\",\"gpus\":2}")
check "G 开始运行" wait_started "$G" 10
api -X POST "$BASE/api/jobs/$G/cancel" -d '{}' >/dev/null
check "G 在宽限期后被强杀，状态为已取消" wait_status "$G" cancelled 15
sleep 0.5
check "G 的所有进程都已结束" bash -c "! grep -l 'DANLU_JOB_ID=$G' /proc/[0-9]*/environ 2>/dev/null | grep -q ."

echo "== danlu 重启后接管 =="
H=$(submit "{\"name\":\"H\",\"workdir\":\"$W\",\"cmd\":\"for i in 1 2 3 4 5 6; do echo step \$i; sleep 1; done\",\"gpus\":1}")
check "H 开始运行" wait_started "$H" 10
# 模拟任务已结束、包采集尚未落库时服务被杀的磁盘状态。
python3 -c 'import json,sqlite3,sys; db=sqlite3.connect(sys.argv[1]); d=json.loads(db.execute("SELECT recipe_json FROM jobs WHERE id=?",(int(sys.argv[2]),)).fetchone()[0]); d["pending"]=True; d.pop("env_hash",None); db.execute("UPDATE jobs SET recipe_json=? WHERE id=?",(json.dumps(d),int(sys.argv[2]))); db.commit()' "$DANLU_HOME/danlu.db" "$E"
kill -9 "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
sleep 1
check "danlu 被杀后训练进程还活着" bash -c "grep -l 'DANLU_JOB_ID=$H' /proc/[0-9]*/environ 2>/dev/null | grep -q ."
start_server
check "重启后 H 仍是运行中" [ "$(job "$H" "d['status']")" = running ]
check "H 最终成功" wait_status "$H" succeeded 15
check "服务日志里有接管记录" grep -q "接管运行中的任务 #$H" "$T/server.log"
check "重启后结束任务不再等待已中断的包采集" [ "$(job "$E" "not d['recipe'].get('pending') and '重启' in d['recipe'].get('env_error','')")" = True ]

echo "== 手动暂停和恢复队列 =="
check "队列默认未暂停" [ "$(api "$BASE/api/queue" | json "d['paused']")" = False ]
QR=$(submit "{\"name\":\"queue-running\",\"workdir\":\"$W\",\"cmd\":\"sleep 5; echo done\",\"gpus\":1}")
check "暂停前的任务已启动" wait_started "$QR" 10
check "手动暂停队列" [ "$(api -X POST "$BASE/api/queue/pause" -d '{}' | json "d['paused'] and d['kind']=='manual' and d['since']>0")" = True ]
QP=$(submit "{\"name\":\"queue-pending\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":1}")
check "暂停时仍可提交任务" [ "$(job "$QP" "d['status']")" = queued ]
check "暂停不影响运行任务正常收尾" wait_status "$QR" succeeded 15
check "卡已空闲时排队任务也不启动" [ "$(job "$QP" "d['status']")" = queued ]
kill -9 "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
start_server
check "重启保留队列暂停状态" [ "$(api "$BASE/api/queue" | json "d['paused'] and d['kind']=='manual'")" = True ]
check "重启后排队任务仍未启动" [ "$(job "$QP" "d['status']")" = queued ]
api -X POST "$BASE/api/queue/pause" -d '{}' >/dev/null
check "手动恢复队列" [ "$(api -X POST "$BASE/api/queue/resume" -d '{}' | json "d['paused']")" = False ]
check "恢复后排队任务开始运行并成功" wait_status "$QP" succeeded 10
api -X POST "$BASE/api/queue/resume" -d '{}' >/dev/null
check "重复暂停和恢复不重复记队列事件" \
  [ "$(python3 -c 'import sqlite3,sys; db=sqlite3.connect("file:"+sys.argv[1]+"?mode=ro",uri=True); print(" ".join(r[0] for r in db.execute("SELECT kind FROM events WHERE kind IN (?,?) ORDER BY id",("queue_paused","queue_resumed"))))' "$DANLU_HOME/danlu.db")" = "queue_paused queue_resumed" ]
check "暂停和恢复均记审计日志" \
  [ "$(api "$BASE/api/audit" | json "sorted(e['action'] for e in d['entries'] if e['action'].startswith('queue_'))")" = "['queue_pause', 'queue_resume']" ]

echo "== 快速失败自动暂停 =="
FF=$(submit "{\"name\":\"fail-fast\",\"workdir\":\"$W\",\"cmd\":\"while [ ! -f $T/finish-fast ]; do sleep 0.1; done; exit 7\",\"gpus\":3}")
check "快速失败测试任务已启动" wait_started "$FF" 10
FN=$(submit "{\"name\":\"after-fast-failure\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":1}")
check "后续任务等待前一个释放 GPU" [ "$(job "$FN" "d['status']")" = queued ]
touch "$T/finish-fast"
check "快速失败任务记录为失败" wait_status "$FF" failed 10
check "自动暂停带触发任务和原因" [ "$(api "$BASE/api/queue" | json "d['paused'] and d['kind']=='fail_fast' and d['job_id']==$FF and bool(d['reason'])")" = True ]
check "失败后后续任务不启动" [ "$(job "$FN" "d['status']")" = queued ]
check "任务结束与自动暂停事件各记一次" [ "$(event_kinds "$FF")" = "job_started job_finished queue_paused" ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
start_server
check "自动暂停重启后保留" [ "$(api "$BASE/api/queue" | json "d['paused'] and d['kind']=='fail_fast' and d['job_id']==$FF")" = True ]
check "自动暂停重启后仍不启动后续任务" [ "$(job "$FN" "d['status']")" = queued ]
check "自动暂停可通过面板接口手动恢复" [ "$(api -X POST "$BASE/api/queue/resume" -d '{}' | json "d['paused']")" = False ]
check "手动继续后后续任务成功" wait_status "$FN" succeeded 10

echo "== 关闭快速失败自动暂停 =="
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
sed -i 's/fail_fast = .*/fail_fast = "0s"/' "$DANLU_HOME/config.toml"
start_server
F0=$(submit "{\"name\":\"fail-fast-disabled\",\"workdir\":\"$W\",\"cmd\":\"while [ ! -f $T/finish-disabled ]; do sleep 0.1; done; exit 8\",\"gpus\":3}")
check "关闭后测试任务已启动" wait_started "$F0" 10
N0=$(submit "{\"name\":\"after-failure-disabled\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":1}")
check "关闭后也先确认后续任务在排队" [ "$(job "$N0" "d['status']")" = queued ]
touch "$T/finish-disabled"
check "关闭后失败状态仍正常记录" wait_status "$F0" failed 10
check "关闭后队列照常执行后续任务" wait_status "$N0" succeeded 10
check "关闭后不会暂停队列" [ "$(api "$BASE/api/queue" | json "d['paused']")" = False ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
sed -i 's/fail_fast = .*/fail_fast = "2m"/' "$DANLU_HOME/config.toml"
start_server

echo "== 任务状态事件 =="
check "任务可标星" [ "$(api -X POST "$BASE/api/jobs/$A/star" -d '{"starred":true}' | json "d['starred']")" = True ]
check "只看标星返回对应任务" [ "$(api "$BASE/api/jobs?starred=true" | json "[j['id'] for j in d['jobs']]")" = "[$A]" ]
check "标星请求必须明确 true 或 false" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/jobs/$A/star")" = 400 ]
check "成功任务的启动、结束事件各一条" [ "$(event_kinds "$A")" = "job_started job_finished" ]
check "排队时取消只记结束事件" [ "$(event_kinds "$C")" = "job_finished" ]
check "失败任务的启动、结束事件各一条" [ "$(event_kinds "$E")" = "job_started job_finished" ]
check "运行中取消记为一次启动、一次结束" [ "$(event_kinds "$G")" = "job_started job_finished" ]
check "重启接管不会重复记启动事件" [ "$(event_kinds "$H")" = "job_started job_finished" ]

echo "== 日志 =="
check "tail 读取到 H 的输出" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$H/log?tail=100000' | grep -q 'step 6'"
check "SSE 推送日志并在结束后发送 end" bash -c "curl -s -N --max-time 5 -b '$JAR' '$BASE/api/jobs/$H/log/stream?offset=0' | grep -q 'event: end'"
printf '\xe4\xb8' >>"$DANLU_HOME/logs/$H.log" # 结束的任务末尾留下半个字符
H_NEXT=$(api "$BASE/api/jobs/$H/log?tail=100" | json "d['next']")
check "日志末尾有残缺字符时，从 tail 的偏移打开 SSE 也能读到 end" bash -c "curl -s -N --max-time 5 -b '$JAR' '$BASE/api/jobs/$H/log/stream?offset=$H_NEXT' | grep -q 'event: end'"
L=$(submit "{\"name\":\"L\",\"workdir\":\"$W\",\"cmd\":\"printf 'epoch 1:  10%%\\\\repoch 1:  70%%\\\\n'; sleep 3\",\"gpus\":0}")
wait_status "$L" running 10; sleep 1
check "运行中的任务列表带最后一行日志（\\r 进度已折叠）" \
  [ "$(api "$BASE/api/jobs?status=running" | json "[j['last_line'] for j in d['jobs'] if j['id']==$L][0]")" = "epoch 1:  70%" ]
check "L 成功" wait_status "$L" succeeded 10

echo "== 代码快照 =="
SNAP=$(api "$BASE/api/jobs/$A/snapshot")
check "快照记录了 git commit" [ "$(echo "$SNAP" | json "len(d['meta']['git']['commit'])")" = 40 ]
check "快照标记了未提交改动" [ "$(echo "$SNAP" | json "d['meta']['git']['dirty']")" = True ]
check "快照保存的是改动后的代码" [ "$(api "$BASE/api/jobs/$A/snapshot/file?path=train.py" | json "d['content'].strip()")" = 'print("v2")' ]
check "diff 包含改动" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$A/snapshot/diff' | grep -q 'v2'"
check "可以下载快照 tar.gz" [ "$(code "$BASE/api/jobs/$A/snapshot/download")" = 200 ]

echo "== 对比任务与快照重跑 =="
S4=$T/s4-project
mkdir -p "$S4/data" "$S4/config"
git -C "$S4" init -q
printf 'data/\nconfig/*.weights\n' > "$S4/.gitignore"
printf 'print("snapshot-v1")\n' > "$S4/train.py"
printf 'print("removed")\n' > "$S4/remove.py"
printf 'small\n' > "$S4/config/options.txt"
truncate -s 2M "$S4/config/large.bin"
printf 'large-file\n' | dd of="$S4/config/large.bin" conv=notrunc status=none
git -C "$S4" add .
git -C "$S4" -c user.name=t -c user.email=t@t commit -qm compare
echo data-v1 > "$S4/data/sample.bin"
echo weights-v1 > "$S4/config/model.weights" # 已跟踪的 config/ 里被 Git 忽略的文件
S4A=$(submit "{\"name\":\"compare-A\",\"workdir\":\"$S4\",\"cmd\":\"python3 train.py --lr 3e-4\",\"env\":{\"RUN\":\"A\",\"ONLY_A\":\"\"},\"gpus\":1,\"conda_env\":\"myenv\"}")
check "基准任务成功" wait_status "$S4A" succeeded 20
check "基准任务记录环境包" wait_recipe "$S4A" 10
printf 'print("snapshot-v2")\n' > "$S4/train.py"
rm "$S4/remove.py"
printf 'print("added")\n' > "$S4/add.py"
S4B=$(submit "{\"name\":\"compare-B\",\"workdir\":\"$S4\",\"cmd\":\"python3 train.py --lr 1e-3\",\"env\":{\"RUN\":\"B\",\"ONLY_B\":\"\"},\"gpus\":2,\"conda_env\":\"compare-b\"}")
check "对比任务成功" wait_status "$S4B" succeeded 20
check "对比任务记录环境包" wait_recipe "$S4B" 10
S4DIFF=$(api "$BASE/api/compare?a=$S4A&b=$S4B")
check "对比命令按词保留变化的参数" [ "$(printf '%s' "$S4DIFF" | json "'3e-4' in d['command_a'] and '1e-3' in d['command_b']")" = True ]
check "对比环境变量区分空值与缺失" [ "$(printf '%s' "$S4DIFF" | json "[(v['name'], v['a'], v['b']) for v in d['env']]")" = "[('ONLY_A', '', None), ('ONLY_B', None, ''), ('RUN', 'A', 'B')]" ]
check "对比识别同大小改动、增删和不完整快照" [ "$(printf '%s' "$S4DIFF" | json "d['code']['partial'] and {f['path']: f['kind'] for f in d['code']['files']} == {'add.py':'added', 'remove.py':'removed', 'train.py':'changed'}")" = True ]
check "对比返回包版本与显卡配置" [ "$(printf '%s' "$S4DIFF" | json "d['packages'] == [{'name':'numpy','a':'2.1','b':'2.2'},{'name':'scipy','a':None,'b':'1.15'}] and d['b']['gpus_req']==2")" = True ]
check "按工作目录找同项目任务" [ "$(api --get --data-urlencode "workdir=$S4/" "$BASE/api/jobs" | json "[j['id'] for j in d['jobs']]")" = "[$S4B, $S4A]" ]
check "拒绝把同一个任务和自己对比" [ "$(code "$BASE/api/compare?a=$S4A&b=$S4A")" = 400 ]
printf 'print("current-v3")\n' > "$S4/train.py"
echo data-v2 > "$S4/data/sample.bin"
api -X POST "$BASE/api/queue/pause" -d '{}' >/dev/null
S4C=$(submit "{\"name\":\"restore-A\",\"workdir\":\"$S4\",\"cmd\":\"python3 train.py; cat data/sample.bin; head -n 1 config/large.bin; test ! -e .git\",\"gpus\":1,\"conda_env\":\"myenv\",\"rerun_of\":$S4A,\"code_from\":$S4A}")
check "重跑排队时源快照不进入清理清单" [ "$(HOME="$T/userhome" "$BIN" prune --dry-run --older-than 0s --json | json "not any(j['job_id']==$S4A for j in d['jobs'])")" = True ]
api -X POST "$BASE/api/queue/resume" -d '{}' >/dev/null
check "快照重跑成功" wait_status "$S4C" succeeded 20
check "快照重跑运行旧代码并读取当前数据及被跳过的文件" [ "$(api "$BASE/api/jobs/$S4C/log?tail=10000" | json "'snapshot-v1\ndata-v2\nlarge-file\n' in d['data'] and 'current-v3' not in d['data']")" = True ]
check "快照重跑记录来源和当前版本文件" [ "$(job "$S4C" "d['code_from']==$S4A and 'config/large.bin' in d['snapshot_note'] and d['snapshot_ok']")" = True ]
check "原快照与重跑任务的代码包复用同一个文件" test "$DANLU_HOME/snapshots/$S4A.tar.gz" -ef "$DANLU_HOME/snapshots/$S4C.tar.gz"
check "快照重跑结束后标星可保留其文件" [ "$(api -X POST "$BASE/api/jobs/$S4C/star" -d '{"starred":true}' | json "d['starred']")" = True ]
S4D=$(submit "{\"name\":\"restore-artifacts\",\"workdir\":\"$S4\",\"cmd\":\"cat config/model.weights; [ -e add.py ] && echo has-add || echo no-add; mkdir -p checkpoints && printf model > checkpoints/best.pt\",\"gpus\":0,\"rerun_of\":$S4A,\"code_from\":$S4A}")
check "快照重跑用相对路径写出产物" wait_status "$S4D" succeeded 20
check "快照重跑读到混合目录里被 Git 忽略的文件" [ "$(api "$BASE/api/jobs/$S4D/log?tail=10000" | json "'weights-v1' in d['data']")" = True ]
check "快照之后新建的代码文件没有混进快照重跑" [ "$(api "$BASE/api/jobs/$S4D/log?tail=10000" | json "'no-add' in d['data']")" = True ]
check "快照备注写明哪些内容来自工作目录" [ "$(job "$S4D" "'config/model.weights' in d['snapshot_note'] and 'data/' in d['snapshot_note'] and 'add.py' not in d['snapshot_note']")" = True ]

echo "== 设备令牌（桌面客户端）=="
INFO=$("$BIN" info --json)
check "info --json 给出地址和接口版本" [ "$(echo "$INFO" | json "d['base_url'] + ' ' + str(d['api_version'])")" = "$BASE 1" ]
TOK_JSON=$("$BIN" token create --name "e2e's pc" --json)
TOK=$(echo "$TOK_JSON" | json "d['token']")
TOK_ID=$(echo "$TOK_JSON" | json "d['id']")
bearer() { curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" "${@:2}"; }
check "令牌不经过安全入口也能访问接口" [ "$(bearer "$TOK" "$BASE/api/me")" = 200 ]
check "令牌可以提交任务" [ "$(bearer "$TOK" -H 'Content-Type: application/json' -d "{\"name\":\"T\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}" "$BASE/api/jobs")" = 201 ]
check "错误的令牌和没过入口一样是空 404" [ "$(curl -s -w '%{http_code}' -H 'Authorization: Bearer dl_wrong' "$BASE/api/me")" = 404 ]
check "面板能列出令牌和最后使用时间" [ "$(api "$BASE/api/tokens" | json "d['tokens'][0]['last_used_at'] is not None")" = True ]
check "面板吊销令牌" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/tokens/$TOK_ID/revoke")" = 200 ]
check "吊销后立即失效" [ "$(bearer "$TOK" "$BASE/api/me")" = 404 ]
FIRST=$(api "$BASE/api/jobs?order=ended&size=2")
LAST_END=$(echo "$FIRST" | json "d['jobs'][-1]['ended_at']")
LAST_ID=$(echo "$FIRST" | json "d['jobs'][-1]['id']")
NEXT_IDS=$(api "$BASE/api/jobs?order=ended&size=200&before=$LAST_END&before_id=$LAST_ID" | json "[j['id'] for j in d['jobs']]")
check "按结束时间游标翻页不重复" python3 -c "
import json, sys
first = [j['id'] for j in json.loads(sys.argv[1])['jobs']]
rest = $NEXT_IDS
assert first and rest and not set(first) & set(rest), (first, rest)
" "$FIRST"

echo "== 配置和密码 =="
"$BIN" port 30001 >/dev/null # 命令行改了配置，还没重启
CFG_SUM=$(sha256sum "$DANLU_HOME/config.toml")
check "面板改密码成功" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"old":"testpass123","new":"newpass1234"}' "$BASE/api/settings/password")" = 200 ]
check "面板改密码不改写 config.toml（命令行刚改的端口还在）" [ "$(sha256sum "$DANLU_HOME/config.toml")" = "$CFG_SUM" ]
check "config.toml 里没有密码哈希" bash -c "! grep -q password_hash '$DANLU_HOME/config.toml'"
login() { # 密码：用新的 cookie 文件过入口、登录，输出状态码
  rm -f "$T/jar-login"
  curl -s -c "$T/jar-login" -o /dev/null "$BASE/$ENTRANCE"
  curl -s -b "$T/jar-login" -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$1\"}" "$BASE/api/login"
}
check "新密码立即可以登录" [ "$(login newpass1234)" = 200 ]
# 和客户端发出的脚本一样：密码放在带引号的 heredoc 里，经标准输入传给 passwd
sh -s >/dev/null <<EOF
exec "$BIN" passwd --password-stdin <<'DANLU_INPUT_1'
new\$pass word\`x
DANLU_INPUT_1
EOF
check "passwd --password-stdin 改的密码不用重启就生效" [ "$(login 'new$pass word`x')" = 200 ]
check "改完以后旧密码失效" [ "$(login newpass1234)" = 401 ]

echo "== 登录限速 =="
curl -s -c "$T/jar2" -o /dev/null "$BASE/$ENTRANCE"
for _ in 1 2 3 4 5; do curl -s -b "$T/jar2" -o /dev/null -H 'Content-Type: application/json' -d '{"username":"admin","password":"x"}' "$BASE/api/login"; done
check "连续输错 5 次后被锁定（429）" [ "$(curl -s -o /dev/null -w '%{http_code}' -b "$T/jar2" -H 'Content-Type: application/json' -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login")" = 429 ]

echo "== 客户端运维用的子命令 =="
cp "$BIN" "$T/danlu-copy" && cp "$BIN" "$T/danlu-copy.new"
check "replace 用新程序原子替换旧程序" env HOME="$T/path-home" bash -c "'$T/danlu-copy.new' replace --no-restart --json '$T/danlu-copy' | grep -q '\"restarted\": false' && [ ! -e '$T/danlu-copy.new' ]"
check "replace 把旧程序留成 .prev" [ -x "$T/danlu-copy.prev" ]
check "replace 自动注册 PATH 命令" test "$T/path-home/.local/bin/danlu" -ef "$T/danlu-copy"
# 假 systemctl：只验证默认重启路径的 JSON 和软链接目标，不重启开发机上的服务。
mkdir -p "$T/update-bin"
printf '#!/bin/sh\nexit 0\n' > "$T/update-bin/systemctl"
chmod +x "$T/update-bin/systemctl"
cp "$BIN" "$T/danlu-copy.new"
check "replace 通过 PATH 软链接升级且 --json 输出有效" env HOME="$T/path-home" PATH="$T/update-bin:$PATH" bash -c \
  "'$T/danlu-copy.new' replace --json '$T/path-home/.local/bin/danlu' | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d[\"restarted\"] and d[\"path\"] == sys.argv[1]' '$T/danlu-copy' && test -L '$T/path-home/.local/bin/danlu' && test '$T/path-home/.local/bin/danlu' -ef '$T/danlu-copy'"

echo "== 上传后直接 update =="
# 模拟服务管理器返回真实服务器进程的位置，并记录重启请求；不操作开发机的用户服务。
cat >"$T/update-bin/systemctl" <<EOF
#!/bin/sh
case "\$*" in
  '--user show danlu --property=MainPID --value') echo '$SERVER_PID' ;;
  '--user restart danlu') echo restarted > '$T/update-restarted' ;;
  *) exit 1 ;;
esac
EOF
mkdir -p "$T/upload"
cp "$BIN" "$T/upload/danlu.new"
check "update 拒绝用旧程序替换自己" env HOME="$T/update-home" PATH="$T/update-bin:$PATH" bash -c \
  "! '$BIN' update >'$T/update-self.log' 2>&1 && test ! -e '$BIN.prev'"
check "新程序在其他目录也能直接 update" env HOME="$T/update-home" PATH="$T/update-bin:$PATH" "$T/upload/danlu.new" update
check "update 保留旧程序备份" [ -x "$BIN.prev" ]
check "update 请求重启 danlu 用户服务" [ -f "$T/update-restarted" ]
check "update 把 PATH 命令指向实际安装位置" test "$T/update-home/.local/bin/danlu" -ef "$BIN"

echo "== 保留策略与清理预览 =="
# 前面故意改过密码、吊销了会话并锁定登录；后续检查使用新建的隔离测试设备令牌。
PRUNE_TOKEN=$("$BIN" token create --name prune-check --json | json "d['token']")
API_AUTH=(-H "Authorization: Bearer $PRUNE_TOKEN")
PR=$(submit "{\"name\":\"prune-running\",\"workdir\":\"$W\",\"cmd\":\"sleep 300\",\"gpus\":0}")
check "清理前的运行任务已启动" wait_started "$PR" 10
printf 'trained checkpoint\n' >"$OUTDIR/checkpoint.pt"
MANIFEST_SUM=$(sha256sum "$OUTDIR/danlu.json")
PREVIEW=$(env HOME="$T/userhome" "$BIN" prune --dry-run --older-than 0s --json)
check "清理预览包含普通完成任务，排除标星与运行任务" [ "$(echo "$PREVIEW" | json "any(j['job_id']==$D for j in d['jobs']) and not any(j['job_id'] in [$A,$PR] for j in d['jobs'])")" = True ]
check "预览不删除日志或改变清理标记" [ "$(job "$D" "d['pruned_at'] is None")" = True ]
check "预览后日志仍在" [ -f "$DANLU_HOME/logs/$D.log" ]
prune_now() { env HOME="$T/userhome" "$BIN" prune --older-than 0s --json >"$T/prune.json"; }
check "实际清理命令成功" prune_now
check "清理后任务记录保留并写入清理时间" [ "$(job "$D" "d['pruned_at'] is not None and d['status']=='succeeded'")" = True ]
check "完成任务的日志、快照和运行目录已清理" bash -c "test ! -e '$DANLU_HOME/logs/$D.log' && test ! -e '$DANLU_HOME/snapshots/$D.tar.gz' && test ! -e '$DANLU_HOME/run/$D'"
check "快照重跑写在相对路径下的产物在清理后保留" grep -q model "$DANLU_HOME/run/$S4D/code/checkpoints/best.pt"
check "快照重跑恢复的代码、链接和包装脚本已清理" bash -c "test ! -e '$DANLU_HOME/run/$S4D/code/train.py' && test ! -L '$DANLU_HOME/run/$S4D/code/data' && test ! -e '$DANLU_HOME/run/$S4D/run.sh' && test ! -e '$DANLU_HOME/snapshots/$S4D.meta.json'"
check "清理链接不影响原工作目录" bash -c "test -f '$S4/data/sample.bin' && test -f '$S4/config/model.weights'"
check "快照重跑记为已清理" [ "$(job "$S4D" "d['pruned_at'] is not None")" = True ]
check "标星任务的日志和快照保留" bash -c "test -f '$DANLU_HOME/logs/$A.log' && test -f '$DANLU_HOME/snapshots/$A.tar.gz'"
check "运行任务继续运行且保留日志" [ "$(job "$PR" "d['status']")" = running ]
check "清理不改训练输出或任务清单" [ "$(sha256sum "$OUTDIR/danlu.json")" = "$MANIFEST_SUM" ]
check "checkpoint 仍保留" grep -q 'trained checkpoint' "$OUTDIR/checkpoint.pt"
check "清理后的日志接口明确返回 410" [ "$(code "$BASE/api/jobs/$D/log?tail=100")" = 410 ]
check "清理后的快照接口明确返回 410" [ "$(code "$BASE/api/jobs/$D/snapshot")" = 410 ]
check "源快照清理后，重跑任务仍保有旧代码" [ "$(api "$BASE/api/jobs/$S4C/snapshot/file?path=train.py" | json "d['content'].strip()")" = 'print("snapshot-v1")' ]
check "对比已清理的任务仍能查看参数，代码区注明原因" [ "$(api "$BASE/api/compare?a=$S4A&b=$S4C" | json "'快照已清理' in d['code']['error'] and '3e-4' in d['command_a']")" = True ]
check "已清理的快照不能用于提交重跑" [ "$(code -X POST -H 'Content-Type: application/json' -d "{\"workdir\":\"$S4\",\"cmd\":\"true\",\"gpus\":0,\"code_from\":$S4A}" "$BASE/api/jobs")" = 400 ]
api -X POST "$BASE/api/jobs/$PR/cancel" -d '{}' >/dev/null
check "清理后仍能取消运行任务" wait_status "$PR" cancelled 10

echo "== 停止服务 =="
kill -TERM "$SERVER_PID"
check "收到 SIGTERM 后正常停止" wait_until 10 grep -q 'danlu 已停止' "$T/server.log"
wait "$SERVER_PID" 2>/dev/null; SERVER_PID=
API_AUTH=()

echo "== 磁盘保护暂停和恢复 =="
MAIN_HOME=$DANLU_HOME
export DANLU_HOME=$T/home-disk
DISK_PORT=$((PORT + 3))
"$BIN" init --yes --tls off --port "$DISK_PORT" --password testpass123 --public-ip 127.0.0.1 >/dev/null
sed -i 's/disk_min_gb = .*/disk_min_gb = 1000000000/; s/disk_warn_gb = .*/disk_warn_gb = 1000000000/' "$DANLU_HOME/config.toml"
BASE=http://127.0.0.1:$DISK_PORT JAR=$T/cookies-disk
start_server
curl -s -c "$JAR" -o /dev/null "$BASE/$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')"
api -o /dev/null -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login"
disk_paused() { [ "$(api "$BASE/api/queue" | json "d['paused'] and d['kind']=='disk'")" = True ]; }
check "容量低于暂停线时自动暂停" wait_until 10 disk_paused
check "磁盘低余量标为告警" [ "$(api "$BASE/api/system" | json "any(x['data'] and x['warn'] for x in d['disks'])")" = True ]
DP=$(submit "{\"name\":\"disk-pending\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}")
check "磁盘保护也阻止不用 GPU 的新任务" [ "$(job "$DP" "d['status']")" = queued ]
check "磁盘仍不足时不能人工绕过保护" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/queue/resume")" = 409 ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
sed -i 's/disk_min_gb = .*/disk_min_gb = 1/; s/disk_warn_gb = .*/disk_warn_gb = 20/' "$DANLU_HOME/config.toml"
start_server
check "空间达到恢复线后自动恢复调度" wait_status "$DP" succeeded 10
check "磁盘恢复后清除暂停状态" [ "$(api "$BASE/api/queue" | json "d['paused']")" = False ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=
export DANLU_HOME=$MAIN_HOME

echo "== GPU 数据不可信时暂停分卡（假 nvidia-smi）=="
FB=$T/fakebin; mkdir -p "$FB"
echo ok >"$T/smi.mode"
cat >"$FB/nvidia-smi" <<EOF
#!/bin/bash
# ok：正常；noprocs：查不到卡上的进程；hang：卡住
mode=\$(cat "$T/smi.mode")
case "\$1" in
  --query-gpu=*)
    [ "\$mode" = hang ] && sleep 300
    echo "0, GPU-aaaa, Fake A100, 10, 100, 40960, 40, 60.5"
    echo "1, GPU-bbbb, Fake A100, 0, 0, 40960, 35, 50.0" ;;
  --query-compute-apps=*)
    [ "\$mode" = noprocs ] && exit 6 ;;
esac
exit 0
EOF
chmod +x "$FB/nvidia-smi"
SMI_HOME=$T/home-smi SMI_PORT=$((PORT + 2))
DANLU_HOME=$SMI_HOME "$BIN" init --yes --tls off --port $SMI_PORT --password testpass123 --public-ip 127.0.0.1 >/dev/null
BASE=http://127.0.0.1:$SMI_PORT JAR=$T/cookies-smi
start_server -u DANLU_FAKE_GPUS -u DANLU_FAKE_BUSY DANLU_HOME="$SMI_HOME" PATH="$FB:$PATH"
curl -s -c "$JAR" -o /dev/null "$BASE/$(DANLU_HOME=$SMI_HOME "$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')"
api -o /dev/null -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login"
gpus_has() { api "$BASE/api/gpus" | grep -q "$1"; }
gpus_error_since() { [ "$(api "$BASE/api/gpus" | json "d.get('error_since', 0) > 0")" = True ]; }
gpus_error_cleared() { [ "$(api "$BASE/api/gpus" | json "'error' not in d and 'error_since' not in d")" = True ]; }
check "nvidia-smi 正常时看到 2 张卡、没有报错" [ "$(api "$BASE/api/gpus" | json "str(len(d['gpus'])) + (d.get('error') or '')")" = 2 ]
echo noprocs >"$T/smi.mode"
check "查不到卡上的进程时报错" wait_until 10 gpus_has '查不到 GPU 上的进程'
check "GPU 错误返回持续时间起点" wait_until 5 gpus_error_since
CPU=$(submit "{\"name\":\"cpu\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":0}")
GJ=$(submit "{\"name\":\"gpu\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":1}")
check "排在前面、不用卡的任务照常运行" wait_status "$CPU" succeeded 10
sleep 2
check "要卡的任务保持排队" [ "$(job "$GJ" "d['status']")" = queued ]
echo hang >"$T/smi.mode"
check "nvidia-smi 卡住时报错" wait_until 25 gpus_has 'nvidia-smi 执行失败'
check "nvidia-smi 卡住期间要卡的任务还在排队" [ "$(job "$GJ" "d['status']")" = queued ]
echo ok >"$T/smi.mode"
check "nvidia-smi 恢复后不再报错" wait_until 25 bash -c "! curl -s -b '$JAR' '$BASE/api/gpus' | grep -q '\"error\"'"
check "GPU 恢复后清除错误持续时间" wait_until 25 gpus_error_cleared
check "恢复后排队的任务分到卡并成功" wait_status "$GJ" succeeded 15
kill "$SERVER_PID" 2>/dev/null; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=

echo "== ntfy 手机推送（本地记录服务）=="
export DANLU_HOME=$T/home-notify
NOTIFY_PORT=$((PORT + 4))
"$BIN" init --yes --tls off --port "$NOTIFY_PORT" --password testpass123 --public-ip 127.0.0.1 >/dev/null
sed -i 's/fail_fast = .*/fail_fast = "0s"/; s/disk_warn_gb = .*/disk_warn_gb = 0/' "$DANLU_HOME/config.toml"
cat >"$T/ntfy.py" <<'PY'
import http.server, json, sys
class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', '0'))).decode('utf-8')
        with open(sys.argv[1], 'a') as f:
            f.write(json.dumps({'path': self.path, 'headers': dict(self.headers), 'body': body}, ensure_ascii=False) + '\n')
        self.send_response(503 if self.path == '/broken-topic' else 200)
        self.end_headers()
        self.wfile.write(b'{}')
    def log_message(self, *args): pass
server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
with open(sys.argv[2], 'w') as f: f.write(str(server.server_port))
server.serve_forever()
PY
python3 "$T/ntfy.py" "$T/ntfy.jsonl" "$T/ntfy.port" >"$T/ntfy.log" 2>&1 &
NTFY_PID=$!
check "本地 ntfy 记录服务启动" wait_until 5 test -s "$T/ntfy.port"
NTFY_URL=http://127.0.0.1:$(cat "$T/ntfy.port")
printf 'notify-local-token\n' | "$BIN" notify setup --server "$NTFY_URL" --topic notify-test --token-stdin --json >"$T/notify-setup.json"
check "CLI 保存推送地址且不回显令牌" [ "$(cat "$T/notify-setup.json" | json "d['enabled'] and d['url']=='$NTFY_URL/notify-test' and 'notify-local-token' not in str(d)")" = True ]
"$BIN" notify setup >"$T/notify-qr.txt"
check "重复 setup 保留现有订阅并输出二维码" grep -q "$NTFY_URL/notify-test" "$T/notify-qr.txt"
BASE=http://127.0.0.1:$NOTIFY_PORT JAR=$T/cookies-notify
start_server
curl -s -c "$JAR" -o /dev/null "$BASE/$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')"
api -o /dev/null -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login"
check "设置页返回开启状态且不泄露 topic 或令牌" [ "$(api "$BASE/api/settings" | json "d['notify']['enabled'] and 'notify-test' not in str(d) and 'notify-local-token' not in str(d)")" = True ]
check "测试推送接口需要登录和安全入口" [ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/notify/test")" = 404 ]
check "面板测试推送成功" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/notify/test")" = 200 ]
check "CLI 测试推送成功" "$BIN" notify test
ntfy_count() { python3 -c 'import json,sys; print(sum(sys.argv[2] in json.loads(line)["body"] for line in open(sys.argv[1])))' "$T/ntfy.jsonl" "$1"; }
check "两个测试入口各发送一次" [ "$(ntfy_count '丹炉手机推送已连通')" = 2 ]
NF1=$(submit "{\"name\":\"notify-failure-one\",\"workdir\":\"$W\",\"cmd\":\"echo SECRET_LOG_PATH; exit 1\",\"gpus\":0}")
NF2=$(submit "{\"name\":\"notify-failure-two\",\"workdir\":\"$W\",\"cmd\":\"exit 2\",\"gpus\":0}")
check "通知测试的第一个任务失败" wait_status "$NF1" failed 10
check "通知测试的第二个任务失败" wait_status "$NF2" failed 10
check "合并窗口结束前不发送失败通知" [ "$(ntfy_count 'notify-failure-one')" = 0 ]
sleep 2
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
start_server
failure_sent() { [ "$(ntfy_count 'notify-failure-one')" = 1 ]; }
check "重启后保留待发批次，两个失败合成一条" wait_until 40 failure_sent
python3 - "$T/ntfy.jsonl" "$NF1" "$NF2" <<'PY' && ok "失败推送包含任务和原因，正文不含日志、路径或安全入口" || bad "失败推送内容不正确"
import json,sys
messages = [json.loads(line) for line in open(sys.argv[1])]
failure = [m for m in messages if 'notify-failure-one' in m['body']]
assert len(failure) == 1
m = failure[0]
assert '#'+sys.argv[2] in m['body'] and '#'+sys.argv[3] in m['body']
assert 'notify-failure-two' in m['body'] and '退出码' in m['body']
assert 'SECRET_LOG_PATH' not in m['body'] and '/tmp/' not in m['body']
assert m['headers']['Priority'] == '3' and m['headers']['Authorization'] == 'Bearer notify-local-token'
assert m['headers']['Click'].endswith('/jobs?status=failed')
PY
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
start_server
sleep 2
check "重启后不重发已经送达的失败批次" [ "$(ntfy_count 'notify-failure-one')" = 1 ]
check "设置页记录最近发送时间和结果" [ "$(api "$BASE/api/settings" | json "d['notify']['last_sent_at'] is not None and not d['notify']['last_error']")" = True ]
NB=$(submit "{\"name\":\"notify-blocked\",\"workdir\":\"$W\",\"cmd\":\"true\",\"pinned_gpus\":[3]}")
blocked_reason() { [ "$(job "$NB" "'外部进程' in d.get('wait_reason','')")" = True ]; }
check "任务视图说明队首等待的外部进程" wait_until 10 blocked_reason
blocked_sent() { [ "$(ntfy_count '正在等待外部进程释放 GPU')" = 1 ]; }
check "队首被外部进程阻塞时推送一次" wait_until 10 blocked_sent
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
start_server
sleep 2
check "重启恢复告警状态，不重复推送队首阻塞" [ "$(ntfy_count '正在等待外部进程释放 GPU')" = 1 ]
api -X POST "$BASE/api/jobs/$NB/cancel" -d '{}' >/dev/null
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
# 快速失败：失败原因随队列暂停一起推送，手机只响一次（ROADMAP.md 场景 3）。
# 不留外部占卡，免得排队任务同时触发"队首等待外部进程"的推送。
sed -i 's/fail_fast = .*/fail_fast = "2m"/' "$DANLU_HOME/config.toml"
start_server -u DANLU_FAKE_BUSY
NFF=$(submit "{\"name\":\"notify-fast-fail\",\"workdir\":\"$W\",\"cmd\":\"sleep 1; exit 3\",\"gpus\":4}")
NFQ=$(submit "{\"name\":\"notify-after-fast-fail\",\"workdir\":\"$W\",\"cmd\":\"true\",\"gpus\":1}")
fast_fail_sent() { [ "$(ntfy_count 'notify-fast-fail')" -ge 1 ]; }
check "快速失败后推送队列暂停" wait_until 15 fast_fail_sent
python3 - "$T/ntfy.jsonl" "$DANLU_HOME/danlu.db" "$NFF" <<'PY' && ok "快速失败只推一条，带上失败原因，不再进入失败批次" || bad "快速失败推送不正确"
import json,sqlite3,sys
messages = [m for m in map(json.loads, open(sys.argv[1])) if 'notify-fast-fail' in m['body']]
assert len(messages) == 1
m = messages[0]
assert m['headers']['Priority'] == '4' and m['headers']['Click'].endswith('/jobs/'+sys.argv[3])
assert '#'+sys.argv[3]+' notify-fast-fail：退出码 3' in m['body'] and '很快失败' in m['body']
db = sqlite3.connect('file:'+sys.argv[2]+'?mode=ro', uri=True)
state = json.loads(db.execute("SELECT value FROM settings WHERE key='ntfy.state'").fetchone()[0])
assert not state.get('batch') and not state.get('outbox')
PY
check "快速失败后的任务保持排队" [ "$(job "$NFQ" "d['status']")" = queued ]
api -X POST "$BASE/api/queue/resume" -d '{}' >/dev/null
check "继续队列后排队任务完成" wait_status "$NFQ" succeeded 15
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
"$BIN" notify setup --server "$NTFY_URL" --topic broken-topic --json >/dev/null
start_server
check "发送失败向面板返回明确错误" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/notify/test")" = 502 ]
check "设置页保留最后错误" [ "$(api "$BASE/api/settings" | json "'503' in d['notify']['last_error']")" = True ]
check "首次请求加三次重试" [ "$(python3 -c 'import json,sys; print(sum(json.loads(l)["path"]=="/broken-topic" for l in open(sys.argv[1])))' "$T/ntfy.jsonl")" = 4 ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
"$BIN" notify setup --disable --json >/dev/null
start_server
check "关闭推送后状态正确" [ "$(api "$BASE/api/settings" | json "d['notify']['enabled']")" = False ]
check "关闭推送时测试接口说明尚未开启" [ "$(code -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/notify/test")" = 409 ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=
kill "$NTFY_PID"; wait "$NTFY_PID" 2>/dev/null; NTFY_PID=

echo "== 机器体检 =="
export DANLU_HOME=$T/home-s5
S5_PORT=$((PORT + 5))
"$BIN" init --yes --tls off --port $S5_PORT --password testpass123 --public-ip 127.0.0.1 --no-qr >/dev/null
S5_ENTRANCE=$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')
BASE=http://127.0.0.1:$S5_PORT JAR=$T/cookies-s5
if env HOME="$T/userhome" "$BIN" doctor >"$T/doctor.txt" 2>&1; then
  bad "doctor 文字输出有 fail 项时应以非 0 退出"
else
  check "doctor 文字输出有 fail 项时以非 0 退出，并指出端口未监听" grep -q '^\[FAIL\] 服务端口' "$T/doctor.txt"
fi
start_server
curl -s -c "$JAR" -o /dev/null "$BASE/$S5_ENTRANCE"
api -o /dev/null -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login"
# 客户端按非 0 退出码丢弃 ssh 输出，所以 --json 有 fail 项（这里没有 systemd 单元）也要以 0 退出
check "doctor --json 有 fail 项时仍以 0 退出" env HOME="$T/userhome" bash -c "'$BIN' doctor --json >'$T/doctor.json'"
python3 - "$T/doctor.json" <<'PY' && ok "doctor --json 覆盖全部检查项，假 GPU 和端口正常，缺服务单元报 fail" || bad "doctor --json 内容不正确"
import json,sys
checks = json.load(open(sys.argv[1]))['checks']
by = {c['id']: c for c in checks}
assert len(by) == len(checks)
assert all(c['level'] in ('ok', 'warn', 'fail') and c['title'] and c['detail'] for c in checks)
for k in ('gpu', 'conda', 'service_unit', 'service_active', 'service_kill_mode', 'service_restart', 'linger',
          'time_sync', 'data_disk', 'output_disk', 'server_cert', 'port', 'version'):
    assert k in by, k
assert by['gpu']['level'] == 'ok' and '4 张 GPU' in by['gpu']['detail']
assert by['port']['level'] == 'ok' and by['service_unit']['level'] == 'fail'
assert by['server_cert']['level'] == 'warn'  # HTTPS 未启用
PY
api "$BASE/api/doctor" >"$T/doctor-api.json"
check "/api/doctor 与命令行返回同一组检查" python3 -c 'import json,sys; ids=lambda p: [c["id"] for c in json.load(open(p))["checks"]]; assert ids(sys.argv[1]) == ids(sys.argv[2])' "$T/doctor.json" "$T/doctor-api.json"
rm -f "$T/jar-nosession"; curl -s -c "$T/jar-nosession" -o /dev/null "$BASE/$S5_ENTRANCE"
check "没登录时 /api/doctor 返回 401" [ "$(curl -s -o /dev/null -w '%{http_code}' -b "$T/jar-nosession" "$BASE/api/doctor")" = 401 ]

echo "== 两步验证 =="
totp_code() { # 密钥 [时间窗偏移]：按 RFC 6238 算出验证码
  python3 -c 'import base64,hashlib,hmac,struct,sys,time
s=sys.argv[1]; k=base64.b32decode(s+"="*(-len(s)%8)); n=int(time.time())//30+int(sys.argv[2])
h=hmac.new(k,struct.pack(">Q",n),hashlib.sha1).digest(); o=h[-1]&15
print("%06d"%((struct.unpack(">I",h[o:o+4])[0]&0x7fffffff)%1000000))' "$1" "${2:-0}"
}
totp_login() { # 验证码（可空）：用新的 cookie 文件过入口、登录，输出状态码，响应体写到 totp-login.json
  rm -f "$T/jar-totp"
  curl -s -c "$T/jar-totp" -o /dev/null "$BASE/$S5_ENTRANCE"
  curl -s -b "$T/jar-totp" -o "$T/totp-login.json" -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"testpass123\",\"code\":\"$1\"}" "$BASE/api/login"
}
check "totp enable 输错验证码时不开启" bash -c "! echo 000000 | '$BIN' totp enable >/dev/null 2>&1"
check "输错后设置页仍显示未开启" [ "$(api "$BASE/api/settings" | json "d['totp_enabled']")" = False ]
# 读出终端打印的密钥，算出当前验证码输入确认；输出"密钥 确认用的验证码"
read -r TOTP_SECRET TOTP_CONFIRM < <(python3 - "$BIN" <<'PY'
import base64,hashlib,hmac,struct,subprocess,sys,time
p = subprocess.Popen([sys.argv[1], 'totp', 'enable'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
secret = next(line.split()[-1] for line in p.stdout if line.startswith('密钥'))
key = base64.b32decode(secret + '=' * (-len(secret) % 8))
h = hmac.new(key, struct.pack('>Q', int(time.time()) // 30), hashlib.sha1).digest(); o = h[-1] & 15
code = '%06d' % ((struct.unpack('>I', h[o:o+4])[0] & 0x7fffffff) % 1000000)
out, _ = p.communicate(code + '\n')
print(secret, code if p.returncode == 0 and '已开启两步验证' in out else 'failed')
PY
)
check "totp enable 确认验证码后开启" [ "${#TOTP_SECRET}" = 32 -a -n "$TOTP_CONFIRM" -a "$TOTP_CONFIRM" != failed ]
check "设置页显示两步验证已开启" [ "$(api "$BASE/api/settings" | json "d['totp_enabled']")" = True ]
check "已开启时再次 enable 报错" bash -c "! echo 000000 | '$BIN' totp enable >/dev/null 2>&1"
check "只填密码返回 401 和 need_totp" [ "$(totp_login '')" = 401 -a "$(json "d['need_totp']" <"$T/totp-login.json")" = True ]
for _ in 1 2 3 4 5; do totp_login '' >/dev/null; done
check "密码正确、未填验证码不计入登录限速" [ "$(totp_login '')" = 401 ]
check "启用时确认过的验证码不能再用来登录" [ "$(totp_login "$TOTP_CONFIRM")" = 401 ]
TOTP_NEXT=$(totp_code "$TOTP_SECRET" 1)
check "下一个时间窗的验证码可以登录" [ "$(totp_login "$TOTP_NEXT")" = 200 ]
check "同一时间窗的验证码不能用第二次" [ "$(totp_login "$TOTP_NEXT")" = 401 ]
check "totp disable 关闭两步验证" bash -c "'$BIN' totp disable | grep -q '已关闭两步验证'"
check "关闭后只填密码即可登录" [ "$(totp_login '')" = 200 ]

echo "== 静态资源压缩 =="
ASSET=$(curl -s -b "$JAR" "$BASE/" | grep -o '/assets/index-[^"]*\.js' | head -1)
check "首页引用带哈希的入口脚本" [ -n "$ASSET" ]
check "接受 gzip 时返回构建时预压缩的资源" bash -c "curl -s -D - -o '$T/asset.gz' -b '$JAR' -H 'Accept-Encoding: gzip' '$BASE$ASSET' | grep -qi '^content-encoding: gzip' && gzip -t '$T/asset.gz'"
check "不接受 gzip 时返回原文" bash -c "! curl -s -D - -o /dev/null -b '$JAR' '$BASE$ASSET' | grep -qi '^content-encoding'"

echo "== 并发登录限速 =="
audit_fails() {
  python3 -c 'import sqlite3,sys; print(sqlite3.connect("file:"+sys.argv[1]+"?mode=ro",uri=True).execute("SELECT count(*) FROM audit WHERE action=?",("login_fail",)).fetchone()[0])' "$DANLU_HOME/danlu.db"
}
FAILS_BEFORE=$(audit_fails)
rm -f "$T/jar-race"; curl -s -c "$T/jar-race" -o /dev/null "$BASE/$S5_ENTRANCE"
seq 21 | xargs -P 21 -I{} curl -s -o /dev/null -b "$T/jar-race" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"wrong"}' "$BASE/api/login"
check "同一 IP 并发输错 21 次，只校验到上限 5 次" [ $(( $(audit_fails) - FAILS_BEFORE )) = 5 ]
check "并发失败后锁定仍然有效（429）" [ "$(curl -s -o /dev/null -w '%{http_code}' -b "$T/jar-race" -H 'Content-Type: application/json' -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login")" = 429 ]
kill -TERM "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=

echo "== HTTPS（本地 CA）=="
export DANLU_HOME=$T/home-tls
TLS_PORT=$((PORT + 1))
"$BIN" init --yes --port $TLS_PORT --password testpass123 --public-ip 127.0.0.1 >/dev/null
"$BIN" serve >>"$T/server-tls.log" 2>&1 &
SERVER_PID=$!
sleep 1.5
TLS_URL=$("$BIN" entrance | awk '{print $NF}')
CA=$DANLU_HOME/tls/ca.crt
if command -v openssl >/dev/null; then
  check "openssl 用本地 CA 校验服务器证书通过" bash -c "openssl verify -CAfile '$CA' '$DANLU_HOME/tls/server.crt' >/dev/null 2>&1"
fi
check "curl 带上 CA 访问安全入口得到 302" [ "$(curl -s -o /dev/null -w '%{http_code}' --cacert "$CA" "$TLS_URL")" = 302 ]
check "不带 CA 时证书校验失败" bash -c "! curl -s -o /dev/null '$TLS_URL'"
"$BIN" ca >"$T/fetched-ca.crt" 2>"$T/ca-stderr.log"
check "danlu ca 输出的就是本地 CA 证书" cmp -s "$T/fetched-ca.crt" "$CA"
INFO_FP=$("$BIN" info --no-qr | awk '/CA 指纹/{print $NF}')
check "danlu ca 的指纹和 danlu info 一致" bash -c "[ -n '$INFO_FP' ] && grep -qF '$INFO_FP' '$T/ca-stderr.log'"
check "用 danlu ca 取回的 CA 能校验面板证书" [ "$(curl -s -o /dev/null -w '%{http_code}' --cacert "$T/fetched-ca.crt" "$TLS_URL")" = 302 ]
check "没有使用本地 CA 时 danlu ca 报错" bash -c "! env DANLU_HOME='$MAIN_HOME' '$BIN' ca >/dev/null 2>&1"
"$BIN" doctor --json >"$T/doctor-tls.json"
python3 - "$T/doctor-tls.json" <<'PY' && ok "doctor 确认本地 CA 和服务器证书有效、证书包含 public_ip" || bad "doctor 的证书检查不正确"
import json,sys
by = {c['id']: c for c in json.load(open(sys.argv[1]))['checks']}
assert all(by[k]['level'] == 'ok' for k in ('server_cert', 'ca_cert', 'cert_ip', 'port')), by
PY

echo
echo "通过 $PASS 项，失败 $FAIL 项（临时目录 $T）"
[ "$FAIL" = 0 ]
