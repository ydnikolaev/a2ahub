package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// P37 D-D/P43: a JSON-Schema contract is drafted AND scaffolded — the .md
	// plus its starter schema and valid and invalid fixtures, so the contract is
	// publishable (POL-009) and §5.4b has a baseline the moment it exists.
	// Every entry reports the same contract id; the paths differ.
	if !ok || len(drafts) != 4 {
		t.Fatalf("expected the drafted contract plus its schema/fixture scaffold (4 entries), got %#v", result)
	}
	for _, d := range drafts {
		if !strings.HasPrefix(d.ID, "XC-") {
			t.Fatalf("every entry must report the contract's own id, got %#v", d)
		}
	}
	if !strings.HasSuffix(drafts[0].Path, ".md") {
		t.Fatalf("the draft itself must come first, got %q", drafts[0].Path)
	}
	if !strings.HasSuffix(drafts[1].Path, "/schema/widget.schema.json") ||
		!strings.HasSuffix(drafts[2].Path, "/fixtures/valid/widget.json") ||
		!strings.HasSuffix(drafts[3].Path, "/fixtures/invalid/widget.json") {
		t.Fatalf("scaffold paths must follow D-E's stem mapping, got %q, %q, and %q", drafts[1].Path, drafts[2].Path, drafts[3].Path)
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
