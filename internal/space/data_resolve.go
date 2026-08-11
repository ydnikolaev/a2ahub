package space

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// This file resolves the two things the contract data exchange loop (spec
// 05a) needs out of the space's local mirror, keeping both space-side: the
// contract's own digest-verified carried schemas (for the EntryChecker) and
// one delivered data package's bytes (for fetch and verify). Neither result
// is a datapackage.* type — internal/space may not import
// internal/datapackage (ADR-001's table), so both are handed back as plain
// bytes/maps and it is cmd/a2a's job (which may import everything) to decode
// them into datapackage.Document/LocalFile and drive the pure core.

var (
	// ErrDataContractNoSchemas refuses a pinned contract version that
	// declares no schema entries at all — nothing an EntryChecker built
	// from it could ever check a dataset entry against.
	ErrDataContractNoSchemas = errors.New("space: pinned contract version declares no schema entries")

	// ErrDataPackageInvalidReference refuses a package id that is not
	// exactly the DP- exchange-broadcast shape (spec 05a §T2.1, plan D-1).
	ErrDataPackageInvalidReference = errors.New("space: data package reference must be an exact DP id")

	// ErrDataPackageNotFound refuses a package id with nothing committed
	// under the exact directory DeliverDataPackage would have written it to.
	ErrDataPackageNotFound = errors.New("space: data package is not present in the local mirror")

	// ErrDataContractMirrorStale refuses a pinned contract version this
	// packing checkout's own local mirror has not synced far enough to
	// see — distinct from ErrContractNotPublished, which means the
	// resolver's authoritative anchor already reaches PAST the requested
	// version and still finds no publish event for it (B42). Collapsing
	// the two into one sentence cost a real wave a run: `a2a data pack`
	// failed with "contract version is not published" for a contract
	// that WAS published and merged — the local mirror simply had not
	// synced.
	ErrDataContractMirrorStale = errors.New("space: contract version is not in the local mirror; run a2a sync")
)

// dataPackagePrefix mirrors datapackage.PackagePrefix ("DP") — internal/space
// may not import internal/datapackage (ADR-001), so this constant, and the
// small id-shape check below, duplicate only the three-character prefix
// string itself, never the mint/parse machinery
// (datapackage.ParsePackageID/MintPackageID) that owns it. DEVIATION,
// recorded because a shared constant is the more honest shape and was not
// reachable from this file's side of the import boundary.
const dataPackagePrefix = "DP"

// splitDataContractReference parses "<XC-id>@<canonical-version>" — the
// exact shape data-package/v1's own `contract` field pins before the digest
// suffix is appended (spec 05a §T2.1) — the same shape
// cmd/a2a's splitExactContractReference already parses for `contract check`/
// `contract materialize`, reimplemented here rather than imported because
// internal/space cannot import cmd/a2a (the dependency runs the other way).
func splitDataContractReference(ref string) (id, canonicalVersion, pinnedDigest string, err error) {
	// A pinned ref may carry the digest this package's own resolver emits:
	// "<XC-id>@<version>#<digest>" is exactly what T2.1 requires a manifest's
	// contract field to hold, so verify hands this function back the value
	// pack wrote. An earlier revision accepted only "<id>@<version>" and
	// refused the digest suffix it had itself produced, which made `data
	// verify` fail against every package `data pack` ever produced. The
	// suffix is now parsed AND checked by the caller, rather than tolerated:
	// re-resolving to different bytes than the producer pinned is the one
	// thing L-2 exists to make impossible.
	body, digest, hasDigest := strings.Cut(ref, "#")
	if hasDigest && digest == "" {
		return "", "", "", fmt.Errorf("%w: %q", ErrContractInvalidReference, ref)
	}
	id, requestedVersion, found := strings.Cut(body, "@")
	parsed, idErr := artifact.ParseID(id)
	canonical, versionErr := version.Canonical(requestedVersion)
	if !found || strings.Contains(requestedVersion, "@") || idErr != nil || parsed.Prefix != "XC" ||
		versionErr != nil || canonical != requestedVersion {
		return "", "", "", fmt.Errorf("%w: %q", ErrContractInvalidReference, ref)
	}
	return id, canonical, digest, nil
}

