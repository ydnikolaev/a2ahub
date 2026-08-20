package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func fixedActorResolver(cli.ActorFlags) (template.Actor, error) {
	return template.Actor{Kind: "agent", Name: "test-bot", Model: "test-model"}, nil
}

// enumFieldArgs names the --field flag a fresh draft of typ must supply for
// its top-level enum-constrained field, joining --slug in this test's list
// of "the author's own input a placeholder-only fill cannot stand in for".
//
// Before agent-exchange-2026-08 B3 (spec 03-fill-classes.md §8 AC4),
// applyFills silently filled an unreplaced enum placeholder with its first
// alternative, so `a2a new announcement` produced `category: release`
// nobody chose. That fill is gone, so these six types now need the same
// explicit choice `--slug` already required for the two standing types.
// `decision` and `handoff` carry no top-level enum placeholder and need no
// entry. Values match internal/livee2e/draftfields.go's own choices for the
// same fields.
func enumFieldArgs(typ string) []string {
	switch typ {
	case "announcement":
		return []string{"--field", "category=notice"}
	case "contract":
		return []string{"--field", "category=other"}
	case "question":
		return []string{"--field", "category=clarification"}
	case "requirement":
		return []string{"--field", "category=other"}
	case "work_request":
		return []string{"--field", "category=data"}
	case "response":
		return []string{"--field", "result=answered"}
	default:
		return nil
	}
}

// TestNewDraftsEveryTypeV1Valid is AC-401.1, the real cli-layer
// integration: for every type in the P2 corpus, `a2a new <type>` with
// placeholder-only fills (plus --slug for the two standing types and the
// enum field required per enumFieldArgs) then `a2a validate` on the
// drafted file returns V1-pass — driven against the real validate.Engine
// (schema.Load), not a fake.
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
			args = append(args, enumFieldArgs(typ)...)

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

// TestNewMintsThreadWhenNoneSupplied is spec 46 §T1 R1: drafting a NEW
// artifact with no --thread supplied mints one rather than leaving the
// template's literal `<thread:...>` placeholder in the committed draft —
// the happy path never leaves an artifact off-thread, and the agent never
// types or invents a thread id.
func TestNewMintsThreadWhenNoneSupplied(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"question"}, io); code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, %v", err, entries)
	}
	raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var threadLine string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "thread: ") {
			threadLine = strings.TrimPrefix(line, "thread: ")
			break
		}
	}
	if threadLine == "" {
		t.Fatalf("no thread: line found in drafted artifact:\n%s", raw)
	}
	parsed, perr := artifact.ParseThreadID(threadLine)
	if perr != nil {
		t.Fatalf("ParseThreadID(%q) = %v; a minted thread must always parse", threadLine, perr)
	}
	if parsed.System != "axon" {
		t.Fatalf("minted thread system = %q, want axon", parsed.System)
	}
}

// TestNewMalformedThreadRefused is spec 46 §T1 R6: draft-time thread
// validation is GRAMMAR ONLY (artifact.ParseThreadID, a pure call, no I/O)
// — a malformed --thread value is refused with exit 2, naming the bad
// value, rather than reaching the store or silently minting a fresh one.
func TestNewMalformedThreadRefused(t *testing.T) {
	t.Parallel()
	cmd := cli.NewNewCommand(t.TempDir(), "axon", fixedActorResolver, nil)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"question", "--thread", "not-a-thread-id"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (malformed --thread)", code)
	}
	if !strings.Contains(errOut.String(), "not-a-thread-id") {
		t.Fatalf("expected the bad value in stderr, got: %s", errOut.String())
	}
}

