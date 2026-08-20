#!/usr/bin/env bash
# check-notify-secrets.sh — token hygiene at rest (space-notify-2026-08 P7,
# spec 07 §T5).
#
# Owns exactly two invariants:
#   1. No credential shape known to `internal/sensitive` — including the
#      Telegram bot-token shape P1 adds there — appears anywhere in the
#      TRACKED tree, fixtures and goldens included, except at a path this
#      gate's own registry names with a reason (below).
#   2. The Telegram shape's regex SOURCE exists in exactly one place: a
#      second copy of it outside internal/sensitive fails the gate.
#
# ASKS THE BINARY, carries no regex of its own. The shape set comes from
# `a2a __catalog --sensitive-shapes --json` (P1, space-notify-2026-08 §1) —
# never from a list hand-maintained in this file. If that projection is
# missing, non-executable, or parses to zero usable shapes, this gate
# REFUSES (fails loudly, exit 1) rather than falling back to an inlined
# pattern: a gate carrying its own copy of the thing it checks is the
# drift it exists to catch. `A2A_VERIFY_BINARY` selects the shared binary
# (the outer verify.sh runner exports it); a direct invocation falls back
# to `go run ./cmd/a2a`, same convention as check-view-vocabulary.sh.
#
# THE FALSE-POSITIVE SURFACE, measured, not assumed. Naively grepping the
# whole tracked tree for `internal/sensitive`'s own (deliberately
# entropy-free) shapes matches far more than the shape's own tests: every
# Go test in this repo that plants a realistic-looking credential fixture
# to prove ITS OWN redaction/policy/parity logic works — coordinator
# session-normalization, work-command failure redaction, MCP hostile-value
# parity, V3's rule-4 manifest-policy test, P6's notify-setup URL-builder
# sentinel, and more — legitimately contains a shape-matching literal. None
# of these is a live credential; gitleaks (`.gitleaks.toml`, checked at
# HEAD) does not flag most of them either, because its own rules carry
# entropy/context checks these simple shapes intentionally omit for
# REDACTION (favouring false positives over a missed real token at
# runtime) — a property that is right at runtime and wrong for a blanket
# static at-rest scan.
#
# SCOPING CHOICE: an explicit, per-file EXEMPT registry below, one entry
# per file with its own one-line reason — never a directory-wide or
# `*_test.go`-wide carve-out. `.gitleaks.toml`'s own comment states why:
# "each is listed separately so a future one cannot hide behind a broad
# rule." A blanket test-file exemption would defeat the invariant (a real
# token pasted into a test file must still red); this list is reviewable
# and grows by one line per new redaction/policy test, which is the cost
# of the invariant actually holding, not a tax on it.
#
# KNOWN DEBT (reported, not built here — outside this phase's footprint):
# a self-registering in-source marker (e.g. a `// sensitive-fixture:
# <reason>` comment this gate requires next to any credential-shaped
# literal) would not need a central list at all and cannot drift the way
# a list can. That needs edits across internal/** and cmd/a2a/**, which
# this phase's allowlist does not include — see this phase's own report
# for the finding.
#
# Invariant 1 overlaps gitleaks deliberately (gitleaks scans history for
# known provider shapes; this asserts the property on the CURRENT tree
# with a rule this repo owns and can extend — spec 07 §T5).
#
# Usage: bash scripts/check-notify-secrets.sh
#        bash scripts/check-notify-secrets.sh --teeth

