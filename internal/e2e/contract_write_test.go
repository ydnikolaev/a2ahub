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
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestContractNewPublishDeprecateDirectConstruction is AC-1's
// contract-lifecycle half (OP-212): `contract new` (delegates to the real P6
// NewCommand), then `contract publish` (first-ever publish, G1-gated), then
// `contract deprecate` — all against a REAL space.WriteFunnel +
// host.NewFakeHost + spacefixture clone, each step's commit legible to the
// next (no fake-funnel materialization step). Direct-construction,
// in-process; never the exec'd binary tier (see txtar_test.go's own doc
// comment for why the write path is covered here rather than by a txtar
// script).
func TestContractNewPublishDeprecateDirectConstruction(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	stagingDir := t.TempDir()

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := e2eHostConfig("axon", fx.RemoteURL())

	newCmd := cli.NewNewCommand(stagingDir, "axon", e2eActorResolver("agent", "bot"), nil)
	cmd := cli.NewContractCommand(newCmd, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))
	configureE2EContractP6(cmd, newE2EContractPublisher(t, funnel, mirrorDir, "axon", hostCfg))

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
	writeContractSchemaFixture(t, mirrorDir, "axon", "widget")

	io2, out2, errOut2 := newIO()
	if code := cmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-axon-widget"}, io2); code != 0 {
		t.Fatalf("contract publish: code = %d, want 0; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call after publish, got %d", len(fakeHost.Opens))
	}

	// Land the publish before deprecating it, because that is what the
	// product requires and this test used to skip. `deprecate` reads the
	// COMMITTED descriptor out of the mirror; the descriptor reaches the
	// mirror's base branch only when the publish PR merges. This test
	// passed without the merge purely because the funnel used to leave the
	// mirror parked on the publish branch — and even then only because the
	// test drives commands directly: a real `a2a contract deprecate` starts
	// with CloneOrFetch, whose `reset --hard origin/<base>` discarded that
	// leftover before the descriptor could be read. So the old green was an
	// artifact of the harness, never a behaviour a user had.
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

	io3, out3, errOut3 := newIO()
	if code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-widget2@1.0.0", "--sunset", "2099-01-01", "XC-axon-widget"}, io3); code != 0 {
		t.Fatalf("contract deprecate: code = %d, want 0; stdout=%s stderr=%s", code, out3.String(), errOut3.String())
	}
	if len(fakeHost.Opens) != 2 {
		t.Fatalf("expected exactly two OpenPR calls after deprecate, got %d", len(fakeHost.Opens))
	}
}

// TestContractRetireCleanUngatedDirectConstruction is OP-212's retire verb,
// general path (no registered consumers -> succeeds ungated), real funnel +
// FakeHost, direct-construction and in-process — never the exec'd binary
// tier.
func TestContractRetireCleanUngatedDirectConstruction(t *testing.T) {
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

// TestContractDiffDirectConstruction is OP-221's contract-diff transport
// clause: the required P6 inspection service reports a changed schema path
// and the CLI renders it. Exact Git-history selection and tree comparison
// belong to the P6 space integration suite; this older direct-construction
// test owns CLI composition. In-process, never the exec'd binary tier.
func TestContractDiffDirectConstruction(t *testing.T) {
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
	configureE2EContractP6(cmd, newE2EContractDiffService())

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

// TestContractVerifyExportLocalDirectConstruction is OP-213's transport
// result: the injected P6 inspection service compares the local export with
// the recorded expected digest and the CLI exits 0 for a match. Production
// repository lookup and immutable-tree reconstruction are covered by the P6
// space integration suite. In-process, never the exec'd binary tier.
func TestContractVerifyExportLocalDirectConstruction(t *testing.T) {
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
	configureE2EContractP6(cmd, newE2EContractVerifyService(t, digest))

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

// TestContractAdoptDirectConstruction is the D-022 consumer-registry round
// trip (`a2a contract adopt`): a REAL funnel + REAL V2 validation writing
// <own-system>/consumes.yaml. The V2 pass matters here — a consumes.yaml
// carries no envelope, so the submit validator has to route it to the
// consumes/v1 schema check instead of demanding frontmatter. In-process,
// never the exec'd binary tier.
func TestContractAdoptDirectConstruction(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")

	// beta publishes a contract; axon adopts it.
	writeContractDescriptorFor(t, mirrorDir, "beta", "content-feed", "2.0.0")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", e2eManifest())
	resolver := cli.NewMirrorResolver(mirrorDir, e2eManifest())
	submitValidator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, submitValidator, "0.1.0")
	cmd := cli.NewContractCommand(nil, funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 0 {
		t.Fatalf("contract adopt: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one PushBranch + OpenPR, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}

	// The committed file is on the funnel's branch — merge it the way the
	// other write tests do, then read it back off disk.
	mergeBranchToMain(t, mirrorDir, "a2a/axon/contract-adopt/XC-beta-content-feed")
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "axon", "consumes.yaml"))
	if err != nil {
		t.Fatalf("read the committed registry: %v", err)
	}
	registry, err := space.ParseConsumes(raw)
	if err != nil {
		t.Fatalf("ParseConsumes: %v", err)
	}
	if len(registry.Dependencies) != 1 || registry.Dependencies[0].Contract != "XC-beta-content-feed" || registry.Dependencies[0].Major != 2 {
		t.Fatalf("committed registry = %+v, want XC-beta-content-feed pinned at major 2", registry)
	}

	// Re-running against the now-committed registry is a no-op: no second PR.
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io2); code != 0 {
		t.Fatalf("re-adopt: code = %d, want 0; stdout=%s", code, out2.String())
	}
	if !strings.Contains(out2.String(), "already registered") {
		t.Fatalf("expected the idempotent message, got %q", out2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one OpenPR after the re-run, got %d", len(fakeHost.Opens))
	}
}
