#!/bin/bash
set -euo pipefail

APP_PATH="${1:?usage: verify-app.sh APP_PATH [EXPECTED_VERSION]}"
EXPECTED_VERSION="${2:-}"
PLIST="$APP_PATH/Contents/Info.plist"
EXECUTABLE="$APP_PATH/Contents/MacOS/A2ANotifier"

test -f "$PLIST"
test -x "$EXECUTABLE"
test "$(plutil -extract CFBundleIdentifier raw "$PLIST")" = "io.a2ahub.notifier"
test "$(plutil -extract LSMinimumSystemVersion raw "$PLIST")" = "13.0"
test "$(plutil -extract LSUIElement raw "$PLIST")" = "true"
test "$(plutil -extract CFBundleExecutable raw "$PLIST")" = "A2ANotifier"

if [[ -n "$EXPECTED_VERSION" ]]; then
    test "$(plutil -extract CFBundleShortVersionString raw "$PLIST")" = "$EXPECTED_VERSION"
fi

echo "verified bundle structure: $APP_PATH"
