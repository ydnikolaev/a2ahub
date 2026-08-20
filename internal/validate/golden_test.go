package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// corpusRoot is the schemas/ directory relative to this package (two
// levels up: internal/validate -> internal -> repo root -> schemas).
// Fixtures are excluded from the embedded schemas.FS by design (schemas/
// embed.go), so every test in this file reads them straight from disk,
// path-relative — same convention as internal/schema's AC-401.2 test.
const corpusRoot = "../../schemas"

func mustEngine(t *testing.T) *Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return New(corpus)
}

type sidecar struct {
	Code string `yaml:"code"`
}

// draftPathFor builds a synthetic repo-relative path that satisfies
// checkIDForm's filename/section guard for id, so the golden-fixture test
// below is isolated to schema-class violations (its purpose) and not
// coupled to how P2's fixture corpus happens to be laid out on disk
// (schemas/**/fixtures/**, which is not itself a "section" per §3.3/
// §4.2 — it's test data, not a real space).
func draftPathFor(id string) (string, error) {
	parsed, err := parseSystemFromID(id)
	if err != nil {
		return "", err
	}
	return parsed + "/exchanges/" + id + ".md", nil
}

// parseSystemFromID extracts the <system> token from a §3.3 ID
// (<PREFIX>-<system>-...) without re-implementing artifact.ParseID's
// full grammar — used only to build a matching synthetic path above.
func parseSystemFromID(id string) (string, error) {
	parts := strings.SplitN(id, "-", 3)
	if len(parts) < 2 {
		return "", fmt.Errorf("id %q has no system segment", id)
	}
	return parts[1], nil
}

// envelopeV2PolicyResolver is the fixed Resolver this file's ENGINE-routed
// envelope/v2 invalid fixtures resolve against (P3 "one reader for both
// wire forms", spec §11 amendment): unlike the v1 half above (schema class
// only, no cross-artifact fact needed), a REF-/POL- sidecar under
// envelope/v2/fixtures/invalid/ can name a rule that needs a resolved
// parent — REF-018's id-form range check chief among them. Small and fixed
// rather than generic, because this phase adds exactly ONE such fixture;
// widen the maps (never fork a second resolver type) when a later phase
// adds another.
type envelopeV2PolicyResolver struct {
	systemMembers map[string]bool
	criteriaIDs   map[string][]string
}

func (r *envelopeV2PolicyResolver) KnownArtifact(id string) bool { return true }
func (r *envelopeV2PolicyResolver) Digest(string) (string, bool) { return "", false }
func (r *envelopeV2PolicyResolver) System(system string) (member, left bool) {
	return r.systemMembers[system], false
}
func (r *envelopeV2PolicyResolver) AcceptanceCriteriaIDs(parentID string) ([]string, bool) {
	ids, ok := r.criteriaIDs[parentID]
	return ids, ok
}

var _ Resolver = (*envelopeV2PolicyResolver)(nil)
var _ ParentCriteriaIDs = (*envelopeV2PolicyResolver)(nil)

