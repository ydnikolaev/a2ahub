#!/usr/bin/env bash
# Teeth for scripts/classify-guard.sh — the publish-boundary gate.
#
# This gate went from 2026-07-25 to 2026-08-18 as the only one in the repo with
# no self-test, and the reason was structural rather than neglect: it judged
# `$(git rev-parse --show-toplevel)` and nothing else, so the only tree it could
# ever be pointed at was the one that is green by construction. There was
# nowhere to put a violating fixture. `CLASSIFY_GUARD_ROOT` (added with this
# file) is the seam; every case below builds its own repository.
#
# Why that matters more here than for any other gate: this is the check that
# stands between a private harness path and the public repository. Its cost of
# being wrong is a leak, and until now the only evidence that it BITES was that
# it had once been seen to.
#
# Each case seeds a tree that is green except for the one violation under test,
# so a red is attributable to that violation and not to fixture noise. The
# green baseline runs FIRST and is asserted green — a suite whose "red" cases
# all pass because the fixture was broken proves nothing, and pinning the green
# case first is the discipline check-dashboard-cards.sh already writes down.
#
# The fixture's `.gitignore` is the REAL one, copied. Hand-writing a minimal
# substitute would make check 3 compare this file's idea of the ignore rules
# against itself — the fixture-agrees-with-itself trap that
# check_contract_carried_set.sh records in its own header, and the same reason
# the gate's boundary lists are never read from the tree under judgement.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/classify-guard.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "classify-guard-test: FAIL — $*" >&2
  exit 1
}

# A minimal tree the gate calls clean: a git repo, the real ignore rules, and
# one tracked file that is unambiguously PUBLIC.
seed() { # $1 = name -> prints the tree path
  local tree="$WORK/$1"
  mkdir -p "$tree"
  git -C "$tree" init -q -b main
  git -C "$tree" config user.email teeth@example.com
  git -C "$tree" config user.name teeth
  cp "$ROOT/.gitignore" "$tree/.gitignore"
  printf 'placeholder\n' > "$tree/README.md"
  git -C "$tree" add .gitignore README.md
  git -C "$tree" commit -qm baseline
  printf '%s' "$tree"
}

expect_red() { # $1 = tree, $2 = needle, $3 = label
  local tree="$1" needle="$2" label="$3" output
  if output="$(CLASSIFY_GUARD_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed GREEN"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label went red, but not with '$needle': $output"
  fi
  echo "  ✓ $label → RED"
}

expect_green() { # $1 = tree, $2 = label
  local tree="$1" label="$2" output
  if ! output="$(CLASSIFY_GUARD_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label went RED on a conforming tree: $output"
  fi
  echo "  ✓ $label → GREEN"
}

# ── the real tree, first: the gate must still pass what it passes today ───────
bash "$GATE" >/dev/null || fail "the real repository is not green"

# ── baseline: a conforming fixture is green, so every red below is the seeded
#    violation and not the fixture ─────────────────────────────────────────────
expect_green "$(seed baseline)" "baseline fixture (no false positive)"

# ── check 1: a private path is TRACKED ───────────────────────────────────────
# `AGENTS.md` is a DENY_FILE and is gitignored, so this needs -f — which is
# exactly how the real mistake happens: somebody forces an add past the ignore
# rule, and the ignore rule then vouches for nothing.
tree="$(seed tracked-private)"
printf 'private harness\n' > "$tree/AGENTS.md"
git -C "$tree" add -f AGENTS.md
git -C "$tree" commit -qm "track a private path"
expect_red "$tree" "tracked but NOT public: AGENTS.md" "check 1: a DENY file is tracked"

# A private path buried inside an ALLOW dir is the same class one level down —
# `.claude/` is a DENY dir, but a tracked file under an allowed tree is caught
# by the top-level classification of its own first segment.
tree="$(seed tracked-private-dir)"
mkdir -p "$tree/.mate"
printf 'x\n' > "$tree/.mate/config.yaml"
git -C "$tree" add -f .mate/config.yaml
git -C "$tree" commit -qm "track a private dir"
expect_red "$tree" "tracked but NOT public: .mate/config.yaml" "check 1: a DENY dir is tracked"

# ── check 2: an UNCLASSIFIED top-level entry ─────────────────────────────────
# Untracked on purpose: check 2 judges what is PRESENT, which is what makes it
# the "never forget to classify" half. A new directory nobody has decided about
# must stop the gate before it is committed, not after.
tree="$(seed unclassified)"
mkdir -p "$tree/somenewthing"
printf 'x\n' > "$tree/somenewthing/file.txt"
expect_red "$tree" "UNCLASSIFIED top-level entry: somenewthing" "check 2: an undecided top-level entry"

# The gate uses `for e in *` rather than `$(ls -A)` specifically so an entry
# whose name contains IFS whitespace cannot word-split past classification.
# That reasoning is written in the source; this is the case that holds it.
tree="$(seed unclassified-space)"
mkdir -p "$tree/new thing"
printf 'x\n' > "$tree/new thing/file.txt"
expect_red "$tree" "UNCLASSIFIED top-level entry: new thing" "check 2: a top-level name containing whitespace"

