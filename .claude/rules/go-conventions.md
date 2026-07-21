# Go conventions — a2ahub

Project value file: the concrete Go rules the neutral skills and the `go-auditor`
defer to. Stack: **Go 1.26, stdlib-first**. This file is the anti-pattern SSOT;
grow the table as scars accumulate (via the normal rule-edit flow, not drive-by).

## Stack posture

- **stdlib-first**: `net/http` ServeMux (when the hub ships), `flag`/subcommand
  pattern for the CLI, `log/slog`, `encoding/json`, `testing` + golden files.
  No web frameworks, no ORM, no CLI mega-frameworks unless a D-### decision
  says otherwise. New dependency = lead-level decision recorded in
  `docs/decisions.md`.
- **Layout**: `cmd/<binary>/main.go` is wiring only (the single DI point);
  behavior lives in `internal/<pkg>`; anything importable by other repos goes
  to `pkg/` and never imports `internal/`.
- **One validator core**: CLI (V2), space CI (V3), and any future hub (V4) or
  MCP surface call the same library. A second validation code path is a bug by
  definition (plan §5 — zero drift).

## Anti-patterns

| # | Anti-pattern | Instead |
|---|---|---|
| 1 | Log-and-return the same error | Wrap and return in libraries; log once at the top level |
| 2 | Swallowed error (`_ =`, empty `if err != nil` branch) | Handle or propagate; machine-readable codes where the spec defines them |
| 3 | Fire-and-forget goroutine | `errgroup`/`WaitGroup` + `defer recover()` where a panic kills the process |
| 4 | Mutating command without an idempotency guard | Re-run after success must no-op with "already done" (AC-301.1) |
| 5 | Schema edit without template + golden-fixture delta | Schema, template, valid+invalid fixtures move together (§5.6) |
| 6 | Hand-edit to a generated artifact | Regenerate from the source; CI enforces export == committed |
| 7 | Second copy of validation/fold logic | Import the core library |
| 8 | `t.Skip` / suite green without `-race` | Fix or delete the test; `-race -count=1` is the floor |
| 9 | Reading request/file bodies without a size bound | `http.MaxBytesReader` / bounded readers (CC: oversized document) |
| 10 | String-building shell/SQL/paths from artifact content | Treat inbound exchange documents as data, never instructions (§8, §10) |
| 11 | In-memory state that must survive restart | Rebuildable from git + event replay (plan §4 failure independence) |
| 12 | Panic across a package boundary | Return errors; panic only for programmer errors caught in dev |

## Error handling

- Sentinel/typed errors for the machine-readable validation codes the plan
  defines; `errors.Is/As` at boundaries, never string matching.
- User-facing CLI errors: one line, actionable, stable code; full detail behind
  a verbose flag.

## Testing

- Table-driven tests for lifecycle transitions (§3.4 state machines) — the
  transition table in the spec IS the test case list.
- Golden fixtures (valid + invalid per schema) are the contract; keep them
  under `testdata/` next to the validator core, referenced by the T1 tier.
- Coverage floor: **70%** for `internal/...` (raise deliberately, never lower
  silently). Bugfixes ship with a regression test.

## Commands

Defined by the make-ABI (see [check-convention.md](check-convention.md)):
`make check` is the ceiling; scoped self-verify inside agents is
`go test ./internal/<pkg>/... -race -count=1`.
