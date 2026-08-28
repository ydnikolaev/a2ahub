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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// cliFixtureThread is the §3.8 thread every hand-built envelope fixture in
// this file carries. Since P46 the schema REQUIRES the field and every
// drafting verb mints or inherits one, so a threadless fixture no longer
// represents a document this product can produce. (Declared here as well as in
// the package's external test file: the two are separate Go packages, `cli`
// and `cli_test`, and cannot share an identifier.)
const cliFixtureThread = "thread:axon-20260721-k3f9"

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
		"thread: " + cliFixtureThread + "\n" +
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

// possessionREF017ConjunctionArtifact returns an envelope whose body trips
// checkPossession's REF-017 conjunction (internal/validate/possession.go):
// an undeclared sha256: digest, a >=3-line file-tree enumeration, and no
// declared attachment at all — the founding-incident shape.
func possessionREF017ConjunctionArtifact(id, from, to string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: Test question\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"category: defect\n" +
		"priority: p2\n" +
		"blocking: true\n" +
		"expected_response: {shape: \"an answer\"}\n" +
		"classification: internal\n" +
		"---\n" +
		"review_digest sha256:" + strings.Repeat("a", 64) + "\n\n" +
		"The bundle contains:\n" +
		"- c0.schema.json\n" +
		"- protocol.json\n" +
		"- errors.json\n"
}

// TestValidateCI_REF017ScopedToPRModeNotFullRepo is ADR-011 decision 3
// (fb-20260812-e6d189): the SAME artifact — the REF-017 conjunction shape
// (undeclared digest + file-tree enumeration + zero declared attachments)
// — is refused under v3-pr (the write gate, where refusing still prevents
// a merge) and passes clean under v3-full-repo (the post-merge audit,
// which can only punish immutable history). One artifact, two modes: that
// is what proves the scoping rather than proving two different fixtures.
func TestValidateCI_REF017ScopedToPRModeNotFullRepo(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XQ-axon-20260730-r017.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: possessionREF017ConjunctionArtifact("XQ-axon-20260730-r017", "axon", "seomatrix"),
	})

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code == 0 || rep.Valid {
		t.Fatalf("expected v3-pr to refuse the REF-017 conjunction, got code=%d valid=%v report=%+v stderr=%s", code, rep.Valid, rep, errOut)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil {
		t.Fatalf("expected one artifact result, got %+v", rep.Artifacts)
	}
	if v := violationWithCodeCI(rep.Artifacts[0].Result.Violations, "REF-017"); v == nil {
		t.Fatalf("expected REF-017 in the v3-pr result, got %+v", rep.Artifacts[0].Result.Violations)
	} else if v.Severity != "reject" {
		t.Fatalf("expected REF-017 SeverityReject in v3-pr, got %q", v.Severity)
	}

	code, rep, errOut = runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 0 || !rep.Valid {
		t.Fatalf("expected v3-full-repo to suppress REF-017 and pass, got code=%d valid=%v report=%+v stderr=%s", code, rep.Valid, rep, errOut)
	}
	// v3-full-repo unconditionally validates space.yaml too (this file's own
	// "v3-full-repo validates it UNCONDITIONALLY" comment above), so the
	// report carries a second artifact besides rel — find rel by path
	// rather than assuming a fixed length.
	var artifactResult *validate.Result
	for _, a := range rep.Artifacts {
		if a.Path == rel {
			artifactResult = a.Result
		}
	}
	if artifactResult == nil {
		t.Fatalf("expected a result for %s, got %+v", rel, rep.Artifacts)
	}
	if v := violationWithCodeCI(artifactResult.Violations, "REF-017"); v != nil {
		t.Fatalf("expected REF-017 suppressed in v3-full-repo, got %+v", artifactResult.Violations)
	}
	if v := violationWithCodeCI(artifactResult.Violations, "POL-017"); v == nil {
		t.Fatalf("expected POL-017 to still surface in v3-full-repo (never suppressed), got %+v", artifactResult.Violations)
	}
}

// violationWithCodeCI mirrors internal/validate's own package-private
// violationWithCode helper — separate packages, cannot share it.
func violationWithCodeCI(vs []validate.Violation, code string) *validate.Violation {
	for i := range vs {
		if vs[i].Code == code {
			return &vs[i]
		}
	}
	return nil
}

func TestValidateCIWorkCheckpointMountsV3AgainstBaseAndHead(t *testing.T) {
	t.Parallel()
	pair := newCIWorkPair(t, nil, false)
	code, report, stderr := runCI(t, ciEngine(t), pair.root, fakeGit(pair.artifactPath, pair.eventPath), "v3-pr", pair.base, "axon-bot")
	if code != 0 || !report.Valid {
		t.Fatalf("valid V3 checkpoint refused: code=%d stderr=%s report=%+v", code, stderr, report)
	}
	code, report, stderr = runCI(t, ciEngine(t), pair.root, nil, "v3-full-repo", "", "")
	if code != 0 || !report.Valid {
		t.Fatalf("valid full-repo V3 checkpoint refused: code=%d stderr=%s report=%+v", code, stderr, report)
	}
	if err := os.WriteFile(filepath.Join(pair.root, filepath.FromSlash(pair.artifactPath)), []byte(strings.Replace(pair.artifactRaw, "summary: Work", "summary: ghp_abcdefghijklmnopqrstuvwxyz1234567890", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, report, _ = runCI(t, ciEngine(t), pair.root, fakeGit(pair.artifactPath, pair.eventPath), "v3-pr", pair.base, "axon-bot")
	if code == 0 || report.Valid {
		t.Fatalf("secret-bearing manual work checkpoint passed V3: %+v", report)
	}
	if err := os.WriteFile(filepath.Join(pair.root, filepath.FromSlash(pair.artifactPath)), []byte(strings.Replace(pair.artifactRaw, "to: [beta]", "to: [ghost]", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, report, _ = runCI(t, ciEngine(t), pair.root, fakeGit(pair.artifactPath, pair.eventPath), "v3-pr", pair.base, "axon-bot")
	if code == 0 || report.Valid {
		t.Fatalf("unknown work recipient passed canonical validation: %+v", report)
	}
}

func TestValidateCIWorkCheckpointRequiresExactPublishPairProvenance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, want string
		edit       func(string) string
		split      bool
	}{
		{name: "wrong transition", want: "publish/published", edit: func(raw string) string { return strings.Replace(raw, "transition: publish", "transition: submit", 1) }},
		{name: "wrong state", want: "publish/published", edit: func(raw string) string { return strings.Replace(raw, "state: published", "state: submitted", 1) }},
		{name: "wrong actor", want: "actor does not match", edit: func(raw string) string { return strings.Replace(raw, "name: codex", "name: other", 1) }},
		{name: "unrelated event", want: "no exact publish/published candidate event", edit: func(raw string) string {
			return strings.Replace(raw, "subject: XA-axon-20260803-b3c4", "subject: XA-axon-20260803-c3d4", 1)
		}},
		{name: "split commits", want: "not introduced in one first-parent commit", split: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pair := newCIWorkPair(t, test.edit, test.split)
			for _, mode := range []string{"v3-pr", "v3-full-repo"} {
				git := fakeGit(pair.artifactPath, pair.eventPath)
				base := pair.base
				if mode == "v3-full-repo" {
					git, base = nil, ""
				}
				code, report, _ := runCI(t, ciEngine(t), pair.root, git, mode, base, "axon-bot")
				if code == 0 || report.Valid || !ciReportHasError(report, test.want) {
					t.Fatalf("invalid %s pair passed or wrong refusal: code=%d want=%q report=%+v", mode, code, test.want, report)
				}
			}
		})
	}
}

func TestValidateCIFullRepoIgnoresLaterSameSubjectNonPublishEventForPairing(t *testing.T) {
	t.Parallel()
	pair := newCIWorkPair(t, nil, false)
	noteID := "01J5A1B2C3D4E5F6G7H8J9K0M2"
	notePath := "axon/events/2026/" + noteID + ".yaml"
	note := "schema: event/v1\nevent: " + noteID + "\nspace: fixture-space\nsubject: XA-axon-20260803-b3c4\ntransition: note\nactor: {kind: agent, name: codex, system: axon}\nat: 2026-08-03T11:00:00Z\nnote: Still working.\nproduced_by: {tool: a2a, version: \"0.19.0\"}\n"
	absolute := filepath.Join(pair.root, filepath.FromSlash(notePath))
	if err := os.WriteFile(absolute, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	ciTestGit(t, pair.root, "add", "--", notePath)
	ciTestGit(t, pair.root, "commit", "-m", "later checkpoint note")
	code, report, stderr := runCI(t, ciEngine(t), pair.root, nil, "v3-full-repo", "", "")
	if code != 0 || !report.Valid {
		t.Fatalf("same-subject note displaced exact publish pair: code=%d stderr=%s report=%+v", code, stderr, report)
	}
}

type ciWorkPairFixture struct {
	root, base, artifactPath, eventPath, artifactRaw string
}

func newCIWorkPair(t *testing.T, editEvent func(string) string, split bool) ciWorkPairFixture {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	root := fx.Clone("axon")
	base := strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
	id := "XA-axon-20260803-b3c4"
	eventID := "01J5A1B2C3D4E5F6G7H8J9K0M1"
	artifactPath := "axon/exchanges/" + id + ".md"
	eventPath := "axon/events/2026/" + eventID + ".yaml"
	artifactRaw := "---\nschema: envelope/v2\nid: " + id + "\ntype: announcement\ntitle: Work checkpoint\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: codex, session: \"session:01K20ABCDEFHJKMNPQRSTVWXYZ\"}\ncreated: 2026-08-03T10:15:00Z\ncategory: status\npriority: p2\nblocking: false\nack_requested: false\nclassification: internal\nthread: thread:axon-20260803-a2b3\nwork: {id: \"work:01K20ABCDEFHJKMNPQRSTVWXYZ\", semantic_sequence: 1, mode: implementing, subject_ref: \"thread:axon-20260803-a2b3\", summary: Work, reported_at: \"2026-08-03T10:15:00Z\", valid_until: \"2026-08-04T10:15:00Z\"}\n---\nWork.\n"
	eventRaw := "schema: event/v1\nevent: " + eventID + "\nspace: fixture-space\nsubject: " + id + "\ntransition: publish\nstate: published\nactor: {kind: agent, name: codex, system: axon, session: \"session:01K20ABCDEFHJKMNPQRSTVWXYZ\"}\nat: 2026-08-03T10:15:00Z\nproduced_by: {tool: a2a, version: \"0.19.0\"}\n"
	if editEvent != nil {
		eventRaw = editEvent(eventRaw)
	}
	for path, raw := range map[string]string{artifactPath: artifactRaw, eventPath: eventRaw} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if split {
		ciTestGit(t, root, "add", "--", artifactPath)
		ciTestGit(t, root, "commit", "-m", "manual work artifact")
		ciTestGit(t, root, "add", "--", eventPath)
		ciTestGit(t, root, "commit", "-m", "manual work event")
	} else {
		ciTestGit(t, root, "add", "--", artifactPath, eventPath)
		ciTestGit(t, root, "commit", "-m", "manual work checkpoint")
	}
	return ciWorkPairFixture{root: root, base: base, artifactPath: artifactPath, eventPath: eventPath, artifactRaw: artifactRaw}
}

func ciReportHasError(report ciReport, want string) bool {
	for _, artifact := range report.Artifacts {
		if strings.Contains(artifact.Error, want) {
			return true
		}
	}
	return false
}

func ciTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a2a", "GIT_AUTHOR_EMAIL=a@invalid", "GIT_COMMITTER_NAME=a2a", "GIT_COMMITTER_EMAIL=a@invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
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
		"thread: " + cliFixtureThread + "\n" +
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
		"thread: " + cliFixtureThread + "\n" +
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
		"thread: " + cliFixtureThread + "\n" +
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
// the new merge-side compat check (space.ContractForPath matches any
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
		"axon/provides/content-feed/schema/main.schema.json":   `{"type":"object"}`,
		"axon/provides/content-feed/fixtures/valid/ok.json":    `{}`,
		"axon/provides/content-feed/fixtures/invalid/bad.json": `null`,
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

// --- CC-024/CC-025 (supersession graph, §3.8): the two corner cases the
// full-repo scan collects from every committed `supersede` event and
// checks exactly once against validate.CheckSupersessionGraph ---

// TestValidateCI_FullRepoSupersessionForkRed is CC-024: two successors
// (two separately-committed supersede events) both claim the same
// predecessor. v3-full-repo must red, naming REF-020.
func TestValidateCI_FullRepoSupersessionForkRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		predecessor = "XW-axon-20260731-aaaa"
		successor1  = "XW-axon-20260801-bbbb"
		successor2  = "XW-axon-20260801-cccc"
	)
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E60.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E60", predecessor, "supersede", "axon", "", successor1),
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E61.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E61", predecessor, "supersede", "axon", "", successor2),
	})

	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code == 0 || rep.Valid {
		t.Fatalf("a supersession fork merged clean: code=%d report=%+v stderr=%s", code, rep, errOut)
	}
	if !ciReportHasViolation(rep, "<supersession-graph>", "REF-020") {
		t.Fatalf("fork did not produce REF-020: %+v", rep)
	}
}

