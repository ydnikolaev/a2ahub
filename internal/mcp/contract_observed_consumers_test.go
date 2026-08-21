package mcp

// contract_observed_consumers_test.go is the MCP half of §8 row 7 — the
// epic's AC5 (spec 03-observed-consumption.md): `a2a contract retire` and
// `a2a_contract_retire` are two doors onto ONE rule, and a rule one door can
// express and the other cannot is the B22 shape this epic exists partly to
// stop recurring.
//
// Its CLI twin is internal/cli/contract_observed_consumers_test.go. The two
// build the SAME mirror fixture (observedRetireFixture, duplicated per
// package because each surface's contractBuildRetirePrecondition is
// unexported to its own package — ADR-001's mirrored-not-imported shape,
// exactly as contractFindDeprecationAnnouncement/contractSunsetPassed
// already are) and assert the SAME notice string, byte for byte, through
// the shared const below. If either surface drifts, one of the two reds.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// wantObservedRetireNotice is the line BOTH surfaces must produce for
// observedRetireFixture. It is a literal, not a call to
// validate.ObservedConsumptionNotice, so that a change to the shared
// renderer is seen here rather than silently agreed with.
const wantObservedRetireNotice = "0 declared consumer(s), 1 observed and undeclared: axon (2 packages) — " +
	"their own verify-passed deliveries pin this contract while they declare it nowhere. " +
	"Observed consumption never blocks retire (§9); it is named so this decision is not made blind. " +
	"Each of them can exit with `a2a contract adopt` (declare the dependency) or by acknowledging the deprecation — no new verb, either way"

