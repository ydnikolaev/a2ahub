#!/usr/bin/env bash
# check-vocabulary-carriers.sh — computed-not-listed-2026-08 P3 (spec
# 03-vocabularies-derive.md §7/§8): a seeded ceiling on Go struct fields
# typed plain `string` and named for a schema-closed vocabulary.
#
# THE UNIVERSE IS DERIVED, NOT TYPED. A "closed vocabulary" here is a JSON
# property name that carries a string `enum` in TWO OR MORE distinct
# `schemas/**/*.schema.json` files — the same threshold spec 03's own
# measured-facts table uses ("16 property names carry an enum in more than
# one schema file"). This gate re-derives that set from the embedded corpus
# on every run; it never hand-lists a name. Adding an enum member to an
# EXISTING closed-vocabulary property changes nothing about the universe's
# membership (the property was already in it); adding a NEW property that
# now appears, with an enum, in a second schema file pulls that name into
# the universe on its own, with no edit here (§8 row 1).
#
# `kind` IS NOT EXCLUDED BY NAME (§8 row 2). Membership in the universe is
# decided by NAME alone — `kind` carries a string enum in 9 distinct schema
# files (>= 2), the same test every other property name is put to, no
# special case. Grouping by property name PLUS the exact enum value-set
# (frozenset(enum)) is a SEPARATE, finer fact this gate also derives — used
# only for the seeded kind-group count (§8 row 11) and for a reader who
# wants to know how many genuinely different `kind` vocabularies those 9
# files carry, never for deciding whether `kind` is in scope at all. Under
# that grouping the 9 occurrences split into 5 DISTINCT groups — re-derived
# at HEAD 2026-08-30, matching computed-not-listed-2026-08 P3 spec's own
# measured-facts table exactly:
#
#   {agent, human}                                    4 files (envelope/v1
#     base, envelope/v2 base, event/v1, event/v2)
#   {bug, docs, feature, friction, protocol}           2 files (feedback/v1
#     backlog, feedback/v1 feedback)
#   {break, feat, fix, known-issue, policy, schema}    1 file  (release-notes/v1)
#   {code, config, contract, data, doc}                1 file  (envelope/v1
#     handoff)
#   {external, human, system, timer, tool}             1 file  (envelope/v2
#     announcement)
#
# The three 1-file groups above would NOT, on their own, clear the
# "more than one schema file" bar this gate's universe test uses — but that
# is moot for `kind` specifically, since the property NAME already clears
# it at 9 files regardless of how those files group. A property whose
# every occurrence sat in singleton groups (no name-level file carried a
# repeated value-set) would still be excluded correctly by the name-level
# test alone; `kind` simply never exercises that edge, because {agent,
# human} alone already supplies 4 of its 9 files. `--teeth`'s
# kind-group-count-drift case (§8 row 11) proves the GROUP count reds, and
# names the new schema, when a 6th distinct value-set appears.
#
# WHAT THIS GATE DOES NOT COVER, STATED SO A GREEN RUN IS NOT OVER-READ (§8
# row 8, US-5): this is the schema→Go half only. It says nothing about
# schema↔schema agreement — whether two schemas that are SUPPOSED to carry
# the same vocabulary actually still do. That half is
# internal/schema/reasoncode_mirror_test.go
# (TestReasonCodeMirrorMatchesTheEventSchema), which reaches event/v1,
# event/v2, and envelope/v2's response.blocked_by for `reason_code`
# specifically — the one property this corpus copies across schema
# families rather than deriving structurally. A green run here proves every
# CARRIER of a closed vocabulary this scan can see is typed or on the seeded
# ceiling; it proves nothing about whether the schemas those carriers derive
# from still agree with each other.
#
# THE 296-SITE / 102-FILE SEED IS MEASURED AGAINST THIS TREE, NOT COPIED
# FROM A SPEC. Spec 03 quotes 155 plain-string carriers over six
# hand-picked vocabulary names (role, status, kind, class, severity, mode),
# measured by a method the spec itself says is not reconstructible from the
# tree. This gate's own scan, run against HEAD 2026-08-30 with the 16-name
# derived universe above, found 296 sites across 102 files — a different
# number by a different, but fully reproducible, method. Re-run
# `--write` any time the ceiling needs re-seeding after a real migration;
# never edit the ceiling file by hand to make a red one go away.
#
# THE SCAN IS SYNTACTIC, NOT SEMANTIC (a stated limitation, not a bug): a
# struct field is a "carrier" if its Go name is the PascalCase form of a
# derived vocabulary property name (`reason_code` -> `ReasonCode`) AND its
# type is the bare identifier `string`. It cannot know whether a given
# `Status string` field actually MEANS the schema's status vocabulary or
# some unrelated status this repo happens to spell the same way — the same
# false-positive risk check-refusal-ratchet.sh's own header names for its
# `Stderr`-anchored scan. That is why this is a ceiling, not an assertion:
# growth reds and must be looked at, not blindly typed away.
#
# Usage: bash scripts/check-vocabulary-carriers.sh            # verify (CI/commit gate)
#        bash scripts/check-vocabulary-carriers.sh --write    # regenerate the ceiling from the current tree
#        bash scripts/check-vocabulary-carriers.sh --teeth    # this gate's own self-test

