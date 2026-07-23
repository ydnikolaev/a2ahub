// contract_cov_test.go extends P26 wave 14c's Contract ops family (spec 26
// §2): the sub-verb paths TestT3ContractNewPublishDeprecate/
// TestT3ContractRetireCleanUngated/TestT3ContractDiff/
// TestT3ContractVerifyExportLocal (contract_write_test.go) don't already
// cover — retire BLOCKED on missing consumer acks + its --override escape
// hatch, `diff --json`, and verify-export's tampered-digest refusal path.
// Same direct-construction idiom (real space.WriteFunnel + host.NewFakeHost
// + spacefixture clone), same helpers (never redeclared — see
// contract_write_test.go/helpers_test.go, this file only ADDS scenarios).
package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestT3ContractRetireBlockedUnackedConsumer is spec 26 §2's "retire
// blocked on missing consumer acks" clause (§5.4/D-022/POL-006, internal/
// cli's own TestContractRetireUnackedNoOverrideBlocked precedent, ported to
// the real funnel + FakeHost): a registered consumer (consumes.yaml) that
// has not acked the linked deprecation announcement blocks retire locally,
// before the funnel is ever called.
func TestT3ContractRetireBlockedUnackedConsumer(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-gated")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-a1a1", "XC-axon-gated@1.0.0", "2099-01-01")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-gated"}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit: beta is a registered consumer that has not acked (AC-202.2)")
	}
	if !strings.Contains(errOut.String(), "POL-006") {
		t.Fatalf("expected the refusal to name POL-006; got %q", errOut.String())
	}
	if len(fakeHost.Opens) != 0 {
		t.Fatalf("expected the write funnel/host NEVER to be reached on a blocked retire; got %d OpenPR call(s)", len(fakeHost.Opens))
	}
}

// TestT3ContractRetireOverrideUnackedConsumer is spec 26 §2's "then
// --override" clause: sunset passed + a reminder + a human actor +
// --override succeeds despite beta's un-acked consumption, and the retire
// event flags the overridden consumer. Mirrors internal/cli's own
// TestContractRetireOverrideFullPreconditionSucceeds fixed-clock idiom
// EXACTLY (SetClockForTest + sunset one day before the fixed "now") — every
// piece of the full precondition must be present or the override path
// itself refuses for the wrong reason.
func TestT3ContractRetireOverrideUnackedConsumer(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	sunset := fixedNow.AddDate(0, 0, -1).Format("2006-01-02") // one day before the fixed clock: passed

	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "override", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-override", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-override", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-override")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-b1b1", "XC-axon-override@1.0.0", sunset)
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XA-axon-20260101-b1b1", "note", "axon") // >=1 reminder

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("human", "owner"))
	cmd.SetClockForTest(func() time.Time { return fixedNow })

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "--override", "XC-axon-override"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call, got %d", len(fakeHost.Opens))
	}
}

// TestT3ContractDiffJSON is spec 26 §2's `diff --json` clause. It asserts
// the SPEC'd contract — machine-parseable JSON (contractDiffTree's own
// `json:"..."` struct tags are the intended wire format, and `--json` is
// the flag's whole point) — not whatever cmd_contract.go's runDiff happens
// to emit today.
//
// REGRESSION GUARD (P26 wave 14c found the defect; fixed by the lead in the
// same phase): runDiff's --json branch (internal/cli/cmd_contract.go) used to
// encode with `gopkg.in/yaml.v3`'s Encoder, not `encoding/json`, so
// `a2a contract diff --json` emitted YAML block-list syntax that failed
// json.Unmarshal — an isolated one-file defect (every other CLI --json uses
// encoding/json). Fixed to `json.NewEncoder(...).SetIndent(...)`; this test
// asserts the output is valid JSON so the regression can't reappear.
func TestT3ContractDiffJSON(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	writeContractDescriptor(t, mirrorDir, "diffable-json", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable-json/schema/main.schema.json", `{"type":"object"}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.0.0")

	writeContractDescriptor(t, mirrorDir, "diffable-json", "1.1.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable-json/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.1.0")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"diff", "--json", "XC-axon-diffable-json", "1.0.0", "1.1.0"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var decoded struct {
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("diff --json: output is not valid JSON (see this test's DEFECT doc comment): %v\nstdout=%s", err, out.String())
	}
	found := false
	for _, p := range decoded.Changed {
		if p == "schema/main.schema.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected schema/main.schema.json under `changed`, got: %+v", decoded)
	}
	if len(fakeHost.Opens) != 0 {
		t.Fatalf("diff is read-only; expected NO funnel/host call, got %d OpenPR calls", len(fakeHost.Opens))
	}
}

// TestT3ContractVerifyExportTamperedDigest is spec 26 §2's "tampered-digest
// red" clause: a local export whose content has drifted from the recorded
// generated_from.source_digest exits non-zero with a digest-mismatch
// diagnostic (the write side of TestT3ContractVerifyExportLocal in
// contract_write_test.go, which only covers the matching/green case).
func TestT3ContractVerifyExportTamperedDigest(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "tampered", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/tampered/schema/main.schema.json", `{"type":"object"}`)

	// Compute the digest over the space's ORIGINAL content and record it,
	// then write a DIFFERENT ("tampered") local export — the digest
	// recorded in generated_from no longer matches the local bytes.
	digest := contractComputeDigest(t, mirrorDir, "axon/provides/tampered")
	appendGeneratedFromDigest(t, mirrorDir, "tampered", digest)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object","x":"TAMPERED"}`)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-export", "--local", localPath, "XC-axon-tampered"}, io)
	if code == 0 {
		t.Fatalf("expected a non-zero exit (tampered/drifted local export); stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "digest mismatch") {
		t.Fatalf("expected a digest-mismatch diagnostic, got %q", errOut.String())
	}
	if len(fakeHost.Opens) != 0 {
		t.Fatalf("verify-export is read-only; expected NO funnel/host call, got %d OpenPR calls", len(fakeHost.Opens))
	}
}
