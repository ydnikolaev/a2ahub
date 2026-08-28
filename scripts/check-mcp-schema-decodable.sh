#!/usr/bin/env bash
# check-mcp-schema-decodable.sh — P12 (answers-that-hold-2026-08): every
# property a published MCP tool schema DECLARES must correspond to a field
# the tool's input struct DECODES.
#
# `no-silent-yes-2026-08` P4 closed the OPPOSITE direction: 28 of 28 decode
# sites now call decodeStrict, which refuses an unknown top-level JSON key
# (DisallowUnknownFields). That makes SENDING an undeclared field loud. It
# does not stop the SCHEMA from advertising a field nothing decodes — an
# agent reads the published schema, sends exactly what it says, and is
# refused for obeying it. Nothing compared the two hand-written halves until
# this gate: internal/mcp's schemas are hand-assembled `map[string]any`
# literals (tools.go's propSpec/rawSchema/groupedSchema), never reflected
# from the input structs, so the two can disagree by construction.
#
# BOTH SIDES ARE DERIVED, NEITHER IS A ROSTER (spec 12 AC-3):
#   - published properties come from parsing each tool's OWN InputSchema
#     builder call (rawSchema/groupedSchema literal, or a zero-arg helper
#     function built the same way — dataToolSchema/notifyToolSchema/
#     workToolSchema) via go/ast — never a hand-listed tool->property map;
#   - decodable struct fields come from walking the call graph reachable
#     from each tool's OWN Handler constructor, collecting every call whose
#     name is `decodeStrict` itself or a THIN WRAPPER over it (a func whose
#     entire body is `return decodeStrict(...)` — the decodeWorkInput/
#     decodeDataInput shape, detected structurally, never by name), and
#     reading the json tags of the struct type each such call decodes into.
#
# SCOPE: top-level schema properties only. A2A_work's nested `actor`/
# `waiting_on` object schemas were hand-verified clean against
# WorkActorInput/WorkWaitingInput at the time this gate was written
# (2026-08-28) but are not walked by this analyzer — see the phase report.
#
# lane-inputs:
#   internal/mcp/**/*.go
#   !internal/mcp/**/*_test.go
#   scripts/check-mcp-schema-decodable.sh
# lane-reads-opaque: this gate writes a complete Go analyzer into
#   "$ANALYZER_DIR/main.go" (a mktemp -d, so the path exists only at run
#   time) and `go run`s it against $PKG_DIR (internal/mcp, or a --teeth
#   scratch fixture). The classifier cannot follow that call, so what the
#   analyzer reads is declared above from having READ the heredoc body: it
#   walks every non-test *.go file directly under the target directory —
#   internal/mcp/**/*.go in the real run, a mktemp -d tree under --teeth.
#   `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"` self-locates
#   the shared helper through command substitution, the same construct
#   check-unmeasured-reach.sh and check-card-content.sh declare.
# Deliberately NOT `set -e`: run_teeth captures a deliberately-failing
# subshell's own exit code (`out="$(... ; gate_summary ...)"; rc=$?`), the
# same shape check-card-content.sh's teeth_expect uses and for the same
# reason — under `set -e` the failing command substitution would abort the
# script before `rc=$?` ever ran. run_check checks `go run`'s own exit
# status explicitly instead of relying on -e to propagate it.
set -uo pipefail

# shellcheck source=lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

ROOT="${MCP_SCHEMA_DECODABLE_ROOT:-$GATE_ROOT}"
PKG_DIR_DEFAULT="$ROOT/internal/mcp"

ANALYZER_DIR="$(mktemp -d)"
trap 'rm -rf "$ANALYZER_DIR"' EXIT

cat >"$ANALYZER_DIR/main.go" <<'GO'
package main

