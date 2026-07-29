# macOS provider facts

Checked 2026-07-29 on macOS 26.1 (25B78), Xcode 26.3 (17C529), Swift 6.2.4.
These are adapter inputs; they are not inferred from the provider name.

| Surface | Current canonical reference | Adapter consequence |
|---|---|---|
| Notification permission | <https://developer.apple.com/documentation/usernotifications/asking-permission-to-use-notifications> | Request only after explicit `--install`; status reads settings; denied/not-determined polling never claims. |
| Agent app | <https://developer.apple.com/documentation/bundleresources/information-property-list/lsuielement> | The shipped artifact is an `.app` with `LSUIElement=true`, not a script or LaunchAgent. |
| Login item | <https://developer.apple.com/documentation/servicemanagement/smappservice/mainapp> | macOS floor is 13; explicit install/uninstall owns `SMAppService.mainApp`; normal polling never registers. |
| Developer ID distribution | <https://developer.apple.com/support/developer-id/> | Developer ID and notarization require Apple Developer Program membership. The `developer-id` packaging mode keeps this protected path but is not the current public default. |
| Unidentified developer | <https://support.apple.com/102445> | The current `adhoc` release mode requires an explicit user Gatekeeper override when macOS quarantines the app. The product must never clear quarantine or weaken Gatekeeper automatically. |
| GitHub-hosted build image | <https://github.com/actions/runner-images/blob/main/images/macos/macos-15-arm64-Readme.md> | The Swift package declares tools 6.0, so CI and release use `macos-15` (Xcode 16.x/Swift 6) and print `swift --version`; `macos-14` selected Swift 5.10 and cannot parse the package. |

Apple's documentation pages require JavaScript in a basic HTTP reader; the
listed SDK symbols were additionally compiled against the Xcode build above.
Runtime notification/login consent and a Gatekeeper override still require an
installed bundle and interactive user session. Developer ID/notarization
remains available only when the protected release credentials are provisioned.
