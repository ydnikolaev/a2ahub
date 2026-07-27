package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
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
}

func contractTestDeps(mirrorDir string, funnel Funnel) ContractDeps {
	write := testWriteDeps(mirrorDir, funnel)
	write.OwnSystem = "axon"
	return ContractDeps{WriteDeps: write}
}

func TestContractPublishFirstPublishGated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-a", "0.0.0")

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-a", Version: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody == "" {
		t.Fatalf("expected first publish to be gated (advisory marker), got %+v", fake.calls)
	}
}

// TestContractPublishNewMajorBaselineIsPriorInLine is epic AC-8
// (04-per-version-lifecycle.md §4, Edge 2) on the MCP surface: the
// baseline `contract publish` computes its G2 major-bump gate against is
// max{v ∈ priorVersions : v < newVersion}, NOT the globally-highest
// published version. This handler does not run the compat check locally
// (see newContractPublishHandler's own doc comment — that half is
// CLI-only, covered by TestContractPublishMaintenanceLineBaselineIsPriorInLine
// in internal/cli), so the observable divergence here has to be through
// the G2 gate itself: publishing 1.2 while 2.0 exists does not flip
// isMajorBump either way (newVersion's major, 1, is not GREATER than
// either candidate baseline's major, 1 or 2) — the scenario that DOES
// distinguish the two rules is publishing a NEW major that is not the
// globally-highest one: priorVersions = {1.0.0, 1.1.0, 3.0.0}, publish
// "2.0.0". Correct baseline (max{v < 2.0.0} = 1.1.0, major 1): 2 > 1, a
// real major bump, gated. Wrong baseline (globally-highest 3.0.0, major
// 3): 2 > 3 is false, silently UNGATED — a major bump that should carry
// the advisory PR marker would not. TEETH: reverting
// contractSelectBaseline's call site back to
// `priorVersions[len(priorVersions)-1]` makes this test go RED (PRBody
// empty) — verified by making that revert and re-running (see this wave's
// own report).
func TestContractPublishNewMajorBaselineIsPriorInLine(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "newmajor", "3.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-newmajor", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-newmajor", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.1.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-newmajor", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "3.0.0")

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-newmajor", Version: "2.0.0"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if fake.calls[0].PRBody == "" {
		t.Fatalf("expected AC-8: baseline must be 1.1.0 (major 1), so 2.0.0 is a real major bump and must be G2-gated; got %+v", fake.calls)
	}
}

// TestContractSelectBaseline asserts epic AC-8's own literal scenario
// directly against the bridge (04-per-version-lifecycle.md §4, Edge 2):
// publishing 1.2 while 2.0 is already published compares against 1.1, not
// 2.0 — the globally-highest published version. The handler-level test
// above (TestContractPublishNewMajorBaselineIsPriorInLine) has to use a
// different numeric scenario because this handler runs no compat check
// and `>` makes the literal 1.2-while-2.0 case invisible through the G2
// gate alone; this unit test closes that gap by checking the bridge's own
// return value. Mirrors internal/cli's own TestContractSelectBaseline
// (ADR-001: contractSelectBaseline is unexported, never a shared symbol —
// decision 8's DECISION lives in internal/version.Baseline, which both
// bridges call).
func TestContractSelectBaseline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		priorVersions []contractSemver
		newVersion    contractSemver
		wantBaseline  contractSemver
		wantFound     bool
	}{
		{
			name:          "first-ever publish has no baseline",
			priorVersions: nil,
			newVersion:    contractSemver{1, 0, 0},
			wantFound:     false,
		},
		{
			name:          "ordinary sequential publish picks the immediate prior",
			priorVersions: []contractSemver{{1, 0, 0}},
			newVersion:    contractSemver{2, 0, 0},
			wantBaseline:  contractSemver{1, 0, 0},
			wantFound:     true,
		},
		{
			name:          "AC-8: maintenance 1.2 while 2.0 is published compares against 1.1, not 2.0",
			priorVersions: []contractSemver{{1, 0, 0}, {1, 1, 0}, {2, 0, 0}},
			newVersion:    contractSemver{1, 2, 0},
			wantBaseline:  contractSemver{1, 1, 0},
			wantFound:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := contractSelectBaseline(tc.priorVersions, tc.newVersion)
			if found != tc.wantFound {
				t.Fatalf("contractSelectBaseline(%v, %v) found = %v, want %v", tc.priorVersions, tc.newVersion, found, tc.wantFound)
			}
			if found && got != tc.wantBaseline {
				t.Fatalf("contractSelectBaseline(%v, %v) = %v, want %v", tc.priorVersions, tc.newVersion, got, tc.wantBaseline)
			}
		})
	}
}

