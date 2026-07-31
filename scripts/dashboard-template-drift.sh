#!/usr/bin/env bash
# internal/html/template.html is GENERATED from web/design-source/ and committed
# so the Go build stays hermetic. That combination is exactly the shape that
# silently forks: a fix typed straight into the generated file builds, ships and
# is then erased by the next regeneration — which is precisely what happened
# once (a per-space artifact lookup and an attention-scope comment lived only in
# template.html and were reverted by an unrelated rebuild).
#
# This gate closes it: regenerate into a scratch copy and require the committed
# bytes to match. It is presence-gated on web/node_modules, because the build
# needs the design runtime and the font packages; a checkout without them skips
# rather than fails, the same way the other tooling-dependent gates behave.
#
# --teeth runs the gate's own self-test: mutate the committed file, prove the
# gate goes red, restore it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template="$root/internal/html/template.html"
web="$root/web"

fail() { printf 'dashboard-template-drift: %s\n' "$1" >&2; exit 1; }

if [ ! -d "$web/node_modules" ]; then
  echo "dashboard-template-drift: skipped (web/node_modules absent — run 'npm ci' in web/ to enable)"
  exit 0
fi

regenerate() {
  (cd "$web" && npm run --silent build:dashboard >/dev/null)
}

check() {
  local before after
  before="$(git -C "$root" hash-object "$template")"
  regenerate
  after="$(git -C "$root" hash-object "$template")"
  # Always restore the committed bytes: the gate must not leave the tree dirty
  # whichever way it answers.
  git -C "$root" checkout -- internal/html/template.html 2>/dev/null || true
  [ "$before" = "$after" ]
}

if [ "${1:-}" = "--teeth" ]; then
  # The gate must go red when the generated file is edited by hand.
  trap 'git -C "$root" checkout -- internal/html/template.html 2>/dev/null || true' EXIT
  printf '\n<!-- teeth: hand edit -->\n' >> "$template"
  if check; then fail 'teeth failed: a hand edit to the generated template did not trip the gate'; fi
  git -C "$root" checkout -- internal/html/template.html
  if ! check; then fail 'teeth failed: the committed template does not match its own regeneration'; fi
  echo "dashboard-template-drift: teeth ok (hand edits trip the gate, clean tree passes)"
  exit 0
fi

if ! check; then
  fail "internal/html/template.html differs from a fresh build of web/design-source/.
  The generated file is not the source of truth. Make the change in
  web/design-source/, then run: (cd web && npm run build:dashboard)"
fi

echo "dashboard-template-drift: template.html matches web/design-source/"
