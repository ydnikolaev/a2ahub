# Notifications

Use the binary as the source of truth. Read exact syntax from
[reference/commands.md](reference/commands.md).

## Activation preflight

Run once for the current project:

```text
a2a notifications status --json
```

This is a passive read: it must not register the project, consume the human
inbox cursor, launch UI, or run platform subprocess probes. It may spend up to
the bounded update-check deadline refreshing the shared release cache so users
without a terminal statusline still discover a2a updates.

| `offer.state` | Agent action |
|---|---|
| `enabled` | Say nothing unless setup or health is relevant to the request. |
| `ask` | Ask whether to enable notifications. Offer only channels in `available_channels`: macOS, VS Code, or both. Also offer “not now; remind me in N days” and “no; do not remind me for this project.” |
| `snoozed`, `never` | Do not ask again. |
| `unavailable` | Do not ask; mention the missing supported surface only when relevant. |

Act only after the human chooses:

```text
a2a notifications install --channel macos
a2a notifications install --channel vscode
a2a notifications install --channel all
a2a notifications preference --remind-in 7
a2a notifications preference --never
```

Use `--global` with `--never` only when the human explicitly means every a2a
project on this machine. A project choice must remain project-scoped. Reset a
previous choice only on request.

Installation is agent-free: the same commands are intended for a human to run
directly. They install or repair the cohort-verified macOS app or
version-matched VSIX, enrol only the current project in the chosen channels,
and keep other project enrolments intact. The macOS app currently uses an
ad-hoc code signature rather than a paid Developer ID. If the result is
`approval-required`, tell the human to open the installed **A2A Notifier**
once, choose **System Settings → Privacy & Security → Open Anyway**, and repeat
the same install command. Never run `xattr`, clear quarantine, or disable
Gatekeeper for them. Use `status` to diagnose and `test` for a synthetic
readiness notification.

## Statusline boundary

The terminal statusline is separate, optional, and user-owned.
`a2a notifications install` never reads or edits it.

If asked to add terminal status, inspect the existing provider first. Preserve
its bytes, JSON, and exit contract and propose an additive `a2a statusline`
segment. Do not replace a custom statusline and do not modify it without
explicit consent.

## Click and update behavior

macOS banners and VS Code items invoke only an opaque route through
`a2a notifications open`; they never construct a path, URL, or command from
artifact text. The route opens the local a2a HTML view and focuses the item.

Update notifications focus the local What’s New card. An older binary can show
future prose only when the standalone `release-notes/v1` asset from the signed
release cohort was verified and cached. Otherwise the card must show the
target version and a clear version-only fallback. Never claim that the old
binary’s embedded notes describe a future release, and never auto-update
without the consent described in `SKILL.md`.
