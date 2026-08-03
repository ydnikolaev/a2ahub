#!/usr/bin/env bash
# Guard P5's carried-set vocabulary and ownership. JSON schemas, the v2
# template and internal/contract must agree on one closed role/profile model;
# published v1 bytes stay immutable; no second carried-set/digest builder may
# grow in a transport or adapter.
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

var publicationProfiles = []string{
	"contract-tree-v1",
	"contract-set-v2",
}

var allProfiles = []string{
	"contract-tree-v1",
	"contract-set-v2",
	"export-source-v1",
}

const publishedV1ManifestDigest = "fbf7b01a7e024e2431c2104422df28c5d74595d509fe61cd6a03a0d9fd59a599"

var canonicalBuilderFunctions = map[string]bool{
	"BuildCarriedSet":     true,
	"ValidateCarriedSet":  true,
	"ResolveDigestProfile": true,
	"buildDeclaredSet":    true,
	"buildLegacySet":      true,
	"digestBytes":         true,
}

var combineDigestAllowlist = map[string]bool{
	"internal/artifact/digesttree.go": true,
	"internal/contract/set.go":        true,
	// These two unreleased legacy-profile adapters are explicitly grandfathered
	// by P5 §5 until P6 replaces their orchestration with internal/contract.
	"internal/cli/cmd_contract.go":     true,
	"internal/mcp/tools_contract.go":   true,
}

var combineDigestCallCeiling = map[string]int{
	"internal/artifact/digesttree.go": 1,
	"internal/contract/set.go":        3,
	"internal/cli/cmd_contract.go":     1,
	"internal/mcp/tools_contract.go":   1,
}

var legacySubtreeAllowlist = map[string]bool{
	"internal/cli/cmd_contract.go":   true,
	"internal/mcp/tools_contract.go": true,
}

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
}

func (c *checker) checkCoreVocabulary() {
	fset := token.NewFileSet()
	rel := "internal/contract/types.go"
	file, err := parser.ParseFile(fset, filepath.Join(c.root, filepath.FromSlash(rel)), nil, parser.AllErrors)
	if err != nil {
		c.add("parse %s: %v", rel, err)
		return
	}
	roles := typedStringConstants(file, "Role")
	if !reflect.DeepEqual(roles, canonicalRoles) {
		c.add("internal/contract Role constants = %v, want %v", roles, canonicalRoles)
	}
	profiles := typedStringConstants(file, "DigestProfile")
	if !reflect.DeepEqual(profiles, allProfiles) {
		c.add("internal/contract DigestProfile constants = %v, want %v", profiles, allProfiles)
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

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func (c *checker) add(format string, args ...any) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}
GO

go run "$ANALYZER_DIR/main.go" "$ROOT"
