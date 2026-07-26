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

// ciArtifactPaths returns the report's entry paths, EXCLUDING the manifest.
//
// Since 2026-07-26 a full-repo audit always validates space.yaml, so it appears
// in every full-repo report. Assertions about "which artifacts were walked"
// mean the artifacts, and rewriting each expected count as N+1 would hide what
// the number is about. ciManifestChecked asserts the other half explicitly.
func ciArtifactPaths(rep ciReport) []string {
	var out []string
	for _, a := range rep.Artifacts {
		if a.Path == "space.yaml" {
			continue
		}
		out = append(out, a.Path)
	}
	return out
}

// ciManifestChecked asserts the report carries a VALID verdict for the space's
// own manifest — the file diff-authz authorises every write against, and which
// nothing validated at all before this.
func ciManifestChecked(t *testing.T, rep ciReport) {
	t.Helper()
	for _, a := range rep.Artifacts {
		if a.Path != "space.yaml" {
			continue
		}
		if a.Result == nil || !a.Result.Valid {
			t.Errorf("space.yaml verdict is not a clean pass: %+v", a)
		}
		return
	}
	t.Error("the report carries no verdict for space.yaml — an audit that skips the file governing " +
		"the space is not an audit, and its absence is invisible from the exit code")
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
	if !rep.Valid || len(ciArtifactPaths(rep)) != 2 {
		t.Fatalf("expected 2 valid artifacts (README excluded), got %+v", ciArtifactPaths(rep))
	}
	ciManifestChecked(t, rep)
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
	if !rep.Valid || len(ciArtifactPaths(rep)) != 0 {
		t.Fatalf("empty repo should be clean, got %+v", rep)
	}
	// An empty space is not an UNCHECKED space: the manifest is still audited,
	// and that is the one thing there is to audit here.
	ciManifestChecked(t, rep)
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
	if rep.Mode != "v3-full-repo" || !rep.Valid || len(ciArtifactPaths(rep)) != 1 {
		t.Fatalf("unexpected report through Run: %+v", rep)
	}
	ciManifestChecked(t, rep)
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

// ciBadManifest is a manifest the SCHEMA rejects: the participant omits `org`
// and `joined`, both of which schemas/manifest/v1/space.schema.json lists as
// required. It is not a contrived shape — it is exactly what came out of
// writing a manifest by hand while following the published from-zero
// walkthrough on 2026-07-26, and it was accepted by `a2a connect`, by
// `a2a doctor`, and by `validate --ci --mode=v3-full-repo`, which answered
// `"valid": true`.
const ciBadManifest = `schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    section: axon/
    status: active
    owners: [ydnikolaev]
  - system: seomatrix
    section: seomatrix/
    status: active
    owners: [misha-gh]
vendored: []
`

// TestValidateCI_ManifestIsValidatedWhenItChanges is the regression for a hole
// that was three layers deep and open at every one of them.
//
// `space.yaml` is the document that decides WHO MAY WRITE WHERE — diff-authz
// authorises every pull request against its participants and sections. A schema
// for it exists; a validator seam for it exists (`space.LoadManifest`); an
// adapter implementing that seam against the real corpus exists
// (`cli.ManifestValidatorAdapter`). On 2026-07-26 a grep established that
// LoadManifest had ZERO production callers and the adapter was constructed
// nowhere outside its own tests. Every production path used
// `space.ParseManifest`, a shape-only decode.
//
// So the trust document could be merged in a shape the schema forbids, and the
// guard that reads it went on reading it.
//
// The pair below is the whole test, and the SECOND case is the one that decides
// whether this is shippable:
//
//   - a change that touches space.yaml with a bad manifest must RED;
//   - a change that touches only an artifact, with that same bad manifest
//     already sitting on main, must NOT red.
//
// Without the second, an old manifest becomes a tripwire on every unrelated
// write in the space — and the operator's only route out is a manifest PR that
// the gate itself is blocking.
func TestValidateCI_ManifestIsValidatedWhenItChanges(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	t.Run("a PR that changes space.yaml reds on a schema-invalid manifest", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciBadManifest, nil)

		code, rep, _ := runCI(t, engine, root, fakeGit("space.yaml"), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("a manifest the schema rejects was merged clean: code=%d valid=%v — this is the "+
				"document diff-authz authorises every write against", code, rep.Valid)
		}
		var found bool
		for _, a := range rep.Artifacts {
			if a.Path != "space.yaml" {
				continue
			}
			found = true
			if a.Result == nil || a.Result.Valid {
				t.Fatalf("space.yaml reported valid: %+v", a)
			}
			var named bool
			for _, v := range a.Result.Violations {
				if strings.Contains(v.Path, "participants") {
					named = true
				}
			}
			if !named {
				t.Errorf("violations do not name the offending field: %+v — a reader has to be able to "+
					"fix it without guessing", a.Result.Violations)
			}
		}
		if !found {
			t.Fatal("the report carries no entry for space.yaml at all — the check never ran, which is " +
				"the failure this test exists to catch and is invisible from the exit code alone")
		}
	})

	t.Run("a PR touching only an artifact does NOT red on a pre-existing bad manifest", func(t *testing.T) {
		t.Parallel()
		rel := "axon/exchanges/XQ-axon-20260730-ab12.md"
		root := ciRepo(t, ciBadManifest, map[string]string{rel: validQuestion("XQ-axon-20260730-ab12", "axon", "seomatrix")})

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("an ordinary artifact PR was reddened by a manifest it did not touch: code=%d "+
				"valid=%v stderr=%s\n"+
				"That turns a manifest merged before this check existed into a tripwire on every write, "+
				"and the way out — a manifest PR — is blocked by the same gate.", code, rep.Valid, errOut)
		}
		for _, a := range rep.Artifacts {
			if a.Path == "space.yaml" {
				t.Errorf("the report mentions space.yaml for a PR that never touched it: %+v", a)
			}
		}
	})

	t.Run("a full-repo audit always checks the manifest", func(t *testing.T) {
		t.Parallel()
		// walkArtifacts yields artifacts, never space.yaml — so an audit that
		// only looked at the changed set would skip the file governing the
		// space. There is no merge to block here, so there is no tripwire
		// concern to trade against.
		root := ciRepo(t, ciBadManifest, nil)

		code, rep, _ := runCI(t, engine, root, fakeGit(), "v3-full-repo", "", "")
		if code == 0 || rep.Valid {
			t.Fatalf("a full-repo audit passed a space whose own manifest is schema-invalid: code=%d "+
				"valid=%v — auditing everything except the file that governs it is not an audit",
				code, rep.Valid)
		}
	})

	t.Run("a valid manifest passes, and every manifest we actually operate is valid", func(t *testing.T) {
		t.Parallel()
		// ciSpaceYAML is the fixture shape; it must pass, or this gate would
		// red every space in existence. Checked before wiring, not after: the
		// production (r22d222/a2a), live-e2e and template manifests were each
		// run through the corpus by hand on 2026-07-26 and all three passed.
		root := ciRepo(t, ciSpaceYAML, nil)

		code, rep, errOut := runCI(t, engine, root, fakeGit("space.yaml"), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("the fixture manifest was rejected: code=%d valid=%v stderr=%s", code, rep.Valid, errOut)
		}
	})

	t.Run("a nested space.yaml is not the space's manifest", func(t *testing.T) {
		t.Parallel()
		// Matching on the basename would validate the ROOT manifest while
		// reporting a path the author never touched — and would let any
		// same-named file inside a section drag the check in.
		root := ciRepo(t, ciBadManifest, map[string]string{"axon/docs/space.yaml": "not: a manifest\n"})

		_, rep, _ := runCI(t, engine, root, fakeGit("axon/docs/space.yaml"), "v3-pr", "deadbeef", "ydnikolaev")
		for _, a := range rep.Artifacts {
			if a.Path == "space.yaml" {
				t.Errorf("a nested file named space.yaml pulled in the root manifest check: %+v", a)
			}
		}
	})
}

