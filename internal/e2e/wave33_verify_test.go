// Wave 33 verification: five flows this epic shipped that no test has ever
// driven end to end (docs/features/active/agent-exchange-2026-08/
// epic-backlog.md, the wave 33 brief) — `contract activate`'s event/v2
// shape and fold acceptance, P5 AC1's activation-debt derivation as read
// by the producer through `a2a inbox --actionable`, `close`/`verify`'s
// `--verdict` on event/v2, `respond --ref`'s submit-time possession
// refusal, and `operational_items` on `inbox`/`outbox --json`.
//
// Two tiers, chosen per verb by ONE fact verified empirically before any
// test here was written (see this file's own report): `contract activate`
// and `--verdict` only exist on event/v2, which requires the SPACE's own
// min_binary_version to be at or above contract.ContractPublicationFloor
// ("0.19.0") — and internal/e2e's exec'd binary is pinned to "0.1.0"
// (main_test.go's own ldflags, off this wave's allowlist, and FOUR
// `.txtar` scripts assert that literal: ops_version, space_init,
// doctor, whatsnew). Raising a hostRig space's floor to 0.19.0 therefore
// refuses EVERY write in that rig with CC-085 ("local binary version
// older than space.yaml min_binary_version") — data_adversarial_test.go
// and data_loop_test.go already recorded hitting exactly this wall for an
// unrelated reason (contract-set-v2 publication) and called it a dead end
// empirically, not assumed.
//
// So the three floor-gated flows (activate, the AC1 derivation, --verdict)
// are driven by DIRECT CONSTRUCTION — contract_write_test.go's own shape
// (a REAL space.WriteFunnel + host.NewFakeHost + a REAL spacefixture
// clone, cli.NewXCommand built directly rather than through
// cmd/a2a/wire.go) — because WriteFunnel's own binaryVersion is an
// injectable constructor argument, independent of the exec'd process. This
// is a DEVIATION from the brief's stated shape (hostRig, exactly reachable
// per contract_write_test.go:230's own pointer) and it costs real
// coverage: cmd/a2a/wire.go's config load, credential resolution and
// mirror handling — the exact tier B28/B29/B30 all lived in — is
// unreachable for these three verbs in this harness. That is itself a
// finding, reported alongside this file's own results: EVERY event/v2
// flow this epic shipped is undrivable through the built binary in the
// repo's own e2e harness, strictly stronger than B28's single dev-build
// wall.
//
// The two floor-agnostic flows (`respond --ref`'s refs[]/possession check
// exists on event/v1 too; `operational_items` reads a contract
// descriptor's own x_operational[], no event required) are driven through
// hostRig — the real exec'd binary against a real testkit/fakegithub
// server — exactly as the brief asks, and reused directly from
// data_loop_test.go's own two-party fixture where the recipe already
// exists (same package, same file set, no re-derivation).
package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
)

// wave33Floor is the floor every direct-construction test in this file
// raises the space to — contract.ContractPublicationFloor itself, the
// exact value lifecycleEventSchema/runActivate compare against, never a
// higher number that would prove less.
const wave33Floor = contract.ContractPublicationFloor

// wave33SubmitValidator builds a REAL V2 submit validator over mirrorDir
// for ownSystem — required by space.WriteFunnel at/above
// contract.ContractPublicationFloor (ErrSubmitValidatorRequired: "final
// submission validator is required at the operational-confidence floor"),
// discovered empirically running this file's own first test, not assumed.
func wave33SubmitValidator(t *testing.T, mirrorDir, ownSystem string, manifest space.Manifest) space.SubmitValidator {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, ownSystem, manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	return cli.NewSubmitValidatorAdapter(engine, ownSystem, resolver, legality)
}

// wave33Manifest is e2eManifest's floor-carrying twin, added here rather
// than parameterizing e2eManifest itself: internal/space and internal/host
// tests do not consume this helper, but every OTHER internal/e2e test that
// calls e2eManifest() (outside this file, unreadable by this wave) does,
// at the fixed "" (below-floor) default — widening it in place would be
// exactly the "can't observe the blast radius" mistake this file's own
// package doc comment warns against for spacefixture's seed.
func wave33Manifest(floor string, systems ...string) space.Manifest {
	participants := make([]space.Participant, len(systems))
	for i, s := range systems {
		participants[i] = space.Participant{System: s, Status: "active"}
	}
	return space.Manifest{MinBinaryVersion: floor, Participants: participants}
}