// mcp-schema-decodable — see scripts/check-mcp-schema-decodable.sh's own
// header for the invariant. This program takes ONE argument (a directory
// holding a `package mcp`-shaped tree of .go files — the real
// internal/mcp, or a --teeth scratch fixture) and prints, one per line,
// tab-separated:
//
//	VIOLATION	<tool>	<property>	<file>:<line>
//	UNMEASURED	<reason>
//	OK	<tools-checked>	<properties-checked>	<decode-structs-reached>
//
// The shell wrapper owns FAIL/WARN/UNMEASURED presentation (gate-lib.sh) —
// this program only derives and reports; it always exits 0 so its stdout is
// the one channel the wrapper reads. A genuine bug in this program (a parse
// panic, a malformed heredoc) is the one case allowed to exit non-zero and
// crash the wrapper loudly under `set -e` — that is a defect in the GATE,
// not a verdict about the corpus, and must never be swallowed as
// "unmeasured".
import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

var fset = token.NewFileSet()

type propRef struct {
	name string
	pos  token.Pos
}

type toolFinding struct {
	name        string
	nameKnown   bool
	pos         token.Pos
	schemaExpr  ast.Expr
	handlerExpr ast.Expr
	enclosing   *ast.FuncDecl
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mcp-schema-decodable-analyzer <package-directory>")
		os.Exit(2)
	}
	root := os.Args[1]

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Printf("UNMEASURED\tcould not read %s: %v\t\t\n", root, err)
		return
	}
	var fileNames []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	if len(fileNames) == 0 {
		fmt.Printf("UNMEASURED\tno non-test .go files under %s\t\t\n", root)
		return
	}

	var files []*ast.File
	for _, name := range fileNames {
		f, err := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.AllErrors)
		if err != nil {
			fmt.Printf("UNMEASURED\tparse %s: %v\t\t\n", name, err)
			return
		}
		files = append(files, f)
	}

	funcTable := map[string]*ast.FuncDecl{}
	typeTable := map[string]*ast.StructType{}
	constStrings := map[string]string{}

	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcTable[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if st, ok := s.Type.(*ast.StructType); ok {
							typeTable[s.Name.Name] = st
						}
					case *ast.ValueSpec:
						if d.Tok == token.CONST {
							for i, name := range s.Names {
								if i >= len(s.Values) {
									continue
								}
								if lit, ok := s.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
									if v, err := strconv.Unquote(lit.Value); err == nil {
										constStrings[name.Name] = v
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// A decode-anchor is `decodeStrict` itself, or any func whose ENTIRE
	// body is `return decodeStrict(...)` — decodeWorkInput/decodeDataInput's
	// own shape (tools_work.go/tools_data.go), detected structurally so
	// this analyzer never hardcodes either name.
	wrapperNames := map[string]bool{}
	for name, fn := range funcTable {
		if fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		if call, ok := ret.Results[0].(*ast.CallExpr); ok && identName(call.Fun) == "decodeStrict" {
			wrapperNames[name] = true
		}
	}

	var findings []toolFinding
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				id, ok := lit.Type.(*ast.Ident)
				if !ok || id.Name != "ToolSpec" {
					return true
				}
				var nameExpr, schemaExpr, handlerExpr ast.Expr
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch key.Name {
					case "Name":
						nameExpr = kv.Value
					case "InputSchema":
						schemaExpr = kv.Value
					case "Handler":
						handlerExpr = kv.Value
					}
				}
				// A ToolSpec{} zero-value (an error-path return) carries
				// neither field — not this gate's subject.
				if schemaExpr == nil || handlerExpr == nil {
					return true
				}
				name, nameKnown := "<unnamed>", false
				if nameExpr != nil {
					if v, ok := stringLiteral(nameExpr); ok {
						name, nameKnown = v, true
					} else if nid, ok := nameExpr.(*ast.Ident); ok {
						if v, ok := constStrings[nid.Name]; ok {
							name, nameKnown = v, true
						}
					}
				}
				findings = append(findings, toolFinding{
					name: name, nameKnown: nameKnown, pos: lit.Pos(),
					schemaExpr: schemaExpr, handlerExpr: handlerExpr, enclosing: fn,
				})
				return true
			})
		}
	}

	if len(findings) == 0 {
		fmt.Printf("UNMEASURED\tno ToolSpec{Name:...,InputSchema:...,Handler:...} literal found under %s\t\t\n", root)
		return
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].pos < findings[j].pos })

	toolsChecked, propsTotal, structsTotal := 0, 0, 0

	for _, tf := range findings {
		label := tf.name
		if !tf.nameKnown {
			label = fmt.Sprintf("<unresolved-name at %s>", position(root, tf.pos))
		}

		props, propsOK := extractSchemaProps(tf.schemaExpr, funcTable, 0)
		if !propsOK {
			fmt.Printf("UNMEASURED\tcould not derive published properties for tool %s (%s)\t\t\n", label, position(root, tf.pos))
			continue
		}

		startFunc, handlerOK := resolveHandlerStart(tf.handlerExpr, tf.enclosing)
		if !handlerOK {
			fmt.Printf("UNMEASURED\tcould not resolve the handler reachable from tool %s (%s)\t\t\n", label, position(root, tf.pos))
			continue
		}

		structTypes, unresolvedSite := bfsDecodeStructs(startFunc, funcTable, wrapperNames)
		if unresolvedSite {
			fmt.Printf("UNMEASURED\ttool %s reaches a decode call whose input type could not be resolved\t\t\n", label)
			continue
		}
		if len(structTypes) == 0 {
			fmt.Printf("UNMEASURED\ttool %s's handler reaches no decodeStrict call\t\t\n", label)
			continue
		}

		decodedFields := map[string]bool{}
		unresolvedType := false
		for typeName := range structTypes {
			st, ok := typeTable[typeName]
			if !ok {
				fmt.Printf("UNMEASURED\ttool %s decodes into unresolved type %s\t\t\n", label, typeName)
				unresolvedType = true
				continue
			}
			for _, jsonName := range fieldsOf(st) {
				decodedFields[jsonName] = true
			}
		}
		if unresolvedType {
			continue
		}

		toolsChecked++
		propsTotal += len(props)
		structsTotal += len(structTypes)

		for _, p := range props {
			if !decodedFields[p.name] {
				fmt.Printf("VIOLATION\t%s\t%s\t%s\n", label, p.name, position(root, p.pos))
			}
		}
	}

	fmt.Printf("OK\t%d\t%d\t%d\n", toolsChecked, propsTotal, structsTotal)
}

