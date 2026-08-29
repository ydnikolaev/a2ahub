#!/usr/bin/env bash
# docs/features/archive/operational-confidence-2026-08/audits/v0.19.1-provider-tier-deferral.md
# §"Scheduled follow-up" promises, in prose only, that the next release
# changing runtime/host/funnel/validator/authorization does not ship on the
# `logic-proven, provider-deferred` tier again until a provider-tier
# (live-e2e) run has happened. Two consecutive deferral records already
# exist; a third one added without an intervening live-e2e run in between
# would make that promise — and the known-issue text that repeats it — false.
# This gate makes the promise machine-refusable instead of just written down.
#
# The deferral-record glob below is `audits/**/` , not `audits/*` : the honesty
# check derives what find_deferral_records() (line ~76) can actually return by
# intersecting its walk root with its own `-path '*/audits/*' -name
# '*-provider-tier-deferral.md'` filters, and that intersection reaches records
# nested deeper than one level under an audits/ directory. An earlier revision
# instead declared the bare walk roots ("docs/features" and ".") to satisfy the
# checker; that was a declaration bent to fit a tool, which is the failure this
# phase exists to stop. The extractor was made precise instead.
#
# docs/features/archive/agent-ops-2026-07/audits/v0.20.0-provider-tier-deferral.md
# §"The count is 10 records, and there is at least one release it does not
# include" found the defect this revision fixes: the gate counted deferral
# RECORDS, and a release that simply omits the file was invisible to it —
# omitting the record was easier than writing it, so the streak the gate
# reported was only a lower bound. v0.19.11 shipped with no deferral record
# and no live-e2e evidence newer than it, and the record-counting gate said
# "ok". The unit of judgment is now the RELEASE, read from releasenotes/*.yaml:
# every released version must carry EITHER a deferral record named for it OR
# live-e2e evidence that covers it, and a release with neither is refused
# unconditionally — that refusal is NOT gated behind the streak-of-3 threshold
# below, because the streak-acknowledgement signature attests "we deferred
# again, on purpose"; a release with no record never made that decision on
# paper, and there is nothing for a count-signature to sign.

# lane-inputs:
#   docs/features/**/audits/**/*-provider-tier-deferral.md
#   **/audits/live-e2e-*
#   **/audits/live-e2e-*/**
#   releasenotes/**/*.yaml
# lane-reads-opaque: find_live_e2e_artifacts() (line ~84) walks the repo root
#   with `\( -path './.git' -o -name node_modules \) -prune -o -path '*/audits/*'
#   … -name 'live-e2e-*'`. That expression MIXES exclusion with selection, and
#   the classifier will not guess which arms select — the two globs above are
#   what it can actually return, verified by reading the invocation.
set -euo pipefail

# The override a third-or-later consecutive deferral must carry to ship.
#
# It is matched as a line in the newest outstanding record, so the signature
# lands in the same artifact that already carries the tier's per-candidate
# authorization — one file a later reader opens, not two. The count is part of
# the marker on purpose: "3rd" cannot be written once and inherited silently by
# a 4th, because the gate compares it to the number it actually counted.
# The trailing context is OPTIONAL: a line ending immediately after the
# number is a signature too, and requiring whitespace after the digits made
# the gate refuse a correctly-signed record for a reason it did not name.
# Fail-closed, but wrong-reason — the class this gate exists to end.
OVERRIDE_MARKER_RE='^consecutive-deferral-acknowledged:[[:space:]]*[0-9]+([[:space:]]|$)'
OVERRIDE_MARKER_EXAMPLE='consecutive-deferral-acknowledged: <N> — <who authorized shipping the Nth in a row, and why waiting was judged the larger risk>'

# THE SIGNATURE MUST NAME WHAT IT SIGNS FOR. Two more parseable lines, siblings
# of the marker above, in the same record — not a new mechanism.
#
# THE INCIDENT, MEASURED. v0.23.0's deferral record was authored 2026-08-14 and
# signed for a candidate carrying, in its own words, "One change:
# DeliverDataPackage looks its transport driver up in the registry", stating the
# candidate "does NOT touch the surfaces that put v0.22.0 in the mandatory
# table" and that "nothing needs it in the field". **v0.23.0 shipped 44
# release-note entries. The record named ZERO of them.** By ship date it carried
# two whole epics, a write-funnel validation change on both write surfaces and a
# behaviour break. This gate — the one standing between a candidate and a
# release cut with NO live provider evidence — was green throughout, and still
# green after a human rewrote the record by hand on 2026-08-20. It never
# participated in either direction, because it read presence, a filename and one
# number, and never opened the record's prose.
#
# THIS DOES NOT INTRODUCE A DISCIPLINE; IT MAKES AN EXISTING ONE CHECKABLE.
# v0.19.1's record already names the candidate SHA, the tree, the transcript's
# own SHA-256 and the logic-tier digest as concrete values, by hand. v0.24.0's
# already carries the SHA, the tree, the transcript path and a "what changed →
# which unproven row covers it" table. `LOGIC_TIER_ROWS_SHA256` is the closest
# ancestor in the runbooks: a digest of what was TESTED, travelling beside the
# claim so a later reader can tell the catalogue moved. `covers-notes:` is the
# same idea applied to what was AUTHORIZED.
#
# THE GAP, STATED RATHER THAN PAPERED OVER. A change that ships carrying NO
# release-note entry is invisible to this binding. The notes are the release's
# own machine-readable statement of what it carries and the SSOT three other
# readers already use, but their completeness is the notes corpus's own
# invariant (`release-notes-freshness`, `release-record`), not this gate's. A
# green here means "the record names everything the notes declare" — never "the
# record describes the diff".
#
# THE VALUE IS IDS AND NOTHING ELSE — no prose tail. The streak marker above
# ends in "— <who authorized …>", and that shape must NOT be copied here: with a
# tail allowed, every word of it becomes a phantom id and the set comparison
# stops meaning anything. An unrecognized token is reported verbatim as
# named-but-not-shipped, which is the honest reading of a typo.
#
# EXACTLY ONE LINE. The range form covers 44 ids in 24 characters, so a second
# line buys nothing and would reintroduce the ambiguity the streak marker's own
# history already paid for (a corrected signature APPENDED instead of edited —
# see case 8).
#
# THE RANGE FORM IS WEAKER THAN THE LIST, AND THE DIFFERENCE IS WORTH KNOWING.
# `A..B` is resolved by POSITION in the notes file's own declared order (never
# by arithmetic on the id — the corpus has no single scheme). An entry APPENDED
# after B therefore reds, which is the case that matters; but an entry INSERTED
# between A and B after signing is covered silently. An enumeration cannot be
# grown behind the operator's back, so a record that can afford the characters
# should enumerate — v0.24.0's does, and says why.
COVERS_MARKER_RE='^covers-notes:[[:space:]]*[^[:space:]]'
COVERS_MARKER_EXAMPLE='covers-notes: RN-02400-0, RN-02400-1, RN-02400-2'
COVERS_MARKER_RANGE_EXAMPLE='covers-notes: RN-02400-0..RN-02400-9'

# The exact published candidate the record governs. Checked for PRESENCE only,
# deliberately: there is no promoted SHA to compare against offline, and the
# field is informational until release time, when the runbook's own three-object
# assertion (candidate SHA == public ref SHA == tree hash, in
# docs/runbooks/publish-to-public.sh) is what compares it.
#
# NO SHAPE CHECK, ON PURPOSE. A 40-hex regex would refuse v0.24.0's own honest
# form, which names the candidate branch `a2a-candidate/1bd62f3a1e85` beside the
# full SHA — that is A1's "never parse the scheme" trap, one field over.
CANDIDATE_SHA_MARKER_RE='^candidate-sha:[[:space:]]*[^[:space:]]'
CANDIDATE_SHA_EXAMPLE='candidate-sha: 1bd62f3a1e85dff151f5f798b674c422ef8f123f'

# The unix timestamp a path was first ADDED to git, or empty if the path has
# never been committed (still untracked / only staged). Filenames are not
# comparable across the two kinds of artifact this gate orders (one carries a
# VERSION, the other a DATE), so git history — not the filename, not mtime —
# is the only honest clock.
added_at() {
  git log --diff-filter=A --format=%ct -1 -- "$1" 2>/dev/null || true
}

# Every release note on disk, in ascending semver order, as
# "<version><TAB><path>". Disk-based `find`, NOT `git ls-files`: this must see
# a release note that has not been committed yet, for the same reason
# find_deferral_records() below is disk-based — a release-cutting flow runs
# this gate BEFORE committing the release it is about to ship, and the
# missing-record case that matters most is the one about to happen, not one
# already sitting in history. (check-release-notes-freshness.sh's
# latest_notes_file() uses `git ls-files` for the same directory, but that gate
# only ever needs the newest ALREADY-COMMITTED note as an anchor; it has no
# pre-commit promise to keep.)
#
# The zero-padded sort key is the same idiom latest_notes_file() uses, for the
# same reason: plain lexical sort puts "0.19.10.yaml" before "0.19.2.yaml",
# which is wrong by an order of magnitude the moment a patch number reaches two
# digits — the exact boundary the "newest record" lookup two functions below
# already had to be fixed for once, at v0.19.10.
# A notes file whose `released:` is the pre-tag SENTINEL is not a release, and
# this function used to say otherwise. It enumerated by FILENAME alone, so
# `releasenotes/0.24.0.yaml` — authored ahead of its tag, carrying
# `released: unreleased` — was reported as "v0.24.0 (RELEASED — no deferral
# record)". It had shipped nothing. Nothing had been deferred, because nothing
# had been cut.
#
# The sentinel is not invented here. It is `internal/notes.UnreleasedSentinel`,
# it is in `schemas/release-notes/v1/release-notes.schema.json`, and
# `scripts/check-release-record.sh`'s own header states its purpose in one
# line: it "keeps that window honest without blocking authoring ahead of the
# tag". Authoring notes before the tag is the documented flow
# (docs/runbooks/release.md Phase 1 step 1), so a gate that treats the act of
# authoring as the act of shipping punishes the flow the runbook prescribes.
#
# Nothing is weakened by skipping it. `release-preflight.sh`'s
# `assert_notes_carry_a_date` REFUSES the sentinel at cut time, so a version
# cannot be tagged while wearing it; the moment it gains a date this gate
# demands the record, which is exactly when an operator is present to sign
# one. The obligation moves to the boundary that can discharge it, and does
# not disappear.
#
# Found 2026-08-21 by judge-the-thing-2026-08's own lane, which is the epic
# about a verdict read off a claim instead of the thing it stands for. This
# was one: the claim was a filename, the thing was whether the version shipped.
find_release_versions() {
  find releasenotes -maxdepth 1 -type f -name '*.yaml' 2>/dev/null |
    awk -F'[/.]' '
      $2 ~ /^[0-9]+$/ && $3 ~ /^[0-9]+$/ && $4 ~ /^[0-9]+$/ && $5 == "yaml" {
        printf "%012d.%012d.%012d\t%s.%s.%s\t%s\n", $2, $3, $4, $2, $3, $4, $0
      }
    ' |
    sort |
    cut -f2- |
    while IFS="$(printf '\t')" read -r ver file; do
      # The skip is ANNOUNCED, never silent: a gate that quietly stops
      # covering a version is the shape this whole epic exists to remove.
      if grep -qE '^released:[[:space:]]*"?unreleased"?[[:space:]]*$' "$file" 2>/dev/null; then
        echo "provider-tier-deferral: not yet a release — v$ver carries the pre-tag sentinel (released: unreleased); its deferral record is due when the tag is cut." >&2
        continue
      fi
      printf '%s\t%s\n' "$ver" "$file"
    done
}