# lane-inputs: ALWAYS
# lane-reason: invariant 1 walks the entire tracked tree for a credential
#   shape; no path glob narrows what it must re-check.
# lane-claims:
#   internal/sensitive/**
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   self-locates the shared helper through command substitution; every other
#   unresolved construct below (the `--teeth` harness's stub binary and
#   fixture roots, `tracked_files`'s `find "$root"` fallback for a non-git
#   fixture) reads a `mktemp -d` scratch path, never a repo path.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# ── the exemption registry (invariant 1) ────────────────────────────────
# Path (relative to the scanned root) | one-line reason. Matched by exact
# string or a `case`-style glob (`*` matches anything, including `/`).
# Four entries mirror `.gitleaks.toml`'s own already-audited allowlist
# exactly (its own comments are the reason, repeated here so this gate does
# not require reading that file to understand itself); the rest are new,
# found by actually running the scan against HEAD (see header).
#
# AND "THE REST ARE NEW" WAS A DEFECT, NOT A NOTE — 2026-08-20. Teaching this
# gate a fabricated fixture while leaving `.gitleaks.toml` unaware left two
# scanners over one corpus disagreeing, and the uninformed one is the one CI
# runs. `internal/spacenotify/redact_test.go` sat exempt here and unlisted
# there from 2026-08-18; the private main had not been pushed since 2026-08-13,
# so the first push after that failed the gitleaks job and the finding was
# chased as a possible leak before it was recognised as this repo's own test
# input. Adding a path here now means adding it there in the same commit.
EXEMPT_PATHS=(
  "internal/sensitive/matcher_test.go"
  "internal/provenance/evidence_test.go"
  "docs/runbooks/publish-to-public.sh"
  "schemas/fixtures/secret-corpus/*"
  "schemas/feedback/v1/fixtures/invalid/planted-secret*.yaml"
  "internal/operational/project_test.go"
  "internal/spacenotify/redact_test.go"
  "internal/spacenotify/telegram_test.go"
  "internal/validate/eventreceipt_test.go"
  "internal/validate/registry_test.go"
  "internal/validate/manifest_policy_test.go"
  "internal/cli/cmd_validate_ci_test.go"
  "cmd/a2a/mcp_equivalence_test.go"
  "internal/cli/cmd_work_test.go"
  "internal/mcp/tools_work_test.go"
  "internal/workreport/coordinator_test.go"
  "internal/cli/cmd_notify_setup_test.go"
)
EXEMPT_REASONS=(
  "the shape set's own test fixtures (matcher_test.go); realistic-looking samples by design"
  "gitleaks-allowlisted: a pre-v0.19 provenance fixture, inert, already redacted from published history"
  "gitleaks-allowlisted: the publisher's own redaction pattern — the string that exists to be removed"
  "gitleaks-allowlisted: the boundary secret-scanner's own purpose-built positive/negative corpus (see its README)"
  "gitleaks-allowlisted: P25 feedback secret-scan fixtures (FB-006) — fabricated tokens the validator's own tests assert it blocks"
  "TestCredentialRedactionCoversAssignmentsAndAuthorizationHeaders / TestPublicSessionHashesCanonicalCredentialShapes — fabricated fixtures proving operational redaction"
  "TestBoundAndRedact_CredentialShapeIsGone — fabricated fixture the test asserts gets removed"
  "TestClient_Send_TokenNeverInErrorText* — a synthetic bot token, planted to prove it never reaches an error string"
  "TestValidateEventCredentialShapedSessionsUsePOL001 — fabricated fixture proving POL-001 classification"
  "TestRegistryClosure's POL-001 scanForSecrets fixture — fabricated"
  "TestValidateManifestPolicyRule4SecretShape — fabricated Telegram-shaped fixture proving rule 4 rejects it"
  "validate --ci artifact-scan fixture proving a planted token is caught"
  "TestIT10HostileValuesRemainDataAcrossCLIAndMCP — fabricated hostile-value parity fixture"
  "TestWorkCommandRequiresPairedOwnershipAndRedactsSelectionFailure — fabricated session fixture"
  "TestWorkToolRequiresPairedOwnershipAndRedactsSelectionFailure — MCP-side mirror of the same fabricated fixture"
  "TestCoordinatorNormalizesSuppliedSessionsBeforeEveryDownstreamBoundary — fabricated session fixture"
  "notifySentinelToken — P6's deliberately Telegram-shaped sentinel, testing the URL builder never mis-encodes it"
)

# The doc that legitimately QUOTES the shipped Telegram regex source in
# prose (explaining the deviation from spec 01's literal pattern) — not a
# second implementation, so invariant 2 exempts it by name.
TELEGRAM_DOC_EXEMPT="docs/features/archive/space-notify-2026-08/specs/01-route-schema.md"

is_exempt() { # $1 = path relative to the scanned root
  local path="$1" pattern
  for pattern in "${EXEMPT_PATHS[@]}"; do
    # Intentionally unquoted: EXEMPT_PATHS entries are glob patterns (a
    # trailing `*` covers the two fixture-corpus directories), not literal
    # strings.
    # shellcheck disable=SC2254
    case "$path" in
      $pattern) return 0 ;;
    esac
  done
  return 1
}

