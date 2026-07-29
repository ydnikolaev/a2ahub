# A2A Notifications for VS Code

This directory contains the local UI adapter for P49. The extension displays
the canonical A2A level in the status bar and presents newly actionable feed
entries through VS Code's information, warning, and error notification APIs.
It does not contain routing or eligibility rules.

## Install

The supported installation path is the product command:

```text
a2a notifications install --channel vscode
```

The installer owns the version-matched VSIX, the exact VS Code target/profile,
and the machine-scoped absolute `a2a` CLI path. Direct VSIX installs are for
development only and do not receive CLI-managed updates automatically. VS
Code may show its normal publisher-trust prompt; the installer does not bypass
it.

The extension remains inert until the installer has written its private,
version-pinned machine config. The optional
`a2ahub.notifications.cliPath` machine setting may override that absolute
path; workspace/folder values are ignored and the extension never searches
`PATH`.

## Security and process boundary

- `extensionKind: ["ui"]` keeps the Node extension in the local UI host.
- Activation is delayed to `onStartupFinished`.
- Every child uses the verified absolute CLI path, `shell: false`, fixed
  operation argv, bounded output, a deadline, and an owned lifetime.
- Project, lease, entry, consumer, and route identifiers are validated before
  they enter argv. Rendered title/summary text can never become a command,
  path, URL, cwd, Markdown, or HTML.
- Status calls cover every local project root in the private CLI-managed
  config, including when no workspace is open. Open local file workspaces are
  added through VS Code's structural folder URI and deduplicated. The
  extension itself never reads project artifacts.
- In Restricted Mode the same boundary applies: workspace settings and
  content do not select an executable or argv.
- Remote SSH/container/Codespaces windows never execute a remote binary or
  use a remote workspace cwd. They may show the registered local-machine
  project feed with an explicit local-only tooltip. A remote notification
  bridge is not part of P49.

The deterministic consumer ID is a hash of VS Code's machine ID and the
profile-scoped global-storage location. Windows in the same installation and
profile therefore present one CLI-coordinated feed consumer; the CLI remains
the sole owner of leases, acknowledgements, cursors, and cross-window races.

## Behavior

The adapter polls:

```text
a2a notifications status --channel vscode --json
a2a notifications claim --channel vscode --consumer ID --project ID --limit 20 --json
a2a notifications ack --lease TOKEN --entry ID... --json
a2a notifications open ROUTE_TOKEN --json
```

Visible levels require a core-minted aggregate route token. Popup entries
require complete project/space/artifact/event identity and their own route
token. Missing or malformed identity fails closed and leaves the lease
unacknowledged for bounded redelivery. Calling a VS Code notification API
counts as platform acceptance; dismissal is presentation-only. Clicking
**Open A2A** invokes the trusted route broker. The extension never mutates
exchange state.

`quietPresentation` hides the item and pauses claims, preserving unseen feed
entries until presentation is re-enabled. Health output contains operation and
stable error class only—never artifact bodies, CLI stderr, secrets, roots, or
noisy success messages.

## Develop and verify

There are no runtime or development package dependencies:

```text
npm run check
```

`npm run build` validates the manifest's placement/activation invariants and
syntax-checks every shipped/test JavaScript file. `npm test` runs protocol,
sanitization, argv, coordination, acknowledgement, quiet-mode, and child
lifetime unit tests with Node's built-in test runner.

Provider facts were rechecked on 2026-07-29 against the current official
[Extension Host](https://code.visualstudio.com/api/advanced-topics/extension-host),
[Remote Extensions](https://code.visualstudio.com/api/advanced-topics/remote-extensions),
[Extension Manifest](https://code.visualstudio.com/api/references/extension-manifest),
[VS Code API](https://code.visualstudio.com/api/references/vscode-api), and
[Status Bar UX](https://code.visualstudio.com/api/ux-guidelines/status-bar)
documentation. The scoped build/test run recorded beside this source used:

- macOS Darwin 25.1.0 arm64;
- Node.js 26.4.0 and npm 11.17.0;
- VS Code CLI 1.129.0 arm64
  (`125df4672b8a6a34975303c6b0baa124e560a4f7`).

The CLI version inspection is not an Extension Test. Release acceptance still
requires the spec's clean local/multi-root/multi-window, Restricted Mode,
remote, disabled/profile, DND, closed/reopen, and VSIX update matrix.
