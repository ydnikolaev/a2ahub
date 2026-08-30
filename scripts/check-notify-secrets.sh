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
# REFUSES — loudly, and as an UNMEASURED run (gate-lib's own exit status
# for "I could not measure this", distinct from a measured failure) rather
# than falling back to an inlined pattern: a gate carrying its own copy of
# the thing it checks is the drift it exists to catch. `A2A_VERIFY_BINARY` selects the shared binary
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

# lane-inputs lives on this gate's Makefile recipe, not here. This script is
# PRIVATE_ONLY and the publisher strips it, so a claim written here is absent
# from every public checkout — and the lane derivation, unable to settle the
# phase, refuses the WHOLE lane rather than that one gate. Public CI on `main`
# was red for exactly that reason. Same placement as check-feedback-corpus.sh.
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

# readable_corpus fills READABLE_CORPUS with the ABSOLUTE path of every
# tracked file under $1 that this process can actually open, and refuses —
# once, as a measurement failure — for the ones it cannot.
#
# THE INCIDENT THIS EXISTS FOR (2026-08-21, spec 02 §11 A1). Three files of
# this very epic were renamed with the plain rename command instead of git's,
# so their old paths stayed in the index and left the working tree. Every
# shape's scan then met an unopenable argument, grep exited 2, and this gate
# announced that all fourteen credential shapes had had their patterns
# rejected. Fourteen lines sending the reader to inspect regular expressions,
# about a pattern list that was correct.
#
# The refusal is per-corpus rather than per-shape because the fact is one
# fact — this path could not be read — and repeating it once per shape is how
# it drowned the real cause last time. The READABLE files are still scanned:
# a partial measurement plus an explicit "and here is the part I could not
# measure" beats refusing wholesale, and gate_summary already makes the
# unmeasured half dominate the exit status.
#
# Returns 1 only when NOTHING is left to scan.
READABLE_CORPUS=()
readable_corpus() { # $1 = root, $2 = prose naming what would otherwise be reported
  local root="$1" what="$2" files=() f unreadable=0 first="" dirlinks=0 firstlink="" rootabs
  READABLE_CORPUS=()
  rootabs="$(cd "$root" && pwd -P)"
  mapfile -t files < <(tracked_files "$root")
  if [ "${#files[@]}" -eq 0 ]; then
    gate_unmeasured "notify-secrets: tracked_files returned zero files under $root — refusing rather than $what and reporting green"
    return 1
  fi
  for f in "${files[@]}"; do
    # A TRACKED SYMLINK POINTING AT A DIRECTORY IS NOT SCANNABLE CONTENT, and
    # feeding one to grep does not skip that path — grep answers "Is a
    # directory" on stderr and the shape's whole scan is reported UNMEASURED.
    # On 2026-08-30 exactly that happened: `plugins/a2ahub/skills/a2ahub` (the
    # plugin's link to the shipped skill tree) entered the tracked set, and
    # ALL FOURTEEN credential shapes stopped policing the ENTIRE repository in
    # one step. The gate said so rather than passing, which is why this was
    # found at all — but the honest state was "nothing was scanned".
    #
    # `find`'s fallback never had this problem: `-type f` excludes a symlink.
    # The git branch had no equivalent, so the two enumerations disagreed about
    # what a corpus is.
    if [ -L "$root/$f" ] && [ -d "$root/$f" ]; then
      local target=""
      target="$(cd "$root/$f" 2>/dev/null && pwd -P)" || target=""
      case "${target:-/dev/null}" in
        "$rootabs"/* | "$rootabs")
          # Its contents are tracked in their own right and are scanned as
          # themselves, so following the link would only scan them twice.
          dirlinks=$((dirlinks + 1))
          [ -n "$firstlink" ] || firstlink="$f"
          ;;
        *)
          gate_unmeasured "notify-secrets: tracked symlink $f resolves OUTSIDE this tree (${target:-unresolvable}), so its content is not in the scanned corpus and no shape was checked against it. That is a fact about the MEASUREMENT: point it inside the repository, or exempt it deliberately."
          ;;
      esac
      continue
    fi
    if [ -r "$root/$f" ]; then
      READABLE_CORPUS+=("$root/$f")
    else
      unreadable=$((unreadable + 1))
      [ -n "$first" ] || first="$f"
    fi
  done
  if [ "$unreadable" -gt 0 ]; then
    gate_unmeasured "notify-secrets: $unreadable tracked path(s) are absent from the working tree or unreadable (first: $first) — this run did NOT scan them, and that is a fact about the MEASUREMENT, not about the credential shapes. A tracked path missing from disk usually means a rename that went through the plain rename command instead of git's; restore or re-stage it and run this gate again."
  fi
  if [ "$dirlinks" -gt 0 ]; then
    echo "notify-secrets: $dirlinks tracked symlink(s) to directories inside this tree were not scanned as files (first: $firstlink); their targets are tracked paths and are scanned as themselves."
  fi
  [ "${#READABLE_CORPUS[@]}" -gt 0 ]
}

# grep_corpus runs one search over READABLE_CORPUS and separates the two
# things a non-zero grep means. Exit 1 is "no match", a finding. Exit 2 or
# more is "I could not run", and grep's own stderr — kept, never discarded —
# is the only thing that says which of the several causes it was.
#
# Sets GREP_HITS and GREP_STDERR; returns 0 when the search ran (match or
# not) and 1 when it did not.
GREP_HITS=""
GREP_STDERR=""
grep_corpus() { # $@ = grep arguments, before the corpus
  local errfile rc
  GREP_HITS=""; GREP_STDERR=""
  errfile="$(mktemp)" || return 1
  GREP_HITS="$(grep "$@" "${READABLE_CORPUS[@]}" 2>"$errfile")"
  rc=$?
  GREP_STDERR="$(tr '\n' ' ' <"$errfile" | cut -c1-400)"
  rm -f "$errfile"
  [ "$rc" -le 1 ]
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
#
# Both of those are measurement failures, not findings, and since 2026-08-21
# they say so in the word this repo reserves for it.
scan_secrets() { # $1 = root, $2 = shapes
  local root="$1" shapes="$2" name pattern hits line file rel
  readable_corpus "$root" "scanning nothing" || return
  while IFS='|' read -r name pattern; do
    [ -z "$name" ] && continue
    if ! grep_corpus -InEH -- "$pattern"; then
      gate_unmeasured "notify-secrets: the scan for the '$name' shape did not run to completion, so this run did NOT police that shape. grep said: ${GREP_STDERR:-<nothing>}. The pattern was: $pattern"
      continue
    fi
    hits="$GREP_HITS"
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
  local root="$1" shapes="$2" pattern hits line file rel
  pattern="$(printf '%s\n' "$shapes" | grep -m1 '^telegram-bot-token|' | cut -d'|' -f2-)"
  if [ -z "$pattern" ]; then
    gate_unmeasured "notify-secrets: the projection carries no telegram-bot-token shape — invariant 2 (single-copy regex) was NOT checked this run"
    return
  fi
  readable_corpus "$root" "checking invariant 2 against nothing" || return
  if ! grep_corpus -FnH -- "$pattern"; then
    gate_unmeasured "notify-secrets: the telegram-bot-token single-copy scan did not run to completion, so invariant 2 is unproven rather than satisfied. grep said: ${GREP_STDERR:-<nothing>}"
    return
  fi
  hits="$GREP_HITS"
  if [ -z "$hits" ]; then
    gate_unmeasured "notify-secrets: found the telegram-bot-token regex source NOWHERE in the tree, not even at internal/sensitive/matcher.go — the shape's own home matches it trivially, so finding it nowhere means the scan looked in the wrong place, not that the invariant holds"
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
    gate_unmeasured "notify-secrets: could not obtain the sensitive-shape projection (a2a __catalog --sensitive-shapes --json) — REFUSING rather than falling back to an inlined regex"
    return
  fi
  shapes="$(shape_pairs "$json")"
  if [ -z "$shapes" ] || ! printf '%s\n' "$shapes" | grep -q '^telegram-bot-token|'; then
    gate_unmeasured "notify-secrets: the sensitive-shape projection parsed to zero usable shapes (or is missing telegram-bot-token) — REFUSING rather than policing an empty or wrong set"
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

# teeth_expect runs one fixture and judges WHICH KIND of red it produced,
# never merely that it was red. The three verdicts are the paired teeth spec
# 02 §6 requires:
#
#   domain-red      the subject is genuinely wrong — a real finding, and the
#                   word UNMEASURED must NOT appear
#   unmeasured-red  the subject may be fine but this run could not measure
#                   it — the word UNMEASURED must appear
#   green           nothing found, and something WAS looked at
#
# Asserting only "it went red with this needle" is what let the class this
# gate belongs to survive: every one of the nine recorded incidents was red
# (or green) with a message about the wrong thing, and a tooth that reads the
# exit status alone cannot see that. The needle stays because the message has
# to be actionable; the verdict kind is what says it is the ACTIONABLE ONE.
#
# The last run's combined output is kept in _TEETH_LAST_OUT so the paired
# tooth below can assert that a domain verdict and a refusal are not the same
# string.
_TEETH_LAST_OUT=""
teeth_expect() { # $1 = label, $2 = domain-red|unmeasured-red|green, $3 = needle, $4 = root, $5 = A2A_VERIFY_BINARY override (may be empty)
  local label="$1" verdict="$2" needle="$3" root="$4" bin="${5:-}" out rc
  set +e
  out="$(
    (
      if [ -n "$bin" ]; then export A2A_VERIFY_BINARY="$bin"; else unset A2A_VERIFY_BINARY; fi
      # BOTH counters, not just the error one: a subshell that inherited a
      # non-zero unmeasured count would make every later fixture look
      # incomplete, and the summary line each tooth reads is built from both.
      _GATE_ERRORS=0
      _GATE_UNMEASURED=0
      run_check "$root"
      gate_summary "notify-secrets-teeth"
    ) 2>&1
  )"
  rc=$?
  set -e
  _TEETH_LAST_OUT="$out"
  case "$verdict" in
    domain-red|unmeasured-red)
      if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
        echo "check-notify-secrets --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
        echo "$out" >&2
        return 1
      fi
      if [ "$verdict" = "unmeasured-red" ] && ! printf '%s\n' "$out" | grep -Fq "UNMEASURED"; then
        echo "check-notify-secrets --teeth: WRONG VERDICT — $label is a failed MEASUREMENT and must say UNMEASURED, not announce a finding about the tree:" >&2
        echo "$out" >&2
        return 1
      fi
      if [ "$verdict" = "domain-red" ] && printf '%s\n' "$out" | grep -Fq "UNMEASURED"; then
        echo "check-notify-secrets --teeth: WRONG VERDICT — $label is a real finding about the tree and must NOT be reported as an unmeasured run:" >&2
        echo "$out" >&2
        return 1
      fi
      echo "check-notify-secrets --teeth: $label reds ($verdict)"
      ;;
    green)
      if [ "$rc" -ne 0 ]; then
        echo "check-notify-secrets --teeth: FALSE RED — $label should green:" >&2
        echo "$out" >&2
        return 1
      fi
      echo "check-notify-secrets --teeth: $label greens"
      ;;
    *)
      echo "check-notify-secrets --teeth: unknown verdict '$verdict' for $label" >&2
      return 1
      ;;
  esac
}

# seed_tracked_but_absent builds spec 02 §11 A1's real incident: a path that
# is IN THE INDEX and NOT ON DISK. The scratch tree gets its own repository —
# nothing here touches the checkout this gate lives in — and no commit is
# needed, because `git ls-files` reads the index and `git add` is what fills
# it. That is the whole reproduction: stage a file, delete it, and the
# production scan meets an argument it cannot open.
seed_tracked_but_absent() { # $1 = root, $2 = relative path to stage then delete
  ( cd "$1" && git init -q . && git add -A ) >/dev/null 2>&1 || return 1
  rm -f "$1/$2"
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
  local work good_bin empty_bin noflag_bin badregex_bin fixture aws_token tg_token ta_out tb_out
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
  teeth_expect "A2A_VERIFY_BINARY not executable" unmeasured-red \
    "REFUSING rather than falling back to an inlined regex" "$fixture" "$not_exec" || return 1

  # Projection parses to zero shapes (missing telegram-bot-token too): refuses.
  teeth_expect "projection parses to zero shapes" unmeasured-red \
    "REFUSING rather than policing an empty or wrong set" "$fixture" "$empty_bin" || return 1

  # AC9's literal case: a binary built WITHOUT --sensitive-shapes support
  # (exits 2, unknown-flag stderr) is a third failure shape, distinct from
  # "not executable" and "parses to zero shapes" — must refuse the same way.
  noflag_bin="$work/noflag-a2a"; write_stub_binary "$noflag_bin" noflag
  teeth_expect "binary built without --sensitive-shapes support" unmeasured-red \
    "REFUSING rather than falling back to an inlined regex" "$fixture" "$noflag_bin" || return 1

  # Clean fixture tree: greens.
  fixture="$work/clean"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "clean fixture tree" green "" "$fixture" "$good_bin" || return 1

  # tracked_files returns zero files (an empty root — not a missing binary,
  # not a bad projection): refuses, does not silently scan nothing green.
  fixture="$work/empty-tree"; mkdir -p "$fixture"
  teeth_expect "empty fixture tree (zero tracked files)" unmeasured-red \
    "returned zero files under" "$fixture" "$good_bin" || return 1

  # A shape whose pattern the ERE engine rejects (unbalanced group): grep
  # exits >1, and that must refuse the shape rather than silently treat the
  # rejection as "no match".
  badregex_bin="$work/badregex-a2a"; write_stub_binary "$badregex_bin" badregex
  fixture="$work/badregex-tree"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "grep rejects an uncompilable shape pattern" unmeasured-red \
    "the scan for the 'broken-shape' shape did not run to completion" "$fixture" "$badregex_bin" || return 1

  # A credential shape at an UNREGISTERED path: reds.
  aws_token="AKIA$(printf 'A%.0s' $(seq 1 16))"
  fixture="$work/unregistered-hit"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nconst leaked = "%s"\n' "$aws_token" >"$fixture/pkg/leak.go"
  seed_matcher_go "$fixture" "$tg_token"
  teeth_expect "credential shape at an unregistered path" domain-red \
    "matches the 'aws-access-key-id' credential shape" "$fixture" "$good_bin" || return 1
  ta_out="$_TEETH_LAST_OUT"

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
  teeth_expect "telegram-bot-token regex source duplicated outside internal/sensitive" domain-red \
    "regex source is duplicated outside internal/sensitive" "$fixture" "$good_bin" || return 1

  # ── Tb, and it is the incident itself (spec 02 §11 A1) ────────────────
  # The tree is CLEAN: no credential-shaped literal anywhere, the telegram
  # regex source exactly where it belongs. The only thing wrong is that one
  # tracked path is not on disk, so the scan cannot read it. That must be
  # reported as a failed measurement — a fact about this run — and never as
  # fourteen findings about the shapes' patterns, which is what it was
  # reported as on 2026-08-21, to the author of the spec about this class,
  # while it was being written.
  fixture="$work/tracked-but-absent"; mkdir -p "$fixture/pkg"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  if ! seed_tracked_but_absent "$fixture" pkg/hello.go; then
    echo "check-notify-secrets --teeth: could not build the tracked-but-absent fixture (git init/add failed)" >&2
    return 1
  fi
  teeth_expect "a tracked path absent from the working tree" unmeasured-red \
    "are absent from the working tree or unreadable" "$fixture" "$good_bin" || return 1
  tb_out="$_TEETH_LAST_OUT"

  # A TRACKED SYMLINK TO A DIRECTORY: green, not an aborted scan. This is the
  # 2026-08-30 defect, where one such link (a plugin's link to the shipped
  # skill tree) made all fourteen shapes report UNMEASURED over the whole
  # repository — grep answers "Is a directory" and the shape's scan ends there.
  # The two arms are separate assertions because they have different remedies:
  # a link INSIDE the tree is skipped, since its target is tracked and scanned
  # as itself; a link pointing OUT of the tree is content nothing scanned, and
  # saying nothing about it would be the silent half of the same bug.
  fixture="$work/dirlink-inside"; mkdir -p "$fixture/pkg/real"
  printf 'package real\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/real/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  ( cd "$fixture" && ln -s pkg/real linked )
  ( cd "$fixture" && git init -q . && git add -A ) >/dev/null 2>&1 || {
    echo "check-notify-secrets --teeth: could not build the dirlink-inside fixture" >&2; return 1; }
  teeth_expect "a tracked symlink to a directory inside the tree" green \
    "were not scanned as files" "$fixture" "$good_bin" || return 1

  fixture="$work/dirlink-outside"; mkdir -p "$fixture/pkg"
  mkdir -p "$work/elsewhere"
  printf 'package pkg\n\nfunc Hello() string { return "hello" }\n' >"$fixture/pkg/hello.go"
  seed_matcher_go "$fixture" "$tg_token"
  ( cd "$fixture" && ln -s ../elsewhere outward )
  ( cd "$fixture" && git init -q . && git add -A ) >/dev/null 2>&1 || {
    echo "check-notify-secrets --teeth: could not build the dirlink-outside fixture" >&2; return 1; }
  teeth_expect "a tracked symlink resolving outside the tree" unmeasured-red \
    "resolves OUTSIDE this tree" "$fixture" "$good_bin" || return 1

  # ── the pairing, which is the assertion that actually holds the class ──
  # Either half alone proves nothing. "Tb says UNMEASURED" still passes if a
  # real finding grew the same marker; "Ta says the file matches a shape"
  # still passes if an unreadable input produces that identical line — which
  # is precisely the substitution this phase exists to make impossible. So
  # the two outputs are compared directly, and a run in which they agree is
  # a failure however green each half looks on its own.
  if [ "$ta_out" = "$tb_out" ]; then
    echo "check-notify-secrets --teeth: a measured finding and a failed measurement produced the SAME output — one verdict is masquerading as the other:" >&2
    echo "$ta_out" >&2
    return 1
  fi
  if printf '%s\n' "$ta_out" | grep -Fq "UNMEASURED"; then
    echo "check-notify-secrets --teeth: a real credential-shape finding was reported as an unmeasured run" >&2
    return 1
  fi
  if ! printf '%s\n' "$tb_out" | grep -Fq "UNMEASURED"; then
    echo "check-notify-secrets --teeth: an unreadable tracked path did not produce the measurement refusal" >&2
    return 1
  fi

  echo "check-notify-secrets --teeth: PASS — not-executable, zero-shape and no-flag-support binaries, an empty tracked-file set, an uncompilable pattern and a tracked path absent from disk all refuse AS UNMEASURED; an unregistered credential-shaped literal and a duplicated telegram regex source both red as findings WITHOUT that marker; the same shape at a registered path greens; a tracked symlink to a directory inside the tree is SKIPPED and named rather than aborting every shape's scan while one resolving outside the tree refuses; and the two verdicts are asserted to differ rather than merely to be red"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  describe_exemptions
  run_check "$GATE_ROOT"
  gate_summary "notify-secrets"
fi
