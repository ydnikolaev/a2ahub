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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
//
// Wave B2 note: this fixture is now also a git repo (contractGitRun) with
// schema/**+fixtures/valid/** on disk and a real `base` — a bare temp dir
// with the placeholder base "deadbeef" (this test's pre-wave-B2 shape) made
// the new merge-side compat check (contractTouchedByPath matches any
// provides/<slug>/contract.md path, unconditionally in v3-pr mode) fail
// with a git error, which is a real defect this setup exposed, not a test
// artifact to route around. The report now carries TWO entries for the
// same path — the pre-existing V2 placement/envelope check plus wave B2's
// own compat verdict — both expected clean.
func TestValidateCI_PRContractAtItsCanonicalPath(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: validContract("axon", "content-feed"),
		"axon/provides/content-feed/schema/main.schema.json": `{"type":"object"}`,
		"axon/provides/content-feed/fixtures/valid/ok.json":  `{}`,
	})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "publish content-feed 1.0.0")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if len(rep.Artifacts) != 2 {
		t.Fatalf("expected two report entries (the V2 placement/envelope check plus wave B2's merge-side compat verdict), got %+v", rep.Artifacts)
	}
	for _, a := range rep.Artifacts {
		if a.Result == nil || !a.Result.Valid {
			t.Fatalf("expected every report entry to be clean, got %+v", rep.Artifacts)
		}
	}
}

// TestValidateCI_PRContractUnderWrongSlugRed proves the placement guard
// still has teeth: the descriptor's own directory must match its id's slug.
//
// Wave B2 note: same git-repo-plus-real-base setup as the sibling test
// above, for the same reason. This fixture publishes no schema/**/
// fixtures/valid/** at all, so wave B2's own D-D check (POL-009) now also
// fires on the second report entry — asserted explicitly below rather than
// masked by only checking the first entry.
func TestValidateCI_PRContractUnderWrongSlugRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/other-feed/contract.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{rel: validContract("axon", "content-feed")})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "misplaced contract")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
	}
	if len(rep.Artifacts) != 2 {
		t.Fatalf("expected two report entries (the V2 placement/envelope check plus wave B2's merge-side compat verdict), got %+v", rep.Artifacts)
	}
	var sawREF001 bool
	var sawPOL009 bool
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "REF-001" {
				sawREF001 = true
			}
			if v.Code == "POL-009" {
				sawPOL009 = true
			}
		}
	}
	if !sawPOL009 {
		t.Fatalf("expected POL-009 (wave B2/D-D: no schema/** or fixtures/valid/** published), got %+v", rep.Artifacts)
	}
	if !sawREF001 {
		t.Fatalf("expected REF-001 for a contract under the wrong slug dir, got %+v", rep.Artifacts)
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

// TestValidateCI_PRConsumesRegistryValidated proves the D-022 registry
// file is checked by V3 — it carries no envelope, but it is normative, and
// an invalid one used to merge silently and register nobody.
func TestValidateCI_PRConsumesRegistryValidated(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/consumes.yaml"

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciSpaceYAML, map[string]string{
			rel: "schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-seomatrix-content-feed\n    major: 1\n    since: \"2026-07-23\"\n",
		})
		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
		}
		if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || !rep.Artifacts[0].Result.Valid {
			t.Fatalf("expected the registry to be validated and clean, got %+v", rep.Artifacts)
		}
	})

	t.Run("the consumes: [] placeholder reds", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciSpaceYAML, map[string]string{rel: "consumes: []\n"})
		code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 1 {
			t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
		}
		if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil || rep.Artifacts[0].Result.Valid {
			t.Fatalf("expected the placeholder to red, got %+v", rep.Artifacts)
		}
	})

	t.Run("full-repo scan picks it up too", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciSpaceYAML, map[string]string{rel: "consumes: []\n"})
		code, rep, _ := runCI(t, engine, root, nil, "v3-full-repo", "", "")
		if code != 1 {
			t.Fatalf("exit = %d, want 1; report=%+v", code, rep)
		}
	})
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

