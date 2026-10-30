#!/usr/bin/env bash
# Teeth for scripts/check-pipefail-early-exit.sh (release-speed-2026-11 P9,
# spec 09 section 6). Every case writes a small script into a scratch
# directory and hands it to the lint by path, so the real tree is never
# mutated. A red case names the exact file:line it expects; a green case
# expects exit 0.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-pipefail-early-exit.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "pipefail-early-exit-test: FAIL — $*" >&2
  exit 1
}

# write_fixture <name> <body...> — body is the literal script text.
write_fixture() {
  local name="$1"
  shift
  printf '%s\n' "$@" >"$WORK/$name"
}

# expect_red <name> <line> <shape> — the lint exits non-zero and names
# <name>:<line>: <shape> under pipefail.
expect_red() {
  local name="$1" line="$2" shape="$3" out rc=0
  out="$(cd "$WORK" && bash "$GATE" "$name" 2>&1)" || rc=$?
  [ "$rc" -ne 0 ] || fail "$name stayed green: $out"
  if ! grep -Fq "$name:$line: $shape under pipefail" <<<"$out"; then
    fail "$name red did not name '$name:$line: $shape under pipefail': $out"
  fi
}

expect_green() {
  local name="$1" out rc=0
  out="$(cd "$WORK" && bash "$GATE" "$name" 2>&1)" || rc=$?
  [ "$rc" -eq 0 ] || fail "$name went red: $out"
}

# --- the shape under each spelling of pipefail -----------------------------
for spelling in 'set -euo pipefail' 'set -o pipefail' 'set -eo pipefail' 'set -uo pipefail' 'set -e -o pipefail' 'set -o errexit -o pipefail' 'shopt -so pipefail' 'if true; then set -o pipefail; fi'; do
  write_fixture spell.sh '#!/usr/bin/env bash' "$spelling" "printf 'a\\n' | grep -q a"
  expect_red spell.sh 3 'grep -q'
done

# --- each early-exit consumer ----------------------------------------------
write_fixture shapes.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "printf 'a\\n' | grep -q a" \
  "printf 'a\\n' | grep -l a" \
  "printf 'a\\n' | grep -m1 a" \
  "printf 'a\\n' | grep -m 1 a" \
  "printf 'a\\n' | head -1" \
  "printf 'a\\n' | head -n 2" \
  "printf 'a\\n' | grep -qE 'a|b'" \
  "printf 'a\\n' | grep -E -q 'a|b'" \
  "printf 'a\\n' | grep -iq a" \
  "printf 'a\\n' | grep --quiet a" \
  "printf 'a\\n' | grep --files-with-matches a" \
  "printf 'a\\n' | awk '/a/ { found=1; exit } END { exit !found }'" \
  "printf 'a\\n' | LC_ALL=C grep -q a" \
  "printf 'a\\n' | /usr/bin/grep -q a" \
  "if printf 'a\\n' | grep -q a; then :; fi" \
  "printf 'a\\n' | grep -q a && echo yes" \
  "! printf 'a\\n' | grep -q a" \
  "x=\$(printf 'a\\n' | grep -q a)" \
  "x=\"\$(printf 'a\\n' | head -1)\"" \
  "{ printf 'a\\n' | grep -q a; }"
expect_red shapes.sh 3 'grep -q'
expect_red shapes.sh 4 'grep -l'
expect_red shapes.sh 5 'grep -m'
expect_red shapes.sh 6 'grep -m'
expect_red shapes.sh 7 'head'
expect_red shapes.sh 8 'head'
expect_red shapes.sh 9 'grep -q'
expect_red shapes.sh 10 'grep -q'
expect_red shapes.sh 11 'grep -q'
expect_red shapes.sh 12 'grep -q'
expect_red shapes.sh 13 'grep -l'
expect_red shapes.sh 14 'awk exit'
expect_red shapes.sh 15 'grep -q'
expect_red shapes.sh 16 'grep -q'
expect_red shapes.sh 17 'grep -q'
expect_red shapes.sh 18 'grep -q'
expect_red shapes.sh 19 'grep -q'
expect_red shapes.sh 20 'grep -q'
expect_red shapes.sh 21 'head'
expect_red shapes.sh 22 'grep -q'

write_fixture bt.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "x=\`printf 'a\\n' | head -1\`" \
  "y=\`echo a\` && printf 'b\\n' | grep -q b"
expect_red bt.sh 3 'head'
expect_red bt.sh 4 'grep -q'

# --- a middle stage kills its producer just as a last stage does; a first stage has no producer ---
write_fixture middle.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "x=\"\$(printf 'a\\n' | head -1 | cut -c1)\"" \
  "printf 'a\\n' | grep -m1 a | cut -c1" \
  "printf 'a\\n' | awk '{exit}' | cut -c1"
