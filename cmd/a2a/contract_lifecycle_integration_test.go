// contract_lifecycle_integration_test.go is the FIRST offline test that
// drives the production contract publication service end to end.
//
// Before this file, nothing offline ever executed contractP6Core.publish's
// own assembly (cmd/a2a/contract_p6_wiring.go): internal/e2e's own contract
// lifecycle test (contract_p6_fixture_test.go's newE2EContractPublisher)
// fakes publication entirely — it string-replaces the version and mints its
// own event, never touching space.NewContractPublicationService or
// contract.PlanPublication. The first place a publication defect ever
// surfaced was a 110-minute live GitHub run.
//
// This file is package main (not internal/e2e) specifically so it can reach
// the unexported production types contractP6Core.publish assembles
// (contractPublicationEventBuilder, contractPublicationSubmitValidator,
// contractManifestEngine, contractHistoryDocumentEngine — all defined in
// contract_p6_wiring.go) and wire them together itself, the same way
// contractP6Core.publish does, but with a fake host in place of
// contractP6Core's concrete *host.GitHubHost.
//
// The descriptor under test is never a hand-written literal: every contract
// drafted here goes through the REAL `a2a contract new` (cli.NewCommand,
// which itself calls template.RenderNew) and then the REAL `a2a submit`
// (cli.SubmitCommand), so the descriptor + schema + fixtures landing on
// main are byte-for-byte what the product actually produces. A hand-rolled
// fixture is exactly how the defect this file guards against stayed hidden
// (see TestContractLifecyclePublishRoundTrip's own doc comment).
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

const (
	contractLifecycleSystem  = "axon"
	contractLifecycleSpaceID = "fixture-space"
)