// TestValidateCI_FullRepoSupersessionCycleRed is CC-025: two supersede
// events whose predecessor/successor pairs close a loop. v3-full-repo must
// red, naming REF-021.
func TestValidateCI_FullRepoSupersessionCycleRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		nodeA = "XW-axon-20260731-aaaa"
		nodeB = "XW-axon-20260801-bbbb"
	)
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E62.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E62", nodeA, "supersede", "axon", "", nodeB),
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E63.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E63", nodeB, "supersede", "axon", "", nodeA),
	})

	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code == 0 || rep.Valid {
		t.Fatalf("a supersession cycle merged clean: code=%d report=%+v stderr=%s", code, rep, errOut)
	}
	if !ciReportHasViolation(rep, "<supersession-graph>", "REF-021") {
		t.Fatalf("cycle did not produce REF-021: %+v", rep)
	}
}

// TestValidateCI_FullRepoSupersessionLinearChainClean is the corresponding
// green: a genuine chain of supersede events (never forking, never
// cycling) must never trip either code.
func TestValidateCI_FullRepoSupersessionLinearChainClean(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		oldest = "XW-axon-20260701-aaaa"
		middle = "XW-axon-20260731-bbbb"
		newest = "XW-axon-20260801-cccc"
	)
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E70.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E70", oldest, "supersede", "axon", "", middle),
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E71.yaml": ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3E71", middle, "supersede", "axon", "", newest),
	})

	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 0 || !rep.Valid {
		t.Fatalf("a clean linear supersession chain was reddened: code=%d report=%+v stderr=%s", code, rep, errOut)
	}
	if ciReportHasViolation(rep, "<supersession-graph>", "REF-020") || ciReportHasViolation(rep, "<supersession-graph>", "REF-021") {
		t.Fatalf("a linear chain must never trip the fork or cycle code: %+v", rep)
	}
}

// TestValidateCI_PRDoesNotRunSupersessionGraph proves v3-pr never builds or
// checks the supersession graph: a PR diff carries only a partial slice of
// the repo's supersede events, so running this check there could report a
// fork that does not exist repo-wide. The fixture shapes a REAL fork
// (base commits one supersede event, the PR candidate adds a second
// successor claiming the same predecessor) and asserts REF-020 never
// appears — regardless of what the lifecycle-legality check itself does
// with a second supersede over an already-superseded subject, which is not
// this test's concern.
func TestValidateCI_PRDoesNotRunSupersessionGraph(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		predecessor = "XW-axon-20260731-aaaa"
		successor1  = "XW-axon-20260801-bbbb"
		successor2  = "XW-axon-20260801-cccc"
		rel1        = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E64.yaml"
		rel2        = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E65.yaml"
	)
	root, base := ciLifecycleCandidateRepo(t, map[string]string{
		rel1: ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E64", predecessor, "supersede", "axon", "", successor1),
	}, rel2, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E65", predecessor, "supersede", "axon", "", successor2))

	_, rep, _ := runCI(t, engine, root, fakeGit(rel2), "v3-pr", base, "ydnikolaev")
	if ciReportHasViolation(rep, "<supersession-graph>", "REF-020") {
		t.Fatalf("v3-pr ran the supersession graph check, which it must never do: %+v", rep)
	}
	for _, a := range rep.Artifacts {
		if a.Path == "<supersession-graph>" {
			t.Fatalf("v3-pr must never emit a supersession-graph report entry at all: %+v", rep)
		}
	}
}

// TestWalkArtifactsExcludesDataPackagePayload is a direct, unit-level red
// receipt for walkArtifacts' own exclusion (AC8, spec 04 §11): a package's
// README.md must never appear in walkArtifacts' own returned slice, and a
// genuine artifact under the same system still does — proven independently
// of runValidateCI's own report shape, one call into the unexported function
// itself, since package cli's test file can call it directly.
func TestWalkArtifactsExcludesDataPackagePayload(t *testing.T) {
	t.Parallel()

	packageID, packageFiles := realDataPackageFixture(t, "seomatrix")
	dpDir := "seomatrix/data/" + packageID + "/"
	const genuineRel = "seomatrix/exchanges/XQ-seomatrix-20260808-h2k8.md"

	files := map[string]string{
		genuineRel: validQuestion("XQ-seomatrix-20260808-h2k8", "seomatrix", "axon"),
	}
	for rel, content := range packageFiles {
		files[dpDir+rel] = content
	}
	root := ciRepo(t, ciSpaceYAML, files)

	manifest, err := space.ParseManifest([]byte(ciSpaceYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	out, err := walkArtifacts(root, manifest)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}

	got := map[string]bool{}
	for _, p := range out {
		got[filepath.ToSlash(p)] = true
	}
	if got[dpDir+"README.md"] {
		t.Fatalf("walkArtifacts returned the packed README.md, want it excluded: %v", out)
	}
	if !got[genuineRel] {
		t.Fatalf("walkArtifacts must still return a genuine artifact under the same system, got %v", out)
	}
}

// --- AC8 (spec 04 §11, amendment 2026-08-09): a directory `a2a data pack`
// writes must pass `a2a validate --ci` BY CONSTRUCTION, and the exemption
// must be scoped to the package's own directory, not a blanket "stop
// validating this system" ---

// fakeDataPackEntryChecker always passes every dataset entry: this fixture
// only needs Pack to produce a schema-valid manifest carrying a real
// RoleReadme entry, not to exercise conformance checking itself (that is
// internal/datapackage's own tests' job).
type fakeDataPackEntryChecker struct{}

func (fakeDataPackEntryChecker) CheckEntry(_ context.Context, req datapackage.EntryCheckRequest) (datapackage.Check, error) {
	return datapackage.NewCheck("chk-ok", req.Entry.RelPath, datapackage.CheckPass, nil), nil
}

// realDataPackageFixture packs a REAL data-package/v1 manifest via
// internal/datapackage.Pack — the exact core `a2a data pack` calls
// (cmd/a2a/data_wiring.go) — declaring README.md with RoleReadme exactly as
// that file's entry classification does (README.md -> RoleReadme, no
// frontmatter), then stages it the way writeStagedPackage does:
// manifest.json holding json.Marshal(document), and every entry's bytes
// copied byte-identical. Returns the minted DP- id and a relPath->content
// map ready to drop into ciRepo under "<system>/data/<id>/".
func realDataPackageFixture(t *testing.T, system string) (packageID string, files map[string]string) {
	t.Helper()
	source := t.TempDir()
	const readme = "# Orders export\n\nA data package's own README — no frontmatter, because it is not an envelope draft.\n"
	const orders = `[{"id":1},{"id":2}]`
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "orders.json"), []byte(orders), 0o644); err != nil {
		t.Fatalf("write orders.json: %v", err)
	}

	req := datapackage.PackRequest{
		Source:         source,
		System:         system,
		Thread:         cliFixtureThread,
		Contract:       "XC-" + system + "-orders@1.0.0#sha256:" + strings.Repeat("a", 64),
		Classification: "restricted",
		DataProfile:    datapackage.DataProfileSynthetic,
		Format:         datapackage.FormatJSON,
		Locator:        "checkout-core/deliveries/DP-test",
		Entries: map[string]datapackage.EntryDeclaration{
			"README.md":   {Role: datapackage.RoleReadme, MediaType: "text/markdown"},
			"orders.json": {Role: datapackage.RoleDataset, MediaType: "application/json", ConformsTo: "schema/orders.schema.json"},
		},
		Expires:    30 * 24 * time.Hour,
		Attempt:    1,
		Bounds:     datapackage.DefaultBounds(),
		Checker:    fakeDataPackEntryChecker{},
		Provenance: datapackage.Provenance{OriginSystem: system + "-orders-db", ExtractedAt: "2026-08-08T11:59:30Z"},
		Now:        time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
		Entropy:    bytes.NewReader([]byte{0x7a, 0xaa, 0, 0}),
	}
	doc, err := datapackage.Pack(context.Background(), req)
	if err != nil {
		t.Fatalf("datapackage.Pack: %v", err)
	}

	manifestRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal(doc): %v", err)
	}

	return doc.ID, map[string]string{
		"manifest.json": string(manifestRaw),
		"README.md":     readme,
		"orders.json":   orders,
	}
}

// assertDataPackagePayloadExempt asserts NO report entry exists for any path
// under dpDir — the package's payload never reached artifact discovery at
// all, exactly as space.ContractForPath's own schema/fixtures/companion
// subtree does not.
func assertDataPackagePayloadExempt(t *testing.T, rep ciReport, dpDir string) {
	t.Helper()
	for _, a := range rep.Artifacts {
		if strings.HasPrefix(a.Path, dpDir) {
			t.Fatalf("a package payload path was validated as an artifact and must not have been: %+v", a)
		}
	}
}

