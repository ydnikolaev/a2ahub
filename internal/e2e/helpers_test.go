// Package e2e is P10's integration-test package (T3 testscript harness,
// E2E-1/E2E-4, statusline perf wiring, cc-coverage gate). It imports
// internal/{artifact,schema,fold-adjacent,validate,host,space,cache,
// template,cli} as a test-only consumer (ADR-001's core-package import
// grant is a ceiling, not a mandate) and drives cmd/a2a as a BUILT BINARY
// via os/exec — it never imports cmd/a2a (package main is not importable)
// and never imports internal/mcp (parity is P14, blocked_by: [P10]).
//
// This file holds every helper shared by the txtar-driven T3 scripts, the
// direct-construction T3 write-verb tests, and E2E-1/E2E-4: fixture-space
// content seeding (artifact/event file bodies, mirroring the exact shapes
// internal/cli's own P6-P9 tests use), the manifest/actor-resolver/host-
// config builders, and the "simulate a merged PR" git helper the write path
// needs to make a FakeHost-recorded commit observable across systems (see
// e2e1_test.go's doc comment for why this is required).
package e2e

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// newIO builds the injected stdout/stderr capture pair every cli.Command
// test needs (same idiom as internal/cli's own newIO in cmd_init_test.go —
// internal/e2e cannot import that unexported test helper, so it carries its
// own copy).
func newIO() (cli.IO, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return cli.IO{Stdin: bytes.NewReader(nil), Stdout: &out, Stderr: &errOut}, &out, &errOut
}

// e2eManifest is the fixture space's participant manifest: three systems,
// matching the §1.3 cascade's three roles (axon = the getvisa agent, beta =
// SeoMatrix/Misha's agent, gamma = the content-factory/SoT startup).
func e2eManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "beta", Status: "active"},
		{System: "gamma", Status: "active"},
	}}
}

// e2eActorResolver is a fixed-identity resolveActor func (§7.4 seam) — the
// same convenience internal/cli's own lifecycle tests use.
func e2eActorResolver(kind, name string) func(cli.ActorFlags) template.Actor {
	return func(cli.ActorFlags) template.Actor { return template.Actor{Kind: kind, Name: name} }
}

// e2eHostConfig is a SubmitHostConfig for a given acting system, RemoteURL
// filled in by the caller (the fixture's real local origin).
func e2eHostConfig(system, remoteURL string) cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL: remoteURL, Repo: host.Repo{Owner: "fixture", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-" + system, CommitAuthorEmail: "a2a-" + system + "@a2ahub.invalid",
	}
}

// --- content seeding (mirrors internal/cli's own P6-P9 test fixtures) -----

func writeMirrorFile(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeMirrorFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMirrorFile: write %s: %v", full, err)
	}
}

// writeQuestionArtifact seeds a committed `question` exchange under axon's
// own section, from axon to `to`.
func writeQuestionArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// writeRequirementArtifact seeds a committed `requirement` under axon's own
// section, from axon to `to`, with acceptance criteria (satisfy needs one).
func writeRequirementArtifact(t *testing.T, mirrorDir, id, from, to string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: requirement\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: new-capability\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"acceptance_criteria: [\"works\"]\n" +
		"thread: " + id + "\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, from+"/requires/"+id+".md", content)
}

// writeHandoffArtifact seeds a committed `handoff` under axon's own
// section, from axon to `to`.
func writeHandoffArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: handoff\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// writeDecisionArtifact seeds a committed `decision` under axon's own
// section, requiring approvals from every id in approvers.
func writeDecisionArtifact(t *testing.T, mirrorDir, id string, approvers []string) {
	t.Helper()
	quoted := strings.Join(approvers, ", ")
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + quoted + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"required_approvers: [" + quoted + "]\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "decisions/"+id+".md", content)
}

// writeContractDescriptor seeds axon's XC-axon-<slug> contract.md.
func writeContractDescriptor(t *testing.T, mirrorDir, slug, version string) {
	t.Helper()
	writeContractDescriptorFor(t, mirrorDir, "axon", slug, version)
}

// writeContractDescriptorFor is writeContractDescriptor's parameterized
// twin (the E2E-1 cascade needs gamma, not axon, to author its own
// contract) — writes XC-<from>-<slug>/contract.md under from's own
// provides/ section.
func writeContractDescriptorFor(t *testing.T, mirrorDir, from, slug, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
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
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/contract.md", content)
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

// writeLifecycleEvent seeds a pre-existing committed event under
// actingSystem's own section at a fixed 2020-baseline ULID (seq seconds
// apart) — strictly earlier than, and correctly ordered relative to, any
// event a command under test mints at real wall-clock "now" (copied from
// internal/cli/cmd_lifecycle_test.go's own documented convention).
func writeLifecycleEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeLifecycleEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\n",
		id.String(), subject, transition, actorSystem,
	)
	writeMirrorFile(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}

func seedAcceptedQuestion(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	writeQuestionArtifact(t, mirrorDir, id, to)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, to, 1, id, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, to, 2, id, "accept", to)
}

// --- git plumbing (test-only; wraps testkit/spacefixture, never re-derives
// its bare-origin/clone construction — only the "simulate a merged PR" step
// that construction doesn't itself need) ---------------------------------

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

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
	return out.String()
}