# describe_exemptions prints the registry (path -> reason), one line per
# entry — both documentation for whoever reads a gate run and the thing
# that makes EXEMPT_REASONS a used array rather than a second, unread copy
# of the same information the array indices already carry.
describe_exemptions() {
  local i
  for i in "${!EXEMPT_PATHS[@]}"; do
    echo "notify-secrets: exempt — ${EXEMPT_PATHS[$i]} (${EXEMPT_REASONS[$i]})"
  done
}

# tracked_files prints one relative path per line. Production scans the
# TRACKED set via `git ls-files` (never a filesystem walk — `.gitleaks.toml`
# names this repo's own recurring mistake of judging `.a2a/`'s live cache as
# if it were source). A --teeth fixture root carries no `.git`, so it falls
# back to `find`, exercising the same scan/exempt/report logic over a small,
# self-contained tree instead of the real one.
tracked_files() { # $1 = root
  local root="$1"
  # ASK GIT, never test for a `.git` DIRECTORY. In a git worktree `.git` is a
  # FILE holding a gitdir pointer, so a `-d` test says "not a repo" about a
  # tree that plainly is one — and this function then silently switched to the
  # find-everything fallback meant for a non-git root (an unpacked tarball),
  # walking node_modules, build output and git internals. Found 2026-08-20 by
  # running `make check` in a worktree to see what a RELEASE would actually
  # contain: the scan blew past its argument list and the gate refused with
  # grep exit 126, which at least failed loudly. The quieter half is worse —
  # a scanner that changes what it scans depending on how the tree was checked
  # out, without saying so.
  if ( cd "$root" 2>/dev/null && git rev-parse --is-inside-work-tree >/dev/null 2>&1 ); then
    ( cd "$root" && git ls-files )
  else
    find "$root" -type f | sed "s|^${root}/||"
  fi
}

# sensitive_shapes_json asks the binary. Failure returns 1 — the caller
# refuses rather than falling back to an inlined regex.
sensitive_shapes_json() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "notify-secrets: A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY" >&2
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog --sensitive-shapes --json
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a __catalog --sensitive-shapes --json )
}

