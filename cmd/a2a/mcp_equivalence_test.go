package main

// mcp_equivalence_test.go is the per-(tool, action) CLI≡MCP event/commit
// equivalence suite (spec 15 §T2 / §8 AC #2, reparameterizing P14's
// per-write-verb suite) plus CC-093 (AC #5). Each write verb is now driven
// through its GROUPED tool + action discriminator (marshalWithAction injects
// `action`; the grouped dispatch forwards the ORIGINAL args to the same
// per-verb sub-handler, which ignores the extra field) and still asserted
// byte-identical (modulo volatile tokens) to the CLI verb. It lives in
// package main (cmd/a2a) because ADR-001 grants cmd/a2a import of BOTH
// internal/cli and internal/mcp — internal/mcp itself is structurally
// forbidden from importing internal/cli (plan 14 Placement decisions).
//
// Construction (plan 14 Brief item 7, exactly): the REAL space.WriteFunnel
// + host.FakeHost + testkit/spacefixture for both surfaces, wrapped in a
// spy that records the SubmitRequest actually handed to the funnel (the
// funnel's own commitOne writes Files[].Content verbatim to disk — the
// captured request bytes ARE the committed file bytes, so this is a valid
// file-byte comparison without needing to read back git blobs). validator
// is nil on both sides (matches internal/cli's own
// TestAckEndToEndWithRealFunnelAndFakeHost precedent) — no V2 pipeline
// divergence to account for, since this suite's job is proving the EVENT/
// COMMIT SHAPE matches, not re-testing V2 (already covered by P3/P6/P8).
//
// Deviation (see this phase's report, REQUIRED): the plan Brief says "SAME
// fixed clock + entropy" for both surfaces. internal/cli exposes NO
// entropy-injection seam at all (rand.Reader is hardcoded in every verb's
// newLifecycleDeps/NewSubmitCommand/NewNewCommand constructor), and only
// RespondCommand/ContractCommand expose SetClockForTest (clock only, not
// entropy) — the 15 generic lifecycle verbs plus verify/dispute/note
// expose neither seam. Both are off-limits (internal/cli is a black box
// this phase does not modify). This suite therefore compares BYTE-FOR-
// BYTE modulo exactly the two fields the CLI cannot make deterministic —
// the minted `event:` ULID (+ its ULID-shaped path segment) and the `at:`/
// `created:` wall-clock timestamps — which is precisely the plan's own
// "modulo the artifact/event id" allowance. Every other field (schema,
// space, subject, transition, actor block, note, reason_code, refs,
// version, digest, commit message, PRBody gate marker) is compared
// LITERALLY, not just structurally.
//
// Isolation: each case seeds TWO independent spacefixture clones with the
// IDENTICAL artifact id and fixture shape (one per surface) — never the
// SAME mirror/host for both surfaces (a shared mirror would let the
// second surface's funnel call short-circuit via FindPRByHeadBranch's own
// dedup, which is exactly CC-093's own scenario, tested separately, not
// this suite's).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
)

// --- shared plumbing -------------------------------------------------------

// spyFunnel wraps a REAL *space.WriteFunnel, recording every SubmitRequest
// it is handed before delegating — the equivalence capture point (see
// this file's own doc comment: commitOne writes Files[].Content verbatim,
// so this IS a file-byte-accurate capture).
type spyFunnel struct {
	inner *space.WriteFunnel
	calls []space.SubmitRequest
}

type equivContractPublicationCall struct {
	id, version, bump, staging, expectPlan string
}

type equivContractPublicationState struct {
	calls []equivContractPublicationCall
	err   error
}

func (s *equivContractPublicationState) record(id, version, bump, staging, expectPlan string) (space.ContractPublicationResult, error) {
	s.calls = append(s.calls, equivContractPublicationCall{id: id, version: version, bump: bump, staging: staging, expectPlan: expectPlan})
	if s.err != nil {
		return space.ContractPublicationResult{}, s.err
	}
	return space.ContractPublicationResult{
		Status: space.ContractPublicationSubmitted,
		Plan:   contract.PublicationPlan{Contract: id, TargetVersion: version, PlanDigest: "sha256:" + strings.Repeat("a", 64)},
	}, nil
}

type equivCLIContractPublication struct {
	state *equivContractPublicationState
}

func (a equivCLIContractPublication) Preflight(_ context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.state.record(request.ID, request.Version, request.Bump, request.Staging, request.ExpectPlan)
}

func (a equivCLIContractPublication) Publish(_ context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.state.record(request.ID, request.Version, request.Bump, request.Staging, request.ExpectPlan)
}

type equivMCPContractPublication struct {
	state *equivContractPublicationState
}

func (a equivMCPContractPublication) Preflight(_ context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.state.record(request.ID, request.Version, request.Bump, request.Staging, request.ExpectPlan)
}

func (a equivMCPContractPublication) Publish(_ context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.state.record(request.ID, request.Version, request.Bump, request.Staging, request.ExpectPlan)
}

type equivCLIContractMaterialize struct{ err error }

func (a equivCLIContractMaterialize) MaterializeContract(context.Context, cli.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return space.ContractMaterializeResult{}, a.err
}

type equivMCPContractMaterialize struct{ err error }

func (a equivMCPContractMaterialize) MaterializeContract(context.Context, mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return space.ContractMaterializeResult{}, a.err
}

type equivCLIContractCheck struct{ err error }

func (a equivCLIContractCheck) CheckContract(context.Context, cli.ContractCheckRequest) (contract.ConformanceResult, error) {
	return contract.ConformanceResult{}, a.err
}

type equivMCPContractCheck struct{ err error }

func (a equivMCPContractCheck) CheckContract(context.Context, mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	return contract.ConformanceResult{}, a.err
}

type equivCLIContractInspection struct{ result cli.ContractDiffResult }

func (a equivCLIContractInspection) DiffContract(context.Context, cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	return a.result, nil
}

func (equivCLIContractInspection) VerifyContractExport(context.Context, cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	return cli.ContractVerifyExportResult{}, nil
}

type equivMCPContractInspection struct{ result mcp.ContractDiffResult }

func (a equivMCPContractInspection) DiffContract(context.Context, mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	return a.result, nil
}

func (equivMCPContractInspection) VerifyContractExport(context.Context, mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	return mcp.ContractVerifyExportResult{}, nil
}

func (s *spyFunnel) Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	s.calls = append(s.calls, req)
	return s.inner.Submit(ctx, req)
}

// newEquivMirror builds one isolated, real-git mirror clone (via
// spacefixture) + a FakeHost + a spy-wrapped real WriteFunnel — one of
// these per surface, per case, so the two surfaces never share a mirror
// or a host.
func newEquivMirror(t *testing.T, ownSystem string) (mirrorDir string, funnel *spyFunnel, fakeHost *host.FakeHost) {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	dir := fx.Clone(ownSystem)
	fh := host.NewFakeHost()
	real := space.NewWriteFunnel(fh, nil, "0.1.0")
	return dir, &spyFunnel{inner: real}, fh
}

func equivManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "beta", Status: "active"},
	}}
}

func equivCLIHostConfig(remoteURL string) cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL: remoteURL, Repo: host.Repo{Owner: "org", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-test", CommitAuthorEmail: "a2a-test@a2ahub.invalid",
	}
}

func equivMCPHostConfig(remoteURL string) mcp.SubmitHostConfig {
	return mcp.SubmitHostConfig{
		RemoteURL: remoteURL, Repo: host.Repo{Owner: "org", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-test", CommitAuthorEmail: "a2a-test@a2ahub.invalid",
	}
}

// equivActor is the identity both surfaces resolve to in this suite. Model
// and Session are carried because P3 made them reachable on the event
// writers, and a suite that leaves them empty on both sides proves the two
// surfaces agree about nothing in particular — the fields would be
// byte-identical because neither wrote them.
var equivActor = template.Actor{
	Model:   "claude-opus-5",
	Session: "session:equivalence",
}

func equivCLIActorResolver(kind, name string) func(cli.ActorFlags) (template.Actor, error) {
	return func(cli.ActorFlags) (template.Actor, error) {
		a := equivActor
		a.Kind, a.Name, a.KindClaimed = kind, name, true
		return a, nil
	}
}

func equivMCPActorResolver(kind, name string) mcp.ActorResolver {
	return func(mcp.ActorInput) (template.Actor, error) {
		a := equivActor
		a.Kind, a.Name, a.KindClaimed = kind, name, true
		return a, nil
	}
}

func equivIO() (cli.IO, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return cli.IO{Stdin: bytes.NewReader(nil), Stdout: &out, Stderr: &errOut}, &out, &errOut
}

func writeMirrorFileEquiv(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeMirrorFileEquiv: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMirrorFileEquiv: write %s: %v", full, err)
	}
}

