package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

func fixedActorResolver(cli.ActorFlags) template.Actor {
	return template.Actor{Kind: "agent", Name: "test-bot", Model: "test-model"}
}

// TestNewDraftsEveryTypeV1Valid is AC-401.1, the real cli-layer
// integration: for every type in the P2 corpus, `a2a new <type>` with
// placeholder-only fills (plus --slug for the two standing types) then
// `a2a validate` on the drafted file returns V1-pass — driven against
// the real validate.Engine (schema.Load), not a fake.
func TestNewDraftsEveryTypeV1Valid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	for _, typ := range schema.EnvelopeTypes() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			stagingDir := filepath.Join(t.TempDir(), "staging")
			cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

			args := []string{typ}
			if typ == "contract" || typ == "requirement" {
				args = append(args, "--slug", "ingest")
			}

			io, out, errOut := newIO()
			code := cmd.Run(context.Background(), args, io)
			if code != 0 {
				t.Fatalf("new %s: code = %d; stdout=%s stderr=%s", typ, code, out.String(), errOut.String())
			}

			entries, err := os.ReadDir(stagingDir)
			if err != nil {
				t.Fatalf("ReadDir(%s): %v", stagingDir, err)
			}
			// D-D: `contract new` additionally scaffolds a starter
			// schema/fixture under an <system>/ subtree next to the
			// staged draft (spec 37 §2 T1), so stagingDir's top level
			// carries a second entry only for the contract type.
			wantEntries := 1
			if typ == "contract" {
				wantEntries = 2
			}
			if len(entries) != wantEntries {
				t.Fatalf("expected %d staged top-level entries, got %d (%v)", wantEntries, len(entries), entries)
			}

			var draftName string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".md") {
					draftName = e.Name()
				}
			}
			if draftName == "" {
				t.Fatalf("no staged .md draft found among entries: %v", entries)
			}

			raw, err := os.ReadFile(filepath.Join(stagingDir, draftName))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			result, err := engine.ValidateDraft(validate.Draft{Path: filepath.Join(stagingDir, draftName), Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if !result.Valid {
				t.Fatalf("draft for %s is V1-invalid: %+v\n---\n%s", typ, result.Violations, raw)
			}
		})
	}
}

func TestNewStandingTypeRequiresSlug(t *testing.T) {
	t.Parallel()
	cmd := cli.NewNewCommand(t.TempDir(), "axon", fixedActorResolver, nil)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"contract"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error, missing --slug)", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable stderr message")
	}
}

func TestNewUnknownType(t *testing.T) {
	t.Parallel()
	cmd := cli.NewNewCommand(t.TempDir(), "axon", fixedActorResolver, nil)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"bogus"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (unknown type)", code)
	}
}

func TestNewFieldOverrideAndBodyFile(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("custom body content\n"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"question", "--field", "category=defect", "--body-file", bodyFile}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, entries=%v", err, entries)
	}
	raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("category: defect")) {
		t.Fatalf("expected the --field override to land; got:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte("custom body content")) {
		t.Fatalf("expected the --body-file content to land; got:\n%s", raw)
	}
}

// TestNewMintsValidIDAndSectionsFromOwnSystem checks the minted id's
// <system> token matches the configured own system.
func TestNewMintsIDFromOwnSystem(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"question"}, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, %v", err, entries)
	}
	stem := entries[0].Name()[:len(entries[0].Name())-len(".md")]
	id, err := artifact.ParseID(stem)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", stem, err)
	}
	if id.System != "axon" {
		t.Fatalf("System = %q, want axon", id.System)
	}
}