// TestNewExplicitThreadPropagatesVerbatim confirms a well-formed --thread
// lands in the drafted artifact unchanged (the R1 mint-fallback must not
// override an explicit, valid value).
func TestNewExplicitThreadPropagatesVerbatim(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
	io, out, errOut := newIO()
	const wantThread = "thread:axon-20260726-a1b2"
	code := cmd.Run(context.Background(), []string{"question", "--thread", wantThread}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, %v", err, entries)
	}
	raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("thread: "+wantThread)) {
		t.Fatalf("expected the explicit --thread to land verbatim; got:\n%s", raw)
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
	layout, err := space.NewLayout("axon")
	if err != nil {
		t.Fatalf("space.NewLayout: %v", err)
	}
	invalidFixturePath := filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesInvalidDir("ingest")), "ingest.json")
	invalidFixtureRaw, err := os.ReadFile(invalidFixturePath)
	if err != nil {
		t.Fatalf("ReadFile(invalid fixture) %s: %v", invalidFixturePath, err)
	}

	compiled, err := schema.CompileExternal("ingest.schema.json", schemaRaw)
	if err != nil {
		t.Fatalf("CompileExternal: %v", err)
	}
	if err := compiled.ValidateInstance(fixtureRaw); err != nil {
		t.Fatalf("scaffolded fixture does not validate against scaffolded schema: %v", err)
	}
	if err := compiled.ValidateInstance(invalidFixtureRaw); err == nil {
		t.Fatal("scaffolded invalid fixture unexpectedly validates against the scaffolded schema")
	}

	// The output names every file it wrote, not only the descriptor.
	if !bytes.Contains(out.Bytes(), []byte(schemaPath)) {
		t.Errorf("expected stdout to name the scaffolded schema path %s; got %q", schemaPath, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(fixturePath)) {
		t.Errorf("expected stdout to name the scaffolded fixture path %s; got %q", fixturePath, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(invalidFixturePath)) {
		t.Errorf("expected stdout to name the scaffolded invalid fixture path %s; got %q", invalidFixturePath, out.String())
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
	invalidFixturesDir := filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesInvalidDir("ingest")))

	schemas, err := os.ReadDir(schemaDir)
	if err != nil {
		t.Fatalf("ReadDir(schema): %v", err)
	}
	fixtures, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("ReadDir(fixtures): %v", err)
	}
	invalidFixtures, err := os.ReadDir(invalidFixturesDir)
	if err != nil {
		t.Fatalf("ReadDir(invalid fixtures): %v", err)
	}

	violation := validate.CheckContractPublishable(validate.PublishableInput{
		SchemaFormat:    "json-schema-2020-12",
		ContractID:      "XC-axon-ingest",
		Schemas:         len(schemas),
		ValidFixtures:   len(fixtures),
		InvalidFixtures: len(invalidFixtures),
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

// TestTemplateShowPrintsTheAuthoringGeneration is the CLI half of the
// fb-20260806-3539ac regression: `a2a template show contract` must print the
// declared-v2 shape `a2a new contract` writes and the publication planner
// requires, not the historical envelope/v1 template.
func TestTemplateShowPrintsTheAuthoringGeneration(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "contract"}, io); code != 0 {
		t.Fatalf("template show contract: code = %d; stderr=%s", code, errOut.String())
	}
	shown := out.String()
	if !strings.Contains(shown, "schema: envelope/v2") {
		t.Fatalf("expected the envelope/v2 contract template; got:\n%s", shown)
	}
	if !strings.Contains(shown, "\nartifacts:\n") {
		t.Fatalf("the shown template declares no top-level artifacts inventory, which is the shape publication requires; got:\n%s", shown)
	}
}

// TestTemplateShowExplicitGeneration keeps the older shape reachable for a
// space still below the publication floor, and refuses an unknown one rather
// than falling back silently.
func TestTemplateShowExplicitGeneration(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()

	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "contract", "--envelope-schema", "envelope/v1"}, io); code != 0 {
		t.Fatalf("template show contract --envelope-schema envelope/v1: code = %d", code)
	}
	if !strings.Contains(out.String(), "schema: envelope/v1") {
		t.Fatalf("expected the envelope/v1 contract template; got:\n%s", out.String())
	}

	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "contract", "--envelope-schema=envelope/v2"}, io2); code != 0 {
		t.Fatalf("--envelope-schema=<value> form: code = %d", code)
	}
	if !strings.Contains(out2.String(), "schema: envelope/v2") {
		t.Fatalf("expected the envelope/v2 contract template; got:\n%s", out2.String())
	}

	io3, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "contract", "--envelope-schema", "envelope/v9"}, io3); code != 1 {
		t.Fatalf("unknown generation: code = %d, want 1", code)
	}

	io4, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "contract", "--bogus"}, io4); code != 2 {
		t.Fatalf("unknown flag: code = %d, want 2 (never silently ignored)", code)
	}
}