func identName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// mapLiteralKeys reads the TOP-LEVEL string keys of a map composite literal
// — `map[string]propSpec{...}` or `map[string]any{...}` alike; the value
// side (a nested object schema, for a property like a2a_work's `actor`) is
// deliberately not descended into — see this gate's header "SCOPE" note.
func mapLiteralKeys(expr ast.Expr) ([]propRef, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	var out []propRef
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := stringLiteral(kv.Key)
		if !ok {
			return nil, false
		}
		out = append(out, propRef{name: key, pos: kv.Pos()})
	}
	return out, true
}

// extractSchemaProps derives a tool's published property set from its
// InputSchema field expression, never from a hand-listed tool->property
// map. Two shapes are recognised BY NAME (tools.go's own two builders,
// called inline): rawSchema(props, required...) and
// groupedSchema(discKey, discDescription, enum, props) — the discriminator
// itself counts as a published property. Any OTHER call is assumed to name
// a zero-arg (or any-arg) local helper function (dataToolSchema/
// notifyToolSchema/workToolSchema's own shape) and is followed into that
// function's body for either a `return rawSchema(...)`/`return
// groupedSchema(...)` (recurse) or a `properties := map[string]any{...}`
// assignment (read directly) — whichever it contains.
func extractSchemaProps(expr ast.Expr, funcTable map[string]*ast.FuncDecl, depth int) ([]propRef, bool) {
	if depth > 8 {
		return nil, false
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	name := identName(call.Fun)
	switch name {
	case "rawSchema":
		if len(call.Args) < 1 {
			return nil, false
		}
		return mapLiteralKeys(call.Args[0])
	case "groupedSchema":
		if len(call.Args) < 4 {
			return nil, false
		}
		disc, ok := stringLiteral(call.Args[0])
		if !ok {
			return nil, false
		}
		props, ok := mapLiteralKeys(call.Args[3])
		if !ok {
			return nil, false
		}
		return append([]propRef{{name: disc, pos: call.Args[0].Pos()}}, props...), true
	default:
		if name == "" {
			return nil, false
		}
		fn, ok := funcTable[name]
		if !ok || fn.Body == nil {
			return nil, false
		}
		for _, stmt := range fn.Body.List {
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				if len(s.Results) == 1 {
					if inner, ok := s.Results[0].(*ast.CallExpr); ok {
						if props, ok := extractSchemaProps(inner, funcTable, depth+1); ok {
							return props, true
						}
					}
				}
			case *ast.AssignStmt:
				if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
					if id, ok := s.Lhs[0].(*ast.Ident); ok && id.Name == "properties" {
						if props, ok := mapLiteralKeys(s.Rhs[0]); ok {
							return props, true
						}
					}
				}
			}
		}
		return nil, false
	}
}

