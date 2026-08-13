package space

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

var (
	// ErrDataDeliveryInvalid guards DeliverDataPackage's own required
	// fields — refused before the funnel, section paths, or any git action.
	ErrDataDeliveryInvalid = errors.New("space: invalid data delivery request")

	// ErrDataDeliveryExpectPackChanged refuses a deliver whose caller-
	// supplied --expect-pack digest disagrees with the packed manifest's own
	// aggregate_digest, BEFORE the funnel is ever touched — mirroring where
	// ErrContractPublicationPlanChanged fires in
	// ContractPublicationService.Publish (contract_publication.go), which
	// also refuses a stale --expect-plan before any write.
	ErrDataDeliveryExpectPackChanged = errors.New("space: --expect-pack does not match the packed manifest's aggregate digest")

	// ErrDataVerificationRecordInvalid guards RecordVerificationReport's own
	// required fields, and its Transition closed set — refused before the
	// funnel, section paths, or any git action.
	ErrDataVerificationRecordInvalid = errors.New("space: invalid verification report record request")
)

// dataPackageDir is the space-relative directory a data package's manifest
// and payload live under. There is no existing Layout constructor for this:
// data-package/v1 is deliberately not one of the eight X- envelope types
// Layout.Exchange serves (spec 05a CONFIRM-3), so this phase is the first to
// define where a package's own bytes live in the space. DeliverDataPackage
// (this file, the only writer) and ResolveDataPackage (data_resolve.go, the
// only reader) are the two places that must agree on it, so the convention
// lives here, beside the writer, rather than being invented twice.
func dataPackageDir(system, packageID string) string {
	return path.Join(system, "data", packageID)
}

// dataPackageManifestName is dataPackageManifestPath's own file name,
// spelled once so spaceGitDriver (data_transport.go) — which keys its Put
// input map and Get output map by this same relative name — and
// DeliverDataPackage — which special-cases it when assembling that input
// map — cannot drift apart on what the manifest is called.
const dataPackageManifestName = "manifest.json"

// dataPackageManifestPath is dataPackageDir's own manifest.json — the one
// file both DeliverDataPackage and ResolveDataPackage look for by this exact
// name, so a resolve can never be pointed at a directory whose manifest
// landed under a different name than the writer used.
func dataPackageManifestPath(system, packageID string) string {
	return path.Join(dataPackageDir(system, packageID), dataPackageManifestName)
}

// dataPackageReportPath is dataPackageDir's own report.json — where
// RecordVerificationReport (below) writes the verification-report/v1
// document (spec 05a §T2.3: "a carried document like the manifest — it
// enters the space through the write, not as a ninth envelope type"), plan
// D-12. It reuses dataPackageDir rather than inventing a second package
// storage layout, exactly as dataPackageManifestPath does.
//
// DEVIATION, recorded because it is not the literal reading of "store it
// beside the package it judges": the report is stored beside a COPY of the
// package's own "data/<id>/" shape rooted at the VERIFYING system's own
// section (system here is the consumer, never the producer whose section
// the package's own manifest.json actually lives under). funnel.go's
// sectionOK enforces one System per commit across every FileWrite —
// including the lifecycle event this write also carries, which must live
// under the acting system's own section, exactly as every other lifecycle
// verb in this codebase already requires (cmd_lifecycle.go's own
// buildRequest/layout construction, universally keyed on the ACTOR's
// system, never the target artifact's owner) — so a literal producer-
// section report path is unreachable from a consumer-authored write. This
// is the honest resolution, not a workaround: it reuses the SAME helper,
// the SAME "data/<id>/" shape, and differs only in whose section it is
// rooted under.
func dataPackageReportPath(system, packageID string) string {
	return path.Join(dataPackageDir(system, packageID), "report.json")
}

