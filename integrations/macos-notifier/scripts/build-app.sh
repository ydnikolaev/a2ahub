#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${VERSION:-$(tr -d '[:space:]' < "$ROOT_DIR/VERSION")}"
BUILD_NUMBER="${BUILD_NUMBER:-1}"
CONFIGURATION="${CONFIGURATION:-release}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist}"
APP_DIR="$OUTPUT_DIR/A2A Notifier.app"
CACHE_DIR="$ROOT_DIR/.build/cache"

case "$VERSION" in
    *[!0-9.]*|"") echo "VERSION must contain only dotted decimal components" >&2; exit 2 ;;
esac
case "$BUILD_NUMBER" in
    *[!0-9]*|"") echo "BUILD_NUMBER must be a positive integer" >&2; exit 2 ;;
esac
case "$CONFIGURATION" in
    release) ARCHITECTURES=(arm64 x86_64) ;;
    debug)
        case "$(uname -m)" in
            arm64|x86_64) ARCHITECTURES=("$(uname -m)") ;;
            *) echo "unsupported debug build architecture: $(uname -m)" >&2; exit 2 ;;
        esac
        ;;
    *) echo "CONFIGURATION must be release or debug" >&2; exit 2 ;;
esac

mkdir -p "$CACHE_DIR/clang" "$CACHE_DIR/swiftpm" "$OUTPUT_DIR"
SWIFT_BUILD_ARGS=(
    --package-path "$ROOT_DIR" \
    --configuration "$CONFIGURATION" \
    --product A2ANotifier \
    --disable-sandbox \
    --cache-path "$CACHE_DIR/swiftpm"
)

ARCH_BINARIES=()
for ARCHITECTURE in "${ARCHITECTURES[@]}"; do
    TRIPLE="$ARCHITECTURE-apple-macosx"
    SCRATCH_PATH="$ROOT_DIR/.build/$ARCHITECTURE-$CONFIGURATION"

    CLANG_MODULE_CACHE_PATH="$CACHE_DIR/clang" \
    SWIFTPM_MODULECACHE_OVERRIDE="$CACHE_DIR/clang" \
    swift build \
        "${SWIFT_BUILD_ARGS[@]}" \
        --triple "$TRIPLE" \
        --scratch-path "$SCRATCH_PATH"

    BIN_DIR="$(
        CLANG_MODULE_CACHE_PATH="$CACHE_DIR/clang" \
        SWIFTPM_MODULECACHE_OVERRIDE="$CACHE_DIR/clang" \
        swift build \
            "${SWIFT_BUILD_ARGS[@]}" \
            --triple "$TRIPLE" \
            --scratch-path "$SCRATCH_PATH" \
            --show-bin-path
    )"
    ARCH_BINARY="$BIN_DIR/A2ANotifier"
    test -x "$ARCH_BINARY"
    xcrun lipo "$ARCH_BINARY" -verify_arch "$ARCHITECTURE"
    ARCH_BINARIES+=("$ARCH_BINARY")
done

rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"
EXECUTABLE="$APP_DIR/Contents/MacOS/A2ANotifier"
if [[ "$CONFIGURATION" == "release" ]]; then
    xcrun lipo -create "${ARCH_BINARIES[@]}" -output "$EXECUTABLE"
    chmod 0755 "$EXECUTABLE"
else
    install -m 0755 "${ARCH_BINARIES[0]}" "$EXECUTABLE"
fi
xcrun lipo "$EXECUTABLE" -verify_arch "${ARCHITECTURES[@]}"
ACTUAL_ARCHITECTURES="$(xcrun lipo "$EXECUTABLE" -archs)"
test "$(awk '{print NF}' <<< "$ACTUAL_ARCHITECTURES")" -eq "${#ARCHITECTURES[@]}"

sed \
    -e "s/@VERSION@/$VERSION/g" \
    -e "s/@BUILD_NUMBER@/$BUILD_NUMBER/g" \
    "$ROOT_DIR/Resources/Info.plist" > "$APP_DIR/Contents/Info.plist"
plutil -lint "$APP_DIR/Contents/Info.plist"

echo "$APP_DIR"
