#!/usr/bin/env bash
# Every feature in the release being cut must make an explicit public-roadmap
# decision. This prevents a new capability from silently missing Shipped now,
# while `omit` keeps the page curated rather than turning it into a changelog.
#
# lane-inputs:
#   releasenotes/*.yaml
set -euo pipefail

check_one() {
  local file="$1" output rc
  [ -f "$file" ] || { echo "roadmap-release-decisions: FAIL — release notes not found: $file" >&2; return 1; }
  set +e
  output="$(awk '
    function finish() {
      if (kind == "feat" && (visibility == "" || why == "")) {
        printf "%s%s%s\n", id, (visibility == "" ? " missing roadmap.visibility" : ""), (why == "" ? " missing roadmap.why" : "")
        bad = 1
      }
    }
    /^  - id: / { finish(); id=$3; kind=""; visibility=""; why=""; next }
    /^    kind: / { kind=$2; next }
    /^      visibility: / { visibility=$2; next }
    /^      why: / { why=$0; sub(/^      why:[[:space:]]*/, "", why); next }
    END { finish(); exit bad }
  ' "$file")"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "roadmap-release-decisions: FAIL — every feat needs roadmap.visibility (shipped|omit) and a reason:" >&2
    printf '%s\n' "$output" >&2
    return 1
  fi
  # The awk above proves every feat CARRIES a decision; this proves the value
  # is one of the two legal ones (it would pass `visibility: maybe`).
  #
  # Only a release that ships a feature owes a decision. A fix-only patch
  # legitimately has none — 0.15.1, 0.15.2, 0.16.1, 0.16.2, 0.16.3 and 0.17.1
  # all carry zero. This gate landed 2026-08-01, one day after the last of
  # them, and every release since happened to carry a feat, so an unconditional
  # requirement here sat latent until the first fix-only release met it and was
  # told to invent a roadmap decision for a bug fix.
  if ! grep -Eq '^    kind: feat$' "$file"; then
    echo "roadmap-release-decisions: $(basename "$file") ships no feature; no roadmap decision is owed."
    return 0
  fi
  if grep -Eq '^      visibility: (shipped|omit)$' "$file"; then
    echo "roadmap-release-decisions: $(basename "$file") has an explicit decision for every feature."
  else
    echo "roadmap-release-decisions: FAIL — no valid shipped|omit decisions in $file" >&2
    return 1
  fi
}

teeth() {
  local tmp out
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  cat >"$tmp/0.1.0.yaml" <<'YAML'
changes:
  - id: RN-1
    kind: feat
    subject: new capability
YAML
  if out="$(check_one "$tmp/0.1.0.yaml" 2>&1)"; then
    echo "roadmap-release-decisions --teeth: FAILED — a newly added feature without a decision stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'RN-1 missing roadmap.visibility missing roadmap.why' || {
    echo "roadmap-release-decisions --teeth: FAILED — red did not name the undecided feature" >&2
    exit 1
  }
  cat >"$tmp/0.1.0.yaml" <<'YAML'
changes:
  - id: RN-1
    kind: feat
    roadmap:
      visibility: shipped
      why: "This capability belongs in the public summary."
YAML
  check_one "$tmp/0.1.0.yaml" >/dev/null

  # A decision the schema would reject must still red: the awk above only
  # proves the key is PRESENT, so this value check is the half that judges it.
  cat >"$tmp/0.1.0.yaml" <<'YAML'
changes:
  - id: RN-1
    kind: feat
    roadmap:
      visibility: maybe
      why: "Undecided dressed as a decision."
YAML
  if check_one "$tmp/0.1.0.yaml" >/dev/null 2>&1; then
    echo "roadmap-release-decisions --teeth: FAILED — an illegal visibility value stayed green" >&2
    exit 1
  fi

  # A fix-only release owes no decision and must NOT be forced to invent one.
  cat >"$tmp/0.1.0.yaml" <<'YAML'
changes:
  - id: RN-1
    kind: fix
    subject: a defect, carrying no roadmap decision
YAML
  if ! out="$(check_one "$tmp/0.1.0.yaml" 2>&1)"; then
    echo "roadmap-release-decisions --teeth: FAILED — a fix-only release was required to carry a roadmap decision" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'ships no feature' || {
    echo "roadmap-release-decisions --teeth: FAILED — a fix-only release greened without saying why it was exempt" >&2
    exit 1
  }

  echo "roadmap-release-decisions --teeth: undecided feature reds by id; an illegal value reds; an explicit decision greens; a fix-only release is exempt."
}

if [ "${1:-}" = "--teeth" ]; then teeth; exit 0; fi
version="${1:-}"
if [ -n "$version" ]; then
  version="${version#v}"
  target="releasenotes/$version.yaml"
else
  target="$(find releasenotes -maxdepth 1 -type f -name '[0-9]*.[0-9]*.[0-9]*.yaml' -print | sort -V | tail -1)"
fi
check_one "$target"
