package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/contractwiring"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	semver "github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// contractP6Core is the one production adapter shared by the CLI and MCP
// transports. It selects bounded rooted inputs and delegates planning,
// history, materialization and conformance to their owning core packages.
type contractP6Core struct {
	projectRoot       string
	mirrorDir         string
	remoteURL         string
	spaceID           string
	ownSystem         string
	repository        host.Repo
	authorName        string
	authorEmail       string
	binary            string
	engine            *validate.Engine
	host              contractwiring.Host
	now               func() time.Time
	entropy           io.Reader
	resolveActor      func(kind, name, model string) (template.Actor, error)
	resolveCredential func(context.Context) (host.Credential, error)
	loadManifest      func() (space.Manifest, error)
}

func newContractP6Core(
	projectRoot, mirrorDir, remoteURL, spaceID, ownSystem string,
	repository host.Repo,
	authorName, authorEmail, binary string,
	engine *validate.Engine,
	h *host.GitHubHost,
	resolveActor func(kind, name, model string) (template.Actor, error),
	resolveCredential func(context.Context) (host.Credential, error),
) (*contractP6Core, error) {
	if projectRoot == "" || mirrorDir == "" || remoteURL == "" || spaceID == "" || ownSystem == "" ||
		repository.Owner == "" || repository.Name == "" || authorName == "" || authorEmail == "" || binary == "" ||
		engine == nil || h == nil || resolveActor == nil || resolveCredential == nil {
		return nil, fmt.Errorf("contract P6 wiring: complete production dependencies are required")
	}
	return &contractP6Core{
		projectRoot: projectRoot, mirrorDir: mirrorDir, remoteURL: remoteURL,
		spaceID: spaceID, ownSystem: ownSystem, repository: repository,
		authorName: authorName, authorEmail: authorEmail, binary: binary,
		engine: engine, host: h, now: time.Now, entropy: rand.Reader,
		resolveActor: resolveActor, resolveCredential: resolveCredential,
		loadManifest: func() (space.Manifest, error) { return loadManifest(mirrorDir) },
	}, nil
}

func newCLIContractP6Core(p paths, deps lifecycleDeps) (*contractP6Core, error) {
	engine, err := newEngine()
	if err != nil {
		return nil, err
	}
	return newContractP6Core(
		p.projectRoot, deps.mirrorDir, deps.hostCfg.RemoteURL, deps.spaceID, deps.ownSystem,
		deps.hostCfg.Repo,
		deps.hostCfg.CommitAuthorName, deps.hostCfg.CommitAuthorEmail, funnelBinaryVersion(),
		engine, host.NewGitHubHost(http.DefaultClient, githubAPIBase()),
		func(kind, name, model string) (template.Actor, error) {
			return deps.resolveActor(cli.ActorFlags{Kind: kind, Name: name, Model: model})
		},
		func(context.Context) (host.Credential, error) { return deps.hostCfg.Credential, nil },
	)
}

func newMCPContractOperationsFactory(p paths, cfg space.ProjectConfig, machine space.MachineConfig) mcp.ContractOperationsFactory {
	return func(resolveMCPActor mcp.ActorResolver) (mcp.ContractToolOperations, error) {
		engine, err := newEngine()
		if err != nil {
			return mcp.ContractToolOperations{}, err
		}
		cores := make(map[string]*contractP6Core, len(cfg.Spaces))
		github := host.NewGitHubHost(http.DefaultClient, githubAPIBase())
		for _, ref := range cfg.Spaces {
			owner, name, parseErr := parseGitHubRepo(ref.RepoURL)
			if parseErr != nil {
				return mcp.ContractToolOperations{}, parseErr
			}
			core, coreErr := newContractP6Core(
				p.projectRoot, space.ResolveMirrorLocation(p.projectRoot, ref, machine), ref.RepoURL, ref.ID, cfg.System,
				host.Repo{Owner: owner, Name: name}, cfg.System, cfg.System+"@a2a.local", funnelBinaryVersion(),
				engine, github,
				func(kind, name, model string) (template.Actor, error) {
					return resolveMCPActor(mcp.ActorInput{Kind: kind, Name: name, Model: model})
				},
				func(ctx context.Context) (host.Credential, error) { return resolveCredential(ctx, ref.ID, machine) },
			)
			if coreErr != nil {
				return mcp.ContractToolOperations{}, coreErr
			}
			cores[ref.ID] = core
		}
		return contractMCPToolOperations(mcpContractP6Router{bySpace: cores}), nil
	}
}

type contractP6PublicationInput struct {
	id             string
	version        string
	bump           string
	staging        string
	expectPlan     string
	allowEmptyBump bool
	actorKind      string
	actorName      string
	actorModel     string
}

