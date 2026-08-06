package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

// The staging half of the D-D scaffold moved here from internal/cli so
// BOTH surfaces could call it — internal/mcp may not import internal/cli,
// so a helper left there would have meant a second implementation, which
// is exactly what the CLI/MCP parity suite exists to forbid. These tests
// moved with it.

func TestScaffoldContractInStagingWritesAllFilesAtTheLayoutShape(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()

	written, err := ScaffoldContractInStaging(staging, "axon", "widget", os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractInStaging: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("written = %v, want the schema and both fixtures", written)
	}

	// The paths must match what a later publish and `submit`'s sidecar
	// carry both look for — the space layout's own shape, rooted at
	// staging. A scaffold at any other path is invisible to both.
	wantSchema := filepath.Join(staging, "axon", "provides", "widget", "schema", "widget.schema.json")
	wantFixture := filepath.Join(staging, "axon", "provides", "widget", "fixtures", "valid", "widget.json")
	wantInvalidFixture := filepath.Join(staging, "axon", "provides", "widget", "fixtures", "invalid", "widget.json")
	for _, want := range []string{wantSchema, wantFixture, wantInvalidFixture} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected a scaffolded file at %s: %v", want, err)
		}
	}

	// D-E's mapping is by file stem, so the two names must share one.
	if filepath.Base(wantFixture) != "widget.json" || !strings.HasPrefix(filepath.Base(wantSchema), "widget.") {
		t.Fatalf("scaffold names break D-E's stem mapping: %s / %s", wantSchema, wantFixture)
	}
}

func TestScaffoldContractCandidateInStagingWritesDescriptorAndSidecars(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	draft := []byte("---\nschema: envelope/v2\nid: XC-axon-widget\n---\nbody\n")

	written, err := ScaffoldContractCandidateInStaging(staging, "axon", "widget", draft, os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractCandidateInStaging: %v", err)
	}
	if len(written) != 4 {
		t.Fatalf("written = %v, want descriptor plus three sidecars", written)
	}
	descriptor := filepath.Join(staging, "axon", "provides", "widget", "contract.md")
	raw, err := os.ReadFile(descriptor)
	if err != nil {
		t.Fatalf("read staged descriptor: %v", err)
	}
	if string(raw) != string(draft) {
		t.Fatalf("staged descriptor = %q, want exact rendered draft", raw)
	}

	authored := []byte("authored descriptor")
	if err := os.WriteFile(descriptor, authored, 0o644); err != nil {
		t.Fatal(err)
	}
	written, err = ScaffoldContractCandidateInStaging(staging, "axon", "widget", []byte("replacement"), os.WriteFile)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("rerun wrote over existing candidate files: %v", written)
	}
	raw, err = os.ReadFile(descriptor)
	if err != nil || string(raw) != string(authored) {
		t.Fatalf("rerun changed authored descriptor: %q err=%v", raw, err)
	}
}