// --- P37 wave B2: the merge-side contract compat check (spec 37 §2 T2/T3,
// AC-970.2) -------------------------------------------------------------
//
// These tests build a REAL git repo (contractGitRun/contractWriteFile/
// contractGitRevParse — contract_git_test.go's own helpers, same package)
// rather than injecting a fake for the compat plumbing itself: the git
// reads under test (contractGitBounded's `show`, contractReadTreeAtSHA's
// `ls-tree`+`show`) are real subprocess calls that need a real object
// store. `fakeGit` still stands in for `git diff --name-only` (the
// gitChangedFilesFunc seam) — only the CHANGED-PATHS LIST is faked; base
// resolution and the prior-version reads are real git against the fixture
// repo.
//
// The "PR" itself is modelled as: commit the PRIOR state for real (this
// becomes --base), then edit the working tree in place, UNCOMMITTED, to
// the "new" state — validateCIContract reads the new state straight off
// disk (contractReadDescriptor/contractWorkingTreeFiles) and the prior
// state out of git history at base, exactly like a real PR checkout.

const (
	ciContractDescriptorDir  = "axon/provides/widget"
	ciContractDescriptorPath = ciContractDescriptorDir + "/contract.md"
	ciContractSchemaPath     = ciContractDescriptorDir + "/schema/main.schema.json"
	ciContractFixtureRel     = "fixtures/valid/ok.json" // relative to ciContractDescriptorDir
	ciContractFixturePath    = ciContractDescriptorDir + "/" + ciContractFixtureRel
)

// ciContractDescriptorAt renders validContract's own genuinely V2-valid
// contract envelope (this file's own helper) with `version`/`schema_format`
// overridden — never a second hand-rolled envelope template.
func ciContractDescriptorAt(version, schemaFormat string) string {
	body := validContract("axon", "widget")
	body = strings.Replace(body, `version: "1.0.0"`, `version: "`+version+`"`, 1)
	body = strings.Replace(body, "schema_format: json-schema-2020-12", "schema_format: "+schemaFormat, 1)
	return body
}

// ciContractPriorCommit builds a real git repo (space.yaml + the given
// contract state) and commits it once — the returned root's HEAD is the
// PRIOR version a caller then reads via contractGitRevParse(t, root,
// "HEAD") as --base, before mutating the working tree to the "new" state.
func ciContractPriorCommit(t *testing.T, descriptorMD, schemaJSON, fixtureJSON string) (root string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "space.yaml"), []byte(ciSpaceYAML), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
	contractWriteFile(t, root, ciContractDescriptorPath, descriptorMD)
	contractWriteFile(t, root, ciContractSchemaPath, schemaJSON)
	contractWriteFile(t, root, ciContractFixturePath, fixtureJSON)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "publish prior")
	return root
}

// TestValidateCI_ContractCompat_MinorMislabeledBreakingRedsPOL007 is
// AC-970.2's headline case: a declared MINOR bump whose new schema rejects
// a prior-version fixture is refused, naming the fixture (AC-970.1).
func TestValidateCI_ContractCompat_MinorMislabeledBreakingRedsPOL007(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciContractPriorCommit(t,
		ciContractDescriptorAt("1.0.0", "json-schema-2020-12"),
		`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`,
		`{"x":"hi"}`,
	)
	base := contractGitRevParse(t, root, "HEAD")

	// Working tree moves to a declared MINOR bump (1.0.0 -> 1.1.0) that is
	// actually breaking: the new schema requires a field ("y") the prior
	// fixture never had.
	contractWriteFile(t, root, ciContractDescriptorPath, ciContractDescriptorAt("1.1.0", "json-schema-2020-12"))
	contractWriteFile(t, root, ciContractSchemaPath, `{"type":"object","properties":{"x":{"type":"string"},"y":{"type":"string"}},"required":["x","y"]}`)

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath, ciContractSchemaPath), "v3-pr", base, "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; report=%+v; stderr=%s", code, rep, errOut)
	}
	var msg string
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-007" {
				msg = v.Message
			}
		}
	}
	if msg == "" {
		t.Fatalf("expected a POL-007 violation, got %+v", rep.Artifacts)
	}
	if !strings.Contains(msg, ciContractFixtureRel) {
		t.Fatalf("POL-007 message %q does not name the offending fixture %s (AC-970.1)", msg, ciContractFixtureRel)
	}
}

