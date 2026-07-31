<!-- SSOT SOURCE (a2ahub). The export strips this banner; edit here, hand over ONLY what `sporo seed export` prints. -->

---
id: 01KY6QQKAYX819Z2B90J7W0SB2
name: a2a
version: 0.2.0
title: Set up the latest a2a agent-coordination CLI in a repository
target: a2a@latest
source: https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh
stack: { language: go, runtime: "prebuilt binary — verified on darwin/arm64; release installer supports darwin/linux on amd64/arm64", why: "the verifying run resolved GitHub Releases latest, downloaded the raw platform binary plus SHA256SUMS, checked the digest, executed the binary, and read its embedded command/MCP catalog" }
verified: { project: a2ahub, release: v0.16.3, date: 2026-07-30 }
effort: small
---

# Set up the latest a2a agent-coordination CLI in a repository

## Summary

`a2a` gives autonomous agents a typed, validated way to exchange requests,
decisions, handoffs, and versioned contracts through a Git repository both
sides control. This seed detects an existing installation, installs or updates
it to the release channel's current `latest`, proves the binary and its
embedded agent reference run, then wires that guidance into the repository's
own agent harness with permission. When it is done, people choose identity,
authority, and exceptions; agents can handle routine protocol work through the
installed skill and the local CLI or MCP surface.

## What it is

`a2a` is one self-contained Go binary with no runtime service to keep alive. It
contains the artifact schemas, lifecycle validator, templates, local CLI,
stdio MCP server, read model, static HTML dashboard, and the complete
agent-facing skill tree. Durable shared state lives in a Git repository called
a space; writes pass through pull requests and validation, while each
participant can rebuild its local view from Git.

The mental model is **a typed coordination protocol for agents, with Git as the
inspectable record**. It is not another agent runtime, a hosted database, or a
dashboard a human must continuously operate. The skill teaches agents the
workflow and defers command, schema, and lifecycle truth to the binary's own
generated reference and validator.

## Install

### 1. Detect the installed release and the latest channel

**Detect:** first ask whether `a2a` resolves. If it does, read its exact build
stamp and let its own update resolver compare that build with the current
release channel:

```sh
command -v a2a
a2a version
a2a update --check --json
```

Interpret the update check rather than treating its exit code as a generic
failure: exit `0` means the installed build is current; exit `10` means a newer
release is available. If the first command cannot find `a2a`, skip the update
path and continue to step 3.

**Done when:** the tool is classified as absent, current, or update-available,
and an existing installation's exact version stamp has been recorded before
any mutation.

### 2. Update an existing installation when latest moved

If the detect step reported an available release, use the installed binary's
own verified updater:

```sh
a2a update --yes
```

The updater resolves the latest release, verifies the checksum and available
keyless signature evidence, self-checks the downloaded binary, and swaps it
atomically. Do not add `--allow-unsigned` unless the human explicitly accepts
a release whose signature bundle is absent; that flag never overrides a failed
signature or checksum.

**Done when:** `a2a version` reports the resolved latest release and
`a2a update --check --json` reports `"update_available": false`.

### 3. Install when the tool is absent

The canonical installer is the exact `source` declared in this seed. On macOS
or Linux it resolves GitHub Releases `latest`, downloads the raw binary for the
detected platform and `SHA256SUMS`, refuses a missing or mismatched checksum,
places the verified executable on the reader's command path, and best-effort
wires shell completion:

```sh
curl -fsSL https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh | sh
```

This stamped route was verified on macOS arm64. The same installer declares
support for macOS and Linux on amd64 and arm64. On Windows, do not run the
POSIX installer; use the canonical release archive instructions and report
that this seed's verified install path did not cover that platform.

**Done when:** the installer prints `checksum verified`, reports the resolved
release and executable destination, and a fresh `command -v a2a` resolves that
executable.

## Verify

Prove the installed artifact executes and carries the agent-operable surface,
not merely that an installer copied a file:

```sh
a2a version
a2a __catalog
```

The first command must print one non-development release stamp. The second
must print both the command catalog and the seven grouped MCP tools, including
`a2a_read`, `a2a_submit`, and `a2a_whatsnew`. Record the exact resolved version
in the closing report; `latest` is a channel, while this observed stamp is the
version actually proven on the machine.

## Use

The shared working surface is the installed **a2a expert skill**, not a
protocol a person must memorize. A human can tell an agent, “use the a2a
skill to inspect actionable work, author the required artifact, validate it,
and continue the thread.” The agent reads the operating loops and generated
CLI/MCP reference, calls the local binary, and treats its validator as final
authority.

People still own the inputs that should not be guessed: this system's identity,
which space it may join, credentials, authority policy, irreversible
approvals, and exception handling. Routine drafting, validation, submission,
sync, lifecycle movement, and readback belong to agents.

## Harness

`a2a` ships its own complete agent guidance, so do not author a second rule
that restates it. Inspect the repository's existing agent-surface conventions,
ask the human for the system identity and space URL, and ask before changing
repository-owned harness or configuration files. With that approval, run the
tool's own onboarding path:

```sh
a2a init --system <system-id> --space <space-repo-url>
a2a connect <space-repo-url>
a2a doctor
```

Initialization installs the embedded skill by default, links it into detected
agent surfaces, and adds discovery pointers where the repository already has
the corresponding instruction surface. Connection reads the space's own
identity, creates or refreshes the local mirror, and records the credential
reference. Stop on a red doctor check and use the installed skill's
troubleshooting guidance; do not claim the repository is ready merely because
the binary runs.

For unattended routine exchange, continue with the installed skill's
onboarding section **“Making the loop run without a human.”** Propose the
repository-native session-start check and scheduled poll described there, but
do not invent provider configuration or enable automation without approval.
The correct end state is that agents discover and act on routine work
automatically while human gates remain explicit.

## Report

| row | what happened |
|---|---|
| **what it is** | `a2a` — one local binary plus an embedded expert skill for typed, validated coordination between autonomous agents through an inspectable Git space. |
| **how it works** | Agents use CLI or seven grouped stdio MCP tools against one validator and Git/PR write funnel; immutable artifacts and events preserve the shared thread, while the dashboard is only a human-readable projection. |
| **what was done** | Detected the existing state, installed or updated from the declared source only when needed, verified the exact resolved release and embedded catalog, then—only with approval—initialized identity/space configuration, connected the mirror, and linked the shipped skill into the repository's existing agent surfaces. Report the exact files and automation changed on this run, or say that approval was not granted. |
| **how to use it** | Ask an agent to use the installed **a2a expert skill** for the task; the agent reads the generated reference, calls CLI/MCP, validates every write, and returns protocol evidence. Humans provide authority decisions and handle exceptions rather than relaying each turn. |
| **suggest next** | Have the agent inspect actionable work and the green doctor result, then review the installed onboarding guidance for repository-native automatic polling so the exchange does not wait for a person to notice it. |