find_deferral_records() {
  find docs/features -path '*/audits/*' -type f -name '*-provider-tier-deferral.md' 2>/dev/null | sort
}

# "any audits/ directory" — not just docs/features/**/audits/ — since a
# live-e2e evidence artifact clearing a deferral could in principle land
# under a differently rooted audits/ tree; .git and node_modules are pruned
# so the walk stays cheap and irrelevant vendored trees never match.
find_live_e2e_artifacts() {
  find . \( -path './.git' -o -name node_modules \) -prune -o \
    -path '*/audits/*' \( -type f -o -type d \) -name 'live-e2e-*' -print \
    2>/dev/null | sed 's#^\./##' | sort
}

# The deferral record whose filename names this exact release, matched
# against the list of paths passed in $2..$N (one call per lookup already
# has find_deferral_records() re-walked disk; the caller hoists it once and
# passes the list so N releases cost one walk, not N). Echoes the path, or
# nothing. `return 0` is explicit and unconditional: under `set -e`, a
# function whose last command is a failed `case` match would otherwise exit
# non-zero and kill the script from inside a `recpath="$(...)"` command
# substitution — the exact mute-failure class the override lookup further
# down already carries a comment warning about.
deferral_record_for_version() {
  local version="$1" rec
  shift
  for rec in "$@"; do
    [ -n "$rec" ] || continue
    case "$(basename "$rec")" in
      "v${version}-provider-tier-deferral.md")
        echo "$rec"
        return 0
        ;;
    esac
  done
  return 0
}

# True if some live-e2e artifact's own filename names this release
# (…-v0.18.2-…, …-v0.15.0-release-gate.txt…). This is the fallback the global
# `newest_live` comparison in check_provider_tier_deferral() cannot give: that
# comparison asks "did ANY live-e2e run happen after this release's note was
# committed", which is false — and wrongly outstanding — for a release whose
# OWN evidence was recorded before its own note commit (verify-then-cut, not
# cut-then-verify) while a still-newer unrelated artifact does not exist to
# rescue it. A name match is direct evidence for the one release it names,
# independent of where it falls in the global timeline.
live_e2e_names_version() {
  local version="$1" art
  while IFS= read -r art; do
    [ -n "$art" ] || continue
    case "$(basename "$art")" in
      *"v${version}"*) return 0 ;;
    esac
  done <<<"$(find_live_e2e_artifacts)"
  return 1
}

# Every release-note id a notes file DECLARES, one per line, in the file's own
# declared order.
#
# THE ONE THING NOT TO GET WRONG (spec §11 A1): this NEVER parses the id scheme.
# `id` is a bare {"type": "string"} in
# schemas/release-notes/v1/release-notes.schema.json, and the RN-<versionkey>-<n>
# shape is convention only — it is NOT consistent across the corpus. RN-0900-1,
# RN-01200-1, RN-01900-0 and RN-02300-36 do not share a padding, so a regex like
# RN-\d{5}-\d+ reds on v0.9.0's own notes. What comes out of here is a list of
# literal strings, compared as strings; the gate never needs to know what an id
# means. The indentation tolerance is the same story from the other side: older
# files indent the sequence under `changes:` by two spaces, newer ones do not,
# and both are valid YAML.
notes_ids() {
  awk '
    /^[ \t]*-[ \t]+id:[ \t]*./ {
      sub(/^[ \t]*-[ \t]+id:[ \t]*/, "")
      sub(/[ \t\r]+$/, "")
      sub(/^"/, ""); sub(/"$/, "")
      sub(/^\047/, ""); sub(/\047$/, "")
      if (length($0)) print
    }
  ' "$1"
}

# The 1-based position of an id inside a newline-delimited list, or empty when
# the list does not contain it. `|| true` covers both grep's no-match exit and a
# SIGPIPE from the producing printf; a bare failure inside `$( )` under `set -e`
# would kill the script mid-refusal, which is the mute-gate failure this file
# already carries two warnings about.
id_position() {
  local hit
  hit="$(printf '%s\n' "$1" | grep -m1 -nxF -e "$2" || true)"
  [ -n "$hit" ] || return 0
  printf '%s' "${hit%%:*}"
}

