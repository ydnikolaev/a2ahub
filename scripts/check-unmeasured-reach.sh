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
#   3. A NEW SHAPE OF NOT-MEASURING that uses none of the tokens below. Check 2
#      narrows the one shape that has actually bitten (a download loop that
#      exhausts its retries), but a fourth costume would be invisible until
#      somebody meets it.
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
EXCUSED=(
  "scripts/verify.sh|a CONSUMER of the verdict, not a producer of one: run_phase reads GATE_EXIT_UNMEASURED to file a phase as 'unmeasured' in telemetry rather than as 'fail'. It emits no verdict of its own, and the gate it ran has already printed the annotation"
)

excuse_for() { # $1 = repo-relative path; echoes the reason, or returns 1
  local e
  for e in "${EXCUSED[@]}"; do
    [ "${e%%|*}" = "$1" ] || continue
    printf '%s\n' "${e#*|}"
    return 0
  done
  return 1
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

  # ── check 3 — an excuse must still excuse something ────────────────────────
  #
  # A permanent exemption for a file that no longer produces an unmeasured
  # verdict is how a list stops being read. Refuse it, the same way
  # lane-ungated.txt is COUNTED rather than merely allowed.
  local e arm all
  all="$(producers)"
  for e in "${EXCUSED[@]}"; do
    arm="${e%%|*}"
    grep -qxF "$arm" <<<"$all" && continue
    gate_fail "unmeasured-reach: EXCUSED carries an entry for $arm, which is no longer an UNMEASURED producer. Remove the entry; an exemption nobody re-examines is how a list stops being read."
  done

  printf 'unmeasured-reach: %s producer(s) — %s carry the token, %s excused by name; %s workflow(s) retry something and all of them say what did not happen.\n' \
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

  # T5 — AN EXCUSE THAT EXCUSES NOTHING IS REFUSED. This is the arm that keeps
  # the exemption list honest, and it is the one a reader would otherwise never
  # check: remove verify.sh and its standing excuse becomes a claim about a
  # file that is not there.
  rm -f "$tmp/scripts/verify.sh"
  git -C "$tmp" rm -q --cached scripts/verify.sh >/dev/null 2>&1
  _t
  if [ "$rc" -eq 0 ] || ! grep -qF 'no longer an UNMEASURED producer' <<<"$out"; then
    echo "unmeasured-reach --teeth: FAILED — T5: a standing excuse for an absent producer stayed green (rc=$rc)" >&2; echo "$out" >&2; fail=1
  else
    echo "unmeasured-reach --teeth: T5 — an excuse whose producer is gone is refused, so the exemption list cannot quietly stop being read"
  fi

  [ "$fail" -eq 0 ] || { echo "unmeasured-reach --teeth: FAIL" >&2; exit 1; }
  echo "unmeasured-reach --teeth: 5 case(s) green."
}

case "${1:-check}" in
  check) run_check ;;
  --teeth) run_teeth ;;
  *) echo "usage: $0 [check|--teeth]" >&2; exit 2 ;;
esac