# ── check 3: a DENY path .gitignore does not actually hide ───────────────────
# The manifest and the ignore file are two statements of one boundary. When
# they disagree, the manifest is the one people read and the ignore file is the
# one git obeys.
tree="$(seed deny-not-ignored)"
grep -v '^\.mate/$' "$tree/.gitignore" > "$tree/.gitignore.new"
mv "$tree/.gitignore.new" "$tree/.gitignore"
grep -q '^\.mate/$' "$tree/.gitignore" && fail "could not remove the .mate/ ignore rule from the fixture"
expect_red "$tree" "DENY dir not ignored: .mate/" "check 3: a DENY dir missing from .gitignore"

# The inverse direction: a path the gate insists must stay PUBLIC, silently
# ignored. `scripts/*` is ignored wholesale with an `!` allowlist, so dropping
# one exception is a one-line edit that publishes nothing and says nothing.
tree="$(seed public-wrongly-ignored)"
grep -v '^!scripts/install\.sh$' "$tree/.gitignore" > "$tree/.gitignore.new"
mv "$tree/.gitignore.new" "$tree/.gitignore"
expect_red "$tree" "scripts/install.sh must stay PUBLIC" "check 3: a public script loses its ! exception"

# ── check 4: the publisher STRIPS a file this gate calls PUBLIC ──────────────
# The real incident (2026-08-06): `scripts/lib/lane-ungated.txt` was classified
# PUBLIC here while the publisher stripped all of `scripts/lib/`, so the public
# tree shipped a gate without the list it reads and `make check` refused on its
# first phase. It cost a full release cycle, because the private tree is green
# by construction and only a candidate executes the filtered one.
tree="$(seed publisher-strips-public)"
mkdir -p "$tree/docs/runbooks"
cat > "$tree/docs/runbooks/publish-to-public.sh" <<'PUBLISHER'
#!/usr/bin/env bash
STRIP=(
  --path .claude/
  --path README.md
)
PUBLISHER
expect_red "$tree" "README.md is classified PUBLIC here but" "check 4: the publisher strips a PUBLIC file by name"

# The directory form, which is how the real one actually bit: the strip names a
# tree, not the file, so nothing matches on a filename comparison.
tree="$(seed publisher-strips-public-dir)"
mkdir -p "$tree/docs/runbooks"
cat > "$tree/docs/runbooks/publish-to-public.sh" <<'PUBLISHER'
#!/usr/bin/env bash
STRIP=(
  --path scripts/lib/
)
PUBLISHER
expect_red "$tree" "scripts/lib/lane-ungated.txt is classified PUBLIC here but" "check 4: a directory strip swallows a PUBLIC file"

# A publisher whose STRIP block cannot be read must refuse, not pass. A check
# that silently vouches for a boundary it could not parse is worse than absent.
tree="$(seed publisher-unreadable)"
mkdir -p "$tree/docs/runbooks"
printf '#!/usr/bin/env bash\n# no STRIP block at all\n' > "$tree/docs/runbooks/publish-to-public.sh"
expect_red "$tree" "could not read its STRIP=( ) block" "check 4: an unreadable STRIP block refuses"

# ── check 5: a PUBLIC gate sources a PRIVATE file ────────────────────────────
# The 2026-08-12 incident: seven tracked gates sourced `scripts/lib/gate-lib.sh`
# while STRIP deleted `scripts/lib/`. Each dies on the candidate with "No such
# file or directory" — one edge further out in the graph than check 4 sees,
# because the missing thing is not the public file but what it reads.
#
# The two fixtures below assemble their `source` line with a printf `%s` rather
# than writing it literally, and that is not stylistic. This file is itself
# classified PUBLIC and matches `scripts/*.sh`, so check 5 scans IT — and a
# heredoc containing a line that begins with `source` is indistinguishable,
# to a line-oriented extractor, from this script really depending on a private
# file. The gate would flag its own teeth. Same shape as the notify-secrets
# gate, whose fixtures assemble credential literals at runtime for exactly this
# reason; the repo's answer to a gate catching its own fixtures is runtime
# assembly, never a self-exemption, because an exemption is the thing that
# would later hide a real one.
tree="$(seed sources-private)"
mkdir -p "$tree/scripts/lib"
{
  printf '#!/usr/bin/env bash\n'
  printf '%s "$(dirname "${BASH_SOURCE[0]}")/lib/private-helper.sh"\n' source
} > "$tree/scripts/check-gosec-scope.sh"
expect_red "$tree" "sources scripts/lib/private-helper.sh, which is NOT classified PUBLIC" "check 5: a public gate sources a private file"

# The refuse-loudly half. The extractor reads exactly one `source` form; a form
# it cannot resolve is FLAGGED rather than skipped, so a new spelling forces the
# extractor to be taught instead of quietly widening the hole.
tree="$(seed sources-unresolvable)"
mkdir -p "$tree/scripts"
{
  printf '#!/usr/bin/env bash\n'
  printf '%s ./lib/whatever.sh\n' source
} > "$tree/scripts/check-gosec-scope.sh"
expect_red "$tree" "has a \`source\` line this check cannot resolve" "check 5: an unresolvable source form refuses"

echo "classify_guard_test: ok — real tree green, baseline fixture green, and 11 seeded violations (tracked DENY file, tracked DENY dir, unclassified entry, whitespace name, DENY missing from .gitignore, public script losing its ! exception, publisher stripping a PUBLIC file by name and by directory, an unreadable STRIP block, a public gate sourcing a private file, an unresolvable source form) all red as designed"