# shape_pairs parses the projection's JSON into "name|pattern" lines, one
# per shape, with the pattern's JSON string-escaping undone (the projection
# encodes a literal backslash as two — `\\b` on the wire is the one-
# backslash regex source `\b`). No jq dependency (not guaranteed present in
# `make check`, same reason check-view-vocabulary.sh gives).
shape_pairs() { # $1 = json
  printf '%s\n' "$1" | awk '
    /"name"[[:space:]]*:/ {
      name=$0
      sub(/^.*"name"[[:space:]]*:[[:space:]]*"/, "", name)
      sub(/".*$/, "", name)
      next
    }
    /"pattern"[[:space:]]*:/ {
      pat=$0
      sub(/^.*"pattern"[[:space:]]*:[[:space:]]*"/, "", pat)
      sub(/",?[[:space:]]*$/, "", pat)
      print name "|" pat
    }
  ' | while IFS='|' read -r name pattern; do
    [ -z "$name" ] && continue
    printf '%s|%s\n' "$name" "$(printf '%s' "$pattern" | sed 's/\\\\/\\/g')"
  done
}

# scan_secrets is invariant 1: no shape from $2 ("name|pattern" lines,
# already unescaped) matches any non-exempt tracked file under $1.
#
# A missing/unreadable tree and a rejected regex are both REFUSED here, not
# silently treated as "nothing found": `|| true` around the grep call would
# make grep's exit 2 (pattern rejected by the ERE engine — a future shape
# using a construct grep -E cannot compile) look identical to exit 1 (no
# match), and that shape would go unpoliced with a green gate. Same failure
# class as a presence-skip; this is the thing spec 07 names by that word.
scan_secrets() { # $1 = root, $2 = shapes
  local root="$1" shapes="$2" files prefixed name pattern hits rc line file rel
  mapfile -t files < <(tracked_files "$root")
  if [ "${#files[@]}" -eq 0 ]; then
    gate_fail "notify-secrets: tracked_files returned zero files under $root — refusing rather than scanning nothing and reporting green"
    return
  fi
  prefixed=("${files[@]/#/$root/}")
  while IFS='|' read -r name pattern; do
    [ -z "$name" ] && continue
    hits="$(grep -InEH -- "$pattern" "${prefixed[@]}" 2>/dev/null)"
    rc=$?
    if [ "$rc" -gt 1 ]; then
      gate_fail "notify-secrets: grep rejected the '$name' shape's pattern (exit $rc) — refusing rather than silently skipping that shape: $pattern"
      continue
    fi
    [ -z "$hits" ] && continue
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      file="${line%%:*}"
      rel="${file#"$root"/}"
      if is_exempt "$rel"; then
        continue
      fi
      gate_fail "notify-secrets: $rel matches the '$name' credential shape (internal/sensitive) — ${line#*:}. If this is a deliberate, non-live test fixture, register it in check-notify-secrets.sh's EXEMPT_PATHS with a reason."
    done <<<"$hits"
  done <<<"$shapes"
}

# check_telegram_single_copy is invariant 2: the telegram-bot-token
# regex SOURCE (not an instance it matches — the pattern text itself)
# appears nowhere outside internal/sensitive/matcher.go, except the one
# doc that quotes it in prose (named above, with its reason).
#
# ZERO hits — not even internal/sensitive/matcher.go itself — is refused,
# not silently passed: the shape's own home is expected to match trivially
# (it IS the regex source), so finding it nowhere means the source moved or
# the file list is wrong, and "the shape exists in exactly one place" is
# unmeasured, not proven, in that state. Same rc>1 refusal as scan_secrets
# for a grep that errors rather than simply finding nothing.
check_telegram_single_copy() { # $1 = root, $2 = shapes
  local root="$1" shapes="$2" pattern files prefixed hits rc line file rel
  pattern="$(printf '%s\n' "$shapes" | grep -m1 '^telegram-bot-token|' | cut -d'|' -f2-)"
  if [ -z "$pattern" ]; then
    gate_fail "notify-secrets: the projection carries no telegram-bot-token shape — cannot check invariant 2 (single-copy regex)"
    return
  fi
  mapfile -t files < <(tracked_files "$root")
  if [ "${#files[@]}" -eq 0 ]; then
    gate_fail "notify-secrets: tracked_files returned zero files under $root — refusing rather than checking invariant 2 against nothing"
    return
  fi
  prefixed=("${files[@]/#/$root/}")
  hits="$(grep -FnH -- "$pattern" "${prefixed[@]}" 2>/dev/null)"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    gate_fail "notify-secrets: grep rejected the telegram-bot-token single-copy scan (exit $rc) — refusing rather than treating it as zero hits"
    return
  fi
  if [ -z "$hits" ]; then
    gate_fail "notify-secrets: found the telegram-bot-token regex source NOWHERE in the tree, not even at internal/sensitive/matcher.go — refusing rather than passing an invariant this run could not measure"
    return
  fi
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    rel="${file#"$root"/}"
    case "$rel" in
      internal/sensitive/matcher.go) continue ;;
      "$TELEGRAM_DOC_EXEMPT") continue ;;
      *) gate_fail "notify-secrets: the telegram-bot-token regex source is duplicated outside internal/sensitive at $rel — it must exist in exactly one place" ;;
    esac
  done <<<"$hits"
}

run_check() { # $1 = root
  local root="${1:-$GATE_ROOT}" json shapes
  if ! json="$(sensitive_shapes_json)"; then
    gate_fail "notify-secrets: could not obtain the sensitive-shape projection (a2a __catalog --sensitive-shapes --json) — REFUSING rather than falling back to an inlined regex"
    return
  fi
  shapes="$(shape_pairs "$json")"
  if [ -z "$shapes" ] || ! printf '%s\n' "$shapes" | grep -q '^telegram-bot-token|'; then
    gate_fail "notify-secrets: the sensitive-shape projection parsed to zero usable shapes (or is missing telegram-bot-token) — REFUSING rather than policing an empty or wrong set"
    return
  fi
  scan_secrets "$root" "$shapes"
  check_telegram_single_copy "$root" "$shapes"
}

# ── teeth ────────────────────────────────────────────────────────────────
# Fixture credential-shaped strings are built at RUNTIME by concatenation,
# never written as a contiguous literal in this file's own source — the
# same discipline internal/sensitive/matcher_test.go already uses
# ("ghp_" + strings.Repeat("a", 36)) — so this gate's own source never
# matches the shapes it polices.

write_stub_binary() { # $1 = path, $2 = mode: good|empty|noflag|badregex
  case "$2" in
    good)
      cat >"$1" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__catalog" ] && [ "$2" = "--sensitive-shapes" ] && [ "$3" = "--json" ]; then
  printf '%s\n' \
    '[' \
    '  {' \
    '    "name": "aws-access-key-id",' \
    '    "pattern": "AKIA[0-9A-Z]{16}"' \
    '  },' \
    '  {' \
    '    "name": "telegram-bot-token",' \
    "    \"pattern\": \"\\\\b[0-9]{6,}"":""[A-Za-z0-9_-]{30,}\"" \
    '  }' \
    ']'
  exit 0
fi
exit 2
STUB
      ;;
    empty)
      cat >"$1" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' '[]'
