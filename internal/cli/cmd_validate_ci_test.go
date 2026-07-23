package cli

// Tests for the `a2a validate --ci` path (spec 17 §6). These live in
// package cli (not cli_test) so they can drive the unexported
// runValidateCI directly and inject a fake git-diff seam — no live git
// checkout needed. The engine is the REAL schema corpus (schema.Load),
// so a "valid artifact" here is genuinely V2-valid, not a stub.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// ciSpaceYAML is the getvisa fixture manifest: axon (owner ydnikolaev) and
// seomatrix (owner misha-gh), each with its own section.
const ciSpaceYAML = `schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
  - system: seomatrix
    org: seomatrix
    section: seomatrix/
    owners: [misha-gh]
    status: active
    joined: 2026-07-28
vendored: []
`

// validQuestion returns a genuinely V2-valid question envelope for system
// `from` addressed to `to`. No refs (so checkRefs is trivially clean).
func validQuestion(id, from, to string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: Test question\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"category: defect\n" +
		"priority: p2\n" +
		"blocking: true\n" +
		"expected_response: {shape: \"an answer\"}\n" +
		"classification: internal\n" +
		"---\nBody.\n"
}

func ciEngine(t *testing.T) *validate.Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return validate.New(corpus)
}

// ciRepo builds a temp space-repo checkout: space.yaml at root plus any
// artifacts (relPath -> content). Returns the root dir.
func ciRepo(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// fakeGit returns a gitChangedFilesFunc that always yields the given paths.
func fakeGit(paths ...string) gitChangedFilesFunc {
	return func(context.Context, string, string) ([]string, error) {
		return paths, nil
	}
}

// runCI drives runValidateCI and returns exit code + decoded report.
func runCI(t *testing.T, engine *validate.Engine, root string, git gitChangedFilesFunc, mode, base, author string) (int, ciReport, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := runValidateCI(context.Background(), engine, root, git, mode, base, author, IO{Stdout: &out, Stderr: &errBuf})
	var rep ciReport
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
			t.Fatalf("decode ci report: %v\nstdout: %s", err, out.String())
		}
	}
	return code, rep, errBuf.String()
}

func TestValidateCI_PRHappyPath(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix"),
	})

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid {
		t.Fatalf("report.Valid = false, want true: %+v", rep)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
		t.Fatalf("expected one valid artifact result, got %+v", rep.Artifacts)
	}
	if len(rep.DiffAuthz) != 0 {
		t.Fatalf("unexpected diff-authz violations: %+v", rep.DiffAuthz)
	}
}

func TestValidateCI_PRNoChangedArtifacts(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// A changed file that is NOT an artifact under a section (e.g. a docs
	// file) filters out -> nothing to validate -> exit 0, and diff-authz
	// is skipped (no unmapped-author red on an empty change set).
	root := ciRepo(t, ciSpaceYAML, nil)
	code, rep, errOut := runCI(t, engine, root, fakeGit("README.md", ".github/workflows/x.yml"), "v3-pr", "deadbeef", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut)
	}
	if !rep.Valid || len(rep.Artifacts) != 0 || len(rep.DiffAuthz) != 0 {
		t.Fatalf("want clean empty report, got %+v", rep)
	}
}

func TestValidateCI_PRSchemaRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	// Missing required `category` -> schema-class violation.
	bad := "---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260730-h2k8\n" +
		"type: question\n" +
		"title: Test question\n" +
		"space: getvisa\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"priority: p2\n" +
		"blocking: true\n" +
		"expected_response: {shape: \"an answer\"}\n" +
		"classification: internal\n" +
		"---\nBody.\n"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: bad})

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if rep.Valid || len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || rep.Artifacts[0].Result.Valid {
		t.Fatalf("expected one invalid artifact result, got %+v", rep.Artifacts)
	}
}

func TestValidateCI_PRReferentialRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// id's system component (seomatrix) disagrees with the owning section
	// (axon/) -> referential CC-003/REF-002.
	rel := "axon/exchanges/XQ-seomatrix-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validQuestion("XQ-seomatrix-20260730-h2k8", "axon", "seomatrix"),
	})
	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if rep.Valid {
		t.Fatalf("expected invalid report, got %+v", rep)
	}
}

