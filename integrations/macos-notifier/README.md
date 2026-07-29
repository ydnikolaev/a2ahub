# A2A Notifier for macOS

`A2A Notifier` is the native, per-user presentation adapter for P49. It is a
real `LSUIElement` application bundle (`io.a2ahub.notifier`) with no Dock or
menu-bar item and a macOS 13 deployment floor.

The app only:

- reads its machine-local registration written by `a2a notifications install`;
- invokes one exact absolute `a2a` executable with controlled
  `notifications status|claim|ack|open` arguments;
- presents accepted feed entries through `UNUserNotificationCenter`;
- acknowledges only entries accepted by the platform API;
- routes a click's opaque token through
  `a2a notifications open <route-token> --json`.

It contains no Git, network, project-domain, HTML, URL, or update logic.

## Configuration contract

The CLI atomically writes this user-only file:

`~/Library/Application Support/io.a2ahub.notifier/config.json`

```json
{
  "schema_version": 1,
  "cli_path": "/absolute/path/to/a2a",
  "cli_version": "0.1.0",
  "consumer_id": "macos:01J...",
  "poll_interval_seconds": 60,
  "projects": [
    {"id": "01J...", "root": "/absolute/canonical/project"}
  ]
}
```

The companion rejects an unsupported schema, relative CLI/project paths,
control characters, duplicate project IDs, oversized configuration/output,
and a CLI version-stamp mismatch. `root` is retained for diagnosis only; it is
never used as a process working directory or command argument. Claims use the
machine registration ID.

The CLI JSON boundary is additive-field tolerant and fail-closed on a missing
lease token, entry ID, route token, or on a non-quiet status level without its
aggregate `route_token`.

## Build and verify locally

No signing secret is needed for unit tests or a structural app build:

```sh
./scripts/test.sh
./scripts/build-app.sh
./scripts/verify-app.sh "dist/A2A Notifier.app" "$(cat VERSION)"
```

The build script produces `dist/A2A Notifier.app`. A development build proves
the bundle shape and compilation only. It does **not** prove Notification
Center authorization, login-item approval, Developer ID identity,
notarization, Gatekeeper acceptance, or delivery under Focus/lock-screen
policy. The default release build uses SwiftPM's `--triple` and separate
scratch paths to compile `arm64` and `x86_64`, combines them into one universal
executable, and verifies both slices with `lipo`. For a faster host-native
development bundle, run `CONFIGURATION=debug ./scripts/build-app.sh`; unit
tests remain host-native through `./scripts/test.sh`.

The CLI installer places the verified release bundle at
`~/Applications/A2A Notifier.app`, writes the configuration with mode `0600`,
invokes the embedded executable with `--install`, then launches the installed
application after the command succeeds. That explicit action requests
notification authorization and registers `SMAppService.mainApp`.
Normal launches never call `register()`, so polling cannot re-enable a
user-disabled login item. Uninstall invokes `--unregister` before removing the
managed bundle. No LaunchAgent plist is generated.

Supported companion diagnostics are `--status`, `--test`, `--install`,
`--unregister`, and `--run`; each accepts an optional `--config <absolute
path>` for CLI-owned diagnostics. The CLI creates the synthetic test entry in
the canonical feed before invoking companion `--test`; the companion performs
one normal claim → platform acceptance → ack cycle. Permission denial names
the Notifications System Settings route and never claims or acknowledges feed
entries.

## Release contract

`scripts/package-release.sh` intentionally refuses to run without all three
release inputs:

- `SIGNING_IDENTITY`: Developer ID Application certificate;
- `SIGNING_TEAM_ID`: the team ID pinned by the signed a2a release cohort;
- `NOTARY_PROFILE`: a `notarytool` keychain profile.

It builds, signs with secure timestamp and hardened runtime, verifies the
actual team ID, submits the zip for notarization, staples and validates the
ticket, asks Gatekeeper to assess the app, recreates the zip with the stapled
ticket, and writes a SHA-256 file. Cohort assembly must bind that checksum,
bundle/team identity, product version, and protocol range together with the
CLI and VSIX. This directory has no credentials and makes no notarization
claim without those checks succeeding.

## Provider facts checked

Implementation was built and tested on macOS 26.1 (25B78), Xcode 26.3
(17C529), and Swift 6.2.4. The adapter follows Apple's current documented
surfaces for notification authorization, `LSUIElement`, `SMAppService.mainApp`,
and outside-App-Store notarization. The provider references are recorded in
`PROVIDER_FACTS.md`.