// resolveHandlerStart names the one function the call-graph walk starts
// from: the Handler field's own call (`newXHandler(deps)`), or — when the
// field is a bare identifier (registerContractTool's `Handler: handler`) —
// whatever call that local was assigned from, found by reading the
// enclosing function's own assignments. Anything else (a selector, a
// struct field, a package-level var never locally reassigned) is reported
// unresolved rather than guessed at.
func resolveHandlerStart(expr ast.Expr, enclosing *ast.FuncDecl) (string, bool) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		name := identName(e.Fun)
		return name, name != ""
	case *ast.Ident:
		if enclosing == nil || enclosing.Body == nil {
			return "", false
		}
		found := ""
		ast.Inspect(enclosing.Body, func(n ast.Node) bool {
			if found != "" {
				return false
			}
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range as.Lhs {
				id, ok := lhs.(*ast.Ident)
				if ok && id.Name == e.Name && i < len(as.Rhs) {
					if call, ok := as.Rhs[i].(*ast.CallExpr); ok {
						if cn := identName(call.Fun); cn != "" {
							found = cn
							return false
						}
					}
				}
			}
			return true
		})
		return found, found != ""
	default:
		return "", false
	}
}

func paramTypes(fields *ast.FieldList) map[string]string {
	out := map[string]string{}
	if fields == nil {
		return out
	}
	for _, field := range fields.List {
		typeName := ""
		switch t := field.Type.(type) {
		case *ast.StarExpr:
			if id, ok := t.X.(*ast.Ident); ok {
				typeName = id.Name
			}
		case *ast.Ident:
			typeName = t.Name
		}
		if typeName == "" {
			continue
		}
		for _, n := range field.Names {
			out[n.Name] = typeName
		}
	}
	return out
}

// varDeclTypes collects every `var <name> <TypeIdent>` declared anywhere
// within root (including inside a nested FuncLit — the
// `newXHandler(deps) HandlerFunc { return func(...) {...} }` shape every
// per-verb handler in this package uses), flat and order-insensitive. The
// package's own convention never shadows a decode target's name within one
// reachable function tree, so this is exact for the corpus it reads.
func varDeclTypes(root ast.Node) map[string]string {
	out := map[string]string{}
	ast.Inspect(root, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				out[name.Name] = id.Name
			}
		}
		return true
	})
	return out
}

func resolveArgType(arg ast.Expr, scope map[string]string) (string, bool) {
	switch a := arg.(type) {
	case *ast.UnaryExpr:
		if a.Op == token.AND {
			if id, ok := a.X.(*ast.Ident); ok {
				t, ok := scope[id.Name]
				return t, ok
			}
		}
	case *ast.Ident:
		t, ok := scope[a.Name]
		return t, ok
	}
	return "", false
}