// TestValidateCI_ContractCompat_MajorBumpNotChecked is AC-970.3 at the
// merge boundary: a major bump is not compat-checked, and the CI log says
// so explicitly (never silence) even though the exit code stays 0.
func TestValidateCI_ContractCompat_MajorBumpNotChecked(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciContractPriorCommit(t,
		ciContractDescriptorAt("1.0.0", "json-schema-2020-12"),
		`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`,
		`{"x":"hi"}`,
	)
	base := contractGitRevParse(t, root, "HEAD")

	// Same breaking schema edit as the minor case above, but declared MAJOR
	// (1.0.0 -> 2.0.0) — not compat-checked, so it must NOT red on POL-007/
	// POL-008. Fixtures/schema stay non-empty so POL-009 (D-D) is satisfied
	// independently of this test's own concern.
	contractWriteFile(t, root, ciContractDescriptorPath, ciContractDescriptorAt("2.0.0", "json-schema-2020-12"))
	contractWriteFile(t, root, ciContractSchemaPath, `{"type":"object","properties":{"x":{"type":"string"},"y":{"type":"string"}},"required":["x","y"]}`)

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath, ciContractSchemaPath), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; report=%+v; stderr=%s", code, rep, errOut)
	}
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-007" || v.Code == "POL-008" {
				t.Fatalf("unexpected %s for a major bump (not compat-checked), got %+v", v.Code, a)
			}
		}
	}
	if !strings.Contains(errOut, "major") {
		t.Fatalf("stderr must explicitly say computed compatibility was not checked for the major bump (never silence), got: %q", errOut)
	}
}

// TestValidateCI_ContractCompat_FirstPublishNotChecked proves D-B's other
// honest non-answer: a contract published for the first time in this PR
// (no descriptor at --base at all) is not compat-checked, and says so.
func TestValidateCI_ContractCompat_FirstPublishNotChecked(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "space.yaml"), []byte(ciSpaceYAML), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "empty space, no contract yet")
	base := contractGitRevParse(t, root, "HEAD")

	// The PR adds the whole contract for the first time.
	contractWriteFile(t, root, ciContractDescriptorPath, ciContractDescriptorAt("1.0.0", "json-schema-2020-12"))
	contractWriteFile(t, root, ciContractSchemaPath, `{"type":"object"}`)
	contractWriteFile(t, root, ciContractFixturePath, `{}`)

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath, ciContractSchemaPath, ciContractFixturePath), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; report=%+v; stderr=%s", code, rep, errOut)
	}
	if !strings.Contains(errOut, "first publish") {
		t.Fatalf("stderr must explicitly say this is a first publish (never silence), got: %q", errOut)
	}
}

// TestValidateCI_ContractCompat_Proto3NotChecked proves the §5.4b format
// gate: a proto3 (non-JSON-Schema) contract is never handed to
// CheckComputedCompatibility, and D-D's fixture requirement (POL-009) does
// not apply to it either.
func TestValidateCI_ContractCompat_Proto3NotChecked(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciContractPriorCommit(t,
		ciContractDescriptorAt("1.0.0", "proto3"),
		`{"type":"object"}`, // irrelevant for proto3 — never read by the compat core
		`{}`,
	)
	base := contractGitRevParse(t, root, "HEAD")

	contractWriteFile(t, root, ciContractDescriptorPath, ciContractDescriptorAt("1.1.0", "proto3"))

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; report=%+v; stderr=%s", code, rep, errOut)
	}
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-007" || v.Code == "POL-008" || v.Code == "POL-009" {
				t.Fatalf("unexpected %s for a proto3 contract, got %+v", v.Code, a)
			}
		}
	}
	if strings.Contains(errOut, "computed compatibility") {
		t.Fatalf("proto3 is entirely out of §5.4b's compat scope — no compat note should print at all, got stderr: %q", errOut)
	}
}

// TestValidateCI_ContractCompat_DedupOneVerdictPerContract is AC-970.2's
// own dedup acceptance: a PR changing five of one contract's schema/
// fixtures files produces exactly ONE compat verdict, not five.
// contract.md itself is deliberately left UNCHANGED in this PR so there is
// no second, unrelated V2-artifact report entry at the same path to
// confuse the count.
func TestValidateCI_ContractCompat_DedupOneVerdictPerContract(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciContractPriorCommit(t,
		ciContractDescriptorAt("1.0.0", "json-schema-2020-12"),
		`{"type":"object"}`,
		`{}`,
	)
	base := contractGitRevParse(t, root, "HEAD")

	contractWriteFile(t, root, ciContractSchemaPath, `{"type":"object","properties":{"z":{}}}`)
	fixA := ciContractDescriptorDir + "/fixtures/valid/a.json"
	fixB := ciContractDescriptorDir + "/fixtures/valid/b.json"
	fixC := ciContractDescriptorDir + "/fixtures/valid/c.json"
	contractWriteFile(t, root, fixA, `{}`)
	contractWriteFile(t, root, fixB, `{}`)
	contractWriteFile(t, root, fixC, `{}`)
	contractWriteFile(t, root, ciContractFixturePath, `{"changed":true}`)

	changed := []string{ciContractSchemaPath, fixA, fixB, fixC, ciContractFixturePath}
	code, rep, errOut := runCI(t, engine, root, fakeGit(changed...), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; report=%+v; stderr=%s", code, rep, errOut)
	}
	if len(rep.Artifacts) != 1 {
		t.Fatalf("5 changed files under one contract must produce exactly ONE report entry, got %d: %+v", len(rep.Artifacts), rep.Artifacts)
	}
	if rep.Artifacts[0].Path != ciContractDescriptorPath {
		t.Fatalf("Path = %q, want %q", rep.Artifacts[0].Path, ciContractDescriptorPath)
	}
}