// ciEventDoc is a valid event/v1 document, optionally stamped.
func ciEventDoc(stamped bool) string {
	s := "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5G\nspace: getvisa\n" +
		"subject: XQ-axon-20260730-ab12\ntransition: submit\n" +
		"actor: {kind: agent, name: bot, system: axon}\nat: 2026-07-30T10:00:00Z\n"
	if stamped {
		s += "produced_by:\n  tool: a2a\n  version: \"0.10.0\"\n"
	}
	return s
}

// ciManifestWithFloor is ciSpaceYAML at a given min_binary_version.
func ciManifestWithFloor(floor string) string {
	return strings.Replace(ciSpaceYAML, "min_binary_version: 0.1.0", "min_binary_version: "+floor, 1)
}

// TestValidateCI_EventMustNameItsProducer is the server-side half of the
// producer stamp, and the ONLY place an old binary can actually be stopped.
//
// `min_binary_version` is checked inside the WRITER's own binary, which binds a
// binary that honours it and does nothing whatever to one that does not. That is
// not hypothetical: before 0.9.0 every lifecycle and contract verb wrote with an
// EMPTY floor because the shared request builder never populated it, so raising
// a space's floor could not have gated those writes even in principle.
//
// The four cases below are two pairs, and the FLOOR pair is the one that decides
// whether this is shippable at all. event/v1 is additionalProperties:false and a
// space's CI validator is pinned BY THE SPACE, so requiring the stamp in a space
// whose validator predates the field would refuse every write over an unknown
// field — worse than the problem. Below the floor the rule must be silent, not
// lenient-but-noisy: no verdict at all.
func TestValidateCI_EventMustNameItsProducer(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const rel = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml"

	t.Run("floor reached, event unstamped: red, naming the remedy", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{rel: ciEventDoc(false)})

		code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("an unstamped event merged clean into a space whose floor requires the stamp: "+
				"code=%d valid=%v", code, rep.Valid)
		}
		var found bool
		for _, a := range rep.Artifacts {
			if a.Path != rel {
				continue
			}
			found = true
			if a.Result == nil || a.Result.Valid {
				t.Fatalf("the event reported valid: %+v", a)
			}
			msg := a.Result.Violations[0].Message
			for _, want := range []string{"a2a update", "min_binary_version", "INSIDE the writer"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message is missing %q — a reader must learn both the fix and why the "+
						"client-side floor could not have caught this\ngot: %s", want, msg)
				}
			}
		}
		if !found {
			t.Fatal("no verdict for the event at all — the check never ran, which the exit code alone " +
				"cannot distinguish from a pass")
		}
	})

	t.Run("floor reached, event stamped: clean", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{rel: ciEventDoc(true)})

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a stamped event was refused: code=%d valid=%v stderr=%s", code, rep.Valid, errOut)
		}
	})

	t.Run("floor NOT reached, event unstamped: schema-checked, but no stamp violation", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.9.1"), map[string]string{rel: ciEventDoc(false)})

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a space below the floor was reddened by a rule not in force there: code=%d "+
				"valid=%v stderr=%s", code, rep.Valid, errOut)
		}
		// An ENTRY is expected and correct — the event's schema is validated in
		// every space, floor or no floor. What must be absent is the producer
		// violation: below the floor that rule is not in force, and a note on
		// every write in every unmigrated space is noise, which is how a real
		// finding gets missed.
		var seen bool
		for _, a := range rep.Artifacts {
			if a.Path != rel {
				continue
			}
			seen = true
			if a.Result == nil {
				t.Fatalf("no result for the event: %+v", a)
			}
			for _, v := range a.Result.Violations {
				if strings.Contains(v.Path, "produced_by") {
					t.Errorf("the producer rule fired in a space whose floor has not reached it: %+v", v)
				}
			}
		}
		if !seen {
			t.Error("the event was not validated at all — its schema is checked regardless of the floor")
		}
	})

	t.Run("floor NOT reached, event stamped: also clean", func(t *testing.T) {
		t.Parallel()
		// The writer only stamps once the floor is reached, so this shape is
		// unusual — but a hand-written or replayed event could carry it, and it
		// must not be treated as a violation of anything.
		root := ciRepo(t, ciManifestWithFloor("0.9.1"), map[string]string{rel: ciEventDoc(true)})

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a stamped event in a pre-floor space was refused: code=%d valid=%v stderr=%s",
				code, rep.Valid, errOut)
		}
	})

	t.Run("a non-event yaml in the section is not checked", func(t *testing.T) {
		t.Parallel()
		// isEventDocument matches the /events/ segment, not the extension: a
		// consumes.yaml or a docs/ yaml is not an event, and requiring a
		// producer stamp on one would red an ordinary write.
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
			"axon/docs/notes.yaml": "anything: at all\n",
		})
		code, rep, errOut := runCI(t, engine, root, fakeGit("axon/docs/notes.yaml"), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a non-event yaml was treated as an event: code=%d valid=%v stderr=%s",
				code, rep.Valid, errOut)
		}
	})
}

