package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/schemas"
)

// P11 Tier B, gate B1 — a path root the descriptor grammar DECLARES must be a
// directory the space can create, recognise, and carry.
//
// This is the machine countermeasure for fb-20260806-c6ad38 (spec 11 §1 defect
// #9), the day's blocker. contract-set-v2's envelope schema requires every
// companion role — errors, vocabulary, limits, changelog, example, other — to
// live under artifacts/, and enforces it with a role-conditional path pattern,
// so a companion has no other legal location. Two OTHER surfaces of that same
// rule did not know the directory existed: space.ContractForPath refused every
// artifacts/** path, so `a2a submit`'s validator dispatched each companion as an
// envelope draft and `a2a contract publish` died parsing frontmatter out of
// JSON; and with no forward constructor, the sidecar collector had no
// artifacts/ arm either, so a declared companion was staged, declared, and then
// never carried. Every individual half was correct and individually tested.
//
// The whole point of the gate is therefore that it DERIVES its root list from
// the schema rather than restating it (spec 11 §3 I4). A hand-maintained copy of
// that list would be defect #2 of the same day — a gate compared against a fixed
// directory instead of against its subject — and would stay green through
// exactly the drift it exists to catch. Nothing below names a root literally;
// grep this file for "artifacts" and you will find it only in prose.
//
// The schema is read through schemas.FS, the embedded corpus that ships in the
// binary, and NOT through a repo-relative path. That is defect #8's class: a
// test that walks up to find a tree marker is a test that decides on ambient
// state. There is no root discovery here at all.

const (
	rootsProbeSystem = "acme"
	rootsProbeSlug   = "widget"
)

// declaredRoot is one role→path-root pair the envelope schema imposes.
type declaredRoot struct {
	root  string   // e.g. "schema", "fixtures/valid"
	roles []string // the roles that arm constrains
}

// contractRootsFromSchema extracts the role→path-root pairs from
// envelope/v2's artifactEntry definition, and reports the full set of roles the
// entry's own enum admits so the caller can check the two agree.
//
// It fails the test rather than returning an empty result on every shape it does
// not understand. A structural extractor that quietly yields nothing is the
// most expensive kind of green: `check-operational-confidence.sh` shipped
// without a vacuity guard the same day and produced twenty-seven confident wrong
// findings about a file that was present and correct.
func contractRootsFromSchema(t *testing.T) (roots []declaredRoot, declaredRoles []string) {
	t.Helper()

	raw, err := schemas.FS.ReadFile("envelope/v2/contract.schema.json")
	if err != nil {
		t.Fatalf("read embedded contract descriptor schema: %v", err)
	}

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
		t.Fatalf("parse embedded contract descriptor schema: %v", err)
	}

	// Scope the walk to $defs/artifactEntry explicitly. The document ALSO
	// carries role conditionals at its top level (the inventory-mode
	// constraints), and silently folding those in would make the derived set
	// depend on which block happened to be walked.
	entry, ok := doc.Defs["artifactEntry"]
	if !ok {
		t.Fatal("envelope/v2 contract schema has no $defs/artifactEntry: the extractor is reading the wrong subject, which is the defect class this gate guards")
	}
	declaredRoles = append([]string(nil), entry.Properties.Role.Enum...)
	if len(declaredRoles) == 0 {
		t.Fatal("$defs/artifactEntry declares no role enum: nothing to derive roots for")
	}
	if len(entry.AllOf) == 0 {
		t.Fatal("$defs/artifactEntry has no allOf conditionals: no role→path rule to derive")
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
		armRoles := arm.If.Properties.Role.Enum
		if c := arm.If.Properties.Role.Const; c != "" {
			armRoles = append(armRoles, c)
		}
		if len(armRoles) == 0 {
			t.Fatalf("$defs/artifactEntry/allOf[%d] constrains path but names no role", i)
		}
		roots = append(roots, declaredRoot{
			root:  rootFromPathPattern(t, i, arm.Then.Properties.Path.Pattern),
			roles: armRoles,
		})
	}
	if len(roots) == 0 {
		t.Fatalf("derived no path roots from %d allOf arms (%d carried no path pattern): the extractor no longer understands the schema's shape", len(entry.AllOf), skipped)
	}

	// Completeness, derived rather than counted: every role the entry admits
	// must be constrained to a root by some arm. A role added with no path rule
	// is sub-class (B) itself — a declared structure with no home — and this is
	// the only check here that catches it before an author uses it.
	constrained := map[string]bool{}
	for _, r := range roots {
		for _, role := range r.roles {
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
		t.Fatalf("roles admitted by $defs/artifactEntry with no path-root rule: %v — a declared role with no legal directory has no publishable shape", uncovered)
	}
	return roots, declaredRoles
}

