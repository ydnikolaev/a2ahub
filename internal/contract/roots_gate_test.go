package contract

import (
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/contractroots"
)

// P11 Tier B, gate B1 — the fourth surface with an opinion about the carried
// set's path roots.
//
// rootForRole and knownRole are a complete hand-written second copy of the map
// the envelope schema already states as role-conditional path patterns, and
// ExportSource partitions the same roles again to decide what
// export-source-v1's digest covers. The day the space layout and the sidecar
// collector disagreed with that schema (fb-20260806-c6ad38), THIS copy happened
// to be right — which is the whole problem with a copy: nothing was checking,
// so being right was luck.
//
// The derivation is testkit/contractroots, shared with internal/e2e's gate over
// the space layout and the sidecar collector, so one schema change reaches every
// surface or none of them. Nothing below names a root or a role literally.
func TestRoleRootMapMatchesTheDescriptorSchema(t *testing.T) {
	t.Parallel()
	roots, declaredRoles := contractroots.Declared(t)

	for _, r := range roots {
		for _, role := range r.Roles {
			got, ok := rootForRole(Role(role))
			if !ok {
				t.Errorf("rootForRole(%q) unknown, but the descriptor schema constrains it to %q/", role, r.Root)
				continue
			}
			if want := r.Root + "/"; got != want {
				t.Errorf("rootForRole(%q) = %q, schema requires %q", role, got, want)
			}
		}
	}

	// The reverse: a role this package accepts that the schema does not admit
	// would be declarable here and refused at validation, which is the same
	// disagreement pointed the other way.
	admitted := map[string]bool{}
	for _, role := range declaredRoles {
		admitted[role] = true
		if !knownRole(Role(role)) {
			t.Errorf("knownRole(%q) = false: the schema admits a role this package cannot place", role)
		}
	}
	if knownRole(Role("no-such-role")) {
		t.Error("knownRole accepted a role the schema does not admit: the vocabulary is no longer bounded")
	}

	// insideCarriedRoot is a third statement of the same boundary, by top-level
	// path segment. It must accept exactly the derived roots' top segments.
	tops := map[string]bool{}
	for _, r := range roots {
		top := r.Root
		if cut := strings.Index(top, "/"); cut >= 0 {
			top = top[:cut]
		}
		tops[top] = true
		if !insideCarriedRoot(top + "/probe.json") {
			t.Errorf("insideCarriedRoot(%q) = false: the schema places role(s) %v there", top+"/probe.json", r.Roles)
		}
	}
	if insideCarriedRoot("no-such-root/probe.json") {
		t.Error("insideCarriedRoot accepted a root the schema does not admit")
	}

	if len(tops) == 0 {
		t.Fatal("derived no top-level roots: the extractor yielded nothing usable")
	}
}

// TestExportSourcePartitionsEveryDeclaredRole pins the fifth surface: which
// roles export-source-v1's provenance digest covers.
//
// The profile is defined as declared schema and fixture entries only —
// descriptor and companions excluded — so a producer can reimplement it in their
// own generator (reference/contract-versions.md). Every role the schema admits
// therefore has to land on a definite side of that partition, and a role added
// later must not silently join the digest just because nothing said otherwise.
//
// It builds a set carrying EVERY declared role and asks ExportSource what it
// covered. Restating the predicate (`RoleSchema || fixtureRole(...)`) and
// comparing that to the derived roots was the first shape of this test, and it
// was worthless: deleting the filter from ExportSource left it green, because a
// copy of the rule agrees with the rule it copied no matter what production
// does.
func TestExportSourcePartitionsEveryDeclaredRole(t *testing.T) {
	t.Parallel()
	roots, declaredRoles := contractroots.Declared(t)

	rootOf := map[string]string{}
	for _, r := range roots {
		for _, role := range r.Roles {
			rootOf[role] = r.Root
		}
	}

	// Which side a role belongs on is decided by the root the SCHEMA gives it —
	// source data lives under the schema and fixture trees, everything else is a
	// companion — so a role added to the schema arrives here with its answer
	// already determined and no line in this file to update.
	isSource := func(role string) bool {
		root := rootOf[role]
		return root == "schema" || strings.HasPrefix(root, "fixtures/")
	}

	// One entry per declared role, placed at its own derived root. Construction
	// is likewise root-driven: the schema tree holds the one schema document,
	// the fixture trees hold instances that conform to it, and every other root
	// holds an advisory companion that may not claim conformance.
	schemaPath := ""
	for _, role := range declaredRoles {
		if rootOf[role] == "schema" {
			schemaPath = "schema/" + role + ".schema.json"
		}
	}
	if schemaPath == "" {
		t.Fatal("no declared role is placed under the schema root: cannot build a set to ask ExportSource about")
	}

	descriptor := Descriptor{SchemaFormat: "json-schema-2020-12"}
	candidates := []CandidateFile{}
	wantCovered := map[string]bool{}
	for _, role := range declaredRoles {
		root := rootOf[role]
		entry := Entry{Role: Role(role), Normative: true}
		switch {
		case root == "schema":
			entry.Path = schemaPath
			entry.MediaType = "application/schema+json"
		case strings.HasPrefix(root, "fixtures/"):
			entry.Path = root + "/" + role + ".json"
			entry.MediaType = "application/json"
			entry.ConformsTo = schemaPath
		default:
			entry.Path = root + "/" + role + ".md"
			entry.MediaType = "text/markdown"
			entry.Normative = false
		}
		descriptor.Artifacts = append(descriptor.Artifacts, entry)
		candidates = append(candidates, regular(entry.Path, `{"role":"`+role+`"}`))
		wantCovered[entry.Path] = isSource(role)
	}

	set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), descriptor, candidates)
	assertNoIssues(t, issues)
	export, err := set.ExportSource()
	if err != nil {
		t.Fatalf("ExportSource() error = %v", err)
	}

	var wrongfullyCovered, wrongfullyOmitted []string
	for path, want := range wantCovered {
		_, covered := export.PerFileDigest[path]
		switch {
		case covered && !want:
			wrongfullyCovered = append(wrongfullyCovered, path)
		case !covered && want:
			wrongfullyOmitted = append(wrongfullyOmitted, path)
		}
	}
	sort.Strings(wrongfullyCovered)
	sort.Strings(wrongfullyOmitted)
	if len(wrongfullyCovered) > 0 {
		t.Errorf("export-source-v1 covered companion entries %v: a producer recomputing the digest from their source tree would disagree with us", wrongfullyCovered)
	}
	if len(wrongfullyOmitted) > 0 {
		t.Errorf("export-source-v1 omitted source entries %v: the provenance assertion no longer covers the bytes it claims to", wrongfullyOmitted)
	}
	if _, covered := export.PerFileDigest[DescriptorPath]; covered {
		t.Error("export-source-v1 covered the descriptor, which asserts the digest — the assertion would be self-referential")
	}

	// Both sides must be non-empty, or the two checks above are vacuous.
	sourceCount := 0
	for _, want := range wantCovered {
		if want {
			sourceCount++
		}
	}
	if sourceCount == 0 || sourceCount == len(wantCovered) {
		t.Fatalf("the schema places %d of %d declared roles on the source side: this test proves nothing at that partition", sourceCount, len(wantCovered))
	}
}
