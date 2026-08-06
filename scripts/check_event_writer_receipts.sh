#!/usr/bin/env bash
# Guard the first-party CLI/MCP event-writer boundary. The analyzer uses the Go
# parser rather than line counts: every typed event constructor must source its
# serialized receipt from the canonical fold candidate evaluation, and no
# public writer input may expose a caller-authored state.
#
# lane-inputs:
#   internal/cli/*.go
#   internal/mcp/*.go
#   !internal/cli/*_test.go
#   !internal/mcp/*_test.go
# lane-reads-opaque: the gate writes its Go analyzer into "$ANALYZER_DIR/main.go"
#   (mktemp -d) and `go run`s it, so the classifier cannot follow the read. The
#   analyzer's own reads are the two non-recursive os.ReadDir walks over
#   internal/cli and internal/mcp declared above, verified by reading the
#   heredoc body rather than inferred from the gate's name.
set -euo pipefail

ROOT="${EVENT_WRITER_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"
ANALYZER_DIR="$(mktemp -d)"
trap 'rm -rf "$ANALYZER_DIR"' EXIT

cat >"$ANALYZER_DIR/main.go" <<'GO'
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var requiredWriters = []string{
	"internal/cli/cmd_submit.go",
	"internal/cli/cmd_lifecycle.go",
	"internal/cli/cmd_contract.go",
	"internal/mcp/tools_submit.go",
	"internal/mcp/tools_lifecycle.go",
	"internal/mcp/tools_contract.go",
}

var canonicalEvaluators = map[string]bool{
	"EvaluateCandidate":                  true,
	"lifecycleEvaluateCandidate":         true,
	"lifecycleEvaluateResponseCandidate": true,
	"contractEvaluateCandidate":          true,
	"evaluateCandidate":                  true,
	"evaluateResponseCandidate":          true,
}

var receiptHelpers = map[string]bool{
	"lifecycleReceiptState": true,
	"contractReceiptState":  true,
	"eventReceiptState":     true,
}

type sourceFile struct {
	rel  string
	fset *token.FileSet
	file *ast.File
}

type checker struct {
	root          string
	errors        []string
	constructors  map[string]int
	eventDocTypes map[string]bool
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: event-writer-receipts-analyzer <repository-root>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "event-writer-receipts: resolve root: %v\n", err)
		os.Exit(2)
	}
	c := &checker{root: root, constructors: make(map[string]int), eventDocTypes: make(map[string]bool)}
	files, err := c.parseProductionFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "event-writer-receipts: %v\n", err)
		os.Exit(2)
	}
	c.findEventDocumentTypes(files)
	for _, src := range files {
		c.checkSource(src)
	}
	for _, rel := range requiredWriters {
		if c.constructors[rel] == 0 {
			c.errors = append(c.errors, fmt.Sprintf("%s: no first-party event constructor inventoried", rel))
		}
	}
	if len(c.errors) > 0 {
		sort.Strings(c.errors)
		for _, message := range c.errors {
			fmt.Fprintf(os.Stderr, "event-writer-receipts: FAIL — %s\n", message)
		}
		os.Exit(1)
	}
	total := 0
	for _, count := range c.constructors {
		total += count
	}
	fmt.Printf("event-writer-receipts: ok — %d required writer files, %d event constructors; state is evaluator-owned\n", len(requiredWriters), total)
}

func (c *checker) parseProductionFiles() ([]sourceFile, error) {
	var files []sourceFile
	for _, dir := range []string{"internal/cli", "internal/mcp"} {
		absDir := filepath.Join(c.root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(dir, entry.Name()))
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, filepath.Join(absDir, entry.Name()), nil, parser.AllErrors)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", rel, err)
			}
			files = append(files, sourceFile{rel: rel, fset: fset, file: parsed})
		}
	}
	return files, nil
}

func (c *checker) findEventDocumentTypes(files []sourceFile) {
	for _, src := range files {
		for _, decl := range src.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, rawSpec := range gen.Specs {
				spec, ok := rawSpec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				strct, ok := spec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				tags := make(map[string]bool)
				for _, field := range strct.Fields.List {
					if field.Tag == nil {
						continue
					}
					tag, err := strconv.Unquote(field.Tag.Value)
					if err != nil {
						continue
					}
					for _, want := range []string{"schema", "event", "subject", "transition", "state"} {
						if hasTagName(tag, "yaml", want) {
							tags[want] = true
						}
					}
				}
				if tags["schema"] && tags["event"] && tags["subject"] && tags["transition"] && tags["state"] {
					c.eventDocTypes[src.file.Name.Name+"."+spec.Name.Name] = true
				}
			}
		}
	}
}

func hasTagName(tag, key, name string) bool {
	for tag != "" {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return false
		}
		colon := strings.IndexByte(tag, ':')
		if colon <= 0 || colon+1 >= len(tag) || tag[colon+1] != '"' {
			return false
		}
		quoted := tag[colon+1:]
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return false
		}
		consumed := len(strconv.Quote(value))
		if tag[:colon] == key && strings.Split(value, ",")[0] == name {
			return true
		}
		tag = quoted[consumed:]
	}
	return false
}