// assertControlArtifactRefused asserts the report carries exactly one entry
// for controlRel and it is refused (error or an invalid V2 result).
func assertControlArtifactRefused(t *testing.T, rep ciReport, controlRel string) {
	t.Helper()
	var sawControl bool
	for _, a := range rep.Artifacts {
		if a.Path != controlRel {
			continue
		}
		sawControl = true
		if a.Error == "" && (a.Result == nil || a.Result.Valid) {
			t.Fatalf("control artifact %q should be refused, got %+v", controlRel, a)
		}
	}
	if !sawControl {
		t.Fatalf("expected a report entry for the control artifact %q, got %+v", controlRel, rep.Artifacts)
	}
}

// TestValidateCI_PRDataPackagePayloadPassesByConstruction is AC8's v3-pr
// GREEN half, asserted on its own: a PR carrying exactly what `a2a data
// pack` + `a2a data deliver` commit — the manifest plus its byte-identical
// payload, including the packed README.md with no frontmatter — is green,
// with no other changed file to make that verdict ambiguous.
func TestValidateCI_PRDataPackagePayloadPassesByConstruction(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	packageID, packageFiles := realDataPackageFixture(t, "seomatrix")
	dpDir := "seomatrix/data/" + packageID + "/"

	files := map[string]string{}
	var changed []string
	for rel, content := range packageFiles {
		p := dpDir + rel
		files[p] = content
		changed = append(changed, p)
	}

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, fakeGit(changed...), "v3-pr", "deadbeef", "misha-gh")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid {
		t.Fatalf("expected a valid report, got %+v", rep)
	}
	assertDataPackagePayloadExempt(t, rep, dpDir)
}

// TestValidateCI_PRDataPackagePayloadExemptionIsNotBlanket is the control:
// the SAME package payload alongside a genuinely malformed artifact filed
// elsewhere under the SAME system still reds on the control — proving the
// exemption above is the package's own directory grammar, not "stop
// validating seomatrix". Remove the exclusion this brief adds and both this
// test's control assertion AND the green test above go red (the latter on
// the packed README.md itself, POL-002 — the exact incident).
func TestValidateCI_PRDataPackagePayloadExemptionIsNotBlanket(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	packageID, packageFiles := realDataPackageFixture(t, "seomatrix")
	dpDir := "seomatrix/data/" + packageID + "/"

	const controlRel = "seomatrix/exchanges/XQ-seomatrix-20260808-ffff.md"
	files := map[string]string{
		controlRel: "this is not frontmatter\n",
	}
	var changed []string
	for rel, content := range packageFiles {
		p := dpDir + rel
		files[p] = content
		changed = append(changed, p)
	}
	changed = append(changed, controlRel)

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, fakeGit(changed...), "v3-pr", "deadbeef", "misha-gh")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the control artifact must still red); stderr=%s; report=%+v", code, errOut, rep)
	}
	if rep.Valid {
		t.Fatalf("expected an invalid report (the control artifact), got %+v", rep)
	}
	assertDataPackagePayloadExempt(t, rep, dpDir)
	assertControlArtifactRefused(t, rep, controlRel)
}

// TestValidateCI_FullRepoDataPackagePayloadPassesByConstruction is AC8's
// v3-full-repo GREEN half — walkArtifacts must exempt the package directory
// exactly as the changed-file loop does above, asserted on its own.
func TestValidateCI_FullRepoDataPackagePayloadPassesByConstruction(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	packageID, packageFiles := realDataPackageFixture(t, "seomatrix")
	dpDir := "seomatrix/data/" + packageID + "/"

	files := map[string]string{}
	for rel, content := range packageFiles {
		files[dpDir+rel] = content
	}

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid {
		t.Fatalf("expected a valid report, got %+v", rep)
	}
	assertDataPackagePayloadExempt(t, rep, dpDir)
	ciManifestChecked(t, rep)
}

// TestValidateCI_FullRepoDataPackagePayloadExemptionIsNotBlanket is the
// v3-full-repo control, mirroring TestValidateCI_PRDataPackagePayloadExemptionIsNotBlanket.
func TestValidateCI_FullRepoDataPackagePayloadExemptionIsNotBlanket(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	packageID, packageFiles := realDataPackageFixture(t, "seomatrix")
	dpDir := "seomatrix/data/" + packageID + "/"

	const controlRel = "seomatrix/exchanges/XQ-seomatrix-20260808-ffff.md"
	files := map[string]string{
		controlRel: "this is not frontmatter\n",
	}
	for rel, content := range packageFiles {
		files[dpDir+rel] = content
	}

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the control artifact must still red); stderr=%s; report=%+v", code, errOut, rep)
	}
	if rep.Valid {
		t.Fatalf("expected an invalid report (the control artifact), got %+v", rep)
	}
	assertDataPackagePayloadExempt(t, rep, dpDir)
	assertControlArtifactRefused(t, rep, controlRel)
}

// --- P10 (agent-exchange-2026-08) spec 10 wave B: the blob equivalent of
// the AC8 carve-out above. Wave A's own handoff (plans/10-blob-primitive.
// plan.md's wave log): `a2a attach` now lands its payload in the space the
// same structural way `a2a data pack`/`deliver` already do
// (space.DeliverBlob, "<system>/blobs/<BL-id>/..."), and had no equivalent
// carve-out — so a blob payload's own frontmatter-bearing .md file would be
// walked as an envelope draft. These tests are the red-first receipt: run
// them against isBlobPayloadPath removed from cmd_validate_ci.go's two call
// sites and TestWalkArtifactsExcludesBlobPayload /
// TestValidateCI_PRBlobPayloadPassesByConstruction both fail on the packed
// README.md itself (a missing-required-fields V2 refusal) ---