// TestValidateCI_EventSchemaIsChecked pins the half that did not exist at all
// until 2026-07-26: a changed event merged as long as it was parseable YAML,
// and the fold read it afterwards.
//
// It is the same shape as the two holes closed the same day — a schema that
// exists and documents nobody fed to it — and it was wired only AFTER every
// event in both live spaces (46 of them) had been run through this schema by
// hand and passed. A gate is not switched on to find out whether it reds.
//
// Unlike the producer stamp, this half is NOT floor-gated: event/v1 has been the
// event schema since v0.1.0, so there is no space where an event is legitimately
// not an event.
func TestValidateCI_EventSchemaIsChecked(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const rel = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml"

	t.Run("an event missing a required field reds", func(t *testing.T) {
		t.Parallel()
		// No `transition`, which event/v1 requires — and which the fold reads to
		// decide what happened to the artifact.
		bad := "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5G\nspace: getvisa\n" +
			"subject: XQ-axon-20260730-ab12\nactor: {kind: agent, name: bot, system: axon}\n" +
			"at: 2026-07-30T10:00:00Z\nproduced_by:\n  tool: a2a\n  version: \"0.10.0\"\n"
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{rel: bad})

		code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("an event missing a schema-required field merged clean: code=%d valid=%v",
				code, rep.Valid)
		}
	})

	t.Run("an unquoted timestamp does not break the validator", func(t *testing.T) {
		t.Parallel()
		// `at:` is a timestamp, and yaml.v3 auto-types an unquoted one as a
		// time.Time — which the JSON-schema validator cannot represent, so it
		// aborts with "unmapped schema-class keyword" INSTEAD of validating.
		// That exact failure was hit twice today on manifests before the decode
		// was normalised; an event carries the same hazard on every single write.
		good := ciEventDoc(true) // `at: 2026-07-30T10:00:00Z`, unquoted
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{rel: good})

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a valid event with an unquoted timestamp was refused: code=%d valid=%v stderr=%s",
				code, rep.Valid, errOut)
		}
		for _, a := range rep.Artifacts {
			if a.Path == rel && a.Error != "" {
				t.Fatalf("the validator errored rather than validating: %s\n"+
					"An engine error here means the instance was not normalised — the timestamp reached "+
					"the schema as a time.Time.", a.Error)
			}
		}
	})

	t.Run("a document that is not YAML at all is POL-002, not a crash", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{rel: "\tthis: [is not\n  valid yaml\n"})

		code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("an unparseable event merged clean: code=%d valid=%v", code, rep.Valid)
		}
	})
}
