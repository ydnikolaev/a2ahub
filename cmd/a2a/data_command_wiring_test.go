package main

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestDataTargetArgsSelectsOnlyRoutingTarget is dataTargetArgs' own parity
// test with contract_p6_wiring_test.go's TestContractTargetArgsSelectsOnlyXCArtifact
// — one table per data sub-verb's own routing source (data_command_wiring.go's
// own doc comment: pack routes on --contract, deliver on --fulfills, fetch/
// verify on their bare positional package id).
//
// The "verify with --record" case is a regression test for a real bug this
// wave's change #2 (Pass -> Record rename) surfaced: cmd_data.go's own
// `data verify` flag was renamed from --pass to --record, but
// dataTargetFlagGrammar's own boolean-flag table for "verify" still listed
// "pass" — so `a2a data verify <id> --record` hit the unrecognized-flag
// `default: return nil` branch in dataTargetArgs' parsing loop, and routing
// silently fell back to resolveLifecycleDepsWithPolicy's own default-space
// selection instead of the id's own space. In a multi-space project that is
// the WRONG mirror, silently. Seeded-red receipt: revert
// dataTargetFlagGrammar's "verify" case back to booleans("pass", "json") and
// this case fails (dataTargetArgs returns nil instead of the package id).
func TestDataTargetArgsSelectsOnlyRoutingTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "pack routes on --contract", args: []string{"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders", "--profile", "synthetic", "--format", "json"}, want: []string{"XC-axon-orders"}},
		{name: "pack --from value cannot masquerade", args: []string{"pack", "--from", "XC-beta-wrong", "--contract", "XC-axon-orders@1.0.0", "--profile", "synthetic", "--format", "json"}, want: []string{"XC-axon-orders"}},
		{name: "deliver routes on --fulfills", args: []string{"deliver", "staging/attempt-1", "--fulfills", "XW-axon-20260804-ef56"}, want: []string{"XW-axon-20260804-ef56"}},
		{name: "fetch routes on the positional package id", args: []string{"fetch", "DP-axon-20260804-ab12", "--to", "vendor/orders"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify routes on the positional package id", args: []string{"verify", "DP-axon-20260804-ab12"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify with --record still routes on the positional package id", args: []string{"verify", "DP-axon-20260804-ab12", "--record"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify with --record --json still routes", args: []string{"verify", "DP-axon-20260804-ab12", "--record", "--json"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "pack subcommand only", args: []string{"pack"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := dataTargetArgs(test.args); !slices.Equal(got, test.want) {
				t.Fatalf("dataTargetArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

// masterDefaultDataOrigin builds a bare origin repo whose default branch is
// "master" — never "main" — plus one clone of it. Group B's own acceptance
// (no-silent-yes-2026-08) needs a space whose real default branch genuinely
// differs from "main" to prove dataCore's own SubmitTemplate.BaseBranch
// literal is gone: this origin publishes NO "main" branch at all, so the
// OLD hardcoded literal would not merely target the wrong branch quietly —
// commitOne's own checkout of origin/main would fail outright, since there
// is no such ref. Deliberately minimal (no space.yaml, no §4.2 tree):
// deliver/deliverBlob/verify read nothing else from the mirror's working
// tree that this test's own dataCore construction doesn't already stub.
func masterDefaultDataOrigin(t *testing.T) (originDir, cloneDir string) {
	t.Helper()
	root := t.TempDir()
	originDir = filepath.Join(root, "origin.git")
	runGitFixture(t, "", "init", "-q", "--bare", "-b", "master", originDir)
	gitfixture.HardenRepo(t, originDir)

	seed := filepath.Join(root, "seed")
	runGitFixture(t, "", "init", "-q", "-b", "master", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, seed, "add", "-A")
	runGitFixture(t, seed, "commit", "-q", "-m", "seed: master-default fixture")
	runGitFixture(t, seed, "remote", "add", "origin", originDir)
	runGitFixture(t, seed, "push", "-q", "origin", "master")

	cloneDir = filepath.Join(root, "clone")
	runGitFixture(t, "", "clone", "-q", originDir, cloneDir)
	return originDir, cloneDir
}

// seedDataWiringWant writes a minimal committed "want" (XW) envelope at
// <mirrorDir>/<system>/exchanges/<id>.md — deliver's own c.loadEnvelope(
// req.Fulfills) precondition (data_wiring.go), mirroring
// seedDataWiringHandoff's own convention (data_wiring_test.go) for the
// handoff type one layer up. Uncommitted in the working tree is enough:
// loadEnvelope reads the mirror's working tree directly, before the funnel
// ever touches git (seedDataWiringHandoff's own precedent).
func seedDataWiringWant(t *testing.T, mirrorDir, system, id string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, system, "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: want\n" +
		"title: test want\n" +
		"space: test\n" +
		"from: " + system + "\n" +
		"to: [peer]\n" +
		"actor: {kind: agent, name: tester}\n" +
		"created: 2026-08-04T12:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"thread: thread:" + system + "-20260804-ab12\n" +
		"acceptance_criteria: [\"delivered\"]\n" +
		"limitations: []\n" +
		"---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDataCoreDeliverTargetsSpaceDefaultBranchNotMain is Group B's own
// acceptance (no-silent-yes-2026-08): space.DeliverDataPackage's
// SubmitTemplate used to carry a hardcoded BaseBranch: "main"
// (data_wiring.go), and DeliverDataPackage itself copies
// req.SubmitTemplate straight through to the funnel with no BaseBranch
// validation of its own (internal/space/data_delivery.go) — so a space
// whose real default branch is "master" got its data-package delivery
// pushed at "main" silently. dataCore.deliver is `a2a data deliver`'s own
// production core, driven here through a REAL fetched mirror and a fake
// (offline, in-memory) GitHub host, never real GitHub.
func TestDataCoreDeliverTargetsSpaceDefaultBranchNotMain(t *testing.T) {
	t.Parallel()

	originDir, cloneDir := masterDefaultDataOrigin(t)
	fulfillsID := "XW-axon-20260804-ab12"
	seedDataWiringWant(t, cloneDir, "axon", fulfillsID)

	fake := host.NewFakeHost()
	schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
	core := &dataCore{
		ownSystem: "peer", mirrorDir: cloneDir, stagingDir: t.TempDir(),
		remoteURL: originDir, repository: host.Repo{Owner: "acme", Name: "getvisa"},
		authorName: "a2a-peer", authorEmail: "a2a-peer@a2ahub.invalid",
		now:     func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) },
		entropy: rand.Reader,
		resolveActor: func(kind, name, model string) (template.Actor, error) {
			return template.Actor{Kind: "agent", Name: "tester"}, nil
		},
		resolveCredential: func(context.Context) (host.Credential, error) { return host.Credential{}, nil },
		loadManifest: func() (space.Manifest, error) {
			return space.Manifest{Participants: []space.Participant{
				{System: "axon"}, {System: "peer"},
			}}, nil
		},
		resolveContractSchemas: func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@1.0.0#sha256:" + strings.Repeat("a", 64), nil
		},
	}
	core.funnel = space.NewWriteFunnel(fake, dataNoopSubmitValidator{}, "0.1.0")

	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packResult, err := core.pack(context.Background(), cli.DataPackRequest{
		ContractRef: "XC-axon-widget@1.0.0", From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
		Fulfills: fulfillsID,
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	stagingRoot := filepath.Join(core.stagingDir, "data", packResult.Manifest.ID)

	if _, err := core.deliver(context.Background(), cli.DataDeliverRequest{StagingRoot: stagingRoot, Fulfills: fulfillsID}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(fake.Opens) != 1 {
		t.Fatalf("deliver did not open exactly one PR: opens=%d", len(fake.Opens))
	}
	if fake.Opens[0].Base != "master" {
		t.Fatalf("PR base = %q, want %q (the space's own resolved default branch, never a hardcoded \"main\")", fake.Opens[0].Base, "master")
	}
}
