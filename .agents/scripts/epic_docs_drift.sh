#!/usr/bin/env bash
# epic_docs_drift.sh — an epic's committed docs must tell the TRUTH about it.
#
# `feature-lint` already checks the tracker's SHAPE. Nothing checks whether the
# epic's docs were TRUE — and the code is green either way, so no other gate can
# see it. Four checks, one invariant family:
#
#   A. COMMITS   — every code commit (feat|fix|refactor|perf) whose scope names a
#                  started epic appears SOMEWHERE in that epic's tracker.yaml:
#                  a phase's `commits:` list, or an explicit `commits_unlisted:`.
#                  Requires git (git log). Skipped, with a notice, outside a git
#                  repository — see the git-availability probe below.
#   B. STAMP     — docs/status.md carries, per enforced epic, a machine-readable
#                  `<!-- epic-state: <slug> phases=<done>/<total> -->` that MATCHES
#                  the tracker. This is the one that catches a status.md entry
#                  narrating a stale reality. Pure text/awk — no git needed.
#   C. LINKS     — every `features/<slug>` reference in status.md resolves to a
#                  directory that exists. Pure text — no git needed.
#   D. RECEIPTS  — a phase marked `status: done` must name the commits that did it,
#                  or say why there are none (`commits: []  # none: <reason>`).
#                  Check A asks "is every commit filed under SOME phase"; D asks the
#                  other direction — "does every finished phase have receipts". A
#                  phase can be closed with an empty list and A never notices, which
#                  is how a progress report ends up unable to show what a done phase
#                  actually shipped. Research and design phases legitimately have no
#                  code: they take the opt-out, in writing, on the line. Pure awk —
#                  no git needed.
#
# WHY THIS EXISTS
# ---------------
# A tracker write-back that depends on a lead remembering to do it, every wave, is
# not a mechanism — it is a hope. This gate turns "the docs match reality" from a
# hope into something that reds the build when it stops being true.
#
# The tracker is the COMMITTED resume point: an epic-continuation workflow reads it
# after a context compact and trusts it over the plan. status.md is what a fresh
# session (or a human) reads to learn what exists. Both lying is invisible and
# free — until the next session re-runs finished work, or builds on a phase it
# believes is done and is not.
#
# WHAT THIS DOES NOT CHECK (say it plainly, don't oversell the gate)
# -----------------------------------------------------------------
#   · It compares the STAMP to the tracker. It does NOT read the prose beside it —
#     a lazy fix ("bump the number, leave the wrong sentence") still passes.
#   · It does NOT gate WHICH SECTION an epic sits in. tracker-done ≠ shipped;
#     section placement in status.md is editorial judgment, and a gate that forced
#     it would make status.md LESS true.
#   · A tracker that lies about its own phases is out of scope — the stamp then
#     faithfully mirrors the lie. Check D is the partial defense: a phase cannot be
#     called done in silence, it must name its commits or its reason. It still cannot
#     tell a TRUE receipt from a plausible one.
#
# COUNTING RULE (fixed, so the number is never a matter of opinion):
#   done  = phases with `status: done`
#   total = all phases in the `phases:` block
# No exclusions for research / re-scoped / deferred phases — any exclusion is a
# judgment call, and a gate built on a judgment call is a gate you can argue with.
#
# TEETH: bash .agents/scripts/epic_docs_drift.sh --teeth
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# FEATURES_DIR / STATUS_MD are overridable so --teeth can point the gate at a
# mutated copy of the docs without touching the real ones.
FEATURES_DIR="${FEATURES_DIR:-docs/features}"
STATUS_MD="${STATUS_MD:-docs/status.md}"