# The core of this phase: does the record that signs for a release NAME what it
# signs for? A SET COMPARISON between the id strings the notes file declares and
# the id strings the record names — in both directions.
#
# Returns 1 with a refusal on any of: an unreadable or id-less notes file
# (measurement-failed, NOT a domain verdict), a missing/duplicated/placeholder
# `covers-notes:` line, an unresolvable or inverted range, a shipped id the
# record does not name, a named id the release does not ship, or a
# missing/placeholder `candidate-sha:`.
check_record_covers_release() {
  local record="$1" notes="$2" version="$3"

  # MEASUREMENT BEFORE VERDICT. A gate that could not read its input must say
  # THAT, and must not answer the domain question in either direction — green
  # would be a claim it did not earn, and a domain FAIL would blame the record
  # for the reader's failure.
  if [ ! -r "$notes" ]; then
    echo "provider-tier-deferral: measurement-failed — $notes exists in the release list but could not be read, so what v$version carries is unknown." >&2
    echo "This is NOT a verdict about $record. The gate could not measure; it will not guess, and 'could not measure' is never green." >&2
    return 1
  fi
  local shipped
  shipped="$(notes_ids "$notes")"
  if [ -z "$shipped" ]; then
    echo "provider-tier-deferral: measurement-failed — $notes declares no release-note ids (empty, truncated, or not a release-notes/v1 document)." >&2
    echo "The schema requires at least one entry under changes:, each with an id. There is nothing to compare $record against, so the" >&2
    echo "coverage of v$version is UNJUDGED — which is not the same as covered. Fix the notes file." >&2
    return 1
  fi
  local shipped_count
  shipped_count="$(printf '%s\n' "$shipped" | wc -l | tr -d '[:space:]')"

  local covers_count
  covers_count="$(grep -cE "$COVERS_MARKER_RE" "$record" || true)"
  [ -n "$covers_count" ] || covers_count=0

  if [ "$covers_count" -eq 0 ]; then
    echo "provider-tier-deferral: FAIL — $record signs for v$version but never says what it signs for: it carries no covers-notes: line," >&2
    echo "while v$version ships $shipped_count release-note entries. Named: 0 of $shipped_count." >&2
    printf '%s\n' "$shipped" | sed 's/^/  - /' >&2
    echo "" >&2
    echo "Add exactly one line naming the entries the operator was shown before signing:" >&2
    echo "  $COVERS_MARKER_EXAMPLE" >&2
    echo "or, when the release is contiguous, the inclusive-range form (resolved against the notes file's own order):" >&2
    echo "  $COVERS_MARKER_RANGE_EXAMPLE" >&2
    echo "and, beside it, the candidate the record governs:" >&2
    echo "  $CANDIDATE_SHA_EXAMPLE" >&2
    echo "" >&2
    echo "This is not a new discipline: v0.19.1's record already names the candidate SHA, the tree, the transcript's own SHA-256 and" >&2
    echo "the logic-tier digest as concrete values, by hand, and v0.24.0's carries the SHA, the tree and a what-changed table. These" >&2
    echo "lines make that voluntary practice checkable — the release-machinery twin of LOGIC_TIER_ROWS_SHA256, which already travels" >&2
    echo "beside a transcript so a reader can tell the catalogue moved. v0.23.0 is why: its record described 'one change', the release" >&2
    echo "shipped 44 entries, and this gate was green the whole time." >&2
    return 1
  fi

  if [ "$covers_count" -gt 1 ]; then
    echo "provider-tier-deferral: FAIL — $record carries $covers_count covers-notes: lines; exactly one must be in force." >&2
    grep -nE "$COVERS_MARKER_RE" "$record" | sed 's/^/    /' >&2
    echo "" >&2
    echo "Correcting the coverage means EDITING the line, not adding a second one — the same rule the streak signature learned the hard" >&2
    echo "way. One line is enough for any release: the inclusive-range form covers 44 ids in 24 characters." >&2
    return 1
  fi

  local covers_line
  covers_line="$(grep -E "$COVERS_MARKER_RE" "$record" || true)"
  if printf '%s' "$covers_line" | grep -q '<[^>]*>'; then
    echo "provider-tier-deferral: FAIL — $record's covers-notes: line still contains a <placeholder>:" >&2
    printf '%s\n' "$covers_line" | sed 's/^/    /' >&2
    echo "" >&2
    echo "That is the documented EXAMPLE shape, not a statement of coverage. A record that quotes the instruction has named nothing." >&2
    return 1
  fi

  # Tokens: comma- and/or whitespace-separated. `A..B` is an inclusive range,
  # resolved by POSITION in the notes file's own declared order — never by
  # arithmetic on the id, which the corpus does not support (§11 A1).
  local value
  value="${covers_line#covers-notes:}"
  local -a tokens=()
  IFS=$', \t' read -r -a tokens <<<"$value"

  local -a named=()
  local tok start end pstart pend missing_ends rid
  for tok in ${tokens[@]+"${tokens[@]}"}; do
    [ -n "$tok" ] || continue
    case "$tok" in
      *..*)
        start="${tok%%..*}"
        end="${tok##*..}"
        pstart="$(id_position "$shipped" "$start")"
        pend="$(id_position "$shipped" "$end")"
        if [ -z "$pstart" ] || [ -z "$pend" ]; then
          missing_ends=""
          [ -n "$pstart" ] || missing_ends="'$start'"
          [ -n "$pend" ] || missing_ends="${missing_ends:+$missing_ends and }'$end'"
          echo "provider-tier-deferral: FAIL — $record names the inclusive range '$tok', but $notes declares no id $missing_ends." >&2
          echo "A range is resolved against the notes file's OWN declared order, so both endpoints must be ids v$version actually ships." >&2
          echo "The gate does not compute ids: the corpus has no single scheme (RN-0900-1, RN-01200-1, RN-01900-0 all differ)." >&2
          return 1
        fi
        if [ "$pstart" -gt "$pend" ]; then
          echo "provider-tier-deferral: FAIL — $record names the range '$tok' backwards: '$start' is entry $pstart of $notes and '$end' is entry $pend." >&2
          echo "An inclusive range must be written low..high in the notes file's own order; read backwards it would silently cover NOTHING." >&2
          return 1
        fi
        while IFS= read -r rid; do
          [ -n "$rid" ] || continue
          named+=("$rid")
        done <<<"$(printf '%s\n' "$shipped" | sed -n "${pstart},${pend}p")"
        ;;
      *)
        named+=("$tok")
        ;;
    esac
  done

  # The set comparison, in BOTH directions. An EMPTY named set is handled
  # explicitly rather than through `grep -f`: an empty pattern file is one of
  # the few places GNU and BSD grep have historically disagreed about what -v
  # means, and "the record named nothing" is precisely the case this gate exists
  # for — it must not depend on which userland is running (see the GNU/BSD find
  # divergence that left check-view-vocabulary.sh inert in CI for a week).
  local named_file shipped_file missing extra
  named_file="$(mktemp)"
  shipped_file="$(mktemp)"
  printf '%s\n' ${named[@]+"${named[@]}"} | grep -v '^$' | sort -u >"$named_file" || true
  printf '%s\n' "$shipped" >"$shipped_file"
  if [ -s "$named_file" ]; then
    missing="$(grep -vxF -f "$named_file" "$shipped_file" || true)"
    extra="$(grep -vxF -f "$shipped_file" "$named_file" || true)"
  else
    missing="$shipped"
    extra=""
  fi
  rm -f "$named_file" "$shipped_file"

  # ONE refusal reporting BOTH directions. They were two sequential refusals
  # until a mutation showed what that costs: swapping a single digit in a real
  # record's list (RN-02400-3 → RN-02400-99) reported only the entry left
  # uncovered and said nothing about the id that does not exist, sending the
  # author to look for a missing change rather than at their own typo. An id
  # swap is the most likely mistake in this field, and it is exactly the case
  # where both halves are needed to read the refusal.
  if [ -n "$missing" ] || [ -n "$extra" ]; then
    local missing_count=0 extra_count=0 named_count
    [ -z "$missing" ] || missing_count="$(printf '%s\n' "$missing" | wc -l | tr -d '[:space:]')"
    [ -z "$extra" ] || extra_count="$(printf '%s\n' "$extra" | wc -l | tr -d '[:space:]')"
    named_count=$(( shipped_count - missing_count ))
    echo "provider-tier-deferral: FAIL — $record signs for v$version, which ships $shipped_count release-note entries; the record names $named_count of them." >&2
    if [ -n "$missing" ]; then
      echo "$missing_count entr(y/ies) the signature does NOT cover:" >&2
      printf '%s\n' "$missing" | sed 's/^/  - /' >&2
      echo "" >&2
      echo "A signature is given for what the operator was SHOWN. When the candidate outgrows it, the authorization is silently inherited" >&2
      echo "by a release nobody saw — which is exactly v0.23.0: a record describing 'one change' and 'nothing needs it in the field',"  >&2
      echo "against a release that shipped 44 entries, two epics and a behaviour break, with this gate green throughout." >&2
      echo "Re-read the candidate and extend covers-notes: in $record to name the entries above, or re-sign it for the release it now is." >&2
    fi
    if [ -n "$extra" ]; then
      [ -z "$missing" ] || echo "" >&2
      echo "$extra_count id(s) the record names that $notes does not declare:" >&2
      printf '%s\n' "$extra" | sed 's/^/  - /' >&2
      echo "" >&2
      echo "A record claiming coverage of an entry the release does not carry was copied from another release, or carries a typo." >&2
      echo "The value of covers-notes: is ids and NOTHING else — no prose tail — so any stray word arrives here verbatim." >&2
    fi
    return 1
  fi

  local sha_count
  sha_count="$(grep -cE "$CANDIDATE_SHA_MARKER_RE" "$record" || true)"
  [ -n "$sha_count" ] || sha_count=0
  if [ "$sha_count" -eq 0 ]; then
    echo "provider-tier-deferral: FAIL — $record names what it covers but not the candidate it governs: no candidate-sha: line." >&2
    echo "Add, beside covers-notes::" >&2
    echo "  $CANDIDATE_SHA_EXAMPLE" >&2
    echo "The runbook's condition 1 is an authorization 'for this candidate'. Without the SHA, a later reader cannot tell WHICH candidate" >&2
    echo "was on the operator's screen, and the promotion step's own three-object assertion (candidate SHA == public ref SHA == tree hash)" >&2
    echo "has nothing written down to assert against. The shape is not checked here — the short candidate-branch form is honest too." >&2
    return 1
  fi
  if [ "$sha_count" -gt 1 ]; then
    echo "provider-tier-deferral: FAIL — $record carries $sha_count candidate-sha: lines; exactly one candidate is governed." >&2
    grep -nE "$CANDIDATE_SHA_MARKER_RE" "$record" | sed 's/^/    /' >&2
    return 1
  fi
  if grep -E "$CANDIDATE_SHA_MARKER_RE" "$record" | grep -q '<[^>]*>'; then
    echo "provider-tier-deferral: FAIL — $record's candidate-sha: line still contains a <placeholder>:" >&2
    grep -E "$CANDIDATE_SHA_MARKER_RE" "$record" | grep '<[^>]*>' | sed 's/^/    /' >&2
    echo "" >&2
    echo "That is the documented EXAMPLE shape, not a candidate. Paste the SHA the operator actually signed against." >&2
    return 1
  fi

  echo "provider-tier-deferral: $record names all $shipped_count of v$version's release-note ids, and the candidate it governs."
}

