package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

func writeContractDescriptor(t *testing.T, mirrorDir, slug, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
}

func contractTestDeps(mirrorDir string, funnel Funnel) ContractDeps {
	write := testWriteDeps(mirrorDir, funnel)
	write.OwnSystem = "axon"
	return ContractDeps{WriteDeps: write}
}

func TestContractPublishFirstPublishGated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-a", "0.0.0")

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-a", Version: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody == "" {
		t.Fatalf("expected first publish to be gated (advisory marker), got %+v", fake.calls)
	}
}

func TestContractPublishMinorBumpUngated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "widget-c", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-widget-c", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeFunnel{}
	handler := newContractPublishHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractPublishInput{ID: "XC-axon-widget-c", Bump: "minor"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected a declared-minor bump to be UNGATED, got %+v", fake.calls)
	}
}

func appendVersionToLatestEvent(t *testing.T, mirrorDir, system, version string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mirrorDir, system, "events", "*", "*.yaml"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("appendVersionToLatestEvent: no event files found: %v", err)
	}
	path := matches[len(matches)-1]
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("appendVersionToLatestEvent: %v", err)
	}
	raw = append(raw, []byte("version: \""+version+"\"\n")...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("appendVersionToLatestEvent: %v", err)
	}
}

func TestContractDeprecateAuthorsAnnouncement(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "dep-a", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-dep-a", "publish", "axon")

	fake := &fakeFunnel{}
	handler := newContractDeprecateHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDeprecateInput{ID: "XC-axon-dep-a", Successor: "XC-axon-dep-b@1.0.0", Sunset: "2099-01-01"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("deprecate failed: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 3 {
		t.Fatalf("expected 3 files (deprecate event + announcement draft + its publish event), got %+v", fake.calls)
	}
}

func TestContractRetireCleanAckSucceedsUngated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "clean", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-clean", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-clean", "deprecate", "axon")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-clean"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an ungated retire (no registered consumers), got %+v", fake.calls)
	}
}

func TestContractRetireUnackedBlocked(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", "schema: consumes/v1\nsystem: beta\ndependencies:\n  - contract: XC-axon-gated\n    major: 1\n    since: \"2026-01-01\"\n")
	writeMirrorFile(t, mirrorDir, "axon/exchanges/XA-axon-20260101-a1a1.md",
		"---\nschema: envelope/v1\nid: XA-axon-20260101-a1a1\ntype: announcement\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: deprecation\npriority: p2\nblocking: false\nack_requested: true\ndeprecates: XC-axon-gated@1.0.0\nvalid_until: 2099-01-01\nclassification: internal\n---\nbody\n")

	fake := &fakeFunnel{}
	handler := newContractRetireHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractRetireInput{ID: "XC-axon-gated"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal (un-acked registered consumer, POL-006)")
	}
	if !strings.Contains(err.Error(), "POL-006") {
		t.Fatalf("expected POL-006 in the refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fake.calls))
	}
}

func gitRunTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

func TestContractDiffTwoVersions(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitRunTest(t, mirrorDir, "init", "-b", "main")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object"}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/fixtures/valid/ok.json", `{}`)
	gitRunTest(t, mirrorDir, "add", "-A")
	gitRunTest(t, mirrorDir, "commit", "-m", "publish 1.0.0")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.1.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	gitRunTest(t, mirrorDir, "add", "-A")
	gitRunTest(t, mirrorDir, "commit", "-m", "publish 1.1.0")

	fake := &fakeFunnel{}
	handler := newContractDiffHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractDiffInput{ID: "XC-axon-diffable", V1: "1.0.0", V2: "1.1.0"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	tree := result.(contractDiffTree)
	found := false
	for _, p := range tree.Changed {
		if p == "schema/main.schema.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected schema/main.schema.json under Changed, got %+v", tree)
	}
}

func TestContractVerifyExportMatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "exportable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/exportable/schema/main.schema.json", `{"type":"object"}`)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object"}`)

	digest, _, err := artifact.DigestTreeFS(filepath.Join(mirrorDir, "axon/provides/exportable"), contractDigestSubtrees)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	writeContractDescriptorWithDigest(t, mirrorDir, "exportable", "1.0.0", digest)

	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractVerifyExportInput{Local: localPath, Ref: "XC-axon-exportable"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify-export failed: %v", err)
	}
	if !result.(contractVerifyExportResult).Matches {
		t.Fatalf("expected a match, got %+v", result)
	}
}

func TestContractVerifyExportMismatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "drifted", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/drifted/schema/main.schema.json", `{"type":"object"}`)

	localPath := t.TempDir()
	writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object","extra":true}`)

	writeContractDescriptorWithDigest(t, mirrorDir, "drifted", "1.0.0", "sha256:0000000000000000000000000000000000000000000000000000000000000000")

	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(mirrorDir, fake))
	args, _ := json.Marshal(ContractVerifyExportInput{Local: localPath, Ref: "XC-axon-drifted"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify-export failed: %v", err)
	}
	if result.(contractVerifyExportResult).Matches {
		t.Fatalf("expected a digest mismatch, got %+v", result)
	}
}

func TestContractVerifyExportMissingArgs(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	handler := newContractVerifyExportHandler(contractTestDeps(t.TempDir(), fake))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for missing local/ref")
	}
}

func TestContractDiffSameVersionRefused(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	handler := newContractDiffHandler(contractTestDeps(t.TempDir(), fake))
	args, _ := json.Marshal(ContractDiffInput{ID: "XC-axon-x", V1: "1.0.0", V2: "1.0.0"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error when v1 == v2")
	}
}

func writeContractDescriptorWithDigest(t *testing.T, mirrorDir, slug, version, digest string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		"generated_from: {tool: test, source_digest: \"" + digest + "\"}\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
}

func TestContractNewDelegatesToNewDraft(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newContractNewHandler(testNewDeps(staging))
	args, _ := json.Marshal(ContractNewInput{Slug: "widget"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("contract new failed: %v", err)
	}
	drafts, ok := result.([]newDraftResult)
	if !ok || len(drafts) != 1 || !strings.HasPrefix(drafts[0].ID, "XC-") {
		t.Fatalf("expected 1 drafted XC- contract, got %#v", result)
	}
}

func TestContractNewMissingSlug(t *testing.T) {
	t.Parallel()
	handler := newContractNewHandler(testNewDeps(t.TempDir()))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for a missing slug")
	}
}