// realBlobFixture builds a blob's own directory (space.BlobForPath's
// "<system>/blobs/<BL-id>/..." grammar), including the digest sidecar
// (blob.json — its CONTENT is irrelevant to this carve-out, which asks only
// about PATH shape, never sidecar content) and a payload file carrying real
// envelope frontmatter (type/schema set, everything else missing) — the
// exact hazard wave A named: a blob payload's own .md file must never be
// walked as an envelope draft. Returns the minted BL- id and a
// relPath->content map ready to drop into ciRepo under
// "<system>/blobs/<id>/".
func realBlobFixture(t *testing.T, system string) (blobID string, files map[string]string) {
	t.Helper()
	blobID = "BL-" + system + "-20260811-aaaa"
	const readme = "---\nschema: envelope/v2\ntype: work_request\ntitle: not an envelope draft\n---\n\n" +
		"A blob's own payload file, sealed by blob.json's digest sidecar — never an envelope draft.\n"
	return blobID, map[string]string{
		"blob.json": `{"id":"` + blobID + `","digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		"README.md": readme,
	}
}

// assertBlobPayloadExempt mirrors assertDataPackagePayloadExempt's own
// shape for a blob's own directory.
func assertBlobPayloadExempt(t *testing.T, rep ciReport, blobDir string) {
	t.Helper()
	for _, a := range rep.Artifacts {
		if strings.HasPrefix(a.Path, blobDir) {
			t.Fatalf("a blob payload path was validated as an artifact and must not have been: %+v", a)
		}
	}
}

// TestWalkArtifactsExcludesBlobPayload is a direct, unit-level red receipt
// for walkArtifacts' own exclusion, mirroring
// TestWalkArtifactsExcludesDataPackagePayload: a blob's own README.md must
// never appear in walkArtifacts' own returned slice, and a genuine artifact
// under the same system still does.
func TestWalkArtifactsExcludesBlobPayload(t *testing.T) {
	t.Parallel()

	blobID, blobFiles := realBlobFixture(t, "seomatrix")
	blobDir := "seomatrix/blobs/" + blobID + "/"
	const genuineRel = "seomatrix/exchanges/XQ-seomatrix-20260808-h2k8.md"

	files := map[string]string{
		genuineRel: validQuestion("XQ-seomatrix-20260808-h2k8", "seomatrix", "axon"),
	}
	for rel, content := range blobFiles {
		files[blobDir+rel] = content
	}
	root := ciRepo(t, ciSpaceYAML, files)

	manifest, err := space.ParseManifest([]byte(ciSpaceYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	out, err := walkArtifacts(root, manifest)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}

	got := map[string]bool{}
	for _, p := range out {
		got[filepath.ToSlash(p)] = true
	}
	if got[blobDir+"README.md"] {
		t.Fatalf("walkArtifacts returned the blob's own README.md, want it excluded: %v", out)
	}
	if !got[genuineRel] {
		t.Fatalf("walkArtifacts must still return a genuine artifact under the same system, got %v", out)
	}
}

// TestValidateCI_PRBlobPayloadPassesByConstruction is the v3-pr GREEN half,
// mirroring TestValidateCI_PRDataPackagePayloadPassesByConstruction: a PR
// carrying exactly what `a2a attach` commits — the payload plus its own
// digest sidecar, including a packed README.md with real but incomplete
// envelope frontmatter — is green, with no other changed file to make that
// verdict ambiguous. Remove isBlobPayloadPath from the changed-file loop's
// call site and this test reds on the README.md itself (missing required
// V2 fields).
func TestValidateCI_PRBlobPayloadPassesByConstruction(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	blobID, blobFiles := realBlobFixture(t, "seomatrix")
	blobDir := "seomatrix/blobs/" + blobID + "/"

	files := map[string]string{}
	var changed []string
	for rel, content := range blobFiles {
		p := blobDir + rel
		files[p] = content
		changed = append(changed, p)
	}

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, fakeGit(changed...), "v3-pr", "deadbeef", "misha-gh")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	if !rep.Valid {
		t.Fatalf("expected a valid report, got %+v", rep)
	}
	assertBlobPayloadExempt(t, rep, blobDir)
}

// TestValidateCI_PRBlobPayloadExemptionIsNotBlanket is the control,
// mirroring TestValidateCI_PRDataPackagePayloadExemptionIsNotBlanket: the
// SAME blob payload alongside a genuinely malformed artifact filed
// elsewhere under the SAME system still reds on the control, proving the
// exemption is the blob's own directory grammar, not "stop validating
// seomatrix".
func TestValidateCI_PRBlobPayloadExemptionIsNotBlanket(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	blobID, blobFiles := realBlobFixture(t, "seomatrix")
	blobDir := "seomatrix/blobs/" + blobID + "/"

	const controlRel = "seomatrix/exchanges/XQ-seomatrix-20260808-ffff.md"
	files := map[string]string{
		controlRel: "this is not frontmatter\n",
	}
	var changed []string
	for rel, content := range blobFiles {
		p := blobDir + rel
		files[p] = content
		changed = append(changed, p)
	}
	changed = append(changed, controlRel)

	root := ciRepo(t, ciSpaceYAML, files)
	code, rep, errOut := runCI(t, engine, root, fakeGit(changed...), "v3-pr", "deadbeef", "misha-gh")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the control artifact must still red); stderr=%s; report=%+v", code, errOut, rep)
	}
	if rep.Valid {
		t.Fatalf("expected an invalid report (the control artifact), got %+v", rep)
	}
	assertBlobPayloadExempt(t, rep, blobDir)
	assertControlArtifactRefused(t, rep, controlRel)
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

// TestValidateCI_MissingManifestRefusalNamesConnectedSpaceMirrors is spec 04
// AC-1: from a participant repo with no space.yaml, the refusal names the
// precondition (a manifest is required), the caller's own state (this cwd
// is a participant project), and the synced mirror path for EVERY
// connected space — "a repo with two connected spaces must name both"
// (spec 04 §6).
func TestValidateCI_MissingManifestRefusalNamesConnectedSpaceMirrors(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := t.TempDir() // no space.yaml — a participant project checkout
	if err := os.MkdirAll(filepath.Join(root, ".a2a"), 0o755); err != nil {
		t.Fatalf("mkdir .a2a: %v", err)
	}
	projectConfig := "system: axon\nspaces:\n  - id: getvisa\n    repo_url: https://example.invalid/getvisa.git\n  - id: seomatrix\n    repo_url: https://example.invalid/seomatrix.git\n"
	if err := os.WriteFile(filepath.Join(root, ".a2a", "config.yaml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatalf("write .a2a/config.yaml: %v", err)
	}

	var stderr bytes.Buffer
	code := runValidateCI(context.Background(), engine, root, fakeGit(), "v3-full-repo", "", "", IO{Stdout: &bytes.Buffer{}, Stderr: &stderr})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (missing manifest); stderr=%s", code, stderr.String())
	}
	got := stderr.String()

	// The precondition + the caller's own state.
	for _, want := range []string{"space.yaml", "participant project", root} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q does not name %q", got, want)
		}
	}
	// Both connected spaces, each with its own synced mirror path.
	wantGetvisaMirror := filepath.Join(root, ".a2a", "cache", "mirrors", "getvisa")
	wantSeomatrixMirror := filepath.Join(root, ".a2a", "cache", "mirrors", "seomatrix")
	for _, want := range []string{"getvisa", wantGetvisaMirror, "seomatrix", wantSeomatrixMirror} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q does not name %q (two connected spaces must both be named)", got, want)
		}
	}
}

// TestValidateCI_MissingManifestRefusalNoConnectedSpaceStillNamesNextStep
// covers the zero-connected-spaces edge: the refusal still names a next
// step (NewRefusal refuses an empty one at construction) even when there
// is no mirror to redirect to.
func TestValidateCI_MissingManifestRefusalNoConnectedSpaceStillNamesNextStep(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root := t.TempDir() // no space.yaml, no .a2a/config.yaml at all

	var stderr bytes.Buffer
	code := runValidateCI(context.Background(), engine, root, fakeGit(), "v3-full-repo", "", "", IO{Stdout: &bytes.Buffer{}, Stderr: &stderr})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (missing manifest); stderr=%s", code, stderr.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "a2a init") {
		t.Errorf("refusal %q does not name a next step for the zero-connected-space case", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("refusal must not be empty")
	}
}

// TestResolveConnectedSpace is spec 04 AC-3: "--space with an unknown id
// refuses, naming the ids that ARE connected" — a table test over the
// resolution helper, not an e2e (spec 04's own instruction).
func TestResolveConnectedSpace(t *testing.T) {
	t.Parallel()
	twoSpaces := []space.Ref{{ID: "getvisa"}, {ID: "seomatrix"}}

	cases := []struct {
		name       string
		id         string
		refs       []space.Ref
		wantFound  bool
		wantNames  []string // substrings the returned error must contain
		wantAbsent []string // substrings the returned error must NOT contain
	}{
		{
			name:      "known id resolves with no error",
			id:        "getvisa",
			refs:      twoSpaces,
			wantFound: true,
		},
		{
			name:      "unknown id names both connected ids",
			id:        "nope",
			refs:      twoSpaces,
			wantNames: []string{"nope", "getvisa", "seomatrix"},
		},
		{
			name:      "empty connected-space list names none, points at init",
			id:        "getvisa",
			refs:      nil,
			wantNames: []string{"getvisa", "a2a init"},
		},
		{
			name:       "unknown id among one connected space names only that one",
			id:         "nope",
			refs:       []space.Ref{{ID: "getvisa"}},
			wantNames:  []string{"nope", "getvisa"},
			wantAbsent: []string{"seomatrix"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref, err := resolveConnectedSpace(tc.id, tc.refs)
			if tc.wantFound {
				if err != nil {
					t.Fatalf("resolveConnectedSpace(%q, %v) = %v, want nil error", tc.id, tc.refs, err)
				}
				if ref.ID != tc.id {
					t.Fatalf("resolveConnectedSpace(%q, %v).ID = %q, want %q", tc.id, tc.refs, ref.ID, tc.id)
				}
				return
			}
			if err == nil {
				t.Fatalf("resolveConnectedSpace(%q, %v) = nil error, want a refusal naming the connected ids", tc.id, tc.refs)
			}
			for _, want := range tc.wantNames {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("resolveConnectedSpace(%q, %v) error %q does not name %q", tc.id, tc.refs, err.Error(), want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("resolveConnectedSpace(%q, %v) error %q wrongly names %q", tc.id, tc.refs, err.Error(), absent)
				}
			}
		})
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
	ciContractDescriptorDir      = "axon/provides/widget"
	ciContractDescriptorPath     = ciContractDescriptorDir + "/contract.md"
	ciContractSchemaPath         = ciContractDescriptorDir + "/schema/main.schema.json"
	ciContractFixtureRel         = "fixtures/valid/ok.json" // relative to ciContractDescriptorDir
	ciContractFixturePath        = ciContractDescriptorDir + "/" + ciContractFixtureRel
	ciContractInvalidFixturePath = ciContractDescriptorDir + "/fixtures/invalid/bad.json"
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
	contractWriteFile(t, root, ciContractInvalidFixturePath, `null`)
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
	contractWriteFile(t, root, ciContractInvalidFixturePath, `null`)

	code, rep, errOut := runCI(t, engine, root, fakeGit(ciContractDescriptorPath, ciContractSchemaPath, ciContractFixturePath, ciContractInvalidFixturePath), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; report=%+v; stderr=%s", code, rep, errOut)
	}
	if !strings.Contains(errOut, "first publish") {
		t.Fatalf("stderr must explicitly say this is a first publish (never silence), got: %q", errOut)
	}
}

// TestValidateCI_ContractCompat_Proto3NotChecked proves the §5.4b format
// gate: a proto3 (non-JSON-Schema) contract is never handed to
// CheckComputedCompatibility. The format-neutral §5.3/POL-009 baseline is
// present independently, so this test isolates only the deep-check carve-out.
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
	// CI is the remaining validation consumer. Contract publication now
	// delegates to the shared P6 planner through an injected interface and
	// must not carry a second transport-local compatibility implementation.
	mustCall := map[string]bool{"cmd_validate_ci.go": false}
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
	for _, file := range []string{"cmd_validate_ci.go"} {
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

	t.Run("a pre-existing ambiguous authority map fails closed before diff authz", func(t *testing.T) {
		t.Parallel()
		ambiguous := strings.Replace(ciSpaceYAML, "owners: [misha-gh]", "owners: [ydnikolaev]", 1)
		rel := "axon/exchanges/XQ-axon-20260730-ab12.md"
		root := ciRepo(t, ambiguous, map[string]string{
			rel: validQuestion("XQ-axon-20260730-ab12", "axon", "seomatrix"),
		})

		code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("ambiguous active owner mapping authorized a PR: code=%d report=%+v", code, rep)
		}
		var found bool
		for _, artifact := range rep.Artifacts {
			if artifact.Path != "space.yaml" || artifact.Result == nil {
				continue
			}
			for _, violation := range artifact.Result.Violations {
				if violation.Code == "REF-013" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("report does not expose REF-013 authority failure: %+v", rep)
		}
		if len(rep.DiffAuthz) != 0 {
			t.Fatalf("diff authz ran against an authority map already known ambiguous: %+v", rep.DiffAuthz)
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

// ciEventCandidateRepo models a new submit event in a PR: the merge-base has
// only the manifest, while the PR head adds both the artifact envelope and the
// event document. The event path is left uncommitted so validate --ci reads it
// from the head working tree while reading prior history from base.
func ciEventCandidateRepo(t *testing.T, manifest, event string) (root, base string) {
	t.Helper()
	root = ciRepo(t, manifest, nil)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "space.yaml")
	contractGitRun(t, root, "commit", "-q", "-m", "space base")
	base = contractGitRevParse(t, root, "HEAD")

	contractWriteFile(t, root,
		"axon/exchanges/XQ-axon-20260730-ab12.md",
		validQuestion("XQ-axon-20260730-ab12", "axon", "seomatrix"),
	)
	contractWriteFile(t, root,
		"axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml",
		event,
	)
	return root, base
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
		root, base := ciEventCandidateRepo(t, ciManifestWithFloor("0.10.0"), ciEventDoc(true))

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("a stamped event was refused: code=%d valid=%v stderr=%s", code, rep.Valid, errOut)
		}
	})

	t.Run("floor NOT reached, event unstamped: schema-checked, but no stamp violation", func(t *testing.T) {
		t.Parallel()
		root, base := ciEventCandidateRepo(t, ciManifestWithFloor("0.9.1"), ciEventDoc(false))

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
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
		root, base := ciEventCandidateRepo(t, ciManifestWithFloor("0.9.1"), ciEventDoc(true))

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
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
		root, base := ciEventCandidateRepo(t, ciManifestWithFloor("0.10.0"), good)

		code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
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

func ciLifecycleEvent(id, subject, transition, actorSystem, version string, refs ...string) string {
	out := "schema: event/v1\n" +
		"event: " + id + "\n" +
		"space: getvisa\n" +
		"subject: " + subject + "\n" +
		"transition: " + transition + "\n" +
		"actor: {kind: agent, name: bot, system: " + actorSystem + "}\n" +
		"at: 2026-07-30T10:00:00Z\n" +
		"produced_by:\n  tool: a2a\n  version: \"0.10.0\"\n"
	if version != "" {
		out += "version: \"" + version + "\"\n"
	}
	if len(refs) > 0 {
		out += "refs:\n"
		for _, ref := range refs {
			out += "  - ref: " + ref + "\n"
		}
	}
	return out
}

func ciLifecycleCandidateRepo(
	t *testing.T,
	baseFiles map[string]string,
	candidatePath, candidate string,
) (root, base string) {
	t.Helper()
	root = ciRepo(t, ciManifestWithFloor("0.10.0"), baseFiles)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "merge base")
	base = contractGitRevParse(t, root, "HEAD")
	contractWriteFile(t, root, candidatePath, candidate)
	return root, base
}

func ciResponseEnvelope(id, parent string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: response\n" +
		"title: Test response\n" +
		"space: getvisa\n" +
		"from: seomatrix\n" +
		"to: [axon]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"parent: " + parent + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-30T14:03:00Z\n" +
		"---\nBody.\n"
}

// TestValidateCI_EventLifecycleCandidates is AC-1106.1. Each candidate first
// passes the real event schema and then reaches validate's existing LFC mapping
// through fold's canonical legality primitive against merge-base history.
func TestValidateCI_EventLifecycleCandidates(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		questionID   = "XQ-axon-20260730-ab12"
		questionPath = "axon/exchanges/XQ-axon-20260730-ab12.md"
		submitPath   = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E50.yaml"
		ackPath      = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3E51.yaml"
	)
	submit := ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E50", questionID, "submit", "axon", "")

	t.Run("legal candidate is green", func(t *testing.T) {
		t.Parallel()
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			submitPath:   submit,
		}, ackPath, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E51", questionID, "acknowledge", "seomatrix", ""))

		code, rep, errOut := runCI(t, engine, root, fakeGit(ackPath), "v3-pr", base, "misha-gh")
		if code != 0 || !rep.Valid {
			t.Fatalf("legal candidate refused: code=%d report=%+v stderr=%s", code, rep, errOut)
		}
	})

	t.Run("transition-free note is green", func(t *testing.T) {
		t.Parallel()
		const path = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3E56.yaml"
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			submitPath:   submit,
		}, path, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E56", questionID, "note", "seomatrix", "")+"note: still working\n")

		code, rep, errOut := runCI(t, engine, root, fakeGit(path), "v3-pr", base, "misha-gh")
		if code != 0 || !rep.Valid {
			t.Fatalf("transition-free note refused: code=%d report=%+v stderr=%s", code, rep, errOut)
		}
	})

	t.Run("illegal transition is LFC-001", func(t *testing.T) {
		t.Parallel()
		const path = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E52.yaml"
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			submitPath:   submit,
		}, path, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E52", questionID, "submit", "axon", ""))

		code, rep, _ := runCI(t, engine, root, fakeGit(path), "v3-pr", base, "ydnikolaev")
		if code != 1 || rep.Valid || !ciReportHasViolation(rep, path, "LFC-001") {
			t.Fatalf("illegal candidate did not produce LFC-001: code=%d report=%+v", code, rep)
		}
	})

	t.Run("unauthorized actor is LFC-002", func(t *testing.T) {
		t.Parallel()
		const path = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E53.yaml"
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			submitPath:   submit,
		}, path, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E53", questionID, "acknowledge", "axon", ""))

		code, rep, _ := runCI(t, engine, root, fakeGit(path), "v3-pr", base, "ydnikolaev")
		if code != 1 || rep.Valid || !ciReportHasViolation(rep, path, "LFC-002") {
			t.Fatalf("unauthorized candidate did not produce LFC-002: code=%d report=%+v", code, rep)
		}
	})

	t.Run("modified candidate is excluded from prior", func(t *testing.T) {
		t.Parallel()
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			submitPath:   submit,
		}, submitPath, submit+"\nnote: rewritten without changing the transition\n")

		code, rep, errOut := runCI(t, engine, root, fakeGit(submitPath), "v3-pr", base, "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("candidate was folded into its own prior: code=%d report=%+v stderr=%s", code, rep, errOut)
		}
	})

	t.Run("response-scoped verify uses parent closure", func(t *testing.T) {
		t.Parallel()
		const (
			responseID   = "XS-seomatrix-20260730-rs12"
			responsePath = "seomatrix/exchanges/XS-seomatrix-20260730-rs12.md"
			ackBasePath  = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3E53.yaml"
			respondPath  = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3E54.yaml"
			verifyPath   = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E55.yaml"
		)
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			questionPath: validQuestion(questionID, "axon", "seomatrix"),
			responsePath: ciResponseEnvelope(responseID, questionID),
			submitPath:   submit,
			ackBasePath: ciLifecycleEvent(
				"01J40A7M9P1S3V5W7Y9A1C3E53", questionID, "acknowledge", "seomatrix", "",
			),
			respondPath: ciLifecycleEvent(
				"01J40A7M9P1S3V5W7Y9A1C3E54", questionID, "respond", "seomatrix", "", responseID,
			),
		}, verifyPath, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E55", responseID, "verify", "axon", ""))

		code, rep, errOut := runCI(t, engine, root, fakeGit(verifyPath), "v3-pr", base, "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("legal response-scoped verify refused: code=%d result=%+v stderr=%s", code, *rep.Artifacts[0].Result, errOut)
		}
	})

	t.Run("contract version candidate uses versioned prior", func(t *testing.T) {
		t.Parallel()
		const (
			contractID   = "XC-axon-widget"
			contractPath = "axon/provides/widget/contract.md"
			publish1Path = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E56.yaml"
			publish2Path = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E57.yaml"
		)
		root, base := ciLifecycleCandidateRepo(t, map[string]string{
			contractPath: validContract("axon", "widget"),
			publish1Path: ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E56", contractID, "publish", "axon", "1.0.0"),
		}, publish2Path, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3E57", contractID, "publish", "axon", "2.0.0"))

		code, rep, errOut := runCI(t, engine, root, fakeGit(publish2Path), "v3-pr", base, "ydnikolaev")
		if code != 0 || !rep.Valid {
			t.Fatalf("legal versioned publish refused: code=%d report=%+v stderr=%s", code, rep, errOut)
		}
	})
}

func ciReportHasViolation(report ciReport, path, code string) bool {
	for _, artifact := range report.Artifacts {
		if artifact.Path != path || artifact.Result == nil {
			continue
		}
		for _, violation := range artifact.Result.Violations {
			if violation.Code == code {
				return true
			}
		}
	}
	return false
}

// declaredV2Contract is validContract's envelope/v2 sibling: the shape
// `a2a new contract` renders and the publication planner requires once a
// space's floor reaches contract.ContractPublicationFloor.
func declaredV2Contract(from, slug string) string {
	return "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: Test contract\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [seomatrix]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"category: data-feed\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"0.0.0\"\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"artifacts:\n" +
		"  - {path: schema/" + slug + ".schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/" + slug + ".json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/" + slug + ".schema.json}\n" +
		"  - {path: fixtures/invalid/" + slug + ".json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/" + slug + ".schema.json}\n" +
		"---\nBody.\n"
}

// contractShapeSidecars is the schema/fixture trio every publishable
// JSON-Schema contract carries, keyed at the paths the descriptor declares.
func contractShapeSidecars(slug string) map[string]string {
	dir := "axon/provides/" + slug + "/"
	return map[string]string{
		dir + "schema/" + slug + ".schema.json":    `{"type":"object"}`,
		dir + "fixtures/valid/" + slug + ".json":   `{}`,
		dir + "fixtures/invalid/" + slug + ".json": `null`,
	}
}

// TestValidateCI_ContractV1DescriptorAboveFloorRed is the regression for
// fb-20260806-3539ac: an envelope/v1 descriptor in a space whose floor has
// reached the contract-set-v2 profile can NEVER publish, so the merge gate
// must refuse it here rather than let it merge and strand the author at
// `a2a contract preflight`.
func TestValidateCI_ContractV1DescriptorAboveFloorRed(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	files := contractShapeSidecars("content-feed")
	files[rel] = validContract("axon", "content-feed")
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), files)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (a v1 descriptor above the floor can never publish); report=%+v", code, rep)
	}
	var refusal string
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-013" && v.Path == "artifacts" {
				refusal = v.Message
			}
		}
	}
	if refusal == "" {
		t.Fatalf("expected a POL-013 artifacts refusal naming the declared-v2 requirement, got %+v", rep.Artifacts)
	}
	// The refusal has to carry its own remedy — that absence is what the
	// external report was actually about.
	for _, want := range []string{"envelope/v2", "artifacts:", "a2a template show contract"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal does not name %q, so it is not actionable: %s", want, refusal)
		}
	}
}

// TestValidateCI_ContractDeclaredV2AboveFloorClean is the other half: the
// shape the tool itself renders must pass the gate that refuses the old one.
func TestValidateCI_ContractDeclaredV2AboveFloorClean(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	files := contractShapeSidecars("content-feed")
	files[rel] = declaredV2Contract("axon", "content-feed")
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), files)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-013" {
				t.Fatalf("a declared-v2 descriptor above the floor must not be refused for its shape: %s", v.Message)
			}
		}
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
}

// TestValidateCI_ContractV1DescriptorBelowFloorClean pins the migration
// guarantee: a space that has NOT raised its floor keeps publishing the
// legacy fixed tree, and this gate must not redden it.
func TestValidateCI_ContractV1DescriptorBelowFloorClean(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	files := contractShapeSidecars("content-feed")
	files[rel] = validContract("axon", "content-feed")
	root := ciRepo(t, ciManifestWithFloor("0.18.0"), files)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (below the floor the v1 tree is still the publishable shape); stderr=%s; report=%+v", code, errOut, rep)
	}
}

// nonBindingV2Contract is a schema-valid declared-v2 descriptor that
// publishes NO schema/valid-fixture/invalid-fixture roles — only a single
// "other"-role artifact, which is what satisfies contract.schema.json's
// unconditional `artifacts` minItems:1 requirement without contradicting
// "carries no [schema/fixture] artifacts" (specs/05-declared-nature.md,
// 2026-08-10 amendment). xBindingBlock is inserted as-is, so a caller may
// pass the `x_binding: none` sentinel line, a long-form mapping, or "" for
// undeclared.
func nonBindingV2Contract(from, slug, xBindingBlock string) string {
	return "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: Test contract\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [seomatrix]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: claude, model: claude-fable-5}\n" +
		"created: 2026-07-30T14:02:00Z\n" +
		"category: data-feed\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"0.0.0\"\n" +
		"schema_format: other\n" +
		"compat_policy: default\n" +
		xBindingBlock +
		"artifacts:\n" +
		"  - {path: artifacts/review.md, role: other, normative: false, media_type: text/markdown}\n" +
		"---\nBody.\n"
}

// TestValidateCI_ContractNonBindingSentinelRelaxesPublishability is P5
// US-1's own reachable half (specs/05-declared-nature.md, 2026-08-10
// amendment, §8 AC2): a contract declaring itself non-binding, and
// publishing no schema/fixture roles, merges clean — the exact shape a
// non-binding review bundle or an agreed definition with no
// machine-checkable grammar needs.
func TestValidateCI_ContractNonBindingSentinelRelaxesPublishability(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), map[string]string{
		rel: nonBindingV2Contract("axon", "content-feed", "x_binding: none\n"),
	})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author non-binding content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-009" || v.Code == "POL-013" {
				t.Fatalf("a non-binding contract with no artifacts to check must not be refused for lacking them: %s", v.Message)
			}
		}
	}
}

// TestValidateCI_ContractNonBindingLongFormNoClaimRelaxesPublishability is
// the long-form half of the same fact: `compatibility_status: none`
// relaxes identically to the bare sentinel, per the schema's own T2
// asymmetry.
func TestValidateCI_ContractNonBindingLongFormNoClaimRelaxesPublishability(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), map[string]string{
		rel: nonBindingV2Contract("axon", "content-feed",
			"x_binding:\n  artifact_class: non_binding_review\n  compatibility_status: none\n  adoptable: false\n  runtime_pinnable: false\n"),
	})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author non-binding content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s; report=%+v", code, errOut, rep)
	}
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-009" || v.Code == "POL-013" {
				t.Fatalf("a long-form compatibility_status: none must relax identically to the sentinel: %s", v.Message)
			}
		}
	}
}

// TestValidateCI_ContractDeclaringAdoptableFalseWithARealClaimStillNeedsTheBaseline
// is the merge-gate half of the relaxation's Direction 2: `adoptable:
// false` alone does not relax §5.3 — only declaring NO compatibility claim
// does. A long form naming a real compatibility_status while publishing
// nothing is still an unverified claim, and POL-009 must still catch it,
// or the relaxation degrades into "any x_binding disables POL-009".
func TestValidateCI_ContractDeclaringAdoptableFalseWithARealClaimStillNeedsTheBaseline(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), map[string]string{
		rel: nonBindingV2Contract("axon", "content-feed",
			"x_binding:\n  artifact_class: reference_impl\n  compatibility_status: strict-semver\n  adoptable: false\n  runtime_pinnable: false\n"),
	})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code == 0 {
		t.Fatalf("expected a POL-009 refusal (a real compatibility claim with nothing to check it against), got exit 0; report=%+v", rep)
	}
	var sawPOL009 bool
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == "POL-009" {
				sawPOL009 = true
			}
		}
	}
	if !sawPOL009 {
		t.Fatalf("expected POL-009 among the violations, got %+v", rep.Artifacts)
	}
}