// ResolveDataContractSchemas resolves and digest-verifies EXACTLY ONE
// pinned contract version (spec 05a §T2.6, plan D-2: "the contract's carried
// set is resolved and digest-verified once per verify run, not once per
// entry") and returns every declared schema entry's bytes keyed by its own
// carried-set path — the exact map[string][]byte shape
// datapackage.NewEntryChecker takes, keyed by the conforms_to value a
// data-package entry declares.
//
// D-2's "once per run" property is a caller discipline this function cannot
// enforce by itself: ResolveContractVersion (which this function calls
// exactly once) already re-verifies the whole carried set internally
// (contract_history.go's readHistoricalContractSnapshot calls
// CarriedSet.VerifyDigest once), so the cost this guards against is a
// CALLER invoking ResolveDataContractSchemas itself more than once per run —
// which is why cmd/a2a's wiring holds this behind an injectable, once-
// counted seam rather than calling it inline per entry.
//
// pinnedRef is "<XC-id>@<version>#<digest>" — the exact value
// data-package/v1's own `contract` field pins (§T2.1).
func ResolveDataContractSchemas(ctx context.Context, mirrorDir, ref string, validator ContractHistoryDocumentValidator) (schemas map[string][]byte, pinnedRef string, err error) {
	id, canonicalVersion, pinnedDigest, err := splitDataContractReference(ref)
	if err != nil {
		return nil, "", err
	}
	snapshot, err := ResolveContractVersion(ctx, mirrorDir, id, canonicalVersion, validator)
	if err != nil {
		if errors.Is(err, ErrContractNotPublished) {
			return nil, "", diagnoseUnpublishedContract(ctx, mirrorDir, id, canonicalVersion)
		}
		return nil, "", err
	}
	if pinnedDigest != "" && pinnedDigest != snapshot.PublishedDigest {
		// The caller pinned a digest and the version resolves to different
		// bytes. Refuse rather than judge: a verdict produced against bytes
		// the producer did not agree to is worse than no verdict, because it
		// looks exactly like one that is true.
		return nil, "", fmt.Errorf("%w: %s@%s pins %s but resolves to %s",
			ErrContractHistoryInvalid, id, canonicalVersion, pinnedDigest, snapshot.PublishedDigest)
	}
	schemas = make(map[string][]byte, len(snapshot.CarriedSet.Entries))
	for _, entry := range snapshot.CarriedSet.Entries {
		if !dataContractSchemaEntry(snapshot.CarriedSet.Profile, entry) {
			continue
		}
		raw, ok := snapshot.CarriedSet.Bytes[entry.Path]
		if !ok {
			return nil, "", fmt.Errorf("%w: schema entry %q has no carried bytes", ErrContractHistoryInvalid, entry.Path)
		}
		schemas[entry.Path] = append([]byte(nil), raw...)
	}
	if len(schemas) == 0 {
		return nil, "", fmt.Errorf("%w: %s@%s", ErrDataContractNoSchemas, id, canonicalVersion)
	}
	return schemas, fmt.Sprintf("%s@%s#%s", id, canonicalVersion, snapshot.PublishedDigest), nil
}