// DataPackageForPath reports whether p is a file committed inside a data
// package's own directory — dataPackageDir(system, packageID)'s own
// "<system>/data/<DP-id>/..." grammar, above — mirroring ContractForPath's
// shape (layout.go:117) for the same reason that function exists: artifact
// discovery (internal/cli/cmd_validate_ci.go) already asks ContractForPath
// whether a changed path is a contract's own schema/fixtures/companion
// subtree rather than a free-standing artifact, and a data package's payload
// needs the identical exemption. DeliverDataPackage (below) commits a
// package's files byte-identical to what `a2a data pack` wrote, and
// datapackage.BuildEntrySet seals every entry's own sha256 with no
// normalisation (entryset.go) — so a discovery site that ran artifact
// frontmatter policy over a package's own README.md would be asking bytes
// the manifest's digest forbids changing to change anyway (spec 04 §11,
// amendment 2026-08-09, AC8).
//
// This is structural, not manifest-reading: it recognises the PATH grammar
// the writer produces, never opens manifest.json to decide. Discovery
// parsing each package's own digest-sealed manifest on the CI hot path is
// the branch that amendment explicitly rejects.
//
// system is deliberately taken from the PATH alone, never cross-checked
// against the id's own embedded system token: dataPackageDir(system,
// packageID) is parameterised on system precisely because
// dataPackageReportPath (below) roots a VERIFYING system's own report.json
// under that system's section while packageID still names the PRODUCER
// (its own doc comment; TestRecordVerificationReportSingleCommit commits
// "peer/data/DP-axon-.../report.json"). A same-system check would make this
// predicate disagree with the one write path it exists to mirror.
//
// It returns the DP- id and the package's own manifest.json path
// (dataPackageManifestPath, resolved under the PATH's own system) so a
// caller that separately wants to read the manifest has the path without
// recomputing it; this function itself never reads it.
func DataPackageForPath(p string) (packageID, manifestPath string, ok bool) {
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	// system, "data", <DP-id>, <at least one path segment beneath it> —
	// mirroring ContractForPath's own len(parts) < 4 floor (layout.go:124).
	if len(parts) < 4 || parts[1] != "data" {
		return "", "", false
	}

	system, candidate := parts[0], parts[2]
	if _, idOK := dataPackagePrefixParsed(candidate); !idOK {
		return "", "", false
	}
	return candidate, dataPackageManifestPath(system, candidate), true
}

// DataDeliveryRequest is one data-delivery write: the already-packed
// manifest and payload bytes, and the already-built handoff envelope and its
// first lifecycle event that carries the package (spec 05a T1 "One write
// through the funnel: payload + manifest + handoff in a single commit").
//
// Building the handoff/event bytes themselves is deliberately NOT this
// file's job: doing so legally requires folding this space's own committed
// event history (internal/cache.CommittedEvents) against fold's legality
// rules, and internal/cache imports internal/space — never the other way
// (ADR-001) — so a builder living here could not call it without a cycle.
// This is the exact same reason ContractPublicationEventBuilder
// (contract_publication.go) is an injected interface implemented in
// cmd/a2a rather than built inline; DeliverDataPackage takes the finished
// bytes for the same reason, just as a plain struct field rather than a
// second interface, since (unlike publication) there is nothing left for
// this file to evaluate once the caller hands them over.
type DataDeliveryRequest struct {
	// System is the producing system — the section this write commits into
	// (funnel.go's sectionOK) and the system dataPackageDir/Layout.Exchange
	// are both keyed by.
	System string

	// PackageID is the DP- id this delivery carries. Immutable from pack
	// time (spec 05a §T2.1); OperationKey is minted from this value alone.
	PackageID string

	// ManifestRaw is the data-package/v1 document's exact committed bytes.
	ManifestRaw []byte
	// AggregateDigest is that same document's own aggregate_digest field,
	// checked against ExpectPack before anything is written.
	AggregateDigest string
	// Payload is every OTHER committed file, package-root-relative.
	Payload map[string][]byte

	// HandoffID/HandoffRaw are the already-minted XH- id and its rendered
	// envelope+body bytes.
	HandoffID  string
	HandoffRaw []byte

	// EventID/EventYear/EventRaw are the handoff's own first lifecycle
	// event: a fresh ULID, the year its EventFile shards under, and the
	// event/v1 document's exact committed bytes.
	EventID   string
	EventYear string
	EventRaw  []byte

	// ExpectPack binds the caller's expected aggregate digest, exactly as
	// --expect-plan binds a publication plan (contract_publication.go's
	// ContractPublicationRequest.ExpectPlan). Empty skips the binding.
	ExpectPack string

	// SubmitTemplate carries the host/author/presentation values this write
	// needs beyond the files themselves (RepoDir, RemoteURL, Repo,
	// Credential, CommitMessage, PRTitle/Body, MinBinaryVersion, BaseBranch).
	// System, Verb, ArtifactID, ArtifactIDs, OperationKey and Files are all
	// overwritten by DeliverDataPackage — the caller supplies only what it
	// alone knows.
	SubmitTemplate SubmitRequest
}