// rootFromPathPattern reduces an anchored path pattern to the literal directory
// prefix it pins — "^schema/(?:…" to "schema", "^fixtures/valid/(?:…" to
// "fixtures/valid".
func rootFromPathPattern(t *testing.T, arm int, pattern string) string {
	t.Helper()
	if !strings.HasPrefix(pattern, "^") {
		t.Fatalf("allOf[%d] path pattern %q is not anchored: it does not pin a root at all", arm, pattern)
	}
	literal := strings.TrimPrefix(pattern, "^")
	if cut := strings.IndexAny(literal, `(){}[]|*+?.\$^`); cut >= 0 {
		literal = literal[:cut]
	}
	if !strings.HasSuffix(literal, "/") {
		t.Fatalf("allOf[%d] path pattern %q has no literal directory prefix: derived %q", arm, pattern, literal)
	}
	return strings.TrimSuffix(literal, "/")
}

// TestDeclaredContractRootsAreCreatedAndRecognised is B1 itself.
func TestDeclaredContractRootsAreCreatedAndRecognised(t *testing.T) {
	t.Parallel()
	roots, _ := contractRootsFromSchema(t)

	layout, err := space.NewLayout(rootsProbeSystem)
	if err != nil {
		t.Fatalf("NewLayout(%q) error = %v", rootsProbeSystem, err)
	}
	descriptor := layout.ProvidesContract(rootsProbeSlug)
	sectionPrefix := rootsProbeSystem + "/provides/" + rootsProbeSlug + "/"

	// A staging tree carrying one probe under every derived root. Two probes per
	// root, one JSON and one Markdown: the reported failure was a companion
	// being frontmatter-parsed because it was not classified as carried
	// baseline data, and a later extension branch anywhere on this path would
	// resurrect exactly that for the roles (changelog, example) whose natural
	// form is prose.
	staging := t.TempDir()
	want := map[string]bool{}
	for _, r := range roots {
		for _, name := range []string{"probe.json", "PROBE.md"} {
			spacePath := sectionPrefix + r.root + "/" + name
			want[spacePath] = false

			// Recognised: the reverse mapping must resolve this path to its own
			// contract and classify it as carried baseline rather than an
			// envelope artifact. This is the exact predicate the submit
			// validator's file dispatch consults.
			id, gotDescriptor, ok := space.ContractForPath(spacePath)
			if !ok {
				t.Errorf("ContractForPath(%q) not recognised, but the descriptor grammar requires role(s) %v to live there", spacePath, r.roles)
				continue
			}
			if gotDescriptor != descriptor {
				t.Errorf("ContractForPath(%q) descriptor = %q, want %q", spacePath, gotDescriptor, descriptor)
			}
			if id == "" {
				t.Errorf("ContractForPath(%q) returned an empty contract id", spacePath)
			}
			if !space.IsContractBaselinePath(spacePath) {
				t.Errorf("IsContractBaselinePath(%q) = false: it would be dispatched as an envelope draft and parsed for frontmatter", spacePath)
			}

			local := filepath.Join(staging, filepath.FromSlash(spacePath))
			if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				t.Fatalf("MkdirAll(%q) error = %v", local, err)
			}
			if err := os.WriteFile(local, []byte("{}\n"), 0o644); err != nil {
				t.Fatalf("WriteFile(%q) error = %v", local, err)
			}
		}
	}

	// Created and carried: a forward constructor that exists but is not wired
	// into the collector is the half of fb-20260806-c6ad38 that silently
	// dropped files. Asserting the collector's output rather than the
	// constructor's return value is what makes this a wiring claim.
	carried, err := template.ContractSidecarsFromStaging(staging, rootsProbeSystem, rootsProbeSlug)
	if err != nil {
		t.Fatalf("ContractSidecarsFromStaging error = %v", err)
	}
	for _, write := range carried {
		if _, expected := want[write.Path]; !expected {
			t.Errorf("collector carried %q, which no derived root accounts for", write.Path)
			continue
		}
		want[write.Path] = true
	}
	var dropped []string
	for spacePath, seen := range want {
		if !seen {
			dropped = append(dropped, spacePath)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		t.Errorf("declared roots the sidecar collector does not carry: %v — a descriptor would name files the space never receives", dropped)
	}
}

