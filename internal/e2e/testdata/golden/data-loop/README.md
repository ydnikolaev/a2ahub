# Data exchange loop — golden fixtures

Spec: `docs/features/active/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md`
§6.6. Test: `internal/e2e/data_golden_test.go` (`TestDataGoldenSequence`).

## What this is

One recorded end-to-end sequence for `a2a data pack|deliver`, produced by a
REAL exec of the built `a2a` binary against a real fixture space and a real
fake GitHub host (`hostRig`, `internal/e2e/host_loop_test.go`) — never a
direct construction of `dataCore` or `internal/datapackage`. The test
compares the produced documents against the files in this directory byte
for byte and prints the first differing line on failure, not a bare "not
equal".

The point: a change to any wire shape this loop produces — a renamed field,
a reordered one, a changed digest algorithm, a changed id format — shows up
as a diff in code review, rather than as a surprise the first time someone
runs the loop for real.

## All five files, now present

Spec §6.6 names five documents: the request, the first package, its
failing report, the superseding package, and the passing report. This
directory now has all five: `01-request.json`, `02-package-attempt-1.json`,
`03-report-fail.json`, `04-package-attempt-2.json`, `05-report-pass.json`.
The numbering was never renumbered 01/02/03 — `03-report-fail.json` and
`05-report-pass.json` are exactly the names the two once-missing documents
were reserved for, so landing them on 2026-08-13 was additive, not a rename.
Both are produced the same way the other three always were: a real `a2a
data verify --record` exec, read back from the committed
`<system>/data/<DP-id>/report.json` the write actually landed — never a
report assembled in memory.

**Resolved 2026-08-04; the two fixtures were simply not written yet until
2026-08-13.** The paragraph below is the defect AS FOUND and is kept because
it is the record of what the two-party proof caught, but its present tense
stopped being true on the day it was written: commit `9f02f261` — the same
commit that added this directory — taught `splitDataContractReference` to
cut on `#` first and CHECK the digest instead of refusing it. Re-verified
2026-08-13: all seven `TestDataLoop*` tests pass, including the three this
once blocked, and `TestDataGoldenSequence` now drives its own fail →
supersede → pass sequence through the real `a2a data verify --record`
exactly as an operator would.

AS FOUND: **they are missing because `a2a data verify` cannot resolve any
real package's contract — a confirmed product defect, not a gap in this
test.** `cmd/a2a/data_wiring.go`'s `dataCore.verify` calls
`resolveContractSchemas(ctx, document.Contract)`, passing the manifest's
own `contract` field — which data-package/v1's schema REQUIRES to carry a
`#sha256:<digest>` suffix (`$defs/pinnedContractRef`, and spec §T2.1 says
the same: "the exact version verified against"). But
`space.ResolveDataContractSchemas`'s own ref parser
(`splitDataContractReference`, `internal/space/data_resolve.go`) accepts
only `<XC-id>@<version>` with NO digest suffix — it cuts once on `@` and
requires the remainder to be a bare canonical version. Every real manifest
therefore makes `data verify` refuse with:

```
space: contract reference must be an exact XC id and canonical version: "XC-...@1.0.0#sha256:..."
```

There is no arrangement-level workaround for this the way there was for
the provenance defect below: patching the manifest's `contract` field to
strip the digest before calling `verify` would make `deliver` refuse
instead (readStagedPackage's own schema validation requires the digest
suffix). The two refusals are mutually exclusive — no input satisfies
both `deliver` and `verify` today. This was found BY this golden fixture
attempting the real five-step sequence the spec describes; see the
implementation report's deviations for the full trace. It also explains
why nothing in the shipped test suite caught it: every existing test that
exercises `dataCore.verify` (`cmd/a2a/data_wiring_test.go`) injects a stub
`resolveContractSchemas` closure that accepts any string, so the real
parser's digest-suffix rejection is never exercised end to end.

## Regenerating

```
UPDATE_GOLDEN=1 go test ./internal/e2e/... -run TestDataGoldenSequence -count=1
```

This overwrites all five files in this directory with the current run's
own masked output. **Always review the diff before committing** — that
review IS the point of this fixture existing. Do not hand-edit these files
directly; if a value needs to change, change it by running the loop and
regenerating, so the fixture always reflects something the product
actually produced.

## What is masked, and why

Every value in these fixtures must be deterministic across runs, or the
comparison is worthless. Two kinds of value cannot be pinned from outside
the product (there is no `--clock`/`--entropy` flag or `A2A_*` env override
for either), so they are masked rather than left to vary:

