#!/usr/bin/env bash
# check-unmeasured-reach.sh — a gate that could not measure must SAY SO where a
# reader looks, and this refuses when a new one does not.
#
# lane-inputs:
#   scripts/**
#   Makefile
#   .github/workflows/**
#   !scripts/tests/**
#
# WHY, and it is one incident wearing two costumes.
#
# On the v0.25.2 release push the `gitleaks` job went red with
# `curl: (35) Recv failure`. Nothing was leaked; the scanner never ran. A red
# secret-scan on a release push reads as a leak until somebody opens the log,
# and that log was opened at 01:00 while a release was in flight.
#
# THE STRUCTURAL FACT THAT BOUNDS EVERYTHING HERE: a GitHub Actions step has
# exactly two outcomes, and GNU Make turns a recipe's exit 3 into its own exit
# 2. So no exit code reaching a workflow can carry "I could not measure this" —
# not today, and not after this gate. The only carriers are the step's printed
# TEXT and the job's NAME, and of those only an `::error::` annotation is
# visible without opening the log. That is why this gate judges TEXT.
#
# `gate_unmeasured` (scripts/lib/gate-lib.sh) prints `::error::UNMEASURED: …`
# on stdout under GITHUB_ACTIONS, so a gate routing through it is correct for
# free. This gate exists for everything that does NOT: a Makefile recipe with
# a plain `echo`, a workflow's own retry loop, a new script written before its
# author has read gate-lib.
#
# WHAT IT CANNOT SEE — printed with every verdict, because a completeness gate
# that quietly under-discovers is the exact defect this phase is about, and
# this repository has already paid for that once (`e07c0655`, the neighbouring
# audit answering with claims it had not checked):
#
#   1. A GATE THAT RETURNS GREEN WHEN IT COULD NOT MEASURE. It appears in no
#      grep here, because there is nothing to find. That class is real — it is
#      what check-notify-secrets.sh's eight refusals were retrofitted against —
#      and it is a larger, different piece of work.
#   2. REACHABILITY IS NOT COMPUTED. Whether a producer is reached from a
#      workflow needs a transitive closure over make targets, REPO_GATES and
#      `run:` lines that this gate does not attempt. It therefore holds EVERY
#      producer to the rule, including local-only ones — STRICTER than the
#      question, never weaker, and cheap to satisfy since gate_unmeasured is
#      how one writes it anyway.
#   3. A NEW SHAPE OF NOT-MEASURING that uses none of the tokens below. Checks
#      2 and 4 narrow the two shapes that have actually bitten — a download
#      loop that exhausts its retries, and `command -v X || exit 1` — but a
#      fourth costume would be invisible until somebody meets it. Check 4 also
#      reads only the SINGLE-LINE guard form; the multi-line
#      `if ! command -v …; then` was converted by hand and is not policed.
#   4. CHECK 4 CANNOT TELL CODE FROM PROSE INSIDE A STRING. `code_of` removes
#      comments, which is the mirror of the mistake `e07c0655` fixed next door
#      — but a line that merely QUOTES the shape inside an `echo` still counts
#      as carrying it. That bit this gate's own teeth twice within an hour,
#      first as a fixture written on one source line and then as a failure
#      message describing what it tests. Both were reworded rather than
#      excused: an exception for the file that defines the rule is the least
#      trustworthy thing this gate could contain.
#
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   self-locates the shared helper through command substitution, the same
#   construct check-notify-secrets.sh and check-view-vocabulary.sh declare.
#   The other unresolved read is `code_of`'s own strip, whose argument is a
#   path this gate has just enumerated out of `git ls-files` over the three
#   globs declared above — so the set it reads is exactly the set it declares,
#   one indirection later, and the `--teeth` harness reads only a `mktemp -d`
#   scratch tree.
set -uo pipefail

ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# shellcheck source=lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# code_of strips comment lines before matching. A command NAMED IN A COMMENT is
# not a command that runs, and the mirror-image mistake — counting a comment as
# coverage — is what `e07c0655` fixed in ci-parity's audit three days before
# this gate was written. Same class, so the same defence, deliberately.
code_of() { # $1 = path
  sed -e 's/[[:space:]]*#.*$//' "$1" 2>/dev/null
}

