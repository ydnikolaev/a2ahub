package main

// Tests for the express data flow's resume paths and refusals (spec 05 §8 rows
// 6-9 and the P5 audit, docs/features/active/local-space-2026-10/audits/
// p5-w2b-2026-10-28.md). The rigs here drive the real sequencer over a real
// git mirror, with the producer's delivery going through the real write funnel
// over a fake host (so a delivery really is a PR that can stay open) and the
// inner lifecycle verbs going through the scripted funnel.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// expressRetryRig is the producer's side of a FAILED first attempt: the
// work_request is back to in_progress, attempt 1's handoff is rejected and its
// response disputed, so the next `deliver --for` is a retry.
type expressRetryRig struct {
	core   *dataCore
	funnel *expressScriptedFunnel
	fake   *host.FakeHost
	from   string
	waits  []string
	// failWait, when set, is what the merge wait returns for a delivery PR
	// instead of landing it.
	failWait error
}

// expressTreeHost is the fake host with the one capability the production hosts
// add on top of host.Host and the delivery check needs: reading a file out of a
// commit in the mirror's object store (an exported method of the git mechanics
// both *host.GitHubHost and *host.LocalHost carry).
type expressTreeHost struct {
	*host.FakeHost
	git *host.GitHubHost
}

func (h expressTreeHost) ReadRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, paths []string) (map[string]host.RemoteTreeFile, error) {
	return h.git.ReadRemoteRecoveryTreeFiles(ctx, repoDir, commit, paths)
}

// expressOldHandoff is the handoff newRecordTestDataCore seeds (it fulfills
// expressTestWR).
const expressOldHandoff = "XH-axon-20260804-cd34"

// expressTestUlid is above every ULID seedDataWiringHandoff mints, so an event
// seeded with it is the newest of that handoff's history.
const expressTestUlid = "01K1A2B3C4D5E6F7G8H9J0K1M3"

func writeExpressEvent(t *testing.T, mirrorDir, system, ulid, subject, transition string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, system, "events", "2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "schema: event/v1\nevent: " + ulid + "\nspace: test\nsubject: " + subject + "\ntransition: " + transition +
		"\nactor: {kind: agent, name: tester, system: " + system + "}\nat: 2026-08-04T12:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, ulid+".yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newExpressRetryRig(t *testing.T) *expressRetryRig {
	t.Helper()
	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	core, doc, fake := newRecordTestDataCore(t, []byte(`{"ok":true}`))
	core.spaceID = "test"
	core.stagingDir = t.TempDir()
	core.ownSystem = "axon"
	core.authorName, core.authorEmail = "a2a-axon", "a2a-axon@a2ahub.invalid"
	core.funnel = space.NewWriteFunnel(fake, dataNoopSubmitValidator{}, "99.0.0")
	core.host = expressTreeHost{FakeHost: fake, git: host.NewGitHubHost(nil, "")}
	core.loadManifest = func() (space.Manifest, error) {
		return space.Manifest{MinBinaryVersion: "0.1.0", Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
	}
	// The staged manifest is schema-validated at deliver, so the pin needs a real digest.
	schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
	core.resolveContractSchemas = func(context.Context, string) (map[string][]byte, string, error) {
		return schemas, "XC-axon-widget@1.0.0#sha256:" + strings.Repeat("a", 64), nil
	}
	// submit/ack/accept/respond of the request, attempt 1's response (it delivers
	// the seeded handoff's package), then the consumer's verdict: the handoff is
	// rejected and the response disputed.
	seedExpressWorkRequest(t, core.mirrorDir, "peer", "axon", expressTestWR, expressTestResponse, doc.ID, []string{"c"})
	writeExpressEvent(t, core.mirrorDir, "peer", expressTestUlid, expressOldHandoff, "verify-fail")
	writeExpressEvent(t, core.mirrorDir, "peer", "01K1A2B3C4D5E6F7G8H9J00006", expressTestResponse, "dispute")

	rig := &expressRetryRig{core: core, funnel: &expressScriptedFunnel{}, fake: fake, from: from}
	installExpressSeams(t, core, rig.funnel, nil)
	core.awaitMerge = func(_ context.Context, req space.AwaitRequest) (space.AwaitResult, error) {
		rig.waits = append(rig.waits, req.Branch)
		if rig.failWait != nil {
			return space.AwaitResult{}, rig.failWait
		}
		if !rig.funnel.land(req.Branch) {
			landBranchIntoMirror(t, core.mirrorDir, req.Branch)
		}
		return space.AwaitResult{PRURL: "merged"}, nil
	}
	return rig
}

// landBranchIntoMirror merges a pushed branch's files into the mirror's working
// tree, as the refresh after its PR merged would show them.
func landBranchIntoMirror(t *testing.T, mirrorDir, branch string) {
	t.Helper()
	changed := gitFixtureOutput(t, mirrorDir, "diff", "--name-only", "main", branch)
	for _, rel := range strings.Fields(changed) {
		content := gitFixtureOutput(t, mirrorDir, "show", branch+":"+rel)
		full := filepath.Join(mirrorDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (rig *expressRetryRig) req() cli.DataDeliverForRequest {
	return cli.DataDeliverForRequest{
		For: expressTestWR, From: rig.from, Profile: "synthetic", Format: "json", Contract: "XC-axon-widget@1.0.0",
	}
}

func (rig *expressRetryRig) verbs() []string { return expressInnerVerbs(rig.funnel) }

// callFor returns the concatenated bytes of the first inner write made with verb.
func (rig *expressRetryRig) callFor(t *testing.T, verb string) string {
	t.Helper()
	for i, call := range rig.funnel.calls {
		if call.Verb == verb {
			return rig.funnel.content(i)
		}
	}
	t.Fatalf("no inner %s write was made; verbs = %v", verb, rig.verbs())
	return ""
}

// TestExpressDeliverRerunWhileFirstPRIsOpenUsesThePRsHandoff is the audit's
// blocker 1. `dataCore.deliver` mints a fresh handoff id on every call, but
// space.DeliverDataPackage collides on the package's operation key and so
// converges on the FIRST delivery's open PR, which carries the FIRST id. The
// supersede and the respond must name the handoff that is actually in that PR.
func TestExpressDeliverRerunWhileFirstPRIsOpenUsesThePRsHandoff(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)

	// Run 1: the delivery's PR is opened and never merges within the timeout.
	rig.failWait = context.DeadlineExceeded
	first, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first deliverFor err = %v, want the unmerged delivery to time out", err)
	}
	firstHandoff := first.Handoff
	if !strings.HasPrefix(firstHandoff, "XH-axon-") || firstHandoff == expressOldHandoff {
		t.Fatalf("first run reported handoff %q, want the new attempt's id", firstHandoff)
	}
	if len(rig.fake.Opens) != 1 {
		t.Fatalf("first run opened %d PRs, want 1", len(rig.fake.Opens))
	}

	// Run 2: the PR is still open (deliver converges on it), then merges.
	rig.failWait = nil
	second, err := rig.core.deliverFor(context.Background(), rig.req())
	if err != nil {
		t.Fatalf("second deliverFor: %v (steps %v)", err, expressStepSummary(second.Steps))
	}
	if len(rig.fake.Opens) != 1 {
		t.Fatalf("re-run opened %d PRs in total, want the first one reused", len(rig.fake.Opens))
	}
	if second.Handoff != firstHandoff {
		t.Fatalf("re-run reports handoff %q, want %q: the one in the open PR", second.Handoff, firstHandoff)
	}
	if got, want := rig.verbs(), []string{"supersede", "respond"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("inner writes = %v, want %v", got, want)
	}
	for _, verb := range []string{"supersede", "respond"} {
		if body := rig.callFor(t, verb); !strings.Contains(body, firstHandoff) {
			t.Fatalf("%s does not reference %s, the handoff in the open PR:\n%s", verb, firstHandoff, body)
		}
	}
	after, err := rig.core.expressSnapshot(expressTestWR)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	states := map[string]string{}
	for _, h := range after.handoffs {
		states[h.id] = string(h.state)
	}
	if states[expressOldHandoff] != "superseded" || states[firstHandoff] != "submitted" || len(states) != 2 {
		t.Fatalf("handoff states = %v, want %s superseded and %s submitted", states, expressOldHandoff, firstHandoff)
	}
}

// TestExpressRetryInterruptedBeforeSupersedeIsFinishedOnRerun is the audit's
// blocker 2 and spec §8 row 6's end state. A retry dies after attempt 2 landed
// and before attempt 1 was superseded; the re-run must supersede attempt 1 (refs
// attempt 2) BEFORE responding, instead of seeing attempt 2 as active and
// skipping the whole block.
func TestExpressRetryInterruptedBeforeSupersedeIsFinishedOnRerun(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)
	// The first inner write is the supersede; it ends needs-attention.
	rig.funnel.states = []space.WriteState{space.WriteStateNeedsAttention}

	first, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !strings.Contains(err.Error(), "supersede") {
		t.Fatalf("first deliverFor err = %v, want the supersede to stop the run", err)
	}
	attempt2 := first.Handoff
	if !strings.HasPrefix(attempt2, "XH-axon-") || attempt2 == expressOldHandoff {
		t.Fatalf("first run reported handoff %q, want the new attempt's id", attempt2)
	}

	second, err := rig.core.deliverFor(context.Background(), rig.req())
	if err != nil {
		t.Fatalf("re-run deliverFor: %v (steps %v)", err, expressStepSummary(second.Steps))
	}
	if got, want := rig.verbs(), []string{"supersede", "supersede", "respond"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("inner writes = %v, want the stopped supersede, its repeat, then the respond", got)
	}
	if len(rig.fake.Opens) != 1 {
		t.Fatalf("the re-run opened %d delivery PRs in total, want 1: no second package", len(rig.fake.Opens))
	}
	if body := rig.funnel.content(1); !strings.Contains(body, attempt2) {
		t.Fatalf("the re-run's supersede does not reference %s:\n%s", attempt2, body)
	}
	if second.Handoff != attempt2 {
		t.Fatalf("re-run reports handoff %q, want %q", second.Handoff, attempt2)
	}
	steps := strings.Join(expressStepSummary(second.Steps), ",")
	if want := "ack:already-done,accept:already-done,pack:already-done,deliver:already-done,supersede:landed,respond:landed"; steps != want {
		t.Fatalf("re-run steps = %s, want %s", steps, want)
	}
	after, err := rig.core.expressSnapshot(expressTestWR)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	states := map[string]string{}
	for _, h := range after.handoffs {
		states[h.id] = string(h.state)
	}
	if states[expressOldHandoff] != "superseded" || states[attempt2] != "submitted" {
		t.Fatalf("handoff states = %v, want %s superseded and %s submitted", states, expressOldHandoff, attempt2)
	}
	if existing := after.responseFor(expressHandoff{id: attempt2, packageID: second.Package}); existing == nil {
		t.Fatalf("no response announces %s after the re-run", second.Package)
	}
}

// TestExpressSettleCoreStopsOnAnyCallError is the audit's finding 3. A data core
// write that came back WITH an error is never a landed step, whatever state it
// reports: a closed-unmerged PR reports already-open next to an error, and the
// standalone verb exits 1 on it. The sequencer must stop, not wait on the PR.
func TestExpressSettleCoreStopsOnAnyCallError(t *testing.T) {
	t.Parallel()
	for _, state := range []space.WriteState{
		space.WriteStateAlreadyOpen, space.WriteStatePendingMerge, space.WriteStateMerged, space.WriteStateAlreadyMerged,
		space.WriteStateNeedsAttention, space.WriteStateOutcomeUnknown,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			core, _, _ := newRecordTestDataCore(t, []byte(`{"ok":true}`))
			waited := false
			core.refreshMirror = func(context.Context) error { return nil }
			core.awaitMerge = func(context.Context, space.AwaitRequest) (space.AwaitResult, error) {
				waited = true
				return space.AwaitResult{}, nil
			}
			run := core.newExpressRun(cli.ActorFlags{}, 0)
			callErr := errors.New("the PR was closed without merging")
			write := space.WriteResult{State: state, Branch: "a2a/axon/data-deliver/x", PRURL: "https://example.invalid/pr/1"}
			err := run.settleCore(context.Background(), cli.DataStep{Step: "deliver", Subject: "XH-axon-20260804-aaaa"}, &write, callErr, []string{"XH-axon-20260804-aaaa"})
			if !errors.Is(err, callErr) {
				t.Fatalf("settleCore err = %v, want it to wrap %q", err, callErr)
			}
			if waited {
				t.Fatal("settleCore waited on a PR after the write reported an error")
			}
			if len(run.result.Steps) != 1 || run.result.Steps[0].Outcome != expressStopped {
				t.Fatalf("steps = %v, want exactly one stopped step", run.result.Steps)
			}
		})
	}
}