check_provider_tier_deferral() {
  local artifact rec ts newest_live=0
  local -a uncleared=()
  local -a uncleared_records=()
  # Parallel to uncleared_records, appended at the SAME site so index N is the
  # notes file and version of the record at index N. It exists so the coverage
  # binding can reach the notes of the release whose record is being judged,
  # WITHOUT a second notion of "newest" — the display strings in `uncleared`
  # are annotated prose and were opened as a filename once already (case 5b).
  local -a uncleared_notes=()
  local -a uncleared_versions=()
  local -a recordless=()

  # THE EVIDENCE CORPUS MUST BE PRESENT, OR THIS GATE HAS NOTHING TO JUDGE.
  #
  # This script is PUBLIC and ships to the published candidate; the deferral
  # records and live-e2e artifacts it reads live under docs/, which
  # publish-to-public.sh STRIPS from every published commit. So in a public
  # checkout the gate sees release notes and no evidence of any kind.
  #
  # That asymmetry used to be harmless in the worst possible way: while this
  # gate counted RECORDS, zero records meant zero outstanding and it printed
  # "ok — 0 outstanding" — a confident green about a streak it could not see,
  # which the v0.20.0 audit had already called out as misleading. Re-keying it
  # to RELEASES turned the same blindness into a candidate-breaking RED:
  # releasenotes/ survives the publish, so every published release would read
  # as "shipped with no evidence" and `make check` on the candidate would fail
  # for a reason that exists only in the checkout.
  #
  # Neither answer is honest. A gate whose inputs are absent must say so and
  # decline — the same shape as the presence-gated private harness gates in
  # the Makefile, moved inside the script because here it is the DATA that is
  # stripped, not the script.
  if [ ! -d docs/features ]; then
    echo "provider-tier-deferral: skip — docs/features absent (public checkout); the deferral records and live-e2e evidence this gate judges are stripped at publish, so this checkout cannot see the streak and will not guess at it."
    return 0
  fi

  # A SHALLOW CLONE CANNOT ANSWER THIS QUESTION EITHER, and it used to answer
  # it anyway — confidently, and wrong.
  #
  # This gate orders artifacts by when git first saw them (added_at, below),
  # because filenames carry a VERSION for one kind and a DATE for the other
  # and are not comparable. With `actions/checkout`'s default fetch-depth of
  # 1 there is exactly one commit and no parent, so `git log --diff-filter=A`
  # reports EVERY tracked path as added in it, at the same timestamp. Every
  # release then reads as "not newer than the last live-e2e evidence" and is
  # skipped, and the gate prints "ok — 0 release(s) outstanding".
  #
  # Observed live on 2026-08-13, in the first CI run of the lane job P13 K4
  # had just wired: this gate reported 0 outstanding in a repository whose
  #real answer was 14. That is the same false-affirmative shape as the stripped
  # public checkout above, reached by a different missing input, and it is
  # worse than the red it replaced because nobody investigates a green.
  # `--is-shallow-repository` is the WRONG test and was tried first: this
  # project's own working tree answers "true" while carrying 1409 commits, so
  # keying on it disabled the gate exactly where it is supposed to refuse. The
  # discriminator is whether there is any history to order BY.
  local depth
  depth="$(git rev-list --count HEAD 2>/dev/null || echo 0)"
  if [ "${depth:-0}" -le 1 ]; then
    echo "provider-tier-deferral: skip — single-commit history (actions/checkout's default depth); this gate orders evidence by git add-date, and with no parent commit EVERY path reads as added at the same moment, which silently reports zero outstanding. Use fetch-depth: 0 to judge the streak here."
    return 0
  fi

  # Find the most recently ADDED live-e2e evidence artifact. An untracked
  # artifact (no git add-date) is treated as though it does not exist yet
  # (timestamp 0): this gate follows committed git history, not the working
  # tree, so a staged-but-uncommitted "evidence" file can never silently
  # clear outstanding releases — the one direction that would let a third
  # deferral ship unrefused. This half of the untracked rule is the
  # fail-SAFE one.
  while IFS= read -r artifact; do
    [ -n "$artifact" ] || continue
    ts="$(added_at "$artifact")"
    ts="${ts:-0}"
    if [ "$ts" -gt "$newest_live" ]; then
      newest_live="$ts"
    fi
  done <<<"$(find_live_e2e_artifacts)"

  # Hoist the deferral-record walk once; deferral_record_for_version() is
  # called once per release below and a fresh `find docs/features` per call
  # would re-walk the same tree N times for no reason.
  local -a all_records=()
  while IFS= read -r rec; do
    [ -n "$rec" ] || continue
    all_records+=("$rec")
  done <<<"$(find_deferral_records)"

  # Walk every RELEASE (not every deferral record — that is the fix). A
  # release is outstanding if no live-e2e run is known to have happened after
  # it (by git history, or by an artifact naming it directly) — and an
  # UNCOMMITTED release note counts as outstanding immediately, the same
  # fail-CLOSED asymmetry find_deferral_records() consumers already rely on:
  # a release-cutting flow runs this gate before committing the note it is
  # about to ship, and that is exactly the moment a missing record must be
  # catchable.
  local relver relpath rel_ts recpath
  while IFS=$'\t' read -r relver relpath; do
    [ -n "$relver" ] || continue
    rel_ts="$(added_at "$relpath")"
    if [ -n "$rel_ts" ] && [ "$rel_ts" -le "$newest_live" ]; then
      continue
    fi
    if live_e2e_names_version "$relver"; then
      continue
    fi
    recpath="$(deferral_record_for_version "$relver" "${all_records[@]}")"
    if [ -n "$recpath" ]; then
      uncleared_records+=("$recpath")
      uncleared_notes+=("$relpath")
      uncleared_versions+=("$relver")
      if [ -z "$rel_ts" ]; then
        uncleared+=("v$relver ($(basename "$recpath"), release note uncommitted)")
      else
        uncleared+=("v$relver ($(basename "$recpath"))")
      fi
    else
      if [ -z "$rel_ts" ]; then
        uncleared+=("v$relver (RELEASED, uncommitted — no deferral record, no live-e2e evidence)")
      else
        uncleared+=("v$relver (RELEASED — no deferral record, no live-e2e evidence)")
      fi
      recordless+=("v$relver")
    fi
  done <<<"$(find_release_versions)"

  local count=${#uncleared[@]}

  # THE ONE SELECTION OF "NEWEST", used by both readers below (the coverage
  # binding and the streak signature). The newest record is the one belonging
  # to the newest outstanding RELEASE, and the two wrong answers this line has
  # already given are both worth keeping.
  #
  # (1) A FILENAME SORT stopped meaning "newest" at v0.19.10: as strings,
  #     "v0.19.9-..." sorts AFTER "v0.19.10-...", so the gate asked the
  #     v0.19.9 record to sign for a streak of nine — a signature its author
  #     could not have made and its file has no business carrying. Found on
  #     2026-08-12, cutting the first release whose patch number has two
  #     digits.
  #
  # (2) GIT ADD-TIME replaced it, with an untracked record short-circuiting
  #     to "newest" so a release flow could sign before committing. That
  #     held for exactly one real use. On 2026-08-13 this revision's own
  #     re-keying created THREE retrospective records at once (v0.19.4,
  #     v0.19.5, v0.19.11 — releases that had shipped with nothing), all
  #     untracked, and the loop took the first it happened to iterate. The
  #     gate then demanded that the operator sign for a thirteen-release
  #     streak inside a record about v0.19.4, a release from a week earlier.
  #     A signature in that file would be a lie about when it was given.
  #
  # `uncleared_records` is appended in RELEASE-VERSION order by the loop
  # above, which is fed by find_release_versions()'s own version sort. So
  # the newest release's record is simply the last element — no clock, no
  # filename, no git index, and nothing that a backfill can reorder. The
  # "sign before committing" property survives untouched: an uncommitted
  # record for the newest release is still that release's record.
  #
  # It is hoisted here, out of the streak-threshold branch it used to live in,
  # so the coverage binding INHERITS this selection rather than inventing a
  # second one. That inheritance is also what keeps the sixteen older records
  # unjudged — ADR-011 holds by construction, not by an exemption list.
  local newest="" newest_notes="" newest_version=""
  if [ "${#uncleared_records[@]}" -gt 0 ]; then
    newest="${uncleared_records[$(( ${#uncleared_records[@]} - 1 ))]}"
    newest_notes="${uncleared_notes[$(( ${#uncleared_notes[@]} - 1 ))]}"
    newest_version="${uncleared_versions[$(( ${#uncleared_versions[@]} - 1 ))]}"
  fi

  # A recordless release is refused UNCONDITIONALLY — not gated behind the
  # streak-of-3 threshold below, and never silenceable by the
  # streak-acknowledgement marker. That marker signs for a DECISION ("we
  # deferred again, on purpose"); a release with no record made no such
  # decision on paper, so there is nothing for a count-signature to attest
  # to. This branch runs before the threshold check specifically so a
  # recordless release inside an already-signed streak still surfaces by
  # name, rather than being swallowed by a signature that matched the wrong
  # thing (see the audit this revision fixes: a signature for "10 records"
  # said nothing about the release that had none).
  if [ "${#recordless[@]}" -gt 0 ]; then
    echo "provider-tier-deferral: FAIL — $count release(s) are outstanding since the last live-e2e evidence, and ${#recordless[@]} of them shipped with NEITHER a deferral record NOR live-e2e evidence:" >&2
    local r
    for r in "${uncleared[@]}"; do
      echo "  - $r" >&2
    done
    echo "" >&2
    echo "A recordless release cannot be covered by consecutive-deferral-acknowledged: that marker signs for a" >&2
    echo "deferral decision that was written down; a release with no record never made one on paper. Add" >&2
    echo "docs/features/<epic>/audits/v<version>-provider-tier-deferral.md for each release named above (dated" >&2
    echo "honestly, not backfilled to look better than it was), or clear the streak with a live-e2e run." >&2
    return 1
  fi

  # THE RECORD MUST NAME WHAT IT SIGNS FOR — and this runs BEFORE every path
  # below that can return 0, which is the whole point of its position.
  #
  # It is NOT gated behind the streak-of-3 threshold, for two reasons. The
  # threshold branch contains a `return 0` of its own: a valid
  # `consecutive-deferral-acknowledged:` would otherwise green the gate with the
  # coverage binding never read, which IS the incident's shape — a signature
  # satisfying the gate while the record describes nothing. And condition 1 of
  # the tier ("an operator authorized it explicitly, FOR THIS CANDIDATE, in
  # writing") binds every deferral, not the third: a fresh streak's first
  # release can carry a stale record just as easily as its sixteenth.
  #
  # Only the NEWEST outstanding release's record is opened, using the single
  # selection hoisted above. The sixteen older records are never read.
  if [ -n "$newest" ]; then
    check_record_covers_release "$newest" "$newest_notes" "$newest_version" || return 1
  fi

  if [ "$count" -ge 3 ]; then
    # Refusal is OVERRIDABLE, and deliberately so. The tier this guards already
    # requires a written operator authorization per candidate; a third
    # consecutive use requires a STRONGER one, stated inside the newest record
    # itself. The gate's job is to make the cost visible and signed, not
    # unavailable.
    #
    # Why not a hard block: the promise this encodes was authored by the agent
    # cutting v0.19.1, not by the operator it binds. A gate stating a rule its
    # owner does not hold is a gate that gets deleted the first time it fires —
    # the same failure mode as prose, with more ceremony. Overridable-but-
    # recorded is the version that survives contact with the person it binds.
    # `newest` is selected ONCE, above, and both readers use that one
    # selection: the coverage binding and this streak signature. A second
    # notion of "newest" is exactly what the comment there refuses.
    local signed=""
    local signed=""
    if [ -n "$newest" ]; then
      # `|| true` is load-bearing under `set -e`: an unsigned record makes both
      # greps exit non-zero, and a failing command substitution in an
      # assignment kills the script — which presents as an EMPTY refusal, the
      # one output a gate must never produce. A gate that dies silently reads
      # as a gate that refused for reasons it declined to give.
      local marker_count
      marker_count="$(grep -cE "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null || true)"
      [ -n "$marker_count" ] || marker_count=0
      # TWO markers in one record is a refusal, not a "read the first one".
      # Correcting a signature by APPENDING the new line is what a person
      # actually does — it is what happened while writing this gate's own
      # case 8 — and `head -1` would then keep reading the superseded count
      # forever, silently, which is the exact failure mode the per-record
      # count exists to prevent. Ambiguity about which signature is in force
      # must be loud.
      # A PLACEHOLDER IS NOT A SIGNATURE. The marker's own documented shape
      # is `consecutive-deferral-acknowledged: <N> — <who authorized ...>`,
      # and a record that quotes that shape as an EXAMPLE — in a fenced block,
      # in an instruction to the operator, anywhere — used to satisfy this
      # gate outright if the author wrote a literal count into the example.
      #
      # Found by stepping in it: v0.21.0's record was written deliberately
      # UNSIGNED, with a fenced example telling the operator what to add, and
      # the gate reported "acknowledges the streak of 14" and went green. An
      # unsigned record read as signed, by the gate whose entire purpose is
      # refusing exactly that. Any angle-bracket placeholder left in the line
      # now disqualifies it.
      if grep -E "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null | grep -q '<[^>]*>'; then
        echo "provider-tier-deferral: FAIL — $newest carries a marker line that still contains a <placeholder>:" >&2
        grep -E "$OVERRIDE_MARKER_RE" "$newest" | grep '<[^>]*>' | sed 's/^/    /' >&2
        echo "" >&2
        echo "That is the documented EXAMPLE shape, not a signature. Replace the placeholders with who authorized" >&2
        echo "shipping the Nth in a row and why waiting was judged the larger risk — a record that merely quotes" >&2
        echo "the instruction has made no decision." >&2
        return 1
      fi
      if [ "$marker_count" -gt 1 ]; then
        echo "provider-tier-deferral: FAIL — $newest carries $marker_count streak signatures; exactly one must be in force." >&2
        echo "Correcting a signature means EDITING the line, not adding a second one: with two present, a reader" >&2
        echo "(and this gate) cannot tell which count the operator stands behind." >&2
        return 1
      fi
      signed="$(grep -oE "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null | grep -oE '[0-9]+' | head -1 || true)"
    fi
    if [ -n "$signed" ]; then
      if [ "$signed" -eq "$count" ]; then
        echo "provider-tier-deferral: ok — $count outstanding, and $newest acknowledges the streak of ${count}."
        return 0
      fi
      # A marker naming a DIFFERENT count is a signature carried forward from an
      # earlier streak position, which is exactly the silent inheritance this
      # gate exists to prevent: signing for the 3rd must not license the 4th —
      # and, since the unit changed from records to releases, a marker written
      # under the old record-count is a stale signature by construction the
      # first time this revision runs against it, and must re-read as one.
      echo "provider-tier-deferral: FAIL — $newest acknowledges $signed consecutive deferral(s), but $count release(s) are outstanding:" >&2
      local r2
      for r2 in "${uncleared[@]}"; do
        echo "  - $r2" >&2
      done
      echo "" >&2
      echo "A signature for an earlier position in the streak does not carry forward. Re-sign for $count, or clear the streak with a live-e2e run." >&2
      return 1
    fi
    echo "provider-tier-deferral: FAIL — $count consecutive release(s) since the last live-e2e evidence; the scheduled follow-up promise is now false:" >&2
    local r3
    for r3 in "${uncleared[@]}"; do
      echo "  - $r3" >&2
    done
    echo "" >&2
    echo "Clear it by running the promised provider tier (a live-e2e-* evidence artifact added to an audits/ dir after these releases)," >&2
    echo "or, if shipping anyway is the deliberate call, state it in $newest as a line reading:" >&2
    echo "  $OVERRIDE_MARKER_EXAMPLE" >&2
    echo "That line is the operator signing for the streak, not merely for this candidate. It is not paperwork: it is the only" >&2
    echo "thing that keeps a third deferral a decision rather than a habit nobody noticed forming." >&2
    return 1
  fi

  echo "provider-tier-deferral: ok — $count release(s) outstanding since the last live-e2e evidence, all recorded (fails at 3 unless the newest record acknowledges the streak)."
}