# has_shape asks a question of TEXT ALREADY IN A VARIABLE, through a herestring,
# and never through a pipe. That is not style.
#
# This script runs under `pipefail`. Piping a large file's text into a quiet
# pattern match lets the matcher exit the instant it succeeds; the writer on the
# left then takes SIGPIPE, and pipefail reports the whole pipeline as failed —
# so a file that DOES match reads as "no match", and only the big ones, because
# a small file finishes writing before the matcher leaves. This gate was written
# that way for its first ten minutes: it found 7 of 13 producers, silently, and
# the 6 it dropped were the six longest files. A discovery gate that
# under-discovers by file size is this phase's own subject, in the tool built to
# police it. A herestring has no left-hand process, so it cannot happen.
has_shape() { # $1 = text, $2 = extended regex
  grep -qE "$2" <<<"$1"
}

# A file is a PRODUCER if, outside its comments, it can reach an unmeasured
# verdict at all.
PRODUCER_RE='gate_unmeasured|GATE_EXIT_UNMEASURED|UNMEASURED|exit 3([^0-9]|$)'
# It CARRIES if the reader gets the token. Two spellings, both real: the
# gate-lib call (which emits the annotation itself) and a workflow writing the
# annotation directly, because a workflow cannot source a shell library into a
# `run:` block it does not own.
CARRIER_RE='gate_unmeasured[[:space:]]+"|::error::UNMEASURED'

# EXCUSED — producers that legitimately do not carry the token, each with the
# reason, in ONE structure that both the excuse lookup and check 3 read.
#
# Same shape as ci-parity.sh's excuse arms and for the same reason: a list in a
# separate file drifts away from the thing it excuses, and an excuse whose
# wording nobody can read next to the code is an excuse nobody re-examines.
# Check 3 refuses an entry that matches no producer, so a stale exemption is a
# red line rather than a quiet permanent one.
#
# `gate-lib.sh` is deliberately NOT here: it DEFINES the annotation, so it
# carries by construction and an arm for it would never be reached — a dead
# excuse, which is the thing check 3 exists to prevent.
#
# EMPTY IS THE CORRECT STATE, the same claim `scripts/lib/flaky-tests.txt`
# makes about itself. It held `scripts/verify.sh` for one afternoon, on the
# grounds that the runner CONSUMES the verdict rather than producing one —
# and then that stopped being true in the same wave: verify.sh now sources
# gate-lib and calls `gate_unmeasured` itself, for a configured lint gate with
# no linter. The arm went dead within hours of being written, which is the
# whole argument for check 3 rather than for a list somebody promises to
# re-read.
EXCUSED=()

excuse_for() { # $1 = repo-relative path; echoes the reason, or returns 1
  local e
  for e in ${EXCUSED[@]+"${EXCUSED[@]}"}; do
    [ "${e%%|*}" = "$1" ] || continue
    printf '%s\n' "${e#*|}"
    return 0
  done
  return 1
}

# TOOL_GUARD_EXCUSED — files whose `command -v … exit 1` is legitimately not a
# gate refusing to measure. Two arms, both checked rather than assumed.
TOOL_GUARD_EXCUSED=(
  "scripts/dev-install.sh|an INSTALLER, not a gate: it puts a dev binary on PATH and has no verdict to report, so a missing Go toolchain is a precondition it cannot proceed without rather than a measurement it failed to take"
  "scripts/release-postflight.sh|the guard is INVERTED and lives inside that script's own --teeth: it fires when \`gh\` IS still resolvable, asserting the fixture's minimal PATH actually hid it. Refusing there is the tooth working"
)

tool_guard_excuse() { # $1 = repo-relative path; echoes the reason, or returns 1
  local e
  for e in "${TOOL_GUARD_EXCUSED[@]}"; do
    [ "${e%%|*}" = "$1" ] || continue
    printf '%s\n' "${e#*|}"
    return 0
  done
  return 1
}