// TestExpressSnapshotSeparatesNoMatchFromUnreadable is the audit's finding 4. A
// file the flow cannot read or decode is not "not a match": treating it as one
// lets a re-run pack and deliver a second package, or respond twice, over
// history it simply failed to see. The flow must stop and name the path.
func TestExpressSnapshotSeparatesNoMatchFromUnreadable(t *testing.T) {
	t.Parallel()

	t.Run("an undecodable handoff stops the snapshot naming its path", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		bad := filepath.Join(rig.core.mirrorDir, "axon", "exchanges", "XH-axon-20260804-ffff.md")
		if err := os.WriteFile(bad, []byte("---\nid: [unterminated\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := rig.core.expressSnapshot(expressTestWR)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("expressSnapshot err = %v, want a stop naming %s", err, bad)
		}
	})

	t.Run("an undecodable response stops the snapshot naming its path", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		bad := filepath.Join(rig.core.mirrorDir, "axon", "exchanges", "XS-axon-20260804-ffff.md")
		if err := os.WriteFile(bad, []byte("no frontmatter at all\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := rig.core.expressSnapshot(expressTestWR)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("expressSnapshot err = %v, want a stop naming %s", err, bad)
		}
	})

	t.Run("an undecodable staged manifest stops the flow before any pack", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		staged := filepath.Join(rig.core.stagingDir, "data", "DP-axon-20260804-ffff")
		if err := os.MkdirAll(staged, 0o755); err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(staged, stagedManifestName)
		if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, err := rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("deliverFor err = %v, want a stop naming %s", err, bad)
		}
		matches, _ := filepath.Glob(filepath.Join(rig.core.stagingDir, "data", "*"))
		if len(matches) != 1 || len(rig.fake.Opens) != 0 || len(rig.funnel.calls) != 0 {
			t.Fatalf("the flow went on past the unreadable staged manifest: staged=%v opens=%d inner=%d steps=%v",
				matches, len(rig.fake.Opens), len(rig.funnel.calls), expressStepSummary(result.Steps))
		}
	})

	t.Run("an unreadable handoff does not make a staged package look undelivered", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		packed, err := rig.core.pack(context.Background(), cli.DataPackRequest{
			ContractRef: "XC-axon-widget@1.0.0", From: rig.from, Profile: "synthetic", Format: "json", Fulfills: expressTestWR,
		})
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		bad := filepath.Join(rig.core.mirrorDir, "axon", "exchanges", "XH-axon-20260804-ffff.md")
		if err := os.WriteFile(bad, []byte("garbage"), 0o644); err != nil {
			t.Fatal(err)
		}
		candidates, err := rig.core.expressStagedCandidates(packed.Manifest.Thread, "XC-axon-widget@1.0.0")
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("expressStagedCandidates = %v, %v, want a stop naming %s", candidates, err, bad)
		}
	})
}