// TestNewDefaultsSpaceFromSingleConnectedSpace is the regression test for
// the live-run defect: `a2a new <type>` used to leave `space: <space-id>`
// (the literal template placeholder) whenever --field space= was not
// given, breaking the very next `a2a submit`. With exactly one connected
// space, the drafted `space:` field now defaults to it; with zero or more
// than one, today's behavior (placeholder survives) is unchanged; an
// explicit --field space= always wins regardless of the connected-space
// count.
func TestNewDefaultsSpaceFromSingleConnectedSpace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		connectedSpaceIDs []string
		fieldOverride     string // "" means no --field space= given
		want              string
	}{
		{
			name:              "one connected space fills the placeholder",
			connectedSpaceIDs: []string{"acme-launch"},
			want:              "space: acme-launch",
		},
		{
			name:              "zero connected spaces leaves the placeholder",
			connectedSpaceIDs: nil,
			want:              "space: <space-id>",
		},
		{
			name:              "two connected spaces leaves the placeholder (ambiguous)",
			connectedSpaceIDs: []string{"acme-launch", "acme-migrate"},
			want:              "space: <space-id>",
		},
		{
			name:              "explicit --field space= always wins",
			connectedSpaceIDs: []string{"acme-launch"},
			fieldOverride:     "other-space",
			want:              "space: other-space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stagingDir := t.TempDir()
			cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, tt.connectedSpaceIDs)

			args := []string{"question"}
			if tt.fieldOverride != "" {
				args = append(args, "--field", "space="+tt.fieldOverride)
			}

			io, out, errOut := newIO()
			code := cmd.Run(context.Background(), args, io)
			if code != 0 {
				t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}

			entries, err := os.ReadDir(stagingDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("ReadDir: %v, entries=%v", err, entries)
			}
			raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !bytes.Contains(raw, []byte(tt.want)) {
				t.Fatalf("expected %q in drafted frontmatter; got:\n%s", tt.want, raw)
			}
		})
	}
}

// --- contract scaffold (D-D) -------------------------------------------

// contractScaffoldPaths returns the space-relative scaffold destinations
// `contract new` is expected to write under stagingDir for slug, mirroring
// internal/space.Layout's ProvidesSchemaDir/ProvidesFixturesValidDir —
// asserted directly against that package rather than re-deriving the
// shape, so this test would fail if cmd_new.go's scaffold path ever drifts
// from the layout the rest of the codebase reads.
func contractScaffoldPaths(t *testing.T, stagingDir, ownSystem, slug string) (schemaPath, fixturePath string) {
	t.Helper()
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		t.Fatalf("space.NewLayout(%q): %v", ownSystem, err)
	}
	schemaPath = filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesSchemaDir(slug)), slug+".schema.json")
	fixturePath = filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesValidDir(slug)), slug+".json")
	return schemaPath, fixturePath
}

// TestNewContractScaffoldsCompilableSchemaAndValidatingFixture proves the
// scaffolded fixture actually validates against the scaffolded schema, by
// compiling it for real (internal/schema.CompileExternal) — not merely
// asserting the files exist.
func TestNewContractScaffoldsCompilableSchemaAndValidatingFixture(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"contract", "--slug", "ingest"}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	schemaPath, fixturePath := contractScaffoldPaths(t, stagingDir, "axon", "ingest")

	schemaRaw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile(schema) %s: %v", schemaPath, err)
	}
	fixtureRaw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile(fixture) %s: %v", fixturePath, err)
	}

	compiled, err := schema.CompileExternal("ingest.schema.json", schemaRaw)
	if err != nil {
		t.Fatalf("CompileExternal: %v", err)
	}
	if err := compiled.ValidateInstance(fixtureRaw); err != nil {
		t.Fatalf("scaffolded fixture does not validate against scaffolded schema: %v", err)
	}

	// The output names every file it wrote, not only the descriptor.
	if !bytes.Contains(out.Bytes(), []byte(schemaPath)) {
		t.Errorf("expected stdout to name the scaffolded schema path %s; got %q", schemaPath, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(fixturePath)) {
		t.Errorf("expected stdout to name the scaffolded fixture path %s; got %q", fixturePath, out.String())
	}
}

// TestNewContractScaffoldSatisfiesCheckContractPublishable proves the
// scaffold is exactly what decision D-D demands: asserted against the
// rule itself (validate.CheckContractPublishable), not against a
// hardcoded file count.
func TestNewContractScaffoldSatisfiesCheckContractPublishable(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"contract", "--slug", "ingest"}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	layout, err := space.NewLayout("axon")
	if err != nil {
		t.Fatalf("space.NewLayout: %v", err)
	}
	schemaDir := filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesSchemaDir("ingest")))
	fixturesDir := filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesValidDir("ingest")))

	schemas, err := os.ReadDir(schemaDir)
	if err != nil {
		t.Fatalf("ReadDir(schema): %v", err)
	}
	fixtures, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("ReadDir(fixtures): %v", err)
	}

	violation := validate.CheckContractPublishable(validate.PublishableInput{
		SchemaFormat:  "json-schema-2020-12",
		ContractID:    "XC-axon-ingest",
		Schemas:       len(schemas),
		ValidFixtures: len(fixtures),
	})
	if violation != nil {
		t.Fatalf("CheckContractPublishable: expected nil for a freshly-scaffolded contract, got %+v", violation)
	}
}