# ── Git availability probe ────────────────────────────────────────────────────
# Check A leans on `git log` / `git cat-file` to know what commits exist. Outside
# a git repository (a fresh checkout, a tarball, this repo before its first
# `git init`) those calls fail — and a gate that reds because its *toolchain* is
# missing, not because the docs lied, teaches people to route around it. So: probe
# once, and if there is no git, skip check A with a loud notice and still run
# checks B/C/D (pure text/awk, no git needed) unconditionally. This mirrors
# parse_tracker.sh's REPO_ROOT fallback — degrade the git-dependent part only,
# never the whole gate.
HAVE_GIT=true
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    HAVE_GIT=false
    echo "epic-docs-drift: not a git repository — skipping check A (commit accounting, needs 'git log')."
    echo "                  checks B (status.md stamps), C (feature links) and D (done-phase receipts) still run."
fi

# Types that MUST be accounted for. docs/chore/test/ci/build/style/audit are
# exempt: they are the write-back itself, the gate wiring, the wave log — the
# bookkeeping around the work, not the work. Requiring a docs commit to list
# itself in the tracker it is editing is a loop, not a check.
CODE_TYPES='feat|fix|refactor|perf'

fail=0
checked=0

# ── The epic's commit SCOPE is not always its slug ───────────────────────────
# Convention here: the slug carries a date suffix the scope drops
# (`hub-service-2026-07` → `feat(hub-service): …`). Derive that by default; a
# tracker may override with an explicit `commit_scope:` line when its scope does
# not follow the convention.
epic_scope() {
    local tracker="$1" slug="$2" explicit
    explicit="$(grep -m1 -E '^commit_scope:[[:space:]]*' "$tracker" 2>/dev/null | sed -E 's/^commit_scope:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' || true)"
    if [ -n "$explicit" ]; then
        printf '%s' "$explicit"
        return
    fi
    # strip a trailing -YYYY-MM (the epic-dir date suffix)
    printf '%s' "$slug" | sed -E 's/-[0-9]{4}-[0-9]{2}$//'
}

# ── Phase counting (awk, deliberately NOT python+yaml) ───────────────────────
# `make` and an interactive shell can resolve DIFFERENT python3s, and PyYAML may
# be absent from the one `make` gets. A gate whose greenness depends on whose
# python is first on PATH is not a gate. So: pure awk, scoped to the `phases:`
# block so an `- id:` elsewhere in the file (a cross-epic blocked_by ref) cannot
# inflate the total.
phase_counts() {
    awk '
        /^phases:/          { inphases = 1; next }
        /^[a-z_]+:/         { inphases = 0 }
        inphases && /^[[:space:]]*- id:/     { total++ }
        inphases && /^[[:space:]]*status:[[:space:]]*done/ { done++ }
        END { printf "%d/%d", done + 0, total + 0 }
    ' "$1"
}