exit 0
STUB
      ;;
    noflag)
      # AC9's literal case: a binary built WITHOUT --sensitive-shapes
      # support — the real catalog.go rejects an unknown flag with exit 2
      # and a stderr message, unconditionally, whatever args it is given.
      cat >"$1" <<'STUB'
#!/usr/bin/env bash
echo "a2a __catalog: unknown flag \"--sensitive-shapes\" (this build predates it)" >&2
exit 2
STUB
      ;;
    badregex)
      cat >"$1" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__catalog" ] && [ "$2" = "--sensitive-shapes" ] && [ "$3" = "--json" ]; then
  printf '%s\n' \
    '[' \
    '  {' \
    '    "name": "broken-shape",' \
    '    "pattern": "[0-9"' \
    '  },' \
    '  {' \
    '    "name": "telegram-bot-token",' \
    "    \"pattern\": \"\\\\b[0-9]{6,}"":""[A-Za-z0-9_-]{30,}\"" \
    '  }' \
    ']'
  exit 0
fi
exit 2
STUB
      ;;
  esac
  chmod +x "$1"
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = root, $5 = A2A_VERIFY_BINARY override (may be empty)
  local label="$1" verdict="$2" needle="$3" root="$4" bin="${5:-}" out rc
  set +e
  out="$(
    (
      if [ -n "$bin" ]; then export A2A_VERIFY_BINARY="$bin"; else unset A2A_VERIFY_BINARY; fi
      _GATE_ERRORS=0
      run_check "$root"
      gate_summary "notify-secrets-teeth"
    ) 2>&1
  )"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
      echo "check-notify-secrets --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-notify-secrets --teeth: $label reds"
  elif [ "$rc" -ne 0 ]; then
    echo "check-notify-secrets --teeth: FALSE RED — $label should green:" >&2
    echo "$out" >&2
    return 1
  else
    echo "check-notify-secrets --teeth: $label greens"
  fi
}

# seed_matcher_go writes a fixture root's internal/sensitive/matcher.go
# carrying the (runtime-built, never contiguous in THIS script's own
# source) telegram-bot-token regex source — the "real" copy every fixture
# exercising invariant 2 needs, so the "found nowhere, refuse" branch only
# fires in the case built to prove it.
seed_matcher_go() { # $1 = root, $2 = telegram pattern source
  mkdir -p "$1/internal/sensitive"
  printf 'package sensitive\n\n// fixture: the telegram-bot-token shape home\nconst pattern = `%s`\n' "$2" >"$1/internal/sensitive/matcher.go"
}

