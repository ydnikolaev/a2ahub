package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// writeContractDescriptor seeds axon's XC-axon-<slug> contract.md at
// version (contract.schema.json's required fields).
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

// TestContractPublishGatePosture is P8-2: first-ever publish (G1), a
// declared-major bump (G2), and a declared-minor bump (ungated) — same
// funnel call shape, only the PRBody advisory marker differs.
func TestContractPublishGatePosture(t *testing.T) {
	t.Parallel()

	t.Run("first_publish_is_G1_gated", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "widget-a", "0.0.0")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-axon-widget-a"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 || fake.calls[0].PRBody == "" {
			t.Fatalf("expected first publish to be gated (advisory marker), got %+v", fake.calls)
		}
	})

	t.Run("declared_major_bump_is_G2_gated", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "widget-b", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-widget-b", "publish", "axon")
		// contractPublishedVersions reads the `version` field off the raw
		// event YAML; writeLifecycleEvent doesn't set one, so append it
		// directly onto the just-written event file.
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"publish", "--bump", "major", "XC-axon-widget-b"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 || fake.calls[0].PRBody == "" {
			t.Fatalf("expected a declared-major bump to be gated (advisory marker), got %+v", fake.calls)
		}
	})

	t.Run("declared_minor_bump_is_ungated", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "widget-c", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-widget-c", "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"publish", "--bump", "minor", "XC-axon-widget-c"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
			t.Fatalf("expected a declared-minor bump to be UNGATED (no marker), got %+v", fake.calls)
		}
	})
}

// appendVersionToLatestEvent appends a `version:` line to the most
// recently written event file under mirrorDir/system/events/**/*.yaml —
// writeLifecycleEvent's own minimal content has no version field, and
// contract publish's G1/G2 detection reads prior publish events' own
// `version` field back off disk.
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

func writeConsumesYAML(t *testing.T, mirrorDir, system, contractID string) {
	t.Helper()
	content := "schema: consumes/v1\nsystem: " + system + "\ndependencies:\n  - contract: " + contractID + "\n    major: 1\n    since: \"2026-01-01\"\n"
	writeMirrorFile(t, mirrorDir, system+"/consumes.yaml", content)
}

func writeDeprecationAnnouncement(t *testing.T, mirrorDir, id, deprecates, sunset string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: announcement\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: deprecation\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"ack_requested: true\n" +
		"deprecates: " + deprecates + "\n" +
		"valid_until: " + sunset + "\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// TestContractRetireCleanAckSucceedsUngated is AC-302.1's retire general
// path: no registered consumers at all -> succeeds ungated.
func TestContractRetireCleanAckSucceedsUngated(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "clean", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-clean", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-clean", "deprecate", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-clean"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an ungated retire (no registered consumers), got %+v", fake.calls)
	}
}

