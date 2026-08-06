// Package contractroots is the SSOT for "which path root does a contract
// artifact role live under", derived from the envelope/v2 descriptor schema
// itself rather than restated in Go.
//
// It exists because the answer was written down independently in four places
// and one of them was wrong. contract-set-v2 requires every companion role —
// errors, vocabulary, limits, changelog, example, other — to live under
// artifacts/, and the schema enforces it with a role-conditional path pattern.
// internal/space.ContractForPath did not know the directory existed;
// internal/template's sidecar collector had no arm for it; and
// internal/contract.rootForRole is a complete hand-written second copy of the
// same map. The first two disagreeing with the schema is fb-20260806-c6ad38:
// `a2a submit` dispatched a declared companion as an envelope draft and
// `a2a contract publish` died parsing frontmatter out of JSON, while
// `a2a validate` and `a2a contract preflight` both passed.
//
// P11 spec §3 I4 is the reason this is a package and not a helper inside one
// test: a gate that keeps its own copy of the list is the defect it guards. Any
// surface with an opinion about the roots consumes THIS, so a schema change
// reaches every one of them or none.
//
// The schema is read through the embedded corpus (schemas.FS), never a
// repo-relative path — a test that walks up to find a tree marker decides on
// ambient state, which is the other half of the same day's findings.
package contractroots

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/schemas"
)

// SchemaPath is the embedded descriptor schema every consumer derives from.
const SchemaPath = "envelope/v2/contract.schema.json"

// Root is one role→path-root rule the descriptor schema imposes. Root carries
// no trailing slash ("schema", "fixtures/valid"); Roles are every role the
// arm constrains, in schema order.
type Root struct {
	Root  string
	Roles []string
}

// Declared returns the role→root rules the schema imposes, plus every role its
// artifactEntry enum admits, so a consumer can check the two agree.
//
// It fails the test on every shape it does not understand rather than returning
// an empty result. A structural extractor that quietly yields nothing is the
// most expensive kind of green: `check-operational-confidence.sh` shipped
// without a vacuity guard and produced twenty-seven confident, specific, wrong
// findings about a file that was present and correct.
func Declared(t *testing.T) (roots []Root, declaredRoles []string) {
	t.Helper()
	raw, err := schemas.FS.ReadFile(SchemaPath)
	if err != nil {
		t.Fatalf("read embedded %s: %v", SchemaPath, err)
	}
	roots, declaredRoles, err = declare(raw)
	if err != nil {
		t.Fatalf("%s: %v", SchemaPath, err)
	}
	return roots, declaredRoles
}

// declare is Declared without the testing dependency, so every refusal below is
// reachable from a unit test with a crafted document rather than only by
// mutating the shipped schema. A vacuity guard nobody can exercise is a vacuity
// guard nobody has checked.
func declare(raw []byte) (roots []Root, declaredRoles []string, err error) {
	var doc struct {
		Defs map[string]struct {
			Properties struct {
				Role struct {
					Enum []string `json:"enum"`
				} `json:"role"`
			} `json:"properties"`
			AllOf []struct {
				If struct {
					Properties struct {
						Role struct {
							Const string   `json:"const"`
							Enum  []string `json:"enum"`
						} `json:"role"`
					} `json:"properties"`
				} `json:"if"`
				Then struct {
					Properties struct {
						Path struct {
							Pattern string `json:"pattern"`
						} `json:"path"`
					} `json:"properties"`
				} `json:"then"`
			} `json:"allOf"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	// Scope the walk to $defs/artifactEntry explicitly. The document ALSO
	// carries role conditionals at its top level (the inventory-mode
	// constraints), and silently folding those in would make the derived set
	// depend on which block happened to be walked.
	entry, ok := doc.Defs["artifactEntry"]
	if !ok {
		return nil, nil, errors.New("no $defs/artifactEntry: the extractor is reading the wrong subject, which is the defect class it guards")
	}
	declaredRoles = append([]string(nil), entry.Properties.Role.Enum...)
	if len(declaredRoles) == 0 {
		return nil, nil, errors.New("$defs/artifactEntry declares no role enum: nothing to derive roots for")
	}
	if len(entry.AllOf) == 0 {
		return nil, nil, errors.New("$defs/artifactEntry has no allOf conditionals: no role→path rule to derive")
	}

	skipped := 0
	for i, arm := range entry.AllOf {
		// Arms that constrain something other than path (conforms_to, for the
		// two fixture roles) are legitimately not root rules. Count them, so
		// "every arm was skipped" cannot read as a pass.
		if arm.Then.Properties.Path.Pattern == "" {
			skipped++
			continue
		}
		armRoles := append([]string(nil), arm.If.Properties.Role.Enum...)
		if c := arm.If.Properties.Role.Const; c != "" {
			armRoles = append(armRoles, c)
		}
		if len(armRoles) == 0 {
			return nil, nil, fmt.Errorf("$defs/artifactEntry/allOf[%d] constrains path but names no role", i)
		}
		root, err := rootFromPathPattern(i, arm.Then.Properties.Path.Pattern)
		if err != nil {
			return nil, nil, err
		}
		roots = append(roots, Root{Root: root, Roles: armRoles})
	}
	if len(roots) == 0 {
		return nil, nil, fmt.Errorf("derived no path roots from %d allOf arms (%d carried no path pattern): the extractor no longer understands the schema's shape", len(entry.AllOf), skipped)
	}

	// Completeness, derived rather than counted: every role the entry admits
	// must be constrained to a root by some arm. A role added with no path rule
	// is a declared structure with no home, which is the sub-class this whole
	// gate family exists for, and this is the only check that catches it before
	// an author uses the role.
	constrained := map[string]bool{}
	for _, r := range roots {
		for _, role := range r.Roles {
			constrained[role] = true
		}
	}
	var uncovered []string
	for _, role := range declaredRoles {
		if !constrained[role] {
			uncovered = append(uncovered, role)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		return nil, nil, fmt.Errorf("roles admitted by $defs/artifactEntry with no path-root rule: %v — a declared role with no legal directory has no publishable shape", uncovered)
	}
	return roots, declaredRoles, nil
}

// rootFromPathPattern reduces an anchored path pattern to the literal directory
// prefix it pins — "^schema/(?:…" to "schema", "^fixtures/valid/(?:…" to
// "fixtures/valid".
func rootFromPathPattern(arm int, pattern string) (string, error) {
	if !strings.HasPrefix(pattern, "^") {
		return "", fmt.Errorf("allOf[%d] path pattern %q is not anchored: it does not pin a root at all", arm, pattern)
	}
	literal := strings.TrimPrefix(pattern, "^")
	if cut := strings.IndexAny(literal, `(){}[]|*+?.\$^`); cut >= 0 {
		literal = literal[:cut]
	}
	if !strings.HasSuffix(literal, "/") {
		return "", fmt.Errorf("allOf[%d] path pattern %q has no literal directory prefix: derived %q", arm, pattern, literal)
	}
	return strings.TrimSuffix(literal, "/"), nil
}