// diagnoseUnpublishedContract distinguishes "this version is not in your
// local mirror; run a2a sync" from "this version was never published" once
// ResolveContractVersion has already reported ErrContractNotPublished
// (backlog B42, spec 12-closeout.md AC3). `a2a data pack` resolves the
// pinned contract out of the packing checkout's own local mirror and never
// over the network — correct for an offline-first tool — but that means an
// absent local copy and a genuinely unpublished version produced the exact
// same sentence, and only one of them was the user's fault: a run was lost
// to "contract version is not published" for a contract that WAS published
// and merged, because the mirror simply had not synced.
//
// It re-reads only the one file ResolveContractVersion would have read
// anyway — the contract's CURRENT descriptor at the same authoritative
// anchor (contractAuthoritativeMainRef) — on the error path only, and hands
// back ErrContractNotPublished ONLY with positive evidence the mirror has
// synced past the point where the requested version would appear: the
// descriptor exists, parses, names this exact contract, and its own
// version is not older than the one requested. Every other outcome —
// descriptor missing, unparseable, wrong id, or genuinely older than
// requested — means the mirror cannot prove the negative, so it must not
// claim "never published"; it names `a2a sync` instead. That also means a
// mirror still behind an upstream that skipped straight to a much later
// version (an honest "not synced" case) never gets misdiagnosed as having
// witnessed the requested version's absence.
func diagnoseUnpublishedContract(ctx context.Context, mirrorDir, id, canonicalVersion string) error {
	stale := fmt.Errorf("%w: %s@%s", ErrDataContractMirrorStale, id, canonicalVersion)

	parsedID, err := artifact.ParseID(id)
	if err != nil {
		return stale
	}
	layout, err := NewLayout(parsedID.System)
	if err != nil {
		return stale
	}
	commit, err := contractGitResolveCommit(ctx, mirrorDir, contractAuthoritativeMainRef)
	if err != nil {
		return stale
	}
	descriptorFile, err := contractGitReadPath(ctx, mirrorDir, commit, layout.ProvidesContract(parsedID.Slug), contract.MaxFileBytes)
	if err != nil {
		return stale
	}
	descriptor, err := parseHistoricalDescriptor(descriptorFile.Raw)
	if err != nil || descriptor.ID != id {
		return stale
	}
	older, err := version.OlderThan(descriptor.Version, canonicalVersion)
	if err != nil || older {
		return stale
	}
	// Positive evidence: the mirror's own current descriptor is already at
	// or past the requested version, and still no publish event named it.
	return fmt.Errorf("%w: %s@%s", ErrContractNotPublished, id, canonicalVersion)
}

// dataContractSchemaEntry reports whether one carried-set entry is a schema,
// by the same rule the shipped conformance core already uses
// (internal/contract's conformanceSchemas).
//
// The role field only carries meaning under the declared contract-set-v2
// profile; the legacy contract-tree-v1 builder never populates it, so a
// role-only test finds ZERO schemas in every legacy contract and reports
// the contract as having none. That is not a legacy edge case: it is every
// contract published before the v2 profile, and the two-party e2e found it
// by being unable to resolve one. Under the legacy profile the path prefix
// is the classifier, exactly as conformance does it.
func dataContractSchemaEntry(profile contract.DigestProfile, entry contract.Entry) bool {
	if profile == contract.ProfileContractSetV2 {
		return entry.Role == contract.RoleSchema
	}
	return strings.HasPrefix(entry.Path, "schema/")
}

// DataPackageBytes is one resolved data-package/v1 attempt's raw bytes:
// its manifest and every OTHER file committed alongside it, addressed
// package-root-relative — never decoded here (internal/space cannot import
// internal/datapackage). Root is the space-relative directory both this
// function and DeliverDataPackage agree a package lives under; a caller
// that needs it for provenance/debugging has it without recomputing it.
type DataPackageBytes struct {
	System      string
	Root        string
	ManifestRaw []byte
	Payload     map[string][]byte
}

// maxDataPackageEntries/maxDataPackageEntryBytes/maxDataPackagePackageBytes
// mirror datapackage.DefaultBounds()'s own numbers (bounds.go) — duplicated
// here, rather than imported, for the same ADR-001 reason dataPackagePrefix
// is: they exist only so this bounded git read cannot be handed an
// unbounded listing or an unbounded blob to hold resident before
// datapackage's OWN bounds ever get a chance to run at pack/fetch time.
const (
	maxDataPackageEntries      = 2049 // datapackage.DefaultBounds().MaxEntries (2048) + the manifest itself
	maxDataPackageEntryBytes   = 128 << 20
	maxDataPackagePackageBytes = 2 << 30
)