func (c *contractP6Core) preflight(ctx context.Context, input contractP6PublicationInput) (space.ContractPublicationResult, error) {
	request, err := c.publicationRequest(ctx, input, host.Credential{}, false)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	// The REQUEST stays credential-free — preflight performs no write and must
	// not imply one. The REPOSITORY still needs a credential, because its
	// refresh fetches origin/main, and against a private space an unauthenticated
	// fetch cannot even see the branch it is asked to pin.
	repository, err := c.publicationRepository(c.readCredential(ctx))
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	service, err := space.NewContractPreflightService(repository, validate.ContractCompatibilityAdapter{})
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return service.Preflight(ctx, request)
}

func (c *contractP6Core) publish(ctx context.Context, input contractP6PublicationInput) (space.ContractPublicationResult, error) {
	actor, err := c.resolveActor(input.actorKind, input.actorName, input.actorModel)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	request, err := c.publicationRequest(ctx, input, credential, true)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	service, err := contractwiring.NewPublicationService(contractwiring.PublicationDependencies{
		MirrorDir: c.mirrorDir, RemoteURL: c.remoteURL, SpaceID: c.spaceID, OwnSystem: c.ownSystem,
		Binary: c.binary, Repository: c.repository, Credential: credential, Host: c.host, Engine: c.engine,
		ManifestValidator: contractManifestEngine{engine: c.engine},
		HistoryValidator:  contractHistoryDocumentEngine{engine: c.engine},
		Compatibility:     validate.ContractCompatibilityAdapter{},
		Actor:             actor, Now: c.now, Entropy: c.entropy, LoadManifest: c.loadManifest,
		// The space's artifact-resolution and legality rules, supplied from
		// HERE because they live in `internal/cli` and ADR-001 makes `cli` a
		// frontend nothing below it imports. `internal/contractwiring` sits
		// below `cli`, so it takes this as a dependency rather than reaching
		// up for it.
		ValidateSubmitFiles: func(ctx context.Context, manifest space.Manifest, files []space.FileWrite) error {
			resolver := cli.NewMirrorResolver(c.mirrorDir, manifest)
			legality := cli.NewLegalityAdapter(c.mirrorDir, c.ownSystem, manifest)
			return cli.NewSubmitValidatorAdapter(c.engine, c.ownSystem, resolver, legality).ValidateSubmit(ctx, files)
		},
	})
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return service.Publish(ctx, request)
}

func (c *contractP6Core) publicationRepository(credential host.Credential) (*space.ContractPublicationRepository, error) {
	return space.NewContractPublicationRepository(
		c.mirrorDir, c.remoteURL, credential,
		contractManifestEngine{engine: c.engine}, contractHistoryDocumentEngine{engine: c.engine},
	)
}

// readCredential resolves this space's credential for a REFRESH, degrading to
// the empty credential when none resolves — the same lenient sibling of
// resolveCredential that wire.go carries, and for the same reason. Preflight
// in particular must keep working against a public space with no credential
// configured, while still being able to reach a private one.
func (c *contractP6Core) readCredential(ctx context.Context) host.Credential {
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return host.Credential{}
	}
	return credential
}

func (c *contractP6Core) publicationRequest(ctx context.Context, input contractP6PublicationInput, credential host.Credential, publishing bool) (space.ContractPublicationRequest, error) {
	selector, err := contractPublicationSelector(input.version, input.bump)
	if err != nil {
		return space.ContractPublicationRequest{}, err
	}
	candidate, source, err := c.freezePublicationCandidate(ctx, input.id, input.staging)
	if err != nil {
		return space.ContractPublicationRequest{}, err
	}
	observedFloor := ""
	if publishing {
		manifest, manifestErr := c.loadManifest()
		if manifestErr != nil {
			return space.ContractPublicationRequest{}, fmt.Errorf("load publication write floor: %w", manifestErr)
		}
		observedFloor = manifest.MinBinaryVersion
	}
	request := space.ContractPublicationRequest{
		System: c.ownSystem, ContractID: input.id, Selector: selector,
		Candidate: candidate, CandidateSource: source, ExpectPlan: input.expectPlan,
		ProducerCompatibility: c.binary, AllowEmptyBump: input.allowEmptyBump,
	}
	if publishing {
		request.SubmitTemplate = space.SubmitRequest{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Repo: c.repository, MinBinaryVersion: observedFloor,
			CommitMessage:    "a2a(contract-publish): " + input.id,
			CommitAuthorName: c.authorName, CommitAuthorEmail: c.authorEmail,
			PRTitle: "Publish " + input.id, PRBody: "Publish an exact immutable contract version.",
			Credential: credential,
		}
		request.SubmissionRuntime = space.SubmissionRuntime{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Credential: credential,
		}
	}
	return request, nil
}

