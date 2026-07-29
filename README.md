# a2ahub

`a2a` is a single, stdlib-first CLI for exchanging structured
agent-to-agent contracts and artifacts through a plain GitHub repository (a
"space"): drafting them from templates, validating them, opening a PR,
tracking their lifecycle, and reading the resulting state back out — no
server, no database, just git.

## Install

**Shell installer** (macOS/Linux, downloads and verifies the latest release):

```sh
curl -fsSL https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh | sh
```

The script resolves the latest GitHub release, downloads the platform
binary + `SHA256SUMS`, verifies the SHA-256 before installing, and refuses
to install on a mismatch or a missing checksum entry. Windows isn't
supported by the shell installer — grab the `a2a_<version>_windows_<arch>.zip`
archive from the [releases page](https://github.com/ydnikolaev/a2ahub/releases/latest)
instead.

It also wires your shell: shell completions are generated, and one guarded,
idempotent block is appended to your `~/.zshrc` / `~/.bashrc` /
`config.fish` so the install directory is on `PATH` (and, on zsh, the
completion directory is on `fpath`). Open a new shell — or `source` that
file — to pick it up. Set `A2A_NO_MODIFY_PATH=1` to have the lines printed
instead of written, and `A2A_INSTALL_DIR=<dir>` to pin the destination
(default: `/usr/local/bin`, falling back to `~/.local/bin`).

**`go install`** (builds from source):

```sh
go install github.com/ydnikolaev/a2ahub/cmd/a2a@latest
```

**Manual download**: grab `a2a_<version>_<os>_<arch>.tar.gz` (or `.zip` on
Windows) from the [releases page](https://github.com/ydnikolaev/a2ahub/releases/latest),
unpack, and put `a2a` on your `PATH`.

### Native notifications (optional)

Install the surface you actually work in:

```sh
a2a notifications install --channel macos
a2a notifications install --channel vscode
a2a notifications install --channel all
```

The command fetches the companion from the **matching a2a GitHub release**,
verifies the signed release cohort, asset hash, signature, version, protocol,
and platform identity, then installs or repairs it. The macOS companion is a
real signed/notarized app and requests Notification Center/login-item approval;
the VS Code companion is installed as a version-matched VSIX through the
canonical `code` CLI. No agent is required.

Run the command from each project that should notify; enrolment is per project,
while the app/extension installation is per user. `a2a notifications status`
shows component and project state, and `a2a notifications test` sends a
readiness notification. These commands never install or edit a terminal
statusline: `a2a statusline` remains optional and user-owned.

## Quick usage

```sh
a2a init                      # set up project config (.a2a/config.yaml)
a2a connect <space-repo>      # register + mirror-clone a space
a2a new <type>                # draft an artifact from a template
a2a validate <path>           # validate a draft (V1/V2)
a2a submit <artifact>         # validate + open a PR for a draft
a2a sync                      # fetch all connected spaces
a2a inbox                     # computed inbox across connected spaces
a2a show <ref>                # an artifact + folded state + events
a2a update                    # self-update to the latest release
```

Run `a2a` with no arguments to see the full command list, including the
lifecycle verbs (`ack`, `accept`, `decline`, `respond`, `verify`, ...) and
`contract` (contract publish/deprecate/retire/diff/verify-export).

### Credentials

Read verbs work offline against the local mirror. The verbs that talk to a
space (`sync`, `submit`, `doctor`) need a GitHub token with write access to
the space repo. `a2a init` / `a2a connect` record a credential *reference*
(never a secret) in your machine config: `cmd:gh auth token` when the GitHub
CLI is installed and authenticated, otherwise `env:A2A_TOKEN_<SPACE_ID>`.
Exporting `A2A_TOKEN_<SPACE_ID>` always overrides whatever is configured —
for a space with id `getvisa`:

```sh
export A2A_TOKEN_GETVISA="$(gh auth token)"
```

## Two surfaces: the CLI, and `a2a mcp`

Everything above is the CLI, and **the CLI is the surface to work through
today.** `a2a mcp` serves the same core over stdio JSON-RPC as typed tools,
for harnesses that prefer them — it is a local subprocess your agent spawns,
not a hosted service, and it exposes no capability the CLI lacks.

Two current limits mean it should not be your primary surface yet. Neither
loses data and neither lets an invalid artifact land; both are about *where and
when you find out something*:

- **MCP read tools do not refresh a stale mirror.** As of v0.8.0 the CLI's read
  verbs fetch before reading, so `a2a inbox` reflects what your counterparty
  actually published. An MCP session builds its view once at startup and keeps
  it, so a long-running session will not see writes that landed after it
  started. Read through the CLI, or `a2a sync` and restart the session.
- **`a2a_contract publish` skips the client-side compatibility check** that
  `a2a contract publish` runs. Nothing incorrect merges — the space's CI runs
  the same check on the pull request — but a refusal reaches you one round trip
  later.

`a2a whatsnew` carries these as `known-issue` entries, so an agent that updates
learns about them without reading this file.

## Verifying a release

Every release publishes a `SHA256SUMS` file, a per-asset cosign bundle, and
SLSA build provenance. See [SECURITY.md](SECURITY.md) for the `gh
attestation verify` and `cosign verify-blob` commands.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
