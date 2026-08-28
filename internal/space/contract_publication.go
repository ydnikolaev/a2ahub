package space

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

var (
	// ErrContractPublicationInvalid is part of the public package API.
	ErrContractPublicationInvalid = errors.New("space: invalid contract publication request")
	// ErrContractPublicationPlanChanged is part of the public package API.
	ErrContractPublicationPlanChanged = errors.New("space: plan-changed")
	// ErrContractPublicationInProgress is part of the public package API.
	ErrContractPublicationInProgress = errors.New("space: publication-in-progress")
	// ErrContractPublicationExplicitVersion is part of the public package API.
	ErrContractPublicationExplicitVersion = errors.New("space: explicit-version-required")
	// ErrContractPublicationNotEstablishing is returned when the finalized
	// descriptor is byte-identical to the one already on authoritative main,
	// so the publication's own commit would not change the descriptor.
	// Resolution defines the establishing commit as the one where the
	// descriptor's version flips to the published version
	// (validateHistoricalContractEstablishment), so such a publication is
	// unresolvable the moment it lands: `contract materialize` fails and every
	// later version fails while resolving its baseline. Refused here, before
	// the write funnel, rather than exiting zero on a pull request that
	// permanently breaks the contract.
	// ErrContractPublicationNotEstablishing is part of the public package API.
	ErrContractPublicationNotEstablishing = errors.New("space: publication-would-not-establish")
)

// ContractPublicationStatus is part of the public package API.
type ContractPublicationStatus string

const (
	// ContractPublicationPlanned is part of the public package API.
	ContractPublicationPlanned ContractPublicationStatus = "planned"
	// ContractPublicationAlreadyPublished is part of the public package API.
	ContractPublicationAlreadyPublished ContractPublicationStatus = "already-published"
	// ContractPublicationSubmitted is part of the public package API.
	ContractPublicationSubmitted ContractPublicationStatus = "submitted"
	// ContractPublicationRepaired is part of the public package API.
	ContractPublicationRepaired ContractPublicationStatus = "repaired"
)

// ContractPublicationCandidateReader freezes one bounded source. It is called
// exactly once before authoritative-main, remote-head, floor, or baseline
// reads. The returned snapshot is detached before any other dependency runs.
// ContractPublicationCandidateReader is part of the public package API.
type ContractPublicationCandidateReader interface {
	ReadContractPublicationCandidate(context.Context) (ContractCandidateSnapshot, error)
	ContractPublicationCandidateSource() contract.CandidateSource
}

// FrozenContractPublicationCandidate is the production space-owned adapter
// from the rooted candidate collector to publication. It binds the exact
// snapshot fingerprint and an opaque mirror tree identity (or the contained
// staging location) before a transport can construct a publication request.
// FrozenContractPublicationCandidate is part of the public package API.
type FrozenContractPublicationCandidate struct {
	snapshot ContractCandidateSnapshot
	source   contract.CandidateSource
}

// ContractPublicationMirrorTree names one exact Git tree from the already
// fetched mirror. Location is derived from that tree's object id only after
// every candidate byte and mode has been compared to the rooted snapshot.
// ContractPublicationMirrorTree is part of the public package API.
type ContractPublicationMirrorTree struct {
	RepoDir string
	Commit  string
	Path    string
}

// ResolveContractPublicationCandidateCommit returns the exact commit checked
// out by the already-synchronized mirror. It deliberately delegates to the
// bounded, ambient-Git-hardened contract read seam; callers then pass this OID
// to NewFrozenContractPublicationCandidate, which proves the rooted working
// tree bytes and modes match that immutable Git tree before planning.
// ResolveContractPublicationCandidateCommit is part of the public package API.
func ResolveContractPublicationCandidateCommit(ctx context.Context, repoDir string) (string, error) {
	return contractGitResolveCommit(ctx, repoDir, "HEAD")
}

// NewFrozenContractPublicationCandidate is part of the public package API.
func NewFrozenContractPublicationCandidate(ctx context.Context, reader *ContractCandidateReader, mirrorTree ContractPublicationMirrorTree) (*FrozenContractPublicationCandidate, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: rooted candidate reader is required", ErrContractPublicationInvalid)
	}
	snapshot, err := reader.Read(ctx)
	if err != nil {
		return nil, err
	}
	kind := contract.CandidateSourceMirror
	location := ""
	switch snapshot.Source {
	case ContractCandidateSourceStaging:
		kind, location = contract.CandidateSourceStaging, reader.location.Path
	case ContractCandidateSourceMirror:
		var verifyErr error
		location, verifyErr = verifyContractPublicationMirrorTree(ctx, snapshot, mirrorTree)
		if verifyErr != nil {
			return nil, verifyErr
		}
	default:
		return nil, fmt.Errorf("%w: candidate source is unsupported", ErrContractPublicationInvalid)
	}
	if snapshot.Fingerprint == "" {
		return nil, fmt.Errorf("%w: candidate fingerprint is missing", ErrContractPublicationInvalid)
	}
	return &FrozenContractPublicationCandidate{
		snapshot: clonePublicationSnapshot(snapshot),
		source:   contract.CandidateSource{Kind: kind, Location: location, Fingerprint: snapshot.Fingerprint},
	}, nil
}

