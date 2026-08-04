package cache

import (
	"encoding/json"
	"path"
	"path/filepath"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// This file implements delivery.go's own PackageResolver seam over one
// connected space's mirror — the concrete, filesystem-backed resolver that
// file's own doc comment deferred to "a later wave", now that the
// space-side write layout is fixed (internal/space/data_delivery.go's
// dataPackageDir/dataPackageManifestPath/dataPackageReportPath, the ONLY
// writer of a package's manifest.json and a report's report.json).
//
// Those three path helpers are unexported to internal/space, and
// internal/cache may not reach across that boundary to call them (ADR-001:
// cache imports space, never the reverse, and unexported symbols do not
// cross package boundaries at all regardless of direction). This file
// duplicates only their PATH SHAPE — never the write/read mechanics — the
// same "one small local copy, not a shared export" idiom mirror.go's own
// runGitOutput doc comment already uses for internal/space's git plumbing
// one file over.
//
// Reads go straight to the mirror's checked-out working directory
// (SpaceMirror.Dir), exactly like every OTHER document this package
// decodes (mirror.go's decodeArtifactFile/decodeEventFile) — never a
// second git-blob-at-commit resolution path
// (internal/space.ResolveDataPackage's own convention, reserved for that
// package's own callers, which need immunity from the mirror's current
// checkout branch at write time). This package's read model already
// accepts "whatever the mirror is synced to" for every other artifact and
// event it reads; a package manifest/report is held to that same bar, not
// a stricter one.

// dataPackageDir mirrors internal/space's own unexported helper of the
// same name (data_delivery.go) — DeliverDataPackage/RecordVerificationReport
// are the only writers, and this file is the only reader on this side of
// the ADR-001 import boundary; the two must agree on the shape, never
// invent a second one.
func dataPackageDir(system, packageID string) string {
	return path.Join(system, "data", packageID)
}

// dataPackageManifestPath mirrors internal/space's own unexported helper of
// the same name.
func dataPackageManifestPath(system, packageID string) string {
	return path.Join(dataPackageDir(system, packageID), "manifest.json")
}

// dataPackageReportPath mirrors internal/space's own unexported helper of
// the same name. Its `system` argument is the VERIFYING (consumer) system,
// never the package's own producer — see mirrorPackageResolver's own doc
// comment below for why that forces ResolveReport to search, not compute,
// its path.
func dataPackageReportPath(system, packageID string) string {
	return path.Join(dataPackageDir(system, packageID), "report.json")
}

// The literal `schema` values a data-package/v1 manifest and a
// verification-report/v1 report carry (datapackage's own pack.go literal
// "data-package/v1"; verification-report/v1's own reportSchema constant is
// unexported to internal/datapackage, so this file names the literal
// directly rather than importing an unexported symbol). A decoded document
// whose schema field does not match is treated exactly like one that
// failed to decode at all — never a panic, never a partial document.
const (
	dataPackageSchema            = "data-package/v1"
	dataVerificationReportSchema = "verification-report/v1"
)

// mirrorPackageResolver implements PackageResolver (delivery.go) over one
// connected space's mirror directory. dir is the mirror's checked-out
// working tree root (SpaceMirror.Dir); participants is that space's
// manifest's own participant system list.
//
// participants exists because a manifest's own producer (encoded in the
// DP- id itself, spec 05a §T2.1) and a report's own VERIFYING system are
// NOT the same field: RecordVerificationReport roots report.json under the
// CONSUMER's own section (data_delivery.go's dataPackageReportPath doc
// comment records this at length as a deliberate DEVIATION from "beside
// the package it judges") — a field ResolvePackage's own packageID cannot
// name. ResolveReport below therefore tries every participant's own
// section, in a fixed deterministic (sorted, deduplicated) order, and
// returns the first section whose report.json exists and names packageID.
// This is a bounded search — len(participants) is a handful of manifest
// rows, never attacker-controlled or proportional to any corpus size — not
// an unbounded scan; the FIRST match wins, so if more than one participant
// ever records a report for the same package, this resolver is not the
// place that tie-break policy would need to live (out of this wave's
// scope: nothing in this codebase mints more than one report per package
// today).
type mirrorPackageResolver struct {
	dir          string
	participants []string
}

// newMirrorPackageResolver builds a PackageResolver over one space's
// mirror. participants is sorted and deduplicated once here so
// ResolveReport's own walk is deterministic regardless of the manifest's
// own participant row order.
func newMirrorPackageResolver(dir string, participants []string) *mirrorPackageResolver {
	sorted := append([]string(nil), participants...)
	sort.Strings(sorted)
	deduped := sorted[:0]
	seen := make(map[string]bool, len(sorted))
	for _, p := range sorted {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		deduped = append(deduped, p)
	}
	return &mirrorPackageResolver{dir: dir, participants: deduped}
}

var _ PackageResolver = (*mirrorPackageResolver)(nil)

// ResolvePackage reads ref's own data-package/v1 manifest. The producing
// system is parsed out of ref itself (DP-<system>-<YYYYMMDD>-<rand4>,
// datapackage.ParsePackageID) — never supplied separately, never
// guessable from anywhere else. A ref that is not exactly the DP-
// exchange-broadcast shape, a manifest that cannot be read, one that does
// not decode, one whose schema literal is wrong, or one that names a
// different id than ref: all resolve to "not found" — never a partial
// Document and never a panic (top-level brief's own guard, named after
// ContractVersionDetail's blanking resolver as the shape of defect NOT to
// repeat here).
func (r *mirrorPackageResolver) ResolvePackage(ref string) (datapackage.Document, bool) {
	parsed, err := datapackage.ParsePackageID(ref)
	if err != nil {
		return datapackage.Document{}, false
	}
	raw, ok := r.readBoundedRelative(dataPackageManifestPath(parsed.System, ref))
	if !ok {
		return datapackage.Document{}, false
	}
	var doc datapackage.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return datapackage.Document{}, false
	}
	if doc.Schema != dataPackageSchema || doc.ID != ref {
		return datapackage.Document{}, false
	}
	return doc, true
}