// TestContractLifecyclePublishRoundTrip is the crux case: it lands a
// product-rendered contract descriptor on main via a real `a2a contract
// new` + `a2a submit`, publishes 1.0.0 through the SAME assembly
// contractP6Core.publish uses (space.NewContractPublicationService, a real
// space.WriteFunnel and space.NewContractPublicationRecovery, both backed by
// host.NewFakeHost), merges that publish PR the way GitHub would, resolves
// the published version back with space.ResolveContractVersion, and then
// publishes 1.1.0 — which must succeed by resolving 1.0.0 as its
// compatibility baseline.
//
// Before commit 4b04317, a drafted descriptor could already read
// `version: 1.0.0` before it was ever published: publishing 1.0.0 then
// wrote byte-identical bytes, the publish commit carried only its event,
// and ResolveContractVersion failed forever with "matching event does not
// change descriptor". Nothing offline ever drove the resolve-back step, so
// nothing offline ever caught it. This test does.
func TestContractLifecyclePublishRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	contractLifecycleRaiseFloor(t, mirrorDir)
	stagingDir := t.TempDir()

	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	actor := template.Actor{Kind: "agent", Name: "lifecycle-tester"}
	resolveActor := func(cli.ActorFlags) (template.Actor, error) { return actor, nil }

	manifest, err := loadManifest(mirrorDir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}

	fakeHost := host.NewFakeHost()
	legality := cli.NewLegalityAdapter(mirrorDir, contractLifecycleSystem, manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	submitValidator := cli.NewSubmitValidatorAdapter(engine, contractLifecycleSystem, resolver, legality)
	submitFunnel := space.NewWriteFunnel(fakeHost, submitValidator, "0.19.0")
	hostCfg := contractLifecycleHostConfig(fx.RemoteURL())

	newCmd := cli.NewNewCommand(stagingDir, contractLifecycleSystem, resolveActor, []string{contractLifecycleSpaceID})
	contractCmd := cli.NewContractCommand(newCmd, submitFunnel, mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, manifest, hostCfg, resolveActor)
	submitCmd := cli.NewSubmitCommand(submitFunnel, legality, cli.NewNoopPendingMarker(), mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, stagingDir, hostCfg)

	// (1) `a2a contract new widget` + `a2a submit` — the real product path,
	// landing the rendered descriptor (plus its scaffolded schema/fixtures)
	// on main, exactly what §4.2 calls "submit".
	contractID := contractLifecycleDraftAndSubmit(t, contractCmd, submitCmd, stagingDir, "widget")
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR after submit, got %d", len(fakeHost.Opens))
	}
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	// (2) Publish 1.0.0 through the exact assembly contractP6Core.publish
	// uses, with a fake host standing in for *host.GitHubHost.
	result, err := contractLifecyclePublish(t, mirrorDir, fx.RemoteURL(), engine, fakeHost, actor, contractID, "1.0.0")
	if err != nil {
		t.Fatalf("publish 1.0.0: %v", err)
	}
	if result.Status != space.ContractPublicationSubmitted {
		t.Fatalf("publish 1.0.0 status = %q, want %q", result.Status, space.ContractPublicationSubmitted)
	}
	if !result.Plan.FirstPublish || result.Plan.BaselineVersion != "" {
		t.Fatalf("publish 1.0.0 plan = %+v, want a first publication with no baseline", result.Plan)
	}
	if len(fakeHost.Opens) != 2 {
		t.Fatalf("expected exactly two OpenPR calls after publishing 1.0.0, got %d", len(fakeHost.Opens))
	}
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	// (3) Resolve the just-published version back. This is the step the
	// live tier's own 110-minute run was the first place ever exercising —
	// see this test's own doc comment.
	snapshot, err := space.ResolveContractVersion(ctx, mirrorDir, contractID, "1.0.0", contractHistoryDocumentEngine{engine: engine})
	if err != nil {
		t.Fatalf("ResolveContractVersion(%s@1.0.0): %v", contractID, err)
	}
	if snapshot.ContractID != contractID || snapshot.Version != "1.0.0" {
		t.Fatalf("resolved snapshot = %+v, want contract=%s version=1.0.0", snapshot, contractID)
	}

	// (4) Publish 1.1.0. This must resolve 1.0.0 as its compatibility
	// baseline, which only works if step (3)'s resolve-back proves the
	// history is actually readable.
	result2, err := contractLifecyclePublish(t, mirrorDir, fx.RemoteURL(), engine, fakeHost, actor, contractID, "1.1.0")
	if err != nil {
		t.Fatalf("publish 1.1.0: %v", err)
	}
	if result2.Status != space.ContractPublicationSubmitted {
		t.Fatalf("publish 1.1.0 status = %q, want %q", result2.Status, space.ContractPublicationSubmitted)
	}
	// The crux assertion this test exists for: 1.1.0 is NOT a first
	// publication, and its baseline is the 1.0.0 this test just resolved
	// back in step (3) — proof the compatibility check actually read the
	// just-merged publish history rather than treating 1.1.0 as establishing.
	if result2.Plan.FirstPublish || result2.Plan.BaselineVersion != "1.0.0" {
		t.Fatalf("publish 1.1.0 plan = %+v, want FirstPublish=false BaselineVersion=1.0.0", result2.Plan)
	}
	if len(fakeHost.Opens) != 3 {
		t.Fatalf("expected exactly three OpenPR calls after publishing 1.1.0, got %d", len(fakeHost.Opens))
	}
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	snapshot2, err := space.ResolveContractVersion(ctx, mirrorDir, contractID, "1.1.0", contractHistoryDocumentEngine{engine: engine})
	if err != nil {
		t.Fatalf("ResolveContractVersion(%s@1.1.0): %v", contractID, err)
	}
	if snapshot2.ContractID != contractID || snapshot2.Version != "1.1.0" {
		t.Fatalf("resolved snapshot = %+v, want contract=%s version=1.1.0", snapshot2, contractID)
	}
}