// bfsDecodeStructs walks the package-level call graph reachable from start
// (a tool's Handler constructor), collecting the struct TYPE NAME behind
// every decode-anchor call it reaches (decodeStrict itself, or a derived
// thin wrapper over it). unresolved reports true the moment a reached
// decode-anchor call's own input argument could not be attributed to a
// known local var/param — the caller must treat that as UNMEASURED, never
// as "this tool decodes nothing".
func bfsDecodeStructs(start string, funcTable map[string]*ast.FuncDecl, wrapperNames map[string]bool) (map[string]bool, bool) {
	visited := map[string]bool{}
	types := map[string]bool{}
	unresolved := false
	queue := []string{start}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		fn, ok := funcTable[name]
		if !ok || fn.Body == nil {
			continue
		}
		scope := paramTypes(fn.Type.Params)
		for k, v := range varDeclTypes(fn.Body) {
			scope[k] = v
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fname := identName(call.Fun)
			if fname == "" {
				return true
			}
			if fname == "decodeStrict" || wrapperNames[fname] {
				if len(call.Args) < 2 {
					unresolved = true
				} else if t, ok := resolveArgType(call.Args[1], scope); ok {
					types[t] = true
				} else {
					unresolved = true
				}
			}
			if _, known := funcTable[fname]; known {
				queue = append(queue, fname)
			}
			return true
		})
	}
	return types, unresolved
}

func fieldsOf(st *ast.StructType) []string {
	var out []string
	if st.Fields == nil {
		return out
	}
	for _, field := range st.Fields.List {
		if field.Tag == nil || len(field.Names) == 0 {
			continue
		}
		tagVal, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		jsonTag := reflect.StructTag(tagVal).Get("json")
		if jsonTag == "" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func position(root string, pos token.Pos) string {
	p := fset.Position(pos)
	rel, err := filepath.Rel(root, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
}
GO

run_check() { # $1 = package directory to analyze
  local pkg_dir="$1" out kind a b c tools=0 props=0 structs=0 rc
  out="$(go run "$ANALYZER_DIR/main.go" "$pkg_dir")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    gate_unmeasured "mcp-schema-decodable: the analyzer process exited $rc running over $pkg_dir — this is a defect in the gate itself, not a verdict about the corpus"
    return 0
  fi
  while IFS=$'\t' read -r kind a b c; do
    [ -z "$kind" ] && continue
    case "$kind" in
      VIOLATION)
        gate_fail "mcp-schema-decodable: tool $a declares published property \"$b\" that no decodable struct field honours ($c)"
        ;;
      UNMEASURED)
        gate_unmeasured "mcp-schema-decodable: $a"
        ;;
      OK)
        tools="$a"; props="$b"; structs="$c"
        ;;
      *)
        gate_unmeasured "mcp-schema-decodable: analyzer emitted an unrecognised line: $kind $a $b $c"
        ;;
    esac
  done <<<"$out"
  local prop_word="properties" struct_word="structs"
  [ "$props" = "1" ] && prop_word="property"
  [ "$structs" = "1" ] && struct_word="struct"
  echo "mcp-schema-decodable: measured $tools tool(s), $props published $prop_word, $structs decode $struct_word reached"
}

# ── --teeth ──────────────────────────────────────────────────────────────
teeth_expect() { # $1=label $2=red|green|unmeasured $3=needle $4=pkg_dir
  local label="$1" verdict="$2" needle="$3" pkg_dir="$4" out rc
  out="$( (_GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0; run_check "$pkg_dir"; gate_summary "mcp-schema-decodable-teeth") 2>&1 )"
  rc=$?
  case "$verdict" in
    red)
      if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq -- "$needle"; then
        echo "check-mcp-schema-decodable --teeth: FALSE GREEN — $label did not red with '$needle' (rc=$rc):" >&2
        echo "$out" >&2
        return 1
      fi
      echo "check-mcp-schema-decodable --teeth: $label reds"
      ;;
    unmeasured)
      if [ "$rc" -ne "$GATE_EXIT_UNMEASURED" ] || ! printf '%s\n' "$out" | grep -Fq -- "$needle"; then
        echo "check-mcp-schema-decodable --teeth: FALSE — $label did not go UNMEASURED with '$needle' (rc=$rc, want $GATE_EXIT_UNMEASURED):" >&2
        echo "$out" >&2
        return 1
      fi
      echo "check-mcp-schema-decodable --teeth: $label is unmeasured"
      ;;
    green)
      if [ "$rc" -ne 0 ]; then
        echo "check-mcp-schema-decodable --teeth: FALSE RED — $label should green (rc=$rc):" >&2
        echo "$out" >&2
        return 1
      fi
      echo "check-mcp-schema-decodable --teeth: $label greens"
      ;;
  esac
}

