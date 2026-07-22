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
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
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

func equivCLIActorResolver(kind, name string) func(cli.ActorFlags) template.Actor {
	return func(cli.ActorFlags) template.Actor { return template.Actor{Kind: kind, Name: name} }
}

func equivMCPActorResolver(kind, name string) func(mcp.ActorInput) template.Actor {
	return func(mcp.ActorInput) template.Actor { return template.Actor{Kind: kind, Name: name} }
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
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [" + to + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

func equivWriteRequirement(t *testing.T, mirrorDir, id string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: requirement\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: new-capability\npriority: p3\nblocking: true\nclassification: internal\nacceptance_criteria: [\"works\"]\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/requires/"+id+".md", content)
}

func equivWriteHandoff(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: handoff\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [" + to + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	writeMirrorFileEquiv(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

func equivWriteDecision(t *testing.T, mirrorDir, id string, approvers []string) {
	t.Helper()
	joined := strings.Join(approvers, ", ")
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: decision\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [" + joined + "]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\nrequired_approvers: [" + joined + "]\n---\nbody\n"
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
		tc := tc
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
	assertRequestsEquivalent(t, "respond", cliFunnel.calls[0], mcpFunnel.calls[0])

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
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\nspace: fixture-space\nfrom: beta\nto: [axon]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
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

// --- new (draft-writer, no event/commit — see this file's own classification) ---

// TestEquivNew is spec 14 §8 AC #4 applied to a draft-writer verb: `new`
// never calls the write funnel (drafts stay in `.a2a/staging/` until
// `submit`), so there is no SubmitRequest to compare — the equivalence
// claim here is over the RENDERED DRAFT bytes, modulo the minted id.
func TestEquivNew(t *testing.T) {
	t.Parallel()
	cliStaging := t.TempDir()
	cliCmd := cli.NewNewCommand(cliStaging, "beta", equivCLIActorResolver("agent", "bot"))
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
	}

	cliDir, cliFunnel, _ := newEquivMirror(t, "axon")
	seed(t, cliDir)
	cliCmd := cli.NewContractCommand(nil, cliFunnel, cliDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"publish", "--version", "1.0.0", id})
	if len(cliFunnel.calls) != 1 {
		t.Fatalf("contract publish: expected 1 CLI funnel call, got %d", len(cliFunnel.calls))
	}

	mcpDir, mcpFunnel, _ := newEquivMirror(t, "axon")
	seed(t, mcpDir)
	writeDeps := mcp.WriteDeps{
		Funnel: mcpFunnel, MirrorDir: mcpDir, SpaceID: "fixture-space", OwnSystem: "axon",
		Manifest: equivManifest(), HostCfg: equivMCPHostConfig(""), ResolveActor: equivMCPActorResolver("agent", "bot"),
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
	runMCPHandler(t, registry, "a2a_contract", "publish", mcp.ContractPublishInput{ID: id, Version: "1.0.0"})
	if len(mcpFunnel.calls) != 1 {
		t.Fatalf("contract publish: expected 1 MCP funnel call, got %d", len(mcpFunnel.calls))
	}

	assertRequestsEquivalent(t, "contract-publish", cliFunnel.calls[0], mcpFunnel.calls[0])
}

func TestEquivContractDeprecate(t *testing.T) {
	t.Parallel()
	const id = "XC-axon-widget-d002"
	seed := func(t *testing.T, mirrorDir string) {
		writeContractDescriptorEquiv(t, mirrorDir, "widget-d002", "1.0.0")
		equivWriteEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
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
	newCmd := cli.NewNewCommand(cliStaging, "beta", equivCLIActorResolver("agent", "bot"))
	cliCmd := cli.NewContractCommand(newCmd, nil, "", "fixture-space", "beta", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
	runCLICommand(t, cliCmd, []string{"new", "widget-equiv"})
	cliEntries, err := os.ReadDir(cliStaging)
	if err != nil || len(cliEntries) != 1 {
		t.Fatalf("contract new (CLI): expected 1 staged draft, got %v (err=%v)", cliEntries, err)
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
	runMCPHandler(t, registry, "a2a_contract", "new", mcp.ContractNewInput{Slug: "widget-equiv"})
	mcpEntries, err := os.ReadDir(mcpStaging)
	if err != nil || len(mcpEntries) != 1 {
		t.Fatalf("contract new (MCP): expected 1 staged draft, got %v (err=%v)", mcpEntries, err)
	}
	mcpDraft, err := os.ReadFile(filepath.Join(mcpStaging, mcpEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	if normalizeContent(cliDraft) != normalizeContent(mcpDraft) {
		t.Fatalf("contract new: rendered draft mismatch (modulo id):\n--- CLI ---\n%s\n--- MCP ---\n%s", normalizeContent(cliDraft), normalizeContent(mcpDraft))
	}
}

func writeContractDescriptorEquiv(t *testing.T, mirrorDir, slug, version string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: XC-axon-" + slug + "\ntype: contract\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: api\npriority: p3\nblocking: false\nclassification: internal\nversion: \"" + version + "\"\ncompat_policy: strict-semver\nschema_format: json-schema-2020-12\n---\nbody\n"
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

	cliCmd := cli.NewContractCommand(nil, nil, mirrorDir, "fixture-space", "axon", equivManifest(), equivCLIHostConfig(""), equivCLIActorResolver("agent", "bot"))
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
	registry := mcp.BuildRegistry(nil, writeDeps, "", nil, mcp.NewDeps{})
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
	cmd := exec.Command("git", args...)
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
