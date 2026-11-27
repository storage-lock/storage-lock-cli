#!/usr/bin/env bash
# 发版前准备：确认模块可解析、依赖完整。
#
# 为什么不直接写 `go mod tidy`：
#   goreleaser 的 before.hooks 只接受字符串命令，无法注入单独的环境变量。
#   而这里的环境（GOPROXY）决定依赖能否拉到。
set -euo pipefail

export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

if ! go mod verify >/dev/null 2>&1; then
  echo "go mod verify failed" >&2
  exit 1
fi
go mod download
echo "dependencies resolved via $GOPROXY"