// DeliverDataPackage commits payload + manifest + handoff (and the
// handoff's own first lifecycle event) as ONE write-funnel commit (spec 05a
// T1, plan D-6). It performs no git or network action of its own beyond the
// one funnel.Submit call: file assembly and the expect-pack refusal both run
// first, entirely locally.
//
// The payload+manifest half of that assembly is delegated to spaceGitDriver
// (data_transport.go)'s Put — spec 05a AC-8's Go seam — rather than built
// inline: Put is pure (no I/O; §0's "not a second write mechanism"), so this
// function still performs the ONE commit itself, through funnel.Submit,
// exactly as before. CONFIRM-3 (§9.1) is why the handoff and its own first
// lifecycle event are NOT part of that delegation: they are protocol, not
// transport, and this function keeps building and committing them directly.
//
// OperationKey is minted from PackageID alone (dataDeliverOperationKey,
// below) — never from the handoff or event bytes. A re-run of `a2a data
// deliver` against the same staging root therefore collides on the exact
// same deterministic branch every time, even though it freshly mints a new
// HandoffID/EventID each call (fresh entropy, per the same shape
// MintExchangeIDAt already uses everywhere else): the funnel's own step-0
// idempotent short-circuit (funnel.go's submitPreparedRequest) finds the
// first call's already-open PR before this call's freshly-minted, never-
// committed handoff/event bytes are ever considered, and repairs that PR
// rather than opening a second one (AC-3) — the same property
// TestFunnelIdempotentShortCircuit already proves for the funnel itself;
// this file's own tests prove it holds through this exact request shape.
func DeliverDataPackage(ctx context.Context, req DataDeliveryRequest, funnel *WriteFunnel) (WriteResult, error) {
	if req.System == "" || req.PackageID == "" || len(req.ManifestRaw) == 0 || req.AggregateDigest == "" ||
		req.HandoffID == "" || len(req.HandoffRaw) == 0 ||
		req.EventID == "" || req.EventYear == "" || len(req.EventRaw) == 0 || funnel == nil {
		return WriteResult{}, fmt.Errorf("%w: every field is required", ErrDataDeliveryInvalid)
	}
	if req.ExpectPack != "" && req.ExpectPack != req.AggregateDigest {
		return WriteResult{}, ErrDataDeliveryExpectPackChanged
	}
	// A payload entry literally named "manifest.json" would otherwise be
	// silently dropped when it is merged into the single map Put takes: Go
	// map construction has no way to hold two values under one key, unlike
	// the two separate FileWrite entries this function used to build (which
	// the funnel's own normalizeMutations would have refused as
	// ErrMutationDuplicatePath, deep inside funnel.Submit, well after this
	// point). Refusing here, before Put is ever called, keeps that same
	// "duplicate path is refused, never silently resolved" property visible
	// at this function's own boundary instead of moving it to a place that
	// can no longer detect it.
	if _, collides := req.Payload[dataPackageManifestName]; collides {
		return WriteResult{}, fmt.Errorf("%w: payload entry %q collides with the package's own manifest name", ErrDataDeliveryInvalid, dataPackageManifestName)
	}

	layout, err := NewLayout(req.System)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrDataDeliveryInvalid, err)
	}

	driver := newSpaceGitDriver("")
	locator, err := driver.Locate(req.System, req.PackageID)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrDataDeliveryInvalid, err)
	}
	putFiles := make(map[string][]byte, len(req.Payload)+1)
	for relative, raw := range req.Payload {
		putFiles[relative] = raw
	}
	putFiles[dataPackageManifestName] = req.ManifestRaw
	inSpace, err := driver.Put(ctx, locator, putFiles)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrDataDeliveryInvalid, err)
	}

	files := make([]FileWrite, 0, len(inSpace)+2)
	for filePath, raw := range inSpace {
		files = append(files, FileWrite{Path: filePath, Content: raw})
	}
	files = append(files, FileWrite{Path: layout.Exchange(req.HandoffID), Content: req.HandoffRaw})
	files = append(files, FileWrite{Path: layout.EventFile(req.EventYear, req.EventID), Content: req.EventRaw})

	submit := req.SubmitTemplate
	submit.System = req.System
	submit.Verb = "data-deliver"
	submit.ArtifactID = req.HandoffID
	submit.ArtifactIDs = []string{req.PackageID, req.HandoffID}
	submit.OperationKey = dataDeliverOperationKey(req.PackageID)
	submit.Files = files

	return funnel.Submit(ctx, submit)
}