// observedRetireFixture is the live case fb-20260820-0cb8c8 reports, in
// miniature: seomatrix publishes XC-seomatrix-regime-corpus, axon
// verify-passed two data packages pinning it, and axon's own consumes.yaml
// declares nothing. Retire's declared-consumer set is therefore EMPTY and
// the retire is clean — which is the defect, not the happy path.
func observedRetireFixture(t *testing.T) (mirrorDir string, manifest space.Manifest) {
	t.Helper()
	mirrorDir = t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(mirrorDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	for i, pkg := range []string{"DP-seomatrix-20260818-p3my", "DP-seomatrix-20260818-n2yp"} {
		write("seomatrix/data/"+pkg+"/manifest.json",
			`{"schema":"data-package/v1","id":"`+pkg+`","contract":"XC-seomatrix-regime-corpus@1.0.0#aaa111"}`)

		id := []string{"XH-seomatrix-20260817-aaaa", "XH-seomatrix-20260817-bbbb"}[i]
		write("seomatrix/"+id+".md",
			"---\nschema: envelope/v1\nid: "+id+"\ntype: handoff\ntitle: t\nspace: fixture-space\n"+
				"from: seomatrix\nto: [axon]\ncreated: 2026-08-17T10:00:00Z\n"+
				"deliverables:\n  - name: dataset\n    ref: "+pkg+"\n    kind: data\n"+
				"verification: manual\nacceptance_criteria: [\"done\"]\nlimitations: []\n"+
				"fulfills: [\"XW-seomatrix-20260817-zzzz\"]\n---\nBody.\n")

		for j, step := range []struct{ transition, system string }{
			{"submit", "seomatrix"}, {"acknowledge", "axon"}, {"verify-pass", "axon"},
		} {
			ulid := "01HFXH" + string(rune('A'+i)) + "0000000000000000" + string(rune('1'+j))
			write("seomatrix/events/2026/"+ulid+".yaml",
				"schema: event/v1\nevent: "+ulid+"\nspace: fixture-space\n"+
					"subject: "+id+"\ntransition: "+step.transition+"\n"+
					"actor: {kind: agent, name: bot, system: "+step.system+"}\n"+
					"at: 2026-08-17T1"+string(rune('0'+j))+":00:00Z\n")
		}
	}

	manifest = space.Manifest{
		Schema: "space/v1", Space: "fixture-space", MinBinaryVersion: "0.0.0",
		Participants: []space.Participant{
			{System: "axon", Status: "active"},
			{System: "seomatrix", Status: "active"},
		},
	}
	return mirrorDir, manifest
}

// TestRetirePreconditionCarriesObservedConsumers is §8 criteria 1 and 7 on
// this surface.
//
// TEETH: drop the cache.FindObservedConsumers call from
// contractBuildRetirePrecondition and this reds with an empty notice —
// which is precisely today's behaviour, and the whole of the reported
// defect: internal/validate has no reference to the delivery path, so a
// producer retiring this contract sees "no registered consumers" and
// proceeds, blind, out from under a live consumer.
func TestRetirePreconditionCarriesObservedConsumers(t *testing.T) {
	t.Parallel()
	mirrorDir, manifest := observedRetireFixture(t)

	pre, err := contractBuildRetirePrecondition(mirrorDir, manifest, "XC-seomatrix-regime-corpus", "1.0.0",
		false, false, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("contractBuildRetirePrecondition: %v", err)
	}

	if len(pre.Consumers) != 0 {
		t.Fatalf("Consumers = %+v, want empty — nobody DECLARED this contract; the fixture's point is that the declared set is blind here", pre.Consumers)
	}
	if got := validate.ObservedConsumptionNotice(pre); got != wantObservedRetireNotice {
		t.Fatalf("notice mismatch\n got: %q\nwant: %q", got, wantObservedRetireNotice)
	}

	// §8 criterion 2: the fact is carried, and it refuses nothing.
	violation, overridden := validate.CheckRetirePrecondition(pre)
	if violation != nil {
		t.Fatalf("CheckRetirePrecondition refused a retire with only OBSERVED consumption: %+v — observed consumption gates nothing (§9)", violation)
	}
	if len(overridden) != 0 {
		t.Fatalf("overridden = %v, want empty", overridden)
	}
}

// TestRetirePreconditionIsUnchangedWithoutObservation is §8 criterion 4 on
// this surface: strip the deliveries and the precondition is exactly what
// it was before this phase — no observed set, no notice, nothing printed.
func TestRetirePreconditionIsUnchangedWithoutObservation(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirrorDir, "axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirrorDir, "axon", "consumes.yaml"),
		[]byte("schema: consumes/v1\nsystem: axon\ndependencies: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := space.Manifest{
		Schema: "space/v1", Space: "fixture-space",
		Participants: []space.Participant{{System: "axon", Status: "active"}, {System: "seomatrix", Status: "active"}},
	}

	pre, err := contractBuildRetirePrecondition(mirrorDir, manifest, "XC-seomatrix-regime-corpus", "1.0.0",
		false, false, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("contractBuildRetirePrecondition: %v", err)
	}
	if len(pre.Observed) != 0 {
		t.Fatalf("Observed = %+v, want empty", pre.Observed)
	}
	if got := validate.ObservedConsumptionNotice(pre); got != "" {
		t.Fatalf("notice = %q, want \"\" — with nothing observed, retire's output must be byte-identical to what it was", got)
	}
}

// TestContractRetireResultCarriesTheObservedNotice is §8 row 7's SURFACING
// half on this door. internal/cli prints the line to stderr; MCP has no
// stderr, so the same words must reach the caller as structured data or the
// rule is CLI-only — B22 exactly.
func TestContractRetireResultCarriesTheObservedNotice(t *testing.T) {
	t.Parallel()

	base := submitResult{Verb: "contract retire", IDs: []string{"XC-seomatrix-regime-corpus"}, State: "merged"}
	pre := validate.RetirePrecondition{
		Observed: []validate.ObservedConsumer{{System: "axon", Packages: 2}},
	}

	widened := contractRetireWithObserved(base, pre)
	raw, err := json.Marshal(widened)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Every §7.7 field the write contract already promised is still there,
	// at the same key: the embedded struct must not have nested itself.
	for _, key := range []string{"verb", "ids", "state"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("widened result dropped %q — embedding must promote submitResult's own fields, not nest them: %s", key, raw)
		}
	}
	notice, ok := decoded["observed_consumption"].(string)
	if !ok || notice != validate.ObservedConsumptionNotice(pre) {
		t.Fatalf("observed_consumption = %v, want the shared notice — this surface must say what the CLI says: %s", decoded["observed_consumption"], raw)
	}
	consumers, ok := decoded["observed_consumers"].([]any)
	if !ok || len(consumers) != 1 {
		t.Fatalf("observed_consumers missing or wrong shape — an agent-to-agent surface must be able to branch without parsing prose: %s", raw)
	}
	entry, ok := consumers[0].(map[string]any)
	if !ok || entry["system"] != "axon" || entry["packages"] != float64(2) {
		t.Fatalf("observed_consumers[0] = %v, want snake_case {system, packages} beside this surface's own pr_url/remaining_action: %s", consumers[0], raw)
	}
}

// TestContractRetireResultIsUntouchedWithoutObservation is §8 criterion 4
// on this door: with nothing observed the result is the SAME value, of the
// same type, with no new key — a plain retire's structured shape is
// byte-identical to what it was before this phase.
func TestContractRetireResultIsUntouchedWithoutObservation(t *testing.T) {
	t.Parallel()

	base := submitResult{Verb: "contract retire", IDs: []string{"XC-seomatrix-regime-corpus"}, State: "merged"}
	got := contractRetireWithObserved(base, validate.RetirePrecondition{})
	if _, widened := got.(contractRetireResult); widened {
		t.Fatalf("got a widened result with nothing observed: %+v", got)
	}
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("got %+v, want the original submitResult unchanged", got)
	}
}

// TestContractRetireResultKeepsAPartialWriteResult pins the non-fatal type
// assertion: a funnel result this function cannot recognise is returned
// untouched rather than dropped for the sake of an advisory. The P4 partial
// -write path returns a result ALONGSIDE an error, and that is exactly when
// a caller most needs the PR/stage facts.
func TestContractRetireResultKeepsAPartialWriteResult(t *testing.T) {
	t.Parallel()

	foreign := struct{ Branch string }{Branch: "a2a/contract-retire"}
	pre := validate.RetirePrecondition{Observed: []validate.ObservedConsumer{{System: "axon", Packages: 2}}}
	if got := contractRetireWithObserved(foreign, pre); !reflect.DeepEqual(got, foreign) {
		t.Fatalf("got %+v, want the unrecognised result returned untouched", got)
	}
}