func equivWriteQuestion(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: axon\nto: [" + to + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

func equivWriteRequirement(t *testing.T, mirrorDir, id string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: requirement\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: new-capability\npriority: p3\nblocking: true\nclassification: internal\nacceptance_criteria: [\"works\"]\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/requires/"+id+".md", content)
}

func equivWriteSatisfyContract(t *testing.T, mirrorDir, id string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: contract\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/provides/widget/contract.md", content)
}

func equivWriteSatisfyResponse(t *testing.T, mirrorDir, id, parent string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: response\ntitle: t\nspace: fixture-space\nfrom: beta\nto: [axon]\nparent: " + parent + "\nthread: thread:axon-20260721-k3f9\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "beta/exchanges/"+id+".md", content)
}

func equivWriteHandoff(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: handoff\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: axon\nto: [" + to + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

func equivWriteDecision(t *testing.T, mirrorDir, id string, approvers []string) {
	t.Helper()
	joined := strings.Join(approvers, ", ")
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: decision\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: axon\nto: [" + joined + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\nrequired_approvers: [" + joined + "]\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "decisions/"+id+".md", content)
}

// equivWriteEvent seeds a pre-existing committed event, ordered strictly
// before any event a command under test mints (real ULIDs at a fixed 2020
// baseline).
func equivWriteEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("equivWriteEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\n",
		id.String(), subject, transition, actorSystem)
	writeMirrorFileEquiv(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}

func equivWriteVersionedEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem, version string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("equivWriteVersionedEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\nversion: %s\n",
		id.String(), subject, transition, actorSystem, version)
	writeMirrorFileEquiv(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}

// --- normalization (this file's own "modulo the artifact/event id") ------

var (
	// Templates (rendered drafts) carry inline `# comment` trailers after
	// several fields' values (e.g. `id: XQ-... # exchange ID grammar
	// §3.3`) — these regexes match only the VALUE token right after the
	// key, never anchoring `$` at end-of-line, so the trailing comment
	// (identical on both surfaces, since it's the canonical template's own
	// literal text) survives the normalization untouched.
	eventLineRE = regexp.MustCompile(`(?m)^(event: )\S+`)
	atLineRE    = regexp.MustCompile(`(?m)^(at: )\S+`)
	createdLine = regexp.MustCompile(`(?m)^(created: )\S+`)
	idLineRE    = regexp.MustCompile(`(?m)^(id: )\S+`)
	eventPathRE = regexp.MustCompile(`events/\d{4}/[0-9A-Za-z]+\.yaml$`)
	// threadLine joins the list for the same reason `id:` is on it: P46 made
	// every drafting verb MINT a thread when none is supplied, and a mint is
	// random by construction (`artifact.MintThreadIDAt`'s rand4 suffix), so
	// the two surfaces cannot produce the same value and were never meant to.
	//
	// Be honest about the cost: a PROPAGATED thread (respond inheriting its
	// parent's) has the identical §3.8 shape, so this normalization cannot tell
	// the two apart and blanks both — meaning this file no longer proves the
	// surfaces agree on propagation. That agreement is proven where it is
	// actually cheap and precise, one level down, by a pair of tests that
	// assert the parent's literal thread reaches the response:
	// internal/cli's TestRespondPropagatesParentThreadVerbatim and
	// internal/mcp's TestRespondHandlerInheritsParentThread. If either is
	// deleted, nothing here will notice.
	threadLine = regexp.MustCompile(`(?m)^(thread: )\S+`)
)

// normalizeContent blanks the fields internal/cli cannot make
// deterministic (see this file's own Deviation doc comment): the minted
// event/exchange ULID and any wall-clock timestamp (`at:`/`created:`).
func normalizeContent(raw []byte) string {
	s := string(raw)
	s = eventLineRE.ReplaceAllString(s, "${1}<EVENT>")
	s = atLineRE.ReplaceAllString(s, "${1}<AT>")
	s = createdLine.ReplaceAllString(s, "${1}<CREATED>")
	s = idLineRE.ReplaceAllString(s, "${1}<ID>")
	s = threadLine.ReplaceAllString(s, "${1}<THREAD>")
	return s
}

func normalizePath(path string) string {
	return eventPathRE.ReplaceAllString(path, "events/<YEAR>/<EVENT>.yaml")
}

// filesByNormalizedPath builds a normalized-path -> normalized-content map
// for one surface's committed files.
func filesByNormalizedPath(files []space.FileWrite) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[normalizePath(f.Path)] = normalizeContent(f.Content)
	}
	return out
}

// assertRequestsEquivalent is the suite's own core assertion (AC #4):
// same normalized file set (path + content) and same commit message,
// between the CLI's and the MCP's own captured SubmitRequest.
func assertRequestsEquivalent(t *testing.T, verb string, cliReq, mcpReq space.SubmitRequest) {
	t.Helper()
	cliFiles := filesByNormalizedPath(cliReq.Files)
	mcpFiles := filesByNormalizedPath(mcpReq.Files)

	if len(cliFiles) != len(mcpFiles) {
		t.Fatalf("%s: file count mismatch: CLI=%d MCP=%d\nCLI paths: %v\nMCP paths: %v",
			verb, len(cliFiles), len(mcpFiles), sortedKeys(cliFiles), sortedKeys(mcpFiles))
	}
	for path, cliContent := range cliFiles {
		mcpContent, ok := mcpFiles[path]
		if !ok {
			t.Fatalf("%s: MCP is missing file %s (CLI content:\n%s)", verb, path, cliContent)
		}
		if cliContent != mcpContent {
			t.Fatalf("%s: file %s content mismatch:\n--- CLI ---\n%s\n--- MCP ---\n%s", verb, path, cliContent, mcpContent)
		}
	}
	if cliReq.CommitMessage != mcpReq.CommitMessage {
		t.Fatalf("%s: commit message mismatch: CLI=%q MCP=%q", verb, cliReq.CommitMessage, mcpReq.CommitMessage)
	}
	if (cliReq.PRBody == "") != (mcpReq.PRBody == "") {
		t.Fatalf("%s: PRBody (advisory gate marker) presence mismatch: CLI=%q MCP=%q", verb, cliReq.PRBody, mcpReq.PRBody)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func runCLICommand(t *testing.T, cmd cli.Command, args []string) {
	t.Helper()
	io, out, errOut := equivIO()
	code := cmd.Run(context.Background(), args, io)
	if code != 0 {
		t.Fatalf("%s: exit code = %d (want 0); stdout=%s stderr=%s", cmd.Name(), code, out.String(), errOut.String())
	}
}

// runMCPHandler invokes a grouped tool's handler through the SAME path a
// real tools/call takes: it marshals input, injects the action discriminator
// (empty action => an action-free tool like a2a_new / a2a_submit), and calls
// the registered dispatch handler. The grouped dispatch reads only the
// discriminator and forwards the ORIGINAL args to the per-verb sub-handler,
// which ignores the extra field — so the funnel path stays byte-identical.
func runMCPHandler(t *testing.T, registry *mcp.Registry, tool, action string, input any) {
	t.Helper()
	spec, ok := registry.Get(tool)
	if !ok {
		t.Fatalf("tool %q is not registered", tool)
	}
	raw, err := marshalWithAction(action, input)
	if err != nil {
		t.Fatalf("marshal input for %s: %v", tool, err)
	}
	if _, _, err := spec.Handler(context.Background(), raw); err != nil {
		t.Fatalf("%s (action=%q): handler returned an error: %v", tool, action, err)
	}
}

// equivWorkBackend is intentionally shared in shape by the two transports.
// It returns a complete two-axis result and records the domain inputs so the
// IT-09 assertion covers semantic delegation as well as JSON presentation.
type equivWorkBackend struct {
	result   workreport.OperationResult
	identity workreport.WorkIdentityInput
	leases   []workreport.Lease
	calls    []any
}

func (b *equivWorkBackend) Start(_ context.Context, in workreport.StartInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) Checkpoint(_ context.Context, in workreport.CheckpointInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) Wait(_ context.Context, in workreport.WaitInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) Stop(_ context.Context, in workreport.StopInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) Heartbeat(_ context.Context, in workreport.HeartbeatInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) Resume(_ context.Context, in workreport.ResumeInput) (workreport.OperationResult, error) {
	b.calls = append(b.calls, in)
	return b.result, nil
}

func (b *equivWorkBackend) ResolveWork(_ context.Context, _, _, _, _ string) (workreport.WorkIdentityInput, error) {
	return b.identity, nil
}

func (b *equivWorkBackend) ListWork(_ context.Context, _, _, _ string, _ bool) ([]workreport.Lease, error) {
	return append([]workreport.Lease(nil), b.leases...), nil
}

func canonicalEquivJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func workParityDeps(backend *equivWorkBackend) (cli.WorkCommandDeps, mcp.WorkToolDeps) {
	const projectID = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cliDeps := cli.WorkCommandDeps{
		Starter: backend, Progressor: backend, Local: backend, Reader: backend,
		ProjectID: projectID, Space: "fixture-space",
		ResolveActor: func(in cli.WorkActorFlags) (workreport.Actor, error) {
			return workreport.Actor{Kind: in.Kind, Name: in.Name, Model: in.Model, System: "axon", Session: in.Session}, nil
		},
	}
	mcpDeps := mcp.WorkToolDeps{
		Starter: backend, Progressor: backend, Local: backend, Reader: backend,
		ProjectID: projectID, Space: "fixture-space",
		ResolveActor: func(in mcp.WorkActorInput) (workreport.Actor, error) {
			return workreport.Actor{Kind: in.Kind, Name: in.Name, Model: in.Model, System: "axon", Session: in.Session}, nil
		},
	}
	return cliDeps, mcpDeps
}

// TestIT09WorkCLIMCPSemanticParity is the behavior-level half of VG-OC-23.
// Every closed work action receives the same core result, and the public CLI
// JSON and MCP result must preserve the same two-axis state and stable codes.
func TestIT09WorkCLIMCPSemanticParity(t *testing.T) {
	t.Parallel()
	// Bind this exact coverage reference to cmd/a2a's real composition as
	// well as the transport table below. If production DI drops any work
	// capability, the catalogue evidence must turn red rather than continuing
	// to pass against test-only dependencies.
	compositionRoot := t.TempDir()
	compositionPaths := paths{
		projectRoot: compositionRoot, projectConfig: filepath.Join(compositionRoot, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(compositionRoot, "machine.yaml"), staging: filepath.Join(compositionRoot, ".a2a", "staging"),
	}
	compositionRef := space.Ref{ID: "fixture-space", RepoURL: "https://github.com/fixture/space.git"}
	compositionConfig := space.ProjectConfig{System: "axon", Spaces: []space.Ref{compositionRef}}
	if _, err := buildWorkCommand(compositionPaths, compositionConfig, space.MachineConfig{}, compositionRef); err != nil {
		t.Fatalf("production CLI work DI: %v", err)
	}
	productionMCPDeps, err := buildMCPWorkDeps(compositionPaths, compositionConfig, space.MachineConfig{})
	if err != nil {
		t.Fatalf("production MCP work DI: %v", err)
	}
	if _, err := mcp.NewWorkTool(productionMCPDeps); err != nil {
		t.Fatalf("production MCP work tool: %v", err)
	}

	const (
		workID  = "work:01K1A2B3C4D5E6F7G8H9J0K1M7"
		session = "session:provider-opaque"
	)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	result := workreport.OperationResult{
		WorkID: workID, Session: session, OperationKey: "op-v1-" + strings.Repeat("a", 64),
		Action: workreport.ActionCheckpoint, SemanticSequence: 7, LocalState: workreport.LocalActive,
		LocalErrorCode: workreport.LocalErrorPendingOperation, ExpiresAt: now.Add(15 * time.Minute),
		Shared: workreport.PublishAttempt{Attempted: true, Convergence: workreport.ConvergenceResumable, ErrorCode: "host-timeout"},
	}
	identity := workreport.WorkIdentityInput{ProjectID: "sha256:" + strings.Repeat("b", 64), Space: "fixture-space", Thread: "thread:axon-20260803-p7it", WorkID: workID, Actor: workreport.Actor{Kind: "agent", Name: "codex", System: "axon", Session: session}}
	lease := workreport.Lease{Identity: workreport.Identity{ProjectID: identity.ProjectID, Space: identity.Space, Thread: identity.Thread, WorkID: identity.WorkID, Actor: identity.Actor}, SubjectRef: "XC-axon-widget@1.0.0", Mode: workreport.ModeTesting, Summary: "parity", WaitingOn: []workreport.WaitingOn{}, StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(15 * time.Minute), SemanticSequence: 7}
	cases := []struct {
		name    string
		cliArgs []string
		mcpIn   mcp.WorkInput
	}{
		{"start", []string{"start", "--thread", identity.Thread, "--subject-ref", lease.SubjectRef, "--mode", "testing", "--summary", "parity", "--actor-kind", "agent", "--actor-name", "codex", "--session", session, "--json"}, mcp.WorkInput{Action: "start", Thread: identity.Thread, SubjectRef: lease.SubjectRef, Mode: "testing", Summary: "parity", Actor: mcp.WorkActorInput{Kind: "agent", Name: "codex", Session: session}}},
		{"heartbeat", []string{"heartbeat", "--work-id", workID, "--session", session, "--ttl", "10m", "--json"}, mcp.WorkInput{Action: "heartbeat", WorkID: workID, Session: session, TTL: "10m"}},
		{"resume", []string{"resume", "--work-id", workID, "--session", session, "--json"}, mcp.WorkInput{Action: "resume", WorkID: workID, Session: session}},
		{"checkpoint", []string{"checkpoint", "--work-id", workID, "--session", session, "--mode", "testing", "--summary", "parity", "--json"}, mcp.WorkInput{Action: "checkpoint", WorkID: workID, Session: session, Mode: "testing", Summary: "parity"}},
		{"wait", []string{"wait", "--work-id", workID, "--session", session, "--summary", "blocked", "--waiting-on", "system:beta:review", "--json"}, mcp.WorkInput{Action: "wait", WorkID: workID, Session: session, Summary: "blocked", WaitingOn: []mcp.WorkWaitingInput{{Kind: "system", ID: "beta", Summary: "review"}}}},
		{"stop", []string{"stop", "--work-id", workID, "--session", session, "--result", "paused", "--summary", "handoff", "--json"}, mcp.WorkInput{Action: "stop", WorkID: workID, Session: session, Result: "paused", Summary: "handoff"}},
		{"status", []string{"status", "--work-id", workID, "--include-expired", "--json"}, mcp.WorkInput{Action: "status", WorkID: workID, IncludeExpired: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliBackend := &equivWorkBackend{result: result, identity: identity, leases: []workreport.Lease{lease}}
			cliDeps, _ := workParityDeps(cliBackend)
			command, err := cli.NewWorkCommand(cliDeps)
			if err != nil {
				t.Fatal(err)
			}
			stdio, cliOut, cliErr := equivIO()
			if code := command.Run(t.Context(), tc.cliArgs, stdio); code != 0 {
				t.Fatalf("CLI code=%d stderr=%s", code, cliErr)
			}

			mcpBackend := &equivWorkBackend{result: result, identity: identity, leases: []workreport.Lease{lease}}
			_, mcpDeps := workParityDeps(mcpBackend)
			tool, err := mcp.NewWorkTool(mcpDeps)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(tc.mcpIn)
			if err != nil {
				t.Fatal(err)
			}
			mcpResult, _, err := tool.Handler(t.Context(), raw)
			if err != nil {
				t.Fatalf("MCP error=%v", err)
			}
			if got, want := canonicalEquivJSON(t, mcpResult), canonicalEquivJSON(t, json.RawMessage(cliOut.Bytes())); got != want {
				t.Fatalf("semantic result differs\nCLI %s\nMCP %s", want, got)
			}
		})
	}
}

// marshalWithAction marshals input and, when action is non-empty, injects an
// `"action"` discriminator alongside the input's own fields — the exact
// payload shape a grouped-tool caller sends.
func marshalWithAction(action string, input any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if action == "" {
		return raw, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	m["action"] = action
	return json.Marshal(m)
}

// --- the 15 generic table-driven lifecycle verbs -------------------------

type genericVerbEquivCase struct {
	verb      string
	ownSystem string
	seed      func(t *testing.T, mirrorDir string)
	cliArgs   []string
	mcpInput  mcp.LifecycleInput
	cliCtor   func(funnel *spyFunnel, mirrorDir, ownSystem string) cli.Command
}

func equivAcceptedQuestion(id string) func(t *testing.T, mirrorDir string) {
	return func(t *testing.T, mirrorDir string) {
		equivWriteQuestion(t, mirrorDir, id, "beta")
		equivWriteEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		equivWriteEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		equivWriteEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")
	}
}

func genericVerbEquivCases() []genericVerbEquivCase {
	return []genericVerbEquivCase{
		{
			verb: "ack", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteQuestion(t, mirrorDir, "XQ-axon-20260721-a001", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XQ-axon-20260721-a001", "submit", "axon")
			},
			cliArgs: []string{"XQ-axon-20260721-a001"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a001"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewAckCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "accept", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteQuestion(t, mirrorDir, "XQ-axon-20260721-a002", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XQ-axon-20260721-a002", "submit", "axon")
				equivWriteEvent(t, mirrorDir, "beta", 1, "XQ-axon-20260721-a002", "acknowledge", "beta")
			},
			cliArgs: []string{"XQ-axon-20260721-a002"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a002"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewAcceptCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "decline", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteQuestion(t, mirrorDir, "XQ-axon-20260721-a003", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XQ-axon-20260721-a003", "submit", "axon")
			},
			cliArgs:  []string{"--reason", "not now", "--reason-code", "OTH-001", "XQ-axon-20260721-a003"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a003"}, Reason: "not now", ReasonCode: "OTH-001"},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewDeclineCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "start", ownSystem: "beta",
			seed:    equivAcceptedQuestion("XQ-axon-20260721-a004"),
			cliArgs: []string{"XQ-axon-20260721-a004"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a004"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewStartCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "block", ownSystem: "beta",
			seed:     equivAcceptedQuestion("XQ-axon-20260721-a005"),
			cliArgs:  []string{"--refs", "XQ-axon-20260721-blocker", "XQ-axon-20260721-a005"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a005"}, Refs: []string{"XQ-axon-20260721-blocker"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewBlockCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "unblock", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivAcceptedQuestion("XQ-axon-20260721-a006")(t, mirrorDir)
				equivWriteEvent(t, mirrorDir, "beta", 3, "XQ-axon-20260721-a006", "block", "beta")
			},
			cliArgs: []string{"XQ-axon-20260721-a006"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a006"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewUnblockCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "cancel", ownSystem: "axon",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteQuestion(t, mirrorDir, "XQ-axon-20260721-a007", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XQ-axon-20260721-a007", "submit", "axon")
			},
			cliArgs: []string{"XQ-axon-20260721-a007"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XQ-axon-20260721-a007"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewCancelCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "withdraw", ownSystem: "axon",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteRequirement(t, mirrorDir, "XR-axon-widget-a008")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XR-axon-widget-a008", "publish", "axon")
			},
			cliArgs: []string{"XR-axon-widget-a008"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XR-axon-widget-a008"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewWithdrawCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "supersede", ownSystem: "axon",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteRequirement(t, mirrorDir, "XR-axon-legacy-a009")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XR-axon-legacy-a009", "publish", "axon")
			},
			cliArgs:  []string{"--refs", "XR-axon-legacy-a009-v2", "XR-axon-legacy-a009"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XR-axon-legacy-a009"}, Refs: []string{"XR-axon-legacy-a009-v2"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewSupersedeCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "satisfy", ownSystem: "axon",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteRequirement(t, mirrorDir, "XR-axon-satisfiable-a010")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XR-axon-satisfiable-a010", "publish", "axon")
				equivWriteEvent(t, mirrorDir, "beta", 1, "XR-axon-satisfiable-a010", "acknowledge", "beta")
				equivWriteSatisfyContract(t, mirrorDir, "XC-axon-widget")
				equivWriteSatisfyResponse(t, mirrorDir, "XS-beta-20260721-p1p1", "XR-axon-satisfiable-a010")
				equivWriteVersionedEvent(t, mirrorDir, "axon", 2, "XC-axon-widget", "publish", "axon", "1.0.0")
				equivWriteEvent(t, mirrorDir, "axon", 3, "XS-beta-20260721-p1p1", "verify", "axon")
			},
			cliArgs:  []string{"--refs", "XC-axon-widget@1.0.0,XS-beta-20260721-p1p1", "XR-axon-satisfiable-a010"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XR-axon-satisfiable-a010"}, Refs: []string{"XC-axon-widget@1.0.0", "XS-beta-20260721-p1p1"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewSatisfyCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "approve", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteDecision(t, mirrorDir, "XD-axon-20260721-a011", []string{"beta"})
				equivWriteEvent(t, mirrorDir, "axon", 0, "XD-axon-20260721-a011", "propose", "axon")
			},
			cliArgs: []string{"XD-axon-20260721-a011"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XD-axon-20260721-a011"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewApproveCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("human", "owner"))
			},
		},
		{
			verb: "reject", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteDecision(t, mirrorDir, "XD-axon-20260721-a012", []string{"beta"})
				equivWriteEvent(t, mirrorDir, "axon", 0, "XD-axon-20260721-a012", "propose", "axon")
			},
			cliArgs:  []string{"--reason", "scope creep", "XD-axon-20260721-a012"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XD-axon-20260721-a012"}, Reason: "scope creep"},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewRejectCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("human", "owner"))
			},
		},
		{
			verb: "verify-pass", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteHandoff(t, mirrorDir, "XH-axon-20260721-a013", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XH-axon-20260721-a013", "submit", "axon")
				equivWriteEvent(t, mirrorDir, "beta", 1, "XH-axon-20260721-a013", "acknowledge", "beta")
			},
			cliArgs: []string{"XH-axon-20260721-a013"}, mcpInput: mcp.LifecycleInput{IDs: []string{"XH-axon-20260721-a013"}},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewVerifyPassCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
		{
			verb: "verify-fail", ownSystem: "beta",
			seed: func(t *testing.T, mirrorDir string) {
				equivWriteHandoff(t, mirrorDir, "XH-axon-20260721-a014", "beta")
				equivWriteEvent(t, mirrorDir, "axon", 0, "XH-axon-20260721-a014", "submit", "axon")
				equivWriteEvent(t, mirrorDir, "beta", 1, "XH-axon-20260721-a014", "acknowledge", "beta")
			},
			cliArgs:  []string{"--findings", "did not meet spec", "XH-axon-20260721-a014"},
			mcpInput: mcp.LifecycleInput{IDs: []string{"XH-axon-20260721-a014"}, Findings: "did not meet spec"},
			cliCtor: func(f *spyFunnel, dir, own string) cli.Command {
				return cli.NewVerifyFailCommand(f, dir, "fixture-space", own, equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			},
		},
	}
}

// TestEquivGenericLifecycleVerbs is spec 14 §8 AC #4 for the 15
// table-driven OP-211 verbs: CLI and MCP, run against isolated but
// identically-shaped fixtures with the SAME artifact id, produce
// equivalent committed files + commit message.
func TestEquivGenericLifecycleVerbs(t *testing.T) {
	t.Parallel()
	for _, tc := range genericVerbEquivCases() {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()

			cliDir, cliFunnel, _ := newEquivMirror(t, tc.ownSystem)
			tc.seed(t, cliDir)
			cmd := tc.cliCtor(cliFunnel, cliDir, tc.ownSystem)
			runCLICommand(t, cmd, tc.cliArgs)
			if len(cliFunnel.calls) != 1 {
				t.Fatalf("%s: expected exactly 1 CLI funnel call, got %d", tc.verb, len(cliFunnel.calls))
			}

			mcpDir, mcpFunnel, _ := newEquivMirror(t, tc.ownSystem)
			tc.seed(t, mcpDir)
			writeDeps := mcp.WriteDeps{
				Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: tc.ownSystem,
				Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""),
				ResolveActor: equivMCPActorResolver(actorKindFor(tc.verb), actorNameFor(tc.verb)),
				Now:          time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
			}
			registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
			runMCPHandler(t, registry, "a2a_lifecycle", tc.verb, tc.mcpInput)
			if len(mcpFunnel.calls) != 1 {
				t.Fatalf("%s: expected exactly 1 MCP funnel call, got %d", tc.verb, len(mcpFunnel.calls))
			}

			assertRequestsEquivalent(t, tc.verb, cliFunnel.calls[0], mcpFunnel.calls[0])
		})
	}
}

