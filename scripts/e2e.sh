#!/usr/bin/env bash
# danlu 端到端测试：假 GPU + 本地 HTTP，在 Linux 或 WSL 里运行。
#   bash scripts/e2e.sh [danlu 二进制路径]
set -uo pipefail

BIN_SRC=${1:-"$(dirname "$0")/../dist/danlu-linux-amd64"}
T=$(mktemp -d /tmp/danlu-e2e.XXXX)
BIN=$T/danlu
cp "$BIN_SRC" "$BIN" && chmod +x "$BIN"
export DANLU_HOME=$T/home DANLU_FAKE_GPUS=4 DANLU_FAKE_BUSY=3
PORT=${E2E_PORT:-17699} # 和演示环境（17600）错开
BASE=http://127.0.0.1:$PORT
JAR=$T/cookies
PASS=0 FAIL=0
SERVER_PID=

ok()   { echo "  ✔ $*"; PASS=$((PASS+1)); }
bad()  { echo "  ✘ $*"; FAIL=$((FAIL+1)); }
check() { local desc=$1; shift; if "$@"; then ok "$desc"; else bad "$desc"; fi; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
api()  { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
code() { curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" "$@"; }
job()  { api "$BASE/api/jobs/$1" | json "$2"; }
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
  [ -f "$T/bashrc-bg.pid" ] && kill "$(cat "$T/bashrc-bg.pid")" 2>/dev/null
}
trap cleanup EXIT

echo "== 初始化 =="
"$BIN" init --yes --tls off --port $PORT --password testpass123 --public-ip 127.0.0.1 --conda "$T/fakeconda" >"$T/init.log" 2>&1 \
  && ok "init 成功" || { bad "init 失败"; cat "$T/init.log"; exit 1; }
ENTRANCE=$("$BIN" entrance | awk '{print $NF}' | sed 's#.*/##')
[ -n "$ENTRANCE" ] || { bad "读不到安全入口"; exit 1; }
sed -i 's/kill_grace = .*/kill_grace = "3s"/' "$DANLU_HOME/config.toml"
# 假 conda：activate 时只设置一个环境变量
mkdir -p "$T/fakeconda/etc/profile.d"
echo 'conda() { [ "$1" = activate ] && export FAKE_CONDA_ENV="$2"; }' > "$T/fakeconda/etc/profile.d/conda.sh"
# 工作目录：一个有未提交改动的 git 仓库
W=$T/work; mkdir -p "$W"
git -C "$W" init -q && echo 'print("v1")' > "$W/train.py" && git -C "$W" add . \
  && git -C "$W" -c user.name=t -c user.email=t@t commit -qm init && echo 'print("v2")' > "$W/train.py"
# 服务进程的 .bashrc：导出一个变量，再起一个占着输出管道的后台进程
mkdir -p "$T/userhome"
cat >"$T/userhome/.bashrc" <<EOF
export E2E_BASHRC=loaded
sleep 30 & echo \$! > "$T/bashrc-bg.pid"
EOF

start_server

echo "== 安全入口与登录 =="
check "未过入口访问首页返回 404" [ "$(code "$BASE/")" = 404 ]
check "未过入口访问 API 返回 404" [ "$(code "$BASE/api/me")" = 404 ]
check "错误入口返回 404" [ "$(code "$BASE/wrongentrance")" = 404 ]
check "正确入口跳转 302" [ "$(code "$BASE/$ENTRANCE")" = 302 ]
check "过入口后未登录 API 返回 401" [ "$(code "$BASE/api/me")" = 401 ]
check "错误密码返回 401" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"nope"}' "$BASE/api/login")" = 401 ]
check "正确密码登录成功" [ "$(code -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"testpass123"}' "$BASE/api/login")" = 200 ]
check "登录后访问 API 返回 200" [ "$(code "$BASE/api/me")" = 200 ]
check "表单格式的写请求被拒绝" [ "$(code -X POST -d 'x=1' "$BASE/api/jobs")" = 415 ]

echo "== GPU =="
sleep 2
GPUS=$(api "$BASE/api/gpus")
check "看到 4 张假 GPU" [ "$(echo "$GPUS" | json "len(d['gpus'])")" = 4 ]
check "GPU 3 显示为外部进程占用" [ "$(echo "$GPUS" | json "len(d['gpus'][3]['external'])")" = 1 ]

echo "== 调度 =="
A=$(submit "{\"name\":\"A\",\"workdir\":\"$W\",\"cmd\":\"echo hello; python3 train.py; sleep 4\",\"gpus\":2,\"conda_env\":\"myenv\"}")
B=$(submit "{\"name\":\"B\",\"workdir\":\"$W\",\"cmd\":\"echo \$CUDA_VISIBLE_DEVICES; sleep 1\",\"gpus\":1}")
C=$(submit "{\"name\":\"C\",\"workdir\":\"$W\",\"cmd\":\"sleep 1\",\"pinned_gpus\":[3]}")
D=$(submit "{\"name\":\"D\",\"workdir\":\"$W\",\"cmd\":\"echo conda=\$FAKE_CONDA_ENV\",\"gpus\":2,\"conda_env\":\"myenv\"}")
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
check "B 的日志里 CUDA_VISIBLE_DEVICES=2" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$B/log?tail=10000' | grep -q '\"data\":\"[^\"]*\\\\n2\\\\n'"

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

echo "== 退出码与残留进程 =="
E=$(submit "{\"name\":\"E\",\"workdir\":\"$W\",\"cmd\":\"echo bye; exit 3\",\"gpus\":1}")
check "退出码 3 记为失败" wait_status "$E" failed 10
check "记录了退出码 3" [ "$(job "$E" "d['exit_code']")" = 3 ]
F=$(submit "{\"name\":\"F\",\"workdir\":\"$W\",\"cmd\":\"setsid sleep 1000 & sleep 1\",\"gpus\":0}")
check "F 成功结束" wait_status "$F" succeeded 10
check "F 用 setsid 留下的残留进程被清理" bash -c "[[ \"\$(curl -s -b '$JAR' '$BASE/api/jobs/$F' | python3 -c 'import sys,json;print(json.load(sys.stdin)[\"fail_reason\"])')\" == *已清理* ]]"
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
kill -9 "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null
sleep 1
check "danlu 被杀后训练进程还活着" bash -c "grep -l 'DANLU_JOB_ID=$H' /proc/[0-9]*/environ 2>/dev/null | grep -q ."
start_server
check "重启后 H 仍是运行中" [ "$(job "$H" "d['status']")" = running ]
check "H 最终成功" wait_status "$H" succeeded 15
check "服务日志里有接管记录" grep -q "接管运行中的任务 #$H" "$T/server.log"

echo "== 日志 =="
check "tail 读取到 H 的输出" bash -c "curl -s -b '$JAR' '$BASE/api/jobs/$H/log?tail=100000' | grep -q 'step 6'"
check "SSE 推送日志并在结束后发送 end" bash -c "curl -s -N --max-time 5 -b '$JAR' '$BASE/api/jobs/$H/log/stream?offset=0' | grep -q 'event: end'"
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
check "replace 用新程序原子替换旧程序" bash -c "'$T/danlu-copy.new' replace --no-restart --json '$T/danlu-copy' | grep -q '\"restarted\": false' && [ ! -e '$T/danlu-copy.new' ]"
check "replace 把旧程序留成 .prev" [ -x "$T/danlu-copy.prev" ]

echo "== 停止服务 =="
kill -TERM "$SERVER_PID"
check "收到 SIGTERM 后正常停止" wait_until 10 grep -q 'danlu 已停止' "$T/server.log"
wait "$SERVER_PID" 2>/dev/null; SERVER_PID=

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
check "nvidia-smi 正常时看到 2 张卡、没有报错" [ "$(api "$BASE/api/gpus" | json "str(len(d['gpus'])) + (d.get('error') or '')")" = 2 ]
echo noprocs >"$T/smi.mode"
check "查不到卡上的进程时报错" wait_until 10 gpus_has '查不到 GPU 上的进程'
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
check "恢复后排队的任务分到卡并成功" wait_status "$GJ" succeeded 15
kill "$SERVER_PID" 2>/dev/null; wait "$SERVER_PID" 2>/dev/null; SERVER_PID=

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

echo
echo "通过 $PASS 项，失败 $FAIL 项（临时目录 $T）"
[ "$FAIL" = 0 ]
