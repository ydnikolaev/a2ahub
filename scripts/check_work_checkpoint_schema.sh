#!/usr/bin/env bash
# Guard P2's durable work vocabulary. The announcement schema, authoring
# template, pure lease core, contextual validator and normative authoring
# contract must carry the same closed mode and wait-kind enum.
set -euo pipefail

ROOT="${WORK_CHECKPOINT_SCHEMA_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"
ANALYZER_DIR="$(mktemp -d)"
trap 'rm -rf "$ANALYZER_DIR"' EXIT

cat >"$ANALYZER_DIR/main.go" <<'GO'
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

var canonicalModes = []string{
	"planning",
	"implementing",
	"testing",
	"reviewing",
	"waiting",
	"paused",
	"finished",
}

var canonicalWaitKinds = []string{
	"system",
	"human",
	"tool",
	"timer",
	"external",
}

type checker struct {
	root   string
	errors []string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: work-checkpoint-schema-analyzer <repository-root>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "work-checkpoint-schema: resolve root: %v\n", err)
		os.Exit(2)
	}
	c := &checker{root: root}
	c.checkSchema()
	c.checkTemplate()
	c.checkGoVocabulary("internal/workreport/types.go", "Mode", "WaitKind", "workreport")
	c.checkGoVocabulary("internal/validate/work_checkpoint.go", "WorkMode", "WorkWaitKind", "validator")
	c.checkDocs()
	if len(c.errors) > 0 {
		sort.Strings(c.errors)
		for _, message := range c.errors {
			fmt.Fprintf(os.Stderr, "work-checkpoint-schema: FAIL — %s\n", message)
		}
		os.Exit(1)
	}
	fmt.Println("work-checkpoint-schema: ok — schema/template/workreport/validator/docs share 7 modes and 5 wait kinds")
}

func (c *checker) checkSchema() {
	const rel = "schemas/envelope/v2/announcement.schema.json"
	var doc map[string]any
	if !c.readJSON(rel, &doc) {
		return
	}
	modes := stringArrayAt(doc, "$defs", "workCheckpoint", "properties", "mode", "enum")
	c.compare(rel+" mode enum", modes, canonicalModes)
	waits := stringArrayAt(doc, "$defs", "waitingOn", "properties", "kind", "enum")
	c.compare(rel+" wait-kind enum", waits, canonicalWaitKinds)
}

func (c *checker) checkTemplate() {
	const rel = "schemas/templates/v2/announcement.md"
	raw, ok := c.read(rel)
	if !ok {
		return
	}
	modes := placeholderVocabulary(string(raw), "mode: <")
	c.compare(rel+" mode vocabulary", modes, canonicalModes)
	waits := placeholderVocabulary(string(raw), "kind: <")
	c.compare(rel+" wait-kind vocabulary", waits, canonicalWaitKinds)
}

func (c *checker) checkGoVocabulary(rel, modeType, waitType, label string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(c.root, filepath.FromSlash(rel)), nil, parser.AllErrors)
	if err != nil {
		c.add("parse %s: %v", rel, err)
		return
	}
	c.compare(label+" "+modeType+" constants", typedStringConstants(file, modeType), canonicalModes)
	c.compare(label+" "+waitType+" constants", typedStringConstants(file, waitType), canonicalWaitKinds)
	if label == "workreport" {
		c.compare(label+" "+modeType+" Valid behavior", validMethodVocabulary(file, modeType), canonicalModes)
		c.compare(label+" "+waitType+" Valid behavior", validMethodVocabulary(file, waitType), canonicalWaitKinds)
	}
}

func (c *checker) checkDocs() {
	const rel = "docs/features/active/operational-confidence-2026-08/specs/02-reported-work-checkpoints.md"
	raw, ok := c.read(rel)
	if !ok {
		return
	}
	modes := tableBacktickVocabulary(string(raw), "| `mode` |", "mode")
	c.compare(rel+" mode vocabulary", modes, canonicalModes)
	waits := tableBacktickVocabulary(string(raw), "| `waiting_on[].kind` |", "waiting_on[].kind")
	c.compare(rel+" wait-kind vocabulary", waits, canonicalWaitKinds)
}

func (c *checker) readJSON(rel string, out any) bool {
	raw, ok := c.read(rel)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		c.add("decode %s: %v", rel, err)
		return false
	}
	return true
}

func (c *checker) read(rel string) ([]byte, bool) {
	raw, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		c.add("read %s: %v", rel, err)
		return nil, false
	}
	return raw, true
}

func (c *checker) compare(label string, got, want []string) {
	if !reflect.DeepEqual(got, want) {
		c.add("%s = %v, want closed vocabulary %v", label, got, want)
	}
}

func (c *checker) add(format string, args ...any) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}

func stringArrayAt(root map[string]any, path ...string) []string {
	var current any = root
	for _, segment := range path {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = mapping[segment]
		if !ok {
			return nil
		}
	}
	raw, ok := current.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, value)
	}
	return out
}

func placeholderVocabulary(text, marker string) []string {
	for _, line := range strings.Split(text, "\n") {
		start := strings.Index(line, marker)
		if start < 0 {
			continue
		}
		value := line[start+len(marker):]
		end := strings.IndexByte(value, '>')
		if end < 0 {
			return nil
		}
		return strings.Split(value[:end], "|")
	}
	return nil
}

func tableBacktickVocabulary(text, marker, field string) []string {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var values []string
		for {
			start := strings.IndexByte(line, '`')
			if start < 0 {
				break
			}
			line = line[start+1:]
			end := strings.IndexByte(line, '`')
			if end < 0 {
				return nil
			}
			values = append(values, line[:end])
			line = line[end+1:]
		}
		if len(values) > 0 && values[0] == field {
			values = values[1:]
		}
		return values
	}
	return nil
}

func typedStringConstants(file *ast.File, typeName string) []string {
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, rawSpec := range gen.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := spec.Type.(*ast.Ident)
			if !ok || ident.Name != typeName || len(spec.Values) != 1 {
				continue
			}
			literal, ok := spec.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			values = append(values, strings.Trim(literal.Value, `"`))
		}
	}
	return values
}

func validMethodVocabulary(file *ast.File, typeName string) []string {
	constants := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, rawSpec := range gen.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}
			ident, ok := spec.Type.(*ast.Ident)
			literal, literalOK := spec.Values[0].(*ast.BasicLit)
			if !ok || ident.Name != typeName || !literalOK || literal.Kind != token.STRING {
				continue
			}
			constants[spec.Names[0].Name] = strings.Trim(literal.Value, `"`)
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Valid" || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
			continue
		}
		receiver, ok := fn.Recv.List[0].Type.(*ast.Ident)
		if !ok || receiver.Name != typeName {
			continue
		}
		var values []string
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				switch item := expression.(type) {
				case *ast.Ident:
					if value, found := constants[item.Name]; found {
						values = append(values, value)
					} else {
						values = append(values, "<unknown-ident:"+item.Name+">")
					}
				case *ast.BasicLit:
					values = append(values, strings.Trim(item.Value, `"`))
				default:
					values = append(values, "<non-constant-case>")
				}
			}
			return true
		})
		return values
	}
	return nil
}
GO

go run "$ANALYZER_DIR/main.go" "$ROOT"