run_teeth() {
  local work good_bin empty_bin noflag_bin badregex_bin fixture aws_token tg_token
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${work:-}"' EXIT

  # Split at the colon for the same reason the projection fixture above is:
  # invariant 2 scans THIS file, and the shape must never appear contiguously
  # in it. The leading backslash-b is a leftover from when the shape carried a
  # word boundary; the closing audit removed it, which is what made the rest of
  # this string the whole pattern.
  tg_token='\'"b[0-9]{6,}"":""[A-Za-z0-9_-]{30,}"

  good_bin="$work/good-a2a"; write_stub_binary "$good_bin" good
  empty_bin="$work/empty-a2a"; write_stub_binary "$empty_bin" empty

  # A2A_VERIFY_BINARY set but not executable: refuses, does not skip.
  local not_exec="$work/not-executable"
  : >"$not_exec"
  fixture="$work/refusal-root"; mkdir -p "$fixture"
  teeth_expect "A2A_VERIFY_BINARY not executable" red \
    "REFUSING rather than falling back to an inlined regex" "$fixture" "$not_exec" || return 1

  # Projection parses to zero shapes (missing telegram-bot-token too): refuses.
  teeth_expect "projection parses to zero shapes" red \
    "REFUSING rather than policing an empty or wrong set" "$fixture" "$empty_bin" || return 1

  # AC9's literal case: a binary built WITHOUT --sensitive-shapes support
  # (exits 2, unknown-flag stderr) is a third failure shape, distinct from
  # "not executable" and "parses to zero shapes" — must refuse the same way.
  noflag_bin="$work/noflag-a2a"; write_stub_binary "$noflag_bin" noflag
  teeth_expect "binary built without --sensitive-shapes support" red \
    "REFUSING rather than falling back to an inlined regex" "$fixture" "$noflag_bin" || return 1

  # Clean fixture tree: greens.
  fixture="$work/clean"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "clean fixture tree" green "" "$fixture" "$good_bin" || return 1

  # tracked_files returns zero files (an empty root — not a missing binary,
  # not a bad projection): refuses, does not silently scan nothing green.
  fixture="$work/empty-tree"; mkdir -p "$fixture"
  teeth_expect "empty fixture tree (zero tracked files)" red \
    "returned zero files under" "$fixture" "$good_bin" || return 1

  # A shape whose pattern the ERE engine rejects (unbalanced group): grep
  # exits >1, and that must refuse the shape rather than silently treat the
  # rejection as "no match".
  badregex_bin="$work/badregex-a2a"; write_stub_binary "$badregex_bin" badregex
  fixture="$work/badregex-tree"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "grep rejects an uncompilable shape pattern" red \
    "grep rejected the 'broken-shape' shape's pattern" "$fixture" "$badregex_bin" || return 1

  # A credential shape at an UNREGISTERED path: reds.
  aws_token="AKIA$(printf 'A%.0s' $(seq 1 16))"
  fixture="$work/unregistered-hit"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nconst leaked = "%s"\n' "$aws_token" >"$fixture/pkg/leak.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "credential shape at an unregistered path" red \
    "matches the 'aws-access-key-id' credential shape" "$fixture" "$good_bin" || return 1

  # The SAME shape at a path this gate's own registry names: greens — the
  # registry, not a second regex, is what's under test here.
  fixture="$work/registered-hit"; mkdir -p "$fixture/internal/sensitive"
  printf 'package sensitive\n\n// test fixture\nconst sample = "%s"\n' "$aws_token" >"$fixture/internal/sensitive/matcher_test.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "same credential shape at a registered (exempt) path" green \
    "" "$fixture" "$good_bin" || return 1

  # Invariant 2: a second copy of the telegram-bot-token regex SOURCE
  # outside internal/sensitive/matcher.go: reds.
  fixture="$work/second-copy"; mkdir -p "$fixture/internal/other"
  seed_matcher_go "$fixture" "$tg_token"
  printf 'package other\n\n// duplicated on purpose for the teeth case\nconst re = `%s`\n' "$tg_token" >"$fixture/internal/other/dup.go"
  teeth_expect "telegram-bot-token regex source duplicated outside internal/sensitive" red \
    "regex source is duplicated outside internal/sensitive" "$fixture" "$good_bin" || return 1

  echo "check-notify-secrets --teeth: PASS — not-executable, zero-shape and no-flag-support binaries all refuse; an empty tracked-file set and a grep-rejected pattern both refuse rather than silently pass; an unregistered credential-shaped literal reds while the same shape at a registered path greens; a second copy of the telegram regex source outside internal/sensitive reds"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  describe_exemptions
  run_check "$GATE_ROOT"
  gate_summary "notify-secrets"
fi
