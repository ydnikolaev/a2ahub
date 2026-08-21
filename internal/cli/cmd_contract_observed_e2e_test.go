package cli_test

// cmd_contract_observed_e2e_test.go drives the REAL `contract retire`
// command for spec 03-observed-consumption.md §8 criterion 1 — "`retire`
// names observed-but-undeclared systems IN ITS OUTPUT and PROCEEDS".
//
// Its sibling (internal/cli/contract_observed_consumers_test.go, package
// cli) asserts that contractBuildRetirePrecondition RESOLVES the fact and
// that the shared renderer formats it. Neither of those touches the one
// line the criterion is actually about: whether the notice reaches a human
// (or an agent) reading the command. Delete the Fprintf from runRetire and
// every test in that file stays green — this one reds, which is the whole
// reason it exists separately.

import (
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// writeObservedDelivery seeds one accepted (verify-passed) handoff from
// axon to `to`, carrying one data package that pins contractRef — the
// evidence FindUnadoptedConsumption walks, in the layout
// packageresolver.go resolves.
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
	// submit (producer) -> acknowledge (target) -> verify-pass (target):
	// §3.4.5's ONE row that folds a handoff to `accepted`.
	writeLifecycleEvent(t, mirrorDir, "axon", 40, handoffID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 41, handoffID, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, "axon", 42, handoffID, "verify-pass", to)
}

// TestContractRetireNamesObservedConsumersAndProceeds is §8 criterion 1 end
// to end, on the exact shape fb-20260820-0cb8c8 reports: beta verify-passed
// two packages pinning XC-axon-observed and registered nowhere, so the
// declared-consumer set is EMPTY and the retire is clean. It must stay
// clean — exit 0, one funnel call, ungated (§8 criterion 2) — and it must
// no longer be silent.
func TestContractRetireNamesObservedConsumersAndProceeds(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "observed", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-observed", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-observed", "deprecate", "axon")

	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-aaaa", "DP-axon-20260818-p3my", "XC-axon-observed@1.0.0#aaa111")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-bbbb", "DP-axon-20260818-n2yp", "XC-axon-observed@1.0.0#bbb222")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-observed"}, io)

	if code != 0 {
		t.Fatalf("code = %d, want 0 — observed consumption never blocks retire (§9); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call (the retire proceeded), got %+v", fake.calls)
	}
	if fake.calls[0].PRBody != "" {
		t.Fatalf("expected an UNGATED retire — observed consumption must not reach the override path: %+v", fake.calls[0])
	}

	stderr := errOut.String()
	for _, want := range []string{
		"0 declared consumer(s), 1 observed and undeclared",
		"beta (2 packages)",
		"never blocks retire",
		"a2a contract adopt",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("retire's output does not contain %q — the producer is still deciding blind, which is the reported defect.\nstderr: %s", want, stderr)
		}
	}
}

// TestContractRetireSaysNothingWithoutObservation is §8 criterion 4 on the
// command itself: the SAME contract with no deliveries against it prints
// not one word about observation. A trigger that fires on a plain space is
// the 2026-08-10 regression, and it is not repeated here.
func TestContractRetireSaysNothingWithoutObservation(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "plain", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-plain", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-plain", "deprecate", "axon")
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-plain"}, io)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "observed") {
		t.Fatalf("a plain retire mentioned observation: %s", errOut.String())
	}
}

// TestContractRetireDropsAnObservedConsumerThatAlreadyAcked is §6's
// "observed system already acked" edge case, answered rather than left
// implicit (spec §11 A5).
//
// Acknowledging IS one of the two exits §9 names — "seen, and I do not
// depend on this". A system that has taken an exit is not an open risk, and
// naming it anyway is precisely the nagging US-5 exists to prevent. So the
// deliveries are still there, the system is still undeclared, and retire
// says nothing about it.
func TestContractRetireDropsAnObservedConsumerThatAlreadyAcked(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "acked", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-acked", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-acked", "deprecate", "axon")

	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260721-ackd", "XC-axon-acked@1.0.0", "2026-12-31")
	writeLifecycleEvent(t, mirrorDir, "axon", 10, "XA-axon-20260721-ackd", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 11, "XA-axon-20260721-ackd", "acknowledge", "beta")

	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies: []\n")
	writeObservedDelivery(t, mirrorDir, "beta", "XH-axon-20260817-aaaa", "DP-axon-20260818-p3my", "XC-axon-acked@1.0.0#aaa111")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-acked"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "observed and undeclared") {
		t.Fatalf("beta acknowledged the deprecation and was still named as an observed risk: %s", errOut.String())
	}
}