func TestValidateCI_PRAuthzRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// id system is `axon` and it sits in axon's section (no REF-002/filename
	// noise), but `from: seomatrix` disagrees with the section owner -> the
	// engine's CC-002/REF-005 authz check reds. Author maps to axon so
	// diff-authz stays clean and cannot mask the engine violation. This
	// pins the per-artifact ownSystem = systemForPath derivation.
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validQuestion("XQ-axon-20260730-h2k8", "seomatrix", "axon"),
	})
	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.DiffAuthz) != 0 {
		t.Fatalf("diff-authz should be clean (author in axon), got %+v", rep.DiffAuthz)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || rep.Artifacts[0].Result.Valid {
		t.Fatalf("expected one invalid artifact result, got %+v", rep.Artifacts)
	}
	var sawAuthz bool
	for _, v := range rep.Artifacts[0].Result.Violations {
		if v.Code == "REF-005" && v.CCRef == "CC-002" {
			sawAuthz = true
		}
	}
	if !sawAuthz {
		t.Fatalf("expected a REF-005/CC-002 authz violation, got %+v", rep.Artifacts[0].Result.Violations)
	}
}

func TestValidateCI_PRMalformedArtifact(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	// Not a frontmatter document at all -> malformed, must red (not panic).
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: "this is not frontmatter\n"})
	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if rep.Valid {
		t.Fatalf("expected invalid report, got %+v", rep)
	}
}

func TestValidateCI_DiffAuthzOutsideSection(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// A V2-valid seomatrix artifact, but the PR author maps to axon: the
	// changed path is outside axon's section -> diff-authz violation.
	rel := "seomatrix/exchanges/XQ-seomatrix-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validQuestion("XQ-seomatrix-20260730-h2k8", "seomatrix", "axon"),
	})
	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.DiffAuthz) != 1 || rep.DiffAuthz[0].Path != rel {
		t.Fatalf("expected one diff-authz violation on %s, got %+v", rel, rep.DiffAuthz)
	}
	// The artifact itself is V2-valid; only diff-authz reds it.
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
		t.Fatalf("artifact should be V2-valid, got %+v", rep.Artifacts)
	}
}

// TestValidateCI_DiffAuthzNonArtifactCrossSection is the strict-L0 gap this
// change closes: a PR touching ONLY another system's NON-artifact file
// (consumes.yaml) — no *.md at all — was previously unguarded (artifacts==0
// skipped diff-authz entirely). Now the section-scoped path is authorized:
// axon author editing seomatrix/consumes.yaml reds.
func TestValidateCI_DiffAuthzNonArtifactCrossSection(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// No *.md in the change set — only a cross-section non-artifact file.
	root := ciRepo(t, ciSpaceYAML, nil)
	changed := "seomatrix/consumes.yaml"
	code, rep, _ := runCI(t, engine, root, fakeGit(changed), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (cross-section non-artifact edit); report=%+v", code, rep)
	}
	if len(rep.DiffAuthz) != 1 || rep.DiffAuthz[0].Path != changed {
		t.Fatalf("expected one diff-authz violation on %s, got %+v", changed, rep.DiffAuthz)
	}
	if len(rep.Artifacts) != 0 {
		t.Fatalf("no *.md changed -> zero artifact results, got %+v", rep.Artifacts)
	}
}

// TestValidateCI_DiffAuthzOwnSectionNonArtifact confirms the widened authz
// does NOT over-fire: an author editing a NON-artifact file inside their OWN
// section is clean.
func TestValidateCI_DiffAuthzOwnSectionNonArtifact(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, nil)
	code, rep, errOut := runCI(t, engine, root, fakeGit("axon/consumes.yaml", "axon/events/2026/e.yaml"), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (own-section edits); stderr=%s; report=%+v", code, errOut, rep)
	}
	if len(rep.DiffAuthz) != 0 || !rep.Valid {
		t.Fatalf("own-section non-artifact edits must be clean, got %+v", rep)
	}
}

// TestValidateCI_DiffAuthzRootFileOutOfScope proves space infrastructure
// under NO participant section (root space.yaml) is deliberately NOT
// author-diff-authz'd — it is governed by CODEOWNERS + branch protection, and
// authorizing it here would red the space owner's own manifest edit.
func TestValidateCI_DiffAuthzRootFileOutOfScope(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, nil)
	code, rep, errOut := runCI(t, engine, root, fakeGit("space.yaml", "CODEOWNERS", ".github/workflows/ci.yml"), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out-of-section infra); stderr=%s; report=%+v", code, errOut, rep)
	}
	if len(rep.DiffAuthz) != 0 {
		t.Fatalf("root/infra paths must be out of author-diff-authz scope, got %+v", rep.DiffAuthz)
	}
}