func TestContractPublishMinorBumpUngated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-c", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-widget-c", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-c", Bump: "minor"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected a declared-minor bump to be UNGATED, got %+v", fake.calls)
	}
}

// TestContractPublishOverlayCarriesStagedSchema is P37 Wave I's MCP-parity
// coverage (mirrors internal/cli's own TestContractPublishOverlayCarriesStagedSchema,
// minus the POL-007/POL-009 assertions this handler deliberately does not
// run locally — see newContractPublishHandler's own doc comment). TEETH:
// reverting newContractPublishHandler to compute Files/digest from the
// mirror's own contractReadDescriptor-relative tree alone (dropping the
// contractStagingOverlay fold-in) reds this test — the staged schema would
// never appear in Files, and the recorded digest would equal the
// mirror-only digest.
func TestContractPublishOverlayCarriesStagedSchema(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-o", "0.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/widget-o/schema/main.schema.json", `{"type":"object"}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/widget-o/fixtures/valid/ok.json", `{}`)

	mirrorOnlyDigest, _, err := artifact.DigestTreeFS(filepath.Join(mirrorDir, "axon", "provides", "widget-o"), []string{"schema", "fixtures"})
	if err != nil {
		t.Fatalf("DigestTreeFS: %v", err)
	}

	stagingDir := t.TempDir()
	staged := filepath.Join(stagingDir, "axon", "provides", "widget-o", "schema", "main.schema.json")
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte(`{"type":"object","properties":{"y":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeFunnel{}
	deps := contractTestDeps(mirrorDir, fake)
	deps.StagingDir = stagingDir
	handler := newContractPublishHandler(deps)
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-o", Version: "1.0.0"})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}

	var sawStagedSchema bool
	var eventRaw []byte
	for _, f := range fake.calls[0].Files {
		if f.Path == "axon/provides/widget-o/schema/main.schema.json" {
			sawStagedSchema = true
			if string(f.Content) != `{"type":"object","properties":{"y":{}}}` {
				t.Fatalf("carried schema = %q, want the STAGED content", f.Content)
			}
		}
		if strings.HasPrefix(f.Path, "axon/events/") {
			eventRaw = f.Content
		}
	}
	if !sawStagedSchema {
		t.Fatalf("expected the staged schema to be carried in Files, got %+v", fake.calls[0].Files)
	}
	if eventRaw == nil {
		t.Fatalf("expected a lifecycle event file, got %+v", fake.calls[0].Files)
	}
	if strings.Contains(string(eventRaw), mirrorOnlyDigest) {
		t.Fatalf("recorded event %s carries the MIRROR-TREE-ONLY digest %q — the staged schema override was not reflected", eventRaw, mirrorOnlyDigest)
	}
}

// TestContractPublishNoStagingDigestMatchesMirrorTree is the no-regression
// case: deps.StagingDir unset (the zero value, every construction that
// predates this wave) must record the SAME digest artifact.DigestTreeFS
// itself would compute over the unmodified mirror tree.
func TestContractPublishNoStagingDigestMatchesMirrorTree(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-p", "0.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/widget-p/schema/main.schema.json", `{"type":"object"}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/widget-p/fixtures/valid/ok.json", `{}`)

	wantDigest, _, err := artifact.DigestTreeFS(filepath.Join(mirrorDir, "axon", "provides", "widget-p"), []string{"schema", "fixtures"})
	if err != nil {
		t.Fatalf("DigestTreeFS: %v", err)
	}

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake)) // StagingDir unset
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-p", Version: "1.0.0"})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	var eventRaw []byte
	for _, f := range fake.calls[0].Files {
		if strings.HasPrefix(f.Path, "axon/events/") {
			eventRaw = f.Content
		}
	}
	if eventRaw == nil {
		t.Fatalf("expected a lifecycle event file, got %+v", fake.calls[0].Files)
	}
	if !strings.Contains(string(eventRaw), wantDigest) {
		t.Fatalf("recorded event %s does not carry DigestTreeFS's own answer %q", eventRaw, wantDigest)
	}
}