func verifyContractPublicationMirrorTree(ctx context.Context, snapshot ContractCandidateSnapshot, source ContractPublicationMirrorTree) (string, error) {
	if strings.TrimSpace(source.RepoDir) == "" || !contractGitObjectID(source.Commit) || source.Commit != strings.ToLower(source.Commit) ||
		!cleanContractRelativePath(source.Path) {
		return "", fmt.Errorf("%w: mirror publication requires an exact Git tree source", ErrContractPublicationInvalid)
	}
	commit, err := contractGitResolveCommit(ctx, source.RepoDir, source.Commit)
	if err != nil {
		return "", fmt.Errorf("%w: mirror commit is not exact: %w", ErrContractPublicationInvalid, err)
	}
	if commit != source.Commit {
		return "", fmt.Errorf("%w: mirror commit is not exact", ErrContractPublicationInvalid)
	}
	treeOID, err := contractGitTreeObjectID(ctx, source.RepoDir, commit, source.Path)
	if err != nil {
		return "", err
	}
	entries, err := contractGitListTree(ctx, source.RepoDir, commit, true, source.Path)
	if err != nil {
		return "", err
	}
	files := make([]ContractSnapshotFile, 0, len(snapshot.Files))
	for _, entry := range entries {
		relative := strings.TrimPrefix(entry.Path, source.Path+"/")
		if relative == entry.Path || relative != contract.DescriptorPath &&
			!strings.HasPrefix(relative, "schema/") && !strings.HasPrefix(relative, "fixtures/") && !strings.HasPrefix(relative, "artifacts/") {
			continue
		}
		raw, readErr := contractGitReadBlob(ctx, source.RepoDir, entry, contract.MaxFileBytes)
		if readErr != nil {
			return "", readErr
		}
		files = append(files, ContractSnapshotFile{
			Path: relative, Kind: contract.CandidateRegular, Mode: entry.Mode,
			Source: ContractCandidateSourceMirror, ObjectID: entry.OID, Raw: raw,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) != len(snapshot.Files) {
		return "", fmt.Errorf("%w: rooted candidate and Git tree have different path sets", ErrContractPublicationInvalid)
	}
	for index := range files {
		if files[index].Path != snapshot.Files[index].Path || files[index].Mode != snapshot.Files[index].Mode ||
			!bytes.Equal(files[index].Raw, snapshot.Files[index].Raw) {
			return "", fmt.Errorf("%w: rooted candidate does not match exact Git tree bytes and modes", ErrContractPublicationInvalid)
		}
	}
	if fingerprintContractFiles(files) != snapshot.Fingerprint {
		return "", fmt.Errorf("%w: rooted candidate fingerprint does not match exact Git tree", ErrContractPublicationInvalid)
	}
	return treeOID, nil
}

// ContractPublicationCandidateSource is part of the public package API.
func (r *FrozenContractPublicationCandidate) ContractPublicationCandidateSource() contract.CandidateSource {
	if r == nil {
		return contract.CandidateSource{}
	}
	return r.source
}

// ReadContractPublicationCandidate is part of the public package API.
func (r *FrozenContractPublicationCandidate) ReadContractPublicationCandidate(context.Context) (ContractCandidateSnapshot, error) {
	if r == nil || r.source.Fingerprint == "" {
		return ContractCandidateSnapshot{}, ErrContractPublicationInvalid
	}
	return clonePublicationSnapshot(r.snapshot), nil
}

func clonePublicationSnapshot(snapshot ContractCandidateSnapshot) ContractCandidateSnapshot {
	clone := snapshot
	clone.Files = make([]ContractSnapshotFile, len(snapshot.Files))
	for index := range snapshot.Files {
		clone.Files[index] = cloneContractSnapshotFile(snapshot.Files[index])
	}
	return clone
}

// ContractPublicationStagingOverlayReader constructs the one canonical
// staging-over-landed virtual candidate. Staging wins by file, retained
// declarations may reuse landed bytes, additions require staged bytes, and a
// staged file removed from the final descriptor is a contradiction.
// ContractPublicationStagingOverlayReader is part of the public package API.
type ContractPublicationStagingOverlayReader struct {
	mirror   ContractPublicationCandidateReader
	staging  ContractPublicationCandidateReader
	location string
}

// NewContractPublicationStagingOverlayReader is part of the public package API.
func NewContractPublicationStagingOverlayReader(mirror, staging ContractPublicationCandidateReader, location string) (*ContractPublicationStagingOverlayReader, error) {
	if mirror == nil || staging == nil || location == "" {
		return nil, fmt.Errorf("%w: exact mirror and contained staging readers are required", ErrContractPublicationInvalid)
	}
	stagingSource := staging.ContractPublicationCandidateSource()
	if stagingSource.Kind != contract.CandidateSourceStaging || stagingSource.Location != location ||
		mirror.ContractPublicationCandidateSource().Kind != contract.CandidateSourceMirror {
		return nil, fmt.Errorf("%w: exact mirror and contained staging readers are required", ErrContractPublicationInvalid)
	}
	return &ContractPublicationStagingOverlayReader{mirror: mirror, staging: staging, location: location}, nil
}

// ContractPublicationCandidateSource is part of the public package API.
func (r *ContractPublicationStagingOverlayReader) ContractPublicationCandidateSource() contract.CandidateSource {
	if r == nil {
		return contract.CandidateSource{}
	}
	return contract.CandidateSource{Kind: contract.CandidateSourceStaging, Location: r.location}
}

// ReadContractPublicationCandidate is part of the public package API.
func (r *ContractPublicationStagingOverlayReader) ReadContractPublicationCandidate(ctx context.Context) (ContractCandidateSnapshot, error) {
	if r == nil || r.mirror == nil || r.staging == nil {
		return ContractCandidateSnapshot{}, ErrContractPublicationInvalid
	}
	mirror, err := r.mirror.ReadContractPublicationCandidate(ctx)
	if err != nil {
		return ContractCandidateSnapshot{}, err
	}
	staging, err := r.staging.ReadContractPublicationCandidate(ctx)
	if err != nil {
		return ContractCandidateSnapshot{}, err
	}
	return overlayContractPublicationCandidate(mirror, staging)
}

func overlayContractPublicationCandidate(mirror, staging ContractCandidateSnapshot) (ContractCandidateSnapshot, error) {
	if mirror.Source != ContractCandidateSourceMirror || staging.Source != ContractCandidateSourceStaging {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: staging overlay source kinds are invalid", ErrContractPublicationInvalid)
	}
	mirrorDescriptor := mirror.file(contract.DescriptorPath)
	stagingDescriptor := staging.file(contract.DescriptorPath)
	if mirrorDescriptor.Path == "" || stagingDescriptor.Path == "" || mirrorDescriptor.Mode != "100644" || stagingDescriptor.Mode != "100644" {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: staging overlay requires regular descriptors", ErrContractPublicationInvalid)
	}
	prior, err := contract.ParseDescriptor(mirrorDescriptor.Raw)
	if err != nil {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: landed descriptor is invalid", ErrContractPublicationInvalid)
	}
	desired, err := contract.ParseDescriptor(stagingDescriptor.Raw)
	if err != nil {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: staged descriptor is invalid", ErrContractPublicationInvalid)
	}
	priorDeclared := make(map[string]struct{}, len(prior.Artifacts))
	for _, entry := range prior.Artifacts {
		priorDeclared[entry.Path] = struct{}{}
	}
	desiredDeclared := make(map[string]struct{}, len(desired.Artifacts))
	for _, entry := range desired.Artifacts {
		desiredDeclared[entry.Path] = struct{}{}
	}
	mirrorFiles := publicationCandidateFilesByPath(mirror.Files)
	stagingFiles := publicationCandidateFilesByPath(staging.Files)
	files := []ContractSnapshotFile{cloneContractSnapshotFile(stagingDescriptor)}
	if len(desired.Artifacts) == 0 {
		for name, file := range mirrorFiles {
			if legacyContractPublicationSidecar(name) {
				files = append(files, cloneContractSnapshotFile(file))
			}
		}
		for name, file := range stagingFiles {
			if !legacyContractPublicationSidecar(name) {
				return ContractCandidateSnapshot{}, fmt.Errorf("%w: legacy staging contains an unsupported sidecar", ErrContractPublicationInvalid)
			}
			files = replaceContractSnapshotFile(files, file)
		}
	} else {
		for name := range stagingFiles {
			if _, declared := desiredDeclared[name]; !declared {
				return ContractCandidateSnapshot{}, fmt.Errorf("%w: staged file is removed or undeclared: %s", ErrContractPublicationInvalid, name)
			}
		}
		for _, entry := range desired.Artifacts {
			if file, ok := stagingFiles[entry.Path]; ok {
				files = append(files, cloneContractSnapshotFile(file))
				continue
			}
			if _, retained := priorDeclared[entry.Path]; retained {
				if file, ok := mirrorFiles[entry.Path]; ok {
					files = append(files, cloneContractSnapshotFile(file))
					continue
				}
			}
			return ContractCandidateSnapshot{}, fmt.Errorf("%w: added declaration requires staged bytes: %s", ErrContractPublicationInvalid, entry.Path)
		}
	}
	for index := range files {
		files[index].Source, files[index].ObjectID = ContractCandidateSourceStaging, ""
		if !validContractPublicationGitMode(files[index].Mode) {
			return ContractCandidateSnapshot{}, fmt.Errorf("%w: candidate has an unknown Git mode", ErrContractPublicationInvalid)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return ContractCandidateSnapshot{Source: ContractCandidateSourceStaging, Fingerprint: fingerprintContractFiles(files), Files: files}, nil
}

func publicationCandidateFilesByPath(files []ContractSnapshotFile) map[string]ContractSnapshotFile {
	out := make(map[string]ContractSnapshotFile, len(files))
	for _, file := range files {
		if file.Path != contract.DescriptorPath {
			out[file.Path] = file
		}
	}
	return out
}

func cloneContractSnapshotFile(file ContractSnapshotFile) ContractSnapshotFile {
	file.Raw = bytes.Clone(file.Raw)
	return file
}

func replaceContractSnapshotFile(files []ContractSnapshotFile, replacement ContractSnapshotFile) []ContractSnapshotFile {
	for index := range files {
		if files[index].Path == replacement.Path {
			files[index] = cloneContractSnapshotFile(replacement)
			return files
		}
	}
	return append(files, cloneContractSnapshotFile(replacement))
}

func legacyContractPublicationSidecar(name string) bool {
	return strings.HasPrefix(name, "schema/") || strings.HasPrefix(name, "fixtures/")
}

// ContractPublicationCandidateSource reports the exact held rooted location.
// The snapshot fingerprint is filled from the bytes returned by Read; keeping
// location on the reader prevents a caller from labelling one directory as a
// different source in the accepted plan.
// ContractPublicationCandidateSource is part of the public package API.
func (r *ContractCandidateReader) ContractPublicationCandidateSource() contract.CandidateSource {
	if r == nil {
		return contract.CandidateSource{}
	}
	kind := contract.CandidateSourceMirror
	if r.location.Source == ContractCandidateSourceStaging {
		kind = contract.CandidateSourceStaging
	}
	return contract.CandidateSource{Kind: kind, Location: r.location.Path}
}

// ContractPublicationMain owns only refreshed authoritative-main reads. Both
// lookup results must prove an exhaustive tree/history walk; non-exhaustive is
// a bounded refusal, never an empty result.
// ContractPublicationMain is part of the public package API.
type ContractPublicationMain interface {
	RefreshContractPublicationMain(context.Context) (string, error)
	ResolveContractPublicationTarget(context.Context, string, string) (ContractPublicationCompletion, bool, error)
	LookupContractPublicationIntent(context.Context, string, string) (ContractPublicationIntentLookup, error)
}

// ContractPublicationCompletion is part of the public package API.
type ContractPublicationCompletion struct {
	Snapshot        HistoricalSnapshot
	PublishedBefore []contract.PublishedContract
}

// ContractPublicationIntentLookup is part of the public package API.
type ContractPublicationIntentLookup struct {
	Matches    []ContractPublicationCompletion
	Exhaustive bool
}

// ContractPublicationPlanningReader supplies mutable current context only
// after completion and remote recovery probes prove absence. Preflight passes
// an empty AuthoritativeCommit and remains local/read-only.
// ContractPublicationPlanningReader is part of the public package API.
type ContractPublicationPlanningReader interface {
	ReadContractPublicationPlanningContext(context.Context, ContractPublicationPlanningRequest) (ContractPublicationPlanningContext, error)
}

// ContractPublicationPlanningRequest is part of the public package API.
type ContractPublicationPlanningRequest struct {
	System              string
	ContractID          string
	Selector            string
	AuthoritativeCommit string
	Candidate           contract.CandidateIntentSnapshot
	CandidateSource     contract.CandidateSource
}

// ContractPublicationPlanningContext is part of the public package API.
type ContractPublicationPlanningContext struct {
	AuthoringFloor        string
	ContractRoot          string
	BaseCommitSHA         string
	Published             []contract.PublishedContract
	SourceDigestAssertion string
	Warnings              []contract.Finding
	Violations            []contract.Finding
	// PriorDeclaredSidecars is exact evidence re-read from the selected prior
	// descriptor/profile. Keys are plan-relative paths (schema/..., etc.).
	PriorDeclaredSidecars map[string]MutationPrecondition
	// MutationBaseline is the publication whose tree authoritative main
	// carries at BaseCommitSHA — what a new publication is about to
	// overwrite. It travels to the planner as PublicationInput's own
	// MutationBaseline so that the write set, its preconditions and this
	// package's PriorDeclaredSidecars all describe ONE tree.
	MutationBaseline contract.PublishedContract
	// CurrentDescriptorDigest is the digest of the descriptor as it stands on
	// authoritative main at BaseCommitSHA, or "" when main carries none. It is
	// deliberately NOT the prior PUBLISHED descriptor (PriorDeclaredSidecars):
	// a first publication has no published prior while main already carries
	// the drafted descriptor that submit landed, and that is exactly the case
	// ErrContractPublicationNotEstablishing has to catch.
	CurrentDescriptorDigest string
}

// ContractPublicationHeadProof is produced only after a remote adapter has
// decoded the exact branch commit, verified its complete first-parent tree,
// and compared raw descriptor/carried/event bytes to the frozen candidate at
// the recorded target/profile. TreeVerified is deliberately explicit so a
// listing-only adapter cannot accidentally authorize repair.
// ContractPublicationHeadProof is part of the public package API.
type ContractPublicationHeadProof struct {
	Branch        string
	HeadSHA       string
	RecoveryJSON  []byte
	TreeVerified  bool
	recoveredPlan contract.PublicationPlan
}

// ContractPublicationHeadListing is part of the public package API.
type ContractPublicationHeadListing struct {
	Heads      []ContractPublicationHeadProof
	Observed   int
	Exhaustive bool
}

// ContractPublicationHeadProbeRequest is part of the public package API.
type ContractPublicationHeadProbeRequest struct {
	System          string
	ContractID      string
	NamespacePrefix string
	MaximumHeads    int
	// ResolvedInMain reports whether contractID@version already resolves in
	// authoritative main's own published history. Publish supplies main's
	// own ResolveContractPublicationTarget (the same lookup its explicit-
	// target fast path already trusts one call earlier) so the probe can
	// skip a head recording an already-published target — a prior publish's
	// own now-merged, not-yet-pruned branch — WITHOUT re-deriving main
	// history itself and without trusting a host-only "merged" signal this
	// package cannot verify offline. Skipping means the head is left out of
	// the listing entirely; it is never treated as relevant, never verified,
	// never eligible for repair. A head whose target is NOT resolved in main
	// is unaffected: it is proved (and, on disagreement, refused) exactly as
	// before. Nil is legal and preserves the pre-existing fail-closed
	// behaviour (every head is proved, unconditionally).
	ResolvedInMain func(ctx context.Context, contractID, target string) (bool, error)
}

// ContractPublicationRemoteProof is the restart boundary. Repair must use the
// already-verified remote commit and reconstruct only its PR from recovery-v1;
// it must not reapply mutations, restamp, rerender, push, or mint a permit.
// ContractPublicationRemoteProof is part of the public package API.
type ContractPublicationRemoteProof interface {
	ProbeContractPublicationHeads(context.Context, ContractPublicationHeadProbeRequest) (ContractPublicationHeadListing, error)
	RepairContractPublicationHead(context.Context, ContractPublicationHeadProof, SubmissionRuntime) (WriteResult, error)
}

// ContractPublicationEventBuilder is part of the public package API.
type ContractPublicationEventBuilder interface {
	BuildContractPublicationEvent(context.Context, contract.PublicationPlan) (FileWrite, error)
}

// ContractPublicationRequest is part of the public package API.
type ContractPublicationRequest struct {
	System          string
	ContractID      string
	Selector        string
	Candidate       ContractPublicationCandidateReader
	CandidateSource contract.CandidateSource
	ExpectPlan      string

	// SubmitTemplate carries host/author/presentation values plus the caller's
	// observed write floor. Publication revalidates that floor against refreshed
	// main; identity, files, mutations, base SHA, and flags remain adapter-owned.
	SubmitTemplate        SubmitRequest
	SubmissionRuntime     SubmissionRuntime
	ProducerCompatibility string
	// AllowEmptyBump is the CLI/MCP `--allow-empty-bump` acknowledgement,
	// threaded unchanged into contract.PublicationInput.AllowEmptyBump (P2
	// AC-3/4). Zero value (false) keeps the safe default: a non-first bump
	// whose mutations touch no normative artifact is refused.
	AllowEmptyBump bool
}

// ContractPublicationResult is part of the public package API.
type ContractPublicationResult struct {
	Status ContractPublicationStatus `json:"status"`
	Plan   contract.PublicationPlan  `json:"plan"`
	Write  WriteResult               `json:"write"`
}

// ContractPublicationPlanningError retains the complete stable planner issue
// set. Transports can render the fields without parsing prose, while errors.Is
// still classifies it as an invalid publication request.
// ContractPublicationPlanningError is part of the public package API.
type ContractPublicationPlanningError struct {
	Issues []contract.Issue
}

// Error is part of the public package API.
//
// Renders every issue, not a count. "refused with 1 issue(s)" told an author
// nothing they could act on: not which file, not which rule, not what to
// change — and the issues were right there, each already carrying a kind, a
// path and a detail. A refusal an agent cannot act on costs the same round
// trip as no refusal at all.
func (e *ContractPublicationPlanningError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Error())
	}
	return "space: contract publication planner refused: " + strings.Join(parts, "; ")
}

// Unwrap is part of the public package API.
func (e *ContractPublicationPlanningError) Unwrap() error { return ErrContractPublicationInvalid }

// ContractPublicationViolationError refuses a publication whose own plan
// contradicts itself, and renders every violation so the author can act on the
// message alone.
//
// The planner deliberately RECORDS a policy violation rather than refusing:
// `internal/contract` is a pure core that returns findings and decides no
// policy. Deciding is this layer's job, and until now nothing did it — the
// violation was carried into the result and the publication went through, so a
// breaking change declared as a minor exited zero and opened a pull request.
// Main was never at risk (the merge gate runs the same two checks), but the
// author learned from a red gate on an already-open pull request instead of
// from the command they ran, which for an agent means learning out of band,
// after it has already moved on, with a branch and a pull request to clean up.
//
// This is not the G1/G2 gate. A gate says "this publication needs a decision";
// every first publish is G1 and every honest major is G2, so gating on it would
// refuse correct work. A violation says the request is self-contradictory —
// the author asserted a compatible bump over incompatible content — and no
// review can make that consistent. It is refused, not routed.
// ContractPublicationViolationError is part of the public package API.
type ContractPublicationViolationError struct {
	Violations []contract.Finding
}

// Error is part of the public package API.
func (e *ContractPublicationViolationError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		part := violation.Code
		if violation.Path != "" {
			part += " " + violation.Path
		}
		if violation.Message != "" {
			part += ": " + violation.Message
		}
		parts = append(parts, part)
	}
	return "space: contract publication refused: " + strings.Join(parts, "; ")
}

// Unwrap is part of the public package API.
func (e *ContractPublicationViolationError) Unwrap() error { return ErrContractPublicationInvalid }

func newContractPublicationPlanningError(issues []contract.Issue) error {
	return &ContractPublicationPlanningError{Issues: append([]contract.Issue(nil), issues...)}
}

// ContractPublicationService is part of the public package API.
type ContractPublicationService struct {
	main     ContractPublicationMain
	planning ContractPublicationPlanningReader
	remote   ContractPublicationRemoteProof
	checker  contract.CompatibilityChecker
	events   ContractPublicationEventBuilder
	funnel   *WriteFunnel
}

// NewContractPublicationService is part of the public package API.
func NewContractPublicationService(
	main ContractPublicationMain,
	planning ContractPublicationPlanningReader,
	remote ContractPublicationRemoteProof,
	checker contract.CompatibilityChecker,
	events ContractPublicationEventBuilder,
	funnel *WriteFunnel,
) (*ContractPublicationService, error) {
	if main == nil || planning == nil || remote == nil || checker == nil || events == nil || funnel == nil {
		return nil, fmt.Errorf("%w: publish service dependencies are required", ErrContractPublicationInvalid)
	}
	return &ContractPublicationService{
		main: main, planning: planning, remote: remote, checker: checker, events: events, funnel: funnel,
	}, nil
}

// NewContractPreflightService exposes the deliberately smaller dependency
// graph for the write-free preflight surface.
// NewContractPreflightService is part of the public package API.
func NewContractPreflightService(planning ContractPublicationPlanningReader, checker contract.CompatibilityChecker) (*ContractPublicationService, error) {
	if planning == nil || checker == nil {
		return nil, fmt.Errorf("%w: preflight planning dependencies are required", ErrContractPublicationInvalid)
	}
	return &ContractPublicationService{planning: planning, checker: checker}, nil
}

// Preflight freezes the candidate and calls the same planner used by Publish.
// It never refreshes main, probes remote heads, prepares, or submits.
// Preflight is part of the public package API.
func (s *ContractPublicationService) Preflight(ctx context.Context, request ContractPublicationRequest) (ContractPublicationResult, error) {
	candidate, modes, err := freezeContractPublicationCandidate(ctx, request)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	if s == nil || s.planning == nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: planning reader is required", ErrContractPublicationInvalid)
	}
	plan, _, err := s.plan(ctx, request, candidate, modes, "")
	if err != nil {
		return ContractPublicationResult{}, err
	}
	if err := refuseAutoBumpBelowFloor(plan, request); err != nil {
		return ContractPublicationResult{}, err
	}
	return ContractPublicationResult{Status: ContractPublicationPlanned, Plan: plan}, nil
}

