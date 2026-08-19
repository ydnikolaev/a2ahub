package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/operation"
	"gopkg.in/yaml.v3"
)

func writeContractDescriptor(t *testing.T, mirrorDir, slug, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/schema/main.schema.json", `{"type":"object","additionalProperties":true}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/fixtures/valid/ok.json", `{}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/fixtures/invalid/bad.json", `null`)
}

func contractTestDeps(mirrorDir string, funnel Funnel) ContractDeps {
	write := testWriteDeps(mirrorDir, funnel)
	write.OwnSystem = "axon"
	return ContractDeps{WriteDeps: write}
}

func appendVersionToLatestEvent(t *testing.T, mirrorDir, system, version string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mirrorDir, system, "events", "*", "*.yaml"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("appendVersionToLatestEvent: no event files found: %v", err)
	}
	path := matches[len(matches)-1]
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("appendVersionToLatestEvent: %v", err)
	}
	raw = append(raw, []byte("version: \""+version+"\"\n")...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("appendVersionToLatestEvent: %v", err)
	}
}

func TestContractDeprecateAuthorsAnnouncement(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "dep-a", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-dep-a", "publish", "axon")

	fake := &fakeFunnel{}
	handler := newContractDeprecateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDeprecateInput{ID: "XC-axon-dep-a", Successor: "XC-axon-dep-b@1.0.0", Sunset: "2099-01-01"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("deprecate failed: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 3 {
		t.Fatalf("expected 3 files (deprecate event + announcement draft + its publish event), got %+v", fake.calls)
	}
	for _, file := range fake.calls[0].Files {
		content := string(file.Content)
		switch {
		case strings.Contains(content, "transition: deprecate"):
			if strings.Contains(content, "state:") {
				t.Fatalf("whole-contract deprecate is receipt-N/A and must omit state:\n%s", content)
			}
		case strings.Contains(content, "transition: publish"):
			if !strings.Contains(content, "state: published") {
				t.Fatalf("derived announcement publish omitted its evaluator receipt:\n%s", content)
			}
		}
	}
}

func TestContractVersionedDeprecateAuthorsReceipt(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "dep-versioned", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-dep-versioned", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeFunnel{}
	handler := newContractDeprecateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDeprecateInput{
		ID: "XC-axon-dep-versioned", Version: "1.0.0",
		Successor: "XC-axon-dep-next@1.0.0", Sunset: "2099-01-01",
	})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("versioned deprecate: %v", err)
	}
	var saw bool
	for _, file := range fake.calls[0].Files {
		content := string(file.Content)
		if strings.Contains(content, "transition: deprecate") {
			saw = true
			if !strings.Contains(content, "state: deprecated") {
				t.Fatalf("versioned deprecate omitted its evaluator receipt:\n%s", content)
			}
		}
	}
	if !saw {
		t.Fatal("versioned deprecate event was not authored")
	}
}

func TestContractRetireCleanAckSucceedsUngated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "clean", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-clean", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-clean", "deprecate", "axon")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-clean"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an ungated retire (no registered consumers), got %+v", fake.calls)
	}
	if strings.Contains(string(fake.calls[0].Files[0].Content), "state:") {
		t.Fatalf("whole-contract retire is receipt-N/A and must omit state:\n%s", fake.calls[0].Files[0].Content)
	}
}

func TestContractRetireUnackedBlocked(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies:\n  - contract: XC-axon-gated\n    major: 1\n    since: \"2026-01-01\"\n")
	writeMirrorFile(t, mirrorDir, "axon/exchanges/XA-axon-20260101-a1a1.md",
		"---\nschema: envelope/v1\nid: XA-axon-20260101-a1a1\ntype: announcement\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: deprecation\npriority: p2\nblocking: false\nack_requested: true\ndeprecates: XC-axon-gated@1.0.0\nvalid_until: 2099-01-01\nclassification: internal\n---\nbody\n")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-gated"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal (un-acked registered consumer, POL-006)")
	}
	if !strings.Contains(err.Error(), "POL-006") {
		t.Fatalf("expected POL-006 in the refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fake.calls))
	}
}

// TestContractDeprecateAddressesConsumesOnlyRegistrant is F3 (AC-971.1,
// AC-971.2) on the MCP surface: a system that registered as a consumer
// ONLY via `consumes.yaml` — and never appears in the descriptor's own
// `to: [beta]` — must still be addressed by the deprecation announcement.
// Before P37's MCP fix, `newContractDeprecateHandler` addressed
// `probe.To` directly and this consumer was silently never told.
func TestContractDeprecateAddressesConsumesOnlyRegistrant(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "dep-f3", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-dep-f3", "publish", "axon")
	// "gamma" registers as a consumer via consumes.yaml — NOT in the
	// descriptor's `to: [beta]`.
	writeMirrorFile(t, mirrorDir, "gamma/consumes.yaml",
		"schema: consumes/v1\nsystem: gamma\ndependencies:\n  - contract: XC-axon-dep-f3\n    major: 1\n    since: \"2026-01-01\"\n")

	fake := &fakeFunnel{}
	handler := newContractDeprecateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDeprecateInput{ID: "XC-axon-dep-f3", Successor: "XC-axon-dep-f3-next@1.0.0", Sunset: "2099-01-01"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("deprecate failed: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 3 {
		t.Fatalf("expected 3 files (deprecate event + announcement draft + its publish event), got %+v", fake.calls)
	}
	// Assert on the exact `to:` block, not a bare substring match — "gamma"
	// or "beta" appearing anywhere else in the rendered frontmatter would
	// otherwise satisfy (or spuriously fail) a looser check.
	announcement := string(fake.calls[0].Files[1].Content)
	if !strings.Contains(announcement, "to:\n    - gamma") {
		t.Fatalf("expected the announcement's `to:` block to address the registered consumer \"gamma\", got:\n%s", announcement)
	}
	if strings.Contains(announcement, "- beta") {
		t.Fatalf("expected the announcement NOT to fall back to the descriptor's `to: [beta]` once a registered consumer exists, got:\n%s", announcement)
	}
}

// TestContractDeprecateRefusesOmittedVersionWithMultiplePublished is F4
// (AC-972.1) on the MCP surface: `deprecate` must REFUSE an omitted
// version once more than one version has been published, listing what
// is published, rather than silently defaulting to the descriptor's
// CURRENT version.
func TestContractDeprecateRefusesOmittedVersionWithMultiplePublished(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "dep-f4", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-dep-f4", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-dep-f4", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")

	fake := &fakeFunnel{}
	handler := newContractDeprecateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDeprecateInput{ID: "XC-axon-dep-f4", Successor: "XC-axon-dep-f4-next@1.0.0", Sunset: "2099-01-01"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal: two published versions, no --version given")
	}
	if !strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "2.0.0") {
		t.Fatalf("expected the refusal to list both published versions, got: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fake.calls))
	}
}

// TestContractRetireRefusesOmittedVersionWithMultiplePublished is F4
// (AC-972.1) on the MCP surface's `retire` verb — same guarantee as
// deprecate's, checked independently since the two handlers each carry
// their own copy (ADR-001).
func TestContractRetireRefusesOmittedVersionWithMultiplePublished(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "ret-f4", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-ret-f4", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-ret-f4", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-ret-f4", "deprecate", "axon")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-ret-f4"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal: two published versions, no version given")
	}
	if !strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "2.0.0") {
		t.Fatalf("expected the refusal to list both published versions, got: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fake.calls))
	}
}

// TestContractRetireGuardRefusesWhileAnotherVersionPublished is
// agent-ops-2026-07 spec 02 (P2)'s MCP twin of the same guard in
// internal/cli: `a2a_contract_retire` must refuse identically — a
// capability that refuses on one surface only is exactly the asymmetry
// P43 exists to close. Publish 1.0.0, publish 2.0.0, deprecate 1.0.0
// (subject-scoped fold: Published -> Published -> Deprecated), then
// retire 1.0.0 is legal at the fold table but would leave the contract
// SUBJECT Retired while 2.0.0 is still published and consumed.
// TestContractRetireSucceedsWhileAnotherVersionPublished is P4's AC-7/AC-9
// (04-per-version-lifecycle.plan.md), MCP parity with internal/cli's own
// TestContractRetireSucceedsWhileAnotherVersionPublished — see that
// test's doc comment for the full rationale. Re-pinned from "refused
// (POL-011)" to "succeeds": POL-011 is deleted this wave, superseded by
// internal/fold's own per-version legality (fold.CheckCandidate).
func TestContractRetireSucceedsWhileAnotherVersionPublished(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "guarded", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-guarded", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-guarded", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-guarded", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-guarded", Version: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("expected retire to succeed (P4: per-version retire, 2.0.0 stays published): %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "state: retired") {
		t.Fatalf("versioned retire omitted its evaluator receipt:\n%s", fake.calls[0].Files[0].Content)
	}
}

// TestContractRetireNotBlockedByConsumerOnAnotherMajor is epic AC-9
// (04-per-version-lifecycle.md §4, Edge 1), MCP parity with internal/cli's
// own test of the same name: a consumer registered on a DIFFERENT major
// must not block retiring this line forever. "beta" registers via
// consumes.yaml at major 2 while 1.0 is the line being retired (2.0 stays
// published) — before the fix, the retire precondition's consumer scan was
// CONTRACT-scoped, so beta's major-2 registration would block this retire
// forever even though beta never depends on the 1.x line, and no
// deprecation announcement even exists to ack against. TEETH: dropping the
// major filter (calling cache.FindRegisteredConsumers unscoped from the
// retire path) reds this test with POL-006 — verified by reverting and
// re-running (see this wave's own report).
func TestContractRetireNotBlockedByConsumerOnAnotherMajor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "majorgap", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-majorgap", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-majorgap", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-majorgap", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	// "beta" depends on the 2.x line only — never the 1.x line being retired.
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml",
		"schema: consumes/v1\nsystem: beta\ndependencies:\n  - contract: XC-axon-majorgap\n    major: 2\n    since: \"2026-01-01\"\n")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-majorgap", Version: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("expected retire to succeed (AC-9: a major-2 consumer must not block retiring the 1.x line): %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an ungated retire (no consumer registered on the major being retired), got %+v", fake.calls)
	}
}

// TestContractRetireGuardAllowsSolePublishedVersion is spec 02's AC-2.2
// regression guard on the MCP surface: retiring the ONLY published
// version must keep working exactly as before this phase.
func TestContractRetireGuardAllowsSolePublishedVersion(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "sole", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-sole", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-sole", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-sole"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed (sole published version, the guard must not block it): %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
}

func TestContractNewDelegatesToNewDraft(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newContractNewHandler(testNewDeps(staging))
	args, _ := json.Marshal(ContractNewInput{Slug: "widget"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("contract new failed: %v", err)
	}
	drafts, ok := result.([]newDraftResult)
	// P37 D-D/P43: a JSON-Schema contract is drafted AND scaffolded — the flat
	// submit draft plus a complete candidate descriptor, starter schema and
	// valid/invalid fixtures, so the first version is publishable at the current
	// declared-v2 floor and §5.4b has a baseline the moment it exists.
	// Every entry reports the same contract id; the paths differ.
	if !ok || len(drafts) != 5 {
		t.Fatalf("expected the submit draft plus a complete four-file publication candidate, got %#v", result)
	}
	for _, d := range drafts {
		if !strings.HasPrefix(d.ID, "XC-") {
			t.Fatalf("every entry must report the contract's own id, got %#v", d)
		}
	}
	if !strings.HasSuffix(drafts[0].Path, ".md") {
		t.Fatalf("the draft itself must come first, got %q", drafts[0].Path)
	}
	if !strings.HasSuffix(drafts[1].Path, "/provides/widget/contract.md") ||
		!strings.HasSuffix(drafts[2].Path, "/schema/widget.schema.json") ||
		!strings.HasSuffix(drafts[3].Path, "/fixtures/valid/widget.json") ||
		!strings.HasSuffix(drafts[4].Path, "/fixtures/invalid/widget.json") {
		t.Fatalf("candidate paths must include its descriptor and follow D-E's stem mapping, got %#v", drafts)
	}
}

func TestContractNewMissingSlug(t *testing.T) {
	t.Parallel()
	handler := newContractNewHandler(testNewDeps(t.TempDir()))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for a missing slug")
	}
}

// --- contract activate (Wave 26C — P5 AC1's MCP mirror of internal/cli's
// runActivate) ---------------------------------------------------------

// contractTestDepsAtFloor is contractTestDeps plus an explicit
// min_binary_version — the field contractActivateEventSchema reads to
// decide event/v1 vs event/v2.
func contractTestDepsAtFloor(mirrorDir string, funnel Funnel, floor string) ContractDeps {
	deps := contractTestDeps(mirrorDir, funnel)
	deps.Manifest.MinBinaryVersion = floor
	return deps
}

// writeContractDescriptorWithXOperational is writeContractDescriptor plus a
// caller-chosen owning system and a raw x_operational block, on
// envelope/v2 (`x_operational` is a v2-only field) — xOperationalRaw is
// inserted as-is, so a caller may pass any number of declared items or
// none at all. Mirrors internal/cli's own helper of the same name.
func writeContractDescriptorWithXOperational(t *testing.T, mirrorDir, system, slug, version, xOperationalRaw string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + system + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + system + "\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		xOperationalRaw +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/contract.md", content)
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/schema/main.schema.json", `{"type":"object","additionalProperties":true}`)
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/fixtures/valid/ok.json", `{}`)
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/fixtures/invalid/bad.json", `null`)
}

// contractActivationProbe/contractActivateEventProbe decode the wire shape
// these tests assert on — DECODED values, never strings.Contains over
// rendered bytes. Mirrors internal/cli's own probes of the same names.
type contractActivationProbe struct {
	Version   string   `yaml:"version"`
	Status    string   `yaml:"status"`
	Satisfies []string `yaml:"satisfies"`
	Note      string   `yaml:"note"`
}

type contractActivateEventProbe struct {
	Schema     string                   `yaml:"schema"`
	Transition string                   `yaml:"transition"`
	Subject    string                   `yaml:"subject"`
	Space      string                   `yaml:"space"`
	Note       string                   `yaml:"note"`
	Activation *contractActivationProbe `yaml:"activation"`
}

func decodeContractActivateEvent(t *testing.T, content []byte) contractActivateEventProbe {
	t.Helper()
	var probe contractActivateEventProbe
	if err := yaml.Unmarshal(content, &probe); err != nil {
		t.Fatalf("decode activate event: %v", err)
	}
	return probe
}

// seedActivatablePublished writes a v2 descriptor with the given
// x_operational block, then a committed `publish` event carrying that same
// version — the shared fixture every activate sub-test below builds on.
func seedActivatablePublished(t *testing.T, mirrorDir, system, slug, version, xOperationalRaw string) {
	t.Helper()
	writeContractDescriptorWithXOperational(t, mirrorDir, system, slug, version, xOperationalRaw)
	writeLifecycleEvent(t, mirrorDir, system, 0, "XC-"+system+"-"+slug, "publish", system)
	appendVersionToLatestEvent(t, mirrorDir, system, version)
}

func TestContractActivateAuthorsEventV2ForDeclaredItem(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "axon", "act-a", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{
		ID: "XC-axon-act-a", Version: "1.0.0", Satisfies: []string{"endpoint"}, Note: "live now",
	})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("activate failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	files := fake.calls[0].Files
	if len(files) != 1 {
		t.Fatalf("expected exactly one event file, got %+v", files)
	}
	ev := decodeContractActivateEvent(t, files[0].Content)
	if ev.Schema != "event/v2" || ev.Transition != "activate" || ev.Subject != "XC-axon-act-a" {
		t.Fatalf("event = %+v, want schema event/v2, transition activate, subject XC-axon-act-a", ev)
	}
	if ev.Space != "fixture-space" {
		t.Fatalf("event.Space = %q, want the descriptor's own space", ev.Space)
	}
	if ev.Note != "live now" {
		t.Fatalf("event.Note = %q, want the note text", ev.Note)
	}
	if ev.Activation == nil {
		t.Fatal("expected an `activation` block, got none")
	}
	if ev.Activation.Version != "1.0.0" || ev.Activation.Status != "live" {
		t.Fatalf("activation = %+v, want version 1.0.0, status live", ev.Activation)
	}
	if len(ev.Activation.Satisfies) != 1 || ev.Activation.Satisfies[0] != "endpoint" {
		t.Fatalf("activation.Satisfies = %+v, want [endpoint]", ev.Activation.Satisfies)
	}
	wantKey := operation.ContractActivate("axon", "XC-axon-act-a", "1.0.0", []string{"endpoint"}, "live now")
	if fake.calls[0].OperationKey != wantKey {
		t.Fatalf("OperationKey = %q, want %q", fake.calls[0].OperationKey, wantKey)
	}
}

// TestContractActivateRefusesBelowFloor is refusal 2 (Wave 26C brief): below
// contract.ContractPublicationFloor, `activation` has no event/v1 shape at
// all, so this verb refuses outright rather than silently falling back.
func TestContractActivateRefusesBelowFloor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "axon", "act-b", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	// No floor set (contractTestDeps' default Manifest never sets
	// min_binary_version) — below the floor, the common pre-P5 case.
	handler := newContractActivateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-axon-act-b", Version: "1.0.0", Satisfies: []string{"endpoint"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a floor refusal")
	}
	if !strings.Contains(err.Error(), "min_binary_version") {
		t.Fatalf("expected a floor refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractActivateRefusesUndeclaredItem is refusal 4: --satisfies may
// only name an item already declared in x_operational[].
func TestContractActivateRefusesUndeclaredItem(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "axon", "act-c", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-axon-act-c", Version: "1.0.0", Satisfies: []string{"registration"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an undeclared-item refusal")
	}
	if !strings.Contains(err.Error(), "not a named item") {
		t.Fatalf("expected an undeclared-item refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractActivateRefusesUnpublishedVersion is refusal 3: activation
// names a published version's readiness, it is not a way to publish one.
func TestContractActivateRefusesUnpublishedVersion(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "axon", "act-e", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-axon-act-e", Version: "2.0.0", Satisfies: []string{"endpoint"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a not-published refusal")
	}
	if !strings.Contains(err.Error(), "has not been published") {
		t.Fatalf("expected a not-published refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractActivateRefusesUnownedContract is refusal 1: only the
// producer (the system embedded in the contract's own id) may declare its
// operational readiness.
func TestContractActivateRefusesUnownedContract(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "beta", "content-feed", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	// contractTestDeps sets OwnSystem to "axon"; the contract above is
	// owned by "beta".
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-beta-content-feed", Version: "1.0.0", Satisfies: []string{"endpoint"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an ownership refusal")
	}
	if !strings.Contains(err.Error(), "not owned by this system") {
		t.Fatalf("expected an ownership refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractActivateRequiresSatisfies proves the required-field guard
// fires before any disk access or funnel call.
func TestContractActivateRequiresSatisfies(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	seedActivatablePublished(t, mirrorDir, "axon", "act-f", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	fake := &fakeFunnel{}
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-axon-act-f", Version: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a required-field error (satisfies is empty)")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("usage error must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractActivateOnUnsyncedMirrorNamesTheCondition is epic-backlog
// B31's own MCP instance: internal/cli's runActivate was fixed to name "run
// `a2a sync` first" instead of surfacing contractReadDescriptor's raw open()
// failure (an absolute cache path) verbatim — but the fix never reached this
// package's own mirror of that verb, ADR-001's duplication notwithstanding.
// The contract is owned by this system and the floor is satisfied (both
// refusals ahead of the descriptor read), so the ONLY thing wrong is that the
// descriptor was never synced into the mirror.
func TestContractActivateOnUnsyncedMirrorNamesTheCondition(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	// Deliberately no seedActivatablePublished/writeContractDescriptor call:
	// "XC-axon-act-unsynced" is owned by "axon" (contractTestDeps' OwnSystem)
	// but its descriptor was never written into mirrorDir.

	fake := &fakeFunnel{}
	handler := newContractActivateHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
	args, _ := json.Marshal(ContractActivateInput{ID: "XC-axon-act-unsynced", Version: "1.0.0", Satisfies: []string{"endpoint"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an unsynced-mirror refusal")
	}
	if !strings.Contains(err.Error(), "run `a2a sync` first") {
		t.Fatalf("refusal must name the condition and the remedy (\"run `a2a sync` first\"), got %q", err.Error())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
	}
}

// TestContractAdoptRefusesANonAdoptableContract closes a hole the 2026-08-10
// coherence audit found HOURS after the CLI half shipped, and the gap is the
// point of the test rather than a footnote.
//
// P5's AC2 counterweight — a contract declaring itself non-adoptable cannot be
// adopted — was committed FIRST, in its own commit, precisely so threat-model
// T2's escape hatch would never exist without the economics that make it
// unattractive. It landed on `a2a contract adopt` and NOT on this surface,
// which performs the identical pin: `contractUpsertDependency` writes the same
// `space.Dependency` into the same `consumes.yaml`, and that dependency IS the
// pin the criterion is about. An MCP-driven agent could adopt what a CLI agent
// could not.
//
// The parity gate could not catch it: `cmd/a2a/mcp_parity_test.go` enforces a
// bijection over VERBS and contract sub-actions, and `adopt` exists on both.
// It says nothing about whether the two implementations REFUSE the same things.
func TestContractAdoptRefusesANonAdoptableContract(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		slug    string
		binding string
		major   int
	}{
		{"the bare sentinel", "nb1", "x_binding: none\n", 0},
		{"the long form with adoptable false", "nb2", "x_binding:\n  artifact_class: non_binding_review\n  compatibility_status: none\n  adoptable: false\n  runtime_pinnable: false\n", 0},
		// An explicit major is the route that made this reachable at all: the
		// descriptor used to be read ONLY when major was 0, so supplying one
		// skipped the read entirely. If this subtest ever passes while the
		// others fail, the check has drifted back inside that branch.
		{"an explicit major cannot skip the check", "nb3", "x_binding: none\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			// `beta`'s contract, adopted by `axon` — adopt refuses one's own.
			writeContractDescriptorWithXOperational(t, mirrorDir, "beta", tc.slug, "1.0.0", tc.binding)

			fake := &fakeFunnel{}
			handler := newContractAdoptHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
			args, _ := json.Marshal(ContractAdoptInput{ID: "XC-beta-" + tc.slug, Major: tc.major})
			_, _, err := handler(context.Background(), args)
			if err == nil {
				t.Fatal("MCP adopted a contract that declares itself non-adoptable — the T2 counterweight is CLI-only and the pin went through on this surface")
			}
			if !strings.Contains(err.Error(), "non-adoptable") {
				t.Fatalf("refused, but not for adoptability: %v", err)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("the refusal must precede any funnel write, got %+v", fake.calls)
			}
		})
	}
}

// TestContractAdoptStillAdoptsWhenBindingPermitsIt is the other direction: a
// refusal that fires on everything is not a refusal. Undeclared (no x_binding
// at all) is adoptable by P-1 — absence is not a declared `none`.
func TestContractAdoptStillAdoptsWhenBindingPermitsIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, slug, binding string }{
		{"undeclared", "ok1", ""},
		{"declared adoptable", "ok2", "x_binding:\n  artifact_class: api\n  compatibility_status: strict-semver\n  adoptable: true\n  runtime_pinnable: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			writeContractDescriptorWithXOperational(t, mirrorDir, "beta", tc.slug, "1.0.0", tc.binding)

			fake := &fakeFunnel{}
			handler := newContractAdoptHandler(contractTestDepsAtFloor(mirrorDir, fake, "0.19.0"))
			args, _ := json.Marshal(ContractAdoptInput{ID: "XC-beta-" + tc.slug, Major: 1})
			if _, _, err := handler(context.Background(), args); err != nil {
				t.Fatalf("an adoptable contract was refused: %v", err)
			}
			if len(fake.calls) != 1 {
				t.Fatalf("expected the adoption to reach the funnel exactly once, got %d", len(fake.calls))
			}
		})
	}
}

// --- LEGACY-four space naming (deprecate/retire/adopt/activate take the
// explicit "space" input their P6 siblings already carry) -----------------

// contractSpaceBuild is one connected space's own mirror + funnel, mirroring
// wire.go's own bySpace entry shape closely enough for these tests to prove
// the same thing wire.go's real wiring proves: WHICH space a call reached.
type contractSpaceBuild struct {
	mirrorDir string
	funnel    *fakeFunnel
	write     WriteDeps
}

// contractMultiSpaceDeps wires TWO connected spaces ("space-a"/"space-b"),
// each with its own mirror dir and funnel, the same shape wire.go's
// buildWriteDeps wires WriteDeps.ResolveSpace/SpaceOfArtifacts across every
// connected space: ONLY the returned (primary/"space-a") WriteDeps carries
// the two closures — "space-b"'s own build does not, matching production,
// where only Spaces[0]'s write is ever mutated to carry them (wire.go
// buildWriteDeps' own doc comment). floorA/floorB set each space's
// Manifest.MinBinaryVersion (the activate tests need these to differ).
func contractMultiSpaceDeps(t *testing.T, floorA, floorB string) (deps ContractDeps, a, b *contractSpaceBuild) {
	t.Helper()
	a = &contractSpaceBuild{mirrorDir: t.TempDir(), funnel: &fakeFunnel{}}
	b = &contractSpaceBuild{mirrorDir: t.TempDir(), funnel: &fakeFunnel{}}

	a.write = testWriteDeps(a.mirrorDir, a.funnel)
	a.write.SpaceID = "space-a"
	a.write.OwnSystem = "axon"
	a.write.Manifest.MinBinaryVersion = floorA

	b.write = testWriteDeps(b.mirrorDir, b.funnel)
	b.write.SpaceID = "space-b"
	b.write.OwnSystem = "axon"
	b.write.Manifest.MinBinaryVersion = floorB

	connected := []string{"space-a", "space-b"}
	a.write.ResolveSpace = func(spaceID string) (WriteDeps, error) {
		switch spaceID {
		case "":
			return WriteDeps{}, fmt.Errorf(
				"mcp: space is required when multiple spaces are connected; connected spaces are %s", strings.Join(connected, ", "))
		case "space-a":
			return a.write, nil
		case "space-b":
			return b.write, nil
		default:
			return WriteDeps{}, fmt.Errorf(
				"mcp: space %q is not connected; connected spaces are %s", spaceID, strings.Join(connected, ", "))
		}
	}
	a.write.SpaceOfArtifacts = func(ids []string) (string, error) {
		return "", fmt.Errorf(
			"mcp: no connected space's mirror holds %s; connected spaces are %s", strings.Join(ids, ", "), strings.Join(connected, ", "))
	}

	return ContractDeps{WriteDeps: a.write}, a, b
}

func TestContractDeprecateResolvesNamedSecondSpace(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptor(t, b.mirrorDir, "dep-space-b", "1.0.0")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 0, "XC-axon-dep-space-b", "publish", "axon")

	handler := newContractDeprecateHandler(deps)
	args, _ := json.Marshal(ContractDeprecateInput{
		Space: "space-b", ID: "XC-axon-dep-space-b",
		Successor: "XC-axon-dep-space-b-next@1.0.0", Sunset: "2099-01-01",
	})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("deprecate naming space-b failed: %v", err)
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected the write to reach space-b's funnel, got %d calls", len(b.funnel.calls))
	}
	if len(a.funnel.calls) != 0 {
		t.Fatalf("space-a's funnel must never be called when the caller named space-b, got %+v", a.funnel.calls)
	}
}

func TestContractRetireResolvesNamedSecondSpace(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptor(t, b.mirrorDir, "ret-space-b", "1.0.0")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 0, "XC-axon-ret-space-b", "publish", "axon")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 1, "XC-axon-ret-space-b", "deprecate", "axon")

	handler := newContractRetireHandler(deps)
	args, _ := json.Marshal(ContractRetireInput{Space: "space-b", ID: "XC-axon-ret-space-b"})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("retire naming space-b failed: %v", err)
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected the write to reach space-b's funnel, got %d calls", len(b.funnel.calls))
	}
	if len(a.funnel.calls) != 0 {
		t.Fatalf("space-a's funnel must never be called when the caller named space-b, got %+v", a.funnel.calls)
	}
}

func TestContractAdoptResolvesNamedSecondSpace(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptorWithXOperational(t, b.mirrorDir, "beta", "adopt-space-b", "1.0.0", "")

	handler := newContractAdoptHandler(deps)
	args, _ := json.Marshal(ContractAdoptInput{Space: "space-b", ID: "XC-beta-adopt-space-b", Major: 1})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("adopt naming space-b failed: %v", err)
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected the write to reach space-b's funnel, got %d calls", len(b.funnel.calls))
	}
	if len(a.funnel.calls) != 0 {
		t.Fatalf("space-a's funnel must never be called when the caller named space-b, got %+v", a.funnel.calls)
	}
}

// TestContractActivateResolvesNamedSecondSpaceAndItsFloor is the activate-
// specific requirement: the min_binary_version floor `activate` enforces
// must be the RESOLVED space's, not the space the handler was constructed
// against. space-a's floor is deliberately left below
// contract.ContractPublicationFloor (empty) while space-b's is at it — a
// call naming space-b must succeed on space-b's OWN floor.
func TestContractActivateResolvesNamedSecondSpaceAndItsFloor(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "0.19.0")
	seedActivatablePublished(t, b.mirrorDir, "axon", "act-space-b", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	handler := newContractActivateHandler(deps)
	args, _ := json.Marshal(ContractActivateInput{
		Space: "space-b", ID: "XC-axon-act-space-b", Version: "1.0.0", Satisfies: []string{"endpoint"},
	})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("activate naming space-b failed: %v", err)
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected the write to reach space-b's funnel, got %d calls", len(b.funnel.calls))
	}
	if len(a.funnel.calls) != 0 {
		t.Fatalf("space-a's funnel must never be called when the caller named space-b, got %+v", a.funnel.calls)
	}
	ev := decodeContractActivateEvent(t, b.funnel.calls[0].Files[0].Content)
	if ev.Schema != "event/v2" {
		t.Fatalf("activate against space-b (floor 0.19.0) authored schema %q, want event/v2 — the RESOLVED space's floor was not used", ev.Schema)
	}
}

// TestContractLegacyFourRefuseWhenSpaceOmittedAndMultipleConnected covers
// all four verbs: with two spaces connected and no "space" named, each must
// refuse — naming the connected spaces — rather than silently defaulting to
// whichever space the handler happened to be constructed against.
func TestContractLegacyFourRefuseWhenSpaceOmittedAndMultipleConnected(t *testing.T) {
	t.Parallel()
	deps, _, _ := contractMultiSpaceDeps(t, "0.19.0", "0.19.0")

	cases := []struct {
		name    string
		handler HandlerFunc
		args    any
	}{
		{"deprecate", newContractDeprecateHandler(deps), ContractDeprecateInput{
			ID: "XC-axon-any", Successor: "XC-axon-other@1.0.0", Sunset: "2099-01-01",
		}},
		{"retire", newContractRetireHandler(deps), ContractRetireInput{ID: "XC-axon-any"}},
		{"adopt", newContractAdoptHandler(deps), ContractAdoptInput{ID: "XC-beta-any", Major: 1}},
		{"activate", newContractActivateHandler(deps), ContractActivateInput{
			ID: "XC-axon-any", Version: "1.0.0", Satisfies: []string{"endpoint"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}
			_, _, err = tc.handler(context.Background(), args)
			if err == nil {
				t.Fatal("expected a refusal naming the connected spaces")
			}
			for _, want := range []string{"space-a", "space-b"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// TestContractDeprecateRefusesUnconnectedSpaceByName proves an explicit
// "space" that names no connected space is refused, naming the space that
// was asked for AND the ones actually connected.
func TestContractDeprecateRefusesUnconnectedSpaceByName(t *testing.T) {
	t.Parallel()
	deps, _, _ := contractMultiSpaceDeps(t, "", "")
	handler := newContractDeprecateHandler(deps)
	args, _ := json.Marshal(ContractDeprecateInput{
		Space: "space-nowhere", ID: "XC-axon-any", Successor: "XC-axon-other@1.0.0", Sunset: "2099-01-01",
	})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal naming the unconnected space")
	}
	if !strings.Contains(err.Error(), `"space-nowhere"`) {
		t.Fatalf("refusal = %v, want it to name the requested space", err)
	}
	for _, want := range []string{"space-a", "space-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, want it to name the connected spaces too", err)
		}
	}
}

// --- the poisoning tests: TWO sequential calls through ONE constructed
// handler, naming different spaces, must not let the first call's resolved
// space leak into the second (the captured `deps` in newContractXHandler's
// closure is shared across every call the session ever makes) -------------

func TestContractDeprecateSecondCallNotPoisonedByFirst(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptor(t, a.mirrorDir, "pois-a", "1.0.0")
	writeLifecycleEvent(t, a.mirrorDir, "axon", 0, "XC-axon-pois-a", "publish", "axon")
	writeContractDescriptor(t, b.mirrorDir, "pois-b", "1.0.0")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 0, "XC-axon-pois-b", "publish", "axon")

	handler := newContractDeprecateHandler(deps)

	firstArgs, _ := json.Marshal(ContractDeprecateInput{
		Space: "space-b", ID: "XC-axon-pois-b", Successor: "XC-axon-pois-b-next@1.0.0", Sunset: "2099-01-01",
	})
	if _, _, err := handler(context.Background(), firstArgs); err != nil {
		t.Fatalf("first call (space-b) failed: %v", err)
	}

	secondArgs, _ := json.Marshal(ContractDeprecateInput{
		Space: "space-a", ID: "XC-axon-pois-a", Successor: "XC-axon-pois-a-next@1.0.0", Sunset: "2099-01-01",
	})
	if _, _, err := handler(context.Background(), secondArgs); err != nil {
		t.Fatalf("second call (space-a) failed — the first call's resolved space poisoned the shared closure: %v", err)
	}

	if len(a.funnel.calls) != 1 {
		t.Fatalf("expected space-a's funnel to be called exactly once (by the SECOND call), got %d", len(a.funnel.calls))
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected space-b's funnel to be called exactly once (by the FIRST call), got %d", len(b.funnel.calls))
	}
}

func TestContractRetireSecondCallNotPoisonedByFirst(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptor(t, a.mirrorDir, "pois-a", "1.0.0")
	writeLifecycleEvent(t, a.mirrorDir, "axon", 0, "XC-axon-pois-a", "publish", "axon")
	writeLifecycleEvent(t, a.mirrorDir, "axon", 1, "XC-axon-pois-a", "deprecate", "axon")
	writeContractDescriptor(t, b.mirrorDir, "pois-b", "1.0.0")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 0, "XC-axon-pois-b", "publish", "axon")
	writeLifecycleEvent(t, b.mirrorDir, "axon", 1, "XC-axon-pois-b", "deprecate", "axon")

	handler := newContractRetireHandler(deps)

	firstArgs, _ := json.Marshal(ContractRetireInput{Space: "space-b", ID: "XC-axon-pois-b"})
	if _, _, err := handler(context.Background(), firstArgs); err != nil {
		t.Fatalf("first call (space-b) failed: %v", err)
	}

	secondArgs, _ := json.Marshal(ContractRetireInput{Space: "space-a", ID: "XC-axon-pois-a"})
	if _, _, err := handler(context.Background(), secondArgs); err != nil {
		t.Fatalf("second call (space-a) failed — the first call's resolved space poisoned the shared closure: %v", err)
	}

	if len(a.funnel.calls) != 1 {
		t.Fatalf("expected space-a's funnel to be called exactly once (by the SECOND call), got %d", len(a.funnel.calls))
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected space-b's funnel to be called exactly once (by the FIRST call), got %d", len(b.funnel.calls))
	}
}

func TestContractAdoptSecondCallNotPoisonedByFirst(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "", "")
	writeContractDescriptorWithXOperational(t, a.mirrorDir, "beta", "pois-a", "1.0.0", "")
	writeContractDescriptorWithXOperational(t, b.mirrorDir, "beta", "pois-b", "1.0.0", "")

	handler := newContractAdoptHandler(deps)

	firstArgs, _ := json.Marshal(ContractAdoptInput{Space: "space-b", ID: "XC-beta-pois-b", Major: 1})
	if _, _, err := handler(context.Background(), firstArgs); err != nil {
		t.Fatalf("first call (space-b) failed: %v", err)
	}

	secondArgs, _ := json.Marshal(ContractAdoptInput{Space: "space-a", ID: "XC-beta-pois-a", Major: 1})
	if _, _, err := handler(context.Background(), secondArgs); err != nil {
		t.Fatalf("second call (space-a) failed — the first call's resolved space poisoned the shared closure: %v", err)
	}

	if len(a.funnel.calls) != 1 {
		t.Fatalf("expected space-a's funnel to be called exactly once (by the SECOND call), got %d", len(a.funnel.calls))
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected space-b's funnel to be called exactly once (by the FIRST call), got %d", len(b.funnel.calls))
	}
}

func TestContractActivateSecondCallNotPoisonedByFirst(t *testing.T) {
	t.Parallel()
	deps, a, b := contractMultiSpaceDeps(t, "0.19.0", "0.19.0")
	seedActivatablePublished(t, a.mirrorDir, "axon", "pois-a", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")
	seedActivatablePublished(t, b.mirrorDir, "axon", "pois-b", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")

	handler := newContractActivateHandler(deps)

	firstArgs, _ := json.Marshal(ContractActivateInput{
		Space: "space-b", ID: "XC-axon-pois-b", Version: "1.0.0", Satisfies: []string{"endpoint"},
	})
	if _, _, err := handler(context.Background(), firstArgs); err != nil {
		t.Fatalf("first call (space-b) failed: %v", err)
	}

	secondArgs, _ := json.Marshal(ContractActivateInput{
		Space: "space-a", ID: "XC-axon-pois-a", Version: "1.0.0", Satisfies: []string{"endpoint"},
	})
	if _, _, err := handler(context.Background(), secondArgs); err != nil {
		t.Fatalf("second call (space-a) failed — the first call's resolved space poisoned the shared closure: %v", err)
	}

	if len(a.funnel.calls) != 1 {
		t.Fatalf("expected space-a's funnel to be called exactly once (by the SECOND call), got %d", len(a.funnel.calls))
	}
	if len(b.funnel.calls) != 1 {
		t.Fatalf("expected space-b's funnel to be called exactly once (by the FIRST call), got %d", len(b.funnel.calls))
	}
}