// writeObservedDelivery seeds one accepted (verify-passed) handoff from
// axon to `to`, carrying one data package that pins contractRef — the
// evidence FindUnadoptedConsumption walks, in the layout
// packageresolver.go resolves. Mirrors internal/cli's own helper.
func writeObservedDelivery(t *testing.T, mirrorDir, to, handoffID, packageID, contractRef string) {
	t.Helper()
	writeMirrorFile(t, mirrorDir, "axon/data/"+packageID+"/manifest.json",
		`{"schema":"data-package/v1","id":"`+packageID+`","contract":"`+contractRef+`"}`)
	writeMirrorFile(t, mirrorDir, "axon/"+handoffID+".md",
		"---\nschema: envelope/v1\nid: "+handoffID+"\ntype: handoff\ntitle: t\nspace: fixture-space\n"+
			"from: axon\nto: ["+to+"]\ncreated: 2026-08-17T10:00:00Z\n"+
			"deliverables:\n  - name: dataset\n    ref: "+packageID+"\n    kind: data\n"+
			"verification: manual\nacceptance_criteria: [\"done\"]\nlimitations: []\n"+
			"fulfills: [\"XW-axon-20260817-zzzz\"]\n---\nBody.\n")
	writeLifecycleEvent(t, mirrorDir, "axon", 40, handoffID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 41, handoffID, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, "axon", 42, handoffID, "verify-pass", to)
}

// TestContractRetireHandlerNamesObservedConsumersAndProceeds is §8 criteria
// 1 and 7 end to end on THIS door: the handler itself, not the helper.
//
// TEETH: revert the handler's return to `result, "", err` — dropping the
// contractRetireWithObserved wrap — and this reds while every unit test
// around it stays green. That is exactly the B22 shape AC5 exists to catch:
// a rule the CLI expresses and MCP silently does not.
func TestContractRetireHandlerNamesObservedConsumersAndProceeds(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "observed", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-observed", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-observed", "deprecate", "axon")

	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-aaaa", "DP-axon-20260818-p3my", "XC-axon-observed@1.0.0#aaa111")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-bbbb", "DP-axon-20260818-n2yp", "XC-axon-observed@1.0.0#bbb222")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-observed"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed: %v — observed consumption never blocks retire (§9)", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an UNGATED retire that proceeded, got %+v", fake.calls)
	}

	raw, merr := json.Marshal(result)
	if merr != nil {
		t.Fatalf("marshal result: %v", merr)
	}
	for _, want := range []string{
		"0 declared consumer(s), 1 observed and undeclared",
		"beta (2 packages)",
		"never blocks retire",
		"a2a contract adopt",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the retire result does not carry %q — this door is still silent where the CLI speaks (§8 row 7).\nresult: %s", want, raw)
		}
	}
}

// TestContractRetireHandlerSaysNothingWithoutObservation is §8 criterion 4
// on this door: the same contract with no deliveries returns a result with
// no observation key at all.
func TestContractRetireHandlerSaysNothingWithoutObservation(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "plain", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-plain", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-plain", "deprecate", "axon")
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-plain"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "observed") {
		t.Fatalf("a plain retire's result mentioned observation: %s", raw)
	}
}

// TestContractRetireHandlerDropsAnObservedConsumerThatAlreadyAcked is the
// MCP twin of internal/cli's own acked edge case (§6, §11 A5). It is here
// because AC5 is about rules, not lines: a filter that quiets one door and
// nags on the other is the same B22 defect wearing different clothes.
func TestContractRetireHandlerDropsAnObservedConsumerThatAlreadyAcked(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "acked", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-acked", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-acked", "deprecate", "axon")

	writeMirrorFile(t, mirrorDir, "axon/exchanges/XA-axon-20260721-ackd.md",
		"---\nschema: envelope/v1\nid: XA-axon-20260721-ackd\ntype: announcement\ntitle: t\nspace: fixture-space\n"+
			"from: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n"+
			"category: deprecation\npriority: p2\nblocking: false\nack_requested: true\n"+
			"deprecates: XC-axon-acked@1.0.0\nvalid_until: 2099-01-01\nclassification: internal\n---\nbody\n")
	writeLifecycleEvent(t, mirrorDir, "axon", 10, "XA-axon-20260721-ackd", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 11, "XA-axon-20260721-ackd", "acknowledge", "beta")

	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-aaaa", "DP-axon-20260818-p3my", "XC-axon-acked@1.0.0#aaa111")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-acked"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "observed and undeclared") {
		t.Fatalf("beta acknowledged the deprecation and was still named as an observed risk: %s", raw)
	}
}