# mutate does a LITERAL (non-regex) substring replacement via bash parameter
# expansion — never sed: `before`/`after` carry backticks, braces and quotes
# (Go struct tags, map literals), which sed's BRE/ERE dialects treat as
# metacharacters on at least one of the two userlands this repo runs gates
# under (BSD sed locally, GNU sed in CI/ci-parity-docker — see
# check-view-vocabulary.sh's own GNU-vs-BSD `find -exec` incident for a
# sibling of this exact class). Refuses BOTH a no-op "before absent" and a
# mutation that silently did not apply — the check-card-content.sh precedent
# this idiom mirrors (mutate_first_row) was itself found false-green once by
# skipping the second check.
mutate() { # $1=file $2=needle-before $3=replacement
  local file="$1" before="$2" after="$3" content
  content="$(cat "$file")"
  case "$content" in
    *"$before"*) ;;
    *)
      echo "check-mcp-schema-decodable --teeth: FIXTURE BUG — '$before' is not present in $file, nothing to mutate" >&2
      return 1
      ;;
  esac
  content="${content//"$before"/"$after"}"
  printf '%s\n' "$content" >"$file"
  case "$content" in
    *"$after"*) ;;
    *)
      echo "check-mcp-schema-decodable --teeth: FIXTURE BUG — mutation to '$after' did not apply to $file" >&2
      return 1
      ;;
  esac
}