// TestValidateCI_DiffAuthzMixedArtifactAndCrossSection: an author edits their
// own valid *.md AND another system's non-artifact file in one PR — the *.md
// validates clean, the cross-section file reds diff-authz.
func TestValidateCI_DiffAuthzMixedArtifactAndCrossSection(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	own := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	cross := "seomatrix/events/2026/e.yaml"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		own: validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix"),
	})
	code, rep, _ := runCI(t, engine, root, fakeGit(own, cross), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.DiffAuthz) != 1 || rep.DiffAuthz[0].Path != cross {
		t.Fatalf("expected one diff-authz violation on %s, got %+v", cross, rep.DiffAuthz)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
		t.Fatalf("own *.md should be V2-valid, got %+v", rep.Artifacts)
	}
}

func TestValidateCI_DiffAuthzUnmappedAuthor(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix"),
	})
	// Author not in any participant's owners -> CC-097.
	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "stranger")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.DiffAuthz) != 1 || rep.DiffAuthz[0].CCRef != "CC-097" {
		t.Fatalf("expected one CC-097 diff-authz violation, got %+v", rep.DiffAuthz)
	}
}

// validContract returns a genuinely V2-valid contract descriptor for
// system `from`, as committed at §4.2's <from>/provides/<slug>/contract.md.
func validContract(from, slug string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: Test contract\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [seomatrix]\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"category: data-feed\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"1.0.0\"\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"---\nBody.\n"
}

// validDecision returns a genuinely V2-valid decision drafted by `from`,
// as committed at §4.2's space-level decisions/<id>.md.
func validDecision(id, from string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: Test decision\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [axon, seomatrix]\n" +
		"actor: {kind: human, name: yura}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"required_approvers: [axon, seomatrix]\n" +
		"context: \"why this needs deciding\"\n" +
		"options_considered: [\"a\", \"b\"]\n" +
		"---\nBody.\n"
}

// TestValidateCI_PRContractAtItsCanonicalPath proves V3 accepts a contract
// at the ONLY path a contract can be committed to — the fixed
// provides/<slug>/contract.md — which the pre-fix stem guard reds
// unconditionally (fb-20260723-9ae145: the same defect blocked `a2a
// submit`, so a hand-opened PR was no workaround).
func TestValidateCI_PRContractAtItsCanonicalPath(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: validContract("axon", "content-feed")})

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
		t.Fatalf("expected the contract to validate clean, got %+v", rep.Artifacts)
	}
}

// TestValidateCI_PRContractUnderWrongSlugRed proves the placement guard
// still has teeth: the descriptor's own directory must match its id's slug.
func TestValidateCI_PRContractUnderWrongSlugRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/other-feed/contract.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: validContract("axon", "content-feed")})

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil {
		t.Fatalf("expected one artifact result, got %+v", rep.Artifacts)
	}
	var sawREF001 bool
	for _, v := range rep.Artifacts[0].Result.Violations {
		if v.Code == "REF-001" {
			sawREF001 = true
		}
	}
	if !sawREF001 {
		t.Fatalf("expected REF-001 for a contract under the wrong slug dir, got %+v", rep.Artifacts[0].Result.Violations)
	}
}

// TestValidateCI_PRSpaceLevelDecisionValidated proves decisions/ — filed
// under no participant section — is validated by V3 rather than skipped,
// and stays out of author-diff-authz (multi-party by §4.2).
func TestValidateCI_PRSpaceLevelDecisionValidated(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "decisions/XD-axon-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: validDecision("XD-axon-20260730-h2k8", "axon")})

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "misha-gh")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
		t.Fatalf("expected the decision to be validated and clean, got %+v", rep.Artifacts)
	}
	if len(rep.DiffAuthz) != 0 {
		t.Fatalf("space-level decisions must stay out of diff-authz, got %+v", rep.DiffAuthz)
	}
}

// TestValidateCI_PRSpaceLevelNonDecisionRed proves the space-level lane is
// not a bypass: a question smuggled into decisions/ still reds (its `from`
// no longer matches the section it claims to live in).
func TestValidateCI_PRSpaceLevelNonDecisionRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "decisions/XQ-axon-20260730-h2k8.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: validQuestion("XQ-axon-20260730-h2k8", "seomatrix", "axon")})

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (a non-decision under decisions/ must red); report=%+v", code, rep)
	}
}