# ── D: done phases with no receipts (awk, same reason as above) ──────────────
# Prints the id of every phase that says `status: done` and lists no commits, unless
# the line carries the line-precise opt-out `# none: <reason>`. Scoped to the phases:
# block, and it tracks WHICH key a `- item` list belongs to, so a `blocked_by: [P1]`
# entry can never be miscounted as a commit.
done_without_commits() {
    awk '
        function flush() {
            if (id != "" && status == "done" && ncommits == 0 && optout == 0) print id
            id = ""; status = ""; ncommits = 0; optout = 0; key = ""
        }
        /^phases:/  { inphases = 1; next }
        /^[a-z_]+:/ { if (inphases) { flush(); inphases = 0 } }
        !inphases   { next }
        /^[[:space:]]*- id:[[:space:]]*/ {
            flush()
            id = $0
            sub(/^[[:space:]]*- id:[[:space:]]*/, "", id)
            sub(/[[:space:]]*(#.*)?$/, "", id)
            next
        }
        /^[[:space:]]*status:[[:space:]]*/ {
            status = $0
            sub(/^[[:space:]]*status:[[:space:]]*/, "", status)
            sub(/[[:space:]]*(#.*)?$/, "", status)
            key = ""
            next
        }
        # `commits_unlisted:` counts too — check A already treats a hash there as accounted
        # for ("a human looked at this commit and placed it"). Today it is only ever an
        # EPIC-level key, so this line changes nothing; the day someone writes one under a
        # phase, A would call it a receipt and D would call the phase receiptless, and the
        # only way out would be to lie with `# none:`. Two checks in one file must not
        # disagree about what a receipt is.
        /^[[:space:]]*commits(_unlisted)?:[[:space:]]*/ {
            key = "commits"
            rest = $0
            sub(/^[[:space:]]*commits(_unlisted)?:[[:space:]]*/, "", rest)
            if (rest ~ /#[[:space:]]*none:[[:space:]]*[^[:space:]]/) optout = 1
            if (rest ~ /\[[^]]*[0-9a-f][^]]*\]/) ncommits++     # inline list with content
            next
        }
        /^[[:space:]]*[a-z_]+:/ { key = ""; next }              # any other key ends the list
        key == "commits" && /^[[:space:]]*-[[:space:]]*[0-9a-f]{7,}/ { ncommits++; next }
        END { flush() }
    ' "$1"
}

# An epic is ENFORCED for the stamp when its README declares a `kind:` — the same
# grandfathering `feature-lint` uses. That keeps the two gates' scopes identical
# and means this gate cannot silently widen onto legacy feature dirs (the
# drift-gate-widening trap: enumerate the tail BEFORE you gate it).
is_enforced() {
    grep -qE '^kind:' "$FEATURES_DIR/$1/README.md" 2>/dev/null
}

stamp_for() {
    printf '<!-- epic-state: %s phases=%s -->' "$1" "$2"
}

# ── TEETH ────────────────────────────────────────────────────────────────────
# A gate nobody has seen fail is a gate nobody knows works. Seed the exact
# violation this exists to catch — a code commit dropped from a tracker — and
# assert the gate goes RED. Then assert the untouched corpus is GREEN, so we know
# the red came from the seed and not from ambient drift.
#
# This self-test needs BOTH a git repository AND at least one committed epic with
# real commit history to seed a violation against (teeth 5/8 picks its victim
# commit out of `git log`). Neither is guaranteed on a fresh checkout — this repo
# may not be a git repository yet, or may have zero epics under docs/features/.
# Rather than fake either up (a synthetic git-init-in-tmp corpus would test the
# harness against fixtures the real gate never sees, which is a different and
# weaker guarantee than "intact"), skip with a loud notice and exit 0: a skip is
# not a false green, it says plainly that the bite is unverified right now.
if [ "${1:-}" = "--teeth" ]; then
    if [ "$HAVE_GIT" = false ]; then
        echo "epic-docs-drift --teeth: skipped — not a git repository yet."
        echo "                          teeth 5/8 seeds its violation from real 'git log' history;"
        echo "                          re-run after 'git init' + at least one committed epic."
        exit 0
    fi
    if ! compgen -G "$FEATURES_DIR"/*/tracker.yaml >/dev/null 2>&1; then
        echo "epic-docs-drift --teeth: skipped — no docs/features/*/tracker.yaml yet."
        echo "                          teeth needs a real, committed epic to seed a violation against."
        exit 0
    fi

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    cp -R docs/features "$tmp/features"
    cp docs/status.md "$tmp/status.md"

    run_gate() { FEATURES_DIR="$tmp/features" STATUS_MD="$tmp/status.md" bash "$0" >/dev/null 2>&1; }
    reset_status() { cp docs/status.md "$tmp/status.md"; }

    echo "== teeth 1/8: the corpus as committed must be GREEN =="
    if ! run_gate; then
        echo "teeth FAILED: the untouched corpus is already red — fix the real drift first, then re-run teeth." >&2
        exit 1
    fi
    echo "   ok — green before any seed"

    # CHECK B — the class this gate was built for: status.md narrating a stale reality.
    # Falsify the done-count to 999, never 0: a fresh epic's real count IS 0, and a
    # seed that replaces 0 with 0 is a no-op — the gate stays honestly green and the
    # teeth red spuriously (caught 2026-07-21 on the first epic of this repo).
    echo "== teeth 2/8: a status.md stamp that disagrees with its tracker → RED =="
    sed -i.bak -E 's/(<!-- epic-state: [a-z0-9-]+ phases=)[0-9]+(\/[0-9]+ -->)/\1999\2/' "$tmp/status.md"
    if run_gate; then
        echo "teeth FAILED: a stamp was falsified and the gate stayed GREEN — check B does not bite." >&2
        exit 1
    fi
    echo "   ok — the gate caught the falsified stamp"
    reset_status

    echo "== teeth 3/8: an epic with NO stamp at all (the shipped-but-undocumented class) → RED =="
    grep -v '<!-- epic-state:' "$tmp/status.md" > "$tmp/status.stripped" && mv "$tmp/status.stripped" "$tmp/status.md"
    if run_gate; then
        echo "teeth FAILED: every stamp was removed and the gate stayed GREEN — check B is not enforcing presence." >&2
        exit 1
    fi
    echo "   ok — the gate demands a stamp per enforced epic"
    reset_status

    # CHECK C — a dangling features/ link must error, not warn.
    echo "== teeth 4/8: a status.md link to a features/ dir that does not exist → RED =="
    printf '\n- [ghost](features/this-epic-does-not-exist/README.md)\n' >> "$tmp/status.md"
    if run_gate; then
        echo "teeth FAILED: a dangling feature link stayed GREEN — check C does not bite." >&2
        exit 1
    fi
    echo "   ok — the gate caught the dangling link"
    reset_status

    # The seed must be the violation this gate exists to catch — a CODE commit
    # dropped from a tracker. Deleting an arbitrary hash line is NOT that: the
    # first one is often a `docs(...)` commit, which the gate is designed to
    # ignore, so the seed would silently test nothing and the teeth would "pass"
    # a gate with no bite (it happened; hence this comment). Pick the victim from
    # git, not from the file: a commit git reports as in-scope and code-typed.
    victim=""
    victim_sha=""
    for tracker in "$tmp/features"/*/tracker.yaml; do
        [ -f "$tracker" ] || continue
        slug="$(basename "$(dirname "$tracker")")"
        grep -qE '^[[:space:]]*status:[[:space:]]*(done|in-progress)' "$tracker" || continue
        scope="$(epic_scope "$tracker" "$slug")"
        [ -n "$scope" ] || continue
        while IFS= read -r sha; do
            [ -n "$sha" ] || continue
            # …and it must actually be written down as a `- <sha>` line we can delete.
            if grep -qE "^[[:space:]]+- ${sha}\b" "$tracker"; then
                victim="$tracker"
                victim_sha="$sha"
                break 2
            fi
        done < <(git log --no-merges --format='%h %s' HEAD 2>/dev/null \
                 | grep -E "^[0-9a-f]+ ($CODE_TYPES)\(${scope}\)" | cut -d' ' -f1 || true)
    done

    if [ -z "$victim_sha" ]; then
        # Not a failure: a fresh epic has no in-scope code commits yet, so there is
        # nothing real to seed. A loud skip, not a red — same philosophy as the
        # git/tracker preconditions at the top of this block. This branch re-arms
        # itself: the seed appears with the first code commit filed in a tracker.
        echo "== teeth 5/8: SKIPPED — no tracker records an in-scope CODE commit yet =="
        echo "   check A's bite is unverified until the first \`feat|fix(...)(<scope>)\` commit lands in a tracker."
    else
        echo "== teeth 5/8: drop code commit $victim_sha from $(basename "$(dirname "$victim")") → the gate must go RED =="
        grep -vE "^[[:space:]]+- ${victim_sha}\b" "$victim" > "$victim.seeded"
        mv "$victim.seeded" "$victim"
        if run_gate; then
            echo "teeth FAILED: a code commit was dropped from a tracker and the gate stayed GREEN — check A does not bite." >&2
            exit 1
        fi
        echo "   ok — the gate caught the dropped commit"
        rm -rf "$tmp/features" && cp -R docs/features "$tmp/features"
    fi

    # CHECK D — the seed must trip D and ONLY D. Emptying a phase's commits list would also
    # trip A (the hashes vanish) and flipping a phase to done would trip B (the stamp count
    # moves) — either would let the teeth "pass" on a red that came from another check. So:
    # strip the REASON off an existing opt-out. Phase counts unchanged, no hash removed, one
    # thing different. Then assert the red text is D's, not somebody else's.
    optout_file="$(grep -rlE '^[[:space:]]*commits:[[:space:]]*\[\][[:space:]]*#[[:space:]]*none:' "$tmp/features"/*/tracker.yaml | head -1 || true)"
    if [ -z "$optout_file" ]; then
        # Same loud-skip rule as teeth 5/8: a corpus with no `# none:` opt-out yet
        # (nothing done without commits) has nothing real to seed check D against.
        echo "== teeth 6/8 + 7/8: SKIPPED — no tracker carries a \`commits: [] # none:\` opt-out yet =="
        echo "   check D's bite is unverified until the first receiptless done phase files its reason."
    else
        echo "== teeth 6/8: a done phase with an empty commits list and no stated reason → RED =="
        sed -i.bak -E 's|^([[:space:]]*commits:[[:space:]]*\[\])[[:space:]]*#[[:space:]]*none:.*$|\1|' "$optout_file"
        out="$(FEATURES_DIR="$tmp/features" STATUS_MD="$tmp/status.md" bash "$0" 2>&1 || true)"
        if ! printf '%s' "$out" | grep -q 'no commits and no stated reason'; then
            echo "teeth FAILED: an opt-out lost its reason and check D did not fire (or another check fired first):" >&2
            printf '%s\n' "$out" >&2
            exit 1
        fi
        echo "   ok — the gate demands receipts, or a reason"

        echo "== teeth 7/8: the opt-out must carry a REASON — a bare \`# none:\` does not silence it =="
        sed -i.bak -E 's|^([[:space:]]*commits:[[:space:]]*\[\])$|\1   # none:|' "$optout_file"
        out="$(FEATURES_DIR="$tmp/features" STATUS_MD="$tmp/status.md" bash "$0" 2>&1 || true)"
        if ! printf '%s' "$out" | grep -q 'no commits and no stated reason'; then
            echo "teeth FAILED: a reasonless \`# none:\` silenced check D — the opt-out is a rubber stamp." >&2
            exit 1
        fi
        echo "   ok — an empty excuse is not an excuse"
        rm -rf "$tmp/features" && cp -R docs/features "$tmp/features"
    fi

    # A gate must also stay green on the shapes that are LEGITIMATE, or it teaches people to
    # work around it. Check A counts a hash under `commits_unlisted:` as accounted for; D must
    # agree, or a phase that files its receipts there would be called receiptless and its only
    # way out would be a lie. Rename the key under a done phase — same hashes, same counts —
    # and the gate must not move.
    unlisted_file="$(grep -rlE '^[[:space:]]+commits:$' "$tmp/features"/*/tracker.yaml | head -1 || true)"
    if [ -n "$unlisted_file" ]; then
        echo "== teeth 8/8: a done phase whose receipts sit under \`commits_unlisted:\` → still GREEN =="
        sed -i.bak -E 's|^([[:space:]]+)commits:$|\1commits_unlisted:|' "$unlisted_file"
        if ! run_gate; then
            echo "teeth FAILED: receipts filed under commits_unlisted read as NO receipts — checks A and D disagree." >&2
            exit 1
        fi
        echo "   ok — the two checks mean the same thing by 'accounted for'"
    fi

    echo ""
    echo "epic-docs-drift: teeth OK — green on the real corpus; red on every violation seeded above."
    echo "                 Any seed marked SKIPPED is honestly unverified until the corpus can carry it."
    exit 0
fi

# Commits already accounted for = every git-hash-shaped token anywhere in the
# tracker. Deliberately broad: a hash under `commits:` and a hash under
# `commits_unlisted:` are BOTH an explicit statement that a human looked at this
# commit and placed it. The gate asks "is it accounted for", not "is it filed in
# the prettiest column".
listed_hashes() {
    grep -oE '\b[0-9a-f]{7,40}\b' "$1" 2>/dev/null | sort -u || true
}

for tracker in "$FEATURES_DIR"/*/tracker.yaml; do
    [ -f "$tracker" ] || continue
    slug="$(basename "$(dirname "$tracker")")"

    # An epic nobody has started has nothing to account for.
    grep -qE '^[[:space:]]*status:[[:space:]]*(done|in-progress)' "$tracker" || continue

    scope="$(epic_scope "$tracker" "$slug")"
    [ -n "$scope" ] || continue

    # ── CHECK A (commit accounting) — needs git. Skip cleanly if we have none. ──
    if [ "$HAVE_GIT" = true ]; then
        # `commit_baseline: <sha>` — only commits AFTER it are checked. This is how an
        # epic that predates the gate starts green WITHOUT hiding its debt: the debt is
        # one visible, dated line in the tracker, not an invisible exemption.
        baseline="$(grep -m1 -E '^commit_baseline:[[:space:]]*' "$tracker" 2>/dev/null | sed -E 's/^commit_baseline:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' || true)"
        range="HEAD"
        if [ -n "$baseline" ]; then
            if git cat-file -e "${baseline}^{commit}" 2>/dev/null; then
                range="${baseline}..HEAD"
            else
                echo "  [ERROR] $slug: commit_baseline '$baseline' is not a commit in this repo" >&2
                fail=1
                continue
            fi
        fi

        # ── A2: a scope that matches NOTHING is a VACUOUS green, not a pass ──────
        # A tracker whose derived scope typos or diverges from what commits actually
        # use would match zero commits and report green, while its code commits sit
        # unaccounted and every phase says `pending`. A gate that is silently blind
        # is worse than no gate: it certifies the lie. So: zero matches is only
        # acceptable if a human SAID so, by declaring `commit_scope:` explicitly. An
        # epic whose work lives on an unmerged branch declares it and passes; a
        # typo'd scope gets caught.
        scope_hits="$(git log --no-merges --format='%s' "$range" 2>/dev/null \
                      | grep -cE "^[a-z]+\(${scope}\)" || true)"
        explicit_scope="$(grep -m1 -E '^commit_scope:' "$tracker" 2>/dev/null || true)"
        if [ "${scope_hits:-0}" -eq 0 ] && [ -z "$explicit_scope" ]; then
            echo "  [ERROR] $slug — derived commit scope '$scope' matches ZERO commits."
            echo "    This gate would then pass VACUOUSLY. Either the epic has no commits yet,"
            echo "    or its commits use a different scope. Check: git log --format='%s' | grep -i <name>"
            echo "    → declare it in the tracker so the silence is a STATEMENT, not an accident:"
            echo "        commit_scope: <the scope its commits actually use>"
            fail=1
        fi

        accounted="$(listed_hashes "$tracker")"
        missing=""

        while IFS= read -r line; do
            [ -n "$line" ] || continue
            sha="${line%% *}"
            subject="${line#* }"
            # Accounted for if ANY listed hash is a prefix of this sha, or vice versa
            # (trackers record short hashes; git log gives us short hashes too, but a
            # human may have pasted a long one).
            if printf '%s\n' "$accounted" | grep -qE "^${sha}|^$(printf '%s' "$sha" | cut -c1-7)"; then
                continue
            fi
            missing="${missing}    ${sha}  ${subject}"$'\n'
        done < <(git log --no-merges --format='%h %s' "$range" 2>/dev/null \
                 | grep -E "^[0-9a-f]+ ($CODE_TYPES)\(${scope}\)" || true)

        checked=$((checked + 1))

        if [ -n "$missing" ]; then
            echo "  [ERROR] $slug — code commits scoped '$scope' that the tracker does not account for:"
            printf '%s' "$missing"
            echo "    → add each to the owning phase's \`commits:\` (or to \`commits_unlisted:\` with a reason)."
            echo "    The tracker is the committed resume point: an epic-continuation workflow trusts it over the plan."
            fail=1
        fi
    fi

    # ── CHECK D — a done phase must name its receipts, or say why it has none ───
    # The other direction from A: a phase can be closed with `commits: []` and A stays
    # green, because A only asks whether each COMMIT found a home. The cost lands on
    # whoever reads the epic later — a progress report cannot show what a finished
    # phase shipped, and a continuation workflow cannot see which work is already on
    # disk. Pure awk — runs whether or not git is available.
    receiptless="$(done_without_commits "$tracker" | tr '\n' ' ' | sed 's/ *$//')"
    if [ -n "$receiptless" ]; then
        echo "  [ERROR] $slug — phase(s) marked done with no commits and no stated reason: $receiptless"
        echo "    → list the commits that did the work under that phase's \`commits:\`,"
        echo "      or, if the phase genuinely shipped no code (research, design, a decision), say so"
        echo "      on the line, where the next reader will see it:"
        echo "          commits: []  # none: research phase — deliverable is specs/00-research.md"
        fail=1
    fi
done

# ── CHECK B — status.md's stamp must equal the tracker's phase count ─────────
# The class this exists for: status.md narrating "implementation not started"
# while several phases were done. Prose cannot be diffed; one derived number can.
stamped=0
for tracker in "$FEATURES_DIR"/*/tracker.yaml; do
    [ -f "$tracker" ] || continue
    slug="$(basename "$(dirname "$tracker")")"
    is_enforced "$slug" || continue

    counts="$(phase_counts "$tracker")"
    [ "$counts" != "0/0" ] || continue          # a tracker with no phases: nothing to state
    want="$(stamp_for "$slug" "$counts")"
    stamped=$((stamped + 1))

    found="$(grep -oE "<!-- epic-state: ${slug} phases=[0-9]+/[0-9]+ -->" "$STATUS_MD" 2>/dev/null | head -1 || true)"

    if [ -z "$found" ]; then
        echo "  [ERROR] $slug — no epic-state stamp in $STATUS_MD (the tracker says $counts phases done)."
        echo "    → the epic's entry in $STATUS_MD must carry, on its own line or inline:"
        echo "        $want"
        echo "    An epic with no entry at all is the loudest case: it shipped and the docs never heard."
        fail=1
    elif [ "$found" != "$want" ]; then
        echo "  [ERROR] $slug — $STATUS_MD is out of date with the tracker:"
        echo "        stamp says: $found"
        echo "        tracker says: $want"
        echo "    → fix the stamp AND the prose beside it. The gate only checks the number;"
        echo "      the sentence next to it is on you (that is the honest limit of this check)."
        fail=1
    fi
done

# ── CHECK C — every features/<slug> reference in status.md must exist ────────
while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    [ -d "$FEATURES_DIR/$ref" ] && continue
    echo "  [ERROR] $STATUS_MD references features/$ref/ — no such directory."
    echo "    → if the epic MOVED (docs/archive/), repoint the link and its LABEL; don't delete the pointer."
    fail=1
done < <(grep -oE 'features/[a-z0-9][a-z0-9-]*' "$STATUS_MD" 2>/dev/null | sed 's|features/||' | sort -u || true)

if [ "$checked" -eq 0 ] && [ "$stamped" -eq 0 ]; then
    if [ "$HAVE_GIT" = true ]; then
        echo "epic-docs-drift: no started epics to check"
    else
        echo "epic-docs-drift: no started epics to check (checks B/C/D only — check A is skipped, no git repo)"
    fi
    exit 0
fi

if [ "$fail" -ne 0 ]; then
    echo ""
    echo "epic-docs-drift: FAILED — an epic's committed docs do not match its reality." >&2
    exit 1
fi

if [ "$HAVE_GIT" = true ]; then
    echo "epic-docs-drift: ok ($checked started epic(s): every code commit accounted for; $stamped stamped epic(s): status.md matches the tracker; all feature links resolve)"
else
    echo "epic-docs-drift: ok ($stamped stamped epic(s): status.md matches the tracker; all feature links resolve; check A skipped — no git repo)"
fi