// TestContractLifecyclePublishRefusesNonEstablishing is the regression
// guard: a drafted descriptor whose `version:` field already reads the
// version about to be published must refuse — refuseNonEstablishingPublication
// (internal/space/contract_publication.go) — and must write nothing at all.
//
// `a2a contract new` always drafts `version: 0.0.0`; this case forces the
// historical defect shape directly with `--field version=1.0.0`, the same
// shape TestContractLifecyclePublishRoundTrip's own doc comment names as the
// pre-4b04317 bug.
func TestContractLifecyclePublishRefusesNonEstablishing(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	contractLifecycleRaiseFloor(t, mirrorDir)
	stagingDir := t.TempDir()

	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	actor := template.Actor{Kind: "agent", Name: "lifecycle-tester"}
	resolveActor := func(cli.ActorFlags) (template.Actor, error) { return actor, nil }

	manifest, err := loadManifest(mirrorDir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}

	fakeHost := host.NewFakeHost()
	legality := cli.NewLegalityAdapter(mirrorDir, contractLifecycleSystem, manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	submitValidator := cli.NewSubmitValidatorAdapter(engine, contractLifecycleSystem, resolver, legality)
	submitFunnel := space.NewWriteFunnel(fakeHost, submitValidator, "0.19.0")
	hostCfg := contractLifecycleHostConfig(fx.RemoteURL())

	newCmd := cli.NewNewCommand(stagingDir, contractLifecycleSystem, resolveActor, []string{contractLifecycleSpaceID})
	contractCmd := cli.NewContractCommand(newCmd, submitFunnel, mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, manifest, hostCfg, resolveActor)
	submitCmd := cli.NewSubmitCommand(submitFunnel, legality, cli.NewNoopPendingMarker(), mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, stagingDir, hostCfg)

	// The drafted descriptor already reads the version this test is about
	// to publish — main carries exactly that byte content the moment submit
	// lands it, with no publish history establishing it at all.
	contractID := contractLifecycleDraftAndSubmit(t, contractCmd, submitCmd, stagingDir, "gadget", "version=1.0.0")
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR after submit, got %d", len(fakeHost.Opens))
	}
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	opensBeforePublish, pushesBeforePublish := len(fakeHost.Opens), len(fakeHost.Pushes)
	_, err = contractLifecyclePublish(t, mirrorDir, fx.RemoteURL(), engine, fakeHost, actor, contractID, "1.0.0")
	if !errors.Is(err, space.ErrContractPublicationNotEstablishing) {
		t.Fatalf("publish 1.0.0 err = %v, want errors.Is(..., space.ErrContractPublicationNotEstablishing)", err)
	}
	// The refusal (refuseNonEstablishingPublication) fires inside planning,
	// strictly before PrepareSubmission ever runs — so NEITHER a push nor an
	// OpenPR should have happened for this publish attempt.
	if len(fakeHost.Opens) != opensBeforePublish {
		t.Fatalf("the refused publish still opened a PR: before=%d after=%d", opensBeforePublish, len(fakeHost.Opens))
	}
	if len(fakeHost.Pushes) != pushesBeforePublish {
		t.Fatalf("the refused publish still pushed a branch: before=%d after=%d", pushesBeforePublish, len(fakeHost.Pushes))
	}
}

// contractLifecycleRaiseFloor rewrites mirrorDir's own space.yaml
// min_binary_version from spacefixture's default "0.0.0" up to
// contract.ContractPublicationFloor ("0.19.0") and pushes the change to
// origin, then re-syncs mirrorDir onto it. A JSON-Schema contract's
// template renders a declared-v2 `artifacts:` inventory (internal/template/
// schemas/templates/v2/contract.md), and contract.PlanPublication requires
// exactly that inventory mode when the authoring floor is at or above the
// publication floor (internal/contract/publication_plan.go's own
// "authoring floor requires declared-v2 candidate inventory" guard) — a
// floor below it instead demands the legacy fixed-v1 inventory, which this
// file's product-rendered descriptor never carries.  internal/space's own
// contract_operation_recovery_test.go raises the same floor for the same
// reason, by editing the same field the same way.
// Raising the floor is also load-bearing for acceptance: contract.
// PlanPublication (internal/contract/publication_plan.go) hard-requires
// InventoryDeclaredV2 at or above the floor and refuses anything else, so
// TestContractLifecyclePublishRoundTrip's own descriptor genuinely must be
// the product's real declared-v2 template.RenderNew output to pass at all
// — a hand-rolled legacy-shaped candidate would fail this same guard, not
// silently succeed under a different profile.
func contractLifecycleRaiseFloor(t *testing.T, mirrorDir string) {
	t.Helper()
	manifestPath := filepath.Join(mirrorDir, "space.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read space.yaml: %v", err)
	}
	const from, to = `min_binary_version: "0.0.0"`, `min_binary_version: "` + contract.ContractPublicationFloor + `"`
	updated := strings.Replace(string(raw), from, to, 1)
	if updated == string(raw) {
		t.Fatalf("space.yaml does not contain %q to raise; got:\n%s", from, raw)
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
	contractLifecycleGit(t, mirrorDir, "add", "space.yaml")
	contractLifecycleGit(t, mirrorDir, "commit", "-m", "test: raise the publication authoring floor")
	contractLifecycleGit(t, mirrorDir, "push", "origin", "main")
}