func contractPublicationSelector(explicit, bump string) (string, error) {
	if (explicit == "") == (bump == "") {
		return "", fmt.Errorf("contract publication requires exactly one of version or bump")
	}
	if explicit != "" {
		canonical, err := semver.Canonical(explicit)
		if err != nil || canonical != explicit {
			return "", fmt.Errorf("contract publication version %q is not canonical", explicit)
		}
		return "explicit:" + canonical, nil
	}
	if bump != "patch" && bump != "minor" && bump != "major" {
		return "", fmt.Errorf("contract publication bump %q is invalid", bump)
	}
	return "auto:" + bump, nil
}

// freezePublicationCandidate delegates to contractwiring.FreezePublicationCandidate,
// the shared production logic cmd/a2a's own offline integration test calls
// directly rather than carrying a hand copy of it.
func (c *contractP6Core) freezePublicationCandidate(ctx context.Context, contractID, staging string) (space.ContractPublicationCandidateReader, contract.CandidateSource, error) {
	return contractwiring.FreezePublicationCandidate(ctx, c.ownSystem, c.projectRoot, c.mirrorDir, contractID, staging)
}

type contractManifestEngine struct{ engine *validate.Engine }

func (v contractManifestEngine) ValidateManifest(_ context.Context, raw []byte) error {
	result, err := v.engine.ValidateManifest(raw)
	if err != nil {
		return err
	}
	return contractValidationResultError("manifest", result)
}

// contractHistoryDocumentEngine is the production historical-document seam.
// It validates the exact descriptor path and exact v1/v2 event bytes through
// the same validate.Engine used by submit and V3. A low historical floor keeps
// producer-stamp rollout policy from being applied retroactively.
type contractHistoryDocumentEngine struct{ engine *validate.Engine }

func (v contractHistoryDocumentEngine) ValidateHistoricalContractDocuments(_ context.Context, documents space.ContractHistoryDocuments) error {
	descriptor, err := v.engine.ValidateDraft(validate.Draft{Path: documents.Descriptor.Path, Raw: documents.Descriptor.Raw})
	if err != nil {
		return err
	}
	if err := contractValidationResultError("historical descriptor", descriptor); err != nil {
		return err
	}
	event, err := v.engine.ValidateEvent(documents.PublishEvent.Raw, "0.0.0")
	if err != nil {
		return err
	}
	return contractValidationResultError("historical publish event", event)
}

func contractValidationResultError(label string, result validate.Result) error {
	if result.Valid {
		return nil
	}
	parts := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		parts = append(parts, fmt.Sprintf("%s %s: %s", violation.Code, violation.Path, violation.Message))
	}
	return fmt.Errorf("%s validation rejected: %s", label, strings.Join(parts, "; "))
}

func (c *contractP6Core) resolveHistorical(ctx context.Context, ref string) (space.HistoricalSnapshot, error) {
	id, requestedVersion, err := splitExactContractReference(ref)
	if err != nil {
		return space.HistoricalSnapshot{}, err
	}
	return space.ResolveContractVersion(ctx, c.mirrorDir, id, requestedVersion, contractHistoryDocumentEngine{engine: c.engine})
}

func splitExactContractReference(ref string) (string, string, error) {
	id, requestedVersion, found := strings.Cut(ref, "@")
	parsed, idErr := artifact.ParseID(id)
	canonical, versionErr := semver.Canonical(requestedVersion)
	if !found || strings.Contains(requestedVersion, "@") || idErr != nil || parsed.Prefix != "XC" ||
		versionErr != nil || canonical != requestedVersion {
		return "", "", fmt.Errorf("contract reference %q must be an exact XC-id@canonical-version", ref)
	}
	return id, canonical, nil
}

func (c *contractP6Core) materialize(ctx context.Context, ref, destination string) (space.ContractMaterializeResult, error) {
	snapshot, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	materializer, err := space.NewContractMaterializer(c.projectRoot)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	return materializeContractAndClose(ctx, materializer, snapshot, destination)
}

type contractMaterializeCapability interface {
	Materialize(context.Context, space.HistoricalSnapshot, string) (space.ContractMaterializeResult, error)
	Close() error
}

func materializeContractAndClose(
	ctx context.Context,
	materializer contractMaterializeCapability,
	snapshot space.HistoricalSnapshot,
	destination string,
) (result space.ContractMaterializeResult, resultErr error) {
	defer func() {
		if closeErr := materializer.Close(); closeErr != nil {
			wrapped := fmt.Errorf("close contract materializer: %w", closeErr)
			if resultErr == nil {
				resultErr = wrapped
				return
			}
			resultErr = errors.Join(resultErr, wrapped)
		}
	}()
	return materializer.Materialize(ctx, snapshot, destination)
}