// dataPackagePrefixParsed reports whether packageID is exactly the DP-
// exchange-broadcast shape, mirroring datapackage.ParsePackageID's own
// check (id.go) without importing it.
func dataPackagePrefixParsed(packageID string) (artifact.ID, bool) {
	parsed, err := artifact.ParseID(packageID)
	if err != nil || parsed.Prefix != dataPackagePrefix || parsed.Class != artifact.ClassExchangeBroadcast {
		return artifact.ID{}, false
	}
	return parsed, true
}

// ResolveDataPackage resolves one DP- package's manifest and payload bytes
// out of the local mirror, reading from origin/main exactly the way
// ResolveContractVersion does (contractGitResolveCommit against
// contractAuthoritativeMainRef) rather than trusting the mirror's checked-
// out working tree, so a fetch/verify run sees the same authoritative bytes
// regardless of what branch the mirror happens to be parked on between
// writes (funnel.go's restoreTreeToBase doc explains why that can vary).
//
// The producing system is parsed out of packageID itself (spec 05a §T2.1:
// DP-<system>-<YYYYMMDD>-<rand4>) — a caller never has to supply it
// separately, and cannot supply a different one than the id itself names.
func ResolveDataPackage(ctx context.Context, mirrorDir, packageID string) (DataPackageBytes, error) {
	parsed, ok := dataPackagePrefixParsed(packageID)
	if !ok {
		return DataPackageBytes{}, fmt.Errorf("%w: %q", ErrDataPackageInvalidReference, packageID)
	}
	if _, err := NewLayout(parsed.System); err != nil {
		return DataPackageBytes{}, fmt.Errorf("%w: %q", ErrDataPackageInvalidReference, packageID)
	}

	commit, err := contractGitResolveCommit(ctx, mirrorDir, contractAuthoritativeMainRef)
	if err != nil {
		return DataPackageBytes{}, err
	}
	root := dataPackageDir(parsed.System, packageID)
	entries, err := contractGitListTree(ctx, mirrorDir, commit, true, root)
	if err != nil {
		return DataPackageBytes{}, err
	}
	if len(entries) == 0 {
		return DataPackageBytes{}, fmt.Errorf("%w: %q", ErrDataPackageNotFound, packageID)
	}
	if len(entries) > maxDataPackageEntries {
		return DataPackageBytes{}, fmt.Errorf("%w: %q lists more than %d entries", ErrContractBoundedResult, packageID, maxDataPackageEntries)
	}

	manifestPath := dataPackageManifestPath(parsed.System, packageID)
	prefix := root + "/"
	payload := make(map[string][]byte, len(entries))
	var manifestRaw []byte
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return DataPackageBytes{}, err
		}
		if !strings.HasPrefix(entry.Path, prefix) {
			return DataPackageBytes{}, fmt.Errorf("%w: Git listed a data package path outside its root", ErrContractHistoryInvalid)
		}
		raw, err := contractGitReadBlob(ctx, mirrorDir, entry, maxDataPackageEntryBytes)
		if err != nil {
			return DataPackageBytes{}, err
		}
		total += int64(len(raw))
		if total > maxDataPackagePackageBytes {
			return DataPackageBytes{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrContractBoundedResult, packageID, maxDataPackagePackageBytes)
		}
		if entry.Path == manifestPath {
			manifestRaw = raw
			continue
		}
		payload[strings.TrimPrefix(entry.Path, prefix)] = raw
	}
	if manifestRaw == nil {
		return DataPackageBytes{}, fmt.Errorf("%w: %q has no manifest at %s", ErrDataPackageNotFound, packageID, manifestPath)
	}
	return DataPackageBytes{System: parsed.System, Root: root, ManifestRaw: manifestRaw, Payload: payload}, nil
}
