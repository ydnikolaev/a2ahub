package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// fakeSubmitFunnel is a hand-written test double for cmd_submit.go's own
// unexported submitFunnel seam (Go's structural typing lets an external
// test satisfy it without naming it) — used by tests that must prove the
// funnel (a git/host call) is NEVER reached.
type fakeSubmitFunnel struct {
	calls  []space.SubmitRequest
	result space.WriteResult
	err    error
}

func (f *fakeSubmitFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return space.WriteResult{}, f.err
	}
	if f.result.State == "" {
		return space.WriteResult{State: space.WriteStatePendingMerge, PRNumber: 1, PRURL: "https://example.invalid/pr/1", Branch: req.ArtifactID}, nil
	}
	return f.result, nil
}

func writeQuestionDraft(t *testing.T, dir, id, from, to string) string {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	return path
}

func testManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember},
		{System: "other", Status: fold.MembershipMember},
	}}
}

// testHostConfig is a generic, fully-populated SubmitHostConfig for
// tests that never reach a real host call (the foreign-section/
// idempotency short-circuit tests) but must still construct a
// SubmitCommand.
func testHostConfig() cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL:         "https://example.invalid/org/space.git",
		Repo:              host.Repo{Owner: "org", Name: "space"},
		BaseBranch:        "main",
		Credential:        host.Credential{Token: "test-token"},
		CommitAuthorName:  "a2a-axon",
		CommitAuthorEmail: "a2a-axon@a2ahub.invalid",
	}
}

// fixtureHostConfig is a SubmitHostConfig wired to a spacefixture.Fixture
// for the end-to-end tests, so RemoteURL genuinely matches the fixture's
// own local git remote.
func fixtureHostConfig(fx *spacefixture.Fixture) cli.SubmitHostConfig {
	cfg := testHostConfig()
	cfg.RemoteURL = fx.RemoteURL()
	return cfg
}

// --- validate (OP-204) ------------------------------------------------

// TestValidateResolvesBareStagedID is the same fix `a2a submit <id>`
// already has (OP-205's Input column): `a2a validate <id>` must accept a
// bare staged artifact id — what `a2a new` just printed — not demand a
// path, "open XQ-...: no such file or directory". Reuses submit's own
// resolveSubmitTarget id-resolution rather than a second copy.
func TestValidateResolvesBareStagedID(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	cmd := cli.NewValidateCommand(engine, stagingDir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"XQ-axon-20260721-k3f9"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (bare staged id must resolve to <staging>/<id>.md); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "XQ-axon-20260721-k3f9.md") {
		t.Fatalf("expected the JSON report to name the resolved staging path; got %q", out.String())
	}
}