func (c *contractP6Core) check(ctx context.Context, ref, payloadPath, schemaPath string, suite bool) (contract.ConformanceResult, error) {
	snapshot, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return contract.ConformanceResult{}, err
	}
	mode := contract.ConformanceModePayload
	var payload []byte
	if suite {
		if payloadPath != "" || schemaPath != "" {
			return contract.ConformanceResult{}, fmt.Errorf("suite mode does not accept payload or schema paths")
		}
		mode = contract.ConformanceModeSuite
	} else {
		if payloadPath == "" {
			return contract.ConformanceResult{}, fmt.Errorf("payload mode requires a project-relative payload path")
		}
		payload, err = readBoundedProjectFile(c.projectRoot, payloadPath, contract.MaxFileBytes)
		if err != nil {
			return contract.ConformanceResult{}, err
		}
	}
	result := contract.CheckConformance(contract.ConformanceInput{
		ContractID: snapshot.ContractID, Version: snapshot.Version, Commit: snapshot.CommitSHA,
		SchemaFormat: snapshot.Descriptor.SchemaFormat, PublishedDigest: snapshot.PublishedDigest,
		Set: snapshot.CarriedSet, Mode: mode, SchemaPath: schemaPath,
		PayloadPath: payloadPath, Payload: payload,
	}, validate.ContractInstanceAdapter{})
	if violations := validate.ValidateContractConformance(result); len(violations) != 0 {
		result.Passed = false
		if result.Message == "" {
			result.Message = violations[0].Code + ": " + violations[0].Message
		}
	}
	return result, nil
}

func readBoundedProjectFile(projectRoot, name string, maximum int) ([]byte, error) {
	if name == "" || name == "." || path.IsAbs(name) || filepath.VolumeName(name) != "" ||
		path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return nil, fmt.Errorf("project payload path %q is not safely contained", name)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return nil, fmt.Errorf("project payload path %q is not safely contained", name)
		}
	}
	rootBefore, err := os.Lstat(projectRoot)
	if err != nil || rootBefore.Mode()&os.ModeSymlink != 0 || !rootBefore.IsDir() {
		return nil, fmt.Errorf("project root is not a real directory")
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("open project root: %w", err)
	}
	defer func() { _ = root.Close() }() // reason: preserve the payload result
	leafBefore, err := root.Lstat(name)
	if err != nil || leafBefore.Mode()&os.ModeSymlink != 0 || !leafBefore.Mode().IsRegular() {
		return nil, fmt.Errorf("project payload %q is not a regular contained file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open project payload %q: %w", name, err)
	}
	defer func() { _ = file.Close() }() // reason: preserve the bounded read result
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(leafBefore, opened) {
		return nil, fmt.Errorf("project payload %q changed while opening", name)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, fmt.Errorf("read project payload %q: %w", name, err)
	}
	leafAfter, err := root.Lstat(name)
	if err != nil || leafAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, leafAfter) {
		return nil, fmt.Errorf("project payload %q changed while reading", name)
	}
	if len(raw) > maximum {
		return nil, fmt.Errorf("project payload %q exceeds %d bytes", name, maximum)
	}
	return raw, nil
}

type contractInspectionResult struct {
	added              []string
	removed            []string
	changed            []string
	frontmatterChanged []string
}

// contractVerifyResult carries the closed three-outcome vocabulary
// (contract.ExportVerification: matched/drifted/unmeasured) rather than a
// `matches bool` — two outcomes cannot express three (spec P2 ACs 5/6/17).
// outcome is always ClassifyExportVerification's return value; nothing
// computes it a second way.
type contractVerifyResult struct {
	id          string
	outcome     contract.ExportVerification
	localDigest string
	wantDigest  string
	diff        contractInspectionResult
}

// contractExportOutcomeWord is D9's render boundary: it maps
// contract.ExportUnmeasured onto the SHIPPED validate.SeverityUnmeasured
// (internal/validate/result.go:72, "UNMEASURED is a SEVERITY, not a fourth
// verdict") by VALUE rather than minting a bespoke word. The two string
// values are already identical ("unmeasured" == "unmeasured" —
// contract.ExportVerification's own doc comment records this), so this
// function is a proof that the mapping holds, not a translation a second
// vocabulary could drift from. matched/drifted are contract's own
// vocabulary — verify-export's own halves, never a validate.Severity — and
// pass through unchanged.
func contractExportOutcomeWord(outcome contract.ExportVerification) string {
	if outcome == contract.ExportUnmeasured {
		return string(validate.SeverityUnmeasured)
	}
	return string(outcome)
}