func TestValidateCI_PRDeletedFileSkipped(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	// The changed set names a path absent on disk (a deletion the fake git
	// surfaces) -> skipped cleanly, not an ENOENT red.
	root := ciRepo(t, ciSpaceYAML, nil)
	del := "axon/exchanges/XQ-axon-gone-0000-0000.md"
	code, rep, errOut := runCI(t, engine, root, fakeGit(del), "v3-pr", "deadbeef", "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid || len(rep.Artifacts) != 0 {
		t.Fatalf("deleted path should be skipped, got %+v", rep)
	}
}

func TestValidateCI_FullRepo(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		"axon/exchanges/XQ-axon-20260730-h2k8.md":           validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix"),
		"seomatrix/exchanges/XQ-seomatrix-20260730-a1b2.md": validQuestion("XQ-seomatrix-20260730-a1b2", "seomatrix", "axon"),
		"README.md": "# not an artifact\n",
	})
	// Full-repo ignores git; walks all *.md under sections. base unused.
	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid || len(rep.Artifacts) != 2 {
		t.Fatalf("expected 2 valid artifacts (README excluded), got %+v", rep.Artifacts)
	}
	if len(rep.DiffAuthz) != 0 {
		t.Fatalf("full-repo must not run diff-authz, got %+v", rep.DiffAuthz)
	}
}

func TestValidateCI_FullRepoEmpty(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, nil)
	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut)
	}
	if !rep.Valid || len(rep.Artifacts) != 0 {
		t.Fatalf("empty repo should be clean, got %+v", rep)
	}
}

func TestValidateCI_UsageErrors(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, nil)
	cases := []struct{ name, mode, base string }{
		{"missing mode", "", "deadbeef"},
		{"unknown mode", "v3-bogus", "deadbeef"},
		{"pr without base", "v3-pr", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errBuf bytes.Buffer
			code := runValidateCI(context.Background(), engine, root, fakeGit(), tc.mode, tc.base, "ydnikolaev", IO{Stdout: &out, Stderr: &errBuf})
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (usage); stderr=%s", code, errBuf.String())
			}
			if out.Len() != 0 {
				t.Fatalf("usage error must not emit JSON, got: %s", out.String())
			}
		})
	}
}

func TestValidateCI_MissingManifest(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := t.TempDir() // no space.yaml
	code := runValidateCI(context.Background(), engine, root, fakeGit(), "v3-full-repo", "", "", IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (missing manifest)", code)
	}
}

// TestValidateCI_ThroughRun exercises the full flag-parse + delegation
// path: `validate --ci --mode=v3-full-repo` reaching runValidateCI with
// root = cwd. Not parallel (t.Chdir changes process-global cwd).
func TestValidateCI_ThroughRun(t *testing.T) {
	engine := ciEngine(t)
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		"axon/exchanges/XQ-axon-20260730-h2k8.md": validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix"),
	})
	t.Chdir(root)

	cmd := NewValidateCommand(engine, filepath.Join(root, ".a2a", "staging"))
	var out, errBuf bytes.Buffer
	code := cmd.Run(context.Background(), []string{"--ci", "--mode=v3-full-repo"}, IO{Stdout: &out, Stderr: &errBuf})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; stdout=%s", code, errBuf.String(), out.String())
	}
	var rep ciReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("decode report: %v\nstdout=%s", err, out.String())
	}
	if rep.Mode != "v3-full-repo" || !rep.Valid || len(rep.Artifacts) != 1 {
		t.Fatalf("unexpected report through Run: %+v", rep)
	}
}

// TestValidate_NonCIPathsUnchanged proves the flag additions did not break
// the existing `validate <path>` / no-arg usage paths.
func TestValidate_NonCIPathsUnchanged(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	dir := t.TempDir()
	valid := filepath.Join(dir, "XQ-axon-20260730-h2k8.md")
	if err := os.WriteFile(valid, []byte(validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix")), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := NewValidateCommand(engine, dir)

	// `validate <path>` on a V1-valid draft -> exit 0.
	var out, errBuf bytes.Buffer
	if code := cmd.Run(context.Background(), []string{valid}, IO{Stdout: &out, Stderr: &errBuf}); code != 0 {
		t.Fatalf("validate <path> exit = %d, want 0; stderr=%s", code, errBuf.String())
	}

	// No args -> usage exit 2.
	out.Reset()
	errBuf.Reset()
	if code := cmd.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errBuf}); code != 2 {
		t.Fatalf("validate (no args) exit = %d, want 2", code)
	}
}