# good_fixture is the shared baseline every mutation below starts from: four
# tools exercising every shape this analyzer must resolve — a literal
# rawSchema call (fixture_tool), a literal groupedSchema call
# (grouped_tool), a schema built by a zero-arg HELPER function rather than a
# literal (helper_tool — AC-4's own named edge case), a decode reached
# through a thin decodeWrapperInput-shaped wrapper rather than a direct
# decodeStrict call (helper_tool again), a property decoded under a
# DIFFERENT Go field name than its own json tag (helper_tool's widget_id),
# and a struct-only field the schema never publishes — the P4 direction,
# AC-6 (structonly_tool's internal_only).
good_fixture() { # $1 = internal/mcp directory to create
  local dir="$1"
  mkdir -p "$dir"
  cat >"$dir/tools_fixture.go" <<'FIXTURE'
package mcp

type FixtureInput struct {
	Name string `json:"name"`
}

func fixtureSchema() any {
	return rawSchema(map[string]propSpec{
		"name": {"string", "the name"},
	})
}

func newFixtureHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in FixtureInput
		if err := decodeStrict(args, &in, "fixture", 0); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

type GroupedInput struct {
	Action string `json:"action,omitempty"`
	Widget string `json:"widget,omitempty"`
}

func groupedFixtureSchema() any {
	return groupedSchema("action", "the action to run", []string{"do"}, map[string]propSpec{
		"widget": {"string", "the widget"},
	})
}

func newGroupedHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in GroupedInput
		if err := decodeStrict(args, &in, "grouped", 0); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

// HelperInput is decoded through decodeWrapperInput — a thin wrapper whose
// ENTIRE body is `return decodeStrict(...)`, the decodeWorkInput/
// decodeDataInput shape, detected structurally rather than by name. Its
// widget_id field proves matching is TAG-based: the Go identifier
// (CompletelyDifferentName) shares nothing with the schema property name.
type HelperInput struct {
	Payload             string `json:"payload,omitempty"`
	CompletelyDifferent string `json:"widget_id,omitempty"`
}

func decodeWrapperInput(raw int, out *HelperInput) error {
	return decodeStrict(raw, out, "helper", 0)
}

func helperFixtureSchema() any {
	properties := map[string]any{
		"payload":   map[string]any{"type": "string", "description": "the payload"},
		"widget_id": map[string]any{"type": "string", "description": "the widget id"},
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	return schema
}

func newHelperHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in HelperInput
		if err := decodeWrapperInput(args, &in); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

// StructOnlyInput carries a field (internal_only) the schema deliberately
// never publishes — P4's own direction (a2a_exchange's VerifyInput.Refs,
// a2a_contract's ContractPublishInput.GeneratedFromDigest), AC-6: this
// gate asserts schema-declares -> struct-decodes ONLY, never the reverse.
type StructOnlyInput struct {
	Payload  string `json:"payload,omitempty"`
	Internal string `json:"internal_only,omitempty"`
}

func structOnlySchema() any {
	return rawSchema(map[string]propSpec{
		"payload": {"string", "the payload"},
	})
}

func newStructOnlyHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in StructOnlyInput
		if err := decodeStrict(args, &in, "structonly", 0); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

func BuildFixtureRegistry() {
	_ = ToolSpec{Name: "fixture_tool", InputSchema: fixtureSchema(), Handler: newFixtureHandler()}
	_ = ToolSpec{Name: "grouped_tool", InputSchema: groupedFixtureSchema(), Handler: newGroupedHandler()}
	_ = ToolSpec{Name: "helper_tool", InputSchema: helperFixtureSchema(), Handler: newHelperHandler()}
	_ = ToolSpec{Name: "structonly_tool", InputSchema: structOnlySchema(), Handler: newStructOnlyHandler()}
}
FIXTURE
}

run_teeth() {
  local work good
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${work:-}"' EXIT

  good="$work/good/internal/mcp"
  good_fixture "$good"
  teeth_expect "the shipped baseline (4 tools: literal/grouped/helper-built/wrapper-decoded schemas, a tag-mismatched Go name, and a struct-only field)" \
    green "" "$good"

  # RED — a schema property with NO struct field decodes it (AC-2/AC-4).
  local missing="$work/missing/internal/mcp"
  good_fixture "$missing"
  mutate "$missing/tools_fixture.go" '`json:"name"`' '`json:"renamed"`'
  teeth_expect "a schema property with no decoding struct field" \
    red 'tool fixture_tool declares published property "name"' "$missing"

  # RED — the edge case the spec names: a property the struct decodes under
  # a DIFFERENT json tag than the schema declares must still red (naming
  # matches by TAG, so a same-named-but-differently-tagged field does not
  # save it).
  local retagged="$work/retagged/internal/mcp"
  good_fixture "$retagged"
  mutate "$retagged/tools_fixture.go" '`json:"widget_id,omitempty"`' '`json:"renamed_widget,omitempty"`'
  teeth_expect "a property whose only near-miss field decodes under a different json tag" \
    red 'tool helper_tool declares published property "widget_id"' "$retagged"

  # RED — a HELPER-built schema (not a literal rawSchema/groupedSchema call)
  # must still be SEEN: prove it by planting a mismatch only reachable
  # through helperFixtureSchema's own `properties := map[string]any{...}`
  # parse path.
  local helper_seen="$work/helper-seen/internal/mcp"
  good_fixture "$helper_seen"
  mutate "$helper_seen/tools_fixture.go" \
    '"widget_id": map[string]any{"type": "string", "description": "the widget id"},' \
    '"widget_id": map[string]any{"type": "string", "description": "the widget id"}, "extra_prop": map[string]any{"type": "string", "description": "unmapped"},'
  teeth_expect "a helper-built (non-literal) schema gaining an unmapped property" \
    red 'tool helper_tool declares published property "extra_prop"' "$helper_seen"

  # GREEN — a struct-only field (P4's own direction) must never red, kept
  # separable from the direction above (AC-6). The baseline itself already
  # carries structonly_tool/internal_only and greened above; assert it
  # explicitly once more against a fixture that ONLY adds a second
  # struct-only field, so a future change to the matcher cannot pass by
  # accident of the baseline's exact shape.
  local structonly="$work/structonly/internal/mcp"
  good_fixture "$structonly"
  mutate "$structonly/tools_fixture.go" \
    'Internal string `json:"internal_only,omitempty"`' \
    'Internal string `json:"internal_only,omitempty"`
	SecondInternal string `json:"second_internal_only,omitempty"`'
  teeth_expect "a second struct-only field added beside the first" \
    green "" "$structonly"

  # UNMEASURED — a Handler this analyzer cannot resolve (a bare identifier
  # never locally assigned from a call) must refuse rather than silently
  # count as clean.
  local unmeasured_handler="$work/unmeasured-handler/internal/mcp"
  mkdir -p "$unmeasured_handler"
  cat >"$unmeasured_handler/tools_fixture.go" <<'FIXTURE'
package mcp

var opaqueHandler HandlerFunc

func BuildOpaqueRegistry() {
	_ = ToolSpec{Name: "opaque_tool", InputSchema: rawSchema(map[string]propSpec{"x": {"string", "x"}}), Handler: opaqueHandler}
}
FIXTURE
  teeth_expect "a Handler this analyzer cannot resolve to a call" \
    unmeasured "could not resolve the handler reachable from tool opaque_tool" "$unmeasured_handler"

  # UNMEASURED — a schema built by a helper this analyzer cannot parse
  # (neither a recognised builder return nor a `properties := ...` literal).
  local unmeasured_schema="$work/unmeasured-schema/internal/mcp"
  mkdir -p "$unmeasured_schema"
  cat >"$unmeasured_schema/tools_fixture.go" <<'FIXTURE'
package mcp

func opaqueSchema() any {
	pieces := []string{"{", "}"}
	return pieces[0] + pieces[1]
}

func newOpaqueSchemaHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in FixtureInput
		if err := decodeStrict(args, &in, "opaque-schema", 0); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

func BuildOpaqueSchemaRegistry() {
	_ = ToolSpec{Name: "opaque_schema_tool", InputSchema: opaqueSchema(), Handler: newOpaqueSchemaHandler()}
}
FIXTURE
  teeth_expect "an InputSchema this analyzer cannot parse into properties" \
    unmeasured "could not derive published properties for tool opaque_schema_tool" "$unmeasured_schema"

  # UNMEASURED — a decode site reaching a struct type never declared in the
  # corpus.
  local unmeasured_type="$work/unmeasured-type/internal/mcp"
  mkdir -p "$unmeasured_type"
  cat >"$unmeasured_type/tools_fixture.go" <<'FIXTURE'
package mcp

func undeclaredTypeSchema() any {
	return rawSchema(map[string]propSpec{"x": {"string", "x"}})
}

func newUndeclaredTypeHandler() HandlerFunc {
	return func(ctx int, args int) (any, string, error) {
		var in UndeclaredInput
		if err := decodeStrict(args, &in, "undeclared", 0); err != nil {
			return nil, "", err
		}
		return in, "", nil
	}
}

func BuildUndeclaredTypeRegistry() {
	_ = ToolSpec{Name: "undeclared_type_tool", InputSchema: undeclaredTypeSchema(), Handler: newUndeclaredTypeHandler()}
}
FIXTURE
  teeth_expect "a decode site reaching a struct type this corpus never declares" \
    unmeasured "decodes into unresolved type UndeclaredInput" "$unmeasured_type"

  echo "check-mcp-schema-decodable --teeth: PASS — schema-declares-but-struct-does-not-decode REDS (literal schema, helper-built schema, tag-mismatch edge case), a struct-only field GREENS (AC-6), and every unresolvable shape (opaque handler, opaque schema, undeclared type) reports UNMEASURED rather than a false green"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

PKG_DIR="${MCP_SCHEMA_DECODABLE_PKG_DIR:-$PKG_DIR_DEFAULT}"
if [ ! -d "$PKG_DIR" ]; then
  gate_unmeasured "mcp-schema-decodable: package directory $PKG_DIR does not exist"
  gate_summary "mcp-schema-decodable"
  exit $?
fi
run_check "$PKG_DIR"
gate_summary "mcp-schema-decodable"
exit $?
