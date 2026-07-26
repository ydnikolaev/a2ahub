package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
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
	writeContractSchemaFixture(t, mirrorDir, "axon", "g2widget")

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

	// Land the first publish: the second one reads the COMMITTED descriptor,
	// which reaches the mirror's base branch only when the first PR merges.
	// This chain used to pass without the merge because the funnel left the
	// mirror parked on the publish branch — never a behaviour a user had, as
	// a real second invocation begins with CloneOrFetch's own reset to base.
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

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
	// Land the deprecation before reading its announcement off the working
	// tree — same reason as above: the funnel no longer leaves the mirror on
	// the write branch.
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
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
	// Land the retire before reading its event off the working tree: the
	// funnel restores the mirror to base, so an unmerged write is no longer
	// visible there (see the funnel's restoreTreeToBase).
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
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
// the new schema") is evaluated by the PRODUCT's own core —
// validate.CheckComputedCompatibility — never by a copy living here.
//
// It used to be a copy: a private compatFixtureValidates that compiled the
// schema with jsonschema/v6 directly. When this test was written that was
// the ONLY implementation of §5.4b anywhere in the repo, which is precisely
// what P37's F1 found — the rule the documents promised was a helper inside
// an e2e test file, reachable from no production path. P37 built the real
// core; leaving the copy here would have left two implementations of one
// rule free to diverge, which is the exact risk spec 37 §T2 cites P35's scar
// for. So this now asserts the shipped verdict, POL-007 and all.
func testE2E4MislabeledMinorFailsCompat(t *testing.T) {
	t.Parallel()
	root := repoRootForTest(t)

	readCompatCase := func(dir string) validate.CompatInput {
		t.Helper()
		schemaRaw, err := os.ReadFile(filepath.Join(root, "schemas/fixtures/compat", dir, "new.schema.json"))
		if err != nil {
			t.Fatalf("read %s/new.schema.json: %v", dir, err)
		}
		fixtureRaw, err := os.ReadFile(filepath.Join(root, "schemas/fixtures/compat", dir, "fixtures/valid/widget-1.json"))
		if err != nil {
			t.Fatalf("read %s fixture: %v", dir, err)
		}
		return validate.CompatInput{
			DeclaredBump:  "minor",
			PriorVersion:  "1.0.0",
			NewVersion:    "1.1.0",
			NewSchemas:    map[string][]byte{"schema/widget-1.schema.json": schemaRaw},
			PriorFixtures: map[string][]byte{"fixtures/valid/widget-1.json": fixtureRaw},
		}
	}

	additive := validate.CheckComputedCompatibility(readCompatCase("additive-minor"))
	if !additive.Computed {
		t.Fatalf("additive-minor: expected the check to be computed, got Reason=%q", additive.Reason)
	}
	if additive.Violation != nil {
		t.Fatalf("additive-minor: the v1.0.0-valid fixture must STILL validate against the new (minor-bumped) schema — genuinely additive, minor bump correct; got %+v", additive.Violation)
	}

	mislabeled := validate.CheckComputedCompatibility(readCompatCase("mislabeled-minor"))
	if mislabeled.Violation == nil || mislabeled.Violation.Code != "POL-007" {
		t.Fatalf("mislabeled-minor: a breaking change declared as a minor must be refused with POL-007 (CC-080); got %+v", mislabeled.Violation)
	}
	if len(mislabeled.Failures) != 1 || mislabeled.Failures[0].Fixture != "fixtures/valid/widget-1.json" {
		t.Fatalf("mislabeled-minor: the refusal must NAME the offending fixture (AC-970.1); got %+v", mislabeled.Failures)
	}
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