// TestTemplateListStaysOneColumn is the regression for a defect this session
// SHIPPED and then had to take back: v0.19.6 added a tab-separated generation
// column to `template list`, which reads fine to a human and broke the
// skill-drift CI job on the release commit — `for t in $(a2a template list)`
// word-splits, so it ran `a2a template show envelope/v1`.
//
// The bare listing is a machine surface consumed by `$(...)`. Its contract is
// one type per line, whether or not anything had written that down.
func TestTemplateListStaysOneColumn(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"list"}, io); code != 0 {
		t.Fatalf("template list: code = %d", code)
	}
	listed := strings.TrimSpace(out.String())
	if listed == "" {
		t.Fatal("template list printed nothing")
	}
	for _, line := range strings.Split(listed, "\n") {
		if strings.ContainsAny(line, " \t") {
			t.Fatalf("a listing line carries whitespace, so `for t in $(a2a template list)` will split it: %q", line)
		}
		// The word-split failure this pins is not hypothetical: every field of
		// every line must be a type `template show` accepts.
		showIO, _, showErr := newIO()
		if code := cmd.Run(context.Background(), []string{"show", line}, showIO); code != 0 {
			t.Fatalf("template show %q: code = %d; stderr=%s", line, code, showErr.String())
		}
	}
}

// TestTemplateListJSONCarriesTheGeneration: the generation each type authors at
// is still reachable — in a representation nothing splits on whitespace.
// TestNewAcceptanceCriterionMintsIDsForV2RenderingType is spec 05 T1 row 1 /
// AC1: for a type template.selectGeneration renders as envelope/v2
// (work_request, response), `--acceptance-criterion` (repeatable) mints ids
// ac1..acN in the order given, over exactly the text the author typed — and
// the resulting draft is still V1-valid.
func TestNewAcceptanceCriterionMintsIDsForV2RenderingType(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	for _, typ := range []string{"work_request", "response"} {
		for _, n := range []int{1, 2, 7} {
			t.Run(typ+"/"+string(rune('0'+n)), func(t *testing.T) {
				t.Parallel()
				stagingDir := t.TempDir()
				cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

				args := []string{typ}
				args = append(args, enumFieldArgs(typ)...)
				var texts []string
				for i := 1; i <= n; i++ {
					text := "criterion text " + string(rune('A'+i-1))
					texts = append(texts, text)
					args = append(args, "--acceptance-criterion", text)
				}

				io, out, errOut := newIO()
				code := cmd.Run(context.Background(), args, io)
				if code != 0 {
					t.Fatalf("new %s: code = %d; stdout=%s stderr=%s", typ, code, out.String(), errOut.String())
				}

				raw, path := readOnlyDraft(t, stagingDir)
				for i, text := range texts {
					wantID := "ac" + string(rune('0'+i+1))
					if i+1 > 9 {
						t.Fatalf("test only supports single-digit N")
					}
					if !bytes.Contains(raw, []byte("id: "+wantID)) {
						t.Fatalf("draft is missing minted id %q:\n%s", wantID, raw)
					}
					if !bytes.Contains(raw, []byte("text: "+text)) {
						t.Fatalf("draft is missing criterion text %q:\n%s", text, raw)
					}
				}

				result, err := engine.ValidateDraft(validate.Draft{Path: path, Raw: raw})
				if err != nil {
					t.Fatalf("ValidateDraft: %v", err)
				}
				if !result.Valid {
					t.Fatalf("draft for %s is V1-invalid: %+v\n---\n%s", typ, result.Violations, raw)
				}
			})
		}
	}
}