// refuseAutoBumpBelowFloor is the explicit-version rule, in ONE place so
// Preflight and Publish cannot answer it differently.
//
// It used to live only inside Publish's body. Preflight therefore printed
// `Status: Planned` with a target version and plan digest for a `--bump`
// selector that Publish then refused outright, before any network call — on a
// space whose floor is below ContractPublicationFloor. That is the same defect
// shape as `a2a template show` printing a template `a2a contract publish`
// rejects: the verb whose whole stated purpose is "preview the exact immutable
// publication plan" previewed one that could not be executed.
//
// It belongs beside the plan rather than inside it because it is not a property
// of the plan — the plan is valid — but of how the TARGET VERSION was chosen.
// Below the floor there is no published history to bump from safely, so the
// operator has to name the version. It is a pure function of two values both
// paths already read identically, so nothing about Preflight's deliberately
// narrower scope (no main refresh, no remote probe) excuses skipping it.
// An UNCOMPARABLE floor is deliberately not this rule's finding: it yields
// belowFloor=false and this rule abstains. A non-canonical authoring floor is
// already refused upstream by the planner's own input validation
// (contract.validatePublicationInput's "authoring_floor" issue), so both paths
// error out of s.plan() before ever reaching here — and a second opinion here
// could only duplicate that refusal or, worse, refuse for a different reason.
// This is exactly the discard the pre-existing Publish code made, preserved
// rather than quietly changed while the rule was being moved.
func refuseAutoBumpBelowFloor(plan contract.PublicationPlan, request ContractPublicationRequest) error {
	belowFloor, _ := version.OlderThan(plan.AuthoringFloor, contract.ContractPublicationFloor)
	if belowFloor && !strings.HasPrefix(request.Selector, "explicit:") {
		return ErrContractPublicationExplicitVersion
	}
	return nil
}

