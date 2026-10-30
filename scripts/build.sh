#!/usr/bin/env bash
# 构建前端并编译 Linux 版本：  bash scripts/build.sh [amd64|arm64]
set -euo pipefail
ARCH=${1:-amd64}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
(cd "$ROOT/web" && pnpm install --frozen-lockfile && pnpm build)
touch "$ROOT/internal/webui/dist/.gitkeep" # vite 会清空 dist，补回占位文件
VERSION="dev-$(date +%Y%m%d-%H%M)"
cd "$ROOT"
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/danlu-linux-$ARCH" ./cmd/danlu
echo "已生成 dist/danlu-linux-$ARCH（$VERSION）"