// TestValidateCI_ContractCompat_UnreachableBaseIsAnErrorNotFirstPublish is
// this brief's discriminating check: an unreachable/bogus --base must
// refuse loudly, never read as "no descriptor at base, first publish,
// nothing to check" — that would silently wave a breaking change through
// under a benign reason (the same fail-open class wave A closed for
// contractReadTreeAtSHA itself). TEETH: reordering validateCIContract to
// read the prior descriptor via `git show` BEFORE reading the prior
// fixtures via contractReadTreeAtSHA reds this test (an unreachable base
// would then be reported as a benign first publish and exit 0).
func TestValidateCI_ContractCompat_UnreachableBaseIsAnErrorNotFirstPublish(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := ciContractPriorCommit(t,
		ciContractDescriptorAt("1.0.0", "json-schema-2020-12"),
		`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`,
		`{"x":"hi"}`,
	)
	// A well-formed but non-existent object id (contract_git_test.go's own
	// `missing` pattern) — deliberately NOT this repo's real prior commit.
	const unreachableBase = "0123456789abcdef0123456789abcdef01234567"

	contractWriteFile(t, root, ciContractDescriptorPath, ciContractDescriptorAt("1.1.0", "json-schema-2020-12"))
	contractWriteFile(t, root, ciContractSchemaPath, `{"type":"object","properties":{"x":{"type":"string"},"y":{"type":"string"}},"required":["x","y"]}`)

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath, ciContractSchemaPath), "v3-pr", unreachableBase, "ydnikolaev")
	if code == 0 {
		t.Fatalf("an unreachable --base must never exit 0; report=%+v; stderr=%s", rep, errOut)
	}
	var sawBaseError bool
	for _, a := range rep.Artifacts {
		if a.Error != "" && strings.Contains(a.Error, unreachableBase) {
			sawBaseError = true
		}
	}
	if !sawBaseError {
		t.Fatalf("expected an artifact error naming the unreachable base %s, got %+v", unreachableBase, rep.Artifacts)
	}
	if strings.Contains(errOut, "first publish") {
		t.Fatalf("an unreachable base must not be reported as a benign first-publish, got stderr: %q", errOut)
	}
}

