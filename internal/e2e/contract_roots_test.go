package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/testkit/contractroots"
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
// The derivation lives in testkit/contractroots, not here, for the same reason:
// internal/contract keeps a THIRD opinion about the same map (rootForRole) and
// gates it in its own package, so both consumers read one extractor and a schema
// change reaches both or neither.

const (
	rootsProbeSystem = "acme"
	rootsProbeSlug   = "widget"
)

// TestDeclaredContractRootsAreCreatedAndRecognised is B1 itself.
func TestDeclaredContractRootsAreCreatedAndRecognised(t *testing.T) {
	t.Parallel()
	roots, _ := contractroots.Declared(t)

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
			spacePath := sectionPrefix + r.Root + "/" + name
			want[spacePath] = false

			// Recognised: the reverse mapping must resolve this path to its own
			// contract and classify it as carried baseline rather than an
			// envelope artifact. This is the exact predicate the submit
			// validator's file dispatch consults.
			id, gotDescriptor, ok := space.ContractForPath(spacePath)
			if !ok {
				t.Errorf("ContractForPath(%q) not recognised, but the descriptor grammar requires role(s) %v to live there", spacePath, r.Roles)
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
	roots, _ := contractroots.Declared(t)

	admitted := map[string]bool{}
	for _, r := range roots {
		admitted[r.Root] = true
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
	roots, _ := contractroots.Declared(t)

	admitted := map[string]bool{}
	for _, r := range roots {
		admitted[r.Root] = true
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
