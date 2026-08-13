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
// # THE RESPONSIBILITY SPLIT, WHICH THE SPEC ALREADY DECIDED
//
// A Driver moves a package's BYTES to a locator and back. That is all. The
// spec settled the rest before this package existed, and reading §9.1 is what
// corrected a first draft of this file that had tried to model the whole
// delivery:
//
//   - §9.1 CONFIRM-3: "Delivery is a `handoff` carrying a package; the fold
//     table is untouched." The handoff, its lifecycle event and the
//     one-commit atomicity are PROTOCOL. `internal/space`'s
//     DeliverDataPackage owns them and CALLS a driver for the payload half —
//     it is not itself a driver, and an adapter that tried to wrap it would
//     be modelling the wrong seam.
//   - AC-3 puts "payload, manifest and handoff in exactly one commit" and
//     idempotent re-runs on the COMMAND. Space-git's idempotency comes from
//     dataDeliverOperationKey and the write funnel's already-open-PR
//     short-circuit, one layer above any driver.
//   - §0's loop sequences VISIBILITY: B delivers, a handoff appears on the
//     thread, A fetches. A driver therefore owes byte-identity, never
//     read-immediately-after-write. Harness.MakeVisible is where each
//     transport says how its bytes become readable; see conformance.go.
//
// What is left for a Driver is genuinely transport-specific and nothing else:
// its name, its locator grammar, and moving bytes.
package datatransport

import (
	"context"
	"errors"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
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

	// Put moves the bytes wherever this transport keeps them, and returns
	// any files that must be committed INTO THE SPACE as part of the same
	// write, keyed by space-relative path.
	//
	// A transport that lives in the space performs no I/O and returns them
	// all. A transport that lives elsewhere performs its own I/O and
	// returns nothing, or a single pointer file. Put refuses with an error
	// wrapping datapackage.ErrUnsafeLocator when !AcceptsLocator(locator).
	Put(ctx context.Context, locator string, files map[string][]byte) (inSpace map[string][]byte, err error)

	// Get retrieves the bytes a prior Put placed at locator, byte-identical
	// to what was put — ONCE THOSE BYTES ARE VISIBLE ON THIS TRANSPORT.
	// Visibility is not this method's contract: space-git writes through a
	// pull request and reads origin/main after merge, and the protocol
	// already sequences that (§0's loop). A driver with a publish boundary
	// returns an error until its write is published; Harness.MakeVisible is
	// how a driver's own package advances it in a test.
	Get(ctx context.Context, locator string) (map[string][]byte, error)
}

var (
	// ErrDuplicateDriver refuses registering a second driver under a name
	// already registered — a driver never silently replaces another.
	ErrDuplicateDriver = errors.New("datatransport: a driver is already registered under this name")

	// ErrUnknownDriver refuses looking up a name nothing was registered
	// under.
	ErrUnknownDriver = errors.New("datatransport: no driver is registered under this name")

	// ErrUnsafeLocator re-exports datapackage.ErrUnsafeLocator so a Driver
	// living in a package ADR-001 (docs/decisions.md) forbids from importing
	// internal/datapackage directly — internal/space, see its own
	// data_transport.go — can still wrap the EXACT sentinel this package's
	// own conformance suite checks with errors.Is. A locally duplicated
	// `errors.New` with the same text would fail that check: errors.Is
	// compares by value identity, never by message. This package already
	// imports internal/datapackage (see the package doc comment above), so
	// re-exporting the value here costs nothing a duplicate would still owe.
	ErrUnsafeLocator = datapackage.ErrUnsafeLocator
)
