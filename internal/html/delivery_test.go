package html

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// loadDeliveryFixture decodes testdata/delivery_fixture.json — the exact
// snake_case wire shape internal/cache.Delivery marshals to, so this test
// exercises the boundary a later wave will actually cross (cache.Delivery
// in, html.Delivery out) rather than a shape invented for this file alone.
func loadDeliveryFixture(t *testing.T) []cache.Delivery {
	t.Helper()
	raw, err := os.ReadFile("testdata/delivery_fixture.json")
	if err != nil {
		t.Fatalf("read testdata/delivery_fixture.json: %v", err)
	}
	var deliveries []cache.Delivery
	if err := json.Unmarshal(raw, &deliveries); err != nil {
		t.Fatalf("decode testdata/delivery_fixture.json into []cache.Delivery: %v", err)
	}
	return deliveries
}

// TestProjectDeliveries_FixtureRendersAC7 is spec 05a AC-7's own render
// test: "render the dashboard from a fixture space with one failed and one
// accepted delivery." The fixture carries two attempts of the SAME
// logical delivery (attempt 1 failed, attempt 2 — which supersedes it —
// passed), plus an unverified delivery and an unresolved one, so every
// state AC-7 and the top-level brief name is asserted from one render.
func TestProjectDeliveries_FixtureRendersAC7(t *testing.T) {
	t.Parallel()
	got := ProjectDeliveries(loadDeliveryFixture(t))

	if len(got) != 4 {
		t.Fatalf("want all 4 deliveries visible under their handoffs, got %d: %+v", len(got), got)
	}

	failed, passed, unverified, unresolved := got[0], got[1], got[2], got[3]

	// --- the failed delivery names its entry and rule ---
	if failed.Resolution != DeliveryResolved {
		t.Fatalf("failed delivery: want resolution=resolved, got %q", failed.Resolution)
	}
	if failed.Verdict != DeliveryVerdictFailed {
		t.Fatalf("failed delivery: want verdict=failed, got %q", failed.Verdict)
	}
	if len(failed.Failures) != 1 {
		t.Fatalf("failed delivery: want exactly 1 named failure, got %+v", failed.Failures)
	}
	f := failed.Failures[0]
	if f.EntryPath != "dataset/records.ndjson" || f.Rule != "chk-schema" {
		t.Fatalf("failed delivery: want the failing entry+rule named, got %+v", f)
	}
	if f.Record == nil || *f.Record != 4108 {
		t.Fatalf("failed delivery: want the ndjson record number named, got %v", f.Record)
	}
	if failed.PackageID != "DP-beta-20260101-aaaa" || failed.Attempt != 1 {
		t.Fatalf("failed delivery: want package id + attempt 1, got %+v", failed)
	}

	// --- the accepted delivery carries the supersede chain back to attempt 1 ---
	if passed.Verdict != DeliveryVerdictPassed {
		t.Fatalf("accepted delivery: want verdict=passed, got %q", passed.Verdict)
	}
	if passed.PackageID != "DP-beta-20260102-bbbb" || passed.Attempt != 2 {
		t.Fatalf("accepted delivery: want package id + attempt 2, got %+v", passed)
	}
	if passed.Supersedes != "DP-beta-20260101-aaaa" {
		t.Fatalf("accepted delivery: want Supersedes naming attempt 1, got %q", passed.Supersedes)
	}
	if len(passed.Chain) != 2 {
		t.Fatalf("accepted delivery: want a 2-entry supersede chain back to the first attempt, got %+v", passed.Chain)
	}
	if passed.Chain[0].PackageID != failed.PackageID || passed.Chain[0].Verdict != DeliveryVerdictFailed {
		t.Fatalf("accepted delivery: want chain[0] to be the first (failed) attempt, got %+v", passed.Chain[0])
	}
	if passed.Chain[1].PackageID != passed.PackageID || passed.Chain[1].Verdict != DeliveryVerdictPassed {
		t.Fatalf("accepted delivery: want chain[1] to be this (passed) attempt, got %+v", passed.Chain[1])
	}

	// --- "not yet verified" must be visibly distinct from "passed" ---
	if unverified.Verdict != DeliveryVerdictUnverified {
		t.Fatalf("unverified delivery: want verdict=unverified, got %q", unverified.Verdict)
	}
	if unverified.Verdict == DeliveryVerdictPassed {
		t.Fatal("unverified delivery must never equal DeliveryVerdictPassed")
	}
	if unverified.Verdict == "" {
		t.Fatal("unverified delivery must not render as the empty string — absent is unknown, never good")
	}

	// --- a delivery whose manifest cannot be resolved renders as unresolved, never vanishes ---
	if unresolved.Resolution != DeliveryUnresolved {
		t.Fatalf("unresolved delivery: want resolution=unresolved, got %q", unresolved.Resolution)
	}
	if unresolved.Unavailable == "" {
		t.Fatal("unresolved delivery must carry a non-empty reason")
	}
	if unresolved.HandoffID == "" || unresolved.Ref == "" {
		t.Fatalf("unresolved delivery must still carry its own identity, got %+v", unresolved)
	}
	if unresolved.Chain == nil {
		t.Fatal("unresolved delivery's Chain must be a non-nil empty slice, never omitted")
	}
	if unresolved.Verdict != "" {
		t.Fatalf("unresolved delivery has no verdict to report, got %q", unresolved.Verdict)
	}
}

// TestProjectVerdict_EveryCacheStateHasAnExplicitBranch guards
// projectVerdict directly: every cache.DeliveryVerdictStatus this project
// defines must map to its OWN non-empty, non-"passed" spelling — the
// single boundary that would let "not yet verified" round down to
// "passed" if it silently fell through to a shared default.
func TestProjectVerdict_EveryCacheStateHasAnExplicitBranch(t *testing.T) {
	t.Parallel()
	cases := map[cache.DeliveryVerdictStatus]DeliveryVerdict{
		cache.DeliveryVerdictPass:       DeliveryVerdictPassed,
		cache.DeliveryVerdictFail:       DeliveryVerdictFailed,
		cache.DeliveryVerdictError:      DeliveryVerdictErrored,
		cache.DeliveryVerdictUnverified: DeliveryVerdictUnverified,
	}
	for in, want := range cases {
		if got := projectVerdict(in); got != want {
			t.Errorf("projectVerdict(%q) = %q, want %q", in, got, want)
		}
	}
	if projectVerdict(cache.DeliveryVerdictUnverified) == DeliveryVerdictPassed {
		t.Fatal("unverified must never project to passed")
	}
}