func (c *contractP6Core) diff(ctx context.Context, id, v1, v2 string) (contractInspectionResult, error) {
	left, err := c.resolveHistorical(ctx, id+"@"+v1)
	if err != nil {
		return contractInspectionResult{}, err
	}
	right, err := c.resolveHistorical(ctx, id+"@"+v2)
	if err != nil {
		return contractInspectionResult{}, err
	}
	return inspectContractSnapshots(left.Files, right.Files), nil
}

func (c *contractP6Core) verifyExport(ctx context.Context, local, ref string) (contractVerifyResult, error) {
	// A bare `<XC-id>`, with no @version, asks the LOCAL question: what is the
	// export-source-v1 digest of the candidate in `local`?
	//
	// It exists because `generated_from.source_digest` must assert that digest
	// and nothing computed it for an unpublished candidate. The only verb that
	// could resolves a PUBLISHED version first, so it was unreachable for
	// exactly the case that needs it — a producer's FIRST release. The remaining
	// path was to run publish, read the value out of the refusal, and paste it
	// back, which makes the assertion self-fulfilling: the field exists so a
	// producer can prove the published export matches their code, and a number
	// copied from a2a's own computation proves only that a2a agrees with itself.
	// Reported as fb-20260806-eb224e.
	//
	// This is deliberately a CHECK, not a source. The digest a producer asserts
	// should come from their own generator implementing the profile — which
	// reference/contract-versions.md now defines — and this prints what a2a
	// computes so the two can be compared before the irreversible verb.
	if !strings.Contains(ref, "@") {
		return c.verifyExportLocalDigest(ctx, local, ref)
	}
	historical, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return contractVerifyResult{}, err
	}
	// AC-13: read the SAME candidate bytes publish would — the mirror
	// overlaid with staging, through the shared freezePublicationCandidate
	// helper — rather than staging alone. A partial staging overlay
	// publish accepts (only the changed file(s) staged, the rest read from
	// the mirror) was previously refused here on a declared file the
	// staging-only read could not see.
	localSnapshot, err := c.readVerifyCandidate(ctx, historical.ContractID, local)
	if err != nil {
		return contractVerifyResult{}, err
	}
	diff := inspectContractSnapshots(localSnapshot.Files, historical.Files)
	projection, err := c.localExportSource(localSnapshot, local)
	if err != nil {
		return contractVerifyResult{}, err
	}
	generatedFromDeclared := historical.Descriptor.GeneratedFrom != nil
	want := ""
	if generatedFromDeclared {
		want = historical.Descriptor.GeneratedFrom.SourceDigest
	}
	// US-3's own comparison (spec §T1, "the comparison, stated precisely"):
	// the published generated_from.source_digest is the PRODUCER's own
	// asserted value, already checked by publish before it was written —
	// comparing the local export-source-v1 digest against it is meaningful
	// PROVENANCE. Comparing against historical.PublishedDigest (the
	// publication AGGREGATE, a2a's own value) would be comparing a2a to
	// a2a, the exact circularity this verb exists to avoid.
	outcome := contract.ClassifyExportVerification(generatedFromDeclared, want, projection.Digest)
	return contractVerifyResult{
		id: historical.ContractID, outcome: outcome, localDigest: projection.Digest,
		wantDigest: want, diff: diff,
	}, nil
}

// verifyExportLocalDigest computes export-source-v1 over a local candidate and
// compares it against whatever the descriptor asserts, without needing any
// published history. wantDigest is the descriptor's own assertion, empty when
// the candidate declares no `generated_from` at all.
func (c *contractP6Core) verifyExportLocalDigest(ctx context.Context, local, ref string) (contractVerifyResult, error) {
	parsed, err := artifact.ParseID(ref)
	if err != nil || parsed.Prefix != "XC" || parsed.Class != artifact.ClassStanding {
		return contractVerifyResult{}, fmt.Errorf("contract reference %q must be an XC-id, or an exact XC-id@canonical-version to compare against published bytes", ref)
	}
	// AC-13: same candidate-freezing helper as publish (see verifyExport's
	// identical note above).
	snapshot, err := c.readVerifyCandidate(ctx, ref, local)
	if err != nil {
		return contractVerifyResult{}, err
	}
	descriptor, err := contract.ParseDescriptor(snapshot.CoreSnapshot().Descriptor.Raw)
	if err != nil {
		return contractVerifyResult{}, fmt.Errorf("candidate descriptor at %s cannot be decoded: %w", local, err)
	}
	projection, err := c.localExportSource(snapshot, local)
	if err != nil {
		return contractVerifyResult{}, err
	}
	generatedFromDeclared := descriptor.GeneratedFrom != nil
	want := ""
	if generatedFromDeclared {
		want = descriptor.GeneratedFrom.SourceDigest
	}
	outcome := contract.ClassifyExportVerification(generatedFromDeclared, want, projection.Digest)
	return contractVerifyResult{
		id: ref, outcome: outcome, localDigest: projection.Digest, wantDigest: want,
	}, nil
}

