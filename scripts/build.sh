#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

mkdir -p "$ROOT_DIR/bin" "$ROOT_DIR/.tmp" "$ROOT_DIR/.cache/go-build"

npm --prefix "$ROOT_DIR/frontend" run build

env \
  CGO_ENABLED=0 \
  GOCACHE="$ROOT_DIR/.cache/go-build" \
  GOTMPDIR="$ROOT_DIR/.tmp" \
  go build -o "$ROOT_DIR/bin/litedns" ./cmd/litedns
