#!/usr/bin/env bash
# Guard P5's carried-set vocabulary and ownership. JSON schemas, the v2
# template and internal/contract must agree on one closed role/profile model;
# published v1 bytes stay immutable; no second carried-set/digest builder may
# grow in a transport or adapter.
#
# lane-inputs:
#   schemas/envelope/v2/contract.schema.json
#   schemas/templates/v2/contract.md
#   schemas/event/v2/event.schema.json
#   schemas/published-v1.sha256
#   schemas/**/v1/**
#   internal/**/*.go
#   !internal/**/*_test.go
# lane-reads-opaque: this gate writes a complete Go analyzer into
#   "$ANALYZER_DIR/main.go" (a mktemp -d, so the path exists only at run time)
#   and `go run`s it. The classifier cannot follow that, and the heredoc body is
#   excluded from shell scanning by design — so what the analyzer reads is
#   declared above from having READ the body: it walks internal/ for a second
#   digest builder, and reads schemas/published-v1.sha256 plus every v1 path the
#   manifest lists.
set -euo pipefail

ROOT="${CONTRACT_CARRIED_SET_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"
ANALYZER_DIR="$(mktemp -d)"
trap 'rm -rf "$ANALYZER_DIR"' EXIT

cat >"$ANALYZER_DIR/main.go" <<'GO'
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

var canonicalRoles = []string{
	"schema",
	"valid-fixture",
	"invalid-fixture",
	"errors",
	"vocabulary",
	"limits",
	"changelog",
	"example",
	"other",
}

var canonicalRoleRoots = map[string]string{
	"schema":          "schema/",
	"valid-fixture":   "fixtures/valid/",
	"invalid-fixture": "fixtures/invalid/",
	"errors":          "artifacts/",
	"vocabulary":      "artifacts/",
	"limits":          "artifacts/",
	"changelog":       "artifacts/",
	"example":         "artifacts/",
	"other":           "artifacts/",
}

var publicationProfiles = []string{
	"contract-tree-v1",
	"contract-set-v2",
}

var allProfiles = []string{
	"contract-tree-v1",
	"contract-set-v2",
	"export-source-v1",
}

// publishedV1ManifestDigest is the second lock on the ratchet: the byte
// manifest pins the schemas, and this pins the MANIFEST, so a row cannot be
// quietly removed to unfreeze a file.
//
// Updated 2026-08-07 when the eight schemas/templates/v1/*.md rows were
// dropped. That was a deliberate scope correction, not an unfreeze: a schema
// is a wire contract whose bytes decide the validity of documents already
// committed to shared spaces, while a template is prose nothing validates
// against. See check-operational-confidence.sh's check_hashes for the full
// reasoning and the defect that forced it. All 18 schema rows stayed.
const publishedV1ManifestDigest = "e1e6e48d47987a2207cf5e6422018cc1d3e45a1367f6ca5d9da918fc8f2c621f"

var canonicalBuilderFunctions = map[string]bool{
	"BuildCarriedSet":      true,
	"ValidateCarriedSet":   true,
	"ResolveDigestProfile": true,
	"buildDeclaredSet":     true,
	"buildLegacySet":       true,
	"digestBytes":          true,
}

// combineDigestAllowlist names every file permitted to compose an aggregate
// digest from per-file digests. The rule this enforces is "one aggregate
// implementation", not "one caller of it": a second CALLER of the single
// shipped function is exactly what keeps a second implementation from being
// written, which is why internal/datapackage/entryset.go is here. A data
// package and a contract carried set are hashed by the same function on
// purpose (spec 05a §5, plan D-3) — a verdict must be able to name bytes the
// other side can reproduce, and two aggregate algorithms is precisely how
// that stops being true.
var combineDigestAllowlist = map[string]bool{
	"internal/artifact/digesttree.go":       true,
	"internal/contract/set.go":              true,
	"internal/contract/publication_plan.go": true,
	"internal/datapackage/entryset.go":      true,
}

var combineDigestCallCeiling = map[string]int{
	"internal/artifact/digesttree.go":       1,
	"internal/contract/set.go":              3,
	"internal/contract/publication_plan.go": 1,
	// BuildEntrySet composes the aggregate for a fresh pack; VerifyEntrySet
	// recomputes it from entries already verified one by one (L-3's ordering).
	"internal/datapackage/entryset.go": 2,
}

var legacySubtreeAllowlist = map[string]bool{}