# lane-reads-opaque: the gate-lib source is written as
# "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh" — the repo-wide idiom
# every gate here uses, and a literal the extractor cannot resolve through
# the subshell.
#
# lane-reads-opaque: this gate writes a complete Go analyzer into
# "$ANALYZER_DIR/vocabcarriers.go" (a mktemp -d, so the path exists only at
# run time) and `go run`s it against $ROOT. That path is scratch, never a
# repo path: nothing under it can change this gate's verdict on a real
# tree, so it is deliberately not a lane-input — the same shape
# check_contract_carried_set.sh and check-refusal-ratchet.sh already
# declare for their own generated analyzers. The analyzer itself imports
# only the standard library (encoding/json, go/ast, go/parser, go/token,
# io/fs, os, path/filepath, sort, strings) and does its own AST parse of
# each candidate file — it never `go build`s or `go vet`s a real package,
# so it carries none of the sibling-concurrency hazard a package-compiling
# gate would (this wave's own plan names P3/P5/P7 as concurrently editing
# different files inside internal/validate/).
#
# lane-inputs:
#   schemas/**/*.schema.json
#   internal/**/*.go
#   cmd/**/*.go
#   !internal/**/*_test.go
#   scripts/lib/vocabulary-carrier-ceiling.txt
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

DEFAULT_ROOT="$GATE_ROOT"
DEFAULT_CEILING_FILE="$GATE_ROOT/scripts/lib/vocabulary-carrier-ceiling.txt"

