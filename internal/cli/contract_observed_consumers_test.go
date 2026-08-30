package cli

// contract_observed_consumers_test.go is the CLI half of §8 row 7 — the
// epic's AC5 (spec 03-observed-consumption.md): `a2a contract retire` and
// `a2a_contract_retire` are two doors onto ONE rule, and a rule one door can
// express and the other cannot is the B22 shape this epic exists partly to
// stop recurring.
//
// Its MCP twin is internal/mcp/contract_observed_consumers_test.go. The two
// build the SAME mirror fixture (observedRetireFixture, duplicated per
// package because each surface's contractBuildRetirePrecondition is
// unexported to its own package — ADR-001's mirrored-not-imported shape,
// exactly as contractFindDeprecationAnnouncement/contractSunsetPassed
// already are) and assert the SAME notice string, byte for byte, through
// the shared const below. If either surface drifts, one of the two reds.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// wantObservedRetireNotice is the line BOTH surfaces must produce for
// observedRetireFixture. It is a literal, not a call to
// validate.ObservedConsumptionNotice, so that a change to the shared
// renderer is seen here rather than silently agreed with.
const wantObservedRetireNotice = "0 declared consumer(s), 1 observed and undeclared: axon (2 packages @ 1.0.0) — " +
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
			{System: "axon", Status: fold.MembershipMember},
			{System: "seomatrix", Status: fold.MembershipMember},
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
		Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}, {System: "seomatrix", Status: fold.MembershipMember}},
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
