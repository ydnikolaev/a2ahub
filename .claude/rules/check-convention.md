# Check convention — a2ahub

Project value file the `/check` skill (mate-synced, neutral) defers to: the
path→stack map, the gate commands, and the lanes.

## Path → stack map

| Paths touched | Stack | Gate (run from repo root) |
|---|---|---|
| `**/*.go`, `go.mod`, `go.sum` | go | `make check` |
| `docs/**`, `schemas/**`, `.claude/**`, `.agents/**`, `scripts/**`, `Makefile` | repo | `make check-validators` |
| both | both | `make check` (it includes the repo gates) |

Nothing touched and no argument → ask which stack; never run every gate blindly.

## Lanes (make-ABI)

- **`make check` — the ceiling.** Repo gates (feature-lint, epic-drift) + the
  Go gates (`gofmt -l`, `go vet ./...`, `go test ./... -race -count=1`) when
  `go.mod` exists. This is what "done" means for any session that touched `.go`.
- **`make check-validators` — the inner loop.** Static/doc gates only, no
  tests. A code change that ran only this lane is NOT gated.
- **`make harness-check`** — the gates' own `--teeth` self-tests; run when the
  gate scripts themselves change.

## Concurrency

- Never two gates in parallel; the gate is lead-side, after any fan-out
  returns. Fan-out sub-agents never run repo-wide gates (sole-writer rule) —
  they scope self-verify to their own package.

## Coverage

- Go coverage floor: 70% for `internal/...` (SSOT: [go-conventions.md](go-conventions.md)).
  Raising is a normal commit; lowering requires an explicit user-approved
  decision.
