# 构建前端并交叉编译 Linux 版本：  .\scripts\build.ps1 [-Arch amd64|arm64]
param([string]$Arch = "amd64")

$root = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $root "web")
pnpm install --frozen-lockfile
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "pnpm install 失败" }
pnpm build
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "前端构建失败" }
Pop-Location
# vite 会清空 dist，补回占位文件让 go:embed 在没构建前端时也能编译
New-Item -ItemType File -Force (Join-Path $root "internal/webui/dist/.gitkeep") | Out-Null

$version = "dev-" + (Get-Date -Format "yyyyMMdd-HHmm")
$env:GOOS = "linux"; $env:GOARCH = $Arch; $env:CGO_ENABLED = "0"
go build -C $root -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/danlu-linux-$Arch" ./cmd/danlu
$code = $LASTEXITCODE
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
if ($code -ne 0) { throw "go build 失败" }
Write-Host "已生成 dist/danlu-linux-$Arch（$version）"