// TestValidateCI_ContractXBindingUntypedValueIsSchemaInvalid proves the
// justification for declaring `x_binding` in contract.schema.json's own
// `properties` (rather than relying solely on the `^x_` patternProperties
// escape): without it, `x_binding: garbage` would pass unchecked. With it,
// a scalar that is not the literal `none` satisfies neither half of the
// field's own oneOf and the envelope-validation report entry must be red.
func TestValidateCI_ContractXBindingUntypedValueIsSchemaInvalid(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/provides/content-feed/contract.md"
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), map[string]string{
		rel: nonBindingV2Contract("axon", "content-feed", "x_binding: garbage\n"),
	})
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "author content-feed")
	base := contractGitRevParse(t, root, "HEAD")

	code, rep, _ := runCI(t, engine, root, fakeGit(rel), "v3-pr", base, "ydnikolaev")
	if code == 0 {
		t.Fatalf("x_binding: garbage must be schema-invalid, got exit 0; report=%+v", rep)
	}
	var sawInvalidResult bool
	for _, a := range rep.Artifacts {
		if a.Result != nil && !a.Result.Valid {
			sawInvalidResult = true
		}
	}
	if !sawInvalidResult {
		t.Fatalf("expected at least one report entry with a decoded invalid Result, got %+v", rep.Artifacts)
	}
}

// TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex is the production-path
// half of P6 wave 25B, and it exists because the wave that WIRED REF-019
// reached no caller that offers a Resolver — so the rule was reachable from a
// unit test and inert everywhere else, which is the exact defect ("a rule that
// is true and inert") threat-model T5 names and this epic has now found four
// times. `validate --ci` is the merge-time path a space actually pins, the same
// reasoning P6 already recorded for REF-018.
//
// Both directions are asserted. Without them the test would pass against an
// engine that refuses every verdict, or against one that refuses none.
func TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	const parentRel = "axon/exchange/XW-axon-20260730-cr01.md"
	const eventRel = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5H.yaml"

	// A parent declaring exactly TWO acceptance criteria: indices 0 and 1 are
	// in range, 2 is not.
	parent := "---\nschema: envelope/v1\nid: XW-axon-20260730-cr01\ntype: work_request\n" +
		"title: two criteria\nspace: getvisa\nfrom: axon\nto: [peer]\n" +
		"actor: {kind: agent, name: bot, model: m}\ncreated: 2026-07-30T09:00:00Z\n" +
		"category: data\npriority: p3\nblocking: false\n" +
		"interim_behavior: \"we wait\"\nneeded_by: 2026-08-01\n" +
		"acceptance_criteria:\n  - \"first\"\n  - \"second\"\n" +
		"thread: thread:axon-20260730-cr01\nclassification: internal\n---\nbody\n"

	closeEvent := func(index int) string {
		return "schema: event/v2\n" +
			"event: 01J40A7M9P1S3V5W7Y9A1C3E5H\n" +
			"space: getvisa\n" +
			"subject: XW-axon-20260730-cr01\n" +
			"transition: close\n" +
			"actor: {kind: agent, name: bot, system: axon}\n" +
			"at: 2026-07-30T10:00:00Z\n" +
			"produced_by:\n  tool: a2a\n  version: \"0.10.0\"\n" +
			"verdicts:\n" +
			"  - index: " + strconv.Itoa(index) + "\n" +
			"    verdict: met\n" +
			"    cause_owner: axon\n"
	}

	t.Run("an index past the parent's criteria count is refused", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
			parentRel: parent,
			eventRel:  closeEvent(2), // the parent declares 2 criteria: 0 and 1
		})
		code, rep, _ := runCI(t, engine, root, fakeGit(eventRel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("a close naming criterion 2 of a 2-criterion parent merged clean: code=%d valid=%v\n"+
				"REF-019 is wired in the engine but `validate --ci` is not offering it a Resolver, so it "+
				"degraded to \"cannot check\" — the rule is inert on the only path that gates a merge.",
				code, rep.Valid)
		}
		if !ciReportHasViolation(rep, eventRel, "REF-019") {
			t.Fatalf("refused, but not by REF-019 — some other rule reddened this and the verdict check "+
				"may still be inert:\n%+v", rep.Artifacts)
		}
	})

	t.Run("an index inside the parent's criteria count is accepted", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
			parentRel: parent,
			eventRel:  closeEvent(1), // in range
		})
		code, rep, errOut := runCI(t, engine, root, fakeGit(eventRel), "v3-pr", "deadbeef", "ydnikolaev")
		if ciReportHasViolation(rep, eventRel, "REF-019") {
			t.Fatalf("a legal verdict index was refused by REF-019 — the bounds check is off by one, or it "+
				"is counting the wrong array: code=%d stderr=%s\n%+v", code, errOut, rep.Artifacts)
		}
	})
}

// TestValidateCI_REF023FiresOnAnIncompleteVerdictsArray is defects-fix-2026-08
// P4's own production-path proof, the exact shape finding 04 measured on the
// real getvisa space: a close over a parent declaring N criteria whose
// verdicts[] names fewer than N. Mirrors
// TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex's own two-directions
// structure (both refused and accepted, so the test cannot pass against an
// engine that refuses everything or nothing).
func TestValidateCI_REF023FiresOnAnIncompleteVerdictsArray(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)

	const parentRel = "axon/exchange/XW-axon-20260730-cr02.md"
	const eventRel = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5J.yaml"

	// A parent declaring exactly TWO acceptance criteria.
	parent := "---\nschema: envelope/v1\nid: XW-axon-20260730-cr02\ntype: work_request\n" +
		"title: two criteria\nspace: getvisa\nfrom: axon\nto: [peer]\n" +
		"actor: {kind: agent, name: bot, model: m}\ncreated: 2026-07-30T09:00:00Z\n" +
		"category: data\npriority: p3\nblocking: false\n" +
		"interim_behavior: \"we wait\"\nneeded_by: 2026-08-01\n" +
		"acceptance_criteria:\n  - \"first\"\n  - \"second\"\n" +
		"thread: thread:axon-20260730-cr02\nclassification: internal\n---\nbody\n"

	closeEventWithVerdicts := func(block string) string {
		return "schema: event/v2\n" +
			"event: 01J40A7M9P1S3V5W7Y9A1C3E5J\n" +
			"space: getvisa\n" +
			"subject: XW-axon-20260730-cr02\n" +
			"transition: close\n" +
			"actor: {kind: agent, name: bot, system: axon}\n" +
			"at: 2026-07-30T10:00:00Z\n" +
			"produced_by:\n  tool: a2a\n  version: \"0.10.0\"\n" +
			block
	}

	t.Run("an empty verdicts array over a 2-criteria parent is refused", func(t *testing.T) {
		t.Parallel()
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
			parentRel: parent,
			eventRel:  closeEventWithVerdicts("verdicts: []\n"),
		})
		code, rep, _ := runCI(t, engine, root, fakeGit(eventRel), "v3-pr", "deadbeef", "ydnikolaev")
		if code == 0 || rep.Valid {
			t.Fatalf("a close with verdicts: [] over a 2-criteria parent merged clean: code=%d valid=%v\n"+
				"REF-023 is the exact defect docs/inbox/defects/"+
				"04-the-verification-record-defaults-to-empty.md measured on the real getvisa space.",
				code, rep.Valid)
		}
		if !ciReportHasViolation(rep, eventRel, "REF-023") {
			t.Fatalf("refused, but not by REF-023 — some other rule reddened this and the completeness check "+
				"may still be inert:\n%+v", rep.Artifacts)
		}
	})

	t.Run("a verdicts array naming both declared criteria is accepted", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n" +
			"  - index: 0\n    verdict: met\n    cause_owner: axon\n" +
			"  - index: 1\n    verdict: not_exercised\n    cause_owner: axon\n"
		root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
			parentRel: parent,
			eventRel:  closeEventWithVerdicts(block),
		})
		code, rep, errOut := runCI(t, engine, root, fakeGit(eventRel), "v3-pr", "deadbeef", "ydnikolaev")
		if ciReportHasViolation(rep, eventRel, "REF-023") {
			t.Fatalf("a verdicts[] array naming every declared criterion was refused by REF-023: "+
				"code=%d stderr=%s\n%+v", code, errOut, rep.Artifacts)
		}
	})
}

// TestValidateCI_REF023ScopedToPRModeNotFullRepo is REF-023 through the
// PRODUCTION entry point and its ADR-011 D3 scoping, proved the way
// REF-017's and POL-022's own tests prove it: ONE event through BOTH modes,
// so what the test measures is the scoping, not two different fixtures. The
// fixture is getvisa's own measured shape: verdicts: [] over a declared
// parent, merged and immutable.
func TestValidateCI_REF023ScopedToPRModeNotFullRepo(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const parentRel = "axon/exchange/XW-axon-20260730-cr03.md"
	const eventRel = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5K.yaml"

	parent := "---\nschema: envelope/v1\nid: XW-axon-20260730-cr03\ntype: work_request\n" +
		"title: two criteria\nspace: getvisa\nfrom: axon\nto: [peer]\n" +
		"actor: {kind: agent, name: bot, model: m}\ncreated: 2026-07-30T09:00:00Z\n" +
		"category: data\npriority: p3\nblocking: false\n" +
		"interim_behavior: \"we wait\"\nneeded_by: 2026-08-01\n" +
		"acceptance_criteria:\n  - \"first\"\n  - \"second\"\n" +
		"thread: thread:axon-20260730-cr03\nclassification: internal\n---\nbody\n"
	event := "schema: event/v2\n" +
		"event: 01J40A7M9P1S3V5W7Y9A1C3E5K\n" +
		"space: getvisa\n" +
		"subject: XW-axon-20260730-cr03\n" +
		"transition: close\n" +
		"actor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-30T10:00:00Z\n" +
		"produced_by:\n  tool: a2a\n  version: \"0.10.0\"\n" +
		"verdicts: []\n"

	root := ciRepo(t, ciManifestWithFloor("0.10.0"), map[string]string{
		parentRel: parent,
		eventRel:  event,
	})

	code, rep, errOut := runCI(t, engine, root, fakeGit(eventRel), "v3-pr", "deadbeef", "ydnikolaev")
	if code == 0 || rep.Valid {
		t.Fatalf("expected v3-pr to refuse an incomplete verdicts[] array, got code=%d valid=%v stderr=%s", code, rep.Valid, errOut)
	}
	if !ciReportHasViolation(rep, eventRel, "REF-023") {
		t.Fatalf("expected REF-023 in the v3-pr result, got %+v", rep.Artifacts)
	}

	code, rep, errOut = runCI(t, engine, root, nil, "v3-full-repo", "", "")
	if ciReportHasViolation(rep, eventRel, "REF-023") {
		t.Fatalf("expected v3-full-repo to suppress REF-023 — a merged verify/close event is immutable and no "+
			"verb rewrites its verdicts[] — got code=%d stderr=%s\n%+v", code, errOut, rep.Artifacts)
	}
}

