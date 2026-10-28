package main

// Tests for the express data flow's resume paths and refusals (spec 05 §8 rows
// 6-9 and the P5 audit, docs/features/active/local-space-2026-10/audits/
// p5-w2b-2026-10-28.md). The rigs here drive the real sequencer over a real
// git mirror, with the producer's delivery going through the real write funnel
// over a fake host (so a delivery really is a PR that can stay open) and the
// inner lifecycle verbs going through the scripted funnel.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
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