// wave33Store builds a *cache.Store over one already-on-disk mirror
// checkout, the same direct construction thread_chain_test.go's own
// buildThreadChainStore uses — read-surface commands (thread/inbox/
// outbox) are driven through the REAL cli.NewXCommand constructors against
// it, never against cache.Store's own methods directly, so a defect in the
// CLI's own thin wrapper (flag parsing, JSON encoding) is exactly as
// reachable as a defect in cache itself.
func wave33Store(t *testing.T, ownSystem, mirrorDir string, manifest space.Manifest) *cache.Store {
	t.Helper()
	return cache.NewStore(ownSystem, t.TempDir(), []cache.SpaceMirror{
		{SpaceID: "fixture-space", Dir: mirrorDir, Manifest: manifest},
	}, time.Now, 0)
}

// writeContractDescriptorV2Operational seeds a from-owned envelope/v2
// contract descriptor at slug/version, carrying xOperationalRaw verbatim
// (mirrors internal/cli/cmd_contract_test.go's own
// writeContractDescriptorWithXOperational — that helper is unexported to
// this package, so this is a file-local twin, not a reuse).
func writeContractDescriptorV2Operational(t *testing.T, mirrorDir, from, slug, version, xOperationalRaw string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		xOperationalRaw +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/contract.md", content)
}

// wave33ActivateEventProbe decodes only the fields this wave's own brief
// names ("is it event/v2, does it carry the activation block") — a
// decoded-struct assertion, never strings.Contains over rendered bytes
// (this epic's own paid-for trap, cmd_contract_test.go's exact words).
type wave33ActivateEventProbe struct {
	Schema     string                         `yaml:"schema"`
	Transition string                         `yaml:"transition"`
	Subject    string                         `yaml:"subject"`
	Space      string                         `yaml:"space"`
	Activation *wave33ContractActivationProbe `yaml:"activation"`
}

type wave33ContractActivationProbe struct {
	Version   string   `yaml:"version"`
	Status    string   `yaml:"status"`
	Satisfies []string `yaml:"satisfies"`
}

// wave33FindEventFile returns the sole non-.gitkeep file under
// <system>/events on ref — the one file a single activate/verify/close
// call adds — via `git ls-tree`, never a hand-derived ULID path (ULIDs are
// time+entropy, not reproducible).
func wave33FindEventFile(t *testing.T, mirrorDir, ref, system string) string {
	t.Helper()
	out := gitOutput(t, mirrorDir, "ls-tree", "-r", "--name-only", ref, "--", system+"/events")
	var found []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasSuffix(line, ".gitkeep") {
			continue
		}
		found = append(found, line)
	}
	if len(found) != 1 {
		t.Fatalf("wave33FindEventFile: ref %s system %s: want exactly one event file, got %v (raw=%q)", ref, system, found, out)
	}
	return found[0]
}

// wave33FindResponseID returns the sole XS- id newly committed under
// <system>/exchanges on ref — the one response artifact a single `respond`
// call adds — via `git ls-tree`, never a hand-derived id (a response id is
// minted, not caller-chosen).
func wave33FindResponseID(t *testing.T, mirrorDir, ref, system string) string {
	t.Helper()
	out := gitOutput(t, mirrorDir, "ls-tree", "-r", "--name-only", ref, "--", system+"/exchanges")
	var found []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		base := filepath.Base(line)
		if !strings.HasPrefix(base, "XS-") {
			continue
		}
		found = append(found, strings.TrimSuffix(base, ".md"))
	}
	if len(found) != 1 {
		t.Fatalf("wave33FindResponseID: ref %s system %s: want exactly one response, got %v (raw=%q)", ref, system, found, out)
	}
	return found[0]
}

// --- 1: contract activate authors event/v2 + activation, fold accepts ---