// TestContractRetireUnackedNoOverrideBlocked is AC-202.2: an un-acked
// registered consumer (consumes.yaml entry) blocks retire locally.
func TestContractRetireUnackedNoOverrideBlocked(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-gated")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-a1a1", "XC-axon-gated@1.0.0", "2099-01-01")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-gated"}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit (un-acked registered consumer, AC-202.2)")
	}
	if !strings.Contains(errOut.String(), "POL-006") {
		t.Fatalf("expected the refusal to name POL-006; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestContractRetireOverrideFullPreconditionSucceeds is AC-202.3's second
// clause: sunset passed + a reminder + a human actor + --override
// succeeds, flags the overridden consumer. LOW fix-wave finding: the
// sunset-passed comparison now runs against a FIXED injected clock
// (cmd.SetClockForTest), never contractSunsetPassed's own former direct
// time.Now().UTC() read — the sunset date below is deliberately one day
// BEFORE that fixed clock, not a hardcoded calendar date compared against
// real wall-clock time (which would eventually go stale/flip).
func TestContractRetireOverrideFullPreconditionSucceeds(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	sunset := fixedNow.AddDate(0, 0, -1).Format("2006-01-02") // one day before the fixed clock: passed

	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "override", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-override", "publish", "axon")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-override", "deprecate", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-override")
	writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-b1b1", "XC-axon-override@1.0.0", sunset)
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XA-axon-20260101-b1b1", "note", "axon") // >=1 reminder

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("human", "owner"))
	cmd.SetClockForTest(func() time.Time { return fixedNow })
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "--override", "XC-axon-override"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if fake.calls[0].PRBody == "" {
		t.Fatal("expected the override path to carry an advisory gate marker")
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "retired-unacked") {
		t.Fatalf("expected the retire event to flag the overridden consumer, got:\n%s", fake.calls[0].Files[0].Content)
	}
}

// gitRun runs `git <args...>` with cwd=dir, failing the test loudly.
func gitRun(t *testing.T, dir string, args ...string) {
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

// TestContractDiffTwoVersions is P8-4: a two-version fixture contract with
// a schema field added between v1 and v2 -> `contract diff` reports it
// under `changed`.
func TestContractDiffTwoVersions(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitRun(t, mirrorDir, "init", "-b", "main")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.0.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object"}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/fixtures/valid/ok.json", `{}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.0.0")

	writeContractDescriptor(t, mirrorDir, "diffable", "1.1.0")
	writeMirrorFile(t, mirrorDir, "axon/provides/diffable/schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)
	gitRun(t, mirrorDir, "add", "-A")
	gitRun(t, mirrorDir, "commit", "-m", "publish 1.1.0")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"diff", "XC-axon-diffable", "1.0.0", "1.1.0"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "changed schema/main.schema.json") {
		t.Fatalf("expected schema/main.schema.json under `changed`, got:\n%s", out.String())
	}
}

// TestContractVerifyExportLocal is AC-1001.1: a matching local export
// exits 0; a deliberately-drifted one exits non-zero with a diagnostic.
func TestContractVerifyExportLocal(t *testing.T) {
	t.Parallel()

	t.Run("matching_export_exits_zero", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "exportable", "1.0.0")
		writeMirrorFile(t, mirrorDir, "axon/provides/exportable/schema/main.schema.json", `{"type":"object"}`)

		localPath := t.TempDir()
		writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object"}`)

		// Record the digest as generated_from.source_digest on the
		// descriptor (verify-export's own-version-unspecified path reads
		// this field back).
		digest := contractComputeDigestForTest(t, mirrorDir, "axon/provides/exportable")
		writeContractDescriptorWithGeneratedFrom(t, mirrorDir, "exportable", "1.0.0", digest)

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"verify-export", "--local", localPath, "XC-axon-exportable"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
	})

	t.Run("drifted_export_exits_nonzero", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "drifted", "1.0.0")
		writeMirrorFile(t, mirrorDir, "axon/provides/drifted/schema/main.schema.json", `{"type":"object"}`)
		digest := contractComputeDigestForTest(t, mirrorDir, "axon/provides/drifted")
		writeContractDescriptorWithGeneratedFrom(t, mirrorDir, "drifted", "1.0.0", digest)

		localPath := t.TempDir()
		writeMirrorFile(t, localPath, "schema/main.schema.json", `{"type":"object","x":"DRIFTED"}`)

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"verify-export", "--local", localPath, "XC-axon-drifted"}, io)
		if code == 0 {
			t.Fatal("expected a non-zero exit (drifted export)")
		}
		if !strings.Contains(errOut.String(), "digest mismatch") {
			t.Fatalf("expected a digest-mismatch diagnostic, got %q", errOut.String())
		}
	})
}