// contractLifecycleHostConfig is this file's own copy of the
// SubmitHostConfig every write path in this file needs (internal/e2e's own
// e2eHostConfig idiom, copied rather than imported — internal/e2e is a
// different test package this file cannot reach into).
func contractLifecycleHostConfig(remoteURL string) cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL: remoteURL, Repo: host.Repo{Owner: "fixture", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-" + contractLifecycleSystem, CommitAuthorEmail: "a2a-" + contractLifecycleSystem + "@a2ahub.invalid",
	}
}

// contractLifecycleDraftAndSubmit drives the REAL `a2a contract new <slug>`
// (which delegates to cli.NewCommand, which renders through
// template.RenderNew — the product's own template selection, never a
// hand-written descriptor literal) and then the REAL `a2a submit`, landing
// the rendered descriptor plus its scaffolded schema/fixtures on main. It
// returns the minted contract id.
func contractLifecycleDraftAndSubmit(t *testing.T, contractCmd *cli.ContractCommand, submitCmd *cli.SubmitCommand, stagingDir, slug string, extraFields ...string) string {
	t.Helper()
	args := []string{"new", slug, "--field", "to=beta", "--field", "title=" + slug + " contract"}
	for _, field := range extraFields {
		args = append(args, "--field", field)
	}

	io, out, errOut := contractLifecycleIO()
	if code := contractCmd.Run(context.Background(), args, io); code != 0 {
		t.Fatalf("contract new %s: code = %d, want 0; stdout=%s stderr=%s", slug, code, out.String(), errOut.String())
	}

	id := "XC-" + contractLifecycleSystem + "-" + slug
	draftPath := filepath.Join(stagingDir, id+".md")
	if _, statErr := os.Stat(draftPath); statErr != nil {
		t.Fatalf("contract new %s did not stage %s: %v", slug, draftPath, statErr)
	}

	io2, out2, errOut2 := contractLifecycleIO()
	if code := submitCmd.Run(context.Background(), []string{draftPath}, io2); code != 0 {
		t.Fatalf("submit %s: code = %d, want 0; stdout=%s stderr=%s", id, code, out2.String(), errOut2.String())
	}
	return id
}

// contractLifecyclePublish assembles space.NewContractPublicationService the
// SAME way contractP6Core.publish does (cmd/a2a/contract_p6_wiring.go:154-193)
// — the same repository, event builder, submit validator, funnel and
// recovery types — except with a fake host in place of contractP6Core's
// concrete *host.GitHubHost, and calls Publish once. It is rebuilt on every
// call, matching production: contractP6Core.publish constructs a fresh
// repository/events/validator/funnel/recovery on every invocation, never
// reusing one across publishes.
func contractLifecyclePublish(
	t *testing.T, mirrorDir, remoteURL string, engine *validate.Engine,
	fakeHost *host.FakeHost, actor template.Actor, contractID, targetVersion string,
) (space.ContractPublicationResult, error) {
	t.Helper()
	ctx := context.Background()

	manifest, err := loadManifest(mirrorDir)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}

	slug := contractID[len("XC-"+contractLifecycleSystem+"-"):]
	candidate, source, err := contractLifecycleFreezeMirrorCandidate(ctx, mirrorDir, slug)
	if err != nil {
		t.Fatalf("freeze publication candidate: %v", err)
	}

	repository, err := space.NewContractPublicationRepository(
		mirrorDir, remoteURL, host.Credential{}, contractManifestEngine{engine: engine}, contractHistoryDocumentEngine{engine: engine},
	)
	if err != nil {
		t.Fatalf("NewContractPublicationRepository: %v", err)
	}

	loadManifestFn := func() (space.Manifest, error) { return loadManifest(mirrorDir) }
	events := &contractPublicationEventBuilder{
		mirrorDir: mirrorDir, spaceID: contractLifecycleSpaceID, ownSystem: contractLifecycleSystem,
		loadManifest: loadManifestFn, actor: actor, engine: engine, now: time.Now, entropy: rand.Reader,
	}
	validator := &contractPublicationSubmitValidator{
		events: events, engine: engine, mirrorDir: mirrorDir,
		ownSystem: contractLifecycleSystem, loadManifest: loadManifestFn,
	}

	// contractLifecycleRecoveryHost is this file's own stand-in for
	// contractP6Core's single concrete *host.GitHubHost, used for BOTH the
	// funnel and the recovery adapter below — see its own doc comment for
	// why a bare host.FakeHost cannot satisfy either on its own once a
	// publication's PreparationContext carries Recovery (every contract
	// publish does).
	recoveryHost := contractLifecycleRecoveryHost{FakeHost: fakeHost, git: host.NewGitHubHost(nil, "")}
	funnel := space.NewWriteFunnel(recoveryHost, validator, "0.19.0")

	recovery, err := space.NewContractPublicationRecovery(
		recoveryHost, mirrorDir, remoteURL, host.Repo{Owner: "fixture", Name: "space"}, host.Credential{Token: "test-token"},
		space.ContractPublicationRecoveryValidation{
			ManifestValidator: contractManifestEngine{engine: engine},
			HistoryValidator:  contractHistoryDocumentEngine{engine: engine},
			Compatibility:     validate.ContractCompatibilityAdapter{},
		},
	)
	if err != nil {
		t.Fatalf("NewContractPublicationRecovery: %v", err)
	}

	service, err := space.NewContractPublicationService(repository, repository, recovery, validate.ContractCompatibilityAdapter{}, events, funnel)
	if err != nil {
		t.Fatalf("NewContractPublicationService: %v", err)
	}

	request := space.ContractPublicationRequest{
		System: contractLifecycleSystem, ContractID: contractID, Selector: "explicit:" + targetVersion,
		Candidate: candidate, CandidateSource: source, ProducerCompatibility: "0.19.0",
		SubmitTemplate: space.SubmitRequest{
			RepoDir: mirrorDir, RemoteURL: remoteURL, Repo: host.Repo{Owner: "fixture", Name: "space"},
			MinBinaryVersion: manifest.MinBinaryVersion,
			CommitMessage:    "a2a(contract-publish): " + contractID,
			CommitAuthorName: "a2a-" + contractLifecycleSystem, CommitAuthorEmail: "a2a-" + contractLifecycleSystem + "@a2ahub.invalid",
			PRTitle: "Publish " + contractID, PRBody: "Publish an exact immutable contract version.",
			Credential: host.Credential{Token: "test-token"},
		},
		SubmissionRuntime: space.SubmissionRuntime{RepoDir: mirrorDir, RemoteURL: remoteURL, Credential: host.Credential{Token: "test-token"}},
	}
	return service.Publish(ctx, request)
}

