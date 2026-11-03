#!/usr/bin/env bash
# 构建前端并编译 Linux 版本：  bash scripts/build.sh [amd64|arm64]
set -euo pipefail
ARCH=${1:-amd64}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
(cd "$ROOT/web" && pnpm install --frozen-lockfile && pnpm build)
node "$ROOT/scripts/precompress.mjs"
touch "$ROOT/internal/webui/dist/.gitkeep" # vite 会清空 dist，补回占位文件
if [[ -z "${VERSION:-}" ]]; then
  DESCRIPTION=$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || printf 'unknown')
  # 只有正好落在 tag 上、工作区干净的构建才算正式版本；tag 之后的提交（-N-g哈希）和未提交改动（-dirty）都算开发版
  if [[ "$DESCRIPTION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ && ! "$DESCRIPTION" =~ -[0-9]+-g[0-9a-f]+ && "$DESCRIPTION" != *-dirty ]]; then
    VERSION=$DESCRIPTION
  else
    VERSION="dev-$DESCRIPTION"
  fi
fi
cd "$ROOT"
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/danlu-linux-$ARCH" ./cmd/danlu
echo "已生成 dist/danlu-linux-$ARCH（$VERSION）"