// dataDeliverOperationKey mints the deterministic op-v1 retry identity for
// one data delivery, keyed on PackageID ALONE (plan D-6): the handoff id,
// the event id, and their content are all deliberately absent, for the same
// reason operation.ContractPublish's own doc gives for a resolved publish
// target — "candidate bytes, selector, baseline, floor, actor, and clock are
// deliberately absent: any attempt to publish the same contract version in
// one repository must collide and prove that its immutable plan/tree is
// identical." A data package is immutable from pack time, so any number of
// deliver retries against it must collide on the same branch and prove the
// same committed tree, never mint a second one.
//
// This reproduces internal/operation's private op-v1 domain-separated
// SHA-256 shape (internal/operation/key.go's unexported encoder) rather than
// calling it: internal/operation was not in this brief's allowlist.
// operation.Valid only checks the string's SHAPE — "op-v1-" followed by 64
// lowercase hex characters (sha256.Size*2) — so a locally derived key of
// that exact shape round-trips through the funnel's own OperationKey
// machinery (submitPreparedRequest's operation.Valid(req.OperationKey) gate,
// ParseOperationMetadata's operation.Valid(metadata.Key) gate) without
// needing a shared implementation. DEVIATION, recorded because
// internal/operation is the more honest home for this — promoting it to
// operation.DataDeliver(packageID string) string is mechanical once that
// file is grantable to a brief.
func dataDeliverOperationKey(packageID string) string {
	const domain = "a2ahub-operation-v1"
	const kind = "data-deliver"
	sum := sha256.Sum256([]byte(domain + "\x00" + kind + "\x00" + packageID))
	return "op-v1-" + hex.EncodeToString(sum[:])
}

// DataVerificationRecordRequest is one `a2a data verify --record` write: the
// already-judged verification-report/v1 document, and the already-built
// lifecycle event that drives the EXISTING handoff's own verify-pass or
// verify-fail transition (spec 05a T1 "performs ONE funnel write carrying
// the verification-report/v1 document AND the lifecycle event on the
// handoff", plan D-12).
//
// Unlike DataDeliveryRequest, this write touches no fresh handoff envelope —
// `deliver` already minted and committed it — only the report and its one
// lifecycle event are committed here, both under System's own section
// (dataPackageReportPath's own doc comment explains why System is the
// VERIFYING system, never the package's producer). Transition is caller-
// supplied but CLOSED: this file refuses anything outside
// {fold.TVerifyPass, fold.TVerifyFail} before the funnel is ever touched, so
// a caller of THIS function cannot reopen the choice dataCore.verify's own
// direction derivation (VerifyPass, never chosen) already closed one layer
// up — the same "closed at every layer, not just the outermost one" property
// dataCore.verify's own doc comment claims for itself.
type DataVerificationRecordRequest struct {
	// System is the VERIFYING (consumer) system — the section this write
	// commits into (funnel.go's sectionOK) and the system
	// dataPackageReportPath/Layout.EventFile are both keyed by.
	System string

	// PackageID is the package this report judges (DP- id), used only to
	// place the report beside a copy of that package's own shape
	// (dataPackageReportPath).
	PackageID string

	// ReportID is the report's own VR- id. OperationKey is minted from this
	// value alone (dataVerifyOperationKey, below) — never from PackageID,
	// HandoffID, EventID or their content, mirroring dataDeliverOperationKey
	// (plan D-6). The caller (cmd/a2a's dataCore) is responsible for this
	// id being STABLE across re-runs of the same verify+record against the
	// same package and contract; this file only mints the key from
	// whatever id it is handed.
	ReportID  string
	ReportRaw []byte

	// HandoffID is the EXISTING handoff carrying PackageID (verify's own
	// reverse lookup, cmd/a2a's findHandoffForPackage) — never minted here.
	HandoffID string

	// Transition is fold.TVerifyPass or fold.TVerifyFail ONLY, derived by
	// the caller from the report's own Result — refused otherwise, before
	// any git action.
	Transition string

	EventID   string
	EventYear string
	EventRaw  []byte

	// SubmitTemplate carries the host/author/presentation values this write
	// needs beyond the files themselves, exactly as DataDeliveryRequest's
	// own field does. System, Verb, ArtifactID, ArtifactIDs, OperationKey
	// and Files are all overwritten by RecordVerificationReport.
	SubmitTemplate SubmitRequest
}