// mergeBranchToMain simulates GitHub's own auto-merge (D-002, "auto-merge
// enabled" — space.WriteFunnel commits to an ephemeral branch and calls
// host.PushBranch/host.OpenPR, which a REAL host would push+merge; a
// host.FakeHost records those calls but performs neither): it fast-forward
// merges branch into main IN mirrorDir (the acting system's own clone,
// where WriteFunnel.commitOne already made the branch's commit locally),
// then pushes main to origin so every OTHER system's clone can observe the
// change via a plain `git fetch` (space.CloneOrFetch). This is this
// phase's own PR-merge stand-in (spec 10 §11's "the G2 required-review
// half is exercised via the host adapter's test double, standing in for
// the GitHub-side gate") — the funnel and validator paths that ran before
// this point are fully real; only the "a human/GitHub actually merged the
// PR" step is simulated, and only for the write→cross-clone-read
// observability the fixture space otherwise has no host to perform.
func mergeBranchToMain(t *testing.T, mirrorDir, branch string) {
	t.Helper()
	// Fetch + fast-forward main to origin's latest tip FIRST: another
	// system's clone may have pushed its own merge to origin since this
	// mirrorDir last synced (the cascade interleaves writes across three
	// independent clones of the SAME origin) — merging branch onto a
	// stale local main would push a non-fast-forward main and be rejected.
	gitRun(t, mirrorDir, "fetch", "origin", "main")
	gitRun(t, mirrorDir, "checkout", "main")
	gitRun(t, mirrorDir, "reset", "--hard", "origin/main")
	gitRun(t, mirrorDir, "merge", "--no-ff", "-m", "merge: "+branch, branch)
	gitRun(t, mirrorDir, "push", "origin", "main")
}

// fetchMain brings dir's own clone up to date with origin/main (the
// read-side of mergeBranchToMain, for a DIFFERENT system's mirror clone).
func fetchMain(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "fetch", "origin", "main")
	gitRun(t, dir, "checkout", "main")
	gitRun(t, dir, "reset", "--hard", "origin/main")
}

// properManifestYAML is a LIST-shaped space.yaml participants block —
// testkit/spacefixture's own auto-seeded space.yaml uses a MAP shape
// (`participants:\n  axon-bot: axon\n...`) that does NOT structurally
// decode into space.Manifest.Participants ([]Participant — see
// spacefixture.go's own doc comment and cmd_submit_test.go's
// writeMinimalSpaceYAML precedent). Every place THIS package reads
// space.yaml back off disk via the BUILT BINARY's own space.ParseManifest
// (buildStore/doctor's loadManifest — never internal/e2e's own direct-
// construction tests, which pass a Go-built space.Manifest straight into
// each cli.NewXCommand constructor and never touch space.yaml at all)
// needs this fixed-up manifest instead, or every fold's own participant/
// role authorization check silently flags every event "unauthorized-actor"
// and every state stays stuck at "draft".
func properManifestYAML(spaceID string, systems ...string) string {
	var b strings.Builder
	b.WriteString("schema: manifest/v1\n")
	b.WriteString("space: " + spaceID + "\n")
	b.WriteString("min_binary_version: \"0.0.0\"\n")
	b.WriteString("participants:\n")
	for _, s := range systems {
		b.WriteString("  - system: " + s + "\n")
		b.WriteString("    status: active\n")
	}
	return b.String()
}

// fixOriginManifest overwrites originDir's main-branch space.yaml with
// properManifestYAML (a throwaway clone + commit + push, the same idiom
// seedOriginExtras uses), then fast-forwards every already-existing clone
// dir onto the new main so callers who cloned BEFORE this fix still see
// it. Must run before any exec'd-binary read-surface assertion.
func fixOriginManifest(t *testing.T, originDir, spaceID string, existingClones ...string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "clone", originDir, dir)
	writeMirrorFile(t, dir, "space.yaml", properManifestYAML(spaceID, "axon", "beta", "gamma"))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "fix: list-shaped space.yaml participants")
	gitRun(t, dir, "push", "origin", "main")
	for _, clone := range existingClones {
		fetchMain(t, clone)
	}
}

// seedOriginExtras pushes additional real content onto the shared fixture
// origin's main branch (beyond spacefixture's own minimal §4.2 seed): one
// pre-existing requirement + its submit event (so the T3 read-verb scripts
// — show/thread/inbox/search — have a real artifact to observe, not just
// an empty tree) and a `.github/workflows/a2a-validate.yml` placeholder
// (so `a2a doctor`'s CI-presence check, cmd_doctor.go's own
// doctorCheckCIPresence, has something to find). Runs once per fixture, via
// a throwaway clone + push — never mutates a per-script clone.
func seedOriginExtras(t *testing.T, originDir string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "clone", originDir, dir)
	writeRequirementArtifact(t, dir, "XR-axon-demo", "axon", "beta")
	writeLifecycleEvent(t, dir, "axon", 0, "XR-axon-demo", "publish", "axon")
	writeMirrorFile(t, dir, ".github/workflows/a2a-validate.yml", "name: a2a-validate\non: [pull_request]\njobs: {}\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "seed: e2e read-surface content")
	gitRun(t, dir, "push", "origin", "main")
}
