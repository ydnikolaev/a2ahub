#!/usr/bin/env bash
# check-pipefail-early-exit.sh — refuse a pipeline with a consuming stage that quits
# reading early, in a shell script that turns pipefail on.
#
# THE DEFECT. A consumer that exits at its first match (the quiet and
# files-with-matches forms of grep, grep with a match cap, head, an awk program
# that calls exit) closes the pipe while the producer is still writing. The
# producer is killed by SIGPIPE and reports 141, and pipefail promotes that
# status to the status of the whole pipeline. Whether the producer is still
# writing at that moment depends on buffer size and scheduler pressure, so the
# same line passes on a quiet laptop and fails on a loaded runner. Under a
# negation the direction flips: a found match reads as not found and the gate
# goes FALSE GREEN. Seven of the ~26 reds between v0.26.0 and v0.27.1 were
# gate defects of exactly this shape (release-speed-2026-11 P9).
#
# THE FIXES (none of them is a pipe): feed the consumer a here-string built
# from a command substitution or from a variable the producer already filled;
# or use a consumer that reads to EOF (a counting grep, or a stream editor or
# awk program with no early exit).
#
# THE SCOPE. Every tracked shell script that sets pipefail (any spelling: set
# with -o, -euo, -eo, -uo, or shopt -so), except the lane package's test data,
# which are parser fixtures that never run. Every stage after the first is
# judged: a middle stage that quits kills its producer exactly as a last stage
# does, and pipefail reports that status either way. A line (or any line of the
# same pipeline) carrying the marker
# comment pipefail-ok followed by a colon and a NON-EMPTY reason is exempt: use
# it only where the producer genuinely must be cut short.
#
# Output per site: file:line: <shape> under pipefail; use a here-string or read to EOF
#
# Usage: bash scripts/check-pipefail-early-exit.sh [file ...]
#        With no arguments it judges the tracked tree; with arguments it
#        judges exactly those files (the tooth uses this).
#
# lane-inputs: **/*.sh
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

files=()
if [ "$#" -gt 0 ]; then
  files=("$@")
else
  cd "$GATE_ROOT" || { gate_unmeasured "cannot enter $GATE_ROOT"; gate_summary pipefail-early-exit; exit $?; }
  listing="$(git ls-files -- '*.sh' ':(exclude)internal/lane/testdata/**')" || {
    gate_unmeasured "git ls-files failed; the tracked shell tree is unknown"
    gate_summary pipefail-early-exit
    exit $?
  }
  while IFS= read -r f; do
    [ -n "$f" ] && files+=("$f")
  done <<<"$listing"
fi

if [ "${#files[@]}" -eq 0 ]; then
  gate_unmeasured "no shell files to judge"
  gate_summary pipefail-early-exit
  exit $?
fi

for f in "${files[@]}"; do
  if [ ! -r "$f" ]; then
    gate_unmeasured "cannot read $f"
  fi
done