// ResolveReport reads packageID's own verification-report/v1, if any
// participant in this space has recorded one — see this type's own doc
// comment for why more than one section must be tried, and in what order.
// false means "not yet verified" — never treated as failed or passed
// (DeliveryVerdictUnverified's own guarantee, delivery.go).
func (r *mirrorPackageResolver) ResolveReport(packageID string) (datapackage.Report, bool) {
	for _, system := range r.participants {
		raw, ok := r.readBoundedRelative(dataPackageReportPath(system, packageID))
		if !ok {
			continue
		}
		var report datapackage.Report
		if err := json.Unmarshal(raw, &report); err != nil {
			continue
		}
		if report.Schema != dataVerificationReportSchema || report.Package.ID != packageID {
			continue
		}
		return report, true
	}
	return datapackage.Report{}, false
}

// maxDataPackageDocumentBytes bounds a single manifest.json/report.json
// read. Deliberately NOT mirror.go's own maxCacheReadBytes (1 MiB, sized
// for an envelope or an event): a verification-report/v1 document scales
// with the number of checks AND violations the judged dataset produced —
// internal/space's own maxDataPackageEntries caps a package at 2048
// entries, and each failing entry can carry its own bounded-but-nonzero
// violations[] (report.go's Check.Violations) — so a 1 MiB cap on THIS
// document class could truncate a real recorded FAIL into an unreadable
// file, which ResolveReport would then report as "not yet verified"
// (DeliveryVerdictUnverified) rather than "failed". That is the exact
// defect class the top-level brief names ("a missing report rounded down
// to a settled state") reached through a different door — a bound that is
// too small for the judgment it is supposed to preserve. This mirrors
// internal/space's own maxDataPackageEntryBytes (the bound the brief names
// explicitly), the number this package's read of the SAME document class
// should agree with, not mirror.go's unrelated small-document bound.
const maxDataPackageDocumentBytes = 128 << 20

// readBoundedRelative reads one mirror-relative path (forward-slash,
// space-relative) under r.dir, bounded by maxDataPackageDocumentBytes
// above (rails: bounded reads everywhere, but bounded to the actual
// document class being read, never an unrelated smaller cap that would
// silently misreport a real verdict as unknown).
func (r *mirrorPackageResolver) readBoundedRelative(relative string) ([]byte, bool) {
	raw, err := readBounded(filepath.Join(r.dir, filepath.FromSlash(relative)), maxDataPackageDocumentBytes)
	if err != nil {
		return nil, false
	}
	return raw, true
}