// readVerifyCandidate is AC-13's one adapter: it delegates to the SAME
// freezePublicationCandidate publish calls (contractwiring.
// FreezePublicationCandidate, mirror overlaid with staging), so verify and
// publish agree on what "the candidate" is. The two snapshot shapes are
// close but not identical to the plain staging read this replaces — this is
// the deliberate judgment call spec P2 §"AC-13" names, written once here
// rather than at each of verifyExport's and verifyExportLocalDigest's two
// call sites.
func (c *contractP6Core) readVerifyCandidate(ctx context.Context, contractID, local string) (space.ContractCandidateSnapshot, error) {
	candidate, _, err := c.freezePublicationCandidate(ctx, contractID, local)
	if err != nil {
		return space.ContractCandidateSnapshot{}, err
	}
	return candidate.ReadContractPublicationCandidate(ctx)
}

// localExportSource builds the candidate's carried set under the ONE
// selected digest profile (AC-9: SelectDigestProfile/DetectInventoryMode,
// never a hardcoded contract.ProfileContractSetV2) and projects
// export-source-v1 from it. ExportSource itself already refuses a non-V2
// set, so a legacy local candidate is refused here rather than silently
// digested under V2 rules.
func (c *contractP6Core) localExportSource(snapshot space.ContractCandidateSnapshot, local string) (contract.DigestProjection, error) {
	core := snapshot.CoreSnapshot()
	descriptor, err := contract.ParseDescriptor(core.Descriptor.Raw)
	if err != nil {
		return contract.DigestProjection{}, fmt.Errorf("candidate descriptor at %s cannot be decoded: %w", local, err)
	}
	mode, err := contract.DetectInventoryMode(core.Descriptor.Raw)
	if err != nil {
		return contract.DigestProjection{}, fmt.Errorf("candidate descriptor at %s cannot be classified: %w", local, err)
	}
	profile := contract.SelectDigestProfile(mode)
	set, issues := contract.BuildCarriedSet(profile, core.Descriptor.Raw, descriptor, core.Files)
	if len(issues) != 0 {
		return contract.DigestProjection{}, fmt.Errorf("candidate at %s is not a valid declared carried set: %s", local, issues[0].Detail)
	}
	return set.ExportSource()
}

func inspectContractSnapshots(left, right []space.ContractSnapshotFile) contractInspectionResult {
	leftByPath, rightByPath := contractSnapshotMap(left), contractSnapshotMap(right)
	result := contractInspectionResult{added: []string{}, removed: []string{}, changed: []string{}, frontmatterChanged: []string{}}
	for name, leftFile := range leftByPath {
		if name == contract.DescriptorPath {
			continue
		}
		rightFile, ok := rightByPath[name]
		if !ok {
			result.removed = append(result.removed, name)
			continue
		}
		if leftFile.Mode != rightFile.Mode || !bytes.Equal(leftFile.Raw, rightFile.Raw) {
			result.changed = append(result.changed, name)
		}
	}
	for name := range rightByPath {
		if name == contract.DescriptorPath {
			continue
		}
		if _, ok := leftByPath[name]; !ok {
			result.added = append(result.added, name)
		}
	}
	result.frontmatterChanged = contractFrontmatterChanges(leftByPath[contract.DescriptorPath].Raw, rightByPath[contract.DescriptorPath].Raw)
	sort.Strings(result.added)
	sort.Strings(result.removed)
	sort.Strings(result.changed)
	return result
}

func contractSnapshotMap(files []space.ContractSnapshotFile) map[string]space.ContractSnapshotFile {
	out := make(map[string]space.ContractSnapshotFile, len(files))
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

func contractFrontmatterChanges(leftRaw, rightRaw []byte) []string {
	left, leftOK := decodeContractFrontmatter(leftRaw)
	right, rightOK := decodeContractFrontmatter(rightRaw)
	if !leftOK || !rightOK {
		return []string{"contract.md: frontmatter could not be compared"}
	}
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	out := make([]string, 0)
	for key := range keys {
		if reflect.DeepEqual(left[key], right[key]) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s -> %s", key, contractFrontmatterValue(left, key), contractFrontmatterValue(right, key)))
	}
	sort.Strings(out)
	return out
}

func decodeContractFrontmatter(raw []byte) (map[string]any, bool) {
	frontmatter, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, false
	}
	var value map[string]any
	if yaml.Unmarshal(frontmatter.YAML, &value) != nil {
		return nil, false
	}
	return value, true
}

func contractFrontmatterValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return "<absent>"
	}
	raw, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(string(raw))
}

type cliContractP6Adapter struct{ core *contractP6Core }

func (a cliContractP6Adapter) Preflight(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.preflight(ctx, contractP6PublicationInput{id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, allowEmptyBump: request.AllowEmptyBump})
}

func (a cliContractP6Adapter) Publish(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.publish(ctx, contractP6PublicationInput{
		id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, expectPlan: request.ExpectPlan,
		allowEmptyBump: request.AllowEmptyBump,
		actorKind:      request.Actor.Kind, actorName: request.Actor.Name, actorModel: request.Actor.Model,
	})
}

func (a cliContractP6Adapter) MaterializeContract(ctx context.Context, request cli.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return a.core.materialize(ctx, request.Ref, request.Destination)
}

func (a cliContractP6Adapter) CheckContract(ctx context.Context, request cli.ContractCheckRequest) (contract.ConformanceResult, error) {
	return a.core.check(ctx, request.Ref, request.PayloadPath, request.SchemaPath, request.Suite)
}

func (a cliContractP6Adapter) DiffContract(ctx context.Context, request cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	result, err := a.core.diff(ctx, request.ID, request.V1, request.V2)
	return cli.ContractDiffResult{Added: result.added, Removed: result.removed, Changed: result.changed, FrontmatterChanged: result.frontmatterChanged}, err
}

func (a cliContractP6Adapter) VerifyContractExport(ctx context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	result, err := a.core.verifyExport(ctx, request.Local, request.Ref)
	return cli.ContractVerifyExportResult{
		// There is no Matches to set: the JSON key is derived at the wire
		// boundary by cli.ContractVerifyExportResult's own MarshalJSON.
		// It USED to be set here, from the same outcome, with a comment
		// saying it was derived — and a fixture one package over set it
		// alone and left Outcome empty. See that type's doc comment.
		ID: result.id, Outcome: contractExportOutcomeWord(result.outcome),
		LocalDigest: result.localDigest, WantDigest: result.wantDigest,
		Diff: cli.ContractDiffResult{Added: result.diff.added, Removed: result.diff.removed, Changed: result.diff.changed, FrontmatterChanged: result.diff.frontmatterChanged},
	}, err
}

type mcpContractP6Adapter struct{ core *contractP6Core }

func (a mcpContractP6Adapter) Preflight(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.preflight(ctx, contractP6PublicationInput{id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, allowEmptyBump: request.AllowEmptyBump})
}

func (a mcpContractP6Adapter) Publish(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.publish(ctx, contractP6PublicationInput{
		id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, expectPlan: request.ExpectPlan,
		allowEmptyBump: request.AllowEmptyBump,
		actorKind:      request.Actor.Kind, actorName: request.Actor.Name, actorModel: request.Actor.Model,
	})
}

func (a mcpContractP6Adapter) MaterializeContract(ctx context.Context, request mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return a.core.materialize(ctx, request.Ref, request.Destination)
}

func (a mcpContractP6Adapter) CheckContract(ctx context.Context, request mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	return a.core.check(ctx, request.Ref, request.PayloadPath, request.SchemaPath, request.Suite)
}

func (a mcpContractP6Adapter) DiffContract(ctx context.Context, request mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	result, err := a.core.diff(ctx, request.ID, request.V1, request.V2)
	return mcp.ContractDiffResult{Added: result.added, Removed: result.removed, Changed: result.changed, FrontmatterChanged: result.frontmatterChanged}, err
}

func (a mcpContractP6Adapter) VerifyContractExport(ctx context.Context, request mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	result, err := a.core.verifyExport(ctx, request.Local, request.Ref)
	return mcp.ContractVerifyExportResult{
		ID: result.id, Outcome: contractExportOutcomeWord(result.outcome),
		LocalDigest: result.localDigest, WantDigest: result.wantDigest,
		Diff: mcp.ContractDiffResult{Added: result.diff.added, Removed: result.diff.removed, Changed: result.diff.changed, FrontmatterChanged: result.diff.frontmatterChanged},
	}, err
}

type mcpContractP6Router struct {
	bySpace map[string]*contractP6Core
}

func (r mcpContractP6Router) coreFor(spaceID string) (*contractP6Core, error) {
	if spaceID != "" {
		core, ok := r.bySpace[spaceID]
		if !ok {
			return nil, fmt.Errorf("a2a_contract: space %q is not connected", spaceID)
		}
		return core, nil
	}
	if len(r.bySpace) == 0 {
		return nil, fmt.Errorf("a2a_contract: no connected space")
	}
	if len(r.bySpace) > 1 {
		return nil, fmt.Errorf("a2a_contract: space is required when multiple spaces are connected")
	}
	for _, core := range r.bySpace {
		return core, nil
	}
	return nil, fmt.Errorf("a2a_contract: no connected space")
}

