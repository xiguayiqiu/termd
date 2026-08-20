#!/usr/bin/env bash
# termd 多平台交叉编译脚本
#
# 用法：
#   ./build.sh                 # 编译全部平台到 dist/
#   ./build.sh linux/arm64     # 只编译指定平台
#
# 支持平台：Linux (amd64/arm64)、macOS (amd64/arm64)、BSD (freebsd/openbsd/netbsd)
set -euo pipefail

cd "$(dirname "$0")"

OUT_DIR="dist"
mkdir -p "$OUT_DIR"

PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "freebsd/amd64"
  "freebsd/arm64"
  "openbsd/amd64"
  "openbsd/arm64"
  "netbsd/amd64"
  "netbsd/arm64"
)

# 传入参数时只编译指定平台（格式：os/arch）
TARGET="${1:-}"

build_one() {
  local os_arch="$1"
  local goos="${os_arch%/*}"
  local goarch="${os_arch#*/}"
  local name="termd-${goos}-${goarch}"
  echo "==> GOOS=${goos} GOARCH=${goarch} -> ${OUT_DIR}/${name}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -trimpath -ldflags "-s -w" -o "${OUT_DIR}/${name}" ./cmd/termd
}

if [[ -n "${TARGET}" ]]; then
  build_one "${TARGET}"
else
  for p in "${PLATFORMS[@]}"; do
    build_one "${p}"
  done
fi

echo "==> 完成："
ls -lh "${OUT_DIR}"