func TestScaffoldContractInStagingNeverOverwrites(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()

	schemaPath := filepath.Join(staging, "axon", "provides", "widget", "schema", "widget.schema.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const authored = `{"$comment":"the author's own work"}`
	if err := os.WriteFile(schemaPath, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := ScaffoldContractInStaging(staging, "axon", "widget", os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractInStaging: %v", err)
	}
	for _, p := range written {
		if p == schemaPath {
			t.Fatalf("re-running the scaffold reported writing over an existing file: %s", p)
		}
	}
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != authored {
		t.Fatalf("the author's schema was clobbered: %s", raw)
	}
	// Both fixtures were absent, so they still get written.
	if len(written) != 2 {
		t.Fatalf("written = %v, want only the two missing fixtures", written)
	}
}

func TestScaffoldContractInStagingRefusesAnUnusableSystem(t *testing.T) {
	t.Parallel()
	if _, err := ScaffoldContractInStaging(t.TempDir(), "", "widget", os.WriteFile); err == nil {
		t.Fatal("expected an error for a system the layout cannot be built for")
	}
}

func TestScaffoldContractInStagingSurfacesAWriteFailure(t *testing.T) {
	t.Parallel()
	boom := func(string, []byte, os.FileMode) error { return os.ErrPermission }
	written, err := ScaffoldContractInStaging(t.TempDir(), "axon", "widget", boom)
	if err == nil {
		t.Fatal("expected the write error to surface, not be swallowed")
	}
	if len(written) != 0 {
		t.Fatalf("nothing was written, so written must be empty; got %v", written)
	}
}

// --- ContractSidecarsFromStaging (P37 Wave I: the shared collector both
// `a2a submit` and `a2a contract publish` read staged schema/fixtures
// back through — moved here from internal/cli's own submitContractSidecars,
// same behaviour, same refusals) --------------------------------------------

func TestContractSidecarsFromStagingCollectsNestedFilesSorted(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(staging, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("axon/provides/widget/schema/main.schema.json", `{"type":"object"}`)
	write("axon/provides/widget/schema/common/types.schema.json", `{"type":"string"}`)
	write("axon/provides/widget/fixtures/valid/ok.json", `{}`)
	write("axon/provides/widget/fixtures/invalid/bad.json", `null`)

	got, err := ContractSidecarsFromStaging(staging, "axon", "widget")
	if err != nil {
		t.Fatalf("ContractSidecarsFromStaging: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d files, want 4: %+v", len(got), got)
	}
	wantPaths := map[string]string{
		"axon/provides/widget/schema/main.schema.json":         `{"type":"object"}`,
		"axon/provides/widget/schema/common/types.schema.json": `{"type":"string"}`,
		"axon/provides/widget/fixtures/valid/ok.json":          `{}`,
		"axon/provides/widget/fixtures/invalid/bad.json":       `null`,
	}
	for _, f := range got {
		want, ok := wantPaths[f.Path]
		if !ok {
			t.Fatalf("unexpected path %s in %+v", f.Path, got)
		}
		if string(f.Content) != want {
			t.Fatalf("%s content = %q, want %q", f.Path, f.Content, want)
		}
	}
}

func TestContractSidecarsFromStagingAbsentIsNilNotError(t *testing.T) {
	t.Parallel()
	got, err := ContractSidecarsFromStaging(t.TempDir(), "axon", "widget")
	if err != nil {
		t.Fatalf("ContractSidecarsFromStaging: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no sidecars for a contract nothing was staged for, got %+v", got)
	}
}

func TestContractSidecarsFromStagingRefusesSymlink(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	schemaDir := filepath.Join(staging, "axon", "provides", "widget", "schema")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(schemaDir, "widget.schema.json")); err != nil {
		t.Fatal(err)
	}
	_, err := ContractSidecarsFromStaging(staging, "axon", "widget")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected a symlink refusal, got %v", err)
	}
}

func TestContractSidecarsFromStagingRefusesAnUnusableSystem(t *testing.T) {
	t.Parallel()
	if _, err := ContractSidecarsFromStaging(t.TempDir(), "", "widget"); err == nil {
		t.Fatal("expected an error for a system the layout cannot be built for")
	}
}

func TestContractDraftSchemaFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		draft   string
		want    string
		wantErr bool
	}{
		{
			name:  "reads what the RENDERED draft declares, not the template default",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\nschema_format: proto3\n---\nbody\n",
			want:  "proto3",
		},
		{
			name:  "the JSON-Schema dialect the scaffold is gated on",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\nschema_format: json-schema-2020-12\n---\nbody\n",
			want:  "json-schema-2020-12",
		},
		{
			name:  "an absent schema_format is empty, not an error",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\n---\nbody\n",
			want:  "",
		},
		{
			name:    "no frontmatter at all is an error, never a silent empty",
			draft:   "just a body\n",
			wantErr: true,
		},
		{
			name:    "malformed YAML is an error",
			draft:   "---\nschema_format: [unclosed\n---\nbody\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ContractDraftSchemaFormat([]byte(tc.draft))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ContractDraftSchemaFormat: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFreshContractDeclaresExactlyWhatItScaffolds is the composition check the
// reported defect was actually made of. Each piece looked right on its own:
// the v2 template declares three artifact paths, the scaffold writes three
// files, and nothing compared the two lists. A single mismatched path makes a
// FRESH contract — one nobody hand-edited — refused at publication as
// "staged file is removed or undeclared", which reads as an authoring mistake
// and is not one.
func TestFreshContractDeclaresExactlyWhatItScaffolds(t *testing.T) {
	t.Parallel()

	const slug = "probe-feed"
	draft, err := RenderNew(Input{
		Type:    "contract",
		ID:      "XC-axon-" + slug,
		Actor:   Actor{Kind: "agent", Name: "test-bot"},
		Created: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	}, func(f string) bool { return strings.HasPrefix(f, "json-schema") })
	if err != nil {
		t.Fatalf("RenderNew(contract): %v", err)
	}

	var descriptor struct {
		Schema    string `yaml:"schema"`
		Artifacts []struct {
			Path       string `yaml:"path"`
			Role       string `yaml:"role"`
			ConformsTo string `yaml:"conforms_to"`
		} `yaml:"artifacts"`
	}
	fm, err := artifact.ParseFrontmatter(draft)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if err := yaml.Unmarshal(fm.YAML, &descriptor); err != nil {
		t.Fatalf("unmarshal descriptor: %v", err)
	}
	if descriptor.Schema != "envelope/v2" {
		t.Fatalf("a fresh JSON-Schema contract renders %q, want envelope/v2", descriptor.Schema)
	}

	staging := t.TempDir()
	written, err := ScaffoldContractCandidateInStaging(staging, "axon", slug, draft, os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractCandidateInStaging: %v", err)
	}

	// The candidate root is <staging>/axon/provides/<slug>/; the declared
	// paths are relative to it, which is exactly how the publication planner
	// resolves them.
	root := filepath.Join(staging, "axon", "provides", slug)
	scaffolded := map[string]bool{}
	for _, p := range written {
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			t.Fatalf("scaffolded %s is outside the candidate root %s", p, root)
		}
		rel = filepath.ToSlash(rel)
		if rel == "contract.md" {
			continue // the descriptor is implicit and must not be declared
		}
		scaffolded[rel] = true
	}

	declared := map[string]bool{}
	for _, entry := range descriptor.Artifacts {
		declared[entry.Path] = true
		if _, ok := scaffolded[entry.Path]; !ok {
			t.Errorf("the descriptor declares %q but the scaffold writes no such file — publication refuses a declared path with no bytes", entry.Path)
		}
		// A fixture's conforms_to must name a DECLARED schema entry, not just
		// a file that happens to exist.
		if entry.Role == "valid-fixture" || entry.Role == "invalid-fixture" {
			if entry.ConformsTo == "" {
				t.Errorf("fixture entry %q declares no conforms_to", entry.Path)
			}
		}
	}
	for path := range scaffolded {
		if !declared[path] {
			t.Errorf("the scaffold writes %q but the descriptor declares it nowhere — publication refuses an undeclared staged file", path)
		}
	}
	for _, entry := range descriptor.Artifacts {
		if entry.ConformsTo != "" && !declared[entry.ConformsTo] {
			t.Errorf("entry %q conforms_to %q, which is not a declared entry", entry.Path, entry.ConformsTo)
		}
	}
	if len(declared) == 0 {
		t.Fatal("the fresh descriptor declared nothing — this test would be guarding nothing")
	}
}

// TestContractSidecarsCarryTheCompanionRoot: a declared companion must actually
// travel. contract-set-v2 gives companion roles exactly one legal directory,
// and for a while the collector `a2a submit` uses did not list it — so a
// descriptor could name a file the space never received, and publish died on
// it later (fb-20260806-c6ad38).
func TestContractSidecarsCarryTheCompanionRoot(t *testing.T) {
	t.Parallel()

	const slug = "widget"
	staging := t.TempDir()
	dir := filepath.Join(staging, "axon", "provides", slug)
	for rel, body := range map[string]string{
		filepath.Join("schema", slug+".schema.json"):    `{"type":"object"}`,
		filepath.Join("fixtures", "valid", "ok.json"):   `{}`,
		filepath.Join("fixtures", "invalid", "no.json"): `null`,
		filepath.Join("artifacts", "errors.yaml"):       "codes: []\n",
		filepath.Join("artifacts", "nested", "cl.md"):   "# changelog\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	carried, err := ContractSidecarsFromStaging(staging, "axon", slug)
	if err != nil {
		t.Fatalf("ContractSidecarsFromStaging: %v", err)
	}
	got := map[string]bool{}
	for _, w := range carried {
		got[w.Path] = true
	}
	for _, want := range []string{
		"axon/provides/widget/schema/widget.schema.json",
		"axon/provides/widget/fixtures/valid/ok.json",
		"axon/provides/widget/fixtures/invalid/no.json",
		"axon/provides/widget/artifacts/errors.yaml",
		"axon/provides/widget/artifacts/nested/cl.md",
	} {
		if !got[want] {
			t.Errorf("staged sidecar %q was not carried; got %v", want, carried)
		}
	}
}