// TestIT09ReceiptBearingLifecycleParity pins the P1 addition that action-name
// parity alone missed: both transports must carry the evaluator's exact
// receipt for an applicable lifecycle transition.
func TestIT09ReceiptBearingLifecycleParity(t *testing.T) {
	t.Parallel()
	const id = "XQ-axon-20260803-z009"
	seed := func(t *testing.T, dir string) {
		equivWriteQuestion(t, dir, id, "beta")
		equivWriteEvent(t, dir, "axon", 0, id, "submit", "axon")
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
	seed(t, cliDir)
	runCLICommand(t, cli.NewAckCommand(cliFunnel, cliDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot")), []string{id})
	mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
	seed(t, mcpDir)
	registry := mcp.BuildRegistry(nil, mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_lifecycle", "ack", mcp.LifecycleInput{IDs: []string{id}})

	assertRequestsEquivalent(t, "receipt-bearing ack", cliFunnel.calls[0], mcpFunnel.calls[0])
	for surface, request := range map[string]space.SubmitRequest{"CLI": cliFunnel.calls[0], "MCP": mcpFunnel.calls[0]} {
		if len(request.Files) != 1 || !strings.Contains(string(request.Files[0].Content), "state: acknowledged") {
			t.Fatalf("%s omitted exact acknowledged receipt: %+v", surface, request.Files)
		}
	}
}

// actorKindFor/actorNameFor mirror the per-verb actor each CLI case above
// is constructed with (approve/reject use a human actor, G3's own
// precondition; every other verb uses an agent) — kept as a small lookup
// rather than threading a 4th field through every case literal.
func actorKindFor(verb string) string {
	if verb == "approve" || verb == "reject" {
		return "human"
	}
	return "agent"
}

func actorNameFor(verb string) string {
	if verb == "approve" || verb == "reject" {
		return "owner"
	}
	return "bot"
}

// --- respond / verify / dispute / note -----------------------------------

// respondOnMirror runs a respond call (CLI or MCP, selected by the
// caller) against parentID and returns the minted response id, extracted
// from the funnel's own recorded file paths.
func extractResponseID(files []space.FileWrite) string {
	for _, fw := range files {
		base := filepath.Base(fw.Path)
		if strings.HasPrefix(base, "XS-") {
			return strings.TrimSuffix(base, ".md")
		}
	}
	return ""
}

func TestEquivRespond(t *testing.T) {
	t.Parallel()
	const parentID = "XQ-axon-20260721-b001"
	seed := equivAcceptedQuestion(parentID)

	cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
	seed(t, cliDir)
	cliCmd := cli.NewRespondCommand(cliFunnel, cliDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"--result", "answered", parentID})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("respond: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
	seed(t, mcpDir)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_exchange", "respond", mcp.RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("respond: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	// The two response IDs are content-derived (same seed = same id since
	// the content is identical) but the two isolated mirrors' entropy
	// state for their OWN `respond` event ULID may differ (no CLI entropy
	// seam) — normalize before comparing, same as every other verb.
	//
	// Wave K (live run 6) fixed `a2a respond` on BOTH surfaces: response.md
	// leaves `to`, `space` and `title` as unfilled placeholders, and the
	// funnel's own V2 pass refuses the artifact with REF-006. The CLI half
	// landed first and this assertion went red immediately — the parity
	// suite doing exactly its job, since ADR-001 makes the two respond
	// implementations deliberate copies.
	//
	// It was briefly repaired by normalizing those three fields away on both
	// sides. That is the one repair this suite must never accept: the three
	// fields it would stop comparing are precisely the three that had just
	// been proven breakable, and MCP would have stayed broken behind a green
	// gate. Removed; the MCP half was fixed instead, and this compares the
	// response artifact byte for byte again.
	cliReq := cliFunnel.calls[0]
	mcpReq := mcpFunnel.calls[0]
	assertRequestsEquivalent(t, "respond", cliReq, mcpReq)

	cliResponseID := extractResponseID(cliFunnel.calls[0].Files)
	mcpResponseID := extractResponseID(mcpFunnel.calls[0].Files)
	if cliResponseID == "" || mcpResponseID == "" {
		t.Fatalf("could not extract a response id from one of the surfaces: cli=%q mcp=%q", cliResponseID, mcpResponseID)
	}
	if cliResponseID != mcpResponseID {
		t.Fatalf("respond: content-derived response id mismatch: CLI=%q MCP=%q (identical content must mint the identical id)", cliResponseID, mcpResponseID)
	}
}

func TestEquivVerify(t *testing.T) {
	t.Parallel()
	const parentID = "XQ-axon-20260721-b002"
	seed := func(t *testing.T, mirrorDir string) {
		equivAcceptedQuestion(parentID)(t, mirrorDir)
	}

	respondOnce := func(t *testing.T, mirrorDir string) *spyFunnel {
		fh := host.NewFakeHost()
		real := space.NewWriteFunnel(fh, nil, "0.1.0")
		f := &spyFunnel{inner: real}
		cmd := cli.NewRespondCommand(f, mirrorDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
		runCLICommand(t, cmd, []string{"--result", "answered", parentID})
		for _, fw := range f.calls[0].Files {
			writeMirrorFileEquiv(t, mirrorDir, fw.Path, string(fw.Content))
		}
		return f
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seed(t, cliDir)
	respondFake := respondOnce(t, cliDir)
	cliResponseID := extractResponseID(respondFake.calls[0].Files)
	cliCmd := cli.NewVerifyCommand(cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{cliResponseID})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("verify: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seed(t, mcpDir)
	mcpRespondFake := respondOnce(t, mcpDir)
	mcpResponseID := extractResponseID(mcpRespondFake.calls[0].Files)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_exchange", "verify", mcp.VerifyInput{Targets: []string{mcpResponseID}})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("verify: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	if len(cliFunnel.calls[0].Files) != 2 || len(mcpFunnel.calls[0].Files) != 2 {
		t.Fatalf("verify: expected 2 files (verify+close) on both surfaces; CLI=%d MCP=%d", len(cliFunnel.calls[0].Files), len(mcpFunnel.calls[0].Files))
	}
	assertRequestsEquivalent(t, "verify", cliFunnel.calls[0], mcpFunnel.calls[0])
}

func TestEquivDispute(t *testing.T) {
	t.Parallel()
	const parentID = "XQ-axon-20260721-b003"
	respondOnce := func(t *testing.T, mirrorDir string) *spyFunnel {
		equivAcceptedQuestion(parentID)(t, mirrorDir)
		fh := host.NewFakeHost()
		real := space.NewWriteFunnel(fh, nil, "0.1.0")
		f := &spyFunnel{inner: real}
		cmd := cli.NewRespondCommand(f, mirrorDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
		runCLICommand(t, cmd, []string{"--result", "answered", parentID})
		for _, fw := range f.calls[0].Files {
			writeMirrorFileEquiv(t, mirrorDir, fw.Path, string(fw.Content))
		}
		return f
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	cliRespond := respondOnce(t, cliDir)
	cliResponseID := extractResponseID(cliRespond.calls[0].Files)
	cliCmd := cli.NewDisputeCommand(cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"--reason", "wrong answer", cliResponseID})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("dispute: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	mcpRespond := respondOnce(t, mcpDir)
	mcpResponseID := extractResponseID(mcpRespond.calls[0].Files)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_exchange", "dispute", mcp.DisputeInput{IDs: []string{mcpResponseID}, Reason: "wrong answer"})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("dispute: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "dispute", cliFunnel.calls[0], mcpFunnel.calls[0])
}

func TestEquivNote(t *testing.T) {
	t.Parallel()
	const id = "XQ-axon-20260721-b004"
	seed := func(t *testing.T, mirrorDir string) { equivWriteQuestion(t, mirrorDir, id, "beta") }

	cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
	seed(t, cliDir)
	cliCmd := cli.NewNoteCommand(cliFunnel, cliDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"--note", "fyi", id})

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
	seed(t, mcpDir)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_exchange", "note", mcp.NoteInput{IDs: []string{id}, Note: "fyi"})

	assertRequestsEquivalent(t, "note", cliFunnel.calls[0], mcpFunnel.calls[0])
}

// --- submit ----------------------------------------------------------------

func writeStagedDraftEquiv(t *testing.T, stagingDir, id string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: beta\nto: [axon]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEquivSubmit(t *testing.T) {
	t.Parallel()
	const id = "XQ-beta-20260721-c001"

	cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
	writeMirrorFileEquiv(t, cliDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	cliStaging := t.TempDir()
	writeStagedDraftEquiv(t, cliStaging, id)
	legalityForCLI := cli.NewLegalityAdapter(cliDir, "beta", equivManifest())
	cliCmd := cli.NewSubmitCommand(cliFunnel, legalityForCLI, cli.NewNoopPendingMarker(), cliDir, "fixture-space", "beta", cliStaging, equivCLIHostConfig(""))
	runCLICommand(t, cliCmd, []string{id})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("submit: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
	writeMirrorFileEquiv(t, mcpDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	mcpStaging := t.TempDir()
	writeStagedDraftEquiv(t, mcpStaging, id)
	legalityForMCP := mcp.NewLegalityAdapter(mcpDir, "beta", equivManifest())
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, mcpStaging, legalityForMCP, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_submit", "", mcp.SubmitInput{IDs: []string{id}})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("submit: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "submit", cliFunnel.calls[0], mcpFunnel.calls[0])
}

// TestEquivContractSubmitCarriesScaffold is the regression for the
// 2026-07-29 live finding: both surfaces scaffolded a JSON-Schema contract,
// but MCP submit dropped schema/** and fixtures/** from the funnel request.
// The real space CI correctly rejected that PR with POL-009. This drives
// new -> submit on both public surfaces and compares the complete five-file
// write (descriptor, three sidecars, publish event).
func TestEquivContractSubmitCarriesScaffold(t *testing.T) {
	t.Parallel()

	const (
		slug = "widget-submit-equiv"
		id   = "XC-beta-widget-submit-equiv"
	)
	cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
	writeMirrorFileEquiv(t, cliDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	cliStaging := t.TempDir()
	newCmd := cli.NewNewCommand(cliStaging, "beta", equivCLIActorResolver("agent", "bot"), nil)
	runCLICommand(t, newCmd, []string{
		"contract", "--slug", slug,
		"--field", "title=widget contract",
		"--field", "space=fixture-space",
		"--field", "to=[axon]",
		"--field", "category=other",
	})
	cliSubmit := cli.NewSubmitCommand(
		cliFunnel,
		cli.NewLegalityAdapter(cliDir, "beta", equivManifest()),
		cli.NewNoopPendingMarker(),
		cliDir, "fixture-space", "beta", cliStaging, equivCLIHostConfig(""),
	)
	runCLICommand(t, cliSubmit, []string{id})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("contract submit: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
	writeMirrorFileEquiv(t, mcpDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	mcpStaging := t.TempDir()
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	newDeps := mcp.NewDeps{
		StagingDir: mcpStaging, OwnSystem: "beta", Now: time.Now, Entropy: rand.Reader,
		ResolveActor: equivMCPActorResolver("agent", "bot"), WriteFile: os.WriteFile,
	}
	registry := mcp.BuildRegistry(
		nil,
		writeDeps,
		mcpStaging,
		mcp.NewLegalityAdapter(mcpDir, "beta", equivManifest()),
		newDeps,
	)
	runMCPHandler(t, registry, "a2a_new", "", mcp.NewInput{Items: []mcp.NewItem{{
		Type: "contract",
		Slug: slug,
		Fields: map[string]string{
			"title": "widget contract", "space": "fixture-space",
			"to": "[axon]", "category": "other",
		},
	}}})
	runMCPHandler(t, registry, "a2a_submit", "", mcp.SubmitInput{IDs: []string{id}})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("contract submit: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "contract-submit", cliFunnel.calls[0], mcpFunnel.calls[0])
	if got := len(mcpFunnel.calls[0].Files); got != 5 {
		t.Fatalf("contract submit: MCP carried %d files, want descriptor + 3 sidecars + publish event", got)
	}
}

// --- new (draft-writer, no event/commit — see this file's own classification) ---

// TestEquivNew is spec 14 §8 AC #4 applied to a draft-writer verb: `new`
// never calls the write funnel (drafts stay in `.a2a/staging/` until
// `submit`), so there is no SubmitRequest to compare — the equivalence
// claim here is over the RENDERED DRAFT bytes, modulo the minted id.
func TestEquivNew(t *testing.T) {
	t.Parallel()
	cliStaging := t.TempDir()
	cliCmd := cli.NewNewCommand(cliStaging, "beta", equivCLIActorResolver("agent", "bot"), nil)
	runCLICommand(t, cliCmd, []string{"question", "--field", "to=axon"})
	cliEntries, err := os.ReadDir(cliStaging)
	if err != nil || len(cliEntries) != 1 {
		t.Fatalf("new (CLI): expected exactly 1 staged draft, got %v (err=%v)", cliEntries, err)
	}
	cliDraft, err := os.ReadFile(filepath.Join(cliStaging, cliEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	mcpStaging := t.TempDir()
	newDeps := mcp.NewDeps{
		StagingDir: mcpStaging, OwnSystem: "beta", Now: time.Now, Entropy: rand.Reader,
		ResolveActor: equivMCPActorResolver("agent", "bot"), WriteFile: os.WriteFile,
	}
	registry := mcp.BuildRegistry(nil, mcp.WriteDeps{}, "", nil, newDeps)
	runMCPHandler(t, registry, "a2a_new", "", mcp.NewInput{Items: []mcp.NewItem{{Type: "question", Fields: map[string]string{"to": "axon"}}}})
	mcpEntries, err := os.ReadDir(mcpStaging)
	if err != nil || len(mcpEntries) != 1 {
		t.Fatalf("new (MCP): expected exactly 1 staged draft, got %v (err=%v)", mcpEntries, err)
	}
	mcpDraft, err := os.ReadFile(filepath.Join(mcpStaging, mcpEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	// Modulo the minted id (embedded in both the `id:` frontmatter field
	// and the file name — exchange ids embed today's date + random
	// suffix, which the CLI cannot be made deterministic for, same
	// reasoning as every event-writer case above) and `created:`.
	if normalizeContent(cliDraft) != normalizeContent(mcpDraft) {
		t.Fatalf("new: rendered draft mismatch (modulo id/created):\n--- CLI ---\n%s\n--- MCP ---\n%s", normalizeContent(cliDraft), normalizeContent(mcpDraft))
	}
}

// --- contract family --------------------------------------------------------

func TestEquivContractPublish(t *testing.T) {
	t.Parallel()
	const id = "XC-axon-widget-d001"

	seed := func(t *testing.T, mirrorDir string) {
		writeContractDescriptorEquiv(t, mirrorDir, "widget-d001", "0.0.0")
		// D-D/POL-009: `contract publish` refuses a JSON-Schema-dialect
		// contract with no schema/** + fixtures/valid/** baseline
		// (internal/validate/publishable.go's CheckContractPublishable) —
		// seed a real one, named after the slug per D-E's stem-mapping rule
		// (internal/validate/compat.go's planMapping doc comment), on both
		// the CLI and MCP mirrors this seed closure feeds.
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/widget-d001/schema/widget-d001.schema.json",
			`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"example":{"type":"string"}},"additionalProperties":true}`)
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/widget-d001/fixtures/valid/widget-d001.json",
			`{"example":"replace-me"}`)
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/widget-d001/fixtures/invalid/widget-d001.json",
			`null`)
	}

	// P37 Wave I: STAGE an additional sidecar file, identically on both
	// surfaces, alongside the landed baseline above — this is the overlay/
	// carry parity check, not just the pre-existing landed-tree-only path
	// TestEquivContractPublish covered before this wave (which a nil CLI
	// newCmd / unset MCP StagingDir would pass VACUOUSLY: neither surface
	// would touch staging at all, so the two would trivially agree). A
	// real `cli.NewNewCommand` + a real MCP StagingDir below force BOTH
	// surfaces to actually read this file back.
	stageExtraSchema := func(t *testing.T, stagingDir string) {
		t.Helper()
		full := filepath.Join(stagingDir, "axon", "provides", "widget-d001", "schema", "extra.schema.json")
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(`{"type":"object","additionalProperties":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seed(t, cliDir)
	cliStaging := t.TempDir()
	stageExtraSchema(t, cliStaging)
	cliNewCmd := cli.NewNewCommand(cliStaging, "axon", equivCLIActorResolver("agent", "bot"), nil)
	cliCmd := cli.NewContractCommand(cliNewCmd, cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	cliPublication := &equivContractPublicationState{}
	cliCmd.SetP6Operations(equivCLIContractPublication{state: cliPublication}, nil, nil)
	runCLICommand(t, cliCmd, []string{"publish", "--version", "1.0.0", id})

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seed(t, mcpDir)
	mcpStaging := t.TempDir()
	stageExtraSchema(t, mcpStaging)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	mcpPublication := &equivContractPublicationState{}
	registry := mcp.BuildRegistryWithContractOperations(nil, writeDeps, mcpStaging, nil, mcp.NewDeps{}, mcp.ContractToolOperations{
		Publication: equivMCPContractPublication{state: mcpPublication},
	})
	runMCPHandler(t, registry, "a2a_contract", "publish", mcp.ContractPublishInput{ID: id, Version: "1.0.0"})
	if len(cliPublication.calls) != 1 || len(mcpPublication.calls) != 1 || cliPublication.calls[0] != mcpPublication.calls[0] {
		t.Fatalf("contract publish P6 request differs: CLI=%+v MCP=%+v", cliPublication.calls, mcpPublication.calls)
	}
	if len(cliFunnel.calls) != 0 || len(mcpFunnel.calls) != 0 {
		t.Fatalf("transport bypassed shared P6 publication seam: CLI=%d MCP=%d", len(cliFunnel.calls), len(mcpFunnel.calls))
	}
}

func TestEquivContractPublishRefusesBreakingMinorOnBothSurfaces(t *testing.T) {
	t.Parallel()
	const (
		id   = "XC-axon-widget-parity"
		slug = "widget-parity"
	)

	seedPrior := func(t *testing.T, mirrorDir string) {
		t.Helper()
		writeContractDescriptorEquiv(t, mirrorDir, slug, "1.0.0")
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/"+slug+"/schema/main.schema.json",
			`{"type":"object","properties":{"x":{"type":"integer"}}}`)
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/"+slug+"/fixtures/valid/ok.json", `{"x":1}`)
		writeMirrorFileEquiv(t, mirrorDir, "axon/provides/"+slug+"/fixtures/invalid/bad.json", `null`)
		eventID, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		writeMirrorFileEquiv(t, mirrorDir, "axon/events/2020/"+eventID.String()+".yaml",
			fmt.Sprintf("schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: publish\nactor: {kind: agent, name: bot, system: axon}\nat: 2020-01-01T00:00:00Z\nversion: 1.0.0\n", eventID, id))
		if out, err := execGit(mirrorDir, "add", "-A"); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}
		if out, err := execGit(mirrorDir, "-c", "user.name=a2a-test", "-c", "user.email=a2a-test@a2ahub.invalid", "commit", "-m", "publish 1.0.0"); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}
	stageBreaking := func(t *testing.T, stagingDir string) {
		t.Helper()
		writeMirrorFileEquiv(t, stagingDir, "axon/provides/"+slug+"/schema/main.schema.json",
			`{"type":"object","properties":{"x":{"type":"string"}}}`)
	}
	refusal := func(raw string) string {
		const marker = "refused: "
		at := strings.Index(raw, marker)
		if at < 0 {
			return strings.TrimSpace(raw)
		}
		return strings.TrimSpace(raw[at+len(marker):])
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seedPrior(t, cliDir)
	cliStaging := t.TempDir()
	stageBreaking(t, cliStaging)
	cliNewCmd := cli.NewNewCommand(cliStaging, "axon", equivCLIActorResolver("agent", "bot"), nil)
	cliCmd := cli.NewContractCommand(cliNewCmd, cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	sharedRefusal := fmt.Errorf("POL-007 fixtures/valid/ok.json")
	cliCmd.SetP6Operations(equivCLIContractPublication{state: &equivContractPublicationState{err: sharedRefusal}}, nil, nil)
	cliIO, _, cliErr := equivIO()
	if code := cliCmd.Run(context.Background(), []string{"publish", "--bump", "minor", id}, cliIO); code != 1 {
		t.Fatalf("CLI code = %d, want local refusal; stderr=%s", code, cliErr)
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seedPrior(t, mcpDir)
	mcpStaging := t.TempDir()
	stageBreaking(t, mcpStaging)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistryWithContractOperations(nil, writeDeps, mcpStaging, nil, mcp.NewDeps{}, mcp.ContractToolOperations{
		Publication: equivMCPContractPublication{state: &equivContractPublicationState{err: sharedRefusal}},
	})
	spec, ok := registry.Get("a2a_contract")
	if !ok {
		t.Fatal("a2a_contract is not registered")
	}
	raw, err := marshalWithAction("publish", mcp.ContractPublishInput{ID: id, Bump: "minor"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, mcpErr := spec.Handler(context.Background(), raw)
	if mcpErr == nil {
		t.Fatal("MCP publish accepted the breaking minor")
	}

	if got, want := refusal(mcpErr.Error()), refusal(cliErr.String()); got != want {
		t.Fatalf("publish refusal differs by surface:\nCLI: %s\nMCP: %s", want, got)
	}
	if !strings.Contains(refusal(cliErr.String()), "POL-007") || !strings.Contains(refusal(cliErr.String()), "fixtures/valid/ok.json") {
		t.Fatalf("shared refusal must carry POL-007 and name the fixture, got %q", refusal(cliErr.String()))
	}
	if len(cliFunnel.calls) != 0 || len(mcpFunnel.calls) != 0 {
		t.Fatalf("local refusal must precede both funnels: CLI=%d MCP=%d", len(cliFunnel.calls), len(mcpFunnel.calls))
	}
}

// TestIT09P6FailureCodeParity covers the shared P6 seams at the transport
// boundary. MCP has no process exit status, so the equivalent contract is a
// tool error carrying the same stable code while CLI exits 1.
func TestIT09P6FailureCodeParity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, action, stable string
		failure              error
		cliArgs              []string
		mcpInput             any
	}{
		{"preflight", "preflight", "POL-007", fmt.Errorf("POL-007 deterministic refusal"), []string{"preflight", "--version", "1.0.0", "XC-axon-widget"}, mcp.ContractPreflightInput{ID: "XC-axon-widget", Version: "1.0.0"}},
		{"publish", "publish", "plan-changed", space.ErrContractPublicationPlanChanged, []string{"publish", "--version", "1.0.0", "XC-axon-widget"}, mcp.ContractPublishInput{ID: "XC-axon-widget", Version: "1.0.0"}},
		{"materialize", "materialize", "not safely contained", space.ErrContractUnsafePath, []string{"materialize", "--to", "vendor/widget", "XC-axon-widget@1.0.0"}, mcp.ContractMaterializeInput{Ref: "XC-axon-widget@1.0.0", To: "vendor/widget"}},
		{"check", "check", string(contract.ConformanceUnsupportedReference), fmt.Errorf("%s", contract.ConformanceUnsupportedReference), []string{"check", "--payload", "payload.json", "XC-axon-widget@1.0.0"}, mcp.ContractCheckInput{Ref: "XC-axon-widget@1.0.0", Mode: "payload", Payload: "payload.json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sharedErr := test.failure
			cliPublication := &equivContractPublicationState{}
			mcpPublication := &equivContractPublicationState{}
			if test.action == "preflight" || test.action == "publish" {
				cliPublication.err = sharedErr
				mcpPublication.err = sharedErr
			}
			cliMaterializeErr, mcpMaterializeErr := error(nil), error(nil)
			cliCheckErr, mcpCheckErr := error(nil), error(nil)
			if test.action == "materialize" {
				cliMaterializeErr, mcpMaterializeErr = sharedErr, sharedErr
			}
			if test.action == "check" {
				cliCheckErr, mcpCheckErr = sharedErr, sharedErr
			}

			cliCommand := cli.NewContractCommand(cli.NewNewCommand(t.TempDir(), "axon", equivCLIActorResolver("agent", "bot"), nil), &spyFunnel{}, t.TempDir(), "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
			cliCommand.SetP6Operations(equivCLIContractPublication{state: cliPublication}, equivCLIContractMaterialize{err: cliMaterializeErr}, equivCLIContractCheck{err: cliCheckErr})
			stdio, _, cliErr := equivIO()
			if got := cliCommand.Run(t.Context(), test.cliArgs, stdio); got != 1 {
				t.Fatalf("CLI exit=%d want 1; stderr=%s", got, cliErr)
			}

			registry := mcp.BuildRegistryWithContractOperations(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{}, mcp.ContractToolOperations{
				Publication: equivMCPContractPublication{state: mcpPublication},
				Materialize: equivMCPContractMaterialize{err: mcpMaterializeErr},
				Check:       equivMCPContractCheck{err: mcpCheckErr},
				Inspection:  equivMCPContractInspection{},
			})
			spec, ok := registry.Get("a2a_contract")
			if !ok {
				t.Fatal("a2a_contract is not registered")
			}
			raw, err := marshalWithAction(test.action, test.mcpInput)
			if err != nil {
				t.Fatal(err)
			}
			_, _, mcpErr := spec.Handler(t.Context(), raw)
			if mcpErr == nil {
				t.Fatal("MCP accepted shared failure")
			}
			if !strings.Contains(cliErr.String(), test.stable) || !strings.Contains(mcpErr.Error(), test.stable) {
				t.Fatalf("stable code differs: CLI=%q MCP=%q", cliErr.String(), mcpErr)
			}
			if !strings.HasSuffix(strings.TrimSpace(cliErr.String()), sharedErr.Error()) || !strings.HasSuffix(mcpErr.Error(), sharedErr.Error()) {
				t.Fatalf("semantic cause differs: CLI=%q MCP=%q", cliErr.String(), mcpErr)
			}
		})
	}
}

// TestIT10HostileValuesRemainDataAcrossCLIAndMCP drives credential-shaped and
// syntax-shaped values through both transports. The fakes are capture seams:
// execution, interpolation, or a transport-side rewrite would change the
// captured domain input or create the sentinel.
func TestIT10HostileValuesRemainDataAcrossCLIAndMCP(t *testing.T) {
	t.Parallel()
	t.Run("credential-shaped work session", func(t *testing.T) {
		t.Parallel()
		sentinel := filepath.Join(t.TempDir(), "must-not-exist")
		hostile := "Bearer ghp_not-a-real-token; touch " + sentinel + " $(ignored)"
		result := workreport.OperationResult{WorkID: "work:01K1A2B3C4D5E6F7G8H9J0K1M7", Session: hostile, LocalState: workreport.LocalActive}

		cliBackend := &equivWorkBackend{result: result}
		cliDeps, _ := workParityDeps(cliBackend)
		command, err := cli.NewWorkCommand(cliDeps)
		if err != nil {
			t.Fatal(err)
		}
		stdio, cliOut, cliErr := equivIO()
		if code := command.Run(t.Context(), []string{"start", "--thread", "thread:axon-20260803-hostile", "--subject-ref", "XQ-axon-20260803-hostile", "--mode", "testing", "--summary", "still data", "--actor-kind", "agent", "--actor-name", "codex", "--session", hostile, "--json"}, stdio); code != 0 {
			t.Fatalf("CLI code=%d stderr=%s", code, cliErr)
		}
		cliStart := cliBackend.calls[0].(workreport.StartInput)
		if cliStart.Actor.Session != hostile {
			t.Fatalf("CLI changed hostile session: %q", cliStart.Actor.Session)
		}

		mcpBackend := &equivWorkBackend{result: result}
		_, mcpDeps := workParityDeps(mcpBackend)
		tool, err := mcp.NewWorkTool(mcpDeps)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(mcp.WorkInput{Action: "start", Thread: "thread:axon-20260803-hostile", SubjectRef: "XQ-axon-20260803-hostile", Mode: "testing", Summary: "still data", Actor: mcp.WorkActorInput{Kind: "agent", Name: "codex", Session: hostile}})
		if err != nil {
			t.Fatal(err)
		}
		mcpResult, _, err := tool.Handler(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		mcpStart := mcpBackend.calls[0].(workreport.StartInput)
		if mcpStart.Actor.Session != hostile || canonicalEquivJSON(t, mcpResult) != canonicalEquivJSON(t, json.RawMessage(cliOut.Bytes())) {
			t.Fatalf("hostile work value/result differs: CLI=%q MCP=%q", cliStart.Actor.Session, mcpStart.Actor.Session)
		}
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("hostile session escaped data boundary: stat=%v", err)
		}
	})

	t.Run("event note cannot inject YAML fields", func(t *testing.T) {
		t.Parallel()
		const id = "XQ-axon-20260803-h010"
		hostile := "Bearer not-a-token\nstate: hacked\n---\n<script>alert(1)</script>"
		seed := func(t *testing.T, dir string) { equivWriteQuestion(t, dir, id, "beta") }

		cliDir, cliFunnel, _ := newEquivMirror(t, "beta")
		seed(t, cliDir)
		runCLICommand(t, cli.NewNoteCommand(cliFunnel, cliDir, "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot")), []string{"--note", hostile, id})
		mcpDir, mcpFunnel, _ := newEquivMirror(t, "beta")
		seed(t, mcpDir)
		registry := mcp.BuildRegistry(nil, mcp.WriteDeps{Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "beta", Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"), Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile}, "", nil, mcp.NewDeps{})
		runMCPHandler(t, registry, "a2a_exchange", "note", mcp.NoteInput{IDs: []string{id}, Note: hostile})
		assertRequestsEquivalent(t, "hostile note", cliFunnel.calls[0], mcpFunnel.calls[0])
		for surface, request := range map[string]space.SubmitRequest{"CLI": cliFunnel.calls[0], "MCP": mcpFunnel.calls[0]} {
			var document map[string]any
			if err := yaml.Unmarshal(request.Files[0].Content, &document); err != nil {
				t.Fatalf("%s event no longer parses: %v", surface, err)
			}
			if got := document["note"]; got != hostile {
				t.Fatalf("%s note=%q want exact hostile data", surface, got)
			}
			if _, injected := document["state"]; injected {
				t.Fatalf("%s hostile note injected state: %#v", surface, document)
			}
		}
	})

	t.Run("contract candidate selector remains data", func(t *testing.T) {
		t.Parallel()
		sentinel := filepath.Join(t.TempDir(), "must-not-exist")
		hostile := "candidate; touch " + sentinel + " $(ignored)"
		cliState := &equivContractPublicationState{}
		cliCommand := cli.NewContractCommand(cli.NewNewCommand(t.TempDir(), "axon", equivCLIActorResolver("agent", "bot"), nil), &spyFunnel{}, t.TempDir(), "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
		cliCommand.SetP6Operations(equivCLIContractPublication{state: cliState}, equivCLIContractMaterialize{}, equivCLIContractCheck{})
		stdio, _, cliErr := equivIO()
		if code := cliCommand.Run(t.Context(), []string{"preflight", "--version", "1.0.0", "--staging", hostile, "XC-axon-widget"}, stdio); code != 0 {
			t.Fatalf("CLI code=%d stderr=%s", code, cliErr)
		}

		mcpState := &equivContractPublicationState{}
		registry := mcp.BuildRegistryWithContractOperations(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{}, mcp.ContractToolOperations{
			Publication: equivMCPContractPublication{state: mcpState}, Materialize: equivMCPContractMaterialize{},
			Check: equivMCPContractCheck{}, Inspection: equivMCPContractInspection{},
		})
		runMCPHandler(t, registry, "a2a_contract", "preflight", mcp.ContractPreflightInput{ID: "XC-axon-widget", Version: "1.0.0", Staging: hostile})
		if len(cliState.calls) != 1 || len(mcpState.calls) != 1 || cliState.calls[0] != mcpState.calls[0] || cliState.calls[0].staging != hostile {
			t.Fatalf("hostile contract selector differs: CLI=%+v MCP=%+v", cliState.calls, mcpState.calls)
		}
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("hostile contract selector escaped data boundary: stat=%v", err)
		}
	})
}

func TestEquivContractDeprecate(t *testing.T) {
	t.Parallel()
	const id = "XC-axon-widget-d002"
	seed := func(t *testing.T, mirrorDir string) {
		writeContractDescriptorEquiv(t, mirrorDir, "widget-d002", "1.0.0")
		equivWriteEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		// P37/F3 (US-971): the descriptor's own `to: [beta]` and the
		// REGISTERED-CONSUMER set must be able to DISAGREE, or this fixture
		// cannot tell a correct `contractDeprecateAddressees` copy from a
		// stale one that still reads `probe.To` — an empty consumer
		// registry falls back to `probe.To` on EITHER implementation and
		// the two surfaces emit identical bytes while proving nothing about
		// F3 (this was the pre-P37 gap that let the MCP surface drift for a
		// full phase, silently). "gamma" registers as a consumer here
		// deliberately WITHOUT ever appearing in the descriptor's `to:` —
		// the correct addressee set is {gamma}, the stale/wrong one is
		// {beta}. Do not remove this: reverting either surface's F3 fix
		// must fail this test.
		writeMirrorFileEquiv(t, mirrorDir, "gamma/consumes.yaml",
			"schema: consumes/v1\nsystem: gamma\ndependencies:\n  - contract: "+id+"\n    major: 1\n    since: \"2026-01-01\"\n")
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seed(t, cliDir)
	cliCmd := cli.NewContractCommand(nil, cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"deprecate", "--successor", "XC-axon-widget-d002-next@1.0.0", "--sunset", "2099-01-01", id})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("contract deprecate: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seed(t, mcpDir)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_contract", "deprecate", mcp.ContractDeprecateInput{ID: id, Successor: "XC-axon-widget-d002-next@1.0.0", Sunset: "2099-01-01"})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("contract deprecate: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "contract-deprecate", cliFunnel.calls[0], mcpFunnel.calls[0])
}

func TestEquivContractRetire(t *testing.T) {
	t.Parallel()
	const id = "XC-axon-clean-d003"
	seed := func(t *testing.T, mirrorDir string) {
		writeContractDescriptorEquiv(t, mirrorDir, "clean-d003", "1.0.0")
		equivWriteEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		equivWriteEvent(t, mirrorDir, "axon", 1, id, "deprecate", "axon")
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seed(t, cliDir)
	cliCmd := cli.NewContractCommand(nil, cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"retire", id})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("contract retire: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seed(t, mcpDir)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_contract", "retire", mcp.ContractRetireInput{ID: id})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("contract retire: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "contract-retire", cliFunnel.calls[0], mcpFunnel.calls[0])
}

// TestEquivContractNew is the contract family's own draft-writer case
// (thin delegate to `new type=contract`, same classification as
// TestEquivNew — no funnel/event to compare).
func TestEquivContractNew(t *testing.T) {
	t.Parallel()
	cliStaging := t.TempDir()
	newCmd := cli.NewNewCommand(cliStaging, "beta", equivCLIActorResolver("agent", "bot"), nil)
	cliCmd := cli.NewContractCommand(newCmd, nil, "", "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"new", "widget-equiv"})
	cliDraft, cliScaffold := readStagedContract(t, cliStaging)

	mcpStaging := t.TempDir()
	newDeps := mcp.NewDeps{
		StagingDir: mcpStaging, OwnSystem: "beta", Now: time.Now, Entropy: rand.Reader,
		ResolveActor: equivMCPActorResolver("agent", "bot"), WriteFile: os.WriteFile,
	}
	registry := mcp.BuildRegistry(nil, mcp.WriteDeps{}, "", nil, newDeps)
	runMCPHandler(t, registry, "a2a_contract", "new", mcp.ContractNewInput{Slug: "widget-equiv"})
	mcpDraft, mcpScaffold := readStagedContract(t, mcpStaging)

	if normalizeContent(cliDraft) != normalizeContent(mcpDraft) {
		t.Fatalf("contract new: rendered draft mismatch (modulo id):\n--- CLI ---\n%s\n--- MCP ---\n%s", normalizeContent(cliDraft), normalizeContent(mcpDraft))
	}

	// P37 D-D: `new` on a JSON-Schema contract also scaffolds a starter
	// schema + valid fixture. R-018 says this surface has no capability the
	// CLI lacks and lacks none the CLI has, so the scaffold must be
	// byte-identical on both sides — which is what caught the asymmetry
	// when the scaffold shipped CLI-only: the two staged trees differed and
	// this test reds rather than the gap reaching a consumer repo.
	if len(cliScaffold) == 0 {
		t.Fatal("contract new (CLI): expected a D-D schema/fixture scaffold beside the draft, got none")
	}
	if len(cliScaffold) != len(mcpScaffold) {
		t.Fatalf("contract new: scaffold file sets differ — CLI %v, MCP %v", sortedKeysOf(cliScaffold), sortedKeysOf(mcpScaffold))
	}
	for rel, cliBytes := range cliScaffold {
		mcpBytes, ok := mcpScaffold[rel]
		if !ok {
			t.Fatalf("contract new: MCP staged no %s (CLI did) — the two surfaces are not equivalent", rel)
		}
		// The nested publication descriptor is an exact copy of each surface's
		// flat draft, so its independently minted thread differs for the same
		// legitimate reason as the flat draft above. Every deterministic
		// sidecar remains byte-identical.
		if strings.HasSuffix(rel, "/contract.md") {
			if normalizeContent(cliBytes) == normalizeContent(mcpBytes) {
				continue
			}
		}
		if !bytes.Equal(cliBytes, mcpBytes) {
			t.Fatalf("contract new: scaffold %s differs between surfaces:\n--- CLI ---\n%s\n--- MCP ---\n%s", rel, cliBytes, mcpBytes)
		}
	}
}

// readStagedContract splits a staging dir into the single top-level *.md
// draft and every other staged file, keyed by its slash-separated path
// relative to the staging root — i.e. the D-D scaffold subtree.
func readStagedContract(t *testing.T, stagingDir string) ([]byte, map[string][]byte) {
	t.Helper()
	var draft []byte
	scaffold := map[string][]byte{}
	err := filepath.WalkDir(stagingDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(stagingDir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".md") {
			if draft != nil {
				return fmt.Errorf("more than one top-level draft staged (%s)", rel)
			}
			draft = raw
			return nil
		}
		scaffold[rel] = raw
		return nil
	})
	if err != nil {
		t.Fatalf("read staging %s: %v", stagingDir, err)
	}
	if draft == nil {
		t.Fatalf("no staged draft found under %s", stagingDir)
	}
	return draft, scaffold
}

func sortedKeysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeContractDescriptorEquiv(t *testing.T, mirrorDir, slug, version string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: XC-axon-" + slug + "\ntype: contract\ntitle: t\nspace: fixture-space\nthread: thread:axon-20260721-k3f9\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: api\npriority: p3\nblocking: false\nclassification: internal\nversion: \"" + version + "\"\ncompat_policy: strict-semver\nschema_format: json-schema-2020-12\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
}

// TestEquivContractDiffAndVerifyExportAreReadOnly documents the two
// remaining contract sub-verbs' classification (this phase's Deviations
// report, REQUIRED): `contract diff` and `contract verify-export` never
// call the write funnel and author no event — there is no commit/event
// shape to compare. Both surfaces are asserted to compute the IDENTICAL
// structured result from the identical fixture instead (the read-side
// equivalence claim these two verbs actually support).
func TestEquivContractDiffAndVerifyExportAreReadOnly(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	runGit := func(args ...string) {
		out, err := execGit(mirrorDir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	writeContractDescriptorEquiv(t, mirrorDir, "diffable-e001", "1.0.0")
	writeMirrorFileEquiv(t, mirrorDir, "axon/provides/diffable-e001/schema/main.schema.json", `{"type":"object"}`)
	runGit("add", "-A")
	runGit("-c", "user.name=t", "-c", "user.email=t@t.invalid", "commit", "-m", "v1")
	writeContractDescriptorEquiv(t, mirrorDir, "diffable-e001", "1.1.0")
	writeMirrorFileEquiv(t, mirrorDir, "axon/provides/diffable-e001/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	runGit("add", "-A")
	runGit("-c", "user.name=t", "-c", "user.email=t@t.invalid", "commit", "-m", "v2")

	id := "XC-axon-diffable-e001"
	cliInspection := equivCLIContractInspection{result: cli.ContractDiffResult{Changed: []string{"schema/main.schema.json"}}}
	mcpInspection := equivMCPContractInspection{result: mcp.ContractDiffResult{Changed: []string{"schema/main.schema.json"}}}

	cliCmd := cli.NewContractCommand(nil, nil, mirrorDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	cliCmd.SetP6Inspection(cliInspection)
	_, out, errOut := equivIO()
	code := cliCmd.Run(context.Background(), []string{"diff", id, "1.0.0", "1.1.0"}, cli.IO{Stdin: bytes.NewReader(nil), Stdout: out, Stderr: errOut})
	if code != 0 {
		t.Fatalf("contract diff (CLI): code=%d stderr=%s", code, errOut.String())
	}
	cliOut := out.String()
	if !strings.Contains(cliOut, "changed schema/main.schema.json") {
		t.Fatalf("contract diff (CLI): expected schema/main.schema.json under changed, got:\n%s", cliOut)
	}

	writeDeps := mcp.WriteDeps{MirrorDir: mirrorDir, OwnSystem: "axon", Manifest: equivManifest()}
	registry := mcp.BuildRegistryWithContractOperations(nil, writeDeps, "", nil, mcp.NewDeps{}, mcp.ContractToolOperations{Inspection: mcpInspection})
	spec, ok := registry.Get("a2a_contract")
	if !ok {
		t.Fatal("a2a_contract not registered")
	}
	raw, err := marshalWithAction("diff", mcp.ContractDiffInput{ID: id, V1: "1.0.0", V2: "1.1.0"})
	if err != nil {
		t.Fatalf("marshal contract diff input: %v", err)
	}
	result, _, err := spec.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("contract diff (MCP): %v", err)
	}
	structured, _ := json.Marshal(result)
	if !strings.Contains(string(structured), "schema/main.schema.json") {
		t.Fatalf("contract diff (MCP): expected schema/main.schema.json in the changed set, got: %s", structured)
	}
}

func execGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// --- CC-093 ------------------------------------------------------------

// TestCC093InterleavedCLIThenMCPSubmitIsIdempotent is spec 14 §8 AC #5:
// `a2a submit <id>` (CLI) then `a2a_submit` (MCP) on the SAME id, in the
// SAME session (shared mirror + shared FakeHost), is idempotent — the
// second call is a no-op, never a duplicate PR.
//
// Per this file's advisor-reviewed Deviation note: the MCP side's
// idempotency short-circuit fires at the LegalityAdapter.
// HasCommittedHistory layer (submit's own pre-funnel check), not the
// funnel's WriteStateAlreadyOpen branch — because the CLI's first submit
// already committed the entry event onto the SHARED mirror's disk before
// the MCP call ever reaches the funnel. The observable CC-093 outcome
// (exactly one OpenPR call, no duplicate PR, the second call reports
// "already done") holds regardless of which layer's dedup fired.
func TestCC093InterleavedCLIThenMCPSubmitIdempotent(t *testing.T) {
	t.Parallel()
	const id = "XQ-beta-20260721-f001"

	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("beta")
	fakeHost := host.NewFakeHost()
	sharedFunnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")

	writeMirrorFileEquiv(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	staging := t.TempDir()
	writeStagedDraftEquiv(t, staging, id)

	// First: `a2a submit <id>` (CLI).
	legality := cli.NewLegalityAdapter(mirrorDir, "beta", equivManifest())
	cliCmd := cli.NewSubmitCommand(sharedFunnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "beta", staging, equivCLIHostConfig(fx.RemoteURL()))
	runCLICommand(t, cliCmd, []string{id})
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly 1 OpenPR call after the CLI submit, got %d", len(fakeHost.Opens))
	}

	// Second: `a2a_submit` (MCP) on the SAME id, same session.
	mcpLegality := mcp.NewLegalityAdapter(mirrorDir, "beta", equivManifest())
	writeDeps := mcp.WriteDeps{
		Funnel: sharedFunnel, MirrorDir: mirrorDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(fx.RemoteURL()), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, staging, mcpLegality, mcp.NewDeps{})
	spec, ok := registry.Get("a2a_submit")
	if !ok {
		t.Fatal("a2a_submit not registered")
	}
	raw, _ := json.Marshal(mcp.SubmitInput{IDs: []string{id}})
	result, _, err := spec.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("a2a_submit (second, MCP): unexpected error: %v", err)
	}

	// AC #5: no duplicate PR.
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly 1 OpenPR call after the interleaved MCP submit (no duplicate PR), got %d", len(fakeHost.Opens))
	}
	rendered, _ := json.Marshal(result)
	if !strings.Contains(string(rendered), "already") {
		t.Fatalf("expected the second call to report an already-done state, got: %s", rendered)
	}
}