// TestValidateCIAndContractHaveNoSecondCompatCopy is AC-970.2's own test:
// it proves the ONLY route to a POL-007/POL-008 verdict anywhere in
// package cli is a call into validate.CheckComputedCompatibility, never a
// hand-rolled re-implementation — by parsing this package's own non-test
// source (go/ast + go/parser, stdlib, no new dependency) for every file in
// this directory and asserting BOTH halves:
//
//   - NEGATIVE: no file imports a jsonschema compiler directly (the
//     pure-core boundary, D-F, says only internal/schema may do that), and
//     no file constructs the string literal "POL-007" or "POL-008"
//     anywhere — the real verdict lives entirely inside
//     validate.CheckComputedCompatibility/brokenBaseline (internal/
//     validate/compat.go), which return *Violation values with those codes
//     already set; a caller never spells the code itself.
//   - POSITIVE: cmd_validate_ci.go actually CALLS
//     validate.CheckComputedCompatibility at least once — the negative half
//     alone would stay green if the call were simply deleted, which is not
//     AC-970.2 ("refused... via the SAME exported core").
//
// _test.go files are excluded from the negative half on purpose: this very
// test file's own assertions (and the compat tests above) legitimately
// contain the literal "POL-007" to check FOR it, which is not a second
// copy of the verdict.
//
// TEETH: paste a copy of compat.go's failure branch —
// `violations = append(violations, validate.Violation{Code: "POL-007", ...})`
// — directly into any non-test file in this package (bypassing
// CheckComputedCompatibility) and the negative half reds on the literal-
// string check. Delete the validate.CheckComputedCompatibility call from
// cmd_validate_ci.go (e.g. replacing it with a hand-rolled fixture-
// revalidation loop, or simply removing the check) and the positive half
// reds even though the negative half stays green.
func TestValidateCIAndContractHaveNoSecondCompatCopy(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	// Every non-test file in package cli that is supposed to enforce the
	// rule must be seen CALLING the core. Asserting only one side lets the
	// other be deleted silently — which is the divergence, not its cure.
	mustCall := map[string]bool{"cmd_validate_ci.go": false, "cmd_contract.go": false}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		astFile, perr := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, imp := range astFile.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(strings.ToLower(importPath), "jsonschema") {
				t.Fatalf("%s imports %q directly — only internal/schema may compile an arbitrary JSON Schema (D-F); package cli must call validate.CheckComputedCompatibility instead of re-implementing it", name, importPath)
			}
		}
		ast.Inspect(astFile, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				// POL-009 is here for the same reason: CheckContractPublishable
				// is a two-consumer check exactly like the compat core, so a
				// hand-rolled D-D refusal in either file is the same defect.
				if val, uerr := strconv.Unquote(lit.Value); uerr == nil && (val == "POL-007" || val == "POL-008" || val == "POL-009") {
					pos := fset.Position(lit.Pos())
					t.Fatalf(
						"%s:%d constructs the literal code %q — POL-007/POL-008/POL-009 must come ONLY "+
							"from internal/validate's own returned Violation, never authored "+
							"directly in package cli (AC-970.2)",
						name, pos.Line, val,
					)
				}
			}
			if _, watched := mustCall[name]; watched {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "validate" && sel.Sel.Name == "CheckComputedCompatibility" {
							mustCall[name] = true
						}
					}
				}
			}
			return true
		})
	}
	for _, file := range []string{"cmd_contract.go", "cmd_validate_ci.go"} {
		if !mustCall[file] {
			t.Fatalf("%s does not call validate.CheckComputedCompatibility at all — AC-970.2 requires BOTH layers to read the SAME exported core, not merely its absence elsewhere", file)
		}
	}
}

// TestContractBumpKindHasOneClassifier guards the divergence wave B
// actually shipped and the propagation probe caught: two independently
// written bump classifiers, one comparing components with `>` and one with
// `!=`. They agreed on every input except a DOWNGRADE — publishing 1.0.0
// over a 2.0.0 baseline — where `contract publish` inferred "patch" and ran
// the strict fixture revalidation while `validate --ci` inferred "major"
// and skipped the check entirely. Nothing guards version monotonicity, so
// the input is reachable, and CI (the merge gate) was the fail-open side.
//
// TEETH: reintroduce a second classifier in package cli, or flip
// contractInferBumpKind's comparisons back to `>`, and this reds.
func TestContractBumpKindHasOneClassifier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		baseline, next contractSemver
		want           string
	}{
		{"major up", contractSemver{1, 2, 3}, contractSemver{2, 0, 0}, "major"},
		{"minor up", contractSemver{1, 2, 3}, contractSemver{1, 3, 0}, "minor"},
		{"patch up", contractSemver{1, 2, 3}, contractSemver{1, 2, 4}, "patch"},
		{"unchanged is a patch, so an omitted bump is still checked", contractSemver{1, 2, 3}, contractSemver{1, 2, 3}, "patch"},
		{"a MAJOR DOWNGRADE is a major change, never a quiet patch", contractSemver{2, 0, 0}, contractSemver{1, 0, 0}, "major"},
		{"a minor downgrade likewise", contractSemver{1, 5, 0}, contractSemver{1, 4, 0}, "minor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := contractInferBumpKind(tc.baseline, tc.next); got != tc.want {
				t.Fatalf("contractInferBumpKind(%v, %v) = %q, want %q", tc.baseline, tc.next, got, tc.want)
			}
		})
	}

	// And there must be exactly ONE of them in the package.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	var classifiers []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		astFile, perr := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, decl := range astFile.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
				continue
			}
			// Any package-level func taking two contractSemver values and
			// returning a string is a bump classifier by shape.
			if len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 2 {
				continue
			}
			paramType, ok := fn.Type.Params.List[0].Type.(*ast.Ident)
			if !ok || paramType.Name != "contractSemver" {
				continue
			}
			if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			if resType, ok := fn.Type.Results.List[0].Type.(*ast.Ident); ok && resType.Name == "string" {
				classifiers = append(classifiers, name+":"+fn.Name.Name)
			}
		}
	}
	if len(classifiers) != 1 {
		t.Fatalf("package cli has %d bump classifiers (%v) — both enforcement layers must read ONE, or they will disagree on an edge nobody tested", len(classifiers), classifiers)
	}
}