- **Package ids** (`DP-axon-<YYYYMMDD>-<rand4>`) — minted from
  `crypto/rand` entropy and the wall clock's own date. Each occurrence is
  replaced by an exact-string substitution (never a blind regex) with a
  fixed, unmistakably-redacted placeholder: `DP-axon-REDACTED-0001` for the
  first attempt, `DP-axon-REDACTED-0002` for the second (which also appears
  as attempt 2's `supersedes` value, keeping the two fixtures
  cross-referentially consistent). Before substitution, each id is
  round-tripped through `datapackage.ParsePackageID` — the real product
  parser — so a regression in the ID FORMAT ITSELF (not just its random
  suffix) fails the test loudly instead of silently vanishing into a
  placeholder.
- **Report ids** (`VR-axon-<YYYYMMDD>-<rand4>`) — verification-report/v1's
  own id, `datapackage.MintReportIDAt`, mirroring the package-id treatment
  exactly: exact-string substitution to `VR-axon-REDACTED-0001` /
  `VR-axon-REDACTED-0002`, round-tripped through `datapackage.ParseReportID`
  first. (Its random suffix is itself derived deterministically from the
  package id and pinned contract ref — see `dataReportEntropy`,
  `cmd/a2a/data_wiring.go` — but the date component is still the wall
  clock's, so it still needs masking the same way.)
- **Timestamps** (`created_at`, `expires_at`, the request envelope's own
  `created`, and `started_at`/`finished_at` on both report fixtures) that
  are wall-clock-derived are replaced with the literal string
  `REDACTED-TIMESTAMP` via a generic RFC 3339 pattern match. Spec §T2.1
  itself says a producer timestamp is "never a protocol ordering key", so
  nothing about its exact value is a wire property this golden should
  assert. (The request envelope's `created` field happens to be fully
  pinned by this test's OWN literal draft content rather than produced by
  the binary — it is masked anyway, defensively, in case a future change
  makes `a2a submit` stamp or rewrite it.)
- **`observed.duration_ms`** on both report fixtures — the consumer's own
  measured elapsed time between `datapackage.Verify`'s two clock reads. Not
  a protocol property either (verify.go's own doc comment: it is one of
  only three values a re-run over the same package and contract may
  legitimately differ on), and not reliably zero, so it is replaced with
  the literal `"duration_ms":0` via a pattern match on the field itself —
  a numeric field's mask must itself be a valid JSON number, unlike the
  string-interior timestamp mask above.

  The asymmetry is deliberate and worth one sentence, because the phase audit
  read it as a contradiction: a masked STRING stays a string and the document
  stays decodable, while a masked NUMBER replaced by a string would change the
  field's type. What the timestamp mask does give up is schema VALIDITY —
  `REDACTED-TIMESTAMP` is not a `format: date-time` — and nothing currently
  validates these fixtures against verification-report/v1 after masking, so
  that is unobserved rather than accepted. If a future gate does validate
  them, the timestamp mask is what it will trip on first.

## What is deliberately NOT masked

This list is the golden's actual value — everything on it is a real
product property this fixture protects, and widening the mask list above
to cover any of these would quietly stop protecting it:

- Every digest: `aggregate_digest`, each entry's `digest`, and the pinned
  contract ref's own `#sha256:...` suffix. All three are computed
  deterministically from fixed input bytes this test controls (the
  contract's schema/fixture content, and each attempt's own payload
  content), so a change in the digest algorithm, its input set, or its
  canonicalization shows up here.
- `contract` (the full pinned ref, id + version + digest) — proves the
  contract this delivery is pinned against resolves to the same identity
  run over run.
- `thread`, `locator`, `classification`, `data_profile`, `format`,
  `transport_driver`, `mode` — every closed-enum and structural field.
- `entries[].path`, `.role`, `.media_type`, `.conforms_to`, `.size_bytes`,
  `.record_count` — the whole carried-set shape.
- `attempt` and `supersedes` — the supersession chain's own bookkeeping.
- `size_bytes` / `record_count` at the manifest's top level.

## A second confirmed defect, visible directly in these fixtures

`02-package-attempt-1.json` and `04-package-attempt-2.json` both show:

```json
"provenance": {
  "origin_system": "",
  "extracted_at": ""
}
```

**Resolved 2026-08-04.** `dataCore.pack` did not populate `provenance` at
all, and `data-package/v1` requires `provenance.origin_system` non-empty —
so every real manifest failed `a2a data deliver`'s own schema check and no
delivery could ever have succeeded. The two-party e2e found it; the fixtures
above now carry real values. `origin_system` is the packing system and
`extracted_at` is its clock; `cursor` stays empty rather than invented,
because a fabricated watermark is worse than an absent one.