# write_analyzer <dir>: writes the standalone Go analyzer (stdlib-only, no
# module resolution needed — see the lane-reads-opaque note above) into
# <dir>/vocabcarriers.go.
write_analyzer() {
  local dir="$1"
  cat >"$dir/vocabcarriers.go" <<'GOEOF'
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type kindGroup struct {
	Members []string `json:"members"`
	Files   []string `json:"files"`
}

type carrierSite struct {
	Path  string `json:"path"`
	Field string `json:"field"`
	Line  int    `json:"line"`
	Vocab string `json:"vocab"`
}

type vocabEntry struct {
	Name   string   `json:"name"`
	GoName string   `json:"go_name"`
	Files  []string `json:"files"`
}

type output struct {
	Vocabulary []vocabEntry  `json:"vocabulary"`
	KindGroups []kindGroup   `json:"kind_groups"`
	Carriers   []carrierSite `json:"carriers"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: vocabcarriers <root>")
		os.Exit(2)
	}
	root := os.Args[1]

	filesPerName, kindGroups, err := deriveVocabulary(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "derive vocabulary:", err)
		os.Exit(2)
	}

	vocab := []vocabEntry{}
	for name, files := range filesPerName {
		if len(files) < 2 {
			continue
		}
		var sortedFiles []string
		for f := range files {
			sortedFiles = append(sortedFiles, f)
		}
		sort.Strings(sortedFiles)
		vocab = append(vocab, vocabEntry{Name: name, GoName: snakeToPascal(name), Files: sortedFiles})
	}
	sort.Slice(vocab, func(i, j int) bool { return vocab[i].Name < vocab[j].Name })

	goNameToVocab := map[string]string{}
	for _, v := range vocab {
		goNameToVocab[v.GoName] = v.Name
	}

	carriers, err := scanCarriers(root, goNameToVocab)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan carriers:", err)
		os.Exit(2)
	}

	out := output{Vocabulary: vocab, KindGroups: kindGroups, Carriers: carriers}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(2)
	}
}

func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

// deriveVocabulary walks root/schemas/**/*.schema.json, finding every JSON
// object key K whose value is itself an object carrying a string-only
// "enum" array, and returns:
//   - filesPerName: K -> the set of distinct schema files (repo-relative,
//     forward-slashed) in which K carries such an enum at least once,
//     deduped per file (an object appearing twice in one file under the
//     same key counts once for that file).
//   - kindGroups: K=="kind" specifically, grouped by the EXACT enum
//     value-set (frozenset) rather than by name alone — spec's own
//     "vocabulary identity is property name PLUS exact enum value-set"
//     rule, needed only for kind's own reporting/teeth requirement, not
//     for universe membership in general.
func deriveVocabulary(root string) (map[string]map[string]bool, []kindGroup, error) {
	schemaRoot := filepath.Join(root, "schemas")
	filesPerName := map[string]map[string]bool{}
	kindGroupFiles := map[string]map[string]bool{}
	kindGroupMembers := map[string][]string{}

	if _, err := os.Stat(schemaRoot); os.IsNotExist(err) {
		return filesPerName, []kindGroup{}, nil
	}

	err := filepath.WalkDir(schemaRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".schema.json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		seenInFile := map[string]bool{}
		seenKindGroupInFile := map[string]bool{}
		var walk func(node any)
		walk = func(node any) {
			switch n := node.(type) {
			case map[string]any:
				for k, v := range n {
					if child, ok := v.(map[string]any); ok {
						if enumVals, ok := stringEnum(child); ok {
							if !seenInFile[k] {
								seenInFile[k] = true
								if filesPerName[k] == nil {
									filesPerName[k] = map[string]bool{}
								}
								filesPerName[k][rel] = true
							}
							if k == "kind" {
								members := append([]string(nil), enumVals...)
								sort.Strings(members)
								key := strings.Join(members, "\x00")
								if !seenKindGroupInFile[key] {
									seenKindGroupInFile[key] = true
									if kindGroupFiles[key] == nil {
										kindGroupFiles[key] = map[string]bool{}
									}
									kindGroupFiles[key][rel] = true
									kindGroupMembers[key] = members
								}
							}
						}
					}
					walk(v)
				}
			case []any:
				for _, v := range n {
					walk(v)
				}
			}
		}
		walk(doc)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	groups := []kindGroup{}
	for key, members := range kindGroupMembers {
		var files []string
		for f := range kindGroupFiles[key] {
			files = append(files, f)
		}
		sort.Strings(files)
		groups = append(groups, kindGroup{Members: members, Files: files})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Files) != len(groups[j].Files) {
			return len(groups[i].Files) > len(groups[j].Files)
		}
		return strings.Join(groups[i].Members, ",") < strings.Join(groups[j].Members, ",")
	})

	return filesPerName, groups, nil
}

func stringEnum(m map[string]any) ([]string, bool) {
	raw, ok := m["enum"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// scanCarriers walks root/internal and root/cmd for non-test *.go files and
// finds every plain `string`-typed struct field whose Go name matches one
// of the derived vocabulary's PascalCase names.
func scanCarriers(root string, goNameToVocab map[string]string) ([]carrierSite, error) {
	out := []carrierSite{}
	roots := []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")}
	fset := token.NewFileSet()
	for _, r := range roots {
		if _, err := os.Stat(r); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			ast.Inspect(f, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, field := range st.Fields.List {
					ident, ok := field.Type.(*ast.Ident)
					if !ok || ident.Name != "string" {
						continue
					}
					for _, name := range field.Names {
						vocabName, matched := goNameToVocab[name.Name]
						if !matched {
							continue
						}
						out = append(out, carrierSite{
							Path: rel, Field: name.Name,
							Line: fset.Position(name.Pos()).Line, Vocab: vocabName,
						})
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}
GOEOF
}

# run_analyzer <root>: writes and runs the Go analyzer against <root>,
# printing its JSON to stdout. Caller captures it; this function's own
# scratch dir is cleaned up before it returns.
run_analyzer() {
  local root="$1" work
  work="$(mktemp -d)" || { gate_unmeasured "vocabulary-carriers: could not create a scratch dir for the analyzer"; return 1; }
  write_analyzer "$work"
  if ! (cd "$work" && go run vocabcarriers.go "$root"); then
    rm -rf -- "$work"
    return 1
  fi
  rm -rf -- "$work"
}

# json_field_for <ceiling-file>: extracts the "kind-groups: N" line's N, or
# 0 if the file names none — an unlisted count is implicitly 0, the same
# "unlisted is 0" convention check-refusal-ratchet.sh's budget_for uses.
kind_budget_for() {
  local ceiling_file="$1"
  awk '
    $0 ~ /^#/ { next }
    $1 == "kind-groups:" { print $2; found=1 }
    END { if (!found) print 0 }
  ' "$ceiling_file" 2>/dev/null || echo 0
}

# budget_for <ceiling-file> <path>: the stored ceiling for a repo-relative
# Go file path, or 0 when the file names no such line.
budget_for() {
  local ceiling_file="$1" path="$2"
  awk -v p="$path" '
    $0 ~ /^#/ { next }
    $1 == "kind-groups:" { next }
    NF >= 2 && $2 == p { print $1; found=1 }
    END { if (!found) print 0 }
  ' "$ceiling_file" 2>/dev/null || echo 0
}

# verify_against_ceiling <root> <ceiling-file>: runs the analyzer, checks
# the kind-group count and every carrying file's count against the ceiling
# file, gate_fail's every violation, and always prints one gate_ok summary
# line — including the schema→Go / schema↔schema boundary statement §8 row
# 8 requires.
verify_against_ceiling() {
  local root="$1" ceiling_file="$2" json work
  work="$(mktemp -d)" || { gate_unmeasured "vocabulary-carriers: could not create a scratch dir"; return 1; }
  json="$work/out.json"
  if ! run_analyzer "$root" >"$json"; then
    gate_unmeasured "vocabulary-carriers: the analyzer failed to run against $root — see stderr above"
    rm -rf -- "$work"
    return 1
  fi

  local kind_count kind_ceiling
  kind_count="$(jq '.kind_groups | length' "$json")"
  kind_ceiling="$(kind_budget_for "$ceiling_file")"
  if [ "$kind_count" -gt "$kind_ceiling" ]; then
    local new_group new_files
    new_group="$(jq -r --argjson n "$kind_ceiling" '.kind_groups[$n].members | join(",")' "$json")"
    new_files="$(jq -r --argjson n "$kind_ceiling" '.kind_groups[$n].files | join(", ")' "$json")"
    gate_fail "vocabulary-carriers: kind's distinct enum value-set count grew from $kind_ceiling to $kind_count — a new group {${new_group}} appeared in: ${new_files}. Re-derive rather than trusting the stale 5-group split in this script's own header; if the new group is legitimate, re-run --write and update the header's prose to match."
  fi

  local total=0 nfiles=0 path count ceiling
  while IFS=$'\t' read -r path count; do
    [ -n "${path:-}" ] || continue
    total=$((total + count))
    nfiles=$((nfiles + 1))
    ceiling="$(budget_for "$ceiling_file" "$path")"
    if [ "$count" -gt "$ceiling" ]; then
      local vocab_names schema_sources
      vocab_names="$(jq -r --arg p "$path" '[.carriers[] | select(.path == $p) | .vocab] | unique | join(", ")' "$json")"
      schema_sources="$(jq -r --arg p "$path" '
        ([.carriers[] | select(.path == $p) | .vocab] | unique) as $names
        | [.vocabulary[] | select(.name as $n | $names | index($n)) | "\(.name): \(.files | join(", "))"]
        | join(" | ")' "$json")"
      gate_fail "vocabulary-carriers: $path carries $count plain-string vocabulary carrier(s), ceiling is $ceiling — new or grown carrier(s) among {${vocab_names}}. Each vocabulary's legal values are declared in: ${schema_sources}. Type the new field against the closed vocabulary (or its Go home) rather than leaving it a bare string; the per-file ceiling may only shrink, never grow."
    fi
  done < <(jq -r '
    [.carriers[].path] | group_by(.) | map([.[0], length] | @tsv) | .[]' "$json")

  rm -rf -- "$work"
  gate_ok "vocabulary-carriers: $total plain-string vocabulary carrier site(s) across $nfiles file(s), kind split into $kind_count group(s) — all at or under scripts/lib/vocabulary-carrier-ceiling.txt's ceiling. This gate proves schema->Go agreement only; it says nothing about schema<->schema agreement (whether two schemas SUPPOSED to share a vocabulary still do) — that half is internal/schema/reasoncode_mirror_test.go."
}

# write_ceiling <root> <out>: regenerates the ceiling file from <root>'s
# CURRENT tree. Never invoked automatically by the verify path.
write_ceiling() {
  local root="$1" out="$2" json work
  work="$(mktemp -d)" || return 1
  json="$work/out.json"
  if ! run_analyzer "$root" >"$json"; then
    rm -rf -- "$work"
    return 1
  fi
  local kind_count total nfiles
  kind_count="$(jq '.kind_groups | length' "$json")"
  total="$(jq '.carriers | length' "$json")"
  nfiles="$(jq -r '[.carriers[].path] | unique | length' "$json")"

  {
    cat <<EOF
# scripts/lib/vocabulary-carrier-ceiling.txt — the CEILING on plain-\`string\`
# struct fields named for a closed vocabulary derived from
# schemas/**/*.schema.json (computed-not-listed-2026-08 P3, spec 03). See
# scripts/check-vocabulary-carriers.sh's own header for the full universe
# derivation, the kind grouping rule, and the schema<->schema half this
# does NOT cover.
#
# Two kinds of line:
#   kind-groups: N       one line, the CURRENT count of distinct \`kind\`
#                         enum value-sets (grouped by exact frozenset(enum))
#                         across the corpus. Growing past N reds and names
#                         the new schema (§8 row 11) — this line may only
#                         rise after confirming the new group is a real,
#                         intentional addition, never edited to silence a
#                         red.
#   <count> <path>       one line per production Go file (repo-relative)
#                         carrying at least one plain-string field named for
#                         a closed vocabulary. A file's line may only be
#                         LOWERED (a carrier typed) or removed (its count
#                         reaches 0); an unlisted file's budget is
#                         implicitly 0.
#
# Regenerate with:
#   bash scripts/check-vocabulary-carriers.sh --write
# then review the diff before committing.
#
# THESE NUMBERS ARE MEASURED AGAINST THIS TREE, NOT COPIED FROM A SPEC —
# see check-vocabulary-carriers.sh's own header for why this run's total
# ($total sites across $nfiles files, kind split $kind_count ways) differs
# from spec 03's own quoted 155/9-vs-5 figures, which used a different
# (and, for 155, not fully reconstructible) method.
EOF
    echo "kind-groups: $kind_count"
    jq -r '
      [.carriers[].path] | group_by(.) | map([.[0], length])
      | sort_by(.[0])
      | .[] | "\(.[1]) \(.[0])"' "$json"
  } >"$out"
  rm -rf -- "$work"
}

# ── --teeth ───────────────────────────────────────────────────────────────

# fixture_root <dir>: lays down a minimal schemas/ + internal/ tree under
# <dir> so the analyzer can run against it in isolation, mirroring
# check-refusal-ratchet.sh's own scratch-fixture --teeth shape. Every
# fixture below builds on this base and mutates one piece of it.
fixture_base() {
  local dir="$1"
  mkdir -p "$dir/schemas/one" "$dir/schemas/two" "$dir/internal/widgetpkg"
  cat >"$dir/schemas/one/a.schema.json" <<'EOF'
{"properties": {"status": {"type": "string", "enum": ["active", "left"]}}}
EOF
  cat >"$dir/schemas/two/b.schema.json" <<'EOF'
{"properties": {"status": {"type": "string", "enum": ["active", "left"]}}}
EOF
}

teeth_run() { # $1 = root, $2 = ceiling file; prints gate_summary's stdout/stderr, returns its exit code
  (
    _GATE_ERRORS=0
    _GATE_WARNINGS=0
    _GATE_UNMEASURED=0
    verify_against_ceiling "$1" "$2"
    gate_summary "check-vocabulary-carriers-teeth"
  ) 2>&1
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = root, $4 = ceiling file
  local label="$1" verdict="$2" root="$3" ceiling="$4" out rc
  set +e
  out="$(teeth_run "$root" "$ceiling")"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ]; then
      echo "check-vocabulary-carriers --teeth: FALSE GREEN — $label did not red:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-vocabulary-carriers --teeth: $label reds"
  else
    if [ "$rc" -ne 0 ]; then
      echo "check-vocabulary-carriers --teeth: FALSE RED — $label should green:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-vocabulary-carriers --teeth: $label greens"
  fi
  echo "$out"
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "$work"' RETURN

  # AC-1: a property with an enum in only ONE schema file is NOT yet in the
  # universe; adding a second file carrying the same enum pulls it in, with
  # no gate edit — proven by widening the fixture, not by editing this
  # script.
  local d1="$work/ac1"
  mkdir -p "$d1/schemas/one" "$d1/internal/widgetpkg"
  cat >"$d1/schemas/one/a.schema.json" <<'EOF'
{"properties": {"newvocab": {"type": "string", "enum": ["a", "b"]}}}
EOF
  cat >"$d1/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg

type Widget struct {
	Newvocab string
}
EOF
  : >"$d1/ceiling.txt"
  local json1
  json1="$(mktemp)"
  run_analyzer "$d1" >"$json1"
  local before_vocab_count
  before_vocab_count="$(jq '.vocabulary | length' "$json1")"
  if [ "$before_vocab_count" != "0" ]; then
    echo "check-vocabulary-carriers --teeth: FALSE — AC1 fixture's single-file enum was already counted as a closed vocabulary ($before_vocab_count)" >&2
    exit 1
  fi
  mkdir -p "$d1/schemas/two"
  cat >"$d1/schemas/two/b.schema.json" <<'EOF'
{"properties": {"newvocab": {"type": "string", "enum": ["a", "b"]}}}
EOF
  run_analyzer "$d1" >"$json1"
  local after_vocab_count after_carrier_count
  after_vocab_count="$(jq '.vocabulary | length' "$json1")"
  after_carrier_count="$(jq '.carriers | length' "$json1")"
  if [ "$after_vocab_count" != "1" ] || [ "$after_carrier_count" != "1" ]; then
    echo "check-vocabulary-carriers --teeth: FALSE — AC1: widening to a second schema file did not pull newvocab into the universe (vocab=$after_vocab_count carriers=$after_carrier_count)" >&2
    exit 1
  fi
  echo "check-vocabulary-carriers --teeth: AC1 (widening the schemas changes the derived universe with no gate edit) passes"
  rm -f "$json1"

  # AC-4/AC-9: a file AT budget that GAINS a new untyped carrier REDS, and
  # the refusal names the schema file(s) the vocabulary was derived from.
  local d2="$work/ac4"
  fixture_base "$d2"
  mkdir -p "$d2/internal/widgetpkg"
  cat >"$d2/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg

type Widget struct {
	Status string
}
EOF
  printf '1 internal/widgetpkg/widget.go\n' >"$d2/ceiling.txt"
  teeth_expect "AC4a: exactly at budget greens" green "$d2" "$d2/ceiling.txt" || exit 1
  cat >"$d2/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg

type Widget struct {
	Status  string
	Newness string
}
EOF
  mkdir -p "$d2/schemas/three"
  cat >"$d2/schemas/three/c.schema.json" <<'EOF'
{"properties": {"newness": {"type": "string", "enum": ["fresh", "stale"]}}}
EOF
  mkdir -p "$d2/schemas/four"
  cat >"$d2/schemas/four/d.schema.json" <<'EOF'
{"properties": {"newness": {"type": "string", "enum": ["fresh", "stale"]}}}
EOF
  local out_ac4b
  out_ac4b="$(teeth_expect "AC4b: gaining a new untyped carrier past budget reds" red "$d2" "$d2/ceiling.txt")" || exit 1
  case "$out_ac4b" in
  *schemas/three/c.schema.json*schemas/four/d.schema.json* | *schemas/four/d.schema.json*schemas/three/c.schema.json*) ;;
  *)
    echo "check-vocabulary-carriers --teeth: FALSE — AC9: refusal did not name both source schema files:" >&2
    echo "$out_ac4b" >&2
    exit 1
    ;;
  esac
  echo "check-vocabulary-carriers --teeth: AC9 (refusal names the schema file(s) the vocabulary was derived from) passes"

  # AC-5: the seeded ceiling does not red as-is, and removing a carrier
  # lets the ceiling shrink and still green.
  local d3="$work/ac5"
  fixture_base "$d3"
  mkdir -p "$d3/internal/widgetpkg"
  cat >"$d3/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg

type Widget struct {
	Status string
}
EOF
  printf '1 internal/widgetpkg/widget.go\n' >"$d3/ceiling.txt"
  teeth_expect "AC5a: seeded ceiling matching the current tree greens" green "$d3" "$d3/ceiling.txt" || exit 1
  cat >"$d3/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg

import "github.com/ydnikolaev/a2ahub/internal/fold"

type Widget struct {
	Status fold.MembershipStatus
}
EOF
  printf '0 internal/widgetpkg/widget.go\n' >"$d3/ceiling.txt"
  teeth_expect "AC5b: typing the carrier and lowering the ceiling greens" green "$d3" "$d3/ceiling.txt" || exit 1

  # AC-11: kind-group-count-drift — a 6th distinct kind value-set reds the
  # seeded count and names the new schema.
  local d4="$work/ac11"
  mkdir -p "$d4/schemas/k1" "$d4/schemas/k2" "$d4/schemas/k3" "$d4/schemas/k4" "$d4/schemas/k5" "$d4/internal/widgetpkg"
  cat >"$d4/schemas/k1/a.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g1a", "g1b"]}}}
EOF
  cat >"$d4/schemas/k2/b.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g1a", "g1b"]}}}
EOF
  cat >"$d4/schemas/k3/c.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g2a", "g2b"]}}}
EOF
  cat >"$d4/schemas/k4/d.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g2a", "g2b"]}}}
EOF
  cat >"$d4/schemas/k5/e.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g3a", "g3b"]}}}
EOF
  cat >"$d4/internal/widgetpkg/widget.go" <<'EOF'
package widgetpkg
EOF
  printf 'kind-groups: 3\n' >"$d4/ceiling.txt"
  teeth_expect "AC11a: kind-groups exactly at budget (3) greens" green "$d4" "$d4/ceiling.txt" || exit 1
  mkdir -p "$d4/schemas/k6"
  cat >"$d4/schemas/k6/f.schema.json" <<'EOF'
{"properties": {"kind": {"type": "string", "enum": ["g4a", "g4b"]}}}
EOF
  local out_ac11b
  out_ac11b="$(teeth_expect "AC11b: a 4th distinct kind value-set past budget reds and names the schema" red "$d4" "$d4/ceiling.txt")" || exit 1
  case "$out_ac11b" in
  *schemas/k6/f.schema.json*) ;;
  *)
    echo "check-vocabulary-carriers --teeth: FALSE — AC11: kind-group-count-drift refusal did not name the new schema file:" >&2
    echo "$out_ac11b" >&2
    exit 1
    ;;
  esac
  echo "check-vocabulary-carriers --teeth: AC11 (kind-group-count-drift reds and names the new schema) passes"

  # AC-8: the green line states the schema<->schema half this gate does not
  # cover.
  local out_ac8
  out_ac8="$(teeth_expect "AC8: green line states the uncovered half" green "$d3" "$d3/ceiling.txt")" || exit 1
  case "$out_ac8" in
  *schema*schema*reasoncode_mirror_test.go*) ;;
  *)
    echo "check-vocabulary-carriers --teeth: FALSE — AC8: green line did not name the schema<->schema half it does not cover:" >&2
    echo "$out_ac8" >&2
    exit 1
    ;;
  esac
  echo "check-vocabulary-carriers --teeth: AC8 (green line states the uncovered half) passes"

  echo "check-vocabulary-carriers --teeth: PASS — AC1 (universe derives from schemas), AC4 (new untyped carrier reds), AC5 (seeded ceiling greens, shrinks on typing), AC8 (green line states the uncovered half), AC9 (refusal names the source schema), AC11 (kind-group-count-drift)"
}

# ── entry point ──────────────────────────────────────────────────────────

case "${1:-}" in
--teeth)
  run_teeth
  exit $?
  ;;
--write)
  write_ceiling "$DEFAULT_ROOT" "$DEFAULT_CEILING_FILE"
  echo "wrote $DEFAULT_CEILING_FILE"
  ;;
"")
  verify_against_ceiling "$DEFAULT_ROOT" "$DEFAULT_CEILING_FILE"
  gate_summary "vocabulary-carriers"
  exit $?
  ;;
*)
  echo "usage: $0 [--teeth|--write]" >&2
  exit 2
  ;;
esac