// Publish performs completion/recovery probes before reading mutable planning
// context, then prepares once and submits that exact immutable form once.
// Publish is part of the public package API.
func (s *ContractPublicationService) Publish(ctx context.Context, request ContractPublicationRequest) (ContractPublicationResult, error) {
	candidate, modes, err := freezeContractPublicationCandidate(ctx, request)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	if s == nil || s.main == nil || s.remote == nil || s.planning == nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: publication dependencies are incomplete", ErrContractPublicationInvalid)
	}

	authoritativeCommit, err := s.main.RefreshContractPublicationMain(ctx)
	if err != nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: authoritative main refresh failed: %w", ErrOperationConflict, err)
	}
	if !validRemoteRecoveryOID(authoritativeCommit) {
		return ContractPublicationResult{}, fmt.Errorf("%w: authoritative main refresh failed", ErrOperationConflict)
	}
	if target, explicit := explicitPublicationTarget(request.Selector); explicit {
		completion, found, resolveErr := s.main.ResolveContractPublicationTarget(ctx, request.ContractID, target)
		if resolveErr != nil {
			return ContractPublicationResult{}, fmt.Errorf("%w: resolve explicit target: %w", ErrOperationConflict, resolveErr)
		}
		if found {
			plan, verifyErr := s.verifyCompletion(request, candidate, modes, completion)
			if verifyErr != nil {
				return ContractPublicationResult{}, verifyErr
			}
			return ContractPublicationResult{Status: ContractPublicationAlreadyPublished, Plan: plan}, nil
		}
	}

	intentKey := operation.ContractPublishIntent(request.System, request.ContractID, request.Selector, candidate.Digest())
	lookup, err := s.main.LookupContractPublicationIntent(ctx, request.ContractID, intentKey)
	if err != nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: authoritative intent lookup failed: %w", ErrOperationConflict, err)
	}
	if !lookup.Exhaustive {
		return ContractPublicationResult{}, fmt.Errorf("%w: authoritative intent lookup was incomplete", ErrContractBoundedResult)
	}
	if len(lookup.Matches) > 1 {
		return ContractPublicationResult{}, fmt.Errorf("%w: publication intent has multiple main completions", ErrOperationConflict)
	}
	if len(lookup.Matches) == 1 {
		plan, verifyErr := s.verifyCompletion(request, candidate, modes, lookup.Matches[0])
		if verifyErr != nil {
			return ContractPublicationResult{}, verifyErr
		}
		return ContractPublicationResult{Status: ContractPublicationAlreadyPublished, Plan: plan}, nil
	}

	namespace := "a2a/" + request.System + "/contract-publish/op-v1-"
	headListing, err := s.remote.ProbeContractPublicationHeads(ctx, ContractPublicationHeadProbeRequest{
		System: request.System, ContractID: request.ContractID, NamespacePrefix: namespace,
		MaximumHeads: hostContractPublishHeadLimit,
		ResolvedInMain: func(ctx context.Context, contractID, target string) (bool, error) {
			_, found, resolveErr := s.main.ResolveContractPublicationTarget(ctx, contractID, target)
			return found, resolveErr
		},
	})
	if err != nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: remote publication-head probe failed: %w", ErrOperationConflict, err)
	}
	matching, inProgress, err := validateContractPublicationHeadListing(request, candidate, intentKey, headListing)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	if matching != nil {
		recoveredPlan, err := validateRecoveredContractPublicationPlan(request, candidate, *matching)
		if err != nil {
			return ContractPublicationResult{}, err
		}
		if request.ExpectPlan != "" && request.ExpectPlan != recoveredPlan.PlanDigest {
			return ContractPublicationResult{}, ErrContractPublicationPlanChanged
		}
		repairRuntime := contractPublicationRepairRuntime(request)
		write, repairErr := s.remote.RepairContractPublicationHead(ctx, *matching, repairRuntime)
		if repairErr != nil {
			return ContractPublicationResult{Plan: recoveredPlan, Write: write}, fmt.Errorf("%w: repair exact remote publication head: %w", ErrOperationConflict, repairErr)
		}
		if err := validateContractPublicationRepair(*matching, write); err != nil {
			return ContractPublicationResult{Plan: recoveredPlan, Write: write}, err
		}
		return ContractPublicationResult{Status: ContractPublicationRepaired, Plan: recoveredPlan, Write: write}, nil
	}
	if inProgress {
		return ContractPublicationResult{}, ErrContractPublicationInProgress
	}

	plan, planning, err := s.plan(ctx, request, candidate, modes, authoritativeCommit)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	// Refused HERE, and only here: the same planning helper also serves
	// verifyCompletion, which re-plans a publication that already landed on
	// main during recovery. Refusing there would make an already-published
	// contract permanently unrecoverable over a finding its own publication
	// already carries.
	if len(plan.Violations) != 0 {
		return ContractPublicationResult{Plan: plan}, &ContractPublicationViolationError{Violations: plan.Violations}
	}
	if err := refuseAutoBumpBelowFloor(plan, request); err != nil {
		return ContractPublicationResult{}, err
	}
	if request.ExpectPlan != "" && request.ExpectPlan != plan.PlanDigest {
		return ContractPublicationResult{}, ErrContractPublicationPlanChanged
	}
	if s.events == nil || s.funnel == nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: event builder and write funnel are required", ErrContractPublicationInvalid)
	}

	mutations, err := s.publicationMutations(plan, planning, request.ExpectPlan)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	event, err := s.events.BuildContractPublicationEvent(ctx, plan)
	if err != nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: build publish event: %w", ErrContractPublicationInvalid, err)
	}
	eventID, err := verifyContractPublicationEvent(request.System, plan, event)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	mutations = append(mutations, Mutation{Path: event.Path, Operation: MutationWrite, Bytes: append([]byte(nil), event.Content...)})

	// The base branch is RESOLVED here, from the mirror this same Publish
	// call already fetched (RefreshContractPublicationMain, above) — never
	// hardcoded "main" (no-silent-yes-2026-08 P2b/S-3). space.ResolveBaseBranch
	// (mirror.go) is the one resolver; a space whose real default is "master"
	// used to have its contract publication pushed at "main" unasked, exactly
	// the defect this phase exists to end. request.SubmissionRuntime.RepoDir
	// takes precedence over request.SubmitTemplate.RepoDir, mirroring
	// bindContractPublicationSubmission's own fallback below for the same
	// pair of fields (both are always the same mirror in production wiring;
	// this only matters for a caller — or a test — that sets one and not the
	// other).
	mirrorDir := request.SubmissionRuntime.RepoDir
	if mirrorDir == "" {
		mirrorDir = request.SubmitTemplate.RepoDir
	}
	if mirrorDir == "" {
		return ContractPublicationResult{}, fmt.Errorf("%w: publication requires a local mirror directory to resolve the base branch", ErrContractPublicationInvalid)
	}
	baseBranch, err := ResolveBaseBranch(ctx, mirrorDir)
	if err != nil {
		return ContractPublicationResult{}, fmt.Errorf("%w: resolve publication base branch: %w", ErrContractPublicationInvalid, err)
	}

	submit, preparation, runtime, err := bindContractPublicationSubmission(request, plan, planning, mutations, eventID, s.funnel.binaryVersion, baseBranch)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	prepared, err := s.funnel.PrepareSubmission(ctx, submit, preparation)
	if err != nil {
		return ContractPublicationResult{}, err
	}
	write, err := s.funnel.SubmitPrepared(ctx, prepared, runtime)
	if err != nil {
		return ContractPublicationResult{Plan: plan, Write: write}, err
	}
	return ContractPublicationResult{Status: ContractPublicationSubmitted, Plan: plan, Write: write}, nil
}