// TestGoldenFixtures_Envelope is AC-201.1's V1 half (schema-class fixtures,
// SCH- sidecars, routed through Engine.ValidateDraft) PLUS P3's own
// widening (spec §11 amendment, AC5): envelope/v2/fixtures/invalid/ is now
// globbed too, and a REF-/POL- sidecar there — a policy-class rule the
// schema corpus itself cannot express — routes through
// Engine.ValidateForSubmit instead, mirroring
// TestGoldenFixtures_EventManifestConsumes's own SCH-vs-REF-/POL- split
// below. Before this widening, schemas/envelope/v2/fixtures/invalid/'s 67
// files were read solely by internal/schema/v2_corpus_test.go
// (schema-class only) — no fixture there could ever prove a
// severity:reject POLICY rule fired through the real Engine.
func TestGoldenFixtures_Envelope(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	validFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v1/fixtures/valid/*.md"))
	if err != nil {
		t.Fatalf("glob valid: %v", err)
	}
	if len(validFiles) == 0 {
		t.Fatal("expected at least one valid envelope fixture")
	}
	for _, f := range validFiles {
		t.Run("valid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			id := idFromFrontmatter(t, raw)
			path, err := draftPathFor(id)
			if err != nil {
				t.Fatalf("draftPathFor(%s): %v", id, err)
			}
			result, err := engine.ValidateDraft(Draft{Path: path, Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if !result.Valid {
				t.Fatalf("expected a valid fixture to pass, got violations: %+v", result.Violations)
			}
		})
	}

	v1InvalidFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v1/fixtures/invalid/*.md"))
	if err != nil {
		t.Fatalf("glob invalid (v1): %v", err)
	}
	if len(v1InvalidFiles) == 0 {
		t.Fatal("expected at least one invalid envelope fixture")
	}
	// P3 widening (spec §11 amendment, AC1/AC5): envelope/v2's own invalid
	// corpus, previously reached by no policy-class-capable test at all,
	// and previously scoped to XS-* only (the family-scoped glob a prior
	// phase used, matching internal/schema/v2_corpus_test.go's own
	// XA-*-scoped globs over this SAME directory) because a blanket
	// `*.md` surfaced a genuine, pre-existing schema_class.go
	// classification bug in two `work` checkpoint fixtures — the
	// conditionally-required `actor.session` and `waiting_on` cases.
	// Now blanket-wide: schemaCode()'s SCH-005 detection has been widened
	// (this same phase) to also match a conditional `required` reached one
	// level deeper into a nested property (`actor.session`) or through a
	// $ref hop into a $defs schema (`waiting_on`) — see
	// isConditionalRequiredPointer's own comment for the three pointer
	// shapes. That $defs-hop shape also corrected
	// XC-axon-invalid-missing-conforms.md's sidecar, which had declared
	// SCH-001 for the structurally identical construct
	// ($defs.artifactEntry.allOf[4]'s role-gated conforms_to) — a sidecar
	// fitted to this function's own prior bug rather than a real second
	// category, per its own note ("fixture roles require conforms_to",
	// SCH-005's title in words while declaring SCH-001 in code).
	v2InvalidFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v2/fixtures/invalid/*.md"))
	if err != nil {
		t.Fatalf("glob invalid (v2): %v", err)
	}
	if len(v2InvalidFiles) == 0 {
		t.Fatal("expected at least one invalid envelope/v2 response (XS-*) fixture")
	}
	invalidFiles := append(append([]string{}, v1InvalidFiles...), v2InvalidFiles...)

	policyResolver := &envelopeV2PolicyResolver{
		systemMembers: map[string]bool{"axon": true, "seomatrix": true},
		criteriaIDs: map[string][]string{
			"XW-axon-20260820-par1": {"ac1", "ac2", "ac3"},
		},
	}

	for _, f := range invalidFiles {
		t.Run("invalid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			wantCode := sidecarCode(t, f+".expect.yaml")

			id := idFromFrontmatter(t, raw)
			path, err := draftPathFor(id)
			if err != nil {
				t.Fatalf("draftPathFor(%s): %v", id, err)
			}

			// Route by the sidecar's own code-class prefix — the SAME
			// mechanism TestGoldenFixtures_EventManifestConsumes already
			// uses below: SCH- is schema class, reachable at V1
			// (ValidateDraft, exactly as before this widening); REF-/POL-
			// are POLICY-class rules the schema corpus never sees, so they
			// need ValidateForSubmit's cross-artifact context instead. Any
			// other prefix fails loudly rather than silently reporting
			// zero violations for a rule this loop cannot route to a real
			// verdict.
			var result Result
			switch {
			case strings.HasPrefix(wantCode, "SCH-"):
				result, err = engine.ValidateDraft(Draft{Path: path, Raw: raw})
			case strings.HasPrefix(wantCode, "REF-"), strings.HasPrefix(wantCode, "POL-"):
				ownSystem := fromFrontmatter(t, raw)
				result, err = engine.ValidateForSubmit(
					Draft{Path: path, Raw: raw},
					nil,
					LocalContext{OwnSystem: ownSystem, Resolver: policyResolver},
				)
			default:
				t.Fatalf("%s: sidecar names code %q with an unrecognised prefix (want SCH-/REF-/POL-)", f, wantCode)
			}
			if err != nil {
				t.Fatalf("validate %s: %v", f, err)
			}
			if result.Valid {
				t.Fatalf("expected fixture to be invalid (code %s), got Valid=true", wantCode)
			}
			var codes []string
			for _, v := range result.Violations {
				codes = append(codes, v.Code)
			}
			if len(codes) != 1 || codes[0] != wantCode {
				t.Fatalf("expected EXACTLY [%s], got %v", wantCode, codes)
			}
		})
	}
}

// TestGoldenFixtures_EventManifestConsumes covers the three non-envelope
// product schemas (event/v1, manifest/v1, consumes/v1). These are not
// §3.3-IDed artifacts (no frontmatter wrapper, no filename/section
// guard), so they are exercised directly against internal/schema's
// per-family ValidateXxx + this package's own mapSchemaViolations —
// proving the same registry-code mapper is family-agnostic, without
// forcing them through the artifact-shaped Engine.ValidateDraft entry
// point (out of scope for how P6 ultimately wires event/manifest/
// consumes into the submit funnel).
func TestGoldenFixtures_EventManifestConsumes(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := New(corpus)

	families := []struct {
		name     string
		validate func(instance any) ([]schema.FieldViolation, error)
	}{
		{"event", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateEvent("v1", i) }},
		{"manifest", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateManifest("v1", i) }},
		{"consumes", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateConsumes("v1", i) }},
	}

	// policyValidators routes a REF-/POL- sidecar to the Engine entry point
	// that actually enforces POLICY-class rules (checkManifestPolicy,
	// checkNotificationRoutes) — logic that lives in hand-written Go, not in
	// the JSON-Schema corpus fam.validate above proves. Only "manifest" has
	// one wired today. A family with no entry here that still carries a
	// REF-/POL- sidecar is a fixture this loop cannot route to a real
	// verdict, so it reds by explicit `default` below rather than silently
	// falling through the schema-only path (which would report zero
	// violations and turn a real refusal invisible in the corpus).
	policyValidators := map[string]func([]byte) (Result, error){
		"manifest": engine.ValidateManifest,
	}

	for _, fam := range families {
		t.Run(fam.name, func(t *testing.T) {
			t.Parallel()

			validFiles := globFixtures(t, filepath.Join(corpusRoot, fam.name+"/v1/fixtures/valid/*.yaml"))
			if len(validFiles) == 0 {
				t.Fatalf("expected at least one valid %s fixture", fam.name)
			}
			for _, f := range validFiles {
				instance := decodeYAMLFile(t, f)
				fvs, err := fam.validate(instance)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				if len(fvs) != 0 {
					t.Errorf("%s: expected valid, got field violations %+v", f, fvs)
				}
			}

			invalidFiles := globFixtures(t, filepath.Join(corpusRoot, fam.name+"/v1/fixtures/invalid/*.yaml"))
			for _, f := range invalidFiles {
				wantCode := sidecarCode(t, f+".expect.yaml")

				// Route by the sidecar's own code-class prefix: SCH- is a
				// pure JSON-Schema violation (fam.validate + mapSchemaViolations,
				// exactly as before); REF-/POL- are POLICY-class rules that the
				// schema corpus never sees (checkManifestPolicy,
				// checkNotificationRoutes), so they must go through the
				// Engine instead. Any other prefix is unrecognised and must
				// fail loudly, naming the code — a future code class added
				// with no branch here must red this gate, never pass silently
				// with zero violations reported.
				var codes []string
				switch {
				case strings.HasPrefix(wantCode, "SCH-"):
					instance := decodeYAMLFile(t, f)
					fvs, err := fam.validate(instance)
					if err != nil {
						t.Fatalf("%s: %v", f, err)
					}
					violations, err := mapSchemaViolations(fvs)
					if err != nil {
						t.Fatalf("%s: mapSchemaViolations: %v", f, err)
					}
					for _, v := range violations {
						codes = append(codes, v.Code)
					}
				case strings.HasPrefix(wantCode, "REF-"), strings.HasPrefix(wantCode, "POL-"):
					policyValidate, ok := policyValidators[fam.name]
					if !ok {
						t.Fatalf("%s: sidecar names policy code %q, but family %q has no policy validator wired in this loop", f, wantCode, fam.name)
					}
					raw, err := os.ReadFile(f)
					if err != nil {
						t.Fatalf("read %s: %v", f, err)
					}
					result, err := policyValidate(raw)
					if err != nil {
						t.Fatalf("%s: %v", f, err)
					}
					for _, v := range result.Violations {
						codes = append(codes, v.Code)
					}
				default:
					t.Fatalf("%s: sidecar names code %q with an unrecognised prefix (want SCH-/REF-/POL-)", f, wantCode)
				}

				if len(codes) != 1 || codes[0] != wantCode {
					t.Fatalf("%s: expected EXACTLY [%s], got %v", f, wantCode, codes)
				}
			}
		})
	}
}

func idFromFrontmatter(t *testing.T, raw []byte) string {
	t.Helper()
	m := frontmatterMap(t, raw)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("fixture has no `id` field")
	}
	return id
}

// fromFrontmatter reads a fixture's own `from` field — TestGoldenFixtures_
// Envelope's REF-/POL- branch needs it to build a matching
// LocalContext.OwnSystem (checkAuthz/CC-002 refuses `from != ownSystem`,
// and a fixture whose ONLY intended violation is the sidecar's own code
// must not pick up a second, unwanted one here).
func fromFrontmatter(t *testing.T, raw []byte) string {
	t.Helper()
	m := frontmatterMap(t, raw)
	from, _ := m["from"].(string)
	if from == "" {
		t.Fatalf("fixture has no `from` field")
	}
	return from
}

// frontmatterMap is idFromFrontmatter's and fromFrontmatter's shared
// frontmatter decode.
func frontmatterMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	body := string(raw)
	parts := strings.SplitN(body, "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("fixture is not frontmatter-shaped")
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(parts[1]), &m); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return m
}

func sidecarCode(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sidecar %s: %v", path, err)
	}
	var sc sidecar
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("parse sidecar %s: %v", path, err)
	}
	if sc.Code == "" {
		t.Fatalf("sidecar %s has no code", path)
	}
	return sc.Code
}

func decodeYAMLFile(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("DecodeYAMLInstance %s: %v", path, err)
	}
	return instance
}

// globFixtures globs pattern and drops any `*.expect.yaml` sidecar that
// the glob's own "*.yaml" suffix incidentally also matches (a sidecar
// filename ends in ".yaml" too).
func globFixtures(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	out := matches[:0]
	for _, m := range matches {
		if strings.HasSuffix(m, ".expect.yaml") {
			continue
		}
		out = append(out, m)
	}
	return out
}
