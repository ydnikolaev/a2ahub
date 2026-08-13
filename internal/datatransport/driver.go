// Package datatransport is the transport-driver SEAM spec 05a AC-8 asks for
// (docs/features/active/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md
// §4 AC-8, as narrowed by the 2026-08-13 amendment "AC-8 asked for something
// v1 cannot give"): the Go half only — interface, registry, conformance
// suite, null driver — with the wire half deliberately deferred to
// data-package/v2. `data-package/v1`'s `transport_driver` is a closed enum
// and its own schema file is frozen in schemas/published-v1.sha256 (§9.3,
// "the one irreversible choice"), so a second driver cannot validate
// against that schema today; the enum opens only when v2 is minted together
// with a real second driver's requirements.
//
// The Driver interface below is derived from what internal/space's
// space-git transport ACTUALLY does — read before this file was written,
// not imagined:
//
//   - internal/datapackage/manifest.go's TransportDriverSpaceGit constant is
//     the manifest's own transport_driver value, written at exactly one
//     site (pack.go's Pack) and read by nobody: internal/datapackage
//     performs no transport I/O today, and this package must not make it
//     start — it is not imported here for anything but its refusal
//     sentinel (ErrUnsafeLocator).
//   - internal/space/data_delivery.go's dataPackageDir(system, packageID)
//     is the exact locator grammar space-git produces: "the package root
//     relative to the space" — spec 05a §T2.1's own words for the
//     `locator` field. DeliverDataPackage is the one writer of a package's
//     bytes to that location; data_resolve.go's ResolveDataPackage is the
//     one reader back.
//
// This package does NOT import internal/space: that would invert the
// dependency (space would need to depend on this seam, not vice versa).
//
// # READ THIS BEFORE TREATING THIS PACKAGE AS THE ANSWER
//
// **AC-8 IS NOT SATISFIED BY THIS PACKAGE, AND THE INTERFACE BELOW CANNOT
// EXPRESS space-git — THE ONLY TRANSPORT THAT EXISTS.** The seam is written,
// tested and mutation-checked; it is also, as drafted, a model of an
// in-memory store rather than of a transport with a publish boundary. Spec
// 05a §9.3 accepts an irreversible choice on the grounds that "the seam is
// not decoration", so a seam the real driver cannot fill is exactly the
// outcome that must not be quietly shipped as done.
//
// Three gaps, each verified against internal/space before this was written
// and re-verified by the lead:
//
//  1. Put(ctx, locator, files) cannot carry what a space-git write needs.
//     DeliverDataPackage requires a *WriteFunnel AND mandatory HandoffID /
//     HandoffRaw / EventID / EventRaw — the payload, the handoff envelope
//     and its first lifecycle event land in ONE commit, and that atomicity
//     is the point of the design, not an implementation detail.
//  2. Space-git's idempotency is not Put's. It comes from
//     dataDeliverOperationKey plus the funnel's own already-open-PR
//     short-circuit; a re-run mints fresh handoff bytes and the funnel
//     repairs the same pull request. Driver has no operation-key concept.
//  3. Put and Get are ASYMMETRIC by nature. DeliverDataPackage writes
//     through the funnel (a pull request); ResolveDataPackage reads
//     contractAuthoritativeMainRef — origin/main, AFTER merge. The
//     immediate round-trip RunConformance asserts is true for an in-memory
//     driver and false for space-git until the PR merges.
//
// Gap 3 is the one that shapes the redesign rather than the adapter: a
// publish boundary is a property every plausible second driver has (object
// storage with eventual consistency, deletable release assets), so
// visibility probably belongs IN the interface — Put returning a receipt,
// with visibility a separate question — instead of being assumed away.
// Gap 1 asks a real question too: is co-committing the handoff a TRANSPORT
// concern or the space's protocol concern? Deciding that decides whether
// Driver stays this narrow.
//
// The fork is the operator's and is recorded in spec 05a's Amendments.
// Until it is taken, nothing imports this package and AC-8 stays open.
package datatransport

import (
	"context"
	"errors"
)

// Driver is the seam a transport implementation fills — deliberately
// narrow: only what is actually transport-specific, derived from
// space-git's own three responsibilities (see the package doc comment).
// Nothing about manifest shape, digest verification or entry conformance
// belongs here; internal/datapackage owns all of that already and stays
// pure — a Driver never reads the manifest itself, only the locator naming
// where its bytes live.
type Driver interface {
	// Name is this driver's own identity — the manifest's
	// transport_driver value (e.g. datapackage.TransportDriverSpaceGit).
	// Non-empty, and stable: the same value on every call.
	Name() string

	// Locate produces the locator this driver would place a package
	// identified by (system, packageID) at. For space-git this is exactly
	// dataPackageDir(system, packageID): the package root relative to the
	// space (spec 05a §T2.1's own words for `locator`).
	//
	// system is the SECTION the write commits into (funnel.go's
	// sectionOK), not necessarily the id embedded in packageID: space-git's
	// own dataPackageReportPath roots a verifying (consumer) system's
	// report.json under that consumer's own section while packageID still
	// names the producer, so system and the id's embedded system may
	// legitimately differ. A future signature that dropped system and
	// derived it from packageID alone would break that case.
	Locate(system, packageID string) (string, error)

	// AcceptsLocator reports whether locator is one this driver will
	// operate on: syntactically valid for this driver's own grammar AND
	// safe. A locator shaped like a credential, a token, or an absolute
	// local path — anything artifact.CleanRelativePath already refuses —
	// must report false here (spec 05a §T2.4's locator refusal, reused by
	// every driver rather than restated per-driver).
	AcceptsLocator(locator string) bool

	// Put moves a package's payload bytes to locator, replacing whatever
	// was previously there under the same locator — a repeated Put of the
	// same or a changed file set is idempotent, never a second copy next
	// to the first. Put refuses with an error wrapping
	// datapackage.ErrUnsafeLocator when !AcceptsLocator(locator).
	Put(ctx context.Context, locator string, files map[string][]byte) error

	// Get retrieves the bytes a prior Put placed at locator, byte-identical
	// to what was put.
	Get(ctx context.Context, locator string) (map[string][]byte, error)
}

var (
	// ErrDuplicateDriver refuses registering a second driver under a name
	// already registered — a driver never silently replaces another.
	ErrDuplicateDriver = errors.New("datatransport: a driver is already registered under this name")

	// ErrUnknownDriver refuses looking up a name nothing was registered
	// under.
	ErrUnknownDriver = errors.New("datatransport: no driver is registered under this name")
)