const hostContractPublishHeadLimit = 256

func freezeContractPublicationCandidate(ctx context.Context, request ContractPublicationRequest) (contract.CandidateIntentSnapshot, map[string]string, error) {
	if request.Candidate == nil || request.System == "" || request.ContractID == "" || request.Selector == "" {
		return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: candidate, system, contract, and selector are required", ErrContractPublicationInvalid)
	}
	if _, err := NewLayout(request.System); err != nil {
		return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: invalid system", ErrContractPublicationInvalid)
	}
	observedSource := request.Candidate.ContractPublicationCandidateSource()
	if observedSource.Kind != request.CandidateSource.Kind || observedSource.Location != request.CandidateSource.Location {
		return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: candidate source location changed", ErrContractPublicationInvalid)
	}
	snapshot, err := request.Candidate.ReadContractPublicationCandidate(ctx)
	if err != nil {
		return contract.CandidateIntentSnapshot{}, nil, err
	}
	if snapshot.Fingerprint == "" || snapshot.Fingerprint != request.CandidateSource.Fingerprint ||
		(snapshot.Source == ContractCandidateSourceMirror && request.CandidateSource.Kind != contract.CandidateSourceMirror) ||
		(snapshot.Source == ContractCandidateSourceStaging && request.CandidateSource.Kind != contract.CandidateSourceStaging) {
		return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: candidate source identity changed", ErrContractPublicationInvalid)
	}
	modes := make(map[string]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Path == "" || !validContractPublicationGitMode(file.Mode) {
			return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: candidate has an unknown Git mode", ErrContractPublicationInvalid)
		}
		modes[file.Path] = file.Mode
	}
	candidate, issues := contract.BuildCandidateIntent(snapshot.CoreSnapshot())
	if len(issues) != 0 || candidate.ContractID() != request.ContractID {
		if len(issues) != 0 {
			return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("frozen candidate is invalid: %w", newContractPublicationPlanningError(issues))
		}
		return contract.CandidateIntentSnapshot{}, nil, fmt.Errorf("%w: frozen candidate contract identity changed", ErrContractPublicationInvalid)
	}
	return candidate, modes, nil
}

func (s *ContractPublicationService) plan(ctx context.Context, request ContractPublicationRequest, candidate contract.CandidateIntentSnapshot, modes map[string]string, authoritativeCommit string) (contract.PublicationPlan, ContractPublicationPlanningContext, error) {
	planning, err := s.planning.ReadContractPublicationPlanningContext(ctx, ContractPublicationPlanningRequest{
		System: request.System, ContractID: request.ContractID, Selector: request.Selector,
		AuthoritativeCommit: authoritativeCommit, Candidate: candidate, CandidateSource: request.CandidateSource,
	})
	if err != nil {
		return contract.PublicationPlan{}, ContractPublicationPlanningContext{}, err
	}
	if authoritativeCommit != "" && planning.BaseCommitSHA != authoritativeCommit {
		return contract.PublicationPlan{}, ContractPublicationPlanningContext{}, fmt.Errorf("%w: planning base is not refreshed main", ErrOperationConflict)
	}
	canonicalRoot, err := canonicalContractPublicationRoot(request.System, request.ContractID)
	if err != nil || planning.ContractRoot != canonicalRoot {
		return contract.PublicationPlan{}, ContractPublicationPlanningContext{}, fmt.Errorf("%w: planning contract root is not canonical", ErrOperationConflict)
	}
	input := contract.PublicationInput{
		System: request.System, ContractID: request.ContractID, Selector: request.Selector,
		AuthoringFloor: planning.AuthoringFloor, Candidate: candidate, CandidateModes: modes,
		Published: planning.Published, CandidateSource: request.CandidateSource,
		ContractRoot: planning.ContractRoot, SourceDigestAssertion: planning.SourceDigestAssertion,
		Warnings: planning.Warnings, Violations: planning.Violations,
		MutationBaseline: planning.MutationBaseline, AllowEmptyBump: request.AllowEmptyBump,
	}
	plan, issues := contract.PlanPublication(input, s.checker)
	if len(issues) != 0 {
		return contract.PublicationPlan{}, ContractPublicationPlanningContext{}, fmt.Errorf("planner refused: %w", newContractPublicationPlanningError(issues))
	}
	if err := refuseNonEstablishingPublication(plan, planning); err != nil {
		return contract.PublicationPlan{}, ContractPublicationPlanningContext{}, err
	}
	return plan, planning, nil
}