// TestContractStagingOverlaySkipsSidecarOutsidePrefix is
// contractStagingOverlay's own defensive branch: a staged FileWrite whose
// Path does not start with THIS contract's own relDir+"/" prefix (should
// never happen given template.ContractSidecarsFromStaging's own slug-
// scoped walk, but defended anyway) is skipped, never allowed to overlay
// an unrelated key.
func TestContractStagingOverlaySkipsSidecarOutsidePrefix(t *testing.T) {
	t.Parallel()
	landed := map[string][]byte{"schema/a.json": []byte("landed")}
	staged := []space.FileWrite{{Path: "beta/provides/other/schema/a.json", Content: []byte("staged")}}
	got := contractStagingOverlay(landed, staged, "axon/provides/widget")
	if string(got["schema/a.json"]) != "landed" {
		t.Fatalf("expected the out-of-prefix staged file to be skipped (landed value preserved); got %q", got["schema/a.json"])
	}
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

func gitRunTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

func TestContractDiffTwoVersions(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitRunTest(t, mirrorDir, "init", "-b", "main")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object"}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/fixtures/valid/ok.json", `{}`)
	gitRunTest(t, mirrorDir, "add", "-A")
	gitRunTest(t, mirrorDir, "commit", "-m", "publish 1.0.0")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.1.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	gitRunTest(t, mirrorDir, "add", "-A")
	gitRunTest(t, mirrorDir, "commit", "-m", "publish 1.1.0")

	fake := &fakeFunnel{}
	handler := newContractDiffHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDiffInput{ID: "XC-axon-diffable", V1: "1.0.0", V2: "1.1.0"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	tree := result.(contractDiffTree)
	found := false
	for _, p := range tree.Changed {
		if p == "schema/main.schema.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected schema/main.schema.json under Changed, got %+v", tree)
	}
}

func TestContractVerifyExportMatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "exportable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/exportable/schema/main.schema.json", `{"type":"object"}`)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object"}`)

	digest, _, err := artifact.DigestTreeFS(filepath.Join(mirrorDir, "axon/provides/exportable"), contractDigestSubtrees)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	writeContractDescriptorWithDigest(t, mirrorDir, "exportable", "1.0.0", digest)

	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractVerifyExportInput{Local: localPath, Ref: "XC-axon-exportable"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify-export failed: %v", err)
	}
	if !result.(contractVerifyExportResult).Matches {
		t.Fatalf("expected a match, got %+v", result)
	}
}

func TestContractVerifyExportMismatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "drifted", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/drifted/schema/main.schema.json", `{"type":"object"}`)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object","extra":true}`)

	writeContractDescriptorWithDigest(t, mirrorDir, "drifted", "1.0.0", "sha256:0000000000000000000000000000000000000000000000000000000000000000")

	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractVerifyExportInput{Local: localPath, Ref: "XC-axon-drifted"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify-export failed: %v", err)
	}
	if result.(contractVerifyExportResult).Matches {
		t.Fatalf("expected a digest mismatch, got %+v", result)
	}
}

func TestContractVerifyExportMissingArgs(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(t.TempDir(), fake))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for missing local/ref")
	}
}

func TestContractDiffSameVersionRefused(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	handler := newContractDiffHandler(contractTestDeps(t.TempDir(), fake))
	args, _ := json.Marshal(ContractDiffInput{ID: "XC-axon-x", V1: "1.0.0", V2: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error when v1 == v2")
	}
}

func writeContractDescriptorWithDigest(t *testing.T, mirrorDir, slug, version, digest string) {
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
		"generated_from: {tool: test, source_digest: \"" + digest + "\"}\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
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
	// P37 D-D: a JSON-Schema contract is drafted AND scaffolded — the .md
	// plus its starter schema and valid fixture, so the contract is
	// publishable (POL-009) and §5.4b has a baseline the moment it exists.
	// Every entry reports the same contract id; the paths differ.
	if !ok || len(drafts) != 3 {
		t.Fatalf("expected the drafted contract plus its D-D schema/fixture scaffold (3 entries), got %#v", result)
	}
	for _, d := range drafts {
		if !strings.HasPrefix(d.ID, "XC-") {
			t.Fatalf("every entry must report the contract's own id, got %#v", d)
		}
	}
	if !strings.HasSuffix(drafts[0].Path, ".md") {
		t.Fatalf("the draft itself must come first, got %q", drafts[0].Path)
	}
	if !strings.HasSuffix(drafts[1].Path, "/schema/widget.schema.json") || !strings.HasSuffix(drafts[2].Path, "/fixtures/valid/widget.json") {
		t.Fatalf("scaffold paths must follow D-E's stem mapping, got %q and %q", drafts[1].Path, drafts[2].Path)
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
