#!/usr/bin/env bash
# Every feature in the release being cut must make an explicit public-roadmap
# decision. This prevents a new capability from silently missing Shipped now,
# while `omit` keeps the page curated rather than turning it into a changelog.
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
  echo "roadmap-release-decisions --teeth: added undecided feature reds by id; an explicit decision greens."
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