// TestValidateAcceptsFlagAfterPositional is Wave K's live-run 6 defect
// applied to `a2a validate`: `validate <path> --all` used to leave --all
// unset (Go's flag package stops parsing at the first non-flag token) and
// then refuse on the single-artifact branch's fs.NArg() != 1 check, with
// the unconsumed "--all" token itself counted as a second positional.
// This exercises the SAME defect with `--author`, a flag that does not
// change validate's own control flow, so the assertion is purely about
// argument-order acceptance, not about --ci's separate branch.
//
// TEETH: reverting ValidateCommand.Run's parseArgsAnyOrder call
// (cmd_submit.go) back to a bare `fs.Parse(args)` reds this.
func TestValidateAcceptsFlagAfterPositional(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k400", "axon", "other")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	cmd := cli.NewValidateCommand(engine, stagingDir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"XQ-axon-20260721-k400", "--author", "octocat"}, io)
	if code != 0 {
		t.Fatalf("validate <id> --author x: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "XQ-axon-20260721-k400.md") {
		t.Fatalf("expected the JSON report to name the resolved staging path; got %q", out.String())
	}
}

// TestSubmitForeignSectionRefusal is AC-201.3: an artifact whose `from`
// does not match the configured own system is refused locally, exits
// non-zero, and the write funnel is NEVER called (no git/network call).
func TestSubmitForeignSectionRefusal(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-other-20260721-k3f9", "other", "axon")

	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a foreign-section artifact")
	}
	if !strings.Contains(errOut.String(), "CC-002") {
		t.Fatalf("expected the refusal message to name CC-002; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestSubmitRefusesUnfilledPlaceholderSpace is the placeholder-space
// message improvement: a draft whose `space:` is still the unfilled
// `<space-id>` template placeholder (schemas/templates/v1/*.md) must be
// refused as "the draft was never filled in", not surfaced as if
// `<space-id>` were a real (merely wrong) space value — that check belongs
// to the wire layer's own findSpace lookup, one layer up, once this guard
// has ruled out "still a draft".
func TestSubmitRefusesUnfilledPlaceholderSpace(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: <space-id>\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(stagingDir, "XQ-axon-20260721-k3f9.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for an artifact with an unfilled placeholder space")
	}
	if !strings.Contains(errOut.String(), "never filled in") {
		t.Fatalf("expected the refusal message to name the unfilled draft, not quote the placeholder as a value; got %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "not a connected space") {
		t.Fatalf("expected the placeholder-specific message, not the connected-space message; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestSubmitForeignSectionRefusalOwnSystemForeignToIsNotRefused is the
// §6 edge case: an own-system artifact with a foreign `to` must NOT be
// refused — refusal is about the acting section (`from`), not the
// addressee.
func TestSubmitOwnSystemForeignToIsNotRefused(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (own from, foreign to, must not be refused); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if fake.calls[0].MinBinaryVersion != "0.1.0" {
		t.Fatalf("MinBinaryVersion = %q, want 0.1.0 (read from the mirror's space.yaml, CC-085)", fake.calls[0].MinBinaryVersion)
	}
	if fake.calls[0].RemoteURL == "" || fake.calls[0].BaseBranch == "" || fake.calls[0].CommitAuthorName == "" {
		t.Fatalf("expected the SubmitRequest to carry the host config through: %+v", fake.calls[0])
	}
	var sawReceipt bool
	for _, file := range fake.calls[0].Files {
		content := string(file.Content)
		if strings.Contains(content, "transition: submit") && strings.Contains(content, "state: submitted") {
			sawReceipt = true
		}
	}
	if !sawReceipt {
		t.Fatalf("entry event omitted its evaluator receipt: %+v", fake.calls[0].Files)
	}
}

// TestSubmitRefusesEmptyHostBaseBranch is no-silent-yes-2026-08 Group A's
// own acceptance for the CLI side: SubmitCommand.buildRequest used to
// silently default an empty HostCfg.BaseBranch to "main" (§4.2's former
// normative default); wave 5b already deleted the equivalent fallback one
// layer down, in space's own write funnel, so a caller reintroducing it
// here pushed at a branch nobody named the moment HostCfg.BaseBranch was
// ever left unresolved. It must now refuse, naming the field, BEFORE the
// write funnel is ever called.
func TestSubmitRefusesEmptyHostBaseBranch(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	hostCfg := testHostConfig()
	hostCfg.BaseBranch = ""
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, hostCfg)

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for an empty HostCfg.BaseBranch")
	}
	if !strings.Contains(errOut.String(), "HostCfg.BaseBranch") {
		t.Fatalf("expected the refusal to name HostCfg.BaseBranch; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// writeMinimalSpaceYAML writes a bare space.yaml with only the
// min_binary_version field this package's submit path reads (CC-085) —
// deliberately NOT the full space.Manifest shape (testkit/spacefixture's
// own seeded space.yaml uses a map-shaped `participants:` block that does
// not structurally decode into space.Manifest.Participants ([]Participant)
// either; this phase's own min_binary_version read is its own minimal,
// permissive decode for exactly this reason — see readMinBinaryVersion's
// doc comment in cmd_submit.go).
func writeMinimalSpaceYAML(t *testing.T, mirrorDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mirrorDir, "space.yaml"), []byte("min_binary_version: \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
}

// TestSubmitIdempotentAlreadySubmitted is AC-301.1: re-running submit on
// an artifact whose id already has a committed event is a no-op "already
// done" — exit 0, and the write funnel is never called again.
func TestSubmitIdempotentAlreadySubmitted(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	mirrorDir := t.TempDir()
	writeCommittedEvent(t, mirrorDir, "axon", "2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS", "XQ-axon-20260721-k3f9", "submit", "axon")
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (idempotent already-submitted no-op)", code)
	}
	if !strings.Contains(out.String(), "already submitted") {
		t.Fatalf("expected an 'already submitted' message; got %q", out.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called on an already-submitted re-run; got %d call(s)", len(fake.calls))
	}
}

// --- contract sidecars (P37 D-D follow-up: submit carries schema/fixtures) -

// writeContractDraft stages a minimal, valid contract draft at
// <stagingDir>/<id>.md (contract.schema.json's required fields, same
// shape as cmd_contract_test.go's own writeContractDescriptor, which
// seeds the MIRROR rather than staging).
func writeContractDraft(t *testing.T, stagingDir, id string) string {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"0.0.0\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(stagingDir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write contract draft: %v", err)
	}
	return path
}

// writeStagedSidecar writes a scaffold-shaped sidecar file under
// stagingDir at the exact space-relative path
// internal/cli/cmd_new.go's newScaffoldContractFiles would have written
// it to (schema/<slug>.schema.json, fixtures/valid/<slug>.json, ...).
func writeStagedSidecar(t *testing.T, stagingDir, spaceRelPath, content string) {
	t.Helper()
	full := filepath.Join(stagingDir, filepath.FromSlash(spaceRelPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write sidecar %s: %v", spaceRelPath, err)
	}
}

// TestSubmitContractCarriesStagedSidecars proves the gap this wave closes:
// a scaffolded contract's staged schema/** and fixtures/**/* travel into
// the SubmitRequest alongside contract.md and its lifecycle event, at
// their correct space-relative paths (§4.2/internal/space.Layout) —
// otherwise a scaffolded contract is refused (POL-009) the first time
// anyone publishes it, because nothing else carries those files into the
// space.
func TestSubmitContractCarriesStagedSidecars(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeContractDraft(t, stagingDir, "XC-axon-widget-a")
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-a/schema/widget-a.schema.json", `{"type":"object"}`)
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-a/fixtures/valid/widget-a.json", `{}`)
	// A nested $ref split under schema/ must be carried too (not just the
	// top-level file) — internal/cli/cmd_contract.go's own
	// contractReadWorkingTreeFiles counts a contract's published schema
	// files the SAME recursive way.
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-a/schema/common/types.schema.json", `{"type":"string"}`)

	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}

	files := fake.calls[0].Files
	if len(files) != 5 { // contract.md + event + schema + nested schema + fixture
		t.Fatalf("Files = %d, want 5 (contract.md, event, schema, nested schema, fixture); got %+v", len(files), files)
	}
	byPath := map[string]bool{}
	for _, f := range files {
		byPath[f.Path] = true
	}
	for _, want := range []string{
		"axon/provides/widget-a/contract.md",
		"axon/provides/widget-a/schema/widget-a.schema.json",
		"axon/provides/widget-a/schema/common/types.schema.json",
		"axon/provides/widget-a/fixtures/valid/widget-a.json",
	} {
		if !byPath[want] {
			t.Errorf("Files missing %q; got %+v", want, files)
		}
	}
	var sawEvent bool
	for p := range byPath {
		if strings.HasPrefix(p, "axon/events/") {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Errorf("Files missing the lifecycle event under axon/events/; got %+v", files)
	}

	if !strings.Contains(out.String(), "widget-a.schema.json") {
		t.Errorf("expected the submit output to name the carried sidecar file(s); got %q", out.String())
	}
}

// TestSubmitContractNoSidecarsUnaffected proves a contract with no staged
// schema/fixtures (a contract authored before the D-D scaffold existed, or
// a non-JSON-Schema contract) submits exactly as before: the same file
// count as the pre-change behaviour (contract.md + event, nothing more).
func TestSubmitContractNoSidecarsUnaffected(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeContractDraft(t, stagingDir, "XC-axon-widget-b")

	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if len(fake.calls[0].Files) != 2 {
		t.Fatalf("Files = %d, want exactly 2 (contract.md + event), unchanged from before this wave; got %+v", len(fake.calls[0].Files), fake.calls[0].Files)
	}
	if strings.Contains(out.String(), "carried sidecar") {
		t.Errorf("expected no 'carried sidecar' message when there are no sidecars; got %q", out.String())
	}
}

// TestSubmitContractSidecarsDoNotPerturbArtifactID is the idempotency
// guard: SubmitRequest.ArtifactID (and therefore the deterministic funnel
// branch, space.BranchName) is derived solely from the submitted
// artifact ids, never from Files — so carrying a contract's sidecars
// cannot make a re-submit of the same contract open a second PR.
func TestSubmitContractSidecarsDoNotPerturbArtifactID(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, withSidecars bool) space.SubmitRequest {
		stagingDir := t.TempDir()
		path := writeContractDraft(t, stagingDir, "XC-axon-widget-c")
		if withSidecars {
			writeStagedSidecar(t, stagingDir, "axon/provides/widget-c/schema/widget-c.schema.json", `{"type":"object"}`)
			writeStagedSidecar(t, stagingDir, "axon/provides/widget-c/fixtures/valid/widget-c.json", `{}`)
		}
		mirrorDir := t.TempDir()
		writeMinimalSpaceYAML(t, mirrorDir)
		legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
		fake := &fakeSubmitFunnel{}
		cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{path}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
		return fake.calls[0]
	}

	without := run(t, false)
	with := run(t, true)

	if without.ArtifactID != "XC-axon-widget-c" || with.ArtifactID != "XC-axon-widget-c" {
		t.Fatalf("ArtifactID = %q / %q, want both %q", without.ArtifactID, with.ArtifactID, "XC-axon-widget-c")
	}
	wantBranch := space.BranchName("axon", "submit", "XC-axon-widget-c")
	if gotBranch := space.BranchName(without.System, without.Verb, without.ArtifactID); gotBranch != wantBranch {
		t.Fatalf("branch (no sidecars) = %q, want %q", gotBranch, wantBranch)
	}
	if gotBranch := space.BranchName(with.System, with.Verb, with.ArtifactID); gotBranch != wantBranch {
		t.Fatalf("branch (with sidecars) = %q, want %q — presence of sidecar Files perturbed the deterministic branch", gotBranch, wantBranch)
	}
}

// TestSubmitContractSidecarSymlinkEscapeRefused proves a sidecar entry
// that would escape the contract's own directory (a symlink, the one way
// a name that stayed inside the schema/fixtures directory listing could
// still resolve to content outside it) is refused locally, BEFORE any
// git/network call — never silently followed or silently dropped.
func TestSubmitContractSidecarSymlinkEscapeRefused(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeContractDraft(t, stagingDir, "XC-axon-widget-d")

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside contract dir"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	schemaDir := filepath.Join(stagingDir, "axon", "provides", "widget-d", "schema")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(schemaDir, "widget-d.schema.json")); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}

	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a sidecar symlink escaping the contract's own directory")
	}
	if !strings.Contains(errOut.String(), "symlink") {
		t.Fatalf("expected the refusal message to name the symlink; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// --- end-to-end (real WriteFunnel + FakeHost + spacefixture) ---------------

func newRealFunnelDeps(t *testing.T) (*space.WriteFunnel, *cli.LegalityAdapter, string, *spacefixture.Fixture) {
	t.Helper()
	fx := spacefixture.New(t, "axon")
	mirrorDir := fx.Clone("axon")
	manifest := testManifest()

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fake := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fake, validator, "0.1.0")
	return funnel, legality, mirrorDir, fx
}

// TestSubmitEndToEndSingleArtifact drives the whole submit pipeline
// (foreign-section check -> idempotency check -> V2 via the real
// validate.Engine -> the real write funnel against a local git fixture)
// for a single, valid artifact.
func TestSubmitEndToEndSingleArtifact(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}

	changed := gitDiffNames(t, mirrorDir, "main", "a2a/axon/submit/XQ-axon-20260721-k3f9")
	if len(changed) != 2 {
		t.Fatalf("changed files = %v, want exactly 2 (artifact + event)", changed)
	}
	_ = fx
}

// TestSubmitBatchAllOrNothing is spec 06 §8 acceptance row 4: one
// V2-invalid artifact among N aborts the whole batch — zero pushed, no
// new commit.
func TestSubmitBatchAllOrNothing(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	p1 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-aaa1", "axon", "other")
	p2 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-aaa2", "axon", "other")
	// Third draft is missing the required `category` field -> V2-invalid.
	invalid := "---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-aaa3\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	p3 := filepath.Join(stagingDir, "XQ-axon-20260721-aaa3.md")
	if err := os.WriteFile(p3, []byte(invalid), 0o644); err != nil {
		t.Fatalf("write invalid draft: %v", err)
	}

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--batch", p1, p2, p3}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a batch containing one V2-invalid artifact")
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable error message")
	}

	count := gitRevListCount(t, mirrorDir, "main", "a2a/axon/submit/*")
	if count != 0 {
		t.Fatalf("expected zero new commits on an aborted batch, found %d branch(es)/commit(s)", count)
	}
}

// TestSubmitBatchOneCommitNEvents is spec 06 §8 acceptance row 5: a batch
// of N valid artifacts produces exactly one commit and N submit events.
func TestSubmitBatchOneCommitNEvents(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	p1 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb1", "axon", "other")
	p2 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb2", "axon", "other")
	p3 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb3", "axon", "other")

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--batch", p1, p2, p3}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	branch := "a2a/axon/submit/XQ-axon-20260721-bbb1+XQ-axon-20260721-bbb2+XQ-axon-20260721-bbb3"
	commits := gitRevListCountBranch(t, mirrorDir, "main", branch)
	if commits != 1 {
		t.Fatalf("commits ahead of main on %s = %d, want exactly 1", branch, commits)
	}
	changed := gitDiffNames(t, mirrorDir, "main", branch)
	if len(changed) != 6 { // 3 artifacts + 3 events
		t.Fatalf("changed files = %v, want exactly 6 (3 artifacts + 3 events)", changed)
	}
}

// gitDiffNames/gitRevListCount(Branch) are small git-plumbing test
// helpers (explicit argv, never sh -c) mirroring internal/space's own
// test-file idiom (funnel_test.go).
func gitDiffNames(t *testing.T, dir, base, head string) []string {
	t.Helper()
	out := runGitOutputForTest(t, dir, "diff", "--name-only", base, head)
	return strings.Fields(out)
}

func gitRevListCountBranch(t *testing.T, dir, base, head string) int {
	t.Helper()
	out := runGitOutputForTest(t, dir, "rev-list", "--count", base+".."+head)
	n := 0
	for _, c := range out {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// gitRevListCount reports whether ANY ref matching pattern exists ahead
// of base (used by the all-or-nothing test, which does not know the exact
// branch name an aborted batch would have used).
func gitRevListCount(t *testing.T, dir, base, branchGlob string) int {
	t.Helper()
	out := runGitOutputForTestAllowFail(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/a2a/axon/")
	if strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Fields(out))
}

func runGitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitCombined(dir, args...)
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(out)
}

func runGitOutputForTestAllowFail(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := runGitCombined(dir, args...)
	return out
}

func runGitCombined(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// --- P9: submit validates what it carries (spec 09) --------------------
//
// fb-20260820-d1e370. `a2a submit` is documented as "validate + open a PR";
// it validated the descriptor, carried fifteen sidecars it did not
// validate, and the space's own `validate --ci` refused one of them with
// POL-002 — the gate submit exists to pre-empt.

// writeV2ContractDraft is writeContractDraft's contract-set-v2 sibling: the
// descriptor carries an `artifacts:` inventory, which is the declaration
// this phase's classifier consults. extraEntries are appended verbatim as
// further inventory rows.
func writeV2ContractDraft(t *testing.T, stagingDir, id, slug string, extraEntries ...string) string {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"0.0.0\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"artifacts:\n" +
		"  - {path: schema/" + slug + ".schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/" + slug + ".json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/" + slug + ".schema.json}\n" +
		"  - {path: artifacts/CHANGELOG.md, role: changelog, normative: false, media_type: text/markdown}\n"
	for _, e := range extraEntries {
		content += "  - " + e + "\n"
	}
	content += "---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(stagingDir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write contract draft: %v", err)
	}
	return path
}

// stageDeclaredCompanionContract stages the incident's exact tree: a v2
// contract whose inventory declares a frontmatter-free changelog under the
// only location the envelope schema permits.
func stageDeclaredCompanionContract(t *testing.T, stagingDir, slug string, extraEntries ...string) string {
	t.Helper()
	path := writeV2ContractDraft(t, stagingDir, "XC-axon-"+slug, slug, extraEntries...)
	dir := "axon/provides/" + slug + "/"
	writeStagedSidecar(t, stagingDir, dir+"schema/"+slug+".schema.json", `{"type":"object"}`)
	writeStagedSidecar(t, stagingDir, dir+"fixtures/valid/"+slug+".json", `{}`)
	writeStagedSidecar(t, stagingDir, dir+"artifacts/CHANGELOG.md", "# Changelog\n\n## 0.0.0\n\n- first publication\n")
	return path
}

func newSubmitRig(t *testing.T, stagingDir string) (*fakeSubmitFunnel, *cli.SubmitCommand) {
	t.Helper()
	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	return fake, cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())
}

// TestSubmitCarriesAndPassesADeclaredCompanion is criterion 1 on the submit
// side: the file the incident turned on is carried AND accepted, because
// the descriptor declares it — not because companions are exempt.
func TestSubmitCarriesAndPassesADeclaredCompanion(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := stageDeclaredCompanionContract(t, stagingDir, "widget-c")
	fake, cmd := newSubmitRig(t, stagingDir)

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{path}, io); code != 0 {
		t.Fatalf("code = %d, want 0 — a DECLARED companion must submit; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	var sawCompanion bool
	for _, f := range fake.calls[0].Files {
		if f.Path == "axon/provides/widget-c/artifacts/CHANGELOG.md" {
			sawCompanion = true
		}
	}
	if !sawCompanion {
		t.Fatalf("the declared companion must still be CARRIED; got %+v", fake.calls[0].Files)
	}
}

// TestSubmitRefusesUndeclaredCarriedFile is criteria 4 and 6: a carried
// path the inventory does not declare is refused with POL-013, naming the
// path and the descriptor, BEFORE any git or network work — the injected
// fake funnel records zero Submit calls — and the carried list is printed
// on that refusal path, where before this phase it was printed only on
// success.
//
// TEETH: removing cmd_submit.go's ContractCarriedMembership pre-flight
// makes this exit 0 with one funnel call.
func TestSubmitRefusesUndeclaredCarriedFile(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := stageDeclaredCompanionContract(t, stagingDir, "widget-d")
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-d/artifacts/NOTES.md", "scratch notes\n")
	fake, cmd := newSubmitRig(t, stagingDir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the refusal must land BEFORE any git or network call; funnel was invoked %d time(s)", len(fake.calls))
	}
	stderr := errOut.String()
	for _, want := range []string{
		"POL-013",
		"artifacts/NOTES.md",
		"axon/provides/widget-d/contract.md",
		"changelog", // the role vocabulary, as the next move
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("refusal must name %q; got:\n%s", want, stderr)
		}
	}
	// S-6: the carried list, with each file's classification.
	for _, want := range []string{
		"carried file(s)",
		"axon/provides/widget-d/artifacts/CHANGELOG.md  [declared-companion] role=changelog",
		"axon/provides/widget-d/artifacts/NOTES.md  [undeclared-companion]",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the carried list must be printed on the REFUSAL path and name %q; got:\n%s", want, stderr)
		}
	}
}

// TestSubmitRefusesDeclaredButAbsentFile is criterion 5, S-3's mirror: the
// descriptor must not name a file the space never receives — the failure
// internal/template/contract_scaffold.go:197 already warns about in a
// comment.
//
// TEETH: removing cmd_submit.go's ContractCarriedMembership pre-flight
// makes this exit 0 with one funnel call.
func TestSubmitRefusesDeclaredButAbsentFile(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := stageDeclaredCompanionContract(t, stagingDir, "widget-e",
		"{path: artifacts/MISSING.md, role: other, normative: false, media_type: text/markdown}")
	fake, cmd := newSubmitRig(t, stagingDir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the refusal must land BEFORE any git or network call; funnel was invoked %d time(s)", len(fake.calls))
	}
	stderr := errOut.String()
	for _, want := range []string{"REF-014", "artifacts/MISSING.md", "axon/provides/widget-e/contract.md"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("refusal must name %q; got:\n%s", want, stderr)
		}
	}
}