// TestWave33ContractActivateEventV2AndFoldAccepts is the brief's item 1: a
// genuinely published contract declaring x_operational, activated for
// real through a real WriteFunnel + real spacefixture clone, at
// contract.ContractPublicationFloor — asserts the committed event decodes
// as event/v2 with an `activation` block, AND that folding the space's
// real history (through cache.Store, exactly what `a2a thread` reads)
// raises no flag for it (the illegal-transition defect this wave's brief
// says a prior wave shipped and only a hand check caught).
func TestWave33ContractActivateEventV2AndFoldAccepts(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	writeContractDescriptorV2Operational(t, mirrorDir, "axon", "opact", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")
	writeLifecycleEventVersion(t, mirrorDir, "axon", 0, "XC-axon-opact", "publish", "axon", "1.0.0")

	manifest := wave33Manifest(wave33Floor, "axon", "beta", "gamma")
	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, wave33SubmitValidator(t, mirrorDir, "axon", manifest), wave33Floor)
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", manifest, e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"activate", "XC-axon-opact", "--version", "1.0.0", "--satisfies", "endpoint", "--note", "live now"}, io)
	if code != 0 {
		t.Fatalf("contract activate: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call, got %d", len(fakeHost.Opens))
	}
	branch := lastOpenedBranch(fakeHost)

	eventPath := wave33FindEventFile(t, mirrorDir, branch, "axon")
	raw := gitOutput(t, mirrorDir, "show", branch+":"+eventPath)
	var ev wave33ActivateEventProbe
	if err := yaml.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("decode activate event %s: %v\nraw=%s", eventPath, err, raw)
	}
	if ev.Schema != "event/v2" {
		t.Fatalf("event.schema = %q, want event/v2 (mutation check: this must fail if the selector regresses to event/v1)", ev.Schema)
	}
	if ev.Transition != "activate" || ev.Subject != "XC-axon-opact" || ev.Space != "fixture-space" {
		t.Fatalf("event = %+v, want transition activate, subject XC-axon-opact, space fixture-space", ev)
	}
	if ev.Activation == nil {
		t.Fatal("event carries no `activation` block")
	}
	if ev.Activation.Version != "1.0.0" || ev.Activation.Status != "live" {
		t.Fatalf("activation = %+v, want version 1.0.0 status live", ev.Activation)
	}
	if len(ev.Activation.Satisfies) != 1 || ev.Activation.Satisfies[0] != "endpoint" {
		t.Fatalf("activation.satisfies = %v, want [endpoint]", ev.Activation.Satisfies)
	}

	// Fold acceptance: merge the activate event into main, then fold the
	// space's REAL committed history through cache.Store (the same read
	// `a2a thread --json` performs) and assert no flag was raised for it —
	// the exact "does fold accept it without an illegal-transition flag"
	// question the brief poses, checked against a real commit history
	// rather than a fakeLifecycleFunnel's recorded call.
	mergeBranchToMain(t, mirrorDir, branch)
	store := wave33Store(t, "axon", mirrorDir, manifest)
	thread := cli.NewThreadCommand(store)
	tIO, tOut, tErr := newIO()
	tCode := thread.Run(context.Background(), []string{"XC-axon-opact", "--json"}, tIO)
	if tCode != 0 {
		t.Fatalf("thread --json: code = %d, want 0; stderr=%s", tCode, tErr.String())
	}
	var result cache.ThreadResult
	if err := json.Unmarshal(tOut.Bytes(), &result); err != nil {
		t.Fatalf("decode thread result: %v\nstdout=%s", err, tOut.String())
	}
	if len(result.Flags) != 0 {
		t.Fatalf("thread carries %d flags after activate, want 0 (fold refused/flagged the event): %+v", len(result.Flags), result.Flags)
	}
}

// --- 2: P5 AC1's derivation, read by the producer through --actionable ---