// contractLifecycleFreezeMirrorCandidate is this file's own copy of
// contractP6Core.freezePublicationCandidate's no-staging-overlay branch
// (cmd/a2a/contract_p6_wiring.go:256-286): resolve the mirror's own checked-
// out HEAD, then freeze the candidate rooted at the contract's provides/
// directory against that exact Git tree.
func contractLifecycleFreezeMirrorCandidate(ctx context.Context, mirrorDir, slug string) (space.ContractPublicationCandidateReader, contract.CandidateSource, error) {
	layout, err := space.NewLayout(contractLifecycleSystem)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	contractRoot := path.Dir(layout.ProvidesContract(slug))
	commit, err := space.ResolveContractPublicationCandidateCommit(ctx, mirrorDir)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	reader, err := space.OpenContractCandidateReader(mirrorDir, space.ContractCandidateLocation{Path: contractRoot, Source: space.ContractCandidateSourceMirror})
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	frozen, freezeErr := space.NewFrozenContractPublicationCandidate(ctx, reader, space.ContractPublicationMirrorTree{
		RepoDir: mirrorDir, Commit: commit, Path: contractRoot,
	})
	closeErr := reader.Close()
	if freezeErr != nil {
		return nil, contract.CandidateSource{}, freezeErr
	}
	if closeErr != nil {
		return nil, contract.CandidateSource{}, closeErr
	}
	return frozen, frozen.ContractPublicationCandidateSource(), nil
}