// TestExpressCloseArgsFollowsTheLifecycleEventSchema is the audit's finding 5.
// Which floor authors event/v2 has ONE home, cli.LifecycleEventSchema, the
// function close itself uses. The express close derives its verdicts from the
// same answer: a verdict is sent exactly when close would author event/v2.
func TestExpressCloseArgsFollowsTheLifecycleEventSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, floor, wantSchema string
	}{
		{"at the contract publication floor", contract.ContractPublicationFloor, "event/v2"},
		{"far above it", "99.0.0", "event/v2"},
		{"below it", "0.1.0", "event/v1"},
		{"absent", "", "event/v1"},
		{"unparseable fails closed", "not-a-version", "event/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cli.LifecycleEventSchema(tc.floor); got != tc.wantSchema {
				t.Fatalf("cli.LifecycleEventSchema(%q) = %s, want %s", tc.floor, got, tc.wantSchema)
			}
			snap := expressSnapshot{
				id: expressTestWR, floor: tc.floor,
				doc: expressDoc{
					ID: expressTestWR, To: []any{"axon"},
					AcceptanceCriteria: []any{map[string]any{"id": "ac1", "text": expressVerifyCriterionText}},
				},
			}
			args, err := expressCloseArgs(snap, nil)
			if err != nil {
				t.Fatalf("expressCloseArgs: %v", err)
			}
			if sent := len(args) > 0; sent != (tc.wantSchema == "event/v2") {
				t.Fatalf("floor %q: close args = %v, but the close authors %s", tc.floor, args, tc.wantSchema)
			}
		})
	}
}

// TestExpressVerifyTakesTheDirectionFromTheRecordedHandoff is the audit's
// finding 6. Once the handoff is accepted or rejected, the consumer's verdict is
// on record, and the close or dispute that follows must go the way it went. A
// fresh verification that now disagrees (the bytes or the contract changed
// between runs) is refused naming both, before any write: closing over a
// rejected handoff, or disputing an accepted one, contradicts the record.
func TestExpressVerifyTakesTheDirectionFromTheRecordedHandoff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		recorded      string // the recorded transition on the handoff
		payload       string // what the package holds now
		wantInMessage []string
	}{
		{"accepted, but the package now fails", "verify-pass", `{"ok":false}`, []string{"accepted", "fail"}},
		{"rejected, but the package now passes", "verify-fail", `{"ok":true}`, []string{"rejected", "pass"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			core, funnel, fake, _ := newExpressVerifyRigWithPayload(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText}, []byte(tc.payload))
			writeExpressEvent(t, core.mirrorDir, "peer", expressTestUlid, expressOldHandoff, tc.recorded)
			pushes := len(fake.Pushes)
			result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
			if err == nil {
				t.Fatalf("verifyFor = nil error, want a refusal (steps %v)", expressStepSummary(result.Steps))
			}
			for _, want := range tc.wantInMessage {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("verifyFor err = %v, want it to name %q", err, want)
				}
			}
			if len(funnel.calls) != 0 || len(fake.Pushes) != pushes || len(result.Steps) != 0 {
				t.Fatalf("a write happened before the refusal: inner=%d pushes=%d steps=%v", len(funnel.calls), len(fake.Pushes)-pushes, expressStepSummary(result.Steps))
			}
		})
	}

	t.Run("an agreeing recorded verdict still completes the close", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
		writeExpressEvent(t, core.mirrorDir, "peer", expressTestUlid, expressOldHandoff, "verify-pass")
		result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
		if err != nil {
			t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		if got, want := strings.Join(expressStepSummary(result.Steps), ","), "ack:already-done,verify:already-done,close:landed"; got != want {
			t.Fatalf("steps = %s, want %s", got, want)
		}
		if len(funnel.calls) != 1 || funnel.calls[0].Verb != "close" {
			t.Fatalf("inner writes = %v, want exactly the close", expressInnerVerbs(funnel))
		}
	})
}

// TestExpressFailNamesStepsAlreadyDoneAsLanded is the audit's nit 8. A step the
// mirror already shows done is as landed as one this run wrote, so a resumed run
// that stops names the furthest step that is in the space, not "none".
func TestExpressFailNamesStepsAlreadyDoneAsLanded(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)
	req := rig.req()
	req.From = filepath.Join(t.TempDir(), "absent") // pack cannot run
	result, err := rig.core.deliverFor(context.Background(), req)
	if err == nil {
		t.Fatal("deliverFor = nil error, want the pack of a missing --from to fail")
	}
	if got, want := strings.Join(expressStepSummary(result.Steps), ","), "ack:already-done,accept:already-done,pack:refused"; got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
	if want := "last landed step: accept " + expressTestWR; !strings.Contains(err.Error(), want) {
		t.Fatalf("deliverFor err = %v, want it to say %q", err, want)
	}
}