# tool_guards prints `path:line` for every single-line absent-tool guard whose
# failure arm exits non-zero without routing through gate_unmeasured.
#
# THE LINE NUMBER COMES FROM THE SAME SCAN AS THE VERDICT, and the first
# version got that wrong: it decided on comment-stripped code and then looked
# the line up with a raw `grep -n`, so the refusal pointed at a COMMENT
# describing the rule while the real match was three hundred lines below. A
# gate that names the wrong line teaches its reader to distrust the finding.
# `code_of` is a per-line substitution, so numbering survives it.
tool_guards() { # -> one `path:line` per match
  git -C "$ROOT" ls-files 'scripts/*.sh' 'Makefile' |
    while IFS= read -r f; do
      case "$f" in scripts/tests/*) continue ;; esac
      [ -f "$ROOT/$f" ] || continue
      code_of "$ROOT/$f" |
        grep -nE 'command -v' |
        grep -E '(exit|return) 1' |
        grep -v gate_unmeasured |
        cut -d: -f1 |
        while IFS= read -r n; do printf '%s:%s\n' "$f" "$n"; done
    done
}

producers() { # -> one repo-relative path per line
  git -C "$ROOT" ls-files 'scripts/*' 'Makefile' '.github/workflows/*' |
    while IFS= read -r f; do
      case "$f" in
        *.md|*.txt|*.tsv|*.json|*.Dockerfile) continue ;;
        scripts/tests/*) continue ;;
      esac
      [ -f "$ROOT/$f" ] || continue
      has_shape "$(code_of "$ROOT/$f")" "$PRODUCER_RE" || continue
      printf '%s\n' "$f"
    done
}

run_check() {
  local f reason found=0 excused=0 carried=0

  # ── check 1 — every producer carries the token, or is excused by name ──────
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    found=$((found + 1))
    if has_shape "$(code_of "$ROOT/$f")" "$CARRIER_RE"; then
      carried=$((carried + 1))
      continue
    fi
    if reason="$(excuse_for "$f")"; then
      gate_ok "unmeasured-reach: excused — $f ($reason)"
      excused=$((excused + 1))
      continue
    fi
    gate_fail "unmeasured-reach: $f can report a run it could not measure, and says so in a channel no reader sees. A step has two outcomes and make collapses exit 3 into its own 2, so the TEXT is the only carrier: emit it with \`gate_unmeasured \"…\"\` (scripts/lib/gate-lib.sh), or — inside a workflow's own \`run:\` block, which cannot source that library — print \`::error::UNMEASURED: <what did NOT happen>, <whose problem it is>\` yourself. If it genuinely cannot, add an arm to excuse_for() in $(basename "${BASH_SOURCE[0]}") saying why."
  done < <(producers)

  # ── check 2 — a retry loop that exhausts must say the check never ran ──────
  #
  # THE ONE SHAPE THAT HAS ACTUALLY BITTEN, narrowed to where it bites. A
  # `run:` block that retries a download has, by construction, a path where the
  # thing it was going to check never happened — and it is the path a reader is
  # least equipped to recognise, because the job that reds is named after the
  # CHECK, not after the download. gitleaks, a2a-validate and a2a-notify all
  # carried this shape; all three now carry the token.
  local wf wfcode loops=0
  for wf in "$ROOT"/.github/workflows/*.yml; do
    [ -f "$wf" ] || continue
    wfcode="$(code_of "$wf")"
    has_shape "$wfcode" 'for attempt in|attempts?/[0-9]|retry|retrying' || continue
    loops=$((loops + 1))
    if ! has_shape "$wfcode" '::error::UNMEASURED'; then
      gate_fail "unmeasured-reach: ${wf#"$ROOT"/} retries something, so it has a path where the thing it was going to check NEVER RAN — and it does not say so. The job reds under the name of the CHECK, not of the download, which is how a failed fetch reads as a finding (v0.25.2's gitleaks push, 2026-08-22). Print \`::error::UNMEASURED: <what did not happen>\` when the retries are exhausted; \`.github/workflows/gitleaks.yml\` is the reference shape."
    fi
  done

  # ── check 4 — AN ABSENT TOOL IS NEVER A FINDING ───────────────────────────
  #
  # THE INVERSE CLASS, and the one check 1 is structurally blind to: a gate
  # that CAN fail to measure but never says the word. `grep gate_unmeasured`
  # finds nothing, because there is nothing to find — so this asks a different
  # question, narrowed to the one shape that has actually been wrong here.
  #
  # `command -v X` guarding an `exit 1` is a gate announcing a MEASURED verdict
  # about something it never measured. `workflow-lint` said
  # "FAIL — actionlint missing" — which reads as "the workflows were linted and
  # found wanting", when nothing was linted; and `check_lint` said the same
  # about golangci-lint. Both are reachable from `make check` and therefore
  # from ci.yml. Found by a ground sweep on 2026-08-23, not by check 1.
  #
  # SINGLE-LINE FORM ONLY. `check_lint`'s multi-line `if ! command -v …; then`
  # is invisible to this and was converted by hand; widening the pattern to
  # multi-line shell would be a parser, and a parser that is wrong is worse
  # than a narrow check that says how narrow it is.
  local tg tgpath tgline seen=""
  while IFS= read -r tg; do
    [ -n "$tg" ] || continue
    tgpath="${tg%:*}"; tgline="${tg##*:}"
    if reason="$(tool_guard_excuse "$tgpath")"; then
      case " $seen " in *" $tgpath "*) continue ;; esac
      seen="$seen $tgpath"
      gate_ok "unmeasured-reach: absent-tool guard excused — $tgpath ($reason)"
      continue
    fi
    gate_fail "unmeasured-reach: $tgpath:$tgline treats an ABSENT TOOL as a measured failure. \`exit 1\` there says the check ran and refused; what happened is that it never ran. Use \`gate_unmeasured\` and exit \$GATE_EXIT_UNMEASURED, so the reader can tell 'not installed' from 'found something'. If the file is not a gate, add an arm to tool_guard_excuse()."
  done < <(tool_guards)

  # ── check 3 — an excuse must still excuse something ────────────────────────
  #
  # A permanent exemption for a file that no longer produces an unmeasured
  # verdict is how a list stops being read. Refuse it, the same way
  # lane-ungated.txt is COUNTED rather than merely allowed.
  local e arm all guards
  all="$(producers)"
  for e in ${EXCUSED[@]+"${EXCUSED[@]}"}; do
    arm="${e%%|*}"
    # AN ABSENT FILE IS NOT A STALE EXCUSE. The publisher strips a path set from
    # the public product, and `make projection` runs this gate inside that
    # projection — so "the file this excuses is not here" is a fact about the
    # checkout, not about the excuse. Reading it as staleness is the exact
    # class that made four private gates red in every public checkout while the
    # private tree stayed green.
    [ -f "$ROOT/$arm" ] || continue
    if ! grep -qxF "$arm" <<<"$all"; then
      gate_fail "unmeasured-reach: EXCUSED carries an entry for $arm, which is no longer an UNMEASURED producer. Remove the entry; an exemption nobody re-examines is how a list stops being read."
      continue
    fi
    # AND AN ENTRY WHOSE FILE NOW CARRIES THE TOKEN IS EQUALLY DEAD — check 1
    # takes the carrying branch and never reaches the excuse. That is not
    # hypothetical: verify.sh's own entry died this way within hours of being
    # written, when it gained a gate_unmeasured call of its own.
    if has_shape "$(code_of "$ROOT/$arm")" "$CARRIER_RE"; then
      gate_fail "unmeasured-reach: EXCUSED carries an entry for $arm, which now CARRIES the token — check 1 never reaches the excuse, so the entry is dead. Remove it."
    fi
  done
  # The absent-tool list gets the same treatment: an excuse for a file that no
  # longer carries a bare `command -v … exit 1` is an exemption nobody is
  # re-examining either.
  guards="$(tool_guards | sed 's/:[0-9]*$//' | sort -u)"
  for e in ${TOOL_GUARD_EXCUSED[@]+"${TOOL_GUARD_EXCUSED[@]}"}; do
    arm="${e%%|*}"
    [ -f "$ROOT/$arm" ] || continue   # absent ≠ stale; see the note above
    grep -qxF "$arm" <<<"$guards" && continue
    gate_fail "unmeasured-reach: TOOL_GUARD_EXCUSED carries an entry for $arm, which no longer treats an absent tool as a failure. Remove the entry."
  done

  printf 'unmeasured-reach: %s producer(s) — %s carry the token, %s excused by name; %s workflow(s) retry something and all of them say what did not happen; every single-line absent-tool guard routes through gate_unmeasured or is excused.\n' \
    "$found" "$carried" "$excused" "$loops"
  printf 'unmeasured-reach: NOT covered by this gate, on purpose — a gate that returns GREEN when it could not measure (nothing to grep for); reachability from a workflow (not computed, so every producer is held to the rule rather than only the reachable ones); and a fourth shape of not-measuring that uses none of these tokens.\n'
  gate_summary "unmeasured-reach"
}

run_teeth() {
  local tmp rc out fail=0
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/unmeasured-reach-teeth.XXXXXX")" || exit 1
  # Expanded AT TRAP-DEFINITION TIME, not at exit: `tmp` is a local, so a
  # single-quoted trap body dereferences a name that no longer exists and
  # `set -u` turns the cleanup into an error message after a green verdict.
  trap "rm -rf -- '$tmp'" EXIT
  git -C "$tmp" init -q -b main
  mkdir -p "$tmp/scripts/lib" "$tmp/.github/workflows"
  cp "$ROOT/scripts/lib/gate-lib.sh" "$tmp/scripts/lib/gate-lib.sh"
  cp "$ROOT/scripts/verify.sh" "$tmp/scripts/verify.sh"
  printf 'all:\n\t@true\n' >"$tmp/Makefile"

  _t() { # $1 = label; runs the check over $tmp and sets out/rc
    git -C "$tmp" add -A >/dev/null 2>&1
    rc=0
    out="$(ROOT="$tmp" bash "$ROOT/scripts/check-unmeasured-reach.sh" 2>&1)" || rc=$?
  }

  # T1 — a producer that does not carry the token is REFUSED, by name.
  printf '#!/usr/bin/env bash\nif [ -z "$TOOL" ]; then echo "could not measure"; exit 3; fi\n' \
    >"$tmp/scripts/check-fabricated.sh"
  _t
  if [ "$rc" -eq 0 ] || ! grep -qF 'scripts/check-fabricated.sh' <<<"$out"; then
    echo "unmeasured-reach --teeth: FAILED — T1: a producer with no carrier stayed green (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T1 — a gate that can exit 3 without printing the token is refused by name"
  fi

  # T2 — AND T1 IS NOT VACUOUS. The same file, routed through gate-lib, passes.
  printf '#!/usr/bin/env bash\nsource "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"\nif [ -z "$TOOL" ]; then gate_unmeasured "the tool is absent, so nothing was scanned"; exit 3; fi\n' \
    >"$tmp/scripts/check-fabricated.sh"
  _t
  if [ "$rc" -ne 0 ]; then
    echo "unmeasured-reach --teeth: FAILED — T2: the same gate WITH gate_unmeasured must pass, or T1 proved nothing (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T2 — the same gate routed through gate_unmeasured passes, so T1 is about the carrier and not about the file"
  fi

  # T3 — a workflow that retries without saying what did not happen is REFUSED.
  printf 'jobs:\n  x:\n    steps:\n      - run: |\n          for attempt in 1 2 3; do curl -fsSL "$u" && break; done\n' \
    >"$tmp/.github/workflows/fetcher.yml"
  _t
  if [ "$rc" -eq 0 ] || ! grep -qF 'fetcher.yml' <<<"$out"; then
    echo "unmeasured-reach --teeth: FAILED — T3: a retry loop with no UNMEASURED text stayed green (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T3 — a workflow that retries a download without saying the check never ran is refused"
  fi

  # T4 — and T3 clears once the token is there.
  printf 'jobs:\n  x:\n    steps:\n      - run: |\n          for attempt in 1 2 3; do curl -fsSL "$u" && break; done\n          echo "::error::UNMEASURED: could not fetch; the scan DID NOT RUN"\n' \
    >"$tmp/.github/workflows/fetcher.yml"
  _t
  if [ "$rc" -ne 0 ]; then
    echo "unmeasured-reach --teeth: FAILED — T4: a retry loop that DOES say what did not happen must pass (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T4 — the same workflow passes once it names what did not happen"
  fi

  # T5 — AN ABSENT-TOOL GUARD WITH NO EXCUSE IS REFUSED, and T6 is its inverse.
  # Together they are check 4: the class check 1 is structurally blind to,
  # because a gate that never says the word leaves nothing to grep for.
  # SPLIT ACROSS TWO SOURCE LINES ON PURPOSE. Check 4 judges a single source
  # line, and this gate's own teeth write a fixture carrying exactly the shape
  # it polices — so as one line, the gate refused ITSELF, pointing at its own
  # tooth. The gate has no absent-tool guard; it WRITES a fixture that does,
  # and the text should not claim otherwise. Same move as verify.sh made for
  # classify-guard, for the same reason.
  printf '#!/usr/bin/env bash\ncommand -v widget >/dev/null 2>&1 || {' >"$tmp/scripts/check-widget.sh"
  printf ' echo "gate: FAIL — widget missing"; exit 1; }\n' >>"$tmp/scripts/check-widget.sh"
  _t
  if [ "$rc" -eq 0 ] || ! grep -qF 'treats an ABSENT TOOL as a measured failure' <<<"$out"; then
    echo "unmeasured-reach --teeth: FAILED — T5: an absent-tool guard whose failure arm exits non-zero must be refused (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T5 — a gate that reports an absent tool as a measured FAILURE is refused; nothing ran, so nothing was found"
  fi

  # T6 — and the same guard routed through gate_unmeasured passes, so T5 is
  # about the verdict and not about the `command -v`.
  printf '#!/usr/bin/env bash\nsource "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"\ncommand -v widget >/dev/null 2>&1 || { gate_unmeasured "widget is absent, so nothing was checked"; exit "$GATE_EXIT_UNMEASURED"; }\n' \
    >"$tmp/scripts/check-widget.sh"
  _t
  if [ "$rc" -ne 0 ]; then
    echo "unmeasured-reach --teeth: FAILED — T6: the same guard via gate_unmeasured must pass, or T5 proved nothing (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T6 — the same guard via gate_unmeasured passes, so T5 is about the verdict and not about the guard"
  fi

  # T7 — AN EXCUSE THAT EXCUSES NOTHING IS REFUSED. The arm that keeps both
  # exemption lists honest, and the one a reader would otherwise never check.
  # Pointed at the absent-tool list because the UNMEASURED list is empty, which
  # is its correct state — its one entry went dead within hours of being
  # written and was removed rather than kept.
  cp "$ROOT/scripts/dev-install.sh" "$tmp/scripts/dev-install.sh"
  rm -f "$tmp/scripts/check-widget.sh"
  _t
  if [ "$rc" -ne 0 ]; then
    echo "unmeasured-reach --teeth: FAILED — T7 setup: an excused absent-tool guard must be green before it goes stale (rc=$rc)" >&2; echo "$out" >&2; fail=1
  fi
  # STALE means the file is STILL HERE and no longer needs excusing — not that
  # it is missing. A missing file is a stripped one, and the gate says so above.
  printf '#!/usr/bin/env bash\necho "no guard here any more"\n' >"$tmp/scripts/dev-install.sh"
  _t
  if [ "$rc" -eq 0 ] || ! grep -qF 'no longer treats an absent tool as a failure' <<<"$out"; then
    echo "unmeasured-reach --teeth: FAILED — T7: a standing excuse for a guard that is gone stayed green (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T7 — an excuse whose guard is gone is refused, so an exemption list cannot quietly stop being read"
  fi

  [ "$fail" -eq 0 ] || { echo "unmeasured-reach --teeth: FAIL" >&2; exit 1; }
  echo "unmeasured-reach --teeth: 7 case(s) green."
}

case "${1:-check}" in
  check) run_check ;;
  --teeth) run_teeth ;;
  *) echo "usage: $0 [check|--teeth]" >&2; exit 2 ;;
esac