// contractLifecycleRecoveryHost embeds *host.FakeHost (which already
// satisfies host.Host in full — PushBranch, OpenPR, CheckStatus,
// TokenScopes, ReviewStatus, FindPRByHeadBranch — plus the optional
// AutoMerger capability space.WriteFunnel's idempotent-retry path uses) and
// adds the four git-only remote-tree-reading methods
// space.NewContractPublicationRecovery additionally requires
// (ListContractPublishHeads, ReadRemoteRecoveryCommit,
// ReadRemoteRecoveryTreeFiles, ListRemoteRecoveryTreeFiles) — none of which
// FakeHost implements — by forwarding to a real *host.GitHubHost. Those
// four are pure local `git ls-remote`/`git show` plumbing against repoDir/
// remoteURL, which are local filesystem paths in every case this file
// builds, so no network call is ever made through them; this is this
// package's own copy of the split internal/space's own recovery test uses
// (contract_operation_recovery_test.go's contractRecoveryTestHost).
//
// One instance of this type is used for BOTH the funnel's host and the
// recovery adapter's host, mirroring contractP6Core's own single concrete
// *host.GitHubHost field serving both roles (cmd/a2a/contract_p6_wiring.go:
// 179-184). A bare *host.FakeHost cannot serve either role on its own the
// moment a publication's PreparationContext carries Recovery (every
// contract publish does, per bindContractPublicationSubmission): the
// funnel type-asserts its OWN host against this same four-method reader
// interface whenever prepared.data.recoveryJSON is non-empty
// (internal/space/funnel.go:566), and Publish() calls
// ProbeContractPublicationHeads unconditionally on every fresh publication
// (internal/space/contract_publication.go:678) — see this file's own
// deviations note.
type contractLifecycleRecoveryHost struct {
	*host.FakeHost
	git *host.GitHubHost
}

func (h contractLifecycleRecoveryHost) ListContractPublishHeads(ctx context.Context, repoDir, remoteURL, prefix string, credential host.Credential) (host.RemoteHeadListing, error) {
	return h.git.ListContractPublishHeads(ctx, repoDir, remoteURL, prefix, credential)
}

func (h contractLifecycleRecoveryHost) ReadRemoteRecoveryCommit(ctx context.Context, repoDir, remoteURL string, repository host.Repo, branch string, credential host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	return h.git.ReadRemoteRecoveryCommit(ctx, repoDir, remoteURL, repository, branch, credential)
}

func (h contractLifecycleRecoveryHost) ReadRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, paths []string) (map[string]host.RemoteTreeFile, error) {
	return h.git.ReadRemoteRecoveryTreeFiles(ctx, repoDir, commit, paths)
}

func (h contractLifecycleRecoveryHost) ListRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, prefixes []string) (map[string]host.RemoteTreeFile, error) {
	return h.git.ListRemoteRecoveryTreeFiles(ctx, repoDir, commit, prefixes)
}

// lastOpenedBranch returns the Head branch of the most recently recorded
// OpenPR call (internal/e2e's own e2e1_test.go idiom, copied rather than
// imported — internal/e2e is a different test package this file cannot
// reach into).
func lastOpenedBranch(fakeHost *host.FakeHost) string {
	return fakeHost.Opens[len(fakeHost.Opens)-1].Head
}

// contractLifecycleMergeToMain simulates GitHub's own auto-merge the way
// internal/e2e's mergeBranchToMain does (its own doc comment explains the
// full rationale): fast-forwards main to origin's latest tip, merges branch
// (the funnel's own local commit) into it, and pushes. This file cannot
// import internal/e2e's helper (a different test package), so this is its
// own local copy — plus one extra `fetch origin main` after the push, which
// this file's own resolve-back step (space.ResolveContractVersion, which
// reads refs/remotes/origin/main) needs to be certain about rather than
// relying on push's own opportunistic tracking-ref update.
func contractLifecycleMergeToMain(t *testing.T, mirrorDir, branch string) {
	t.Helper()
	contractLifecycleGit(t, mirrorDir, "fetch", "origin", "main")
	contractLifecycleGit(t, mirrorDir, "checkout", "main")
	contractLifecycleGit(t, mirrorDir, "reset", "--hard", "origin/main")
	contractLifecycleGit(t, mirrorDir, "merge", "--no-ff", "-m", "merge: "+branch, branch)
	contractLifecycleGit(t, mirrorDir, "push", "origin", "main")
	contractLifecycleGit(t, mirrorDir, "fetch", "origin", "main")
}

func contractLifecycleGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
}

// contractLifecycleIO builds the injected stdout/stderr capture pair every
// cli.Command test needs (internal/e2e's own newIO idiom, copied rather
// than imported).
func contractLifecycleIO() (cli.IO, *contractLifecycleBuffer, *contractLifecycleBuffer) {
	out, errOut := &contractLifecycleBuffer{}, &contractLifecycleBuffer{}
	return cli.IO{Stdin: nil, Stdout: out, Stderr: errOut}, out, errOut
}

// contractLifecycleBuffer is a minimal io.Writer + String() capture, so this
// file needs no bytes import solely for that pairing.
type contractLifecycleBuffer struct{ data []byte }

func (b *contractLifecycleBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *contractLifecycleBuffer) String() string { return string(b.data) }