// TestContractPathRootsRefuseWhatTheGrammarDoesNotAdmit is B1's other
// direction. Widening ContractForPath until everything is "contract baseline
// data" would pass the test above and destroy the classification it exists to
// protect.
func TestContractPathRootsRefuseWhatTheGrammarDoesNotAdmit(t *testing.T) {
	t.Parallel()
	roots, _ := contractRootsFromSchema(t)

	admitted := map[string]bool{}
	for _, r := range roots {
		admitted[r.root] = true
	}

	// Derived near-misses: the parent of every multi-segment root that is not
	// itself a root. This yields the reported case — a fixture-suite manifest
	// at fixtures/manifest.json, which has no declarable role and must NOT be
	// silently accepted as carried data (fb-20260806-2af06c).
	var probes []string
	for root := range admitted {
		for {
			cut := strings.LastIndex(root, "/")
			if cut < 0 {
				break
			}
			root = root[:cut]
			if !admitted[root] {
				probes = append(probes, root+"/manifest.json")
			}
		}
	}
	if len(probes) == 0 {
		t.Fatal("derived no near-miss probes: every declared root is a single segment, so this negative check proves nothing and needs rewriting")
	}

	sectionPrefix := rootsProbeSystem + "/provides/" + rootsProbeSlug + "/"
	for _, probe := range probes {
		spacePath := sectionPrefix + probe
		if _, _, ok := space.ContractForPath(spacePath); ok {
			t.Errorf("ContractForPath(%q) accepted a path the descriptor grammar gives no role: the roots are no longer a bounded set", spacePath)
		}
	}
}

// TestContractRootConstructorsMatchTheGrammar closes the loop the other way: a
// forward constructor for a directory the schema does not admit would create a
// place authors put files that can never be declared.
//
// The discriminator is the Provides…Dir naming convention over Layout's method
// set, which reflection can read but cannot enforce — a root constructor named
// something else is invisible here. That residue is stated rather than hidden;
// the assertion above (schema roots ⇒ carried) is the load-bearing direction,
// and this one only stops the set from growing silently.
func TestContractRootConstructorsMatchTheGrammar(t *testing.T) {
	t.Parallel()
	roots, _ := contractRootsFromSchema(t)

	admitted := map[string]bool{}
	for _, r := range roots {
		admitted[r.root] = true
	}

	layout, err := space.NewLayout(rootsProbeSystem)
	if err != nil {
		t.Fatalf("NewLayout(%q) error = %v", rootsProbeSystem, err)
	}
	sectionPrefix := rootsProbeSystem + "/provides/" + rootsProbeSlug + "/"

	value := reflect.ValueOf(layout)
	stringType := reflect.TypeOf("")
	found := 0
	for i := 0; i < value.NumMethod(); i++ {
		name := value.Type().Method(i).Name
		if !strings.HasPrefix(name, "Provides") || !strings.HasSuffix(name, "Dir") {
			continue
		}
		method := value.Method(i)
		if method.Type().NumIn() != 1 || method.Type().In(0) != stringType ||
			method.Type().NumOut() != 1 || method.Type().Out(0) != stringType {
			continue
		}
		found++
		got := method.Call([]reflect.Value{reflect.ValueOf(rootsProbeSlug)})[0].String()
		root, ok := strings.CutPrefix(got, sectionPrefix)
		if !ok {
			t.Errorf("Layout.%s(%q) = %q, which is not inside the contract's own section %q", name, rootsProbeSlug, got, sectionPrefix)
			continue
		}
		if !admitted[root] {
			t.Errorf("Layout.%s constructs root %q, which the descriptor grammar admits no role for: authors can fill a directory nothing can declare", name, root)
		}
	}
	if found != len(admitted) {
		t.Errorf("found %d Provides…Dir constructors for %d roots the grammar declares: every declared root needs one, and reflection sees only this naming convention", found, len(admitted))
	}
}
