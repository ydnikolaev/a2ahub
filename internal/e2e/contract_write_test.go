package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestT3ContractNewPublishDeprecate is AC-1's contract-lifecycle half
// (OP-212): `contract new` (delegates to the real P6 NewCommand), then
// `contract publish` (first-ever publish, G1-gated), then `contract
// deprecate` — all against a REAL space.WriteFunnel + host.NewFakeHost +
// spacefixture clone, each step's commit legible to the next (no fake-
// funnel materialization step).
func TestT3ContractNewPublishDeprecate(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	stagingDir := t.TempDir()

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := e2eHostConfig("axon", fx.RemoteURL())

	newCmd := cli.NewNewCommand(stagingDir, "axon", e2eActorResolver("agent", "bot"))
	cmd := cli.NewContractCommand(newCmd, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"new", "widget"}, io); code != 0 {
		t.Fatalf("contract new: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	stagedPath := filepath.Join(stagingDir, "XC-axon-widget.md")
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("expected a staged draft at %s: %v", stagedPath, err)
	}
	// contract new stages a draft only (drafts never enter the space,
	// §3.4) — publish operates on a COMMITTED descriptor, so this test
	// seeds one directly (the same precedent internal/cli's own P8/P9
	// tests use) rather than fabricating a submit round-trip this file
	// doesn't otherwise need.
	writeContractDescriptor(t, mirrorDir, "widget", "0.0.0")

	io2, out2, errOut2 := newIO()
	if code := cmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-axon-widget"}, io2); code != 0 {
		t.Fatalf("contract publish: code = %d, want 0; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call after publish, got %d", len(fakeHost.Opens))
	}

	io3, out3, errOut3 := newIO()
	if code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-widget2@1.0.0", "--sunset", "2099-01-01", "XC-axon-widget"}, io3); code != 0 {
		t.Fatalf("contract deprecate: code = %d, want 0; stdout=%s stderr=%s", code, out3.String(), errOut3.String())
	}
	if len(fakeHost.Opens) != 2 {
		t.Fatalf("expected exactly two OpenPR calls after deprecate, got %d", len(fakeHost.Opens))
	}
}

// TestT3ContractRetireCleanUngated is OP-212's retire verb, general path
// (no registered consumers -> succeeds ungated), real funnel + FakeHost.
func TestT3ContractRetireCleanUngated(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "clean", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-clean", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-clean", "deprecate", "axon")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"retire", "XC-axon-clean"}, io); code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call, got %d", len(fakeHost.Opens))
	}
}

// TestT3ContractDiff is OP-221's contract-diff clause: two committed
// descriptor versions, real git history (fx.Clone gives a real repo).
func TestT3ContractDiff(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object"}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.0.0")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.1.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.1.0")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"diff", "XC-axon-diffable", "1.0.0", "1.1.0"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "changed schema/main.schema.json") {
		t.Fatalf("expected schema/main.schema.json under `changed`, got:\n%s", out.String())
	}
	if len(fakeHost.Opens) != 0 {
		t.Fatalf("diff is read-only; expected NO funnel/host call, got %d OpenPR calls", len(fakeHost.Opens))
	}
}

// TestT3ContractVerifyExportLocal is OP-213: a matching local export exits
// 0 (this phase's own copy of internal/cli's TestContractVerifyExportLocal
// digest algorithm, used only to derive the EXPECTED digest — never to
// self-validate cmd_contract.go's own computation).
func TestT3ContractVerifyExportLocal(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	writeContractDescriptor(t, mirrorDir, "exportable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/exportable/schema/main.schema.json", `{"type":"object"}`)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object"}`)

	digest := contractComputeDigest(t, mirrorDir, "axon/provides/exportable")
	appendGeneratedFromDigest(t, mirrorDir, "exportable", digest)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-export", "--local", localPath, "XC-axon-exportable"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func contractComputeDigest(t *testing.T, mirrorDir, contractRelDir string) string {
	t.Helper()
	perFile := map[string]string{}
	root := filepath.Join(mirrorDir, contractRelDir)
	for _, sub := range []string{"schema", "fixtures"} {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("contractComputeDigest: %v", err)
			}
			sum := sha256.Sum256(raw)
			perFile[sub+"/"+e.Name()] = "sha256:" + hex.EncodeToString(sum[:])
		}
	}
	paths := make([]string, 0, len(perFile))
	for p := range perFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write([]byte(perFile[p]))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func appendGeneratedFromDigest(t *testing.T, mirrorDir, slug, digest string) {
	t.Helper()
	path := filepath.Join(mirrorDir, "axon/provides/"+slug+"/contract.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("appendGeneratedFromDigest: read: %v", err)
	}
	content := strings.Replace(string(raw), "---\nbody\n", "generated_from: {tool: test, source_digest: \""+digest+"\"}\n---\nbody\n", 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("appendGeneratedFromDigest: write: %v", err)
	}
}