// contractComputeDigestForTest is this test file's own copy of §5.7's
// exact multi-file digest algorithm ("SHA-256 over the sorted list of
// (repo-relative-path, sha256(file-bytes)) pairs covering schema/** and
// fixtures/**") — used only to independently derive the expected digest
// a fixture's generated_from.source_digest should carry, never to
// validate cmd_contract.go's OWN computation against itself.
func contractComputeDigestForTest(t *testing.T, mirrorDir, contractRelDir string) string {
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
				t.Fatalf("contractComputeDigestForTest: %v", err)
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

// writeContractDescriptorWithGeneratedFrom is writeContractDescriptor plus
// a `generated_from` block (§5.3) — verify-export's own-version-
// unspecified path reads generated_from.source_digest back.
func writeContractDescriptorWithGeneratedFrom(t *testing.T, mirrorDir, slug, version, digest string) {
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
		"generated_from: {tool: \"codegen\", source_digest: \"" + digest + "\"}\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
}

// TestContractDeprecateRealTemplateRender is an AC-302.1 transition test
// that runs `contract deprecate` for real (never hand-writing the
// deprecation announcement, unlike the retire tests above): it is the
// ONLY producer of the announcement retire's own tests otherwise seed by
// hand, and the one place template.Render's announcement path (whose
// canonical template carries ack_requested/deprecates/valid_until only as
// COMMENTED-OUT example lines, not real keys) is actually exercised.
func TestContractDeprecateRealTemplateRender(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "depme", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-depme", "publish", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-depme@2.0.0", "--sunset", "2026-12-31", "XC-axon-depme"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call (deprecate event + announcement in one PR), got %d", len(fake.calls))
	}
	files := fake.calls[0].Files
	if len(files) != 3 {
		t.Fatalf("expected 3 files (deprecate event, announcement artifact, announcement publish event), got %d: %+v", len(files), files)
	}
	var sawAnnouncement bool
	for _, fw := range files {
		if strings.Contains(fw.Path, "/exchanges/XA-") {
			sawAnnouncement = true
			content := string(fw.Content)
			if !strings.Contains(content, "deprecates: XC-axon-depme@1.0.0") {
				t.Fatalf("expected the announcement to carry `deprecates` (a template.Render-added field, not a commented-out placeholder), got:\n%s", content)
			}
			if !strings.Contains(content, "valid_until:") || !strings.Contains(content, "2026-12-31") {
				t.Fatalf("expected the announcement to carry `valid_until: 2026-12-31`, got:\n%s", content)
			}
			if !strings.Contains(content, "ack_requested: true") {
				t.Fatalf("expected the announcement to carry `ack_requested: true`, got:\n%s", content)
			}
		}
	}
	if !sawAnnouncement {
		t.Fatalf("expected an announcement artifact among the committed files, got %+v", files)
	}
}

// TestContractPublishIdempotentRerun is the Constraints block's "idempotent
// re-run test per mutating verb": for `publish`, idempotency is entirely
// funnel-provided (the deterministic-branch short-circuit,
// space.WriteStateAlreadyOpen) — publish's own ArtifactID is just the
// contract id + explicit --version/--bump, both caller-supplied and
// already stable across retries, so this proves ContractCommand.runPublish
// wires the shared funnel contract correctly. This is NOT true of every
// verb, though: `respond` and `contract deprecate` each mint a SECOND,
// SELF-GENERATED id (responseID / announcementID) that also feeds the
// funnel's branch key — those two verbs carry their OWN verb-specific
// deterministic-seed logic (lifecycleRespondSeed / contractDeprecateSeed,
// HIGH-1 fix-wave finding) precisely because a naively-random secondary id
// would defeat the funnel's dedup on retry even though the funnel itself
// behaves identically. See TestRespondDeterministicResponseID/
// TestRespondIdempotentRetryReturnsAlreadyOpen and
// TestContractDeprecateDeterministicAnnouncementID for that coverage.
func TestContractPublishIdempotentRerun(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "again", "0.0.0")

	fake := &fakeLifecycleFunnel{result: space.WriteResult{State: space.WriteStateAlreadyOpen, PRURL: "https://example.invalid/pr/1"}}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-axon-again"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (already-open re-run is a success no-op); stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "already submitted") {
		t.Fatalf("expected the already-submitted idempotent message, got stdout=%q", out.String())
	}
}

// TestContractNewDelegatesToNewCommand is the Placement decision (binding):
// `contract new <slug>` translates the positional slug into P6's own
// `a2a new contract --slug <slug>` path — this test drives a REAL
// *cli.NewCommand (never nil), proving the delegation actually produces a
// staged contract draft, not just that the arg-munging looks right on
// read.
func TestContractNewDelegatesToNewCommand(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	newCmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver)
	cmd := cli.NewContractCommand(newCmd, &fakeLifecycleFunnel{}, t.TempDir(), "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"new", "delegated-widget"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	stagedPath := filepath.Join(stagingDir, "XC-axon-delegated-widget.md")
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("expected a staged draft at %s (slug -> --slug delegation into P6's new-path): %v", stagedPath, err)
	}
}