// refuseNonEstablishingPublication rejects a publication whose own commit
// would leave the descriptor untouched. The planner cannot see this: it
// baselines against the PUBLISHED prior, and on a first publication there is
// none, so it emits a descriptor write regardless of what main already
// carries. Only the space knows main, so the decision is taken here — before
// the write funnel, in the same spirit as the POL-007 refusal, because a
// pull request that lands is read as success and the contradiction surfaces
// later, on work the agent has already left behind.
func refuseNonEstablishingPublication(plan contract.PublicationPlan, planning ContractPublicationPlanningContext) error {
	if planning.CurrentDescriptorDigest == "" {
		return nil
	}
	finalized, ok := plan.PlannedBytes()[contract.DescriptorPath]
	if !ok {
		return fmt.Errorf("%w: plan carries no finalized descriptor", ErrOperationConflict)
	}
	if artifact.Digest(finalized) != planning.CurrentDescriptorDigest {
		return nil
	}
	return fmt.Errorf("%w: publishing %s@%s would not change the descriptor already on main, so no commit establishes that version and it could never be resolved afterwards; the drafted descriptor must not already read the version being published (an unpublished contract stays at 0.0.0)",
		ErrContractPublicationNotEstablishing, plan.Contract, plan.TargetVersion)
}

func (s *ContractPublicationService) verifyCompletion(request ContractPublicationRequest, candidate contract.CandidateIntentSnapshot, modes map[string]string, completion ContractPublicationCompletion) (contract.PublicationPlan, error) {
	snapshot := completion.Snapshot
	if snapshot.ContractID != request.ContractID || snapshot.Version == "" || snapshot.DescriptorPath == "" {
		return contract.PublicationPlan{}, fmt.Errorf("%w: completion identity mismatch", ErrOperationConflict)
	}
	canonicalRoot, err := canonicalContractPublicationRoot(request.System, request.ContractID)
	if err != nil || path.Dir(snapshot.DescriptorPath) != canonicalRoot ||
		!isContractEventPath(snapshot.PublishEventPath) || !strings.HasPrefix(snapshot.PublishEventPath, request.System+"/events/") {
		return contract.PublicationPlan{}, fmt.Errorf("%w: completion paths are not canonical", ErrOperationConflict)
	}
	floor := contract.ContractPublicationFloor
	if snapshot.DigestProfile == contract.ProfileContractTreeV1 {
		floor = "0.18.0"
	} else if snapshot.DigestProfile != contract.ProfileContractSetV2 {
		return contract.PublicationPlan{}, fmt.Errorf("%w: completion profile is unsupported", ErrOperationConflict)
	}
	plan, issues := contract.PlanPublication(contract.PublicationInput{
		System: request.System, ContractID: request.ContractID,
		Selector: request.Selector, AuthoringFloor: floor, Candidate: candidate, CandidateModes: modes,
		Published: completion.PublishedBefore, CandidateSource: request.CandidateSource,
		ContractRoot: path.Dir(snapshot.DescriptorPath),
	}, s.checker)
	if len(issues) != 0 {
		return contract.PublicationPlan{}, fmt.Errorf("%w: cannot re-finalize completion: %w", ErrOperationConflict, newContractPublicationPlanningError(issues))
	}
	if plan.TargetVersion != snapshot.Version {
		return contract.PublicationPlan{}, fmt.Errorf("%w: completion version disagrees with the original selector", ErrOperationConflict)
	}
	if err := verifyContractPublicationCompletionEvent(request, candidate, plan, snapshot); err != nil {
		return contract.PublicationPlan{}, err
	}
	want := plan.PlannedBytes()
	got := make(map[string][]byte, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Kind != contract.CandidateRegular || file.Path == "" || file.Mode != modes[file.Path] {
			return contract.PublicationPlan{}, fmt.Errorf("%w: completion contains an unsafe entry", ErrOperationConflict)
		}
		if _, duplicate := got[file.Path]; duplicate {
			return contract.PublicationPlan{}, fmt.Errorf("%w: completion repeats a path", ErrOperationConflict)
		}
		got[file.Path] = append([]byte(nil), file.Raw...)
	}
	if !equalContractPublicationTree(want, got) {
		return contract.PublicationPlan{}, fmt.Errorf("%w: semantic intent does not match exact published tree", ErrOperationConflict)
	}
	return plan, nil
}

func validateRecoveredContractPublicationPlan(
	request ContractPublicationRequest,
	candidate contract.CandidateIntentSnapshot,
	proof ContractPublicationHeadProof,
) (contract.PublicationPlan, error) {
	record, err := DecodeRecoveryV1(proof.RecoveryJSON)
	contractID, target, targetOK := strings.Cut(record.Target, "@")
	plan := proof.recoveredPlan
	if err != nil || !targetOK || contractID != request.ContractID ||
		plan.Contract != contractID || plan.TargetVersion != target ||
		plan.CandidateIntentDigest != candidate.Digest() || plan.CandidateIntentDigest != record.CandidateIntentDigest ||
		plan.IntentKey != record.IntentKey || plan.OperationKey != record.OperationKey ||
		plan.VersionSelector != record.VersionSelector || plan.VersionSelector != request.Selector ||
		plan.HeadBranch != record.HeadBranch || plan.HeadBranch != proof.Branch ||
		plan.PlanDigest == "" || plan.PlanDigest != record.PlanDigest ||
		plan.CandidateSource != request.CandidateSource {
		return contract.PublicationPlan{}, fmt.Errorf("%w: recovered publication plan identity is invalid", ErrOperationConflict)
	}
	return plan, nil
}

func validateContractPublicationRepair(proof ContractPublicationHeadProof, write WriteResult) error {
	if write.Branch != proof.Branch || write.CommitSHA != proof.HeadSHA || write.PRNumber <= 0 || write.PRURL == "" ||
		writeStageRank(write.Stage) < writeStageRank(WriteStagePRCreated) {
		return fmt.Errorf("%w: repair did not prove the exact remote commit and PR", ErrOperationConflict)
	}
	action, err := RemainingActionFor(write.Stage, write.State)
	if err != nil || action != write.RemainingAction || (write.State == WriteStateNeedsAttention && write.Note == "") {
		return fmt.Errorf("%w: repair returned an invalid durable outcome", ErrOperationConflict)
	}
	return nil
}

func verifyContractPublicationCompletionEvent(request ContractPublicationRequest, candidate contract.CandidateIntentSnapshot, plan contract.PublicationPlan, snapshot HistoricalSnapshot) error {
	var event publicationEventWire
	if err := yaml.Unmarshal(snapshot.PublishEventRaw, &event); err != nil {
		return fmt.Errorf("%w: completion event is malformed", ErrOperationConflict)
	}
	if event.Schema != snapshot.EventSchema || event.Subject != request.ContractID || event.Transition != "publish" ||
		event.Version != snapshot.Version || event.Digest != plan.AggregateDigest {
		return fmt.Errorf("%w: completion event metadata mismatch", ErrOperationConflict)
	}
	if snapshot.EventSchema == "event/v2" {
		want := contract.PublicationIntent{
			IntentKey:             operation.ContractPublishIntent(request.System, request.ContractID, request.Selector, candidate.Digest()),
			CandidateIntentDigest: candidate.Digest(), VersionSelector: request.Selector,
			OperationKey: operation.ContractPublish(request.System, request.ContractID, snapshot.Version),
		}
		if event.DigestProfile != string(contract.ProfileContractSetV2) || event.Publication != want {
			return fmt.Errorf("%w: completion publication intent mismatch", ErrOperationConflict)
		}
	} else if snapshot.EventSchema != "event/v1" || event.DigestProfile != "" || event.Publication != (contract.PublicationIntent{}) {
		return fmt.Errorf("%w: legacy completion event shape mismatch", ErrOperationConflict)
	}
	return nil
}

func equalContractPublicationTree(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, raw := range left {
		if !bytes.Equal(raw, right[name]) {
			return false
		}
	}
	return true
}