type checker struct {
	root   string
	errors []string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: contract-carried-set-analyzer <repository-root>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "contract-carried-set: resolve root: %v\n", err)
		os.Exit(2)
	}
	c := &checker{root: root}
	c.checkContractSchema()
	c.checkCoreVocabulary()
	c.checkTemplateVocabulary()
	c.checkEventProfiles()
	c.checkPublishedV1()
	c.checkDuplicateImplementations()
	if len(c.errors) != 0 {
		sort.Strings(c.errors)
		for _, message := range c.errors {
			fmt.Fprintf(os.Stderr, "contract-carried-set: FAIL — %s\n", message)
		}
		os.Exit(1)
	}
	fmt.Println("contract-carried-set: ok — 9 roles, 2 publication profiles, 1 provenance profile, immutable v1 and one canonical builder")
}

func (c *checker) checkContractSchema() {
	doc := c.readJSON("schemas/envelope/v2/contract.schema.json")
	roles := stringArrayAt(doc, "$defs", "artifactEntry", "properties", "role", "enum")
	if !reflect.DeepEqual(roles, canonicalRoles) {
		c.add("contract schema role enum = %v, want canonical 9-role vocabulary %v", roles, canonicalRoles)
	}
	roleRoots := schemaRoleRoots(doc)
	if !reflect.DeepEqual(roleRoots, canonicalRoleRoots) {
		c.add("contract schema role/root matrix = %v, want %v", roleRoots, canonicalRoleRoots)
	}
}

func (c *checker) checkCoreVocabulary() {
	fset := token.NewFileSet()
	typesRel := "internal/contract/types.go"
	typesFile, err := parser.ParseFile(fset, filepath.Join(c.root, filepath.FromSlash(typesRel)), nil, parser.AllErrors)
	if err != nil {
		c.add("parse %s: %v", typesRel, err)
		return
	}
	roles := typedStringConstants(typesFile, "Role")
	if !reflect.DeepEqual(roles, canonicalRoles) {
		c.add("internal/contract Role constants = %v, want %v", roles, canonicalRoles)
	}
	profiles := typedStringConstants(typesFile, "DigestProfile")
	if !reflect.DeepEqual(profiles, allProfiles) {
		c.add("internal/contract DigestProfile constants = %v, want %v", profiles, allProfiles)
	}

	setRel := "internal/contract/set.go"
	setFile, err := parser.ParseFile(fset, filepath.Join(c.root, filepath.FromSlash(setRel)), nil, parser.AllErrors)
	if err != nil {
		c.add("parse %s: %v", setRel, err)
		return
	}
	roleRoots := coreRoleRoots(setFile, typedStringConstantMap(typesFile, "Role"))
	if !reflect.DeepEqual(roleRoots, canonicalRoleRoots) {
		c.add("internal/contract role/root matrix = %v, want %v", roleRoots, canonicalRoleRoots)
	}
	routedProfiles := switchCaseValues(setFile, "BuildCarriedSet", typedStringConstantMap(typesFile, "DigestProfile"))
	if !reflect.DeepEqual(routedProfiles, sortedStrings(publicationProfiles)) {
		c.add("internal/contract BuildCarriedSet profiles = %v, want publication profiles %v", routedProfiles, publicationProfiles)
	}
	legacyRoots := stringLiteralsInFunction(setFile, "insideLegacyRoot")
	if !reflect.DeepEqual(legacyRoots, []string{"schema/", "fixtures/"}) {
		c.add("internal/contract legacy roots = %v, want immutable [schema/ fixtures/]", legacyRoots)
	}
}