// RecordVerificationReport commits the report + its one lifecycle event as
// ONE write-funnel commit (spec 05a T1, plan D-12), mirroring
// DeliverDataPackage's own shape and doc comment.
//
// OperationKey is minted from ReportID alone (dataVerifyOperationKey,
// below), the same "identity, not content" discipline dataDeliverOperationKey
// already uses for PackageID (plan D-6): a re-run of `a2a data verify
// --record` against the same package and contract collides on the SAME
// operation key — because the caller mints a STABLE ReportID for that exact
// (package, contract) pair rather than fresh entropy every call — and
// therefore repairs the same pull request rather than opening a second one.
func RecordVerificationReport(ctx context.Context, req DataVerificationRecordRequest, funnel *WriteFunnel) (WriteResult, error) {
	if req.System == "" || req.PackageID == "" || req.ReportID == "" || len(req.ReportRaw) == 0 ||
		req.HandoffID == "" || req.EventID == "" || req.EventYear == "" || len(req.EventRaw) == 0 || funnel == nil {
		return WriteResult{}, fmt.Errorf("%w: every field is required", ErrDataVerificationRecordInvalid)
	}
	if req.Transition != fold.TVerifyPass && req.Transition != fold.TVerifyFail {
		return WriteResult{}, fmt.Errorf("%w: transition must be %q or %q, got %q", ErrDataVerificationRecordInvalid, fold.TVerifyPass, fold.TVerifyFail, req.Transition)
	}

	layout, err := NewLayout(req.System)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrDataVerificationRecordInvalid, err)
	}

	files := []FileWrite{
		{Path: dataPackageReportPath(req.System, req.PackageID), Content: req.ReportRaw},
		{Path: layout.EventFile(req.EventYear, req.EventID), Content: req.EventRaw},
	}

	submit := req.SubmitTemplate
	submit.System = req.System
	submit.Verb = "data-verify"
	submit.ArtifactID = req.HandoffID
	submit.ArtifactIDs = []string{req.PackageID, req.HandoffID}
	submit.OperationKey = dataVerifyOperationKey(req.ReportID)
	submit.Files = files

	return funnel.Submit(ctx, submit)
}

// dataVerifyOperationKey mints the deterministic op-v1 retry identity for
// one verification-report record, keyed on ReportID ALONE — mirroring
// dataDeliverOperationKey's own doc comment (plan D-6, D-12): PackageID,
// HandoffID, EventID and their content are all deliberately absent. See that
// function's doc comment for why this reproduces internal/operation's
// private op-v1 shape rather than calling it (same DEVIATION, same reason:
// internal/operation is not in this brief's allowlist).
func dataVerifyOperationKey(reportID string) string {
	const domain = "a2ahub-operation-v1"
	const kind = "data-verify"
	sum := sha256.Sum256([]byte(domain + "\x00" + kind + "\x00" + reportID))
	return "op-v1-" + hex.EncodeToString(sum[:])
}
