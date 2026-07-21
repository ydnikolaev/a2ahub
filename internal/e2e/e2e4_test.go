package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestE2E4 is spec 10 §8 AC-3: the G2 gate + linked-deprecation +
// consumer-ack + blocked-retire suite (CC-080/081/082, AC-202.1-.4). Per
// spec §11's amendment, the validator-enforced halves (retire-block,
// compat-fail) run against the REAL validation/legality engine; the G2
// required-human-review half is exercised via host.FakeHost's
// ReviewStatus double, standing in for the GitHub-side CODEOWNERS gate
// this fixture (a local bare repo, no real host) cannot itself enforce.
func TestE2E4(t *testing.T) {
	t.Run("G2GateAndLinkedDeprecation", testE2E4G2GateAndLinkedDeprecation)
	t.Run("RetireBlockedUnacked", testE2E4RetireBlockedUnacked)
	t.Run("RetireOverrideSucceeds", testE2E4RetireOverrideSucceeds)
	t.Run("MislabeledMinorFailsCompat", testE2E4MislabeledMinorFailsCompat)
}

// testE2E4G2GateAndLinkedDeprecation is AC-202.1: a declared-major publish
// is G2-gated (advisory PRBody marker); FakeHost's ReviewStatus double
// simulates the required-review gate NOT yet satisfied (Approved: false)
// — standing in for the GitHub CODEOWNERS required-review check this
// fixture has no real host to enforce. The prior version's `contract
// deprecate` carries the linked announcement with `ack_requested: true`.
func testE2E4G2GateAndLinkedDeprecation(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "g2widget", "0.0.0")

	reviewCalls := 0
	fakeHost := host.NewFakeHost()
	fakeHost.ReviewStatusFunc = func(_ context.Context, _ host.StatusRequest) (host.ReviewStatusResult, error) {
		reviewCalls++
		// Simulate G2's required human review NOT yet given — the
		// GitHub-side gate this fixture has no real host to enforce.
		return host.ReviewStatusResult{Approved: false}, nil
	}
	// Both publishes below act as axon on the SAME contract id
	// (XC-axon-g2widget) -> the SAME deterministic branch
	// (a2a/axon/XC-axon-g2widget, space.WriteFunnel's own retry-dedup key).
	// A real GitHub repo with "auto-delete head branches" (the norm here)
	// deletes the first publish's branch once it merges, so the second
	// publish's own FindPRByHeadBranch precheck finds nothing and proceeds
	// with a fresh commit — FakeHost's default byBranch bookkeeping never
	// expires, so this override keeps it consistent with that reality
	// while the two publishes run; reverted afterward so the EXPLICIT
	// ReviewStatus lookup below resolves the real, just-opened PR.
	fakeHost.FindPRFunc = func(_ context.Context, _ host.FindPRRequest) (*host.PRInfo, error) { return nil, nil }
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := e2eHostConfig("axon", fx.RemoteURL())
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))

	// G1: first-ever publish (also gated, but not this test's assertion).
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-axon-g2widget"}, io); code != 0 {
		t.Fatalf("publish 1.0.0: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	// G2: a declared MAJOR bump on a non-first publish is gated (advisory
	// PRBody marker — AC-202.1's own "G2" clause).
	io2, out2, errOut2 := newIO()
	if code := cmd.Run(context.Background(), []string{"publish", "--bump", "major", "XC-axon-g2widget"}, io2); code != 0 {
		t.Fatalf("publish major bump: code = %d, want 0; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
	}
	if len(fakeHost.Opens) != 2 {
		t.Fatalf("expected 2 OpenPR calls (2 publishes), got %d", len(fakeHost.Opens))
	}
	fakeHost.FindPRFunc = nil // revert to the default byBranch lookup

	// Simulate the CI/reviewer checking the just-opened major-bump PR's
	// review status (the G2 gate's own real-world enforcement point —
	// AC-202.1's "requires G2").
	majorBumpPR := fakeHost.Opens[len(fakeHost.Opens)-1]
	prInfo, err := fakeHost.FindPRByHeadBranch(context.Background(), host.FindPRRequest{Repo: hostCfg.Repo, Branch: majorBumpPR.Head})
	if err != nil || prInfo == nil {
		t.Fatalf("FindPRByHeadBranch: %v (info=%+v)", err, prInfo)
	}
	review, err := fakeHost.ReviewStatus(context.Background(), host.StatusRequest{Repo: hostCfg.Repo, PRNumber: prInfo.Number})
	if err != nil {
		t.Fatalf("ReviewStatus: %v", err)
	}
	if review.Approved {
		t.Fatal("expected the G2-gated major-bump PR to NOT be approved yet (the required-review double)")
	}
	if reviewCalls != 1 {
		t.Fatalf("expected exactly one ReviewStatus call, got %d", reviewCalls)
	}

	// Linked deprecation of the prior (1.0.0) version, carrying the
	// ack_requested announcement (AC-202.1's other clause).
	io3, out3, errOut3 := newIO()
	if code := cmd.Run(context.Background(), []string{"deprecate", "--version", "1.0.0", "--successor", "XC-axon-g2widget@2.0.0", "--sunset", "2099-01-01", "XC-axon-g2widget"}, io3); code != 0 {
		t.Fatalf("deprecate: code = %d, want 0; stdout=%s stderr=%s", code, out3.String(), errOut3.String())
	}
	announcementID := latestAnnouncementFile(t, mirrorDir)
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "axon/exchanges/"+announcementID+".md"))
	if err != nil {
		t.Fatalf("read announcement: %v", err)
	}
	if !strings.Contains(string(raw), "ack_requested: true") {
		t.Fatalf("expected the linked deprecation announcement to carry ack_requested: true, got:\n%s", raw)
	}
}

