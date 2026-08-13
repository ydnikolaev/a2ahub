package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/datatransport"
)

// TestSpaceGitDriverConformance runs the shared driver conformance suite
// (spec 05a AC-8) against spaceGitDriver, the way the brief's own
// 2026-08-13 amendment names it: "What remains before this row is ticked
// end to end is the space-git Driver itself."
//
// makeVisible's replace-not-merge behaviour is a DEVIATION worth reading
// before this test's green is trusted at face value — see its own doc
// comment for the empirical proof (watched red first) and why the gap it
// papers over is unreachable in production.
func TestSpaceGitDriverConformance(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	driver := newSpaceGitDriver(repo)

	datatransport.RunConformance(t, datatransport.Harness{
		Driver:      driver,
		MakeVisible: spaceGitConformanceMakeVisible(t, repo),
	})
}

// spaceGitConformanceMakeVisible commits inSpace into repo's origin/main —
// the same "write files, git add, commit, push" idiom
// commitDataPackageFixture (data_resolve_test.go) already uses to build a
// data-package fixture, extended here to also CLEAR whatever previously sat
// under locator before writing inSpace's own batch (`git add -A`, not
// `git add --`, so a removed path is staged as a deletion).
//
// DEVIATION, reported rather than silently chosen: RunConformance's own
// "repeated Put of a DIFFERENT file set at the same locator" case asserts
// GET returns EXACTLY the second set, not a union of both — a generic
// replace-not-merge property of the Driver abstraction (real for a future
// driver that overwrites in place, e.g. object storage). Put's own new doc
// comment (driver.go) DROPS the old "replacing whatever was previously
// there" sentence — replacement is no longer the DRIVER's contract, it is a
// property of whoever commits inSpace — yet the suite still asserts it
// against the driver as a whole. Watched empirically before this file
// existed in its current form: a PLAIN ADDITIVE makeVisible (write inSpace,
// `git add -A -- <locator>` restricted to inSpace's own new paths only, no
// prior removal) reproduces the exact production write shape
// (DeliverDataPackage's own submit.Files, which never deletes) and reds with
// "Get() returned 2 entries [manifest.json payload/orders.json], want 1
// entries [manifest.json]" — because the stale payload/orders.json from the
// FIRST Put survives underneath the SECOND Put's manifest-only batch.
//
// That red names something real about space-git as ACTUALLY WIRED
// (DeliverDataPackage commits via submit.Files, never Mutations/
// MutationDelete) but not something reachable in production: PackageID is
// immutable per attempt (spec 05a §T2.1), dataDeliverOperationKey
// (data_delivery.go) is minted from PackageID alone so a retry collides on
// IDENTICAL bytes rather than different ones, and §9.3 keeps every attempt —
// under its OWN packageID/locator — forever. A different file set at the
// SAME locator is therefore unreachable by construction: no real caller of
// spaceGitDriver.Put ever exercises the branch this harness's replace
// behaviour stands in for. This harness makes the fixture hold "exactly the
// last batch" — which is what a write-once locator holds trivially in
// production — using an ordinary git operation (`git add -A`, staging a
// removal) that is real Git capability, not an invented one; it is stricter
// than DeliverDataPackage's own additive writer, never looser, and the gap
// between the two is exactly this comment.
func spaceGitConformanceMakeVisible(t *testing.T, repo string) func(ctx context.Context, locator string, inSpace map[string][]byte) error {
	t.Helper()
	return func(ctx context.Context, locator string, inSpace map[string][]byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(inSpace) == 0 {
			// A driver that performs its own I/O (this one never does, but
			// the harness contract is generic) has nothing for this hook to
			// commit — matching Harness.MakeVisible's own doc comment.
			return nil
		}
		full := filepath.Join(repo, filepath.FromSlash(locator))
		if err := os.RemoveAll(full); err != nil {
			return err
		}
		for path, raw := range inSpace {
			writeContractCandidateBytes(t, repo, path, raw)
		}
		contractTestGit(t, repo, "add", "-A", "--", locator)
		contractTestGit(t, repo, "commit", "-q", "-m", "make visible: "+locator)
		contractTestGit(t, repo, "push", "-q", "origin", "main")
		return nil
	}
}

func TestSpaceGitDriverName(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver("")
	if got := d.Name(); got != "space-git" {
		t.Fatalf("Name() = %q, want %q", got, "space-git")
	}
}

func TestSpaceGitDriverLocateMirrorsDataPackageDir(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver("")
	got, err := d.Locate("axon", "DP-axon-20260101-abcd")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if want := dataPackageDir("axon", "DP-axon-20260101-abcd"); got != want {
		t.Fatalf("Locate() = %q, want %q", got, want)
	}
}

func TestSpaceGitDriverLocateRefusesEmptyArgs(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver("")
	for _, tc := range []struct{ system, packageID string }{
		{"", "DP-axon-20260101-abcd"},
		{"axon", ""},
		{"", ""},
	} {
		if _, err := d.Locate(tc.system, tc.packageID); err == nil {
			t.Fatalf("Locate(%q, %q) = nil error, want a refusal", tc.system, tc.packageID)
		}
	}
}

// TestSpaceGitDriverPutIsPure proves Put performs no I/O of its own: it is
// called with a mirrorDir that does not exist on disk at all, and still
// succeeds — the seeded-red receipt is
// TestSpaceGitDriverConformance's own round trip, which WOULD fail if Put
// silently depended on a real git checkout.
func TestSpaceGitDriverPutIsPure(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver(filepath.Join(t.TempDir(), "does-not-exist"))
	locator := dataPackageDir("axon", "DP-axon-20260101-abcd")
	files := map[string][]byte{
		"manifest.json": []byte(`{}`),
		"orders.json":   []byte(`[]`),
	}
	got, err := d.Put(context.Background(), locator, files)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := map[string][]byte{
		locator + "/manifest.json": []byte(`{}`),
		locator + "/orders.json":   []byte(`[]`),
	}
	if len(got) != len(want) {
		t.Fatalf("Put() = %v, want %v", got, want)
	}
	for k, v := range want {
		if string(got[k]) != string(v) {
			t.Fatalf("Put()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestSpaceGitDriverPutRefusesUnsafeEntryPath(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver("")
	locator := dataPackageDir("axon", "DP-axon-20260101-abcd")
	_, err := d.Put(context.Background(), locator, map[string][]byte{"../escape.json": []byte("{}")})
	if !errors.Is(err, datatransport.ErrUnsafeLocator) {
		t.Fatalf("Put(unsafe entry) error = %v, want an error wrapping %v", err, datatransport.ErrUnsafeLocator)
	}
}

func TestSpaceGitDriverAcceptsLocatorRefusesCredentialShaped(t *testing.T) {
	t.Parallel()
	d := newSpaceGitDriver("")
	// The exact vector RunConformance's own unsafe-locator table drives:
	// no ".." segment, not absolute, already in path.Clean form — so
	// isCleanRelativePath (funnel.go's own section-guard predicate) alone
	// would ACCEPT it; artifact.CleanRelativePath's ASCII whitelist is what
	// actually refuses it. See AcceptsLocator's own doc comment.
	if d.AcceptsLocator("user@host:22/axon") {
		t.Fatalf("AcceptsLocator(credential-shaped) = true, want false")
	}
}
