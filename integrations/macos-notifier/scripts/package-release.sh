#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
: "${SIGNING_MODE:?SIGNING_MODE must be developer-id or adhoc}"

VERSION="${VERSION:-$(tr -d '[:space:]' < "$ROOT_DIR/VERSION")}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist}"
APP_PATH="$OUTPUT_DIR/A2A Notifier.app"
ZIP_PATH="$OUTPUT_DIR/a2a-notifier-$VERSION.zip"

case "$SIGNING_MODE" in
    developer-id)
        : "${SIGNING_IDENTITY:?SIGNING_IDENTITY must name the Developer ID Application identity}"
        : "${SIGNING_TEAM_ID:?SIGNING_TEAM_ID must be the pinned Apple team ID from the cohort}"
        : "${NOTARY_PROFILE:?NOTARY_PROFILE must name an xcrun notarytool keychain profile}"
        ;;
    adhoc)
        if [[ -n "${SIGNING_IDENTITY:-}${SIGNING_TEAM_ID:-}${NOTARY_PROFILE:-}" ]]; then
            echo "adhoc mode refuses Developer ID/notary inputs; choose exactly one signing mode" >&2
            exit 2
        fi
        ;;
    *)
        echo "SIGNING_MODE must be developer-id or adhoc" >&2
        exit 2
        ;;
esac

"$SCRIPT_DIR/build-app.sh"

case "$SIGNING_MODE" in
    developer-id)
        /usr/bin/codesign \
            --force \
            --timestamp \
            --options runtime \
            --entitlements "$ROOT_DIR/Resources/A2ANotifier.entitlements" \
            --sign "$SIGNING_IDENTITY" \
            "$APP_PATH"

        /usr/bin/codesign --verify --deep --strict --verbose=2 "$APP_PATH"
        ACTUAL_TEAM_ID="$(
            /usr/bin/codesign -d --verbose=4 "$APP_PATH" 2>&1 |
                sed -n 's/^TeamIdentifier=//p'
        )"
        test "$ACTUAL_TEAM_ID" = "$SIGNING_TEAM_ID"

        rm -f "$ZIP_PATH"
        /usr/bin/ditto -c -k --keepParent "$APP_PATH" "$ZIP_PATH"
        xcrun notarytool submit "$ZIP_PATH" --keychain-profile "$NOTARY_PROFILE" --wait
        xcrun stapler staple "$APP_PATH"
        xcrun stapler validate "$APP_PATH"

        # The stapled ticket is part of the shipped app, so recreate the archive.
        rm -f "$ZIP_PATH"
        /usr/bin/ditto -c -k --keepParent "$APP_PATH" "$ZIP_PATH"
        /usr/sbin/spctl --assess --type execute --verbose=2 "$APP_PATH"
        ;;
    adhoc)
        /usr/bin/codesign \
            --force \
            --options runtime \
            --entitlements "$ROOT_DIR/Resources/A2ANotifier.entitlements" \
            --sign - \
            "$APP_PATH"
        /usr/bin/codesign --verify --deep --strict --verbose=2 "$APP_PATH"
        SIGNATURE="$(
            /usr/bin/codesign -d --verbose=4 "$APP_PATH" 2>&1 |
                sed -n 's/^Signature=//p'
        )"
        ACTUAL_TEAM_ID="$(
            /usr/bin/codesign -d --verbose=4 "$APP_PATH" 2>&1 |
                sed -n 's/^TeamIdentifier=//p'
        )"
        test "$SIGNATURE" = "adhoc"
        test "$ACTUAL_TEAM_ID" = "not set"

        rm -f "$ZIP_PATH"
        /usr/bin/ditto -c -k --keepParent "$APP_PATH" "$ZIP_PATH"
        ;;
esac

"$SCRIPT_DIR/verify-app.sh" "$APP_PATH" "$VERSION"
shasum -a 256 "$ZIP_PATH" > "$ZIP_PATH.sha256"

echo "$ZIP_PATH"