// TestNewAcceptanceCriterionOnV1RenderingTypeDraftsBareStringsNoIDs is spec
// 05 T1 row 2 / AC3: on a type generationTable pins to envelope/v1, the same
// flag drafts the bare-string form — no `id:`/`text:` keys anywhere, because
// v1's own published `items: {type: string}` shape is immutable.
func TestNewAcceptanceCriterionOnV1RenderingTypeDraftsBareStringsNoIDs(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	for _, typ := range []string{"requirement", "question", "decision", "handoff", "announcement"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			stagingDir := t.TempDir()
			cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)

			args := []string{typ}
			if typ == "requirement" {
				args = append(args, "--slug", "ingest")
			}
			args = append(args, enumFieldArgs(typ)...)
			args = append(args, "--acceptance-criterion", "first criterion", "--acceptance-criterion", "second criterion")

			io, out, errOut := newIO()
			code := cmd.Run(context.Background(), args, io)
			if code != 0 {
				t.Fatalf("new %s: code = %d; stdout=%s stderr=%s", typ, code, out.String(), errOut.String())
			}

			raw, path := readOnlyDraft(t, stagingDir)
			if !bytes.Contains(raw, []byte("first criterion")) || !bytes.Contains(raw, []byte("second criterion")) {
				t.Fatalf("draft is missing the bare-string criteria:\n%s", raw)
			}
			if bytes.Contains(raw, []byte("id: ac1")) || bytes.Contains(raw, []byte(" text: first criterion")) {
				t.Fatalf("a v1-rendering type must NOT mint ids — got:\n%s", raw)
			}

			result, err := engine.ValidateDraft(validate.Draft{Path: path, Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if !result.Valid {
				t.Fatalf("draft for %s is V1-invalid: %+v\n---\n%s", typ, result.Violations, raw)
			}
		})
	}
}

// TestNewAcceptanceCriterionZeroDraftsNoKey is spec 05 §8 AC8: a type whose
// template carries no acceptance_criteria key at all (question) gets none
// when the flag is not given — never an empty array, and never a key the
// author never asked for.
func TestNewAcceptanceCriterionZeroDraftsNoKey(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
	args := []string{"question"}
	args = append(args, enumFieldArgs("question")...)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), args, io)
	if code != 0 {
		t.Fatalf("new question: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	raw, _ := readOnlyDraft(t, stagingDir)
	if bytes.Contains(raw, []byte("acceptance_criteria")) {
		t.Fatalf("expected no acceptance_criteria key at all when the flag is not given:\n%s", raw)
	}
}

// readOnlyDraft reads the single staged .md draft under stagingDir — the
// same "find the one .md entry" shape TestNewDraftsEveryTypeV1Valid already
// uses, factored out because these three tests all need it. Returns the raw
// bytes and the draft's real path (ValidateDraft only reports Path; it never
// reads it back off disk, but a real path keeps failure messages honest).
func readOnlyDraft(t *testing.T, stagingDir string) ([]byte, string) {
	t.Helper()
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", stagingDir, err)
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
	path := filepath.Join(stagingDir, draftName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return raw, path
}

func TestTemplateListJSONCarriesTheGeneration(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"list", "--json"}, io); code != 0 {
		t.Fatalf("template list --json: code = %d; stderr=%s", code, errOut.String())
	}
	var rows []struct {
		Type           string `json:"type"`
		EnvelopeSchema string `json:"envelope_schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode template list --json: %v\noutput: %s", err, out.String())
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[row.Type] = row.EnvelopeSchema
	}
	if seen["contract"] != "envelope/v2" {
		t.Fatalf("contract authors at %q, want envelope/v2; rows=%+v", seen["contract"], rows)
	}
	if seen["question"] != "envelope/v1" {
		t.Fatalf("question authors at %q, want envelope/v1; rows=%+v", seen["question"], rows)
	}
}