func (c *checker) checkTemplateVocabulary() {
	rel := "schemas/templates/v2/contract.md"
	raw, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		c.add("read %s: %v", rel, err)
		return
	}
	text := string(raw)
	if !strings.Contains(text, "export-source-v1") {
		c.add("v2 contract template omits export-source-v1 provenance profile")
	}

	allowed := stringSet(canonicalRoles)
	seen := make(map[string]bool, len(canonicalRoles))
	var roles []string
	addRole := func(role string, line int) {
		if !allowed[role] {
			c.add("%s:%d declares non-canonical template role %q", rel, line, role)
			return
		}
		if !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "#")))
		if strings.HasPrefix(value, "role:") {
			fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(value, "role:")))
			if len(fields) == 0 {
				c.add("%s:%d has an empty template role", rel, line)
			} else {
				addRole(fields[0], line)
			}
		}
		if marker := strings.Index(value, "under artifacts/:"); marker >= 0 {
			vocabulary := value[marker+len("under artifacts/:"):]
			vocabulary = strings.NewReplacer(",", " ", ".", " ", " or ", " ").Replace(vocabulary)
			for _, role := range strings.Fields(vocabulary) {
				addRole(role, line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		c.add("scan %s: %v", rel, err)
	}
	if !reflect.DeepEqual(roles, canonicalRoles) {
		c.add("v2 contract template role vocabulary = %v, want %v", roles, canonicalRoles)
	}
}

func (c *checker) checkEventProfiles() {
	doc := c.readJSON("schemas/event/v2/event.schema.json")
	profiles := stringArrayAt(doc, "properties", "digest_profile", "enum")
	if !reflect.DeepEqual(profiles, publicationProfiles) {
		c.add("event/v2 digest_profile enum = %v, want %v", profiles, publicationProfiles)
	}
	required := stringArrayAt(doc, "allOf", "0", "then", "required")
	if required == nil {
		rawAllOf, _ := doc["allOf"].([]any)
		if len(rawAllOf) != 0 {
			if first, ok := rawAllOf[0].(map[string]any); ok {
				required = stringArrayAt(first, "then", "required")
			}
		}
	}
	wantRequired := []string{"version", "digest", "digest_profile", "publication"}
	if !reflect.DeepEqual(required, wantRequired) {
		c.add("event/v2 contract publish required fields = %v, want %v", required, wantRequired)
	}
	if pattern, _ := valueAt(doc, "properties", "version", "pattern").(string); pattern != "^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$" {
		c.add("event/v2 publication version pattern is not canonical semver")
	}
	if pattern, _ := valueAt(doc, "properties", "digest", "pattern").(string); pattern != "^sha256:[a-f0-9]{64}$" {
		c.add("event/v2 publication digest pattern is not full lowercase sha256")
	}
}

func (c *checker) checkPublishedV1() {
	const rel = "schemas/published-v1.sha256"
	manifestPath := filepath.Join(c.root, filepath.FromSlash(rel))
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		c.add("read %s: %v", rel, err)
		return
	}
	manifestDigest := fmt.Sprintf("%x", sha256.Sum256(manifestRaw))
	if manifestDigest != publishedV1ManifestDigest {
		c.add("published v1 checksum manifest mutated: digest %s, want %s", manifestDigest, publishedV1ManifestDigest)
	}

	file, err := os.Open(manifestPath)
	if err != nil {
		c.add("open %s: %v", rel, err)
		return
	}
	defer file.Close()

	seen := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 || len(parts[0]) != 64 {
			c.add("%s has malformed row %q", rel, line)
			continue
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			c.add("%s has malformed digest %q", rel, parts[0])
			continue
		}
		if !strings.Contains(parts[1], "/v1/") && !strings.Contains(parts[1], "/templates/v1/") {
			c.add("%s row is not a published v1 schema/template: %s", rel, parts[1])
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(parts[1])))
		if err != nil {
			c.add("published v1 file %s: %v", parts[1], err)
			continue
		}
		got := fmt.Sprintf("%x", sha256.Sum256(raw))
		if got != parts[0] {
			c.add("published v1 mutation: %s digest %s, want %s", parts[1], got, parts[0])
		}
		seen++
	}
	if err := scanner.Err(); err != nil {
		c.add("scan %s: %v", rel, err)
	}
	if seen == 0 {
		c.add("%s inventories no published v1 files", rel)
	}
}