// TestWave33ContractActivationDebtSurfacesToProducerOnlyUnderActionable is
// the brief's item 2: publish a contract, have another system adopt it,
// then read `a2a inbox`/`a2a inbox --actionable` AS THE PRODUCER — spec
// 05-declared-nature.md §8 AC1's own words: "a published contract...with a
// registered consumer yields a named owner, and the derivation is GATED ON
// THE SPACE'S OWN FLOOR". No x_operational field is set anywhere (P-1: the
// debt derives from registration and publication ALONE), matching §6's own
// testing row.
//
// Both --json and --actionable --json are read (advisor-flagged
// discrimination, epic-backlog B19): if the debt shows in the first and is
// silently dropped from the second, that is B19 killing this exact AC1
// derivation, not a separate, already-accepted risk — B19 was filed on the
// explicit ground that it falsifies no numbered criterion, and this would
// be the numbered criterion it falsifies.
func TestWave33ContractActivationDebtSurfacesToProducerOnlyUnderActionable(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	manifest := wave33Manifest(wave33Floor, "axon", "beta", "gamma")

	// Arrangement: commit axon's published contract straight to origin (a
	// throwaway clone + push, helpers_test.go's own seedOriginExtras/
	// fixOriginManifest idiom) — envelope/v1, no x_operational at all,
	// which is the point (P-1).
	arrange := t.TempDir()
	gitRun(t, "", "clone", fx.RemoteURL(), arrange)
	writeContractDescriptorFor(t, arrange, "axon", "opdebt", "1.0.0")
	writeLifecycleEventVersion(t, arrange, "axon", 0, "XC-axon-opdebt", "publish", "axon", "1.0.0")
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "publish: XC-axon-opdebt@1.0.0")
	gitRun(t, arrange, "push", "origin", "main")

	// beta adopts it for real: a REAL V2 submit validator (TestT3ContractAdopt's
	// own precedent — consumes.yaml has no envelope, so the funnel must
	// route it to the consumes/v1 schema check, not demand frontmatter).
	betaMirror := fx.Clone("beta")
	fetchMain(t, betaMirror)
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(betaMirror, "beta", manifest)
	resolver := cli.NewMirrorResolver(betaMirror, manifest)
	submitValidator := cli.NewSubmitValidatorAdapter(engine, "beta", resolver, legality)
	fakeHost := host.NewFakeHost()
	betaFunnel := space.NewWriteFunnel(fakeHost, submitValidator, wave33Floor)
	adoptCmd := cli.NewContractCommand(nil, betaFunnel, betaMirror, "fixture-space", "beta", manifest, e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	if code := adoptCmd.Run(context.Background(), []string{"adopt", "XC-axon-opdebt"}, io); code != 0 {
		t.Fatalf("contract adopt: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	mergeBranchToMain(t, betaMirror, lastOpenedBranch(fakeHost))

	// Read as the PRODUCER (axon), over a fresh clone carrying both the
	// publication and the registered consumer.
	readerDir := t.TempDir()
	gitRun(t, "", "clone", fx.RemoteURL(), readerDir)
	store := wave33Store(t, "axon", readerDir, manifest)

	// (a) the unfiltered OUTBOX (never plain inbox: a contract is never
	// addressed to its own producer, so inbox's own !addressedToMe filter
	// drops it before the verdict is even visible — operational_debt_test.go's
	// own documented reason for using outbox/actionable here, verified
	// empirically: this test's own first run hit exactly that empty
	// result off plain `inbox --json`). The verdict is computed
	// unconditionally (store.go's own documented rule), so the item's
	// waiting_on/expected_transition must name the debt even without
	// --actionable.
	outboxAll := cli.NewOutboxCommand(store)
	ioAll, outAll, errAll := newIO()
	if code := outboxAll.Run(context.Background(), []string{"--json"}, ioAll); code != 0 {
		t.Fatalf("outbox --json: code = %d, want 0; stderr=%s", code, errAll.String())
	}
	var allItems []cache.Item
	if err := json.Unmarshal(outAll.Bytes(), &allItems); err != nil {
		t.Fatalf("decode outbox --json: %v\nstdout=%s", err, outAll.String())
	}
	var plain *cache.Item
	for i := range allItems {
		if allItems[i].ID == "XC-axon-opdebt" {
			plain = &allItems[i]
		}
	}
	if plain == nil {
		t.Fatalf("outbox --json does not even list XC-axon-opdebt: %+v", allItems)
	}
	if plain.ExpectedTransition != "activate" {
		t.Fatalf("plain outbox item.expected_transition = %q, want %q (AC1: registration+publication alone owe activate)", plain.ExpectedTransition, "activate")
	}
	if len(plain.WaitingOn) != 1 || plain.WaitingOn[0] != "axon" {
		t.Fatalf("plain outbox item.waiting_on = %v, want [axon] (the producer is the named owner)", plain.WaitingOn)
	}

	// (b) --actionable AS THE PRODUCER: this is the ONE inbox scope that
	// lists an item not addressed to me (inbox.go's own comment on
	// condition 6) — the decisive read for "does the producer's own tool
	// tell it the debt exists".
	inboxActionable := cli.NewInboxCommand(store)
	ioAct, outAct, errAct := newIO()
	if code := inboxActionable.Run(context.Background(), []string{"--actionable", "--json"}, ioAct); code != 0 {
		t.Fatalf("inbox --actionable --json: code = %d, want 0; stderr=%s", code, errAct.String())
	}
	var actionableItems []cache.Item
	if err := json.Unmarshal(outAct.Bytes(), &actionableItems); err != nil {
		t.Fatalf("decode inbox --actionable --json: %v\nstdout=%s", err, outAct.String())
	}
	var actionable *cache.Item
	for i := range actionableItems {
		if actionableItems[i].ID == "XC-axon-opdebt" {
			actionable = &actionableItems[i]
		}
	}
	if actionable == nil {
		t.Fatalf("`a2a inbox --actionable` (AS THE PRODUCER) does not surface XC-axon-opdebt's activation debt — "+
			"AC1's own criterion ('yields a named owner') is unmet on the ONE surface an agent actually polls. "+
			"Full actionable set: %+v", actionableItems)
	}
	found := false
	for _, r := range actionable.Reasons {
		if r == "activation-owed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("XC-axon-opdebt is actionable but not for `activation-owed`: reasons=%v", actionable.Reasons)
	}
}

// --- 3: close/verify --verdict author event/v2 verdicts[], thread shows them ---

// TestWave33CloseAndVerifyVerdictsAuthorEventV2AndThreadShowsThem is the
// brief's item 3: a real question thread driven to `responded` through the
// real `respond` verb (TWICE, from beta — table.go's own `respond` row
// admits `StateResponded` as a legal fromState, D-017's multi-response
// allowance), then `verify --verdict` and `close --verdict` through the
// real funnel at the event/v2 floor — asserts `a2a thread --json` (the
// exact surface the brief names) carries both events' verdicts[].
//
// Two responses, not one: VerifyCommand.Run carries a D-024 convenience
// that ALSO closes the parent in the SAME commit whenever exactly one
// response is tracked (discovered running this test's own first version,
// which asserted a standalone `close --verdict` and found it refused —
// "responded" was no longer the parent's state because verify's own
// convenience had already closed it). A second response keeps that
// convenience from firing (its own guard is `len(result.Responses) == 1`),
// so `close --verdict` is reached as the genuinely standalone command the
// brief names.
func TestWave33CloseAndVerifyVerdictsAuthorEventV2AndThreadShowsThem(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	manifest := wave33Manifest(wave33Floor, "axon", "beta", "gamma")
	fakeHost := host.NewFakeHost()

	questionID := "XQ-axon-20260811-v001"
	seedAcceptedQuestion(t, mirrorDir, questionID, "beta")

	betaFunnel := space.NewWriteFunnel(fakeHost, wave33SubmitValidator(t, mirrorDir, "beta", manifest), wave33Floor)
	respondCmd := cli.NewRespondCommand(betaFunnel, mirrorDir, "fixture-space", "beta", manifest, e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	// First response.
	r1IO, r1Out, r1Err := newIO()
	if code := respondCmd.Run(context.Background(), []string{"--result", "answered", questionID}, r1IO); code != 0 {
		t.Fatalf("respond (1st): code = %d, want 0; stdout=%s stderr=%s", code, r1Out.String(), r1Err.String())
	}
	firstRespondBranch := lastOpenedBranch(fakeHost)
	firstResponseID := wave33FindResponseID(t, mirrorDir, firstRespondBranch, "beta")
	mergeBranchToMain(t, mirrorDir, firstRespondBranch)

	// Second response — table.go admits StateResponded as a legal
	// fromState for `respond`, so this is a real, legal second act, not an
	// arrangement shortcut. A DIFFERENT actor name than the first call
	// (this file's own finding, running this test the first time): the
	// operation key this repo's idempotency-by-design derives from the
	// call's own content is otherwise byte-identical to the first
	// response, so a second `--result answered` from the SAME actor is
	// correctly treated as a RETRY of the first, not a second response —
	// varying the actor is what makes it a genuinely distinct act.
	respondCmd2 := cli.NewRespondCommand(betaFunnel, mirrorDir, "fixture-space", "beta", manifest, e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot2"))
	r2IO, r2Out, r2Err := newIO()
	if code := respondCmd2.Run(context.Background(), []string{"--result", "answered", questionID}, r2IO); code != 0 {
		t.Fatalf("respond (2nd): code = %d, want 0; stdout=%s stderr=%s", code, r2Out.String(), r2Err.String())
	}
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	// axon verifies the FIRST response with a verdict — RoleOwner
	// (applyResponseScoped). --refs disambiguates: lifecycleResolveResponseID
	// refuses a bare parent id once more than one response exists.
	axonFunnel := space.NewWriteFunnel(fakeHost, wave33SubmitValidator(t, mirrorDir, "axon", manifest), wave33Floor)
	verifyCmd := cli.NewVerifyCommand(axonFunnel, mirrorDir, "fixture-space", "axon", manifest, e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
	vIO, vOut, vErr := newIO()
	vCode := verifyCmd.Run(context.Background(), []string{questionID, "--refs", firstResponseID, "--verdict", "0:met:beta"}, vIO)
	if vCode != 0 {
		t.Fatalf("verify --verdict: code = %d, want 0; stdout=%s stderr=%s", vCode, vOut.String(), vErr.String())
	}
	verifyBranch := lastOpenedBranch(fakeHost)
	verifyEventPath := wave33FindEventFile(t, mirrorDir, verifyBranch, "axon")
	verifyRaw := gitOutput(t, mirrorDir, "show", verifyBranch+":"+verifyEventPath)
	var verifyEv wave33ActivateEventProbe // reused: only schema/transition/subject read below
	if err := yaml.Unmarshal([]byte(verifyRaw), &verifyEv); err != nil {
		t.Fatalf("decode verify event: %v\nraw=%s", err, verifyRaw)
	}
	if verifyEv.Schema != "event/v2" {
		t.Fatalf("verify event.schema = %q, want event/v2", verifyEv.Schema)
	}
	if verifyEv.Transition != "verify" {
		t.Fatalf("verify event.transition = %q, want verify (D-024's convenience must NOT have fired with two responses tracked)", verifyEv.Transition)
	}
	mergeBranchToMain(t, mirrorDir, verifyBranch)

	// axon closes with a verdict — RoleOwner, legal from `responded`,
	// driven as its own standalone command.
	closeFunnel := space.NewWriteFunnel(fakeHost, wave33SubmitValidator(t, mirrorDir, "axon", manifest), wave33Floor)
	closeCmd := cli.NewCloseCommand(closeFunnel, mirrorDir, "fixture-space", "axon", manifest, e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
	cIO, cOut, cErr := newIO()
	cCode := closeCmd.Run(context.Background(), []string{questionID, "--verdict", "0:met:beta"}, cIO)
	if cCode != 0 {
		t.Fatalf("close --verdict: code = %d, want 0; stdout=%s stderr=%s", cCode, cOut.String(), cErr.String())
	}
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	// a2a thread --json shows them both.
	store := wave33Store(t, "axon", mirrorDir, manifest)
	thread := cli.NewThreadCommand(store)
	tIO, tOut, tErr := newIO()
	if code := thread.Run(context.Background(), []string{questionID, "--json"}, tIO); code != 0 {
		t.Fatalf("thread --json: code = %d, want 0; stderr=%s", code, tErr.String())
	}
	var result cache.ThreadResult
	if err := json.Unmarshal(tOut.Bytes(), &result); err != nil {
		t.Fatalf("decode thread result: %v\nstdout=%s", err, tOut.String())
	}
	if len(result.Flags) != 0 {
		t.Fatalf("thread carries %d flags, want 0: %+v", len(result.Flags), result.Flags)
	}

	var verifyVerdicts, closeVerdicts []cache.TranscriptVerdict
	for _, te := range result.Transcript {
		if te.Kind != "event" || te.Event == nil {
			continue
		}
		switch te.Event.Transition {
		case "verify":
			verifyVerdicts = te.Event.Verdicts
		case "close":
			closeVerdicts = te.Event.Verdicts
		}
	}
	for name, got := range map[string][]cache.TranscriptVerdict{"verify": verifyVerdicts, "close": closeVerdicts} {
		if len(got) != 1 {
			t.Fatalf("thread's %s event carries %d verdicts, want 1: %+v", name, len(got), got)
		}
		if got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "beta" {
			t.Fatalf("thread's %s verdict = %+v, want {0 met beta}", name, got[0])
		}
	}
}

// --- 4: respond --ref submits when the fulfilling handoff resolves, ------
// --- and is refused at submit when it does not -------------------------

// TestWave33RespondRefSubmitsWhenHandoffResolves is the brief's item 4,
// positive half: driven through hostRig (this flow needs no event/v2
// floor — refs[] exists on event/v1 too), reusing data_loop_test.go's own
// two-party fixture and pack/deliver recipe (same package, no
// re-derivation) to produce a real handoff whose `kind: data` deliverable
// resolves, then responds to the ORIGINAL work_request naming it via
// `--ref` — proving the CLI's own composition (flag -> refs[] -> funnel)
// reaches internal/space's already-unit-tested possession check, not just
// that the check exists in isolation.
func TestWave33RespondRefSubmitsWhenHandoffResolves(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	beta.mustRun("sync")
	beta.mustRun("ack", wrID)

	from := filepath.Join(beta.projectDir, "wave33src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"wave33"}`+"\n")
	_, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	delivered := beta.dataLoopDeliver(t, stagingRoot, wrID)
	if delivered.HandoffID == "" {
		t.Fatalf("deliver result carries no handoff_id: %+v", delivered)
	}

	axon.mustRun("sync")
	stdout, stderr, code := beta.run("respond", "--result", "delivered", "--ref", delivered.HandoffID, wrID)
	if code != 0 {
		t.Fatalf("respond --ref (resolvable handoff): exit %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	axon.mustRun("sync")
	out := axon.mustRun("show", wrID)
	if !strings.Contains(out, "responded") {
		t.Fatalf("show %s after respond --ref = %q, want state responded", wrID, out)
	}
}

// TestWave33RespondRefRefusedWhenHandoffDeliverableUnresolvable is the
// brief's item 4, refusal half: a handoff committed directly (arrangement,
// this file's own established idiom) declaring a `kind: data` deliverable
// naming a package id that resolves nowhere — `respond --ref` on it must
// be refused AT SUBMIT, never silently accepted, and the destination
// (the work_request's own state) must be left unchanged.
func TestWave33RespondRefRefusedWhenHandoffDeliverableUnresolvable(t *testing.T) {
	t.Parallel()
	axon := newHostRig(t, "axon", "axon", "beta")
	beta := axon.peer("beta")

	wrID := "XW-axon-20260811-w901"
	wrPath := axon.stageDataLoopWorkRequest(wrID, "beta")
	axon.mustRun("submit", wrPath)
	beta.mustRun("sync")
	beta.mustRun("ack", wrID)

	handoffID := "XH-beta-20260811-h901"
	handoffContent := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + handoffID + "\n" +
		"type: handoff\n" +
		"title: unresolvable delivery\n" +
		"space: " + hostRigSpaceID + "\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-11T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"fulfills: " + wrID + "\n" +
		"deliverables:\n" +
		"  - name: sample\n" +
		"    ref: DP-beta-20260101-nowhere\n" +
		"    kind: data\n" +
		"---\nbody\n"

	arrange := t.TempDir()
	gitRun(t, "", "clone", axon.fx.RemoteURL(), arrange)
	writeMirrorFile(t, arrange, "beta/exchanges/"+handoffID+".md", handoffContent)
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: unresolvable handoff (wave 33 refusal fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	// No `ack` step: checkResponseDeliveryPossession resolves the handoff's
	// raw committed bytes directly (delivery_possession.go's own doc
	// comment — "no local filesystem read", via origin/main) and never
	// consults its folded state, so the refusal fires independent of
	// whether the handoff was ever acknowledged.
	beta.mustRun("sync")

	stdout, stderr, code := beta.run("respond", "--result", "delivered", "--ref", handoffID, wrID)
	if code == 0 {
		t.Fatalf("respond --ref (unresolvable handoff deliverable) succeeded, want a refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "does not resolve") {
		t.Fatalf("refusal does not name the possession condition (want %q): %s", "does not resolve", combined)
	}

	axon.mustRun("sync")
	out := axon.mustRun("show", wrID)
	if strings.Contains(out, "responded") {
		t.Fatalf("show %s after a refused respond --ref reports responded — the refused write leaked state: %s", wrID, out)
	}
}

// --- 5: operational_items on inbox/outbox --json (AC4) -------------------

// TestWave33OperationalItemsOnInboxAndOutboxJSON is the brief's item 5:
// driven through hostRig — this projection reads a contract descriptor's
// own x_operational[] (mirror.go's DeriveOperationalItems/
// OperationalItemsFromEnvelope), never an event, so it needs no floor and
// exercises cmd/a2a/wire.go's real config/mirror plumbing. A contract with
// TWO named items, one `absent` and one `ready`, seeded as a direct commit
// (arrangement — a real `a2a contract publish` cannot reach event/v2 in
// this harness for the reason this file's own package doc comment
// records) — the point under test is the READ projection, not the write
// path that produced the descriptor.
func TestWave33OperationalItemsOnInboxAndOutboxJSON(t *testing.T) {
	t.Parallel()
	axon := newHostRig(t, "axon", "axon", "beta")
	beta := axon.peer("beta")

	slug := "opitems"
	contractID := "XC-axon-" + slug
	descriptor := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + contractID + "\n" +
		"type: contract\n" +
		"title: the operational-items contract under test\n" +
		"space: " + hostRigSpaceID + "\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-11T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"1.0.0\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"x_operational:\n" +
		"  - name: endpoint\n    state: absent\n" +
		"  - name: docs\n    state: ready\n" +
		"---\n# opitems\n\nbody\n"

	arrange := t.TempDir()
	gitRun(t, "", "clone", axon.fx.RemoteURL(), arrange)
	writeMirrorFile(t, arrange, "axon/provides/"+slug+"/contract.md", descriptor)
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: "+contractID+" with x_operational (wave 33 AC4 fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	axon.mustRun("sync")
	beta.mustRun("sync")

	outboxJSON := axon.mustRun("outbox", "--json")
	var outboxItems []wave33ItemProbe
	if err := json.Unmarshal([]byte(outboxJSON), &outboxItems); err != nil {
		t.Fatalf("decode outbox --json: %v\nstdout=%s", err, outboxJSON)
	}
	assertWave33OperationalItems(t, "outbox (producer)", outboxItems, contractID)

	inboxJSON := beta.mustRun("inbox", "--json")
	var inboxItems []wave33ItemProbe
	if err := json.Unmarshal([]byte(inboxJSON), &inboxItems); err != nil {
		t.Fatalf("decode inbox --json: %v\nstdout=%s", err, inboxJSON)
	}
	assertWave33OperationalItems(t, "inbox (addressed consumer)", inboxItems, contractID)
}

// wave33ItemProbe decodes only what this test needs off cache.Item's
// guaranteed JSON shape.
type wave33ItemProbe struct {
	ID               string                       `json:"id"`
	OperationalItems []wave33OperationalItemProbe `json:"operational_items,omitempty"`
}

type wave33OperationalItemProbe struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func assertWave33OperationalItems(t *testing.T, surface string, items []wave33ItemProbe, contractID string) {
	t.Helper()
	var found *wave33ItemProbe
	for i := range items {
		if items[i].ID == contractID {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatalf("%s does not list %s at all", surface, contractID)
	}
	// DeriveOperationalItems (mirror.go) unions the descriptor's own
	// declared names onto a FIXED well-known catalogue, each absent one
	// reading `undeclared` (P-1: silence is a live state) — so the
	// projection carries more than the two names this test declares; the
	// two named ones are what this assertion checks, never a total count.
	if len(found.OperationalItems) < 2 {
		t.Fatalf("%s: %s.operational_items = %+v, want at least the 2 declared entries", surface, contractID, found.OperationalItems)
	}
	states := map[string]string{}
	for _, it := range found.OperationalItems {
		states[it.Name] = it.State
	}
	if states["endpoint"] != "absent" {
		t.Fatalf("%s: operational_items[endpoint].state = %q, want %q (full: %+v)", surface, states["endpoint"], "absent", found.OperationalItems)
	}
	if states["docs"] != "ready" {
		t.Fatalf("%s: operational_items[docs].state = %q, want %q (full: %+v)", surface, states["docs"], "ready", found.OperationalItems)
	}
}
