#!/usr/bin/env bash
# 发版前准备：确认模块可解析、依赖完整。
#
# 为什么不直接写 `go mod tidy`：
#   goreleaser 的 before.hooks 只接受字符串命令，无法注入单独的环境变量，
#   而 GOPROXY 必须在这里设——默认的 direct 会在 CI 上为每个依赖单独
#   git ls-remote，网络抖动一次就整体失败。
set -euo pipefail

export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

# 只做校验，不改依赖：发布物必须与仓库里的 go.mod 完全一致，
# 在这里 tidy 反而会让 tag 指向的代码和产物对不上。
if ! go mod verify >/dev/null 2>&1; then
  echo "go mod verify failed" >&2
  exit 1
fi
go mod download
echo "dependencies resolved via $GOPROXY"