teeth() {
  local out
  # tmp1..tmp7 are deliberately NOT `local`: the cleanup trap below is an
  # EXIT trap (failure paths call `exit 1` directly, which bypasses a RETURN
  # trap), and it fires from the top-level script after this function has
  # already returned — a `local` variable would already be unbound by then,
  # silently turning `rm -rf` into a no-op and leaking every temp dir.
  # Every tmp the cases below create must appear in BOTH lines. Cases 5, 5b
  # and 6 were added later and their dirs were in neither, so each
  # `make harness-check` leaked two temp git repos — in a trap whose own
  # comment explains this exact hazard for the vars it did cover. Case 7
  # (recordless release) follows the same rule.
  #
  # tmp8, tmp9 and tmp11 were in NEITHER line — added by cases 8, 10 and 11
  # under a comment demanding they be in both — so every `make harness-check`
  # leaked three more temp git repos. Listed now, along with the cases below.
  tmp1="" tmp2="" tmp3="" tmp4="" tmp5="" tmp6="" tmp7="" tmp8="" tmp9="" tmp11=""
  tmp12="" tmp13="" tmp14="" tmp15=""
  trap 'rm -rf "${tmp1:-}" "${tmp2:-}" "${tmp3:-}" "${tmp4:-}" "${tmp5:-}" "${tmp6:-}" "${tmp7:-}" "${tmp8:-}" "${tmp9:-}" "${tmp11:-}" "${tmp12:-}" "${tmp13:-}" "${tmp14:-}" "${tmp15:-}"' EXIT

  init_repo() {
    local dir="$1"
    mkdir -p "$dir/docs/features/x/audits" "$dir/releasenotes"
    (
      cd "$dir"
      git init -q
      git config user.email test@example.invalid
      git config user.name teeth
      # A ROOT COMMIT, so every fixture has real history to order by.
      #
      # Without it a fixture that makes exactly one commit is
      # indistinguishable from `actions/checkout`'s depth-1 clone, and the
      # single-commit guard above correctly declines — which made case 7 read
      # as "a recordless release stayed green" when the gate had in fact
      # refused to judge at all. The fixture was the unrealistic one: a real
      # repository does not have one commit, and the guard exists precisely
      # because a one-commit history cannot date anything.
      : >.teeth-root
      git add .teeth-root
      GIT_AUTHOR_DATE="@1 +0000" GIT_COMMITTER_DATE="@1 +0000" \
        git commit -q -m "root"
    )
  }

  commit_at() {
    local dir="$1" epoch="$2" msg="$3"
    (
      cd "$dir"
      git add -A
      GIT_AUTHOR_DATE="@${epoch} +0000" GIT_COMMITTER_DATE="@${epoch} +0000" \
        git commit -q -m "$msg"
    )
  }

  # A release-notes file that actually DECLARES entries. The coverage binding
  # measures what a release carries, so a `version: "0.1.0"` stub measures as
  # "measurement-failed" — correctly — and every fixture whose newest release
  # must reach the streak logic has to carry real ids.
  #
  # The ids are deliberately NOT of the RN-<versionkey>-<n> shape: the gate
  # compares literal strings and must never parse the scheme (§11 A1). If any
  # arithmetic ever creeps into the comparison, these fixtures red first.
  notes_with_ids() {
    local file="$1" version="$2" id
    shift 2
    {
      printf 'schema: release-notes/v1\nversion: "%s"\nreleased: "2026-08-21"\nheadline: fixture\nchanges:\n' "$version"
      for id in "$@"; do
        printf -- '  - id: %s\n    kind: fix\n    impact: normal\n    subject: fixture\n    detail: fixture\n' "$id"
      done
    } >"$file"
  }

  # A deferral record carrying the coverage binding. Arguments after the id list
  # are extra lines appended verbatim (the streak signature, when a case needs
  # one). Only the NEWEST outstanding release's record ever needs this — every
  # fixture that leaves older records as bare `echo r1 >` stubs is also tooth Te
  # proving those older records are not judged.
  record_covering() {
    local file="$1" ids="$2" line
    shift 2
    {
      printf 'fixture deferral record\n'
      printf 'covers-notes: %s\n' "$ids"
      printf 'candidate-sha: 0123456789abcdef0123456789abcdef01234567\n'
      for line in "$@"; do printf '%s\n' "$line"; done
    } >"$file"
  }

  # Case 1: 3 releases, each carrying a matching deferral record, no live-e2e
  # artifact at all → RED, naming each record. The release note and its
  # record are committed TOGETHER, mirroring how this repo actually cuts a
  # release (verified against real git history: releasenotes/0.20.0.yaml and
  # its deferral record share one add-timestamp).
  tmp1="$(mktemp -d)"
  init_repo "$tmp1"
  echo r1 >"$tmp1/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp1/releasenotes/0.1.0.yaml"
  commit_at "$tmp1" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp1/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp1/releasenotes/0.2.0.yaml"
  commit_at "$tmp1" 200 "add v0.2.0 deferral"
  record_covering "$tmp1/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a, fix-b"
  notes_with_ids "$tmp1/releasenotes/0.3.0.yaml" 0.3.0 fix-a fix-b
  commit_at "$tmp1" 300 "add v0.3.0 deferral"

  if out="$(cd "$tmp1" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 3 uncleared releases without an intervening live-e2e run stayed green" >&2
    exit 1
  fi
  for name in v0.1.0-provider-tier-deferral.md v0.2.0-provider-tier-deferral.md v0.3.0-provider-tier-deferral.md; do
    printf '%s' "$out" | grep -q "$name" || {
      echo "provider-tier-deferral --teeth: FAILED — red did not name $name" >&2
      exit 1
    }
  done

  # Case 2: exactly 2 uncleared releases (both recorded) → GREEN.
  tmp2="$(mktemp -d)"
  init_repo "$tmp2"
  echo r1 >"$tmp2/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp2/releasenotes/0.1.0.yaml"
  commit_at "$tmp2" 100 "add v0.1.0 deferral"
  record_covering "$tmp2/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a"
  notes_with_ids "$tmp2/releasenotes/0.2.0.yaml" 0.2.0 fix-a
  commit_at "$tmp2" 200 "add v0.2.0 deferral"

  if ! out="$(cd "$tmp2" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — exactly 2 uncleared releases went red" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report the outstanding count" >&2
    exit 1
  }

  # Case 3: 3 releases exist, but a live-e2e artifact was added (by git
  # history) after the OLDEST of them, before the 2nd and 3rd → only the
  # oldest is cleared, 2 remain outstanding → GREEN, same threshold as case
  # 2. The artifact is deliberately named with a DATE that sorts and reads
  # as much older than the VERSION-named records around it, and its mtime is
  # bumped to "now" (the newest thing in the whole tree) after every commit
  # — with no git-visible change — so that filename-lexical or mtime-based
  # ordering would get this case wrong while git add-date gets it right.
  tmp3="$(mktemp -d)"
  init_repo "$tmp3"
  echo r1 >"$tmp3/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp3/releasenotes/0.1.0.yaml"
  commit_at "$tmp3" 100 "add v0.1.0 deferral"
  mkdir -p "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"
  echo evidence >"$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  commit_at "$tmp3" 150 "add live-e2e evidence after the oldest deferral"
  echo r2 >"$tmp3/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp3/releasenotes/0.2.0.yaml"
  commit_at "$tmp3" 200 "add v0.2.0 deferral"
  record_covering "$tmp3/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a"
  notes_with_ids "$tmp3/releasenotes/0.3.0.yaml" 0.3.0 fix-a
  commit_at "$tmp3" 300 "add v0.3.0 deferral"
  # Bump mtime only, no git-visible change (content is identical), so mtime
  # is now the newest path in the tree while its git add-date stays 150.
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"

  if ! out="$(cd "$tmp3" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a live-e2e run after the oldest of 3 releases should leave only 2 outstanding and stay green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green after the mid-run live-e2e evidence did not report 2 outstanding" >&2
    exit 1
  }

  # Case 4: 2 committed releases + 1 UNTRACKED (uncommitted) third release
  # (note and record both) → RED. Proves the untracked rule is asymmetric: an
  # uncommitted release counts as outstanding immediately rather than hiding
  # at timestamp 0 until it is committed (which would let a third deferral
  # ship unrefused by any gate that runs pre-commit).
  tmp4="$(mktemp -d)"
  init_repo "$tmp4"
  echo r1 >"$tmp4/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp4/releasenotes/0.1.0.yaml"
  commit_at "$tmp4" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp4/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp4/releasenotes/0.2.0.yaml"
  commit_at "$tmp4" 200 "add v0.2.0 deferral"
  record_covering "$tmp4/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a"
  notes_with_ids "$tmp4/releasenotes/0.3.0.yaml" 0.3.0 fix-a
  # v0.3.0 deliberately left untracked — no commit_at call.

  if out="$(cd "$tmp4" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 2 committed + 1 untracked release stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.3.0 (v0.3.0-provider-tier-deferral.md, release note uncommitted)' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the untracked third release" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 5: 3 uncleared releases, but the NEWEST carries an acknowledgement
  # signed for exactly 3 → GREEN. The refusal is an escalation, not a wall:
  # the tier already requires a per-candidate authorization, and this is the
  # operator additionally signing for the streak.
  tmp5="$(mktemp -d)"
  init_repo "$tmp5"
  echo r1 >"$tmp5/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp5/releasenotes/0.1.0.yaml"
  commit_at "$tmp5" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp5/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp5/releasenotes/0.2.0.yaml"
  commit_at "$tmp5" 200 "add v0.2.0 deferral"
  record_covering "$tmp5/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a" \
    'consecutive-deferral-acknowledged: 3 — operator signed for the streak'
  notes_with_ids "$tmp5/releasenotes/0.3.0.yaml" 0.3.0 fix-a
  commit_at "$tmp5" 300 "add v0.3.0 deferral with the streak acknowledgement"

  if ! out="$(cd "$tmp5" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an acknowledged 3rd deferral must ship, not be blocked" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'acknowledges the streak of 3' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report that the streak was acknowledged" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 5b: the signed record is UNTRACKED — which is the normal state at the
  # moment a release authors it, and therefore the case that matters most.
  #
  # This is a regression test for a real defect in this gate, caught by
  # running it against the actual v0.19.2 record rather than a fixture: the
  # outstanding list carried DISPLAY strings, so an untracked entry read
  # "<path> (uncommitted)", and the override lookup opened that annotated
  # string as a filename. It found nothing, the grep failed, and `set -e`
  # killed the script mid-refusal — producing exit 1 with NO output at all.
  # A gate that dies silently is worse than one that never ran: it reads as a
  # refusal whose reasons were withheld. Cases 1-5 all used committed
  # fixtures, where the display string happens to equal the path, so none of
  # them could see it.
  tmp6="$(mktemp -d)"
  init_repo "$tmp6"
  echo r1 >"$tmp6/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp6/releasenotes/0.1.0.yaml"
  commit_at "$tmp6" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp6/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp6/releasenotes/0.2.0.yaml"
  commit_at "$tmp6" 200 "add v0.2.0 deferral"
  record_covering "$tmp6/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a" \
    'consecutive-deferral-acknowledged: 3 — signed before the record was committed'
  # ...both deliberately NOT committed.
  notes_with_ids "$tmp6/releasenotes/0.3.0.yaml" 0.3.0 fix-a

  if ! out="$(cd "$tmp6" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a signed but uncommitted 3rd record must ship" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'acknowledges the streak of 3' || {
    echo "provider-tier-deferral --teeth: FAILED — the override was not read from an untracked record" >&2
    echo "$out" >&2
    exit 1
  }

  # ...and unsigned-and-untracked must refuse WITH a message, never die mute.
  # The coverage lines stay: this half is about the missing streak SIGNATURE, and
  # a record stripped of both would refuse for the first reason it hit instead.
  record_covering "$tmp6/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a"
  if out="$(cd "$tmp6" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an unsigned 3rd record shipped" >&2
    exit 1
  fi
  [ -n "$out" ] || {
    echo "provider-tier-deferral --teeth: FAILED — the refusal produced NO output; a mute gate reads as one that withheld its reasons" >&2
    exit 1
  }

  # Case 6: a 4th release COPIED from the 3rd, stale "3" and all → RED.
  #
  # This is the realistic inheritance path and the reason the count is inside
  # the marker: the next deferral record gets written by copying the last one,
  # and a signature that carried forward unread would let the streak grow
  # under a signature nobody re-made. (A 4th carrying NO marker is covered by
  # case 1's generic refusal, which correctly tells the author to sign.)
  record_covering "$tmp5/docs/features/x/audits/v0.4.0-provider-tier-deferral.md" "fix-d" \
    'consecutive-deferral-acknowledged: 3 — copied from the previous record and never re-read'
  notes_with_ids "$tmp5/releasenotes/0.4.0.yaml" 0.4.0 fix-d
  commit_at "$tmp5" 400 "add a 4th deferral carrying the 3rd's stale acknowledgement"

  if out="$(cd "$tmp5" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a 4th deferral shipped on the 3rd's signature" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'does not carry forward' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not explain that an earlier signature is not inherited" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 7: the actual defect this revision fixes. A release ships with NO
  # deferral record and NO live-e2e evidence at all (v0.19.11's real shape) →
  # RED, naming it as recordless, and NOT silenceable by a streak signature —
  # even a streak of exactly 1 (well under the streak-of-3 threshold) must
  # refuse, because "every release carries one or the other" is unconditional.
  tmp7="$(mktemp -d)"
  init_repo "$tmp7"
  echo 'version: "0.1.0"' >"$tmp7/releasenotes/0.1.0.yaml"
  # No matching v0.1.0-provider-tier-deferral.md anywhere, and no live-e2e
  # artifact in the repo at all.
  commit_at "$tmp7" 100 "ship v0.1.0 with no provider-tier evidence of any kind"

  if out="$(cd "$tmp7" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a recordless release with no live-e2e evidence stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.1.0 (RELEASED — no deferral record, no live-e2e evidence)' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the recordless release" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -q 'A recordless release cannot be covered' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not explain that a recordless release cannot be signed for" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 8: THE BACKFILL. Several retrospective records are added at once for
  # OLD releases, all untracked, while the newest release's record already
  # carries the signature. The streak signature must be read from the newest
  # RELEASE's record — not from whichever backfilled file the loop reached
  # first.
  #
  # This is not hypothetical: it is what happened the first time this
  # revision ran for real. Re-keying the gate from records to releases
  # surfaced three recordless releases at once; writing their records made
  # the gate demand a thirteen-release signature inside a record about a
  # release from the previous week. A signature there would have been a lie
  # about when it was given, and the only thing that made it visible was
  # someone reading the refusal instead of obeying it.
  tmp8="$(mktemp -d)"
  init_repo "$tmp8"
  for v in 0.1.0 0.2.0 0.3.0; do
    echo "version: \"$v\"" >"$tmp8/releasenotes/$v.yaml"
    echo "r-$v" >"$tmp8/docs/features/x/audits/v$v-provider-tier-deferral.md"
  done
  notes_with_ids "$tmp8/releasenotes/0.3.0.yaml" 0.3.0 fix-a
  record_covering "$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md" "fix-a" \
    'consecutive-deferral-acknowledged: 3 — signed in the NEWEST release, where the decision was made'
  commit_at "$tmp8" 100 "three releases, the newest carrying the signature"

  # Now backfill two OLDER releases' records, uncommitted — the exact shape
  # that broke the add-time heuristic.
  for v in 0.1.5 0.2.5; do
    echo "version: \"$v\"" >"$tmp8/releasenotes/$v.yaml"
    echo "retrospective, unsigned by design" >"$tmp8/docs/features/x/audits/v$v-provider-tier-deferral.md"
  done
  # The streak is now 5, so the newest record's "3" is genuinely stale and
  # must red — but it must red for the RIGHT reason, naming the newest
  # RELEASE's record, never a backfilled one.
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a streak of 5 shipped on a signature for 3" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.3.0-provider-tier-deferral.md acknowledges 3' || {
    echo "provider-tier-deferral --teeth: FAILED — the signature was not read from the NEWEST RELEASE's record; a backfilled older record was picked instead:" >&2
    echo "$out" >&2
    exit 1
  }
  # APPENDING a corrected signature instead of editing the old one must RED,
  # not silently keep the stale count. This case exists because it is what the
  # author of this very teeth block did by reflex.
  printf 'consecutive-deferral-acknowledged: 5 — appended instead of edited\n' \
    >>"$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record carrying TWO signatures shipped on the first one" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'carries 2 streak signatures' || {
    echo "provider-tier-deferral --teeth: FAILED — two signatures did not red as ambiguous:" >&2
    echo "$out" >&2
    exit 1
  }

  # And EDITING it to the true count greens — proving the backfilled records
  # did not need signatures of their own.
  perl -0pi -e 's/^consecutive-deferral-acknowledged: 3 .*\n//m' \
    "$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if ! out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — re-signing the newest release's record for the true count did not green:" >&2
    echo "$out" >&2
    exit 1
  fi

  # Case 9: an UNSIGNED record that quotes the signature's own shape as an
  # example, literal count and all. This is not hypothetical — it is how
  # v0.21.0's record was first written, and the gate greened on it.
  printf 'consecutive-deferral-acknowledged: 5 — <who authorized shipping the 5th in a row>\n' \
    >>"$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a <placeholder> example counted as a signature" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'still contains a <placeholder>' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the placeholder:" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 10: THE PUBLISHED CANDIDATE. Release notes survive the publish; the
  # deferral records and live-e2e evidence under docs/ do not. A checkout in
  # that state must DECLINE, not answer — it used to answer "ok — 0
  # outstanding" while blind, and re-keying to releases would have turned the
  # same blindness into a red that fails every candidate.
  tmp9="$(mktemp -d)"
  init_repo "$tmp9"
  echo 'version: "0.1.0"' >"$tmp9/releasenotes/0.1.0.yaml"
  echo 'version: "0.2.0"' >"$tmp9/releasenotes/0.2.0.yaml"
  rm -rf "$tmp9/docs"
  commit_at "$tmp9" 100 "a published candidate: notes present, docs stripped"

  if ! out="$(cd "$tmp9" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a stripped (public) checkout RED; the candidate would fail on evidence it cannot see:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'skip — docs/features absent' || {
    echo "provider-tier-deferral --teeth: FAILED — a stripped checkout must DECLINE by name, not answer green silently:" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 11: a notes file carrying the PRE-TAG SENTINEL is not a release.
  #
  # Two halves, and the first is what makes the second mean anything. A
  # version whose `released:` is `unreleased` has shipped nothing, so it owes
  # no deferral record — authoring notes ahead of the tag is the runbook's own
  # flow. The SAME file with a real DATE and no record must still red, or this
  # skip would be a hole rather than a boundary.
  tmp11="$(mktemp -d)"
  init_repo "$tmp11"
  echo 'version: "0.1.0"' >"$tmp11/releasenotes/0.1.0.yaml"
  printf 'released: unreleased\n' >>"$tmp11/releasenotes/0.1.0.yaml"
  commit_at "$tmp11" 100 "a version authored ahead of its tag"

  if ! out="$(cd "$tmp11" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a version carrying the pre-tag sentinel was judged as RELEASED:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'not yet a release' || {
    echo "provider-tier-deferral --teeth: FAILED — the skip was silent; a version dropping out of coverage must say so" >&2
    echo "$out" >&2
    exit 1
  }

  printf 'version: "0.1.0"\nreleased: "2026-08-21"\n' >"$tmp11/releasenotes/0.1.0.yaml"
  commit_at "$tmp11" 200 "the same version, now actually cut"
  if out="$(cd "$tmp11" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — the SAME file with a real date and no record stayed green; the sentinel skip is a hole, not a boundary" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.1.0 (RELEASED' || {
    echo "provider-tier-deferral --teeth: FAILED — a dated, recordless release was not named" >&2
    echo "$out" >&2
    exit 1
  }

  # ══════════════════════════════════════════════════════════════════════════
  # THE COVERAGE BINDING — does the record NAME what it signs for?
  #
  # Cases 12-19 are the teeth of release-loop-2026-08 P7. Two of them replay
  # REAL corpus artifacts rather than invented fixtures, because the incident
  # and its counter-example both already exist in this repository.
  # ══════════════════════════════════════════════════════════════════════════
  local repo_root="$PWD" corpus
  for corpus in releasenotes/0.23.0.yaml releasenotes/0.9.0.yaml; do
    [ -r "$repo_root/$corpus" ] || {
      echo "provider-tier-deferral --teeth: measurement-failed — $corpus is not readable from $repo_root." >&2
      echo "Cases 13 and 19 replay real corpus files; run the teeth from the repository root." >&2
      exit 1
    }
  done

  # Case 12: teeth Ta and Te, plus AC5's grow → red → re-sign → green cycle.
  #
  # TWO outstanding releases, deliberately UNDER the streak-of-3 threshold: the
  # binding is not gated behind it. Condition 1 of the tier ("an operator
  # authorized it explicitly, FOR THIS CANDIDATE, in writing") binds a first
  # deferral exactly as much as a seventeenth, and the threshold branch has a
  # `return 0` of its own — a valid streak signature must never be able to green
  # a record that describes nothing.
  tmp12="$(mktemp -d)"
  init_repo "$tmp12"
  echo r1 >"$tmp12/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp12/releasenotes/0.1.0.yaml"
  commit_at "$tmp12" 100 "v0.1.0, an older record from before the binding existed"
  notes_with_ids "$tmp12/releasenotes/0.2.0.yaml" 0.2.0 fix-a fix-b fix-c
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a, fix-b, fix-c"
  commit_at "$tmp12" 200 "v0.2.0, whose record names every entry it ships"

  if ! out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record naming every id its release ships went red:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'names all 3 of v0.2.0' || {
    echo "provider-tier-deferral --teeth: FAILED — the green did not report what was verified:" >&2
    echo "$out" >&2
    exit 1
  }
  # Tooth Te: v0.1.0's record carries no covers-notes: line at all and is NOT
  # judged. This is the whole of ADR-011 compliance — the sixteen real older
  # records are unjudged by inheriting the newest-record lookup, not by an
  # exemption list.
  if printf '%s' "$out" | grep -q 'v0.1.0-provider-tier-deferral'; then
    echo "provider-tier-deferral --teeth: FAILED — an OLDER record was judged; the scope is inherited from the newest-record lookup and must never be widened:" >&2
    echo "$out" >&2
    exit 1
  fi

  # ...the release then GROWS an entry after the signature. This is v0.23.0 in
  # miniature: the record is unchanged, the candidate is not.
  notes_with_ids "$tmp12/releasenotes/0.2.0.yaml" 0.2.0 fix-a fix-b fix-c fix-d
  commit_at "$tmp12" 300 "v0.2.0 grows a fourth entry after its record was signed"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a release that outgrew its signature stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'the record names 3 of them' || {
    echo "provider-tier-deferral --teeth: FAILED — the red did not report how many of the shipped entries the record names:" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -qE '^  - fix-d$' || {
    echo "provider-tier-deferral --teeth: FAILED — the red did not NAME the uncovered entry:" >&2
    echo "$out" >&2
    exit 1
  }

  # ...and re-signing for what it now is greens again.
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a, fix-b, fix-c, fix-d"
  if ! out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — re-signing for the grown release did not green:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'names all 4 of v0.2.0' || {
    echo "provider-tier-deferral --teeth: FAILED — the re-signed green did not report the new count:" >&2
    echo "$out" >&2
    exit 1
  }

  # Tooth Tc: the record names an id the release does not carry. A record
  # claiming coverage of something absent is a copy from another release.
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a, fix-b, fix-c, fix-d, fix-e"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record naming an entry the release does not ship stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -qE '^  - fix-e$' || {
    echo "provider-tier-deferral --teeth: FAILED — the red did not name the id the notes file does not declare:" >&2
    echo "$out" >&2
    exit 1
  }

  # ...which is also what happens to a PROSE TAIL. The streak signature's shape
  # ends in "— <who authorized …>"; copying that habit here would turn every
  # word into a phantom id, so the tail is refused verbatim rather than trimmed.
  printf 'fixture\ncovers-notes: fix-a, fix-b, fix-c, fix-d — yura signed this\ncandidate-sha: 0123456789abcdef0123456789abcdef01234567\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a prose tail on covers-notes: was silently tolerated; every word of it is a phantom id" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -qE '^  - yura$' || {
    echo "provider-tier-deferral --teeth: FAILED — the red did not quote the prose tail's tokens verbatim:" >&2
    echo "$out" >&2
    exit 1
  }

  # Tooth Td: the newest record carries no covers-notes: line → RED, printing
  # the template, exactly as a missing signature does.
  printf 'a record that signs for nothing in particular\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record with no covers-notes: line stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'never says what it signs for' || {
    echo "provider-tier-deferral --teeth: FAILED — the red did not say the record names nothing:" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -qF "$COVERS_MARKER_EXAMPLE" || {
    echo "provider-tier-deferral --teeth: FAILED — the refusal did not print the template; a refusal that does not say what to write is a wall:" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -qF "$CANDIDATE_SHA_EXAMPLE" || {
    echo "provider-tier-deferral --teeth: FAILED — the refusal did not print the candidate-sha template:" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -q "v0.19.1" || {
    echo "provider-tier-deferral --teeth: FAILED — the refusal did not say this makes an EXISTING voluntary discipline checkable (v0.19.1's record already does it by hand); a rule that reads as newly invented is a rule that gets deleted:" >&2
    echo "$out" >&2
    exit 1
  }

  # Tooth Tg: the inclusive RANGE form, and it must WORK rather than merely
  # parse. Four halves, because a range that greens on everything is also what a
  # broken "expand to all ids" implementation does.
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a..fix-d"
  if ! out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an inclusive range covering every id went red:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'names all 4 of v0.2.0' || {
    echo "provider-tier-deferral --teeth: FAILED — the range form parsed but did not resolve to the ids it covers:" >&2
    echo "$out" >&2
    exit 1
  }
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a..fix-c"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a range stopping short of the last entry stayed green; the range expands to everything, which is not a range" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -qE '^  - fix-d$' || {
    echo "provider-tier-deferral --teeth: FAILED — the short range's red did not name the entry left outside it:" >&2
    echo "$out" >&2
    exit 1
  }
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-d..fix-a"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an inverted range stayed green; read backwards it covers NOTHING" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'backwards' || {
    echo "provider-tier-deferral --teeth: FAILED — the inverted range did not red as inverted:" >&2
    echo "$out" >&2
    exit 1
  }
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a..fix-z"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a range whose endpoint the release does not ship stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q "declares no id 'fix-z'" || {
    echo "provider-tier-deferral --teeth: FAILED — the unresolvable endpoint was not named:" >&2
    echo "$out" >&2
    exit 1
  }
  # ...and the two forms mix on one line.
  record_covering "$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md" "fix-a..fix-b, fix-c, fix-d"
  if ! out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a range mixed with a list on one line went red:" >&2
    echo "$out" >&2
    exit 1
  fi

  # A SECOND covers-notes: line is ambiguity, not a correction — the same
  # lesson the streak signature learned when someone appended instead of edited.
  printf 'covers-notes: fix-a, fix-b\n' \
    >>"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record carrying TWO covers-notes: lines shipped on one of them" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'covers-notes: lines; exactly one must be in force' || {
    echo "provider-tier-deferral --teeth: FAILED — two coverage lines did not red as ambiguous:" >&2
    echo "$out" >&2
    exit 1
  }

  # A <placeholder> is not a statement of coverage — v0.21.0's record greened
  # this gate once by quoting the streak marker's own example.
  printf 'fixture\ncovers-notes: <the ids this signature was given against>\ncandidate-sha: 0123456789abcdef0123456789abcdef01234567\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a <placeholder> counted as a statement of coverage" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q "covers-notes: line still contains a <placeholder>" || {
    echo "provider-tier-deferral --teeth: FAILED — the placeholder coverage line was not named as one:" >&2
    echo "$out" >&2
    exit 1
  }

  # candidate-sha: is required by PRESENCE, and its shape is deliberately not
  # judged — v0.24.0's own record names `a2a-candidate/1bd62f3a1e85` beside the
  # full SHA, and a 40-hex regex would refuse that honest short form.
  printf 'fixture\ncovers-notes: fix-a, fix-b, fix-c, fix-d\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record naming its coverage but not its candidate stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'no candidate-sha: line' || {
    echo "provider-tier-deferral --teeth: FAILED — the missing candidate was not named as missing:" >&2
    echo "$out" >&2
    exit 1
  }
  printf 'fixture\ncovers-notes: fix-a, fix-b, fix-c, fix-d\ncandidate-sha: <the SHA the operator signed against>\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a <placeholder> counted as a candidate" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q "candidate-sha: line still contains a <placeholder>" || {
    echo "provider-tier-deferral --teeth: FAILED — the placeholder candidate was not named as one:" >&2
    echo "$out" >&2
    exit 1
  }
  printf 'fixture\ncovers-notes: fix-a, fix-b, fix-c, fix-d\ncandidate-sha: a2a-candidate/1bd62f3a1e85\n' \
    >"$tmp12/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  if ! out="$(cd "$tmp12" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — the short candidate-branch form was refused; the shape is not this gate's to judge:" >&2
    echo "$out" >&2
    exit 1
  fi

  # THE REPLAY NEEDS A COMMIT, AND A PUBLISHED CHECKOUT'S HISTORY DOES NOT HAVE
  # ONE. This script is on the Makefile's HARNESS_TEETH list and is NOT stripped
  # at publish, so `_harness-check` runs these teeth on the published candidate
  # too — that is how v0.24.0's third candidate died with Error 127. The public
  # repository's history is created fresh by publish-to-public.sh, so a72ee625
  # is not in it and never can be; refusing there would red a candidate for a
  # reason that exists only in the checkout, which is exactly what case 10
  # declines to do.
  #
  # The discriminator is the same STRUCTURAL one the gate itself uses: docs/ is
  # what the publisher strips. In a PRIVATE checkout the object must be
  # reachable, and its absence (a shallow clone) stays a hard refusal naming the
  # fix — there the tooth can be run and simply was not.
  local tb_summary=""
  if [ ! -d docs/features ]; then
    tb_summary="tooth Tb (the a72ee625 replay) was DECLINED in this checkout, so this line does NOT claim it"
    echo "provider-tier-deferral --teeth: skip — tooth Tb replays commit a72ee625, and a published checkout's history is created fresh at publish, so that object cannot exist here. It is judged in the private repository, where the record it replays also lives."
  else
    # ══════════════════════════════════════════════════════════════════════════
    # Case 13: TOOTH Tb — THE ACCEPTANCE OF THIS PHASE, and not an invented
    # fixture. The real v0.23.0 deferral record, recovered from commit a72ee625,
    # judged against what v0.23.0 actually shipped. The record signed for "one
    # change"; the release carried 44 release-note entries. 0 named against 44
    # shipped, and this gate was green throughout.
    # ══════════════════════════════════════════════════════════════════════════
    tmp13="$(mktemp -d)"
    init_repo "$tmp13"
    local stale_ref='a72ee625:docs/features/archive/agent-ops-2026-07/audits/v0.23.0-provider-tier-deferral.md'
    local stale_dest="$tmp13/docs/features/x/audits/v0.23.0-provider-tier-deferral.md"
    if ! git -C "$repo_root" cat-file -e "$stale_ref"; then
      echo "provider-tier-deferral --teeth: measurement-failed — cannot replay $stale_ref, so tooth Tb did not run." >&2
      echo "This tooth is the acceptance of release-loop-2026-08 P7 and it replays REAL history rather than a fabricated record;" >&2
      echo "a checkout that cannot reach that commit cannot judge it. Use fetch-depth: 0 (CI's check job already does)." >&2
      exit 1
    fi
    git -C "$repo_root" cat-file -p "$stale_ref" >"$stale_dest"
    cp "$repo_root/releasenotes/0.23.0.yaml" "$tmp13/releasenotes/0.23.0.yaml"
    local shipped_23 want_23=44
    shipped_23="$(notes_ids "$tmp13/releasenotes/0.23.0.yaml" | wc -l | tr -d '[:space:]')"
    [ "$shipped_23" -eq "$want_23" ] || {
      echo "provider-tier-deferral --teeth: measurement-failed — releasenotes/0.23.0.yaml declares $shipped_23 ids, not the $want_23 this tooth is the acceptance for." >&2
      echo "The number is pinned on purpose: a fixture that drifts silently would keep passing while testing a different claim." >&2
      exit 1
    }
    commit_at "$tmp13" 100 "replay: the stale v0.23.0 record against what v0.23.0 shipped"

    if out="$(cd "$tmp13" && check_provider_tier_deferral 2>&1)"; then
      echo "provider-tier-deferral --teeth: FAILED — the REAL stale v0.23.0 record stayed green against the 44 entries v0.23.0 shipped." >&2
      echo "This is the incident itself; if it greens, the gate is exactly as blind as it was on 2026-08-14." >&2
      echo "$out" >&2
      exit 1
    fi
    printf '%s' "$out" | grep -q 'Named: 0 of 44' || {
      echo "provider-tier-deferral --teeth: FAILED — the red did not measure the record against the release: 0 named of 44 shipped is the number:" >&2
      echo "$out" >&2
      exit 1
    }
    printf '%s' "$out" | grep -qE '^  - RN-02300-0$' || {
      echo "provider-tier-deferral --teeth: FAILED — the red did not name the entries the record failed to cover:" >&2
      echo "$out" >&2
      exit 1
    }
    printf '%s' "$out" | grep -qE '^  - RN-02300-43$' || {
      echo "provider-tier-deferral --teeth: FAILED — the red named some entries but not the last one; a truncated list hides the drift it exists to show:" >&2
      echo "$out" >&2
      exit 1
    }

    # ...and a PARTIAL signature reds on exactly the remainder. The stale record
    # is given credit for the first eleven entries — far more than it named — and
    # the other thirty-three must still surface, by name.
    {
      printf 'covers-notes: RN-02300-0..RN-02300-10\n'
      printf 'candidate-sha: a72ee625\n'
      git -C "$repo_root" cat-file -p "$stale_ref"
    } >"$stale_dest"
    if out="$(cd "$tmp13" && check_provider_tier_deferral 2>&1)"; then
      echo "provider-tier-deferral --teeth: FAILED — a record covering 11 of 44 entries stayed green" >&2
      echo "$out" >&2
      exit 1
    fi
    printf '%s' "$out" | grep -q 'the record names 11 of them' || {
      echo "provider-tier-deferral --teeth: FAILED — the partial red did not report how much of the release the signature covers:" >&2
      echo "$out" >&2
      exit 1
    }
    printf '%s' "$out" | grep -qE '^  - RN-02300-11$' || {
      echo "provider-tier-deferral --teeth: FAILED — the partial red did not name the first uncovered entry:" >&2
      echo "$out" >&2
      exit 1
    }
    # BOTH ENDS OF THE LIST. Found by mutation: truncating the uncovered list to
    # its first five entries left every other assertion here green, because they
    # all read the front of it. A refusal that shows the beginning of the drift
    # and hides the rest is the same false comfort as no refusal.
    printf '%s' "$out" | grep -qE '^  - RN-02300-43$' || {
      echo "provider-tier-deferral --teeth: FAILED — the partial red truncated the uncovered list before its last entry:" >&2
      echo "$out" >&2
      exit 1
    }
    if printf '%s' "$out" | grep -qE '^  - RN-02300-10$'; then
      echo "provider-tier-deferral --teeth: FAILED — an entry INSIDE the signed range was reported as uncovered; the range does not resolve:" >&2
      echo "$out" >&2
      exit 1
    fi
    tb_summary="the real stale v0.23.0 record from a72ee625 reds against the 44 entries v0.23.0 shipped (0 named of 44) and again when credited with 11 of them"
  fi

  # Case 14: TOOTH Tf — a gate that cannot MEASURE must not announce a domain
  # verdict. Never green, and never "the record is wrong" either: the record is
  # not what failed.
  tmp14="$(mktemp -d)"
  init_repo "$tmp14"
  printf 'schema: release-notes/v1\nversion: "0.1.0"\nreleased: "2026-08-21"\nheadline: truncated before its entries\n' \
    >"$tmp14/releasenotes/0.1.0.yaml"
  record_covering "$tmp14/docs/features/x/audits/v0.1.0-provider-tier-deferral.md" "fix-a"
  commit_at "$tmp14" 100 "a release whose notes declare no entries at all"
  if out="$(cd "$tmp14" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a notes file declaring NO ids produced a green; unjudged is not covered" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'measurement-failed' || {
    echo "provider-tier-deferral --teeth: FAILED — an unmeasurable notes file produced a DOMAIN verdict instead of a measurement-failed refusal:" >&2
    echo "$out" >&2
    exit 1
  }

  : >"$tmp14/releasenotes/0.1.0.yaml"
  if out="$(cd "$tmp14" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an EMPTY notes file produced a green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'measurement-failed' || {
    echo "provider-tier-deferral --teeth: FAILED — an empty notes file did not refuse as a failed measurement:" >&2
    echo "$out" >&2
    exit 1
  }

  notes_with_ids "$tmp14/releasenotes/0.1.0.yaml" 0.1.0 fix-a
  chmod 000 "$tmp14/releasenotes/0.1.0.yaml"
  if [ -r "$tmp14/releasenotes/0.1.0.yaml" ]; then
    # ANNOUNCED, never silent: as root (the container lane's default user) the
    # mode bits do not make a file unreadable, so this half cannot be staged
    # here. The empty-file half above exercises the same refusal path.
    echo "provider-tier-deferral --teeth: note — this user can read a 000-mode file (root), so Tf's unreadable-notes half was not staged; the empty-notes half above covers the same refusal."
  else
    if out="$(cd "$tmp14" && check_provider_tier_deferral 2>&1)"; then
      echo "provider-tier-deferral --teeth: FAILED — an UNREADABLE notes file produced a green" >&2
      echo "$out" >&2
      exit 1
    fi
    printf '%s' "$out" | grep -q 'could not be read' || {
      echo "provider-tier-deferral --teeth: FAILED — an unreadable notes file did not refuse as a failed measurement:" >&2
      echo "$out" >&2
      exit 1
    }
  fi
  chmod 644 "$tmp14/releasenotes/0.1.0.yaml"

  # Case 15: TOOTH Th — §11 A1's own test, against a REAL non-modern notes
  # file. releasenotes/0.9.0.yaml indents its sequence two spaces, pads the
  # version key to four digits and starts its counter at 1 (RN-0900-1), while
  # v0.19.0 uses five digits and starts at 0. Any arithmetic on the id — any
  # regex like RN-\d{5}-\d+ — reds here.
  tmp15="$(mktemp -d)"
  init_repo "$tmp15"
  cp "$repo_root/releasenotes/0.9.0.yaml" "$tmp15/releasenotes/0.9.0.yaml"
  local shipped_09 want_09=8
  shipped_09="$(notes_ids "$tmp15/releasenotes/0.9.0.yaml" | wc -l | tr -d '[:space:]')"
  [ "$shipped_09" -eq "$want_09" ] || {
    echo "provider-tier-deferral --teeth: measurement-failed — releasenotes/0.9.0.yaml declares $shipped_09 ids, not the $want_09 this tooth pins." >&2
    exit 1
  }
  record_covering "$tmp15/docs/features/x/audits/v0.9.0-provider-tier-deferral.md" "RN-0900-1..RN-0900-8"
  commit_at "$tmp15" 100 "a release whose ids predate the modern padding"
  if ! out="$(cd "$tmp15" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a notes file using the OLD id padding and indentation did not compare correctly:" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'names all 8 of v0.9.0' || {
    echo "provider-tier-deferral --teeth: FAILED — the non-modern notes file was not read as 8 entries:" >&2
    echo "$out" >&2
    exit 1
  }
  record_covering "$tmp15/docs/features/x/audits/v0.9.0-provider-tier-deferral.md" "RN-0900-1..RN-0900-7"
  if out="$(cd "$tmp15" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a short range over the non-modern file stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -qE '^  - RN-0900-8$' || {
    echo "provider-tier-deferral --teeth: FAILED — the entry outside the range was not named on the non-modern file:" >&2
    echo "$out" >&2
    exit 1
  }

  echo "provider-tier-deferral --teeth: a pre-tag sentinel is not a release while the same file dated and recordless still reds; 3 uncleared releases red and names them; 2 uncleared greens; a live-e2e run after the oldest of 3 clears it to 2 and greens by git history, not mtime or filename; an untracked 3rd release reds too; an acknowledged 3rd ships; a 4th does NOT inherit the 3rd's signature; a recordless release reds unconditionally, even a lone one; a backfill of older records leaves the signature in the NEWEST RELEASE's record; a <placeholder> example is not a signature; a stripped public checkout declines instead of answering. The record also NAMES what it signs for: ${tb_summary}; a release that grows an entry after signing reds until re-signed; an older record without the field is not judged; a missing covers-notes: prints the template; an inclusive range resolves, refuses when short, inverted or unresolvable, and works on v0.9.0's non-modern id padding; a prose tail, a second line and a <placeholder> all refuse; candidate-sha: is required by presence and its shape is not judged; an unreadable or entry-less notes file refuses as a failed MEASUREMENT, never as a verdict."
}

if [ "${1:-}" = "--teeth" ]; then teeth; exit 0; fi
check_provider_tier_deferral
