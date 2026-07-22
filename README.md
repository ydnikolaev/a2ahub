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

**`go install`** (builds from source):

```sh
go install github.com/ydnikolaev/a2ahub/cmd/a2a@latest
```

**Manual download**: grab `a2a_<version>_<os>_<arch>.tar.gz` (or `.zip` on
Windows) from the [releases page](https://github.com/ydnikolaev/a2ahub/releases/latest),
unpack, and put `a2a` on your `PATH`.

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

## Verifying a release

Every release publishes a `SHA256SUMS` file, a per-asset cosign bundle, and
SLSA build provenance. See [SECURITY.md](SECURITY.md) for the `gh
attestation verify` and `cosign verify-blob` commands.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