// TestNewNonJSONSchemaContractGetsNoScaffold proves §5.4b's deep
// compatibility is left to the owner's own CI for non-JSON-Schema
// formats (proto3 here): no schema/fixture subtree is scaffolded at all.
func TestNewNonJSONSchemaContractGetsNoScaffold(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"contract", "--slug", "ingest", "--field", "schema_format=proto3"}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", stagingDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the staged .md draft for a proto3 contract, got %d entries: %v", len(entries), entries)
	}

	schemaPath, fixturePath := contractScaffoldPaths(t, stagingDir, "axon", "ingest")
	if _, err := os.Stat(schemaPath); !os.IsNotExist(err) {
		t.Errorf("expected no scaffolded schema for a proto3 contract, got err=%v at %s", err, schemaPath)
	}
	if _, err := os.Stat(fixturePath); !os.IsNotExist(err) {
		t.Errorf("expected no scaffolded fixture for a proto3 contract, got err=%v at %s", err, fixturePath)
	}
}

// TestNewContractScaffoldIsIdempotentAndNonDestructive proves a re-run
// over an existing contract never clobbers the author's own edits to a
// previously-scaffolded schema or fixture.
func TestNewContractScaffoldIsIdempotentAndNonDestructive(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"contract", "--slug", "ingest"}, io); code != 0 {
		t.Fatalf("first run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	schemaPath, fixturePath := contractScaffoldPaths(t, stagingDir, "axon", "ingest")

	const authorEdit = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"real_field":{"type":"integer"}},"required":["real_field"],"additionalProperties":false}`
	if err := os.WriteFile(schemaPath, []byte(authorEdit), 0o644); err != nil {
		t.Fatalf("simulate author edit: %v", err)
	}
	const authorFixtureEdit = `{"real_field": 7}`
	if err := os.WriteFile(fixturePath, []byte(authorFixtureEdit), 0o644); err != nil {
		t.Fatalf("simulate author edit: %v", err)
	}

	io2, out2, errOut2 := newIO()
	if code := cmd.Run(context.Background(), []string{"contract", "--slug", "ingest"}, io2); code != 0 {
		t.Fatalf("second run: code = %d; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
	}

	// The second run must not re-announce the schema/fixture as
	// scaffolded — nothing was written the second time.
	if bytes.Contains(out2.Bytes(), []byte(schemaPath)) {
		t.Errorf("expected the second run not to re-scaffold (and re-announce) the existing schema; got %q", out2.String())
	}
	if bytes.Contains(out2.Bytes(), []byte(fixturePath)) {
		t.Errorf("expected the second run not to re-scaffold (and re-announce) the existing fixture; got %q", out2.String())
	}

	gotSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile(schema): %v", err)
	}
	if string(gotSchema) != authorEdit {
		t.Fatalf("schema was overwritten by the re-run; got:\n%s\nwant (author's edit) unchanged:\n%s", gotSchema, authorEdit)
	}
	gotFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile(fixture): %v", err)
	}
	if string(gotFixture) != authorFixtureEdit {
		t.Fatalf("fixture was overwritten by the re-run; got:\n%s\nwant (author's edit) unchanged:\n%s", gotFixture, authorFixtureEdit)
	}
}

// --- template list/show ----------------------------------------------------

func TestTemplateListAndShow(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()

	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"list"}, io); code != 0 {
		t.Fatalf("template list: code = %d", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("question")) {
		t.Fatalf("expected 'question' in template list output; got %q", out.String())
	}

	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "question"}, io2); code != 0 {
		t.Fatalf("template show question: code = %d", code)
	}
	if !bytes.Contains(out2.Bytes(), []byte("type: question")) {
		t.Fatalf("expected the canonical question template body; got %q", out2.String())
	}
}

func TestTemplateShowUnknownType(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"show", "bogus"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (unknown type)", code)
	}
}
