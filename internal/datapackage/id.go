package datapackage

import (
	"io"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// Both this package's ids are the exchange-broadcast shape
// (<PREFIX>-<system>-<YYYYMMDD>-<rand4>): every attempt and every report is
// a new immutable document, never a standing artifact that accrues
// versions (spec 05a §T2.1, plan D-1). Both prefixes are closed and both
// live OUTSIDE the eight X- envelope types: a data package is deliberately
// not a ninth envelope type (CONFIRM-3), and internal/artifact's parse
// layer accepts any uppercase prefix, so an X- prefix here would silently
// route through an existing X-prefix dispatch table as one of the eight.
const (
	// PackagePrefix is data-package/v1's id prefix. NOT "XD-": that is the
	// live, closed prefix for `decision` (the normative registry,
	// envelope/v2's id pattern, both mint tables, both prefix-dispatch
	// tables, and the dashboard's TypeBadge — verified against the tree,
	// not assumed; plan D-1). A package minted with XD- would not error at
	// mint or parse time — it would silently route as a decision.
	PackagePrefix = "DP"

	// ReportPrefix is verification-report/v1's id prefix.
	//
	// DEVIATION (recorded here and in the schema, loudly, because a later
	// wave's minting must agree with it): spec 05a names DP- for the
	// package (§T2.1, plan D-1) but leaves the report's own id shape
	// unspecified beyond "report identity" (§T2.3). An unconstrained id
	// would reopen exactly the collision class D-1 exists to close for
	// packages, so this wave closes it the same way: a second closed,
	// non-X, non-DP prefix, the same exchange-broadcast shape. W2's
	// `verify` command mints against this constant.
	ReportPrefix = "VR"

	// BlobPrefix is P10 (agent-exchange-2026-08)'s blob-attachment id
	// prefix — the third closed, non-X prefix in this file, the same
	// exchange-broadcast shape as PackagePrefix/ReportPrefix (spec
	// 10-blob-primitive.md §3 seam 1). "BL" was verified free before being
	// chosen, against every prefix/mint/dispatch table the tree actually
	// has, not assumed: the envelope id patterns
	// (schemas/envelope/v2/base.schema.json's closed
	// `^(XC|XR)-...$|^(XQ|XW|XD|XH|XA|XS)-...$`) admit only the eight X-
	// prefixes and would refuse "BL-..." outright rather than silently
	// mis-route it; DP/VR's own schema patterns
	// (schemas/data-package/v1, schemas/verification-report/v1) name only
	// themselves; internal/cli's and internal/mcp's `switch parsed.Prefix`
	// dispatch tables (cmd_lifecycle.go, eventdoc.go) list only the eight
	// X- prefixes and have no default arm a "BL" could fall into; and the
	// dashboard's TypeBadge prefix map (web/src/components/TypeBadge.astro)
	// enumerates only the eight envelope types, not DP/VR either — so a
	// blob following their precedent introduces no third-registry
	// collision. A data-package/v1 or verification-report/v1 document
	// minted with "BL-" would not error at mint or parse time either — it
	// would silently be treated as the other kind — which is exactly why
	// this is a distinct constant rather than a shared one.
	BlobPrefix = "BL"
)

// MintPackageID mints a new data-package/v1 id for system.
func MintPackageID(system string) (string, error) {
	return artifact.MintExchangeID(PackagePrefix, system)
}

// MintPackageIDAt is MintPackageID's testable variant: the caller supplies
// the timestamp (converted to UTC) and the entropy source.
func MintPackageIDAt(system string, at time.Time, entropy io.Reader) (string, error) {
	return artifact.MintExchangeIDAt(PackagePrefix, system, at, entropy)
}

// ParsePackageID parses id and confirms it is exactly the DP- exchange-
// broadcast shape — never merely artifact-well-formed. A string that
// parses under internal/artifact's general grammar but carries a different
// prefix or class (for example a standing id, which no mint path here
// produces but a hand-authored or corrupted string could) is refused
// rather than silently accepted and mis-routed downstream.
func ParsePackageID(id string) (artifact.ID, error) {
	return parsePrefixedExchangeID("ParsePackageID", PackagePrefix, id)
}

// MintReportID mints a new verification-report/v1 id for system.
func MintReportID(system string) (string, error) {
	return artifact.MintExchangeID(ReportPrefix, system)
}

// MintReportIDAt is MintReportID's testable variant.
func MintReportIDAt(system string, at time.Time, entropy io.Reader) (string, error) {
	return artifact.MintExchangeIDAt(ReportPrefix, system, at, entropy)
}

// ParseReportID parses id and confirms it is exactly the VR- exchange-
// broadcast shape.
func ParseReportID(id string) (artifact.ID, error) {
	return parsePrefixedExchangeID("ParseReportID", ReportPrefix, id)
}

// MintBlobID mints a new blob-attachment id for system (spec
// 10-blob-primitive.md §3 seam 1).
func MintBlobID(system string) (string, error) {
	return artifact.MintExchangeID(BlobPrefix, system)
}

// MintBlobIDAt is MintBlobID's testable variant: the caller supplies the
// timestamp (converted to UTC) and the entropy source.
func MintBlobIDAt(system string, at time.Time, entropy io.Reader) (string, error) {
	return artifact.MintExchangeIDAt(BlobPrefix, system, at, entropy)
}

// ParseBlobID parses id and confirms it is exactly the BL- exchange-
// broadcast shape — never merely artifact-well-formed, mirroring
// ParsePackageID/ParseReportID's own discipline above.
func ParseBlobID(id string) (artifact.ID, error) {
	return parsePrefixedExchangeID("ParseBlobID", BlobPrefix, id)
}

func parsePrefixedExchangeID(op, prefix, id string) (artifact.ID, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return artifact.ID{}, err
	}
	if parsed.Prefix != prefix || parsed.Class != artifact.ClassExchangeBroadcast {
		return artifact.ID{}, &artifact.Error{Op: op, Input: id, Err: artifact.ErrMalformedID}
	}
	return parsed, nil
}
