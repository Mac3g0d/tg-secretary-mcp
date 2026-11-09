#!/usr/bin/env bash
# Cross-compiles release archives into dist/.
#
# Usage: scripts/dist.sh <version>      e.g. scripts/dist.sh v0.1.0
set -euo pipefail

version="${1:?usage: scripts/dist.sh <version>}"
cd "$(dirname "$0")/.."

# os/arch[/goarm] — linux/arm/7 covers 32-bit ARMv7 boards such as the
# Orange Pi PC Plus (Allwinner H3); linux/arm/6 covers Raspberry Pi 1/Zero.
targets=(
  linux/amd64
  linux/arm64
  linux/arm/7
  linux/arm/6
  darwin/amd64
  darwin/arm64
  windows/amd64
)

out="dist"
mkdir -p "$out"
work="$(mktemp -d)"
trap 'rm -r "$work"' EXIT

for target in "${targets[@]}"; do
  IFS=/ read -r goos goarch goarm <<<"$target"
  name="tg-secretary_${version}_${goos}_${goarch}${goarm:+v$goarm}"
  stage="$work/$name"
  mkdir -p "$stage"

  binary="tg-secretary"
  if [[ "$goos" == "windows" ]]; then
    binary="tg-secretary.exe"
  fi

  echo "building $name"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
    go build -trimpath -ldflags "-s -w -X main.version=$version" \
    -o "$stage/$binary" ./cmd/tg-secretary
  cp README.md LICENSE .env.example "$stage/"

  # Archives are flat: the binary and the docs sit at the archive root.
  files=("$binary" README.md LICENSE .env.example)
  if [[ "$goos" == "windows" ]]; then
    (cd "$stage" && zip -q "$work/$name.zip" "${files[@]}")
    mv "$work/$name.zip" "$out/"
  else
    tar -C "$stage" -czf "$out/$name.tar.gz" "${files[@]}"
  fi
done

(cd "$out" && sha256sum tg-secretary_"${version}"_* >checksums.txt)
echo "done:"
ls -1 "$out"
