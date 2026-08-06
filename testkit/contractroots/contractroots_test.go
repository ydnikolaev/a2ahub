package contractroots

import (
	"strings"
	"testing"
)

// The shipped schema is the subject every consumer actually derives from, so it
// gets its own row: the extractor must produce a usable, complete result from
// the real document, not merely from documents this file wrote.
func TestDeclaredReadsTheShippedSchema(t *testing.T) {
	t.Parallel()
	roots, roles := Declared(t)
	if len(roots) == 0 || len(roles) == 0 {
		t.Fatalf("Declared() = %d roots, %d roles", len(roots), len(roles))
	}
	seen := map[string]bool{}
	for _, r := range roots {
		if r.Root == "" || strings.HasSuffix(r.Root, "/") {
			t.Errorf("root %q is empty or carries a trailing slash", r.Root)
		}
		if len(r.Roles) == 0 {
			t.Errorf("root %q constrains no role", r.Root)
		}
		for _, role := range r.Roles {
			if seen[role] {
				t.Errorf("role %q is constrained to more than one root: the map is not a function", role)
			}
			seen[role] = true
		}
	}
	for _, role := range roles {
		if !seen[role] {
			t.Errorf("declared role %q has no root, which Declared should have refused", role)
		}
	}
}

// Every refusal below is a vacuity guard, and a vacuity guard nobody exercises
// is one nobody has checked — which is the exact failure this package was
// written after (a gate that yielded nothing and reported findings anyway).
func TestDeclareRefusesEveryShapeItDoesNotUnderstand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"not json", `{`, "parse"},
		{"no artifactEntry", `{"$defs":{"other":{}}}`, "wrong subject"},
		{
			"no role enum",
			`{"$defs":{"artifactEntry":{"properties":{"role":{}},"allOf":[{}]}}}`,
			"no role enum",
		},
		{
			"no conditionals",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema"]}},"allOf":[]}}}`,
			"no allOf conditionals",
		},
		{
			"every arm skipped",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema"]}},
			  "allOf":[{"if":{"properties":{"role":{"const":"schema"}}},"then":{"properties":{"conforms_to":{"pattern":"^schema/"}}}}]}}}`,
			"derived no path roots",
		},
		{
			"path arm names no role",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema"]}},
			  "allOf":[{"if":{"properties":{}},"then":{"properties":{"path":{"pattern":"^schema/[a-z]+"}}}}]}}}`,
			"names no role",
		},
		{
			"role with no path rule",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema","telemetry"]}},
			  "allOf":[{"if":{"properties":{"role":{"const":"schema"}}},"then":{"properties":{"path":{"pattern":"^schema/[a-z]+"}}}}]}}}`,
			"no path-root rule",
		},
		{
			"unanchored pattern",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema"]}},
			  "allOf":[{"if":{"properties":{"role":{"const":"schema"}}},"then":{"properties":{"path":{"pattern":"schema/x"}}}}]}}}`,
			"not anchored",
		},
		{
			"no literal directory prefix",
			`{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["schema"]}},
			  "allOf":[{"if":{"properties":{"role":{"const":"schema"}}},"then":{"properties":{"path":{"pattern":"^(schema|other)/x"}}}}]}}}`,
			"no literal directory prefix",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			roots, roles, err := declare([]byte(test.raw))
			if err == nil {
				t.Fatalf("declare() accepted the document and returned %d roots, %d roles", len(roots), len(roles))
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("declare() error = %q, want it to mention %q", err, test.want)
			}
			if roots != nil || roles != nil {
				t.Errorf("declare() returned a partial result alongside its error: a caller that ignores err must not get half a root list")
			}
		})
	}
}

// A const arm and an enum arm are the two shapes the schema uses to name roles,
// and both have to reach the same place.
func TestDeclareAcceptsBothRoleArmShapes(t *testing.T) {
	t.Parallel()
	raw := `{"$defs":{"artifactEntry":{"properties":{"role":{"enum":["one","two","three"]}},
	  "allOf":[
	    {"if":{"properties":{"role":{"const":"one"}}},"then":{"properties":{"path":{"pattern":"^alpha/[a-z]+"}}}},
	    {"if":{"properties":{"role":{"enum":["two","three"]}}},"then":{"properties":{"path":{"pattern":"^beta/nested/[a-z]+"}}}}
	  ]}}}`
	roots, roles, err := declare([]byte(raw))
	if err != nil {
		t.Fatalf("declare() error = %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("roles = %v", roles)
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %+v", roots)
	}
	if roots[0].Root != "alpha" || len(roots[0].Roles) != 1 || roots[0].Roles[0] != "one" {
		t.Errorf("const arm = %+v", roots[0])
	}
	if roots[1].Root != "beta/nested" || len(roots[1].Roles) != 2 {
		t.Errorf("enum arm = %+v", roots[1])
	}
}