expect_red middle.sh 3 'head'
expect_red middle.sh 4 'grep -m'
expect_red middle.sh 5 'awk exit'
write_fixture first.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "head -1 /etc/hosts | cut -c1" \
  "grep -m1 a /etc/hosts | cut -c1"
expect_green first.sh

# --- without pipefail the same line is not this lint's business ------------
write_fixture nopf.sh '#!/usr/bin/env bash' 'set -eu' "printf 'a\\n' | grep -q a" "printf 'a\\n' | head -1"
expect_green nopf.sh
write_fixture unsetpf.sh '#!/usr/bin/env bash' 'set +o pipefail' "printf 'a\\n' | grep -q a"
expect_green unsetpf.sh
write_fixture commentpf.sh '#!/usr/bin/env bash' '# set -o pipefail' "printf 'a\\n' | grep -q a"
expect_green commentpf.sh

# --- the fixes are green ---------------------------------------------------
write_fixture fixed.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "x=\$(printf 'a\\n')" \
  'grep -q a <<<"$x"' \
  "grep -q a <<<\"\$(printf 'a\\n')\"" \
  "printf 'a\\n' | grep -c a" \
  "printf 'a\\n' | grep a >/dev/null" \
  "printf 'a\\n' | sed -n '1p'" \
  "printf 'a\\n' | sed -n '1,3p'" \
  "printf 'a\\n' | awk 'NR == 1 { print } END { exit 0 }'" \
  "printf 'a\\n' | awk '/a/ { f=1 } END { exit !f }'" \
  "[ -n \"\$(printf 'a\\n' | grep a)\" ]" \
  "grep -q a file || true" \
  "[ -f x ] || grep -q a file" \
  "case \"\$x\" in grep|head) : ;; esac" \
  "echo \"\${x:-\"y | grep -q z\"}\" | cat" \
  "printf 'a\\n' | grep -v q" \
  "echo \"a | grep -q b\"" \
  "echo 'a | head -1'" \
  "echo \${#x} \${x%|*} | cat" \
  "# printf 'a\\n' | grep -q a" \
  "printf 'a\\n' >|out.txt" \
  "cmd 2>&1 | cat"
expect_green fixed.sh

# --- the marker ------------------------------------------------------------
write_fixture marker.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "yes | grep -q y # pipefail-ok: yes never ends, the consumer must cut it"
expect_green marker.sh
write_fixture marker_empty.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "yes | grep -q y # pipefail-ok:"
expect_red marker_empty.sh 3 'grep -q'
write_fixture marker_wrong.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "# pipefail-ok: on the line above does not cover the next line" \
  "yes | grep -q y"
expect_red marker_wrong.sh 4 'grep -q'

# --- multi-line pipelines --------------------------------------------------
write_fixture multi.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  'printf "a\n" |' \
  '  grep -q a'
expect_red multi.sh 4 'grep -q'
write_fixture multi_bs.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  'printf "a\n" \' \
  '  | grep -q a'
expect_red multi_bs.sh 4 'grep -q'
write_fixture multi_mid.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  'printf "a\n" |' \
  '  grep a |' \
  '  head -1'
expect_red multi_mid.sh 5 'head'
write_fixture multi_ok.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  'printf "a\n" |' \
  '  grep -q a # pipefail-ok: fixture, marker on the continuation line'
expect_green multi_ok.sh
write_fixture multi_inner.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  'x=$(' \
  '  printf "a\n" | head -1' \
  ')'
expect_red multi_inner.sh 4 'head'

# --- heredoc bodies are data -----------------------------------------------
write_fixture heredoc.sh '#!/usr/bin/env bash' 'set -euo pipefail' \
  "cat <<'EOF'" \
  "printf 'a\\n' | grep -q a" \
  'EOF' \
  "printf 'a\\n' | grep -q a"
expect_red heredoc.sh 6 'grep -q'
grep -Fq 'heredoc.sh:4:' < <(cd "$WORK" && bash "$GATE" heredoc.sh 2>&1) && fail "heredoc body was judged as code"

# --- an unreadable input is UNMEASURED, never green ------------------------
rc=0
out="$(cd "$WORK" && bash "$GATE" no-such-file.sh 2>&1)" || rc=$?
[ "$rc" -ne 0 ] || fail "a missing input stayed green: $out"
grep -Fq UNMEASURED <<<"$out" || fail "a missing input was not reported UNMEASURED: $out"

# --- the real tree is green (the drain's own guard) ------------------------
bash "$GATE" >/dev/null || fail "the tracked shell tree still has sites; run: bash scripts/check-pipefail-early-exit.sh"

echo "pipefail-early-exit-test: ok"