// handoffEmptySectionsArtifact renders a schema-valid handoff whose five
// §16.2-required body sections are present and empty — the shape all four
// handoffs in the real getvisa space have, measured before POL-022 existed.
func handoffEmptySectionsArtifact(id, from, to string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: handoff\n" +
		"title: Delivery with nothing said about it\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: claude-code}\n" +
		"created: \"2026-08-10T09:00:00Z\"\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"thread: thread:" + from + "-20260810-e7k3\n" +
		"deliverables:\n" +
		"  - {name: \"the thing\", ref: \"XC-axon-ingest@1.1.0\", kind: contract}\n" +
		"verification: \"Run the receiver's own check.\"\n" +
		"limitations: []\n" +
		"---\n" +
		"## Context\n\n## What was built\n\n## How to verify\n\n## How to operate\n\n## Limitations & next steps\n"
}

// TestValidateCI_POL022ScopedToPRModeNotFullRepo is POL-022 through the
// PRODUCTION entry point — `a2a validate --ci`, the merge gate a space
// actually pins — and its ADR-011 D3 scoping, proved the way REF-017's own
// test proves it: ONE artifact through BOTH modes, so what the test measures
// is the scoping and not two different fixtures.
func TestValidateCI_POL022ScopedToPRModeNotFullRepo(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "axon/exchanges/XH-axon-20260810-p022.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: handoffEmptySectionsArtifact("XH-axon-20260810-p022", "axon", "seomatrix"),
	})

	code, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	if code == 0 || rep.Valid {
		t.Fatalf("expected v3-pr to refuse a handoff whose required sections are empty, got code=%d valid=%v stderr=%s", code, rep.Valid, errOut)
	}
	if len(rep.Artifacts) != 1 || rep.Artifacts[0].Result == nil {
		t.Fatalf("expected one artifact result, got %+v", rep.Artifacts)
	}
	if v := violationWithCodeCI(rep.Artifacts[0].Result.Violations, "POL-022"); v == nil {
		t.Fatalf("expected POL-022 in the v3-pr result, got %+v", rep.Artifacts[0].Result.Violations)
	} else if v.Severity != "reject" {
		t.Fatalf("expected POL-022 SeverityReject in v3-pr, got %q", v.Severity)
	}

	code, rep, errOut = runCI(t, engine, root, nil, "v3-full-repo", "", "")
	for _, a := range rep.Artifacts {
		if a.Path != rel || a.Result == nil {
			continue
		}
		if v := violationWithCodeCI(a.Result.Violations, "POL-022"); v != nil {
			t.Fatalf("expected v3-full-repo to suppress POL-022 — a merged handoff's body is immutable and no verb rewrites it — got %+v (code=%d stderr=%s)", v, code, errOut)
		}
	}
}

// contractRuntimePinAgainstAbsentEndpoint renders a contract descriptor that
// declares BOTH `runtime_pinnable: true` and an absent operational half —
// XC-seomatrix-c4-export's own shape in the real getvisa space, and the
// conjunction POL-023 exists to surface.
func contractRuntimePinAgainstAbsentEndpoint(id, from string) string {
	return "---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: A promise whose endpoint does not exist yet\n" +
		"space: getvisa\n" +
		"from: " + from + "\n" +
		"to: [axon]\n" +
		"actor: {kind: agent, name: claude-code}\n" +
		"created: \"2026-08-17T17:31:37Z\"\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: 1.0.0\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"x_binding:\n" +
		"    artifact_class: producer_capability\n" +
		"    compatibility_status: none\n" +
		"    adoptable: true\n" +
		"    runtime_pinnable: true\n" +
		"x_operational:\n" +
		"  - name: endpoint\n" +
		"    state: absent\n" +
		"thread: thread:" + from + "-20260817-knze\n" +
		"artifacts:\n" +
		"  - path: schema/c4-export.schema.json\n" +
		"    role: schema\n" +
		"    normative: true\n" +
		"    media_type: application/schema+json\n" +
		"---\n" +
		"A contract descriptor. Its endpoint does not exist yet and it says so.\n"
}

// TestValidateCI_POL023WarnsOnARuntimePinAgainstAnAbsentEndpoint proves
// POL-023 fires through the PRODUCTION entry point — `a2a validate --ci`,
// the merge gate a space pins — and that it WARNS rather than refuses.
// ADR-011 D2: publishing a promise before activating it is a designed
// sequence (spec 05's own x_operational / `a2a contract activate` pair), so
// the conjunction is a claim ahead of the facts, not an invalid document.
func TestValidateCI_POL023WarnsOnARuntimePinAgainstAnAbsentEndpoint(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	rel := "seomatrix/provides/c4-export/contract.md"
	root := ciRepo(t, ciSpaceYAML, map[string]string{
		rel: contractRuntimePinAgainstAbsentEndpoint("XC-seomatrix-c4-export", "seomatrix"),
	})

	_, rep, errOut := runCI(t, engine, root, fakeGit(rel), "v3-pr", "deadbeef", "ydnikolaev")
	var found *validate.Violation
	for _, a := range rep.Artifacts {
		if a.Path != rel || a.Result == nil {
			continue
		}
		found = violationWithCodeCI(a.Result.Violations, "POL-023")
	}
	if found == nil {
		t.Fatalf("expected POL-023 through validate --ci, got report=%+v stderr=%s", rep, errOut)
	}
	if found.Severity == "reject" {
		t.Fatalf("POL-023 must WARN, not refuse — publishing before activating is a designed sequence (ADR-011 D2), got %q", found.Severity)
	}
}

// --- P9 (spec 09-submit-validates-what-it-carries) ---------------------
//
// fb-20260820-d1e370: `a2a submit` carried a declared companion that the
// space's own `validate --ci` then refused with POL-002. Discovery
// classified `artifacts/CHANGELOG.md` as an ENVELOPE ARTIFACT purely by its
// `.md` suffix, three lines below a `space.ContractForPath` call whose
// answer it discarded.

// declaredV2ContractWithCompanion is declaredV2Contract plus the ONE file
// the incident turned on: a frontmatter-free `artifacts/CHANGELOG.md`,
// declared `role: changelog, normative: false, media_type: text/markdown` —
// the only location the envelope/v2 role-conditional path pattern permits.
func declaredV2ContractWithCompanion(from, slug string) string {
	return strings.Replace(declaredV2Contract(from, slug),
		"---\nBody.\n",
		"  - {path: artifacts/CHANGELOG.md, role: changelog, normative: false, media_type: text/markdown}\n---\nBody.\n",
		1)
}

// companionChangelog is the carried bytes exactly as a producer's release
// tree holds them: markdown, no frontmatter, nothing to parse an envelope
// out of. The report's own third option (add frontmatter) is rejected
// because it would make the carried bytes differ from that tree.
const companionChangelog = "# Changelog\n\n## 0.0.0\n\n- first publication\n"

// ciCompanionFixture builds a space checkout carrying one declared-companion
// contract plus ONE genuinely malformed artifact elsewhere under the same
// system — criterion 2's control, so the exemption reads as a
// classification rather than as a suppression.
func ciCompanionFixture(t *testing.T, slug string) (root, companionRel, brokenRel string, contractPaths []string) {
	t.Helper()
	dir := "axon/provides/" + slug + "/"
	rel := dir + "contract.md"
	companionRel = dir + "artifacts/CHANGELOG.md"
	brokenRel = "axon/exchanges/XQ-axon-20260730-h2k8.md"

	files := contractShapeSidecars(slug)
	files[rel] = declaredV2ContractWithCompanion("axon", slug)
	files[companionRel] = companionChangelog
	files[brokenRel] = "no frontmatter at all, and this one really is an artifact\n"

	// The floor must admit a declared `artifacts:` inventory at all — below
	// 0.19.0 the descriptor-shape check refuses the key outright, and this
	// fixture would then be red for a reason that is not its subject.
	root = ciRepo(t, ciManifestWithFloor("0.19.3"), files)
	contractPaths = []string{rel, companionRel,
		dir + "schema/" + slug + ".schema.json",
		dir + "fixtures/valid/" + slug + ".json",
		dir + "fixtures/invalid/" + slug + ".json"}
	return root, companionRel, brokenRel, contractPaths
}

// ciViolationsFor returns every violation the report carries for one path.
func ciViolationsFor(rep ciReport, path string) []validate.Violation {
	var out []validate.Violation
	for _, a := range rep.Artifacts {
		if a.Path != path || a.Result == nil {
			continue
		}
		out = append(out, a.Result.Violations...)
	}
	return out
}

// TestValidateCI_DeclaredCompanionIsNotAnArtifact is the incident,
// reproduced (criterion 1) in BOTH modes with criterion 2's control in the
// same run: the declared companion carries no POL-002 anywhere, while a
// genuinely malformed artifact under the same system still reds.
//
// TEETH: before the classifier landed, both modes reported
// `[POL-002] artifact frontmatter is missing or is not valid YAML` against
// axon/provides/<slug>/artifacts/CHANGELOG.md.
func TestValidateCI_DeclaredCompanionIsNotAnArtifact(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	root, companionRel, brokenRel, _ := ciCompanionFixture(t, "content-feed")
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "publish content-feed with a companion changelog")
	base := contractGitRevParse(t, root, "HEAD")

	for _, tc := range []struct {
		mode    string
		changed []string
	}{
		// brokenRel is in the v3-pr diff too, deliberately: criterion 2
		// says the control runs "in the same run as #1", and v3-pr is the
		// run that stops a pull request. A control that only fired in
		// full-repo would leave the merge-gating mode proving nothing
		// about suppression.
		{mode: "v3-pr", changed: []string{"axon/provides/content-feed/contract.md", companionRel, brokenRel}},
		{mode: "v3-full-repo"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, rep, errOut := runCI(t, engine, root, fakeGit(tc.changed...), tc.mode, base, "ydnikolaev")
			for _, v := range ciViolationsFor(rep, companionRel) {
				if v.Code == "POL-002" {
					t.Fatalf("%s: a DECLARED companion must never be judged as an envelope artifact; got %+v (stderr=%s)", tc.mode, v, errOut)
				}
			}
			var sawBroken bool
			for _, v := range ciViolationsFor(rep, brokenRel) {
				if v.Code == "POL-002" {
					sawBroken = true
				}
			}
			if !sawBroken {
				t.Fatalf("%s: POL-002 must still fire on a genuinely malformed artifact under the SAME system, in the SAME run — the exemption is a classification, not a suppression; report=%+v", tc.mode, rep.Artifacts)
			}
		})
	}
}