// extractAnnouncementID pulls the minted XA- announcement id out of a
// funnel call's committed files (`contract deprecate`'s own analogue of
// cmd_lifecycle_test.go's extractResponseID).
func extractAnnouncementID(files []space.FileWrite) string {
	for _, fw := range files {
		if strings.Contains(fw.Path, "/exchanges/XA-") {
			return strings.TrimSuffix(filepath.Base(fw.Path), ".md")
		}
	}
	return ""
}

// TestContractDeprecateDeterministicAnnouncementID is HIGH-1's own
// discriminating test for `contract deprecate` (AC-301.1, anti-pattern
// #4): with a FIXED injected clock, two deprecate invocations with
// IDENTICAL (contract id, version, sunset) content mint the IDENTICAL
// announcementID (a retry lands on the SAME funnel branch); two
// invocations that differ only in --sunset mint DISTINCT ids.
func TestContractDeprecateDeterministicAnnouncementID(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	runDeprecate := func(t *testing.T, slug, sunset string) *fakeLifecycleFunnel {
		t.Helper()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, slug, "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-"+slug, "publish", "axon")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(func() time.Time { return fixedNow })
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-" + slug + "@2.0.0", "--sunset", sunset, "XC-axon-" + slug}, io)
		if code != 0 {
			t.Fatalf("contract deprecate: code = %d, want 0; stderr=%s", code, errOut.String())
		}
		return fake
	}

	t.Run("same_contract_retry_mints_identical_id", func(t *testing.T) {
		t.Parallel()
		fake1 := runDeprecate(t, "dep-retry", "2026-12-31")
		id1 := extractAnnouncementID(fake1.calls[0].Files)
		if id1 == "" {
			t.Fatal("expected a minted announcement id")
		}

		// A second, independent mirror for the SAME contract id/version/
		// sunset (simulating a retry against a fresh clone) mints the
		// IDENTICAL announcement id.
		mirrorDir2 := t.TempDir()
		writeContractDescriptor(t, mirrorDir2, "dep-retry", "1.0.0")
		writeLifecycleEvent(t, mirrorDir2, "axon", 0, "XC-axon-dep-retry", "publish", "axon")
		fake2 := &fakeLifecycleFunnel{}
		cmd2 := cli.NewContractCommand(nil, fake2, mirrorDir2, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd2.SetClockForTest(func() time.Time { return fixedNow })
		io2, _, errOut2 := newIO()
		code := cmd2.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-dep-retry@2.0.0", "--sunset", "2026-12-31", "XC-axon-dep-retry"}, io2)
		if code != 0 {
			t.Fatalf("contract deprecate (retry): code = %d, want 0; stderr=%s", code, errOut2.String())
		}
		id2 := extractAnnouncementID(fake2.calls[0].Files)
		if id2 == "" {
			t.Fatal("expected a minted announcement id on the retry")
		}
		if id1 != id2 {
			t.Fatalf("announcementID = %q vs %q; expected the SAME id for an identical (id, version, sunset) retry under a fixed clock", id1, id2)
		}
	})

	t.Run("different_sunset_mints_different_id", func(t *testing.T) {
		t.Parallel()
		fake1 := runDeprecate(t, "dep-diff", "2026-12-31")
		mirrorDir2 := t.TempDir()
		writeContractDescriptor(t, mirrorDir2, "dep-diff", "1.0.0")
		writeLifecycleEvent(t, mirrorDir2, "axon", 0, "XC-axon-dep-diff", "publish", "axon")
		fake2 := &fakeLifecycleFunnel{}
		cmd2 := cli.NewContractCommand(nil, fake2, mirrorDir2, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd2.SetClockForTest(func() time.Time { return fixedNow })
		io2, _, errOut2 := newIO()
		code := cmd2.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-dep-diff@2.0.0", "--sunset", "2027-06-30", "XC-axon-dep-diff"}, io2)
		if code != 0 {
			t.Fatalf("contract deprecate (different sunset): code = %d, want 0; stderr=%s", code, errOut2.String())
		}

		id1 := extractAnnouncementID(fake1.calls[0].Files)
		id2 := extractAnnouncementID(fake2.calls[0].Files)
		if id1 == "" || id2 == "" {
			t.Fatalf("expected a minted announcement id in both calls; got %q and %q", id1, id2)
		}
		if id1 == id2 {
			t.Fatalf("expected DIFFERENT ids for different --sunset values, got the same id %q", id1)
		}
	})
}
