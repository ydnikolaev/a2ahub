#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CACHE_DIR="$ROOT_DIR/.build/cache"

mkdir -p "$CACHE_DIR/clang" "$CACHE_DIR/swiftpm"
CLANG_MODULE_CACHE_PATH="$CACHE_DIR/clang" \
SWIFTPM_MODULECACHE_OVERRIDE="$CACHE_DIR/clang" \
swift test \
    --package-path "$ROOT_DIR" \
    --disable-sandbox \
    --cache-path "$CACHE_DIR/swiftpm"