// TestValidateCI_UndeclaredCompanionRefusedInPRNotFullRepo is criterion 9,
// both halves in ONE test so the pair cannot drift: a file under a
// contract's own directory that the descriptor does not declare is refused
// when it is PROPOSED (v3-pr), and is NOT refused when it is already merged
// (v3-full-repo, US-7 / ADR-011 D3/D4 — an immutable publication must not
// be redded retroactively by a new rule). In full-repo it is also not
// judged as an artifact, which is strictly better than today, where it was
// wrongly rejected with POL-002.
//
// TEETH: dropping the `mode == "v3-pr"` guard reds the full-repo half;
// dropping the CarriedUndeclaredCompanion arm entirely reds the PR half.
func TestValidateCI_UndeclaredCompanionRefusedInPRNotFullRepo(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const slug = "notes-feed"
	dir := "axon/provides/" + slug + "/"
	undeclared := dir + "artifacts/NOTES.md"
	files := contractShapeSidecars(slug)
	files[dir+"contract.md"] = declaredV2ContractWithCompanion("axon", slug)
	files[dir+"artifacts/CHANGELOG.md"] = companionChangelog
	files[undeclared] = "scratch notes nobody declared\n"
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), files)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "publish notes-feed")
	base := contractGitRevParse(t, root, "HEAD")

	prCode, prRep, _ := runCI(t, engine, root, fakeGit(dir+"contract.md", undeclared), "v3-pr", base, "ydnikolaev")
	var sawPOL013 *validate.Violation
	for _, v := range ciViolationsFor(prRep, undeclared) {
		if v.Code == "POL-013" {
			found := v
			sawPOL013 = &found
		}
	}
	if sawPOL013 == nil {
		t.Fatalf("v3-pr must refuse an undeclared companion with POL-013 — a space must never receive a file nothing classifies; report=%+v", prRep.Artifacts)
	}
	if prCode != 1 {
		t.Fatalf("v3-pr exit = %d, want 1", prCode)
	}
	for _, want := range []string{"artifacts/NOTES.md", dir + "contract.md"} {
		if !strings.Contains(sawPOL013.Message, want) {
			t.Fatalf("the refusal must print the FIX and name %q; got %q", want, sawPOL013.Message)
		}
	}

	fullCode, fullRep, _ := runCI(t, engine, root, nil, "v3-full-repo", "", "")
	for _, v := range ciViolationsFor(fullRep, undeclared) {
		t.Fatalf("v3-full-repo must not judge an ALREADY-MERGED undeclared companion at all (US-7, ADR-011); got %+v", v)
	}
	if fullCode != 0 {
		t.Fatalf("v3-full-repo exit = %d, want 0; report=%+v", fullCode, fullRep.Artifacts)
	}
}

// --- criterion 11: the invariant, computed ----------------------------

// parityFunnel captures the SubmitRequest without performing any write, so
// the submit side of the comparison below is the real request builder's
// own output rather than a re-derivation of it.
type parityFunnel struct{ req space.SubmitRequest }

func (f *parityFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.req = req
	return space.WriteResult{State: space.WriteStatePendingMerge, PRNumber: 1, PRURL: "https://example.invalid/pr/1", Branch: req.ArtifactID}, nil
}

// TestSubmitAndValidateCIClassifyTheSameSet is US-8 / criterion 11 — the
// invariant this phase establishes, stated once and TESTED as a SET
// EQUALITY rather than asserted:
//
//	{space-relative paths `a2a submit` CARRIES for a contract}
//	  = {paths `validate --ci` CLASSIFIES and judges for that contract}
//
// and no path in that set is classified DIFFERENTLY by the two sides.
//
// A per-instance assertion is explicitly insufficient: before this phase it
// would have passed for fourteen of the fifteen carried files, and the
// fifteenth is the whole report. The two sides are driven from their OWN
// inputs — submit from the staging tree through buildRequest, `--ci` from a
// real `git diff` over a checkout of exactly what submit produced — so the
// comparison cannot be satisfied by both halves reading one list.
//
// The second clause is the one that is easy to fake, and it is why the two
// sides resolve the descriptor from DIFFERENT places: submit reads it out
// of the batch it is about to write, `--ci` reads it from the checkout. A
// classifier that quietly depended on one of those would show up here as a
// class disagreement, not as a missing path.
func TestSubmitAndValidateCIClassifyTheSameSet(t *testing.T) {
	t.Parallel()
	const slug = "parity-feed"
	dir := "axon/provides/" + slug + "/"

	// --- the submit side, from the staging tree it actually reads ---
	stagingDir := t.TempDir()
	descriptor := declaredV2ContractWithCompanion("axon", slug)
	descriptor = strings.Replace(descriptor, "space: getvisa", "space: fixture-space", 1)
	if err := os.WriteFile(filepath.Join(stagingDir, "XC-axon-"+slug+".md"), []byte(descriptor), 0o644); err != nil {
		t.Fatalf("write staged descriptor: %v", err)
	}
	for rel, content := range map[string]string{
		dir + "schema/" + slug + ".schema.json":    `{"type":"object"}`,
		dir + "fixtures/valid/" + slug + ".json":   `{}`,
		dir + "fixtures/invalid/" + slug + ".json": `null`,
		dir + "artifacts/CHANGELOG.md":             companionChangelog,
	} {
		abs := filepath.Join(stagingDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write staged sidecar: %v", err)
		}
	}

	mirrorDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mirrorDir, "space.yaml"), []byte("min_binary_version: \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("write mirror space.yaml: %v", err)
	}
	legality := NewLegalityAdapter(mirrorDir, "axon", space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"}, {System: "seomatrix", Status: "active"},
	}})
	funnel := &parityFunnel{}
	cmd := NewSubmitCommand(funnel, legality, NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir,
		SubmitHostConfig{RemoteURL: "https://example.invalid/org/space.git", BaseBranch: "main"})
	var out, errBuf bytes.Buffer
	if code := cmd.Run(context.Background(), []string{filepath.Join(stagingDir, "XC-axon-"+slug+".md")},
		IO{Stdout: &out, Stderr: &errBuf}); code != 0 {
		t.Fatalf("submit code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errBuf.String())
	}

	submitSet := map[string]space.Carried{}
	for _, carried := range space.ClassifyCarriedBatch(funnel.req.Files) {
		if carried.Class == space.CarriedNotAContractPath {
			continue
		}
		submitSet[carried.Path] = carried
	}
	if len(submitSet) == 0 {
		t.Fatalf("submit carried no contract paths at all; files=%+v", funnel.req.Files)
	}

	// --- the --ci side, from a real git diff over a checkout of exactly
	// what submit produced ---
	root := ciRepo(t, ciManifestWithFloor("0.19.3"), nil)
	contractGitRun(t, root, "init", "-q", "-b", "main")
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "empty space")
	base := contractGitRevParse(t, root, "HEAD")
	for _, f := range funnel.req.Files {
		abs := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, f.Content, 0o644); err != nil {
			t.Fatalf("write %s: %v", f.Path, err)
		}
	}
	contractGitRun(t, root, "add", "-A")
	contractGitRun(t, root, "commit", "-q", "-m", "the PR submit would open")

	changed, err := gitDiffNameOnly(context.Background(), root, base)
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	descriptors := newCIDescriptorReader(root)
	ciSet := map[string]space.Carried{}
	for _, p := range changed {
		carried := descriptors.classify(p)
		if carried.Class == space.CarriedNotAContractPath {
			continue
		}
		ciSet[p] = carried
	}

	// --- the set equality ---
	for p := range submitSet {
		if _, ok := ciSet[p]; !ok {
			t.Errorf("submit carries %q into the space and `validate --ci` classifies nothing for it — that gap IS the defect", p)
		}
	}
	for p := range ciSet {
		if _, ok := submitSet[p]; !ok {
			t.Errorf("`validate --ci` classifies %q but `a2a submit` never carried it", p)
		}
	}

	// --- and no path is classified DIFFERENTLY by the two sides ---
	for p, submitClass := range submitSet {
		ciClass, ok := ciSet[p]
		if !ok {
			continue
		}
		if submitClass.Class != ciClass.Class {
			t.Errorf("%q is %q to submit and %q to `validate --ci` — two halves of one binary must not disagree about one path", p, submitClass.Class, ciClass.Class)
		}
		if submitClass.Class == space.CarriedUnclassifiable {
			t.Errorf("%q is unclassifiable to both sides — a carried path nothing classifies is itself the refusal (US-3)", p)
		}
	}

	// The incident's own file must be in the set, on both sides, as a
	// declared companion — otherwise the equality above could be satisfied
	// by an empty intersection.
	companion := dir + "artifacts/CHANGELOG.md"
	if submitSet[companion].Class != space.CarriedDeclaredCompanion || ciSet[companion].Class != space.CarriedDeclaredCompanion {
		t.Fatalf("the incident's own file must be a declared-companion on BOTH sides; submit=%q ci=%q",
			submitSet[companion].Class, ciSet[companion].Class)
	}

	// ...and CLASSIFIES is only half of criterion 11. The production verb
	// must reach the same answer, so the real `--ci` runs over the very
	// commit submit would have opened and every path in the set must come
	// back CLEAN — POL-002 above all. Without this the equality above would
	// be a statement about a function the discovery loop might not call.
	//
	// Scoped to the set under comparison rather than to the run's exit code:
	// the lifecycle event in the same commit is stamped by the REAL write
	// funnel (`produced_by`, POL-012), which this fake deliberately is not,
	// and that path is not a contract path and not this criterion's subject.
	_, rep, errOut := runCI(t, ciEngine(t), root, fakeGit(changed...), "v3-pr", base, "ydnikolaev")
	var judged bool
	for p := range ciSet {
		for _, a := range rep.Artifacts {
			if a.Path == p {
				judged = true
			}
		}
		for _, v := range ciViolationsFor(rep, p) {
			t.Errorf("`validate --ci` refused %q with [%s] — the PR `a2a submit` opens must not be refused for a file submit itself carried: %s (stderr=%s)", p, v.Code, v.Message, errOut)
		}
	}
	if !judged {
		t.Fatalf("`validate --ci` produced no report line for ANY path in the carried set — it classified nothing it was handed; report=%+v", rep.Artifacts)
	}
}