// allReport is `validate --all`'s JSON output, decoded from OUTSIDE package
// cli (validateReport is unexported) — the same bytes an author's tooling
// reads.
type allReport struct {
	Path   string `json:"path"`
	Error  string `json:"error,omitempty"`
	Result *struct {
		Valid      bool `json:"valid"`
		Violations []struct {
			Code string `json:"code"`
			Path string `json:"path"`
		} `json:"violations"`
	} `json:"result,omitempty"`
}

func runValidateAll(t *testing.T, stagingDir string) (int, []allReport) {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	cmd := cli.NewValidateCommand(validate.New(corpus), stagingDir)
	// An EMPTY space, given explicitly. `--all` asks the space whether a
	// declared companion is merely unchanged on main before calling it
	// missing, and an empty MirrorDir means UNKNOWN — under which the
	// REF-014 direction stays silent and a test asserting it would assert
	// nothing. A temp dir with no contract tree is the honest "the space
	// has none of these" these cases mean.
	cmd.MirrorDir = t.TempDir()
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--all"}, io)
	var reports []allReport
	if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
		t.Fatalf("decode --all report: %v\nstdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	return code, reports
}

// TestValidateAllJudgesWhatSubmitWouldCarry is criteria 7 and 8: `--all`'s
// set is {top-level drafts} ∪ {exactly what submit would carry}, asserted
// as the exact path SET rather than as a count, and green over the
// incident's own tree. A stray `.md` under staging that submit would NOT
// carry stays absent — the widening reuses submit's collector, it is not a
// recursive `*.md` walk (A-2), which would re-create the incident one
// directory over.
//
// TEETH: reverting `--all` to the non-recursive top-level readDir reds the
// set comparison — before this phase the report had ONE line here.
func TestValidateAllJudgesWhatSubmitWouldCarry(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	stageDeclaredCompanionContract(t, stagingDir, "widget-f")
	// Two strays submit would never carry: one outside any contract's
	// carried roots, one at the staging root under no section at all.
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-f/NOTES.md", "not carried by submit\n")
	writeStagedSidecar(t, stagingDir, "axon/docs/scratch.md", "not carried by submit\n")

	code, reports := runValidateAll(t, stagingDir)

	got := map[string]bool{}
	for _, r := range reports {
		got[filepath.ToSlash(strings.TrimPrefix(r.Path, stagingDir+string(filepath.Separator)))] = true
	}
	want := []string{
		"XC-axon-widget-f.md",
		"axon/provides/widget-f/schema/widget-f.schema.json",
		"axon/provides/widget-f/fixtures/valid/widget-f.json",
		"axon/provides/widget-f/artifacts/CHANGELOG.md",
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("`--all` must judge %q — it is what `a2a submit` carries; got %v", w, got)
		}
	}
	for _, unwanted := range []string{"axon/provides/widget-f/NOTES.md", "axon/docs/scratch.md"} {
		if got[unwanted] {
			t.Fatalf("`--all` reported %q, which `a2a submit` does not carry — the widening must reuse the collector, not walk *.md (A-2); got %v", unwanted, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("`--all`'s set must EQUAL {top-level drafts} ∪ {what submit carries}; got %v, want exactly %v", got, want)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0 — the incident's own tree is legal; reports=%+v", code, reports)
	}
}

// TestValidateAllIsADryRunOfSubmitsRefusals is A-4: the same two membership
// refusals submit makes, reported by `--all` over the same tree, so an
// author never learns about them for the first time from a dead PR.
//
// TEETH: dropping stagedCarriedSet.reports' ContractCarriedMembership call
// makes this exit 0 with no codes.
func TestValidateAllIsADryRunOfSubmitsRefusals(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	stageDeclaredCompanionContract(t, stagingDir, "widget-g",
		"{path: artifacts/MISSING.md, role: other, normative: false, media_type: text/markdown}")
	writeStagedSidecar(t, stagingDir, "axon/provides/widget-g/artifacts/NOTES.md", "scratch\n")

	code, reports := runValidateAll(t, stagingDir)
	if code != 1 {
		t.Fatalf("code = %d, want 1 — `--all` must be a true dry run of submit's refusals; reports=%+v", code, reports)
	}
	codes := map[string]string{}
	for _, r := range reports {
		if r.Result == nil {
			continue
		}
		for _, v := range r.Result.Violations {
			codes[v.Code] = r.Path
		}
	}
	if _, ok := codes["POL-013"]; !ok {
		t.Fatalf("expected POL-013 for the undeclared artifacts/NOTES.md; got %+v", reports)
	}
	if line, ok := codes["REF-014"]; !ok {
		t.Fatalf("expected REF-014 for the declared-but-absent artifacts/MISSING.md; got %+v", reports)
	} else if !strings.Contains(filepath.ToSlash(line), "artifacts/MISSING.md") {
		t.Fatalf("REF-014 must be reported against the path the entry points at, got %q", line)
	}
}