func (c *checker) checkDuplicateImplementations() {
	root := filepath.Join(c.root, "internal")
	combineCalls := make(map[string]int)
	canonicalDefinitions := make(map[string]int)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(c.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			c.add("parse %s: %v", rel, err)
			return nil
		}
		insideCanonicalPackage := strings.HasPrefix(rel, "internal/contract/")
		for _, decl := range file.Decls {
			switch value := decl.(type) {
			case *ast.FuncDecl:
				if insideCanonicalPackage && canonicalBuilderFunctions[value.Name.Name] {
					canonicalDefinitions[value.Name.Name]++
				}
				if !insideCanonicalPackage && canonicalBuilderFunctions[value.Name.Name] {
					c.add("%s declares second carried-set/profile builder %s", rel, value.Name.Name)
				}
			case *ast.GenDecl:
				var localRoles, localProfiles []string
				for _, rawSpec := range value.Specs {
					spec, ok := rawSpec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range spec.Names {
						if name.Name == "contractDigestSubtrees" && !legacySubtreeAllowlist[rel] {
							c.add("%s declares a second legacy contract digest subtree inventory", rel)
						}
					}
					if value.Tok == token.CONST && !insideCanonicalPackage {
						for _, expression := range spec.Values {
							literal, ok := expression.(*ast.BasicLit)
							if !ok || literal.Kind != token.STRING {
								continue
							}
							decoded, err := strconv.Unquote(literal.Value)
							if err == nil && stringSet(canonicalRoles)[decoded] {
								localRoles = append(localRoles, decoded)
							}
							if err == nil && stringSet(allProfiles)[decoded] {
								localProfiles = append(localProfiles, decoded)
							}
						}
					}
				}
				if len(localRoles) > 1 {
					c.add("%s redeclares carried-set role enum %v outside internal/contract", rel, localRoles)
				}
				if len(localProfiles) != 0 {
					c.add("%s redeclares carried-set digest profile %v outside internal/contract", rel, localProfiles)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "CombineDigestPairs" {
				combineCalls[rel]++
				if !combineDigestAllowlist[rel] {
					c.add("%s calls CombineDigestPairs outside the canonical builder/legacy adapter allowlist", rel)
				} else if combineCalls[rel] > combineDigestCallCeiling[rel] {
					c.add("%s adds a duplicate CombineDigestPairs implementation beyond grandfathered ceiling %d", rel, combineDigestCallCeiling[rel])
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		c.add("walk internal Go sources: %v", err)
	}
	for name := range canonicalBuilderFunctions {
		if canonicalDefinitions[name] != 1 {
			c.add("internal/contract canonical function %s has %d definitions, want exactly 1", name, canonicalDefinitions[name])
		}
	}
}

func (c *checker) readJSON(rel string) map[string]any {
	raw, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		c.add("read %s: %v", rel, err)
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		c.add("decode %s: %v", rel, err)
		return nil
	}
	return doc
}

func stringArrayAt(root map[string]any, path ...string) []string {
	var value any = root
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	return result
}

func schemaRoleRoots(doc map[string]any) map[string]string {
	result := make(map[string]string)
	rawRules, ok := valueAt(doc, "$defs", "artifactEntry", "allOf").([]any)
	if !ok {
		return result
	}
	for _, rawRule := range rawRules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		var roles []string
		if role, ok := valueAt(rule, "if", "properties", "role", "const").(string); ok {
			roles = []string{role}
		} else {
			rawRoles, ok := valueAt(rule, "if", "properties", "role", "enum").([]any)
			if !ok {
				continue
			}
			for _, rawRole := range rawRoles {
				role, ok := rawRole.(string)
				if !ok {
					roles = nil
					break
				}
				roles = append(roles, role)
			}
		}
		pattern, ok := valueAt(rule, "then", "properties", "path", "pattern").(string)
		if !ok {
			continue
		}
		root := ""
		for _, candidate := range []string{"schema/", "fixtures/valid/", "fixtures/invalid/", "artifacts/"} {
			if strings.HasPrefix(pattern, "^"+candidate) {
				root = candidate
				break
			}
		}
		for _, role := range roles {
			result[role] = root
		}
	}
	return result
}

func valueAt(root map[string]any, path ...string) any {
	var value any = root
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func coreRoleRoots(file *ast.File, constants map[string]string) map[string]string {
	result := make(map[string]string)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "rootForRole" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switchStmt, ok := node.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, rawClause := range switchStmt.Body.List {
				clause, ok := rawClause.(*ast.CaseClause)
				if !ok || len(clause.List) == 0 {
					continue
				}
				root := ""
				for _, stmt := range clause.Body {
					ret, ok := stmt.(*ast.ReturnStmt)
					if !ok || len(ret.Results) == 0 {
						continue
					}
					literal, ok := ret.Results[0].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					root, _ = strconv.Unquote(literal.Value)
				}
				for _, expression := range clause.List {
					ident, ok := expression.(*ast.Ident)
					if !ok {
						continue
					}
					if role, exists := constants[ident.Name]; exists {
						result[role] = root
					}
				}
			}
			return false
		})
	}
	return result
}

func switchCaseValues(file *ast.File, function string, constants map[string]string) []string {
	var result []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switchStmt, ok := node.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, rawClause := range switchStmt.Body.List {
				clause, ok := rawClause.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expression := range clause.List {
					ident, ok := expression.(*ast.Ident)
					if !ok {
						continue
					}
					if value, exists := constants[ident.Name]; exists {
						result = append(result, value)
					}
				}
			}
			return false
		})
	}
	sort.Strings(result)
	return result
}

func stringLiteralsInFunction(file *ast.File, function string) []string {
	var result []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil {
				result = append(result, value)
			}
			return true
		})
	}
	return result
}

func typedStringConstants(file *ast.File, typeName string) []string {
	var result []string
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
			if !ok || ident.Name != typeName {
				continue
			}
			for _, expression := range spec.Values {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				decoded, err := strconv.Unquote(literal.Value)
				if err == nil {
					result = append(result, decoded)
				}
			}
		}
	}
	return result
}

func typedStringConstantMap(file *ast.File, typeName string) map[string]string {
	result := make(map[string]string)
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
			if !ok || ident.Name != typeName || len(spec.Names) != len(spec.Values) {
				continue
			}
			for i, expression := range spec.Values {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				decoded, err := strconv.Unquote(literal.Value)
				if err == nil {
					result[spec.Names[i].Name] = decoded
				}
			}
		}
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func (c *checker) add(format string, args ...any) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}
GO

go run "$ANALYZER_DIR/main.go" "$ROOT"
