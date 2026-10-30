#!/usr/bin/env bash
# 本地演示环境：假 GPU + HTTP，在 WSL / Linux 里运行，用浏览器打开打印出的地址。
#   bash scripts/dev-wsl.sh [danlu 二进制路径]
set -euo pipefail
BIN_SRC=${1:-"$(dirname "$0")/../dist/danlu-linux-amd64"}
D=/tmp/danlu-dev
mkdir -p "$D"
cp "$BIN_SRC" "$D/danlu" && chmod +x "$D/danlu"
export DANLU_HOME=$D/home DANLU_FAKE_GPUS=${DANLU_FAKE_GPUS:-4} DANLU_FAKE_BUSY=${DANLU_FAKE_BUSY:-3}

if [ ! -f "$DANLU_HOME/config.toml" ]; then
  "$D/danlu" init --yes --tls off --port 17600 --password danlu-dev-123 --public-ip 127.0.0.1 >/dev/null
fi

# 演示用的训练项目：git 仓库 + 一个会输出 tqdm 风格进度和 W&B 链接的脚本
W=$D/demo-project
if [ ! -d "$W/.git" ]; then
  mkdir -p "$W/configs"
  cat > "$W/train.py" <<'PY'
import os, sys, time

print("\x1b[34m\x1b[1mwandb\x1b[0m: 🚀 View run at \x1b[34m\x1b[4mhttps://wandb.ai/demo/vit/runs/r%s\x1b[0m" % os.environ.get("DANLU_JOB_ID", "0"), flush=True)
print("GPU:", os.environ.get("CUDA_VISIBLE_DEVICES"), " master_port:", os.environ.get("PET_MASTER_PORT"), flush=True)
epochs = int(sys.argv[1]) if len(sys.argv) > 1 else 3
for ep in range(epochs):
    for i in range(101):
        bar = "█" * (i // 5)
        sys.stdout.write(f"\repoch {ep + 1}/{epochs}: {i:3d}%|{bar:<20}| {i}/100 [loss={2.5 / (1 + ep + i / 100):.4f}]")
        sys.stdout.flush()
        time.sleep(0.08)
    print(f"\n\x1b[32mepoch {ep + 1} 完成\x1b[0m val_acc={0.6 + ep * 0.1:.3f}", flush=True)
if os.environ.get("FAIL"):
    print("\x1b[31mRuntimeError: CUDA out of memory. Tried to allocate 2.00 GiB\x1b[0m", flush=True)
    sys.exit(1)
PY
  printf 'model: vit_b16\nlr: 3e-4\nbatch_size: 256\n' > "$W/configs/vit.yaml"
  printf 'outputs/\nwandb/\n' > "$W/.gitignore"
  git -C "$W" init -q && git -C "$W" add . && git -C "$W" -c user.name=demo -c user.email=demo@x commit -qm "init"
  sed -i 's/lr: 3e-4/lr: 1e-4/' "$W/configs/vit.yaml"   # 一处未提交的改动
fi

echo "演示项目目录：$W"
echo "访问地址：$("$D/danlu" entrance | awk '{print $NF}')   用户名 admin  密码 danlu-dev-123"
exec "$D/danlu" serve