func validateContractPublicationHeadListing(request ContractPublicationRequest, candidate contract.CandidateIntentSnapshot, intentKey string, listing ContractPublicationHeadListing) (*ContractPublicationHeadProof, bool, error) {
	if !listing.Exhaustive || listing.Observed > hostContractPublishHeadLimit || listing.Observed != len(listing.Heads) {
		return nil, false, fmt.Errorf("%w: remote publication-head namespace was not exhausted", ErrContractBoundedResult)
	}
	if listing.Heads == nil {
		return nil, false, fmt.Errorf("%w: remote publication-head proof omitted its result set", ErrOperationConflict)
	}
	wantPrefix := "a2a/" + request.System + "/contract-publish/op-v1-"
	var matching *ContractPublicationHeadProof
	inProgress := false
	seen := make(map[string]struct{}, len(listing.Heads))
	for index := range listing.Heads {
		head := listing.Heads[index]
		if _, duplicate := seen[head.Branch]; duplicate {
			return nil, false, fmt.Errorf("%w: remote publication head is duplicated", ErrOperationConflict)
		}
		seen[head.Branch] = struct{}{}
		if !strings.HasPrefix(head.Branch, wantPrefix) || !validRemoteRecoveryOID(head.HeadSHA) || !head.TreeVerified {
			return nil, false, fmt.Errorf("%w: remote publication head proof is invalid", ErrOperationConflict)
		}
		record, err := DecodeRecoveryV1(head.RecoveryJSON)
		if err != nil || record.HeadBranch != head.Branch || record.System != request.System {
			return nil, false, fmt.Errorf("%w: remote publication recovery record is invalid", ErrOperationConflict)
		}
		if request.SubmitTemplate.Repo.Owner == "" || request.SubmitTemplate.Repo.Name == "" ||
			record.Repository != repositoryIdentity(request.SubmitTemplate.Repo) {
			return nil, false, fmt.Errorf("%w: remote publication recovery repository mismatch", ErrOperationConflict)
		}
		contractID, target, ok := strings.Cut(record.Target, "@")
		if !ok || operation.ContractPublish(request.System, contractID, target) != record.OperationKey {
			return nil, false, fmt.Errorf("%w: remote publication recovery target is inconsistent", ErrOperationConflict)
		}
		if contractID != request.ContractID {
			continue
		}
		if record.IntentKey == intentKey {
			if record.CandidateIntentDigest != candidate.Digest() || record.VersionSelector != request.Selector ||
				record.OperationKey != operation.ContractPublish(request.System, request.ContractID, target) {
				return nil, false, fmt.Errorf("%w: matching remote intent metadata disagrees", ErrOperationConflict)
			}
			if matching != nil {
				return nil, false, fmt.Errorf("%w: multiple remote heads match one publication intent", ErrOperationConflict)
			}
			copy := head
			copy.RecoveryJSON = append([]byte(nil), head.RecoveryJSON...)
			matching = &copy
			continue
		}
		if explicit, ok := explicitPublicationTarget(request.Selector); ok {
			if target == explicit {
				return nil, false, fmt.Errorf("%w: target operation exists with different semantic intent", ErrOperationConflict)
			}
			inProgress = true
		}
	}
	return matching, inProgress, nil
}

func (s *ContractPublicationService) publicationMutations(plan contract.PublicationPlan, planning ContractPublicationPlanningContext, expectPlan string) ([]Mutation, error) {
	plannedBytes := plan.PlannedBytes()
	mutations := make([]Mutation, 0, len(plan.Mutations))
	for _, planned := range plan.Mutations {
		fullPath := path.Join(planning.ContractRoot, planned.Path)
		switch planned.Action {
		case contract.MutationWrite:
			raw, ok := plannedBytes[planned.Path]
			if !ok || artifact.Digest(raw) != planned.AfterDigest || len(raw) != planned.AfterSize || !validContractPublicationGitMode(planned.AfterMode) {
				return nil, fmt.Errorf("%w: planned write bytes disagree with mutation metadata", ErrOperationConflict)
			}
			mutation := Mutation{Path: fullPath, Operation: MutationWrite, Bytes: raw}
			if planned.AfterMode == "100755" || planned.BeforeMode != "" && planned.AfterMode != planned.BeforeMode {
				mutation.Mode = planned.AfterMode
			}
			if planned.BeforeDigest == "" {
				if planned.BeforeMode != "" {
					return nil, fmt.Errorf("%w: new write carries prior-tree metadata", ErrOperationConflict)
				}
			} else {
				prior, exists := planning.PriorDeclaredSidecars[planned.Path]
				if !validContractPublicationGitMode(planned.BeforeMode) ||
					!exists || prior.Digest != planned.BeforeDigest || prior.Mode != planned.BeforeMode || !validMutationPrecondition(prior) {
					return nil, fmt.Errorf("%w: replacement is not proven by the exact prior tree", ErrMutationPrecondition)
				}
				copy := prior
				mutation.ExpectedBefore = &copy
			}
			mutations = append(mutations, mutation)
		case contract.MutationDelete:
			if expectPlan == "" || expectPlan != plan.PlanDigest || planned.DeleteDomain != contract.ContractSidecarDeleteDomain ||
				planned.ContractRoot != planning.ContractRoot || !validContractPublicationGitMode(planned.BeforeMode) {
				return nil, ErrContractPublicationPlanChanged
			}
			prior, ok := planning.PriorDeclaredSidecars[planned.Path]
			if !ok || prior.Digest != planned.BeforeDigest || prior.Mode != planned.BeforeMode || !validMutationPrecondition(prior) {
				return nil, fmt.Errorf("%w: delete is not proven by the prior declared descriptor", ErrMutationUnauthorized)
			}
			mutation, err := s.funnel.mintContractSidecarDelete(contractSidecarDeleteBinding{
				OperationKey: plan.OperationKey, PlanDigest: plan.PlanDigest, ContractRoot: planning.ContractRoot,
				Path: fullPath, PriorDigest: prior.Digest, PriorMode: prior.Mode,
			})
			if err != nil {
				return nil, err
			}
			mutations = append(mutations, mutation)
		default:
			return nil, fmt.Errorf("%w: planner returned an unknown mutation", ErrOperationConflict)
		}
	}
	return mutations, nil
}

func validContractPublicationGitMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}

type publicationEventWire struct {
	Schema        string                     `yaml:"schema"`
	Event         string                     `yaml:"event"`
	Subject       string                     `yaml:"subject"`
	Transition    string                     `yaml:"transition"`
	Version       string                     `yaml:"version"`
	Digest        string                     `yaml:"digest"`
	DigestProfile string                     `yaml:"digest_profile"`
	Publication   contract.PublicationIntent `yaml:"publication"`
	Actor         struct {
		System string `yaml:"system"`
	} `yaml:"actor"`
	ProducedBy struct {
		Tool    string `yaml:"tool"`
		Version string `yaml:"version"`
	} `yaml:"produced_by"`
}

func verifyContractPublicationEvent(system string, plan contract.PublicationPlan, file FileWrite) (string, error) {
	if !isContractEventPath(file.Path) || !strings.HasPrefix(file.Path, system+"/events/") || len(file.Content) == 0 {
		return "", fmt.Errorf("%w: publish event path is invalid", ErrContractPublicationInvalid)
	}
	var event publicationEventWire
	if err := yaml.Unmarshal(file.Content, &event); err != nil {
		return "", fmt.Errorf("%w: publish event is malformed", ErrContractPublicationInvalid)
	}
	if event.Schema != plan.EventSchema || event.Subject != plan.Contract || event.Transition != "publish" ||
		event.Version != plan.TargetVersion || event.Digest != plan.AggregateDigest {
		return "", fmt.Errorf("%w: publish event does not match the accepted plan", ErrContractPublicationInvalid)
	}
	if plan.EventSchema == "event/v2" {
		want := contract.PublicationIntent{
			IntentKey: plan.IntentKey, CandidateIntentDigest: plan.CandidateIntentDigest,
			VersionSelector: plan.VersionSelector, OperationKey: plan.OperationKey,
		}
		if event.DigestProfile != string(plan.DigestProfile) || event.Publication != want {
			return "", fmt.Errorf("%w: publish event intent/profile does not match the accepted plan", ErrContractPublicationInvalid)
		}
	} else if event.DigestProfile != "" || event.Publication != (contract.PublicationIntent{}) {
		return "", fmt.Errorf("%w: legacy publish event carries v2-only fields", ErrContractPublicationInvalid)
	}
	parsed, err := artifact.ParseULID(event.Event)
	if err != nil || parsed.String() != event.Event || strings.TrimSuffix(path.Base(file.Path), ".yaml") != event.Event || event.Actor.System != system {
		return "", fmt.Errorf("%w: publish event id is not canonical", ErrContractPublicationInvalid)
	}
	return "XE-" + event.Event, nil
}

