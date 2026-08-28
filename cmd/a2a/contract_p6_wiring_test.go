package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
)

func TestContractTargetArgsSelectsOnlyXCArtifact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "publish flags before id", args: []string{"publish", "--version", "1.2.3", "XC-beta-orders"}, want: []string{"XC-beta-orders"}},
		{name: "exact reference", args: []string{"materialize", "--to", "generated", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "staging value cannot masquerade", args: []string{"publish", "--staging", "XC-beta-wrong", "--version", "1.2.3", "XC-axon-orders"}, want: []string{"XC-axon-orders"}},
		{name: "payload value cannot masquerade", args: []string{"check", "--payload", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "destination value cannot masquerade", args: []string{"materialize", "--to", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "local value cannot masquerade", args: []string{"verify-export", "--local", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "equals flag value cannot masquerade", args: []string{"publish", "--staging=XC-beta-wrong", "XC-axon-orders", "--version=1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "new slug is not target", args: []string{"new", "orders"}},
		{name: "subcommand only", args: []string{"diff"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := contractTargetArgs(test.args); !slices.Equal(got, test.want) {
				t.Fatalf("contractTargetArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestMCPContractP6RouterRequiresPerRequestSpaceAcrossMultipleConnections(t *testing.T) {
	t.Parallel()

	orders := &contractP6Core{spaceID: "orders"}
	billing := &contractP6Core{spaceID: "billing"}
	router := mcpContractP6Router{bySpace: map[string]*contractP6Core{"orders": orders, "billing": billing}}
	if got, err := router.coreFor("orders"); err != nil || got != orders {
		t.Fatalf("explicit orders route = %p, %v", got, err)
	}
	if _, err := router.coreFor(""); err == nil || !strings.Contains(err.Error(), "space is required when multiple spaces are connected") {
		t.Fatalf("ambiguous route error = %v", err)
	}
	if _, err := router.coreFor("missing"); err == nil || !strings.Contains(err.Error(), `space "missing" is not connected`) {
		t.Fatalf("unknown route error = %v", err)
	}
	single := mcpContractP6Router{bySpace: map[string]*contractP6Core{"orders": orders}}
	if got, err := single.coreFor(""); err != nil || got != orders {
		t.Fatalf("single-space default route = %p, %v", got, err)
	}
	if _, err := (mcpContractP6Router{}).coreFor(""); err == nil || !strings.Contains(err.Error(), "no connected space") {
		t.Fatalf("empty route error = %v", err)
	}
}

func TestReadBoundedProjectFileEnforcesContainmentAndLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "payloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "payloads", "valid.json"), []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "too-large.json"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "payloads", "valid.json"), filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}

	raw, err := readBoundedProjectFile(root, "payloads/valid.json", 8)
	if err != nil || string(raw) != `{"id":1}` {
		t.Fatalf("bounded valid read = %q, %v", raw, err)
	}
	for _, name := range []string{"../outside.json", "./payloads/valid.json", "/tmp/outside.json", "linked.json", "too-large.json"} {
		if _, err := readBoundedProjectFile(root, name, 4); err == nil {
			t.Fatalf("unsafe or oversized path %q was accepted", name)
		}
	}
}

func TestMaterializeContractClosesHeldRootOnEveryOutcome(t *testing.T) {
	t.Parallel()

	operationErr := fmt.Errorf("materialize failed")
	closeErr := fmt.Errorf("close failed")
	for _, test := range []struct {
		name      string
		operation error
		close     error
		wantErr   string
	}{
		{name: "success"},
		{name: "operation failure", operation: operationErr, wantErr: operationErr.Error()},
		{name: "close failure", close: closeErr, wantErr: "close contract materializer: " + closeErr.Error()},
		{name: "both failures", operation: operationErr, close: closeErr, wantErr: operationErr.Error() + "\nclose contract materializer: " + closeErr.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			materializer := &contractMaterializeCapabilityFake{operationErr: test.operation, closeErr: test.close}
			_, err := materializeContractAndClose(t.Context(), materializer, space.HistoricalSnapshot{}, "generated")
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != test.wantErr {
				t.Fatalf("materialize error = %q, want %q", got, test.wantErr)
			}
			if materializer.materializeCalls != 1 || materializer.closeCalls != 1 {
				t.Fatalf("calls = materialize %d close %d, want 1 each", materializer.materializeCalls, materializer.closeCalls)
			}
		})
	}
}

type contractMaterializeCapabilityFake struct {
	operationErr     error
	closeErr         error
	materializeCalls int
	closeCalls       int
}

func (f *contractMaterializeCapabilityFake) Materialize(context.Context, space.HistoricalSnapshot, string) (space.ContractMaterializeResult, error) {
	f.materializeCalls++
	return space.ContractMaterializeResult{}, f.operationErr
}

func (f *contractMaterializeCapabilityFake) Close() error {
	f.closeCalls++
	return f.closeErr
}

func TestContractHistoryDocumentEngineUsesCanonicalSchemas(t *testing.T) {
	t.Parallel()

	engine, err := newEngine()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := os.ReadFile(filepath.Join("..", "..", "schemas", "envelope", "v2", "fixtures", "valid", "XC-axon-order-api.md"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := os.ReadFile(filepath.Join("..", "..", "schemas", "event", "v2", "fixtures", "valid", "contract-publish.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator := contractHistoryDocumentEngine{engine: engine}
	documents := space.ContractHistoryDocuments{
		Descriptor:   space.ContractHistoryDocument{Path: "axon/provides/order-api/contract.md", Schema: "envelope/v2", Raw: descriptor},
		PublishEvent: space.ContractHistoryDocument{Path: "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml", Schema: "event/v2", Raw: event},
	}
	if err := validator.ValidateHistoricalContractDocuments(t.Context(), documents); err != nil {
		t.Fatalf("canonical historical documents rejected: %v", err)
	}
	documents.PublishEvent.Raw = []byte(strings.Replace(string(event), `"schema": "event/v2"`, `"schema": "event/v9"`, 1))
	if err := validator.ValidateHistoricalContractDocuments(t.Context(), documents); err == nil {
		t.Fatal("unknown historical event schema was accepted")
	}
}

// TestContractExportOutcomeWordMapsUnmeasuredOntoSharedSeverity is D9's
// render boundary (spec P2 ACs 5/6/17): ExportUnmeasured must render as the
// SHIPPED validate.SeverityUnmeasured word, never a bespoke fourth verdict.
func TestContractExportOutcomeWordMapsUnmeasuredOntoSharedSeverity(t *testing.T) {
	t.Parallel()
	for outcome, want := range map[contract.ExportVerification]string{
		contract.ExportMatched:    "matched",
		contract.ExportDrifted:    "drifted",
		contract.ExportUnmeasured: string(validate.SeverityUnmeasured),
	} {
		if got := contractExportOutcomeWord(outcome); got != want {
			t.Fatalf("contractExportOutcomeWord(%q) = %q, want %q", outcome, got, want)
		}
	}
	if string(validate.SeverityUnmeasured) != string(contract.ExportUnmeasured) {
		t.Fatalf("D9's own premise broke: validate.SeverityUnmeasured=%q contract.ExportUnmeasured=%q — the mapping is no longer a value proof", validate.SeverityUnmeasured, contract.ExportUnmeasured)
	}
}

// contractCopyTree copies src (a directory) onto dst, both project-relative
// paths already resolved to absolute directories by the caller.
func contractCopyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, raw, 0o644)
	}); err != nil {
		t.Fatalf("copy contract tree %s -> %s: %v", src, dst, err)
	}
}

// contractInjectGeneratedFrom rewrites path's frontmatter to assert
// generated_from.source_digest = digest, preserving every other field and
// the body verbatim.
func contractInjectGeneratedFrom(t *testing.T, path, digest string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("parse frontmatter %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		t.Fatalf("decode frontmatter %s: %v", path, err)
	}
	doc["generated_from"] = map[string]any{"tool": "verify-export-test/1.0", "source_digest": digest}
	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("encode frontmatter %s: %v", path, err)
	}
	if err := os.WriteFile(path, artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// contractDriftOneFile mutates the first *.json file found under dir (a
// schema or fixture leaf, never contract.md — the descriptor plays no part
// in export-source-v1) so its recomputed digest changes deterministically.
func contractDriftOneFile(t *testing.T, dir string) {
	t.Helper()
	mutated := false
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || mutated || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.WriteFile(path, append(raw, ' '), 0o644); writeErr != nil {
			return writeErr
		}
		mutated = true
		return nil
	}); err != nil {
		t.Fatalf("drift one file under %s: %v", dir, err)
	}
	if !mutated {
		t.Fatalf("no .json file found to drift under %s", dir)
	}
}

// TestContractVerifyExportRoundTrip drives verifyExport/verifyExportLocalDigest
// against a real submit + (hand-amended, still-unpublished) generated_from +
// publish sequence, covering P2's AC-5 (matched), AC-6/AC-7 (unmeasured),
// AC-9 (the selected V2 profile, never hardcoded) and AC-13 (a partial
// staging overlay reads the same candidate bytes publish would).
func TestContractVerifyExportRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	contractLifecycleRaiseFloor(t, mirrorDir)
	draftStagingDir := t.TempDir()

	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	actor := template.Actor{Kind: "agent", Name: "verify-tester"}
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

	newCmd := cli.NewNewCommand(draftStagingDir, contractLifecycleSystem, resolveActor, []string{contractLifecycleSpaceID})
	contractCmd := cli.NewContractCommand(newCmd, submitFunnel, mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, manifest, hostCfg, resolveActor)
	submitCmd := cli.NewSubmitCommand(submitFunnel, legality, cli.NewNoopPendingMarker(), mirrorDir, contractLifecycleSpaceID, contractLifecycleSystem, draftStagingDir, hostCfg)

	contractID := contractLifecycleDraftAndSubmit(t, contractCmd, submitCmd, draftStagingDir, "gizmo")
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	core := &contractP6Core{projectRoot: t.TempDir(), mirrorDir: mirrorDir, ownSystem: contractLifecycleSystem, engine: engine}

	slug := strings.TrimPrefix(contractID, "XC-"+contractLifecycleSystem+"-")
	mirrorContractDir := filepath.Join(mirrorDir, contractLifecycleSystem, "provides", slug)
	descriptorRel := filepath.Join(contractLifecycleSystem, "provides", slug, "contract.md")

	// (1) Fresh candidate, no generated_from: UNMEASURED, and its localDigest
	// is a2a's own computation — what a real producer reads before writing
	// generated_from into their own descriptor (this verb's whole purpose).
	unmeasuredDir := "unmeasured-candidate"
	contractCopyTree(t, mirrorContractDir, filepath.Join(core.projectRoot, unmeasuredDir))
	unmeasured, err := core.verifyExportLocalDigest(ctx, unmeasuredDir, contractID)
	if err != nil {
		t.Fatalf("verifyExportLocalDigest (no generated_from): %v", err)
	}
	if unmeasured.outcome != contract.ExportUnmeasured || unmeasured.wantDigest != "" || unmeasured.localDigest == "" {
		t.Fatalf("unmeasured result = %+v, want outcome=unmeasured wantDigest=\"\" localDigest!=\"\"", unmeasured)
	}

	// (2) Amend the MIRROR's own contract.md (still unpublished, version
	// 0.0.0) to assert generated_from.source_digest = the digest step (1)
	// just computed, and land it directly on origin/main — simulating a
	// producer editing contract.md by hand between `a2a submit` and
	// `a2a contract publish`, exactly the workflow the field exists for.
	contractInjectGeneratedFrom(t, filepath.Join(mirrorDir, descriptorRel), unmeasured.localDigest)
	contractLifecycleGit(t, mirrorDir, "add", descriptorRel)
	contractLifecycleGit(t, mirrorDir, "commit", "-m", "test: assert generated_from.source_digest")
	contractLifecycleGit(t, mirrorDir, "push", "origin", "main")

	// (3) Bare-id verify against a FULL local copy of the now-amended
	// descriptor: MATCHED.
	matchedDir := "matched-candidate"
	contractCopyTree(t, mirrorContractDir, filepath.Join(core.projectRoot, matchedDir))
	matched, err := core.verifyExportLocalDigest(ctx, matchedDir, contractID)
	if err != nil {
		t.Fatalf("verifyExportLocalDigest (matched): %v", err)
	}
	if matched.outcome != contract.ExportMatched || matched.wantDigest != unmeasured.localDigest || matched.localDigest != unmeasured.localDigest {
		t.Fatalf("matched result = %+v, want outcome=matched wantDigest=localDigest=%s", matched, unmeasured.localDigest)
	}

	// (4) AC-13: a PARTIAL overlay — only the amended contract.md, schema/
	// and fixtures read from the mirror — must succeed identically. This is
	// exactly the shape publish already accepts (mirror overlaid with
	// staging) and verify used to refuse (staging read ALONE, which cannot
	// see the declared schema/fixtures this partial overlay omits).
	partialDir := "partial-overlay-candidate"
	if err := os.MkdirAll(filepath.Join(core.projectRoot, partialDir), 0o755); err != nil {
		t.Fatalf("mkdir partial overlay dir: %v", err)
	}
	amendedDescriptor, err := os.ReadFile(filepath.Join(mirrorDir, descriptorRel))
	if err != nil {
		t.Fatalf("read amended descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(core.projectRoot, partialDir, "contract.md"), amendedDescriptor, 0o644); err != nil {
		t.Fatalf("write partial overlay descriptor: %v", err)
	}
	partial, err := core.verifyExportLocalDigest(ctx, partialDir, contractID)
	if err != nil {
		t.Fatalf("verifyExportLocalDigest (partial overlay): %v", err)
	}
	if partial.outcome != contract.ExportMatched || partial.localDigest != unmeasured.localDigest {
		t.Fatalf("partial overlay result = %+v, want outcome=matched localDigest=%s", partial, unmeasured.localDigest)
	}

	// (5) Publish 1.0.0 from the amended mirror content (no staging — the
	// producer already landed the amendment via (2) above), then verify the
	// @version path against it: MATCHED.
	published, err := contractLifecyclePublish(t, mirrorDir, fx.RemoteURL(), engine, fakeHost, actor, contractID, "1.0.0", false)
	if err != nil {
		t.Fatalf("publish 1.0.0: %v", err)
	}
	if published.Status != space.ContractPublicationSubmitted {
		t.Fatalf("publish 1.0.0 status = %q, want %q", published.Status, space.ContractPublicationSubmitted)
	}
	contractLifecycleMergeToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	sameContentDir := "same-content-candidate"
	contractCopyTree(t, mirrorContractDir, filepath.Join(core.projectRoot, sameContentDir))
	// The published descriptor now carries version 1.0.0; the local copy
	// still reads 0.0.0. That is fine — export-source-v1 covers schema and
	// fixture bytes only, never the descriptor, so the version number does
	// not participate in the comparison.
	versionMatch, err := core.verifyExport(ctx, sameContentDir, contractID+"@1.0.0")
	if err != nil {
		t.Fatalf("verifyExport @1.0.0 (same content): %v", err)
	}
	if versionMatch.outcome != contract.ExportMatched {
		t.Fatalf("verifyExport @1.0.0 outcome = %q, want matched (result=%+v)", versionMatch.outcome, versionMatch)
	}

	// (6) Mutate a schema/fixture file locally: DRIFTED.
	driftedDir := "drifted-candidate"
	contractCopyTree(t, mirrorContractDir, filepath.Join(core.projectRoot, driftedDir))
	contractDriftOneFile(t, filepath.Join(core.projectRoot, driftedDir))
	drifted, err := core.verifyExport(ctx, driftedDir, contractID+"@1.0.0")
	if err != nil {
		t.Fatalf("verifyExport @1.0.0 (drifted): %v", err)
	}
	if drifted.outcome != contract.ExportDrifted {
		t.Fatalf("verifyExport @1.0.0 outcome = %q, want drifted (result=%+v)", drifted.outcome, drifted)
	}
}

// legacyWidgetDescriptor is a hand-written descriptor declaring NO
// `artifacts:` key at all — contract.DetectInventoryMode's own predicate for
// InventoryLegacyFixedV1 (internal/contract/publication_intent.go). It is
// never run through `a2a contract new`/`a2a submit` (which always render a
// declared-v2 `artifacts:` inventory — contractLifecycleRaiseFloor's own doc
// comment), because AC-9's target is specifically the profile SELECTOR at
// the verify call site, not the full submit/publish legality pipeline.
const legacyWidgetDescriptor = `---
schema: envelope/v2
id: XC-axon-legacy-widget
type: contract
title: Legacy widget
space: fixture-space
from: axon
to: [beta]
actor: {kind: agent, name: test, model: test}
created: 2026-01-01T00:00:00Z
category: other
priority: p3
blocking: false
classification: internal
version: 0.0.0
schema_format: json-schema-2020-12
compat_policy: default
---
Legacy widget contract body.
`

// TestVerifyExportLocalDigestSelectsProfileFromInventoryMode is AC-9: the
// verifier must call contract.SelectDigestProfile(contract.DetectInventoryMode(...))
// — the SAME selector the publisher calls — rather than hardcoding
// contract.ProfileContractSetV2. A legacy candidate (no `artifacts:` key)
// must be refused by ExportSource's own "requires contract-set-v2" guard,
// never silently digested under V2 rules (which would instead fail
// BuildCarriedSet's declared-set validation, a DIFFERENT error, because a
// legacy descriptor has no artifacts to validate against V2's shape).
func TestVerifyExportLocalDigestSelectsProfileFromInventoryMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	contractDir := filepath.Join(mirrorDir, "axon", "provides", "legacy-widget")
	if err := os.MkdirAll(filepath.Join(contractDir, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contractDir, "fixtures", "valid"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "contract.md"), []byte(legacyWidgetDescriptor), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "schema", "main.schema.json"), []byte(`{"type":"object"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "fixtures", "valid", "one.json"), []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	contractLifecycleGit(t, mirrorDir, "add", "axon/provides/legacy-widget")
	contractLifecycleGit(t, mirrorDir, "commit", "-m", "test: land a legacy-shaped candidate")
	contractLifecycleGit(t, mirrorDir, "push", "origin", "main")

	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	core := &contractP6Core{projectRoot: t.TempDir(), mirrorDir: mirrorDir, ownSystem: contractLifecycleSystem, engine: engine}

	localDir := "legacy-candidate"
	contractCopyTree(t, contractDir, filepath.Join(core.projectRoot, localDir))

	_, err = core.verifyExportLocalDigest(ctx, localDir, "XC-axon-legacy-widget")
	if err == nil {
		t.Fatal("legacy candidate was accepted; want a refusal naming export-source-v1's V2 requirement")
	}
	if !strings.Contains(err.Error(), "export-source-v1 requires contract-set-v2") {
		t.Fatalf("legacy candidate error = %q, want it naming export-source-v1's V2 requirement (proof the LEGACY profile was actually selected, not silently forced to V2)", err.Error())
	}
}

// TestVerifyExportLocalDigestRequiresTheMirrorScaffold documents AC-13's one
// real behaviour change: routing bare-id verify through the SAME
// freezePublicationCandidate helper as publish means the candidate's mirror
// directory must already exist (freezePublicationCandidate always opens it,
// staging or not — internal/contractwiring/candidate.go:54). Before this
// wave, the staging-only read needed no mirror presence at all. In this
// codebase's actual domain model that is not a regression: an XC-id only
// exists once `a2a contract new` + `a2a submit` have landed the scaffold on
// the mirror (submit is what MINTS the id), so by the time any caller holds
// an id to pass to verify-export, submit has already run and the mirror
// entry already exists. This test proves the (expected) refusal shape for
// the one case that would differ — an id that was never submitted at all —
// so a later reader does not have to re-derive it.
func TestVerifyExportLocalDigestRequiresTheMirrorScaffold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	core := &contractP6Core{projectRoot: t.TempDir(), mirrorDir: mirrorDir, ownSystem: contractLifecycleSystem, engine: engine}

	localDir := "never-submitted-candidate"
	if err := os.MkdirAll(filepath.Join(core.projectRoot, localDir, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(core.projectRoot, localDir, "contract.md"), []byte(legacyWidgetDescriptor), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = core.verifyExportLocalDigest(ctx, localDir, "XC-axon-never-submitted")
	if err == nil {
		t.Fatal("verify-export against a never-submitted id was accepted; want a refusal — see this test's own doc comment")
	}
}
