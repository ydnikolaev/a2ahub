# macOS provider facts

Checked 2026-07-29 on macOS 26.1 (25B78), Xcode 26.3 (17C529), Swift 6.2.4.
These are adapter inputs; they are not inferred from the provider name.

| Surface | Current canonical reference | Adapter consequence |
|---|---|---|
| Notification permission | <https://developer.apple.com/documentation/usernotifications/asking-permission-to-use-notifications> | Request only after explicit `--install`; status reads settings; denied/not-determined polling never claims. |
| Agent app | <https://developer.apple.com/documentation/bundleresources/information-property-list/lsuielement> | The shipped artifact is an `.app` with `LSUIElement=true`, not a script or LaunchAgent. |
| Login item | <https://developer.apple.com/documentation/servicemanagement/smappservice/mainapp> | macOS floor is 13; explicit install/uninstall owns `SMAppService.mainApp`; normal polling never registers. |
| Distribution | <https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution> | Release packaging requires Developer ID signing, hardened runtime, timestamp, notarization, stapling, validation, and Gatekeeper assessment. |

Apple's documentation pages require JavaScript in a basic HTTP reader; the
listed SDK symbols were additionally compiled against the Xcode build above.
Runtime notification/login consent and release signing/notarization still
require an installed bundle, interactive user session, and release
credentials respectively.