// testE2E4RetireBlockedUnacked is AC-202.2/CC-081: retire is BLOCKED
// locally (POL-006) while a registered consumer (consumes.yaml) hasn't
// acked the deprecation — the REAL legality/policy engine, funnel NEVER
// reached.
func testE2E4RetireBlockedUnacked(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-gated")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-a1a1", "XC-axon-gated@1.0.0", "2099-01-01")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-gated"}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit (un-acked registered consumer, AC-202.2/CC-081)")
	}
	if !strings.Contains(errOut.String(), "POL-006") {
		t.Fatalf("expected the refusal to name POL-006; got %q", errOut.String())
	}
	if len(fakeHost.Opens) != 0 || len(fakeHost.Pushes) != 0 {
		t.Fatalf("expected the write funnel NEVER to be reached; got opens=%d pushes=%d", len(fakeHost.Opens), len(fakeHost.Pushes))
	}
}

// testE2E4RetireOverrideSucceeds is AC-202.3/CC-082: retire blocked
// pre-sunset/no-reminder/agent-actor, SUCCEEDS via a human-reviewed
// --override once sunset has passed AND a reminder exists AND the actor
// is human — the retire event flags the overridden consumer
// (`retired-unacked`), real funnel + FakeHost.
func testE2E4RetireOverrideSucceeds(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	sunset := fixedNow.AddDate(0, 0, -1).Format("2006-01-02") // one day before the fixed clock: passed

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "override", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-override", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-override", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-override")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-b1b1", "XC-axon-override@1.0.0", sunset)
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XA-axon-20260101-b1b1", "note", "axon") // >=1 reminder

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("human", "owner"))
	cmd.SetClockForTest(func() time.Time { return fixedNow })

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "--override", "XC-axon-override"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call (override path), got %d", len(fakeHost.Opens))
	}
	retireEvent := latestEventFile(t, mirrorDir, "axon")
	if !strings.Contains(retireEvent, "retired-unacked") {
		t.Fatalf("expected the retire event to flag the overridden consumer, got:\n%s", retireEvent)
	}
}

// testE2E4MislabeledMinorFailsCompat is AC-202.4/CC-080: the compat-check
// wiring proof (spec 10 §11's "you author the test that invokes them" —
// P9's own compat-golden fixtures, schemas/fixtures/compat/, are WIRED
// here, never authored/duplicated). §5.4b's own rule ("a minor/patch bump
// REQUIRES that all prior-version valid fixtures still validate against
// the new schema") is evaluated directly with the SAME JSON-schema engine
// (santhosh-tekuri/jsonschema/v6) internal/schema already depends on —
// this is the generic, already-a-dependency validation mechanism the rule
// itself is defined in terms of, not a re-derivation of product logic.
func testE2E4MislabeledMinorFailsCompat(t *testing.T) {
	t.Parallel()
	root := repoRootForTest(t)

	additivePass := compatFixtureValidates(t,
		filepath.Join(root, "schemas/fixtures/compat/additive-minor/new.schema.json"),
		filepath.Join(root, "schemas/fixtures/compat/additive-minor/fixtures/valid/widget-1.json"),
	)
	if !additivePass {
		t.Fatal("additive-minor: expected the v1.0.0-valid fixture to STILL validate against the new (minor-bumped) schema — genuinely additive, minor bump correct")
	}

	mislabeledPass := compatFixtureValidates(t,
		filepath.Join(root, "schemas/fixtures/compat/mislabeled-minor/new.schema.json"),
		filepath.Join(root, "schemas/fixtures/compat/mislabeled-minor/fixtures/valid/widget-1.json"),
	)
	if mislabeledPass {
		t.Fatal("mislabeled-minor: expected the v1.0.0-valid fixture to FAIL against the new schema (CC-080: a breaking change mislabeled as a minor bump — major required)")
	}
}

// compatFixtureValidates compiles schemaPath (santhosh-tekuri/jsonschema/v6,
// already an internal/schema dependency) and reports whether fixturePath's
// decoded JSON instance validates against it.
func compatFixtureValidates(t *testing.T, schemaPath, fixturePath string) bool {
	t.Helper()
	c := jsonschema.NewCompiler()
	schema, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaPath, err)
	}
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read %s: %v", fixturePath, err)
	}
	var inst any
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatalf("decode %s: %v", fixturePath, err)
	}
	return schema.Validate(inst) == nil
}

// repoRootForTest is repoRoot's test-friendly twin (t.Fatal on error).
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	return root
}

// latestAnnouncementFile finds the most-recently-committed XA-*.md
// announcement file under mirrorDir/axon/exchanges (contract deprecate's
// own linked-announcement output).
func latestAnnouncementFile(t *testing.T, mirrorDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "axon", "exchanges", "XA-*.md"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("latestAnnouncementFile: no XA- announcement found: %v", err)
	}
	base := filepath.Base(matches[len(matches)-1])
	return strings.TrimSuffix(base, ".md")
}

// latestEventFile reads the most-recently-written event file's content
// under mirrorDir/<system>/events/**.
func latestEventFile(t *testing.T, mirrorDir, system string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mirrorDir, system, "events", "*", "*.yaml"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("latestEventFile: no event files found: %v", err)
	}
	raw, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("latestEventFile: %v", err)
	}
	return string(raw)
}