func (r mcpContractP6Router) adapter(spaceID string) (mcpContractP6Adapter, error) {
	core, err := r.coreFor(spaceID)
	return mcpContractP6Adapter{core: core}, err
}

func (r mcpContractP6Router) Preflight(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return adapter.Preflight(ctx, request)
}

func (r mcpContractP6Router) Publish(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return adapter.Publish(ctx, request)
}

func (r mcpContractP6Router) MaterializeContract(ctx context.Context, request mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	return adapter.MaterializeContract(ctx, request)
}

func (r mcpContractP6Router) CheckContract(ctx context.Context, request mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return contract.ConformanceResult{}, err
	}
	return adapter.CheckContract(ctx, request)
}

func (r mcpContractP6Router) DiffContract(ctx context.Context, request mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return mcp.ContractDiffResult{}, err
	}
	return adapter.DiffContract(ctx, request)
}

func (r mcpContractP6Router) VerifyContractExport(ctx context.Context, request mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return mcp.ContractVerifyExportResult{}, err
	}
	return adapter.VerifyContractExport(ctx, request)
}

type mcpContractP6Operations interface {
	mcp.ContractPublicationOperations
	mcp.ContractMaterializeOperation
	mcp.ContractCheckOperation
	mcp.ContractInspectionOperations
}

func contractMCPToolOperations(adapter mcpContractP6Operations) mcp.ContractToolOperations {
	return mcp.ContractToolOperations{Publication: adapter, Materialize: adapter, Check: adapter, Inspection: adapter}
}

var (
	_ space.ContractHistoryDocumentValidator = contractHistoryDocumentEngine{}
	_ cli.ContractPublicationOperations      = cliContractP6Adapter{}
	_ cli.ContractMaterializeOperation       = cliContractP6Adapter{}
	_ cli.ContractCheckOperation             = cliContractP6Adapter{}
	_ cli.ContractInspectionOperations       = cliContractP6Adapter{}
	_ mcp.ContractPublicationOperations      = mcpContractP6Adapter{}
	_ mcp.ContractMaterializeOperation       = mcpContractP6Adapter{}
	_ mcp.ContractCheckOperation             = mcpContractP6Adapter{}
	_ mcp.ContractInspectionOperations       = mcpContractP6Adapter{}
	_ mcpContractP6Operations                = mcpContractP6Router{}
)

// newCLIContractVerifyPublishedInspector builds one read-only inspection
// capability per connected space for `a2a contract verify-published`
// (spec 07 AC-7). It exists because ContractCommand is constructed bound to
// exactly ONE space — resolveContractDeps keys space resolution off an XC
// artifact id, and this verb names no artifact at all — so the aggregate
// verb cannot be reached through it without lying about what it checked.
//
// The per-space keying is deliberately the SAME shape
// newMCPContractOperationsFactory already uses for mcpContractP6Router.bySpace:
// one core per space.Ref, its mirror resolved by space.ResolveMirrorLocation,
// its credential by resolveCredential. Two surfaces answering one question
// must not answer it two ways.
//
// The actor resolver is wired rather than stubbed even though
// ContractInspectionOperations is read-only (DiffContract, VerifyContractExport
// — neither authors an event, so it is never reached). A closure that refuses
// would encode "this is read-only today" as a runtime crash tomorrow.
func newCLIContractVerifyPublishedInspector(p paths, cfg space.ProjectConfig, machine space.MachineConfig) cli.ContractVerifyPublishedSpaceInspector {
	resolve := actorResolver()
	return func(ref space.Ref, mirrorDir string) (cli.ContractInspectionOperations, error) {
		engine, err := newEngine()
		if err != nil {
			return nil, err
		}
		owner, name, err := parseGitHubRepo(ref.RepoURL)
		if err != nil {
			return nil, err
		}
		core, err := newContractP6Core(
			p.projectRoot, mirrorDir, ref.RepoURL, ref.ID, cfg.System,
			host.Repo{Owner: owner, Name: name}, cfg.System, cfg.System+"@a2a.local", funnelBinaryVersion(),
			engine, host.NewGitHubHost(http.DefaultClient, githubAPIBase()),
			func(kind, name, model string) (template.Actor, error) {
				return resolve(cli.ActorFlags{Kind: kind, Name: name, Model: model})
			},
			func(ctx context.Context) (host.Credential, error) { return resolveCredential(ctx, ref.ID, machine) },
		)
		if err != nil {
			return nil, err
		}
		return cliContractP6Adapter{core: core}, nil
	}
}