# One awk program over every file. It walks each script character by
# character with a small context stack (code, double quote, single quote,
# parameter expansion, arithmetic), so a pipe or a quote inside a string never
# counts, a command substitution is judged as its own code, a pipeline that
# continues on the next line is one pipeline, and heredoc bodies are skipped.
findings="$(awk '
function rtop(   k) { k = d; while (typ[k] != "c") k--; return k }
function app(ch, m,   k) {
  k = rtop(); raw[k] = raw[k] ch; mask[k] = mask[k] m
  if (sline[k] == 0 && ch !~ /[ \t]/) sline[k] = lineno
}
function push(t) {
  d++; typ[d] = t
  if (t == "c") { raw[d] = ""; mask[d] = ""; inpipe[d] = 0; sline[d] = 0; bt[d] = 0 }
  par[d] = 0
}
function pop() { d-- }
function startpipe(k) {
  if (inpipe[k] && mask[k] ~ /[^ \t]/) check(k)
  inpipe[k] = 1; raw[k] = ""; mask[k] = ""; sline[k] = 0
}
function term(k) {
  if (inpipe[k] && mask[k] ~ /[^ \t]/) check(k)
  inpipe[k] = 0; raw[k] = ""; mask[k] = ""; sline[k] = 0
}
function shape(m, r,   n, a, i, w, cmd, j, t, c, ch, sh, pre, ix) {
  n = split(m, a, /[ \t]+/)
  i = 1
  while (i <= n) {
    w = a[i]
    if (w == "" || w == "!" || w == "{" || w == "command" || w == "builtin" || w == "exec" || w == "time" || w == "env" || w == "sudo" || w == "nice" || w ~ /^[A-Za-z_][A-Za-z0-9_]*=/) { i++; continue }
    break
  }
  if (i > n) return ""
  cmd = a[i]; sub(/^.*\//, "", cmd)
  if (cmd == "head") return "head"
  if (cmd ~ /^(awk|gawk|mawk|nawk)$/) {
    ix = index(r, "END")
    pre = ix ? substr(r, 1, ix - 1) : r
    if (pre ~ /(^|[^A-Za-z_])exit([^A-Za-z_]|$)/) return "awk exit"
    return ""
  }
  if (cmd ~ /^(grep|egrep|fgrep|ggrep|zgrep|rg)$/) {
    sh = ""
    for (j = i + 1; j <= n; j++) {
      t = a[j]
      if (t == "--") break
      if (t ~ /^--/) {
        if (t == "--quiet" || t == "--silent") sh = sh == "" ? "grep -q" : sh
        else if (t == "--files-with-matches") sh = sh == "" ? "grep -l" : sh
        else if (t ~ /^--max-count/) sh = sh == "" ? "grep -m" : sh
        continue
      }
      if (t ~ /^-[A-Za-z0-9]/) {
        for (c = 2; c <= length(t); c++) {
          ch = substr(t, c, 1)
          if (ch == "q") { if (sh == "") sh = "grep -q" }
          else if (ch == "l") { if (sh == "") sh = "grep -l" }
          else if (ch == "m") { if (sh == "") sh = "grep -m"; break }
          else if (ch ~ /[efABC]/) break
        }
      }
    }
    return sh
  }
  return ""
}
function check(k,   s, ok, j) {
  s = shape(mask[k], raw[k])
  if (s == "") return
  ok = 0
  for (j = sline[k]; j <= lineno; j++) if (j in okline) ok = 1
  if (ok) return
  nv++; vl[nv] = sline[k]; vs[nv] = s
}
function scan(line,   n, i, ch, nx, prev, t, k, rest, w) {
  n = length(line)
  for (i = 1; i <= n; i++) {
    ch = substr(line, i, 1); nx = substr(line, i + 1, 1)
    prev = i > 1 ? substr(line, i - 1, 1) : ""
    t = typ[d]
    if (t == "s") {
      if (ch == "\047") { app(ch, ch); pop() } else app(ch, "x")
      continue
    }
    if (t == "d") {
      if (ch == "\\") { app(ch, "x"); if (nx != "") { app(nx, "x"); i++ } }
      else if (ch == "\"") { app(ch, ch); pop() }
      else if (ch == "$" && nx == "(") {
        app("S", "S")
        if (substr(line, i + 2, 1) == "(") { push("a"); i += 2 } else { push("c"); i++ }
      }
      else if (ch == "$" && nx == "{") { app("S", "S"); push("b"); i++ }
      else if (ch == "`") { app("S", "S"); push("c"); bt[d] = 1 }
      else app(ch, "x")
      continue
    }
    if (t == "b") {
      if (ch == "\\") i++
      else if (ch == "}") pop()
      else if (ch == "\047") { app(ch, ch); push("s") }
      else if (ch == "\"") { app(ch, ch); push("d") }
      else if (ch == "$" && nx == "{") { push("b"); i++ }
      continue
    }
    if (t == "a") {
      if (ch == "\\") i++
      else if (ch == "(") par[d]++
      else if (ch == ")") { if (par[d] > 0) par[d]--; else { pop(); if (nx == ")") i++ } }
      continue
    }
    # code context
    k = d
    if (ch == " " || ch == "\t") app(ch, ch)
    else if (ch == "\\") {
      if (nx == "") contflag = 1
      else { app(ch, "x"); app(nx, "x"); i++ }
    }
    else if (ch == "\047") { app(ch, ch); push("s") }
    else if (ch == "\"") { app(ch, ch); push("d") }
    else if (ch == "`") {
      if (bt[d]) { term(d); pop(); app("S", "S") }
      else { app("S", "S"); push("c"); bt[d] = 1 }
    }
    else if (ch == "$" && nx == "(") {
      app("S", "S")
      if (substr(line, i + 2, 1) == "(") { push("a"); i += 2 } else { push("c"); i++ }
    }
    else if (ch == "$" && nx == "{") { app("S", "S"); push("b"); i++ }
    else if (ch == "(") {
      app("S", "S")
      if (nx == "(") { push("a"); i++ } else push("c")
    }
    else if (ch == ")") {
      if (d > 0 && !bt[d]) { term(d); pop(); app("S", "S") }
      else { inpipe[k] = 0; raw[k] = ""; mask[k] = ""; sline[k] = 0 }  # case-pattern close: not a stage end
    }
    else if (ch == "|") {
      if (prev == ">") app(ch, ch)
      else if (nx == "|") { term(k); i++ }
      else { if (nx == "&") i++; startpipe(k) }
    }
    else if (ch == ";") term(k)
    else if (ch == "&") {
      if (nx == "&") { term(k); i++ }
      else if (prev == ">" || prev == "<" || nx == ">") app(ch, ch)
      else term(k)
    }
    else if (ch == "#") {
      if (prev == "" || prev ~ /[ \t;&|(]/) break
      app(ch, ch)
    }
    else if (ch == "<" && nx == "<") {
      if (substr(line, i + 2, 1) == "<") { app("<<<", "<<<"); i += 2 }
      else {
        rest = substr(line, i + 2)
        dash = 0
        if (substr(rest, 1, 1) == "-") { dash = 1; rest = substr(rest, 2) }
        sub(/^[ \t]*/, "", rest)
        if (match(rest, /^\\?["\047]?[A-Za-z_][A-Za-z0-9_]*/)) {
          w = substr(rest, 1, RLENGTH); gsub(/[\\"\047]/, "", w)
          nh++; hdw[nh] = w; hddash[nh] = dash
        }
        app("<<", "<<"); i++
      }
    }
    else app(ch, ch)
  }
}
function eol(   k) {
  if (contflag) { contflag = 0; return }
  if (typ[d] == "c") {
    k = d
    if (!(inpipe[k] && mask[k] !~ /[^ \t]/)) term(k)
  } else if (typ[d] == "s" || typ[d] == "d") app(" ", " ")
}
function flush(   j) {
  if (cur == "") return
  if (pf) for (j = 1; j <= nv; j++) printf "%s:%d: %s under pipefail; use a here-string or read to EOF\n", cur, vl[j], vs[j]
}
function reset() {
  d = 0; typ[0] = "c"; raw[0] = ""; mask[0] = ""; inpipe[0] = 0; sline[0] = 0; bt[0] = 0
  split("", okline); split("", vl); split("", vs); split("", hdw); split("", hddash)
  nv = 0; nh = 0; hdi = 0; inhd = 0; pf = 0; contflag = 0
}
FNR == 1 { flush(); cur = FILENAME; reset() }
{
  lineno = FNR
  line = $0
  if (inhd) {
    t = line
    if (hddash[hdi]) sub(/^\t+/, "", t)
    if (t == hdw[hdi]) { hdi++; if (hdi > nh) { inhd = 0; nh = 0; hdi = 0 } }
    next
  }
  if (line ~ /#[ \t]*pipefail-ok:[ \t]*[^ \t]/) okline[lineno] = 1
  if (line !~ /^[ \t]*#/) {
    if (line ~ /(^|[;&({]|then|else|do)[ \t]*set[ \t]+[^;&|#]*pipefail/ && line !~ /set[ \t]+\+o/) pf = 1
    if (line ~ /shopt[ \t]+-[a-z]+[ \t]+pipefail/ && line !~ /shopt[ \t]+-u/) pf = 1
  }
  scan(line)
  eol()
  if (nh > 0) { inhd = 1; hdi = 1 }
}
END { flush() }
' "${files[@]}")" || {
  gate_unmeasured "awk failed while scanning the shell tree"
  gate_summary pipefail-early-exit
  exit $?
}

if [ -n "$findings" ]; then
  while IFS= read -r line; do
    gate_fail "$line"
  done <<<"$findings"
else
  gate_ok "pipefail-early-exit: no pipeline ends in an early-exit consumer under pipefail (${#files[@]} files)"
fi
gate_summary pipefail-early-exit
exit $?