func publicationEventProducerVersion(raw []byte) (string, bool) {
	var event publicationEventWire
	if yaml.Unmarshal(raw, &event) != nil || event.ProducedBy.Tool != producerStampTool {
		return "", false
	}
	canonical, err := version.Canonical(event.ProducedBy.Version)
	return event.ProducedBy.Version, err == nil && canonical == event.ProducedBy.Version
}

func bindContractPublicationSubmission(request ContractPublicationRequest, plan contract.PublicationPlan, planning ContractPublicationPlanningContext, mutations []Mutation, eventID string, binaryVersion string, baseBranch string) (SubmitRequest, PreparationContext, SubmissionRuntime, error) {
	submit := request.SubmitTemplate
	if len(submit.Files) != 0 || len(submit.Mutations) != 0 || len(submit.ArtifactIDs) != 0 || submit.OperationKey != "" ||
		submit.ExpectedBaseSHA != "" ||
		submit.AllowForkFallback || submit.AllowSpaceInfrastructure || submit.ReplaceOrphanBranch ||
		(submit.System != "" && submit.System != request.System) ||
		(submit.Verb != "" && submit.Verb != "contract-publish") ||
		(submit.ArtifactID != "" && submit.ArtifactID != request.ContractID) ||
		// Publication owns BaseBranch: a caller may leave it unset (the
		// common case) or supply the space's own resolved value (a caller
		// that shares one SubmitRequest template across verbs), but never a
		// DIFFERENT one. This used to hardcode "main" as the one legal
		// value, which refused a caller correctly supplying "master" — the
		// guard's INTENT (publication-owned fields are not caller-set)
		// survives; only its notion of the one legal value changed, from a
		// literal to the resolved branch this call was actually given.
		(submit.BaseBranch != "" && submit.BaseBranch != baseBranch) || request.SubmissionRuntime.PriorResult.Stage != "" {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, fmt.Errorf("%w: submit template contains publication-owned fields", ErrContractPublicationInvalid)
	}
	if baseBranch == "" {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, fmt.Errorf("%w: publication base branch was not resolved before binding", ErrContractPublicationInvalid)
	}
	if !validRemoteRecoveryOID(planning.BaseCommitSHA) || planning.AuthoringFloor == "" {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, fmt.Errorf("%w: planning context lacks an exact base/floor", ErrContractPublicationInvalid)
	}
	if submit.MinBinaryVersion == "" {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, fmt.Errorf("%w: observed publication write floor is required", ErrPreparedTargetStale)
	}
	if submit.MinBinaryVersion != planning.AuthoringFloor {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, fmt.Errorf("%w: observed publication write floor changed after main refresh", ErrPreparedTargetStale)
	}
	submit.System, submit.Verb, submit.ArtifactID = request.System, "contract-publish", request.ContractID
	boundBody, err := bindContractPublicationCandidateSource(submit.PRBody, plan.CandidateSource)
	if err != nil {
		return SubmitRequest{}, PreparationContext{}, SubmissionRuntime{}, err
	}
	submit.PRBody = boundBody
	submit.ArtifactIDs = []string{request.ContractID, eventID}
	sort.Strings(submit.ArtifactIDs)
	submit.OperationKey, submit.Mutations = plan.OperationKey, mutations
	submit.BaseBranch, submit.ExpectedBaseSHA = baseBranch, planning.BaseCommitSHA
	submit.MinBinaryVersion = planning.AuthoringFloor
	targetIdentity := repositoryIdentity(submit.Repo)
	recovery := &RecoveryV1{
		CandidateIntentDigest: plan.CandidateIntentDigest, IntentKey: plan.IntentKey,
		// MintedByVersion records the RUNNING binary at mint time. It is set
		// here, not in WriteFunnel.PrepareSubmission's own later finalization
		// pass (prepared.go), because that pass only overwrites the specific
		// fields it lists — every other field set here, including this one,
		// survives unchanged into the frozen record.
		MintedByVersion: binaryVersion,
		PlanDigest:      plan.PlanDigest, Target: plan.Contract + "@" + plan.TargetVersion,
		VersionSelector: plan.VersionSelector,
	}
	preparation := PreparationContext{
		TargetIdentity: targetIdentity, BaseCommitSHA: planning.BaseCommitSHA,
		ObservedSpaceFloor: planning.AuthoringFloor, FeatureFloor: planning.AuthoringFloor,
		SchemaFloor: planning.AuthoringFloor, ProfileFloor: planning.AuthoringFloor,
		ProducerCompatibility: request.ProducerCompatibility, Recovery: recovery,
	}
	runtime := request.SubmissionRuntime
	runtime.TargetIdentity, runtime.CurrentSpaceFloor = targetIdentity, planning.AuthoringFloor
	if runtime.RepoDir == "" {
		runtime.RepoDir = submit.RepoDir
	}
	if runtime.RemoteURL == "" {
		runtime.RemoteURL = submit.RemoteURL
	}
	if runtime.Credential.Token == "" {
		runtime.Credential = submit.Credential
	}
	return submit, preparation, runtime, nil
}

const contractPublicationCandidateSourceMarker = "<!-- a2a-candidate-source-v1:"

func bindContractPublicationCandidateSource(body string, source contract.CandidateSource) (string, error) {
	if strings.Contains(body, contractPublicationCandidateSourceMarker) || !validBoundContractPublicationCandidateSource(source) {
		return "", fmt.Errorf("%w: candidate source binding is invalid", ErrContractPublicationInvalid)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return "", fmt.Errorf("%w: encode candidate source binding: %w", ErrContractPublicationInvalid, err)
	}
	marker := contractPublicationCandidateSourceMarker + base64.RawURLEncoding.EncodeToString(raw) + " -->"
	if body == "" {
		return marker, nil
	}
	return strings.TrimRight(body, "\n") + "\n\n" + marker, nil
}

func contractPublicationCandidateSourceFromPRBody(body string) (contract.CandidateSource, bool) {
	start := strings.LastIndex(body, contractPublicationCandidateSourceMarker)
	if start < 0 || strings.Count(body, contractPublicationCandidateSourceMarker) != 1 {
		return contract.CandidateSource{}, false
	}
	remainder := body[start+len(contractPublicationCandidateSourceMarker):]
	end := strings.Index(remainder, " -->")
	if end < 0 || strings.ContainsAny(remainder[:end], "\r\n\t ") {
		return contract.CandidateSource{}, false
	}
	encoded := remainder[:end]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return contract.CandidateSource{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var source contract.CandidateSource
	if decoder.Decode(&source) != nil || !validBoundContractPublicationCandidateSource(source) {
		return contract.CandidateSource{}, false
	}
	canonical, err := json.Marshal(source)
	return source, err == nil && bytes.Equal(raw, canonical)
}

func validBoundContractPublicationCandidateSource(source contract.CandidateSource) bool {
	if !validTypedSHA256(source.Fingerprint) || source.Location == "" {
		return false
	}
	switch source.Kind {
	case contract.CandidateSourceStaging:
		return cleanContractRelativePath(source.Location)
	case contract.CandidateSourceMirror:
		return !strings.ContainsAny(source.Location, "/\\\x00\r\n\t ")
	default:
		return false
	}
}

func explicitPublicationTarget(selector string) (string, bool) {
	target, ok := strings.CutPrefix(selector, "explicit:")
	if !ok {
		return "", false
	}
	canonical, err := version.Canonical(target)
	return target, err == nil && canonical == target
}

func contractPublicationRepairRuntime(request ContractPublicationRequest) SubmissionRuntime {
	runtime := request.SubmissionRuntime
	runtime.TargetIdentity = repositoryIdentity(request.SubmitTemplate.Repo)
	if runtime.RepoDir == "" {
		runtime.RepoDir = request.SubmitTemplate.RepoDir
	}
	if runtime.RemoteURL == "" {
		runtime.RemoteURL = request.SubmitTemplate.RemoteURL
	}
	if runtime.Credential.Token == "" {
		runtime.Credential = request.SubmitTemplate.Credential
	}
	return runtime
}

func canonicalContractPublicationRoot(system, contractID string) (string, error) {
	parsed, err := artifact.ParseID(contractID)
	if err != nil || parsed.Prefix != "XC" || parsed.System != system {
		return "", ErrContractPublicationInvalid
	}
	layout, err := NewLayout(system)
	if err != nil {
		return "", err
	}
	return path.Dir(layout.ProvidesContract(parsed.Slug)), nil
}