func (c *checker) checkSource(src sourceFile) {
	isWriter := c.isWriterSource(src)
	for _, decl := range src.file.Decls {
		if isWriter {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			c.checkPublicInputs(src, gen)
			}
		}
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		canonical := canonicalBindings(fn.Body)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CompositeLit:
				c.checkEventConstructor(src, value, canonical)
			case *ast.CallExpr:
				if !isWriter {
					return true
				}
				c.checkPublicStateSource(src, value)
			}
			return true
		})
	}
}

func (c *checker) isWriterSource(src sourceFile) bool {
	found := false
	ast.Inspect(src.file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		fields := keyedFields(lit)
		if c.isEventConstructor(src, lit, fields) && fields["Transition"] != nil {
			found = true
			return false
		}
		return true
	})
	return found
}

func (c *checker) checkPublicInputs(src sourceFile, gen *ast.GenDecl) {
	for _, rawSpec := range gen.Specs {
		spec, ok := rawSpec.(*ast.TypeSpec)
		if !ok || !strings.HasSuffix(spec.Name.Name, "Input") {
			continue
		}
		strct, ok := spec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range strct.Fields.List {
			for _, name := range field.Names {
				if name.Name == "State" {
					c.addError(src, field.Pos(), "%s exposes user-authored State", spec.Name.Name)
				}
			}
			if field.Tag != nil {
				tag, err := strconv.Unquote(field.Tag.Value)
				if err == nil && (hasTagName(tag, "json", "state") || hasTagName(tag, "yaml", "state")) {
					c.addError(src, field.Pos(), "%s exposes a public state field", spec.Name.Name)
				}
			}
		}
	}
}

func (c *checker) checkPublicStateSource(src sourceFile, call *ast.CallExpr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	flagNameArg := -1
	switch selector.Sel.Name {
	case "String", "Bool", "Int", "Duration", "Func":
		flagNameArg = 0
	case "StringVar", "BoolVar", "IntVar", "DurationVar", "Var":
		flagNameArg = 1
	}
	if flagNameArg >= 0 && len(call.Args) > flagNameArg && stringLiteral(call.Args[flagNameArg]) == "state" {
		c.addError(src, call.Pos(), "CLI writer exposes --state")
	}
	if (selector.Sel.Name == "Getenv" || selector.Sel.Name == "LookupEnv") && len(call.Args) > 0 {
		if strings.Contains(strings.ToUpper(stringLiteral(call.Args[0])), "STATE") {
			c.addError(src, call.Pos(), "writer accepts state from environment")
		}
	}
}

func canonicalBindings(body *ast.BlockStmt) map[string]bool {
	bindings := make(map[string]bool)
	ast.Inspect(body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.AssignStmt:
			if len(stmt.Rhs) == 1 && len(stmt.Lhs) > 0 && canonicalCall(stmt.Rhs[0]) {
				if id, ok := stmt.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
					bindings[id.Name] = true
				}
			}
		case *ast.ValueSpec:
			if len(stmt.Values) == 1 && len(stmt.Names) > 0 && canonicalCall(stmt.Values[0]) {
				if stmt.Names[0].Name != "_" {
					bindings[stmt.Names[0].Name] = true
				}
			}
		}
		return true
	})
	return bindings
}

func canonicalCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	return canonicalEvaluators[callName(call.Fun)]
}

func callName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func (c *checker) checkEventConstructor(src sourceFile, lit *ast.CompositeLit, canonical map[string]bool) {
	fields := keyedFields(lit)
	if !c.isEventConstructor(src, lit, fields) {
		return
	}
	if _, hasTransition := fields["Transition"]; !hasTransition {
		return
	}
	c.constructors[src.rel]++
	state, ok := fields["State"]
	if !ok {
		c.addError(src, lit.Pos(), "event constructor omits evaluator-owned State")
		return
	}
	if !receiptExpression(state, canonical) {
		c.addError(src, state.Pos(), "event constructor State bypasses canonical fold evaluation")
	}
}

func (c *checker) isEventConstructor(src sourceFile, lit *ast.CompositeLit, fields map[string]ast.Expr) bool {
	if id, ok := lit.Type.(*ast.Ident); ok && c.eventDocTypes[src.file.Name.Name+"."+id.Name] {
		return true
	}
	return strings.HasPrefix(stringLiteral(fields["Schema"]), "event/") && fields["Event"] != nil && fields["Subject"] != nil
}

func keyedFields(lit *ast.CompositeLit) map[string]ast.Expr {
	fields := make(map[string]ast.Expr)
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		var name string
		switch key := pair.Key.(type) {
		case *ast.Ident:
			name = key.Name
		case *ast.BasicLit:
			if key.Kind == token.STRING {
				name, _ = strconv.Unquote(key.Value)
			}
		}
		fields[name] = pair.Value
	}
	return fields
}

func receiptExpression(expr ast.Expr, canonical map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if receiptHelpers[callName(call.Fun)] {
		id, ok := call.Args[0].(*ast.Ident)
		return ok && canonical[id.Name]
	}
	if callName(call.Fun) != "string" {
		return false
	}
	selector, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Outcome" {
		return false
	}
	id, ok := selector.X.(*ast.Ident)
	return ok && canonical[id.Name]
}

func stringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return value
}

func (c *checker) addError(src sourceFile, pos token.Pos, format string, args ...any) {
	location := src.fset.Position(pos)
	c.errors = append(c.errors, fmt.Sprintf("%s:%d: %s", src.rel, location.Line, fmt.Sprintf(format, args...)))
}
GO

exec go run "$ANALYZER_DIR/main.go" "$ROOT"