// TestExpressDeliverForResumesAfterPackThroughTheWholeVerb is spec 05 §8 row 8,
// driven through deliverFor itself (TestExpressDeliverResumesAfterPack reaches
// only packOrResume): a run interrupted after pack and before the delivery is
// re-run, and the staged package is the one delivered, with no second package
// minted. A re-run over different bytes is refused at the same place, naming the
// staging root, and writes nothing.
func TestExpressDeliverForResumesAfterPackThroughTheWholeVerb(t *testing.T) {
	t.Parallel()
	interrupt := func(t *testing.T) (*expressRetryRig, cli.DataExpressResult) {
		t.Helper()
		rig := newExpressRetryRig(t)
		rig.fake.PushBranchFunc = func(context.Context, host.PushBranchRequest) (host.PushBranchResult, error) {
			return host.PushBranchResult{}, errors.New("the network went away")
		}
		first, err := rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), "the network went away") {
			t.Fatalf("first deliverFor err = %v, want the push failure", err)
		}
		if got := strings.Join(expressStepSummary(first.Steps), ","); !strings.HasPrefix(got, "ack:already-done,accept:already-done,pack:landed,deliver:") {
			t.Fatalf("first run steps = %s, want it to stop at the delivery after a landed pack", got)
		}
		if first.Package == "" {
			t.Fatal("first run reported no package")
		}
		rig.fake.PushBranchFunc = nil
		return rig, first
	}
	stagedPackages := func(rig *expressRetryRig) []string {
		matches, _ := filepath.Glob(filepath.Join(rig.core.stagingDir, "data", "*"))
		return matches
	}

	t.Run("same bytes deliver the staged package", func(t *testing.T) {
		t.Parallel()
		rig, first := interrupt(t)
		second, err := rig.core.deliverFor(context.Background(), rig.req())
		if err != nil {
			t.Fatalf("re-run deliverFor: %v (steps %v)", err, expressStepSummary(second.Steps))
		}
		if second.Package != first.Package {
			t.Fatalf("re-run delivered %s, want the staged %s", second.Package, first.Package)
		}
		if got := len(stagedPackages(rig)); got != 1 {
			t.Fatalf("staging holds %d packages after the re-run, want 1", got)
		}
		steps := expressStepSummary(second.Steps)
		if steps[2] != "pack:already-done" || steps[3] != "deliver:landed" {
			t.Fatalf("re-run steps = %v, want the staged pack reused (pack:already-done) and then delivered", steps)
		}
		if len(rig.fake.Opens) != 1 {
			t.Fatalf("the re-run opened %d delivery PRs, want 1", len(rig.fake.Opens))
		}
	})

	t.Run("different bytes are refused naming the staging root", func(t *testing.T) {
		t.Parallel()
		rig, _ := interrupt(t)
		staged := stagedPackages(rig)
		if len(staged) != 1 {
			t.Fatalf("staging holds %d packages after the interruption, want 1", len(staged))
		}
		if err := os.WriteFile(filepath.Join(rig.from, "orders.json"), []byte(`{"ok":true,"changed":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), staged[0]) {
			t.Fatalf("re-run deliverFor err = %v, want a refusal naming the staging root %s", err, staged[0])
		}
		if got := len(stagedPackages(rig)); got != 1 || len(rig.fake.Opens) != 0 || len(rig.funnel.calls) != 0 {
			t.Fatalf("the refused re-run went on: staged=%d opens=%d inner writes=%d", got, len(rig.fake.Opens), len(rig.funnel.calls))
		}
	})
}

// TestExpressDisputeCarriesTheCallersActor guards the argv shape of the one
// inner verb the equivalence tests do not reach with an actor: the shipped
// verbs read "<flags> <positional>" or "<positional> <flags>", never a flag
// after a flag-then-positional run, so the dispute (like the respond) must put
// the actor flags before its id.
func TestExpressDisputeCarriesTheCallersActor(t *testing.T) {
	t.Parallel()
	core, funnel, _, _ := newExpressVerifyRigWithPayload(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText}, []byte(`{"ok":false}`))
	echoingActor(core)
	result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{
		For: expressTestWR, Record: true, Actor: cli.ActorFlags{Kind: "agent", Name: "named-caller"},
	})
	if err != nil {
		t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
	}
	var disputed bool
	for i, call := range funnel.calls {
		if !strings.Contains(funnel.content(i), "name: named-caller") {
			t.Fatalf("inner %s write does not carry the caller's actor:\n%s", call.Verb, funnel.content(i))
		}
		disputed = disputed || call.Verb == "dispute"
	}
	if !disputed {
		t.Fatalf("no dispute was written; inner writes = %v", expressInnerVerbs(funnel))
	}
}

// --- P5-fix-2: what the express flow judges is bound to what the request pins ----

// expressWriteExchange commits one exchange document into the mirror's working
// tree, under system's own section.
func expressWriteExchange(t *testing.T, mirrorDir, system, id, doc string) string {
	t.Helper()
	dir := filepath.Join(mirrorDir, system, "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// expressHandoffDoc is a handoff envelope from `from` that fulfills wr and
// carries packageID as its one data deliverable.
func expressHandoffDoc(id, from, wr, packageID string) string {
	return "---\nschema: envelope/v1\nid: " + id + "\ntype: handoff\ntitle: h\nspace: test\nfrom: " + from + "\nto: [peer]\n" +
		"actor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\npriority: p3\nblocking: false\nclassification: internal\n" +
		"thread: thread:" + from + "-20260804-bad1\nfulfills: [" + wr + "]\n" +
		"deliverables:\n  - {name: \"x\", ref: \"" + packageID + "\", kind: data}\nverification: v\nacceptance_criteria: [\"d\"]\nlimitations: []\n---\nbody\n"
}

// expressResponseDoc is a response envelope from `from` answering parent.
func expressResponseDoc(id, from, parent, packageID string) string {
	return "---\nschema: envelope/v1\nid: " + id + "\ntype: response\ntitle: r\nspace: test\nfrom: " + from + "\nto: [peer]\n" +
		"actor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\nparent: " + parent + "\ndelivers: [" + packageID + "]\n---\nbody\n"
}

// withParticipants lets the manifest name zeta, the third party of these
// tests, so a foreign actor's events are folded the way a real member's are.
func withParticipants(core *dataCore) {
	core.loadManifest = func() (space.Manifest, error) {
		return space.Manifest{MinBinaryVersion: contract.ContractPublicationFloor, Participants: []space.Participant{{System: "axon"}, {System: "peer"}, {System: "zeta"}}}, nil
	}
}

// TestExpressSnapshotIgnoresAThirdPartysHandoff is the audit's finding 15. A
// handoff counts only when the request's producer wrote it: any other system can
// commit a handoff that names the XW in `fulfills`, with a newer event than the
// producer's, and verify --for --record would judge (and close over) it.
func TestExpressSnapshotIgnoresAThirdPartysHandoff(t *testing.T) {
	t.Parallel()
	const hostileHandoff = "XH-zeta-20260804-bad1"
	const hostilePackage = "DP-zeta-20260804-0000"

	t.Run("verify --for judges the producer's handoff", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
		withParticipants(core)
		expressWriteExchange(t, core.mirrorDir, "zeta", hostileHandoff, expressHandoffDoc(hostileHandoff, "zeta", expressTestWR, hostilePackage))
		// Newer than the producer's own submit, so it would be the active handoff.
		writeExpressEvent(t, core.mirrorDir, "zeta", "01K1A2B3C4D5E6F7G8H9J0K1M5", hostileHandoff, "submit")

		result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
		if err != nil {
			t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		if result.Handoff != expressOldHandoff || result.Package == hostilePackage {
			t.Fatalf("verifyFor judged handoff %q package %q, want the producer's %s", result.Handoff, result.Package, expressOldHandoff)
		}
		for i, call := range funnel.calls {
			if body := funnel.content(i); strings.Contains(body, hostileHandoff) || strings.Contains(body, hostilePackage) {
				t.Fatalf("inner %s write names the third party's artifact:\n%s", call.Verb, body)
			}
		}
	})

	t.Run("a third party's undecodable file does not stop the flow", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		withParticipants(rig.core)
		expressWriteExchange(t, rig.core.mirrorDir, "zeta", "XH-zeta-20260804-ffff", "no frontmatter at all\n")
		expressWriteExchange(t, rig.core.mirrorDir, "zeta", "XS-zeta-20260804-ffff", "garbage")
		snap, err := rig.core.expressSnapshot(expressTestWR)
		if err != nil {
			t.Fatalf("expressSnapshot: %v: a file in a section that is not the producer's must not stop the flow", err)
		}
		if len(snap.handoffs) != 1 || snap.handoffs[0].id != expressOldHandoff {
			t.Fatalf("handoffs = %+v, want only %s", snap.handoffs, expressOldHandoff)
		}
	})

	t.Run("a file in the producer's section written by another system is ignored", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		withParticipants(rig.core)
		expressWriteExchange(t, rig.core.mirrorDir, "axon", "XH-axon-20260804-ee11", expressHandoffDoc("XH-axon-20260804-ee11", "zeta", expressTestWR, hostilePackage))
		writeExpressEvent(t, rig.core.mirrorDir, "zeta", "01K1A2B3C4D5E6F7G8H9J0K1M5", "XH-axon-20260804-ee11", "submit")
		snap, err := rig.core.expressSnapshot(expressTestWR)
		if err != nil {
			t.Fatalf("expressSnapshot: %v", err)
		}
		if len(snap.handoffs) != 1 || snap.handoffs[0].id != expressOldHandoff {
			t.Fatalf("handoffs = %+v, want only %s", snap.handoffs, expressOldHandoff)
		}
	})

	t.Run("a producer's handoff with a malformed id stops the snapshot naming its path", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		bad := expressWriteExchange(t, rig.core.mirrorDir, "axon", "XH-axon-20260804-ee00", expressHandoffDoc("--force", "axon", expressTestWR, "DP-axon-20260804-0000"))
		_, err := rig.core.expressSnapshot(expressTestWR)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("expressSnapshot err = %v, want a stop naming %s", err, bad)
		}
	})
}

// TestExpressDisputeTargetsOnlyTheProducersResponse is the audit's finding 16.
// A response counts only when the request's producer wrote it; the dispute's
// target is passed to the shipped dispute verb as an argument, so it must also
// be an XS id.
func TestExpressDisputeTargetsOnlyTheProducersResponse(t *testing.T) {
	t.Parallel()
	const hostileResponse = "XS-zeta-20260804-bad2"

	t.Run("the dispute names the producer's response", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRigWithPayload(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText}, []byte(`{"ok":false}`))
		withParticipants(core)
		// Sorts after axon's section, announces the same package, and is written by a third party.
		expressWriteExchange(t, core.mirrorDir, "zeta", hostileResponse, expressResponseDoc(hostileResponse, "zeta", expressTestWR, "DP-axon-20260804-ab12"))

		result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
		if err != nil {
			t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		var disputed bool
		for i, call := range funnel.calls {
			if call.Verb != "dispute" {
				continue
			}
			disputed = true
			if body := funnel.content(i); !strings.Contains(body, "subject: "+expressTestResponse) || strings.Contains(body, hostileResponse) {
				t.Fatalf("the dispute does not target %s:\n%s", expressTestResponse, body)
			}
		}
		if !disputed {
			t.Fatalf("no dispute was written; inner writes = %v", expressInnerVerbs(funnel))
		}
		if result.Response != expressTestResponse {
			t.Fatalf("result.Response = %q, want %s", result.Response, expressTestResponse)
		}
	})

	t.Run("a producer's response with a malformed id stops the snapshot naming its path", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		bad := expressWriteExchange(t, rig.core.mirrorDir, "axon", "XS-axon-20260804-ee00", expressResponseDoc("--reason", "axon", expressTestWR, "DP-axon-20260804-0000"))
		_, err := rig.core.expressSnapshot(expressTestWR)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("expressSnapshot err = %v, want a stop naming %s", err, bad)
		}
	})
}

// TestExpressContractRefBindsToThePin is the audit's finding 17, deliver side.
// An explicit --contract names a contract the request pins or it is refused:
// preferring it silently packs the producer's data against a contract the
// consumer never asked for.
func TestExpressContractRefBindsToThePin(t *testing.T) {
	t.Parallel()
	doc := func(refs ...string) expressDoc {
		d := expressDoc{ID: expressTestWR}
		for _, ref := range refs {
			d.Refs = append(d.Refs, struct {
				Ref string `yaml:"ref"`
			}{Ref: ref})
		}
		return d
	}
	for _, tc := range []struct {
		name     string
		doc      expressDoc
		explicit string
		want     string
		wantErr  []string
	}{
		{"the one pin", doc("XC-axon-widget@1.0.0"), "", "XC-axon-widget@1.0.0", nil},
		{"an explicit ref equal to the one pin", doc("XC-axon-widget@1.0.0"), "XC-axon-widget@1.0.0", "XC-axon-widget@1.0.0", nil},
		{"an explicit ref carrying a digest", doc("XC-axon-widget@1.0.0"), "XC-axon-widget@1.0.0#sha256:" + strings.Repeat("a", 64), "XC-axon-widget@1.0.0", nil},
		{"an explicit ref of another version", doc("XC-axon-widget@1.0.0"), "XC-axon-widget@2.0.0", "", []string{"XC-axon-widget@2.0.0", "XC-axon-widget@1.0.0"}},
		{"an explicit ref of another contract", doc("XC-axon-widget@1.0.0"), "XC-other-thing@1.0.0", "", []string{"XC-other-thing@1.0.0", "XC-axon-widget@1.0.0"}},
		{"no pin: the caller names it", doc(), "XC-axon-widget@1.0.0", "XC-axon-widget@1.0.0", nil},
		{"no pin and no ref", doc(), "", "", []string{"pins 0"}},
		{"several pins: the caller chooses one of them", doc("XC-axon-widget@1.0.0", "XC-axon-gadget@1.0.0"), "XC-axon-gadget@1.0.0", "XC-axon-gadget@1.0.0", nil},
		{"several pins: a ref outside them is refused", doc("XC-axon-widget@1.0.0", "XC-axon-gadget@1.0.0"), "XC-other-thing@1.0.0", "", []string{"XC-other-thing@1.0.0", "XC-axon-widget@1.0.0", "XC-axon-gadget@1.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := expressContractRef(tc.doc, tc.explicit)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expressContractRef = %q, nil error, want a refusal naming %v", got, tc.wantErr)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("expressContractRef err = %v, want it to name %q", err, want)
					}
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("expressContractRef = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// TestExpressVerifyForBindsThePackageToThePinnedContract is the audit's
// finding 17, verify side. The package manifest names the contract it was
// packed against; judging it against that contract and closing the request
// with ac1 met would certify data the consumer never asked for.
func TestExpressVerifyForBindsThePackageToThePinnedContract(t *testing.T) {
	t.Parallel()
	withManifestContract := func(t *testing.T, core *dataCore, contractRef string) {
		t.Helper()
		doc, payload := dataWiringTestDocument(t, []byte(`{"ok":true}`))
		doc.Contract = contractRef
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		core.resolveDataPackage = func(context.Context, string) (space.DataPackageBytes, error) {
			return space.DataPackageBytes{System: "axon", Root: "axon/data/" + doc.ID, ManifestRaw: raw, Payload: payload}, nil
		}
	}

	for _, tc := range []struct {
		name, packed string
	}{
		{"another contract", "XC-other-thing@1.0.0#sha256:" + strings.Repeat("b", 64)},
		{"another version", "XC-axon-widget@2.0.0#sha256:" + strings.Repeat("b", 64)},
		{"a prerelease of the pinned version", "XC-axon-widget@1.0.0-rc.1#sha256:" + strings.Repeat("b", 64)},
	} {
		for _, record := range []bool{false, true} {
			name := tc.name + ", judged only"
			if record {
				name = tc.name + ", recorded"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				core, funnel, fake, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
				withManifestContract(t, core, tc.packed)
				result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: record})
				if err == nil {
					t.Fatalf("verifyFor = nil error, want a refusal (steps %v)", expressStepSummary(result.Steps))
				}
				for _, want := range []string{tc.packed, "XC-axon-widget@1.0.0"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("verifyFor err = %v, want it to name %q", err, want)
					}
				}
				if result.Report != nil || len(funnel.calls) != 0 || len(fake.Pushes) != 0 {
					t.Fatalf("the package was judged or written before the refusal: report=%v inner=%d pushes=%d", result.Report != nil, len(funnel.calls), len(fake.Pushes))
				}
			})
		}
	}

	t.Run("the pinned contract at another digest is the same contract", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
		const packed = "XC-axon-widget@1.0.0#sha256:" + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		withManifestContract(t, core, packed)
		// verify itself re-resolves the contract and compares digests.
		core.resolveContractSchemas = func(context.Context, string) (map[string][]byte, string, error) {
			return map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}, packed, nil
		}
		if result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true}); err != nil {
			t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		if len(funnel.calls) != 1 || funnel.calls[0].Verb != "close" {
			t.Fatalf("inner writes = %v, want the close", expressInnerVerbs(funnel))
		}
	})

	t.Run("a request that pins no contract has nothing to bind to: the package is judged against its own", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
		path := filepath.Join(core.mirrorDir, "peer", "exchanges", expressTestWR+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		unpinned := strings.Replace(string(raw), "refs:\n  - ref: XC-axon-widget@1.0.0\n", "", 1)
		if unpinned == string(raw) {
			t.Fatal("the fixture request carries no pin to remove")
		}
		if err := os.WriteFile(path, []byte(unpinned), 0o644); err != nil {
			t.Fatal(err)
		}
		if result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true}); err != nil {
			t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		if len(funnel.calls) != 1 || funnel.calls[0].Verb != "close" {
			t.Fatalf("inner writes = %v, want the close", expressInnerVerbs(funnel))
		}
	})
}

// TestExpressDeliverForBindsThePackageToThePinnedContract is the audit's
// finding 17, deliver side: nothing is delivered whose manifest names another
// contract than the request pins, whichever way the contract reached the pack.
func TestExpressDeliverForBindsThePackageToThePinnedContract(t *testing.T) {
	t.Parallel()

	t.Run("an explicit --contract that differs from the pin is refused before any pack", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		req := rig.req()
		req.Contract = "XC-axon-widget@2.0.0"
		_, err := rig.core.deliverFor(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "XC-axon-widget@2.0.0") || !strings.Contains(err.Error(), "XC-axon-widget@1.0.0") {
			t.Fatalf("deliverFor err = %v, want a refusal naming both refs", err)
		}
		matches, _ := filepath.Glob(filepath.Join(rig.core.stagingDir, "data", "*"))
		if len(matches) != 0 || len(rig.fake.Opens) != 0 || len(rig.funnel.calls) != 0 {
			t.Fatalf("the flow went on past the refusal: staged=%v opens=%d inner=%d", matches, len(rig.fake.Opens), len(rig.funnel.calls))
		}
	})

	t.Run("a package packed against another contract is not delivered", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
		// Whatever ref pack asks for, the contract it resolves (and stamps into the
		// manifest) is version 2.0.0.
		rig.core.resolveContractSchemas = func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@2.0.0#sha256:" + strings.Repeat("a", 64), nil
		}
		_, err := rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), "XC-axon-widget@2.0.0") || !strings.Contains(err.Error(), "XC-axon-widget@1.0.0") {
			t.Fatalf("deliverFor err = %v, want a refusal naming both refs", err)
		}
		if len(rig.fake.Opens) != 0 || len(rig.fake.Pushes) != 0 {
			t.Fatalf("a package of another contract was delivered: opens=%d pushes=%d", len(rig.fake.Opens), len(rig.fake.Pushes))
		}
	})

	// A prerelease spelling is no <XC-id>@<major.minor.patch> the flow can key
	// on, so the staged package is neither resumed as the pinned version nor
	// passed over as "another contract" (wave 5 audit finding F4): the run stops
	// and names the staged manifest.
	t.Run("a staged package of a prerelease of the pinned version is not resumed", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
		rig.core.resolveContractSchemas = func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@1.0.0-rc.1#sha256:" + strings.Repeat("a", 64), nil
		}
		packed, err := rig.core.pack(context.Background(), cli.DataPackRequest{
			ContractRef: "XC-axon-widget@1.0.0-rc.1", From: rig.from, Profile: "synthetic", Format: "json", Fulfills: expressTestWR,
		})
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		manifest := filepath.Join(packed.StagingRoot, stagedManifestName)
		staged, err := rig.core.expressStagedCandidates(packed.Manifest.Thread, "XC-axon-widget@1.0.0")
		if err == nil || len(staged) != 0 || !strings.Contains(err.Error(), manifest) {
			t.Fatalf("expressStagedCandidates = %v, %v, want the rc package refused, naming %s", staged, err, manifest)
		}
	})
}

// TestExpressStagedCandidatesRefusesAnUnparseableContract is wave 5's audit
// finding F4. A staged package of this thread whose contract ref does not parse
// was passed over as "another contract", and a fresh package was packed beside
// it: an error read as a negative answer. It is refused, naming the staged
// manifest, and deliverFor packs nothing. A staged package of another thread is
// another thread's whatever its ref says, so it stays out of the way.
func TestExpressStagedCandidatesRefusesAnUnparseableContract(t *testing.T) {
	t.Parallel()
	stage := func(t *testing.T, rig *expressRetryRig, id, thread, contractRef string) string {
		t.Helper()
		dir := filepath.Join(rig.core.stagingDir, "data", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(datapackage.Document{ID: id, Thread: thread, Contract: contractRef})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, stagedManifestName)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	threadOf := func(t *testing.T, rig *expressRetryRig) string {
		t.Helper()
		snap, err := rig.core.expressSnapshot(expressTestWR)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return snap.doc.Thread
	}

	t.Run("this thread's staged package with no version is refused", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		manifest := stage(t, rig, "DP-axon-20260804-aaa1", threadOf(t, rig), "XC-axon-widget")
		staged, err := rig.core.expressStagedCandidates(threadOf(t, rig), "XC-axon-widget@1.0.0")
		if err == nil || len(staged) != 0 || !strings.Contains(err.Error(), manifest) || !strings.Contains(err.Error(), "XC-axon-widget") {
			t.Fatalf("expressStagedCandidates = %v, %v, want a refusal naming %s", staged, err, manifest)
		}
		result, err := rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), manifest) {
			t.Fatalf("deliverFor err = %v, want the refusal naming %s (steps %v)", err, manifest, expressStepSummary(result.Steps))
		}
		matches, _ := filepath.Glob(filepath.Join(rig.core.stagingDir, "data", "*"))
		if len(matches) != 1 || len(rig.fake.Opens) != 0 {
			t.Fatalf("the flow packed or delivered past the refusal: staged=%v opens=%d", matches, len(rig.fake.Opens))
		}
	})

	t.Run("another thread's staged package is not this thread's business", func(t *testing.T) {
		t.Parallel()
		rig := newExpressRetryRig(t)
		stage(t, rig, "DP-axon-20260804-aaa2", "thread:peer-20260804-0th3", "XC-axon-widget")
		staged, err := rig.core.expressStagedCandidates(threadOf(t, rig), "XC-axon-widget@1.0.0")
		if err != nil || len(staged) != 0 {
			t.Fatalf("expressStagedCandidates = %v, %v, want no candidates and no error", staged, err)
		}
	})
}

// TestExpressDeliverRefusesAForgedHandoffInTheDeliveryPR is the audit's
// finding 18. When the delivery write converges on a PR that already exists on
// the deterministic head branch, the artifact ids it reports are read from that
// PR's body, which anyone who can open a PR can write. A handoff id that the
// PR's commit does not carry is refused: no supersede, no respond and no wait
// names it, and the minted id is not substituted.
func TestExpressDeliverRefusesAForgedHandoffInTheDeliveryPR(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)

	// Run 1 opens the delivery PR and never sees it merge.
	rig.failWait = context.DeadlineExceeded
	first, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first deliverFor err = %v, want the unmerged delivery to time out", err)
	}
	if len(rig.fake.Opens) != 1 {
		t.Fatalf("first run opened %d PRs, want 1", len(rig.fake.Opens))
	}
	open := rig.fake.Opens[0]
	if !strings.Contains(open.Body, first.Handoff) {
		t.Fatalf("the delivery PR body does not name %s:\n%s", first.Handoff, open.Body)
	}

	// The body of the PR on that branch now names a handoff the commit does not carry.
	const forged = "XH-axon-20260804-f0f0"
	real, err := rig.fake.FindPRByHeadBranch(context.Background(), host.FindPRRequest{Repo: open.Repo, Branch: open.Head})
	if err != nil || real == nil {
		t.Fatalf("FindPRByHeadBranch = %v, %v, want the open PR", real, err)
	}
	forgedPR := *real
	forgedPR.Body = strings.ReplaceAll(real.Body, first.Handoff, forged)
	rig.fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) { return &forgedPR, nil }

	rig.failWait = nil
	waits := len(rig.waits)
	second, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !strings.Contains(err.Error(), forged) {
		t.Fatalf("second deliverFor err = %v, want a refusal naming the forged handoff %s (steps %v)", err, forged, expressStepSummary(second.Steps))
	}
	if second.Handoff == forged || second.Handoff == first.Handoff {
		t.Fatalf("second run reports handoff %q: neither the forged id nor the minted one may be adopted", second.Handoff)
	}
	if got := rig.verbs(); len(got) != 0 {
		t.Fatalf("inner writes = %v, want none: nothing may be built on the forged handoff", got)
	}
	if len(rig.waits) != waits {
		t.Fatalf("the run waited on the forged PR: %v", rig.waits[waits:])
	}
}

// TestExpressDeliverRefusesASelfConsistentForgery is wave 5's audit finding F3
// (audits/p4b-p5fix2-w4-2026-10-28.md). Whoever pushes the delivery PR's head
// commit writes both its tree and the PR body, so a forgery can be consistent
// with itself: a head commit carrying a handoff of this system, for this
// request and this package, under the id the body names. Reading that commit
// proves nothing. The handoff the later verbs build on is the one that merged,
// read from the mirror after the merge, and a main that carries more than one
// such handoff (here the real one and the forged one) is refused before any
// supersede or respond names either.
func TestExpressDeliverRefusesASelfConsistentForgery(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)

	// Run 1 opens the delivery PR and never sees it merge.
	rig.failWait = context.DeadlineExceeded
	first, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first deliverFor err = %v, want the unmerged delivery to time out", err)
	}
	var write *space.WriteResult
	for _, step := range first.Steps {
		if step.Step == "deliver" && step.Write != nil {
			write = step.Write
		}
	}
	if write == nil || write.CommitSHA == "" || len(rig.fake.Opens) != 1 {
		t.Fatalf("no open delivery PR with a commit after run 1: steps %v, opens %d", expressStepSummary(first.Steps), len(rig.fake.Opens))
	}

	// The forger pushes a head commit on top of the delivery that carries a second,
	// fully consistent handoff, and rewrites the PR body to name it.
	const forged = "XH-axon-20260804-f0f0"
	mirror := rig.core.mirrorDir
	branch := strings.TrimSpace(gitFixtureOutput(t, mirror, "rev-parse", "--abbrev-ref", "HEAD"))
	gitFixtureOutput(t, mirror, "checkout", "--quiet", "--detach", write.CommitSHA)
	rel := filepath.Join("axon", "exchanges", forged+".md")
	if err := os.WriteFile(filepath.Join(mirror, rel), []byte(expressHandoffDoc(forged, "axon", expressTestWR, first.Package)), 0o644); err != nil {
		t.Fatal(err)
	}
	gitFixtureOutput(t, mirror, "add", "--", rel)
	gitFixtureOutput(t, mirror, "commit", "--quiet", "-m", "forged "+forged)
	forgedSHA := strings.TrimSpace(gitFixtureOutput(t, mirror, "rev-parse", "HEAD"))
	gitFixtureOutput(t, mirror, "checkout", "--quiet", branch)

	open := rig.fake.Opens[0]
	real, err := rig.fake.FindPRByHeadBranch(context.Background(), host.FindPRRequest{Repo: open.Repo, Branch: open.Head})
	if err != nil || real == nil {
		t.Fatalf("FindPRByHeadBranch = %v, %v, want the open PR", real, err)
	}
	forgedPR := *real
	forgedPR.Body = strings.ReplaceAll(real.Body, first.Handoff, forged)
	forgedPR.HeadSHA = forgedSHA
	rig.fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) { return &forgedPR, nil }
	// The forged head is what merges.
	rig.core.awaitMerge = func(context.Context, space.AwaitRequest) (space.AwaitResult, error) {
		landBranchIntoMirror(t, mirror, forgedSHA)
		return space.AwaitResult{PRURL: "merged"}, nil
	}

	second, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !strings.Contains(err.Error(), forged) || !strings.Contains(err.Error(), first.Handoff) {
		t.Fatalf("second deliverFor err = %v, want a refusal naming both handoffs on main, %s and %s (steps %v)", err, first.Handoff, forged, expressStepSummary(second.Steps))
	}
	if second.Handoff == forged {
		t.Fatalf("second run reports the forged handoff %s", forged)
	}
	if got := rig.verbs(); len(got) != 0 {
		t.Fatalf("inner writes = %v, want none: nothing may be built on a handoff main does not single out", got)
	}

	// A plain re-run finds both handoffs on main and resumes from the mirror; it
	// must not pick one of them either.
	third, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !strings.Contains(err.Error(), forged) || !strings.Contains(err.Error(), first.Handoff) {
		t.Fatalf("re-run deliverFor err = %v, want the same refusal naming %s and %s (steps %v)", err, first.Handoff, forged, expressStepSummary(third.Steps))
	}
	if got := rig.verbs(); len(got) != 0 {
		t.Fatalf("re-run inner writes = %v, want none", got)
	}
}

// TestExpressMergedHandoffIDSinglesOutTheDelivery pins each answer of the
// post-merge derivation (finding F3): the one producer handoff on main that
// fulfils the request and delivers the package, and a refusal for none, for
// several, for one the PR did not name, and for a run that is not the producer.
func TestExpressMergedHandoffIDSinglesOutTheDelivery(t *testing.T) {
	t.Parallel()
	seeded := func(t *testing.T) (*expressRetryRig, string) {
		t.Helper()
		rig := newExpressRetryRig(t)
		snap, err := rig.core.expressSnapshot(expressTestWR)
		if err != nil || len(snap.handoffs) != 1 || snap.handoffs[0].id != expressOldHandoff {
			t.Fatalf("snapshot = %+v, %v, want the one seeded handoff %s", snap.handoffs, err, expressOldHandoff)
		}
		return rig, snap.handoffs[0].packageID
	}

	t.Run("the one handoff on main is the answer", func(t *testing.T) {
		t.Parallel()
		rig, pkg := seeded(t)
		got, err := rig.core.mergedHandoffID(expressTestWR, pkg, expressOldHandoff)
		if err != nil || got != expressOldHandoff {
			t.Fatalf("mergedHandoffID = %q, %v, want %s", got, err, expressOldHandoff)
		}
	})
	t.Run("no handoff on main delivers the package", func(t *testing.T) {
		t.Parallel()
		rig, _ := seeded(t)
		const other = "DP-axon-20260804-0000"
		got, err := rig.core.mergedHandoffID(expressTestWR, other, expressOldHandoff)
		if err == nil || got != "" || !strings.Contains(err.Error(), "no handoff") || !strings.Contains(err.Error(), other) {
			t.Fatalf("mergedHandoffID = %q, %v, want a refusal naming no handoff for %s", got, err, other)
		}
	})
	t.Run("main carries a second handoff of the same package", func(t *testing.T) {
		t.Parallel()
		rig, pkg := seeded(t)
		const second = "XH-axon-20260804-f0f1"
		expressWriteExchange(t, rig.core.mirrorDir, "axon", second, expressHandoffDoc(second, "axon", expressTestWR, pkg))
		got, err := rig.core.mergedHandoffID(expressTestWR, pkg, expressOldHandoff)
		if err == nil || got != "" || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), expressOldHandoff) {
			t.Fatalf("mergedHandoffID = %q, %v, want a refusal naming %s and %s", got, err, expressOldHandoff, second)
		}
	})
	t.Run("the handoff on main is not the one the PR named", func(t *testing.T) {
		t.Parallel()
		rig, pkg := seeded(t)
		const claimed = "XH-axon-20260804-f0f2"
		got, err := rig.core.mergedHandoffID(expressTestWR, pkg, claimed)
		if err == nil || got != "" || !strings.Contains(err.Error(), claimed) || !strings.Contains(err.Error(), expressOldHandoff) {
			t.Fatalf("mergedHandoffID = %q, %v, want a refusal naming %s and %s", got, err, claimed, expressOldHandoff)
		}
	})
	t.Run("an addressee that is not the producer is refused before any write", func(t *testing.T) {
		t.Parallel()
		rig, _ := seeded(t)
		wrPath := filepath.Join(rig.core.mirrorDir, "peer", "exchanges", expressTestWR+".md")
		raw, err := os.ReadFile(wrPath)
		if err != nil {
			t.Fatal(err)
		}
		widened := strings.Replace(string(raw), "\nto: [axon]\n", "\nto: [axon, zeta]\n", 1)
		if widened == string(raw) {
			t.Fatalf("the seeded request has no `to: [axon]` line:\n%s", raw)
		}
		if err := os.WriteFile(wrPath, []byte(widened), 0o644); err != nil {
			t.Fatal(err)
		}
		withParticipants(rig.core)
		rig.core.ownSystem = "zeta"
		_, err = rig.core.deliverFor(context.Background(), rig.req())
		if err == nil || !strings.Contains(err.Error(), "producer is axon") || !strings.Contains(err.Error(), "zeta") {
			t.Fatalf("deliverFor err = %v, want a refusal naming the producer axon and this system zeta", err)
		}
		matches, _ := filepath.Glob(filepath.Join(rig.core.stagingDir, "data", "*"))
		if len(matches) != 0 || len(rig.fake.Opens) != 0 || len(rig.funnel.calls) != 0 {
			t.Fatalf("the flow wrote past the refusal: staged=%v opens=%d inner=%d", matches, len(rig.fake.Opens), len(rig.funnel.calls))
		}
	})
}

// TestExpressDeliveredHandoffIDIsConfirmedAgainstTheCommit is the audit's
// finding 18, validator side: the handoff id in the delivery's write is
// accepted only when the delivery commit carries that handoff, whole.
func TestExpressDeliveredHandoffIDIsConfirmedAgainstTheCommit(t *testing.T) {
	t.Parallel()
	rig := newExpressRetryRig(t)
	rig.failWait = context.DeadlineExceeded // leave the PR open: the write is what we judge
	first, err := rig.core.deliverFor(context.Background(), rig.req())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deliverFor err = %v, want the unmerged delivery to time out", err)
	}
	var write *space.WriteResult
	for _, step := range first.Steps {
		if step.Step == "deliver" && step.Write != nil {
			write = step.Write
		}
	}
	if write == nil || write.CommitSHA == "" {
		t.Fatalf("no deliver write with a commit in steps %v", expressStepSummary(first.Steps))
	}
	handoff := first.Handoff
	pkg := first.Package

	delivered := func(mutate func(w *space.WriteResult)) cli.DataResult {
		w := *write
		w.ArtifactIDs = []string{pkg, handoff}
		if mutate != nil {
			mutate(&w)
		}
		return cli.DataResult{Manifest: &datapackage.Document{ID: pkg}, Write: &w}
	}

	// Commits that DO carry a file at the claimed handoff's path, but a file that is
	// not the producer's handoff for this package: made on detached heads in the
	// mirror, off the delivery commit, before any subtest reads it.
	mirror := rig.core.mirrorDir
	branch := strings.TrimSpace(gitFixtureOutput(t, mirror, "rev-parse", "--abbrev-ref", "HEAD"))
	forgeCommit := func(id, body string) string {
		gitFixtureOutput(t, mirror, "checkout", "--quiet", "--detach", write.CommitSHA)
		rel := filepath.Join("axon", "exchanges", id+".md")
		if err := os.WriteFile(filepath.Join(mirror, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitFixtureOutput(t, mirror, "add", "--", rel)
		gitFixtureOutput(t, mirror, "commit", "--quiet", "-m", "forged "+id)
		sha := strings.TrimSpace(gitFixtureOutput(t, mirror, "rev-parse", "HEAD"))
		gitFixtureOutput(t, mirror, "checkout", "--quiet", branch)
		return sha
	}
	const idMismatch, fromMismatch = "XH-axon-20260804-aa11", "XH-axon-20260804-cc33"
	idMismatchCommit := forgeCommit(idMismatch, expressHandoffDoc("XH-axon-20260804-bb22", "axon", expressTestWR, pkg))
	fromMismatchCommit := forgeCommit(fromMismatch, expressHandoffDoc(fromMismatch, "zeta", expressTestWR, pkg))

	t.Run("a file in the commit whose own id is another is refused", func(t *testing.T) {
		t.Parallel()
		got, err := rig.core.deliveredHandoffID(context.Background(), expressTestWR, delivered(func(w *space.WriteResult) {
			w.CommitSHA, w.ArtifactIDs = idMismatchCommit, []string{pkg, idMismatch}
		}))
		if err == nil || got != "" || !strings.Contains(err.Error(), idMismatch) {
			t.Fatalf("deliveredHandoffID = %q, %v, want a refusal naming %s", got, err, idMismatch)
		}
	})

	t.Run("a handoff in the commit written by another system is refused", func(t *testing.T) {
		t.Parallel()
		got, err := rig.core.deliveredHandoffID(context.Background(), expressTestWR, delivered(func(w *space.WriteResult) {
			w.CommitSHA, w.ArtifactIDs = fromMismatchCommit, []string{pkg, fromMismatch}
		}))
		if err == nil || got != "" || !strings.Contains(err.Error(), fromMismatch) {
			t.Fatalf("deliveredHandoffID = %q, %v, want a refusal naming %s", got, err, fromMismatch)
		}
	})

	t.Run("the handoff in the commit is accepted", func(t *testing.T) {
		t.Parallel()
		got, err := rig.core.deliveredHandoffID(context.Background(), expressTestWR, delivered(nil))
		if err != nil || got != handoff {
			t.Fatalf("deliveredHandoffID = %q, %v, want %s", got, err, handoff)
		}
	})

	for _, tc := range []struct {
		name     string
		mutate   func(w *space.WriteResult)
		wr       string
		manifest string
		want     string // a substring of the refusal
	}{
		{"a forged id absent from the commit", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, "XH-axon-20260804-f0f0"} }, "", "", "XH-axon-20260804-f0f0"},
		{"the seeded handoff of the earlier attempt", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, expressOldHandoff} }, "", "", expressOldHandoff},
		{"two handoff ids", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, handoff, "XH-axon-20260804-f0f0"} }, "", "", "names artifacts"},
		{"no handoff id", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg} }, "", "", "names artifacts"},
		{"no package id", func(w *space.WriteResult) { w.ArtifactIDs = []string{handoff} }, "", "", "names artifacts"},
		{"an extra id", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, handoff, "../../escape"} }, "", "", "names artifacts"},
		{"a handoff id of another system", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, "XH-zeta-20260804-f0f0"} }, "", "", "XH-zeta-20260804-f0f0"},
		{"a malformed handoff id", func(w *space.WriteResult) { w.ArtifactIDs = []string{pkg, "XH-../x"} }, "", "", "XH-../x"},
		{"no commit reported", func(w *space.WriteResult) { w.CommitSHA = "" }, "", "", "no commit"},
		{"a commit that is not in the object store", func(w *space.WriteResult) { w.CommitSHA = strings.Repeat("1", 40) }, "", "", handoff},
		{"a request the handoff does not fulfil", nil, "XW-peer-20260804-0000", "", "XW-peer-20260804-0000"},
		{"another package than the handoff delivers", nil, "", "DP-axon-20260804-0000", handoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := delivered(tc.mutate)
			if tc.manifest != "" {
				result.Manifest = &datapackage.Document{ID: tc.manifest}
				result.Write.ArtifactIDs = []string{tc.manifest, handoff}
			}
			wr := expressTestWR
			if tc.wr != "" {
				wr = tc.wr
			}
			got, err := rig.core.deliveredHandoffID(context.Background(), wr, result)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("deliveredHandoffID = %q, %v, want a refusal naming %q", got, err, tc.want)
			}
			if got != "" {
				t.Fatalf("deliveredHandoffID returned %q beside the refusal", got)
			}
		})
	}

	t.Run("a host that cannot read the commit confirms nothing", func(t *testing.T) {
		t.Parallel()
		plain := *rig.core
		plain.host = rig.fake
		got, err := plain.deliveredHandoffID(context.Background(), expressTestWR, delivered(nil))
		if err == nil || got != "" {
			t.Fatalf("deliveredHandoffID = %q, %v, want a refusal", got, err)
		}
	})
}

// TestExpressBindContractHonoursAPinnedDigest: a pin that carries a digest binds
// the digest too; one that does not leaves the digest to verification itself.
func TestExpressBindContractHonoursAPinnedDigest(t *testing.T) {
	t.Parallel()
	a := "XC-axon-widget@1.0.0#sha256:" + strings.Repeat("a", 64)
	b := "XC-axon-widget@1.0.0#sha256:" + strings.Repeat("b", 64)
	if err := expressBindContract(expressTestWR, []string{a}, a); err != nil {
		t.Fatalf("same digest: %v", err)
	}
	if err := expressBindContract(expressTestWR, []string{"XC-axon-widget@1.0.0"}, b); err != nil {
		t.Fatalf("a pin without a digest: %v", err)
	}
	if err := expressBindContract(expressTestWR, []string{a}, b); err == nil || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Fatalf("another digest: err = %v, want a refusal naming both refs", err)
	}
	if err := expressBindContract(expressTestWR, []string{a, "XC-axon-widget@1.0.0"}, b); err != nil {
		t.Fatalf("one of several pins matches: %v", err)
	}
}
