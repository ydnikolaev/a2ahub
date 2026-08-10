package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"gopkg.in/yaml.v3"
)

// writeContractDescriptor seeds axon's XC-axon-<slug> contract.md at
// version (contract.schema.json's required fields). It carries a
// well-formed `thread:` (spec 46 §T1 R1: every real artifact `a2a new`
// drafts mints one) so `contract deprecate`'s R2 propagation
// (cmd_contract.go) has a real value to inherit onto its announcement.
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
		"thread: thread:axon-20260721-c9c1\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/schema/main.schema.json", `{"type":"object","additionalProperties":true}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/fixtures/valid/ok.json", `{}`)
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/fixtures/invalid/bad.json", `null`)
}

// TestContractPublishGatePosture is P8-2: first-ever publish (G1) and a
// declared-minor bump (ungated) — same funnel call shape, only the PRBody
// advisory marker differs.
//
// The THIRD original member of this trio — "a declared-major bump succeeds
// and is gated (G2)" — is retired here, not merely renamed: P37's F2/D-A
// precondition (a major bump is refused unless a `deprecate` of the prior
// major already exists) is UNSATISFIABLE on the real write path under the
// existing (frozen, off-limits to this phase) `internal/fold/table.go`
// state table. `contractRows()` folds `deprecate` to `StateDeprecated` for
// the WHOLE contract subject id (not scoped by the event's own `version`
// field), and carries no `(StateDeprecated, TPublish)` row — so a real
// TestContractPublishComputedCompatibility is spec 37's own T2/F1 (D-010,
// §5.4b) driven end to end through `contract publish`: AC-970.1 (a
// mislabeled minor is refused, naming the fixture), a genuinely additive
// minor publishes, AC-970.3 (a major bump is not compat-checked and says
// so), D-D/POL-009 (a fixture-less JSON-Schema contract is refused), and
// D-D's own scope limit (a non-JSON-Schema contract with no fixtures at
// all still publishes — computed compatibility, and the baseline
// requirement, are both a JSON-Schema-only concern).
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
		"thread: " + cliFixtureThread + "\n" +
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
	if strings.Contains(string(fake.calls[0].Files[0].Content), "state:") {
		t.Fatalf("whole-contract retire is receipt-N/A and must omit state:\n%s", fake.calls[0].Files[0].Content)
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

// TestContractRetireRefusesAMalformedConsumerRegistry is the fail-closed
// half of AC-202.2: the retire precondition counts REGISTERED CONSUMERS by
// reading every system's consumes.yaml, and a file it cannot read as a
// consumes/v1 registry used to be skipped silently — so a wrong-shaped
// registry (the `consumes: []` placeholder a real space carried) meant
// "zero consumers" and a contract could be retired out from under a system
// subscribed to it. Reading must fail loudly instead.
func TestContractRetireRefusesAMalformedConsumerRegistry(t *testing.T) {
	t.Parallel()

	for name, registry := range map[string]string{
		"placeholder shape": "consumes: []\n",
		"missing header":    "dependencies:\n  - contract: XC-axon-gated\n    major: 1\n",
		"unparseable yaml":  "dependencies: [unclosed\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			writeContractDescriptor(t, mirrorDir, "gated", "1.0.0")
			writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-gated", "publish", "axon")
			writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-gated", "deprecate", "axon")
			writeMirrorFile(t, mirrorDir, "beta/consumes.yaml", registry)
			writeDeprecationAnnouncement(t, mirrorDir, "XA-axon-20260101-a1a1", "XC-axon-gated@1.0.0", "2099-01-01")

			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("human", "owner"))
			io, _, errOut := newIO()
			if code := cmd.Run(context.Background(), []string{"retire", "--override", "XC-axon-gated"}, io); code == 0 {
				t.Fatal("expected a non-zero exit: the consumer registry could not be read, so the consumer count is unknown")
			}
			if !strings.Contains(errOut.String(), "consumes.yaml") {
				t.Fatalf("expected the refusal to name the offending file; got %q", errOut.String())
			}
			if len(fake.calls) != 0 {
				t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
			}
		})
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
	var sawAnnouncementPublishReceipt bool
	for _, fw := range files {
		content := string(fw.Content)
		if strings.Contains(content, "transition: publish") && strings.Contains(content, "state: published") {
			sawAnnouncementPublishReceipt = true
		}
	}
	if !sawAnnouncementPublishReceipt {
		t.Fatalf("derived announcement publish omitted its evaluator receipt: %+v", files)
	}
}

// TestContractDeprecateAnnouncementInheritsContractThread is spec 46 §T1
// R2: `contract deprecate`'s linked announcement is DERIVED from the
// contract being deprecated, so it inherits the CONTRACT's own thread
// verbatim rather than leaving the template's thread placeholder unfilled.
func TestContractDeprecateAnnouncementInheritsContractThread(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "threaded", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-threaded", "publish", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-threaded@2.0.0", "--sunset", "2026-12-31", "XC-axon-threaded"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	var sawAnnouncement bool
	for _, fw := range fake.calls[0].Files {
		if strings.Contains(fw.Path, "/exchanges/XA-") {
			sawAnnouncement = true
			content := string(fw.Content)
			if !strings.Contains(content, "thread: thread:axon-20260721-c9c1\n") {
				t.Fatalf("expected the announcement to inherit the contract's thread verbatim, got:\n%s", content)
			}
		}
	}
	if !sawAnnouncement {
		t.Fatalf("expected an announcement artifact among the committed files, got %+v", fake.calls[0].Files)
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
func TestContractNewDelegatesToNewCommand(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	newCmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
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

// TestContractNewSlugSpellings is the defect-fix test: `contract new`
// previously took the slug positionally ONLY, so `a2a contract new --slug
// foo` passed the literal string "--slug" through as the slug (silently
// wrong, no error) — inconsistent with `a2a new contract --slug foo`,
// which IS a flag. This proves the positional form, the `--slug` flag
// form, and the `--field slug=` form all resolve to the SAME staged
// draft, and that a positional/--slug disagreement is a loud usage error
// rather than a silent pick.
func TestContractNewSlugSpellings(t *testing.T) {
	t.Parallel()

	newContractCmd := func(t *testing.T, stagingDir string) *cli.ContractCommand {
		t.Helper()
		newCmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver, nil)
		return cli.NewContractCommand(newCmd, &fakeLifecycleFunnel{}, t.TempDir(), "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	}

	t.Run("positional_form", func(t *testing.T) {
		t.Parallel()
		stagingDir := t.TempDir()
		cmd := newContractCmd(t, stagingDir)
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"new", "widget-positional"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-positional.md")); err != nil {
			t.Fatalf("expected a staged draft: %v", err)
		}
	})

	t.Run("--slug_flag_form", func(t *testing.T) {
		t.Parallel()
		stagingDir := t.TempDir()
		cmd := newContractCmd(t, stagingDir)
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"new", "--slug", "widget-flag"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-flag.md")); err != nil {
			t.Fatalf("expected a staged draft at the --slug-derived path (regression: --slug used to be swallowed as the literal slug string): %v", err)
		}
	})

	t.Run("--field_slug_form", func(t *testing.T) {
		t.Parallel()
		stagingDir := t.TempDir()
		cmd := newContractCmd(t, stagingDir)
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"new", "--field", "slug=widget-field"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-field.md")); err != nil {
			t.Fatalf("expected a staged draft at the --field slug=-derived path: %v", err)
		}
	})

	t.Run("conflicting_positional_and_flag_errors_loudly", func(t *testing.T) {
		t.Parallel()
		stagingDir := t.TempDir()
		cmd := newContractCmd(t, stagingDir)
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"new", "widget-a", "--slug", "widget-b"}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2 (usage error); stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if !strings.Contains(errOut.String(), "conflicting slug") {
			t.Fatalf("expected a conflicting-slug error, got stderr=%q", errOut.String())
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-a.md")); err == nil {
			t.Fatal("expected no draft staged for the losing positional slug")
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-b.md")); err == nil {
			t.Fatal("expected no draft staged for the losing --slug value either — a conflict must refuse both, not silently pick one")
		}
	})

	t.Run("agreeing_positional_and_flag_is_not_a_conflict", func(t *testing.T) {
		t.Parallel()
		stagingDir := t.TempDir()
		cmd := newContractCmd(t, stagingDir)
		io, out, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"new", "widget-same", "--slug", "widget-same"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (identical spellings must not conflict); stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if _, err := os.Stat(filepath.Join(stagingDir, "XC-axon-widget-same.md")); err != nil {
			t.Fatalf("expected a staged draft: %v", err)
		}
	})
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

// extractAnnouncementTo decodes the committed announcement artifact's own
// `to:` frontmatter field out of a funnel call's files — used by the F3/T4
// addressing tests below to read back what `contract deprecate` actually
// addressed the deprecation to.
func extractAnnouncementTo(t *testing.T, files []space.FileWrite) []string {
	t.Helper()
	for _, fw := range files {
		if !strings.Contains(fw.Path, "/exchanges/XA-") {
			continue
		}
		fm, err := artifact.ParseFrontmatter(fw.Content)
		if err != nil {
			t.Fatalf("extractAnnouncementTo: parse frontmatter: %v", err)
		}
		var probe struct {
			To []string `yaml:"to"`
		}
		if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
			t.Fatalf("extractAnnouncementTo: decode `to`: %v", err)
		}
		return probe.To
	}
	t.Fatal("extractAnnouncementTo: no announcement artifact among the committed files")
	return nil
}

// TestContractDeprecateAddressesRegisteredConsumers is F3/T4 (AC-971.1,
// AC-971.2): the deprecation announcement's `to:` is the registered-
// consumer set (cache.FindRegisteredConsumers), not the descriptor's own
// authoring-time `to:`. The descriptor here carries `to: [beta]`
// (writeContractDescriptor's own fixed shape); `gamma` is registered ONLY
// via a `consumes.yaml` entry (the `contract adopt` shape) and never
// appears in the descriptor at all — exactly AC-971.1's own scenario, a
// consumer that would otherwise never be addressed. Expect `[beta gamma]`:
// this reds under every wrong implementation the brief calls out —
// reverting to `probe.To` yields `[beta]`; reading only the consumes.yaml
// half of the union (never the requirement half, exercised by
// TestContractRetireCleanAckSucceedsUngated's siblings elsewhere) would
// still pass here since both consumers are consumes.yaml-registered, so
// the point this test proves is specifically "the descriptor's `to:` is
// NOT the source" and "the set is sorted, deduped, and excludes `from`
// (axon never appears)".
func TestContractDeprecateAddressesRegisteredConsumers(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "addressed", "1.0.0") // descriptor `to: [beta]`
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-addressed", "publish", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-addressed")
	writeConsumesYAML(t, mirrorDir, "gamma", "XC-axon-addressed")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"deprecate", "--version", "1.0.0", "--successor", "XC-axon-addressed@2.0.0", "--sunset", "2026-12-31", "XC-axon-addressed"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	got := extractAnnouncementTo(t, fake.calls[0].Files)
	want := []string{"beta", "gamma"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("announcement `to:` = %v, want %v (registered-consumer set, not descriptor `to:`)", got, want)
	}
}

// TestContractDeprecateAddressesAreOneQueryWithRetire is AC-971.2's "one
// query" proof: `contract deprecate`'s `to:` and `contract retire
// --override`'s own `retired-unacked` note (see
// TestContractRetireOverrideFullPreconditionSucceeds — that note IS the
// registered-consumer set the retire precondition reads, per
// contractBuildRetirePrecondition/cache.FindRegisteredConsumers) must be
// the SAME set, because both are computed by calling
// cache.FindRegisteredConsumers(mirrorDir, contractID) — literally the
// one function, not two independently-written call sites that happen to
// agree today. Materializing deprecate's own committed files into the
// mirror (as a real commit would) and then running retire against that
// SAME mirror is what makes the two reads observe identical registry
// state.
//
// The two sets coincide here for a second, narrower reason worth stating
// explicitly: `retired-unacked` is filtered to `!Left && !Acked` consumers,
// while deprecate's `to:` is the raw registered set (minus `from`). They
// are equal in THIS test only because nobody has acked and nobody has
// `left` — not because the two computations are defined to always match on
// every input, only because they both start from the same
// cache.FindRegisteredConsumers query.
//
// Reverting runDeprecate's `to:` back to `probe.To`, or computing the
// addressee set any other way than calling cache.FindRegisteredConsumers
// directly, reds this test: the descriptor's own `to: [beta]` differs from
// the two-system registered set `[beta gamma]` that retire's own
// `retired-unacked` note independently proves is correct.
func TestContractDeprecateAddressesAreOneQueryWithRetire(t *testing.T) {
	t.Parallel()
	fixedNowDeprecate := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fixedNowRetire := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) // after the sunset below

	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "onequery", "1.0.0") // descriptor `to: [beta]`
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-onequery", "publish", "axon")
	writeConsumesYAML(t, mirrorDir, "beta", "XC-axon-onequery")
	writeConsumesYAML(t, mirrorDir, "gamma", "XC-axon-onequery")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	cmd.SetClockForTest(func() time.Time { return fixedNowDeprecate })
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"deprecate", "--version", "1.0.0", "--successor", "XC-axon-onequery@2.0.0", "--sunset", "2026-01-02", "XC-axon-onequery"}, io)
	if code != 0 {
		t.Fatalf("deprecate: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	deprecateTo := extractAnnouncementTo(t, fake.calls[0].Files)
	announcementID := extractAnnouncementID(fake.calls[0].Files)
	materializeFiles(t, mirrorDir, fake.calls[0])

	// A reminder note event on the announcement's own thread, plus a human
	// actor and --override, is what makes retire's override path succeed
	// and reveal `retired-unacked` (§5.4 bullet (b) — see
	// TestContractRetireOverrideFullPreconditionSucceeds).
	writeLifecycleEvent(t, mirrorDir, "axon", 1, announcementID, "note", "axon")

	retireFake := &fakeLifecycleFunnel{}
	retireCmd := cli.NewContractCommand(nil, retireFake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("human", "owner"))
	retireCmd.SetClockForTest(func() time.Time { return fixedNowRetire })
	retireIO, _, retireErrOut := newIO()
	retireCode := retireCmd.Run(context.Background(), []string{"retire", "--version", "1.0.0", "--override", "XC-axon-onequery"}, retireIO)
	if retireCode != 0 {
		t.Fatalf("retire: code = %d, want 0; stderr=%s", retireCode, retireErrOut.String())
	}
	if len(retireFake.calls) != 1 {
		t.Fatalf("expected exactly one retire funnel call, got %d", len(retireFake.calls))
	}
	retireContent := string(retireFake.calls[0].Files[0].Content)
	if !strings.Contains(retireContent, "retired-unacked: beta, gamma") {
		t.Fatalf("expected retire's own registered-consumer read to name both beta and gamma, got:\n%s", retireContent)
	}
	sortedDeprecateTo := append([]string(nil), deprecateTo...)
	sort.Strings(sortedDeprecateTo)
	if strings.Join(sortedDeprecateTo, ", ") != "beta, gamma" {
		t.Fatalf("deprecate `to:` = %v, want [beta gamma] — the SAME set retire's `retired-unacked` note names", deprecateTo)
	}
}

// TestContractDeprecateRetireRefuseAmbiguousVersion is F4/AC-972.1: with
// MORE THAN ONE distinct published version on record and `--version`
// omitted, both `deprecate` and `retire` used to default to the
// descriptor's CURRENT version — after a `--bump major` that is the NEW
// version, so the OLD one silently got no announcement/retire at all. Both
// now REFUSE (usage error, exit 2) and list every published version.
func TestContractDeprecateRetireRefuseAmbiguousVersion(t *testing.T) {
	t.Parallel()

	seedTwoPublishedVersions := func(t *testing.T, mirrorDir, slug string) {
		t.Helper()
		writeContractDescriptor(t, mirrorDir, slug, "2.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-"+slug, "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-"+slug, "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	}

	t.Run("deprecate_refuses_and_lists_versions", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		seedTwoPublishedVersions(t, mirrorDir, "ambiguous-dep")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-ambiguous-dep@3.0.0", "--sunset", "2026-12-31", "XC-axon-ambiguous-dep"}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2 (usage error: ambiguous --version); stderr=%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "--version is required") || !strings.Contains(errOut.String(), "1.0.0") || !strings.Contains(errOut.String(), "2.0.0") {
			t.Fatalf("expected a refusal listing both published versions, got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called, got %d call(s)", len(fake.calls))
		}
	})

	t.Run("retire_refuses_and_lists_versions", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		seedTwoPublishedVersions(t, mirrorDir, "ambiguous-ret")
		// retire's own legality requires a prior deprecate on record
		// (LFC-001) regardless of F4 — seeded here so the refusal under
		// test is actually the version-ambiguity one, not legality.
		writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-ambiguous-ret", "deprecate", "axon")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"retire", "XC-axon-ambiguous-ret"}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2 (usage error: ambiguous --version); stderr=%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "--version is required") || !strings.Contains(errOut.String(), "1.0.0") || !strings.Contains(errOut.String(), "2.0.0") {
			t.Fatalf("expected a refusal listing both published versions, got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called, got %d call(s)", len(fake.calls))
		}
	})
}

// TestContractDeprecateRetireDefaultWithOneVersion is F4/AC-972.1's other
// half: with exactly ONE distinct published version on record, omitting
// `--version` stays unambiguous and both verbs still default (never
// refuse).
func TestContractDeprecateRetireDefaultWithOneVersion(t *testing.T) {
	t.Parallel()

	seedOnePublishedVersion := func(t *testing.T, mirrorDir, slug string) {
		t.Helper()
		writeContractDescriptor(t, mirrorDir, slug, "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-"+slug, "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	}

	t.Run("deprecate_defaults", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		seedOnePublishedVersion(t, mirrorDir, "single-dep")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-single-dep@2.0.0", "--sunset", "2026-12-31", "XC-axon-single-dep"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (single published version, unambiguous default); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
		var sawReceipt bool
		for _, file := range fake.calls[0].Files {
			content := string(file.Content)
			if strings.Contains(content, "transition: deprecate") && strings.Contains(content, "state: deprecated") {
				sawReceipt = true
			}
		}
		if !sawReceipt {
			t.Fatalf("versioned deprecate omitted its evaluator receipt: %+v", fake.calls[0].Files)
		}
	})

	t.Run("retire_defaults", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		seedOnePublishedVersion(t, mirrorDir, "single-ret")
		writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-single-ret", "deprecate", "axon")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"retire", "XC-axon-single-ret"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (single published version, unambiguous default; no registered consumers); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})
}

// TestContractRetireSucceedsWhileAnotherVersionPublished is P4's AC-7/AC-9
// (04-per-version-lifecycle.plan.md): `publish 2.0 -> deprecate 1.0 ->
// retire 1.0` while 2.0 stays published and consumed. Before P4 this was
// blocked by POL-011, a fail-closed stopgap for internal/fold's then
// SUBJECT-scoped table (retiring 1.0 legally moved the whole contract
// SUBJECT to Retired, bricking 2.0's own future publish/deprecate). P4
// turns on internal/fold's per-version engine and deletes POL-011 in the
// same wave (agent-ops-2026-07 wave 4): fold.CheckCandidate now answers
// per VERSION (Versions["1.0.0"] == deprecated is independently legal to
// retire, regardless of Versions["2.0.0"] == published), so this exact
// sequence — the everyday rolling-window case P4 exists to enable — now
// SUCCEEDS instead of refusing. Re-pinned from "refused (POL-011, exit
// 2)" to "succeeds (exit 0, one funnel call)".
func TestContractRetireSucceedsWhileAnotherVersionPublished(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "guarded", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-guarded", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-guarded", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-guarded", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "--version", "1.0.0", "XC-axon-guarded"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (P4: per-version retire, 2.0.0 stays published); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "state: retired") {
		t.Fatalf("versioned retire omitted its evaluator receipt:\n%s", fake.calls[0].Files[0].Content)
	}
}

// TestContractRetireBothCycleOrdersSucceed is P4's AC-P4.2
// (04-per-version-lifecycle.plan.md): the two §5.4 event orders that
// differ only in WHEN 1.0 is deprecated relative to 2.0's publish must
// BOTH complete a `retire --version 1.0.0`, in either order, rather than
// completing in one and refusing (or bricking the contract) in the
// other. Before P4, internal/fold's SUBJECT-scoped table made this
// order-dependent: "deprecate then publish" left the subject Published
// (no (Published, retire) row — LFC-001) while "publish then deprecate"
// left it Deprecated and reachable, but only past POL-011's own
// still-published-elsewhere guard (deleted this wave). P4's per-version
// engine answers both orders identically because it tracks 1.0 and 2.0
// independently: 1.0 deprecated is retireable regardless of when 2.0
// published relative to it. Re-pinned from "both refuse (one via
// LFC-001, one via POL-011)" to "both succeed" — this is the exact
// defect spec 02/agent-ops-2026-07 P2 filed and P4 now fixes at its
// root, so the two orders converging is not a coincidence to merely
// tolerate.
func TestContractRetireBothCycleOrdersSucceed(t *testing.T) {
	t.Parallel()

	t.Run("deprecate_then_publish_succeeds", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "order-a", "2.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-order-a", "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-order-a", "deprecate", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-order-a", "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"retire", "--version", "1.0.0", "XC-axon-order-a"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (P4: 1.0 deprecated is independently retireable); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	t.Run("publish_then_deprecate_succeeds", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "order-b", "2.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-order-b", "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-order-b", "publish", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-order-b", "deprecate", "axon")
		appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"retire", "--version", "1.0.0", "XC-axon-order-b"}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (P4: 1.0 deprecated is independently retireable); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})
}

// TestContractRetireNotBlockedByConsumerOnAnotherMajor is epic AC-9
// (04-per-version-lifecycle.md §4, Edge 1): a consumer registered on a
// DIFFERENT major must not block retiring this line forever. "beta"
// registers via consumes.yaml at major 2 while 1.0 is the line being
// retired (2.0 stays published) — before the fix, the retire path's
// consumer scan was CONTRACT-scoped (every registered system, regardless
// of which major it consumes), so beta's major-2
// registration would block this retire forever even though beta never
// depends on the 1.x line, and no deprecation announcement even exists to
// ack against. TEETH: calling the unscoped consumer query from the retire
// path (or dropping cache.FindRegisteredConsumersForMajor's own major
// filter) reds this test with POL-006 — verified by reverting the filter
// and re-running (see this wave's own report).
func TestContractRetireNotBlockedByConsumerOnAnotherMajor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "majorgap", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-majorgap", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-majorgap", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "2.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 2, "XC-axon-majorgap", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	// "beta" depends on the 2.x line only — never the 1.x line being retired.
	writeMirrorFile(t, mirrorDir, "beta/consumes.yaml",
		"schema: consumes/v1\nsystem: beta\ndependencies:\n  - contract: XC-axon-majorgap\n    major: 2\n    since: \"2026-01-01\"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "--version", "1.0.0", "XC-axon-majorgap"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (AC-9: a major-2 consumer must not block retiring the 1.x line); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 || fake.calls[0].PRBody != "" {
		t.Fatalf("expected an ungated retire (no consumer registered on the major being retired), got %+v", fake.calls)
	}
}

// TestContractRetireGuardAllowsSolePublishedVersion is spec 02's AC-2.2
// regression guard — the single named regression risk: retiring the
// ONLY published version, the everyday unremarkable case, must keep
// working exactly as before this phase.
func TestContractRetireGuardAllowsSolePublishedVersion(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "sole", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-sole", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-sole", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-sole"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (sole published version, the guard must not block it); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
}

// TestContractRetireGuardNormalizesVersionComparison guards against a
// review finding made while writing this guard: contractResolveVersionOrRefuse
// returns retiredVersion VERBATIM (the raw descriptor `version:` spelling),
// never reformatted through contractSemver — so the guard must compare
// PARSED contractSemver values, not raw strings, or a non-canonically
// spelled sole version (a redundant leading zero here) would fail to
// exclude itself from "still published", and the guard would wrongly
// refuse the everyday case AC-2.2 protects.
func TestContractRetireGuardNormalizesVersionComparison(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	// The spellings MUST differ across the events. `publish` records its own
	// parsed value; `deprecate`/`retire` record the operator's --version
	// verbatim. Writing the same literal twice passes against the raw-string
	// bug — the deprecate already unsets the key the delete would remove —
	// and so proves nothing. This fixture is the wiring half; the
	// discriminating unit repro lives in internal/validate.
	writeContractDescriptor(t, mirrorDir, "normalize", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-normalize", "publish", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 1, "XC-axon-normalize", "deprecate", "axon")
	appendVersionToLatestEvent(t, mirrorDir, "axon", "01.0.0")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"retire", "XC-axon-normalize"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (a non-canonical spelling of the sole version must still exclude itself); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
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

// writeForeignContractDescriptor seeds ANOTHER system's published
// contract in the mirror — what `contract adopt` reads to pin a major.
func writeForeignContractDescriptor(t *testing.T, mirrorDir, system, slug, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-" + system + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + system + "\n" +
		"to: [axon]\n" +
		"thread: " + cliFixtureThread + "\n" +
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
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/contract.md", content)
}

// writeForeignContractDescriptorWithXBinding is writeForeignContractDescriptor
// plus a raw x_binding block (spec 05, 2026-08-10 amendment) — xBindingRaw is
// inserted as-is, so a caller may pass either the `x_binding: none` sentinel
// line or a full long-form mapping.
func writeForeignContractDescriptorWithXBinding(t *testing.T, mirrorDir, system, slug, version, xBindingRaw string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-" + system + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + system + "\n" +
		"to: [axon]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		xBindingRaw +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, system+"/provides/"+slug+"/contract.md", content)
}

// TestContractAdopt covers the D-022 consumer registry writer: the verb
// that makes a system a REGISTERED consumer (§5.2.3). Before it existed
// there was no tool path to that file at all.
func TestContractAdopt(t *testing.T) {
	t.Parallel()

	t.Run("writes a schema-shaped registry pinned to the published major", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptor(t, mirrorDir, "beta", "content-feed", "2.3.1")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, out, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
		files := fake.calls[0].Files
		if len(files) != 1 || files[0].Path != "axon/consumes.yaml" {
			t.Fatalf("expected one write to axon/consumes.yaml, got %+v", files)
		}
		registry, err := space.ParseConsumes(files[0].Content)
		if err != nil {
			t.Fatalf("ParseConsumes: %v", err)
		}
		if registry.Schema != "consumes/v1" || registry.System != "axon" {
			t.Fatalf("registry header = %+v, want schema consumes/v1 + system axon", registry)
		}
		if len(registry.Dependencies) != 1 {
			t.Fatalf("Dependencies = %+v, want exactly one", registry.Dependencies)
		}
		dep := registry.Dependencies[0]
		if dep.Contract != "XC-beta-content-feed" || dep.Major != 2 {
			t.Fatalf("dependency = %+v, want XC-beta-content-feed at major 2 (from version 2.3.1)", dep)
		}
		if dep.Since == "" {
			t.Fatalf("dependency = %+v, want a `since` date", dep)
		}
	})

	t.Run("appends to an existing registry, sorted, keeping prior entries", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptor(t, mirrorDir, "beta", "content-feed", "1.0.0")
		writeMirrorFile(t, mirrorDir, "axon/consumes.yaml",
			"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-gamma-todo-feed\n    major: 4\n    since: \"2026-01-01\"\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed", "--note", "renders the feed"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		registry, err := space.ParseConsumes(fake.calls[0].Files[0].Content)
		if err != nil {
			t.Fatalf("ParseConsumes: %v", err)
		}
		if len(registry.Dependencies) != 2 {
			t.Fatalf("Dependencies = %+v, want both the prior and the new one", registry.Dependencies)
		}
		if registry.Dependencies[0].Contract != "XC-beta-content-feed" || registry.Dependencies[1].Contract != "XC-gamma-todo-feed" {
			t.Fatalf("Dependencies not sorted by contract id: %+v", registry.Dependencies)
		}
		if registry.Dependencies[0].Note != "renders the feed" {
			t.Fatalf("--note not recorded: %+v", registry.Dependencies[0])
		}
		if registry.Dependencies[1].Major != 4 || registry.Dependencies[1].Since != "2026-01-01" {
			t.Fatalf("the prior entry must survive untouched: %+v", registry.Dependencies[1])
		}
	})

	t.Run("re-adopting the same major is an idempotent no-op", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptor(t, mirrorDir, "beta", "content-feed", "1.2.0")
		writeMirrorFile(t, mirrorDir, "axon/consumes.yaml",
			"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-beta-content-feed\n    major: 1\n    since: \"2026-01-01\"\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, out, _ := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("an already-registered dependency must not open a PR, got %+v", fake.calls)
		}
		if !strings.Contains(out.String(), "already registered") {
			t.Fatalf("expected the idempotent message, got %q", out.String())
		}
	})

	t.Run("re-pinning to a new major rewrites the entry", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeMirrorFile(t, mirrorDir, "axon/consumes.yaml",
			"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-beta-content-feed\n    major: 1\n    since: \"2026-01-01\"\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed", "--major", "2"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		registry, err := space.ParseConsumes(fake.calls[0].Files[0].Content)
		if err != nil {
			t.Fatalf("ParseConsumes: %v", err)
		}
		if len(registry.Dependencies) != 1 || registry.Dependencies[0].Major != 2 {
			t.Fatalf("Dependencies = %+v, want the single entry re-pinned to major 2", registry.Dependencies)
		}
	})

	t.Run("a note-only edit keeps the original since date", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeMirrorFile(t, mirrorDir, "axon/consumes.yaml",
			"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-beta-content-feed\n    major: 1\n    since: \"2026-01-01\"\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed", "--major", "1", "--note", "why we depend on it"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		registry, err := space.ParseConsumes(fake.calls[0].Files[0].Content)
		if err != nil {
			t.Fatalf("ParseConsumes: %v", err)
		}
		dep := registry.Dependencies[0]
		if dep.Note != "why we depend on it" {
			t.Fatalf("dependency = %+v, want the new note recorded", dep)
		}
		if dep.Since != "2026-01-01" {
			t.Fatalf("dependency = %+v, want `since` untouched — it records when the dependency was DECLARED, not when the row was last edited", dep)
		}
	})

	t.Run("refuses this system's own contract", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeContractDescriptor(t, mirrorDir, "widget-a", "1.0.0")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-axon-widget-a"}, io); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), "OWN contract") {
			t.Fatalf("expected an own-contract refusal, got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
		}
	})

	t.Run("an unsynced contract is an actionable error, never a guessed major", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), "--major") {
			t.Fatalf("expected the error to name the --major escape hatch, got %q", errOut.String())
		}
	})

	// P5 US-1's counterweight (specs/05-declared-nature.md, 2026-08-10
	// amendment): a contract that declares itself non-adoptable via
	// x_binding refuses adopt outright, in both the sentinel and the
	// long-form shape, and regardless of whether --major is passed
	// explicitly — the flag must not be an escape hatch around the
	// refusal.
	t.Run("refuses a contract declaring x_binding: none", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptorWithXBinding(t, mirrorDir, "beta", "content-feed", "1.0.0", "x_binding: none\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 1 {
			t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "non-adoptable") {
			t.Fatalf("expected a non-adoptable refusal, got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
		}
	})

	t.Run("refuses a contract declaring the long form with adoptable: false", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptorWithXBinding(t, mirrorDir, "beta", "content-feed", "1.0.0",
			"x_binding:\n  artifact_class: non_binding_review\n  compatibility_status: none\n  adoptable: false\n  runtime_pinnable: false\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 1 {
			t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "non-adoptable") {
			t.Fatalf("expected a non-adoptable refusal, got %q", errOut.String())
		}
	})

	t.Run("refuses a non-adoptable contract even when --major is passed explicitly", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptorWithXBinding(t, mirrorDir, "beta", "content-feed", "1.0.0", "x_binding: none\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed", "--major", "1"}, io); code != 1 {
			t.Fatalf("code = %d, want 1 (an explicit --major must not be an escape hatch); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("refusal must happen before any funnel call, got %+v", fake.calls)
		}
	})

	t.Run("adopts a contract declaring the long form with adoptable: true", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptorWithXBinding(t, mirrorDir, "beta", "content-feed", "1.0.0",
			"x_binding:\n  artifact_class: reference_impl\n  compatibility_status: strict-semver\n  adoptable: true\n  runtime_pinnable: true\n")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	t.Run("adopts a contract with no x_binding declared at all (undeclared, distinct from none)", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		writeForeignContractDescriptor(t, mirrorDir, "beta", "content-feed", "1.0.0")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"adopt", "XC-beta-content-feed"}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})
}

// Every contract sub-verb must accept its flags AFTER the positional id,
// because that is the order its OWN usage string tells the caller to use.
//
// This is not a style preference. Go's flag package stops parsing at the
// first non-flag token, so `contract deprecate <id> --successor X --sunset Y`
// leaves both flags unparsed, fails the required-flag check, and prints the
// very usage line the caller just followed. `contract adopt` already lifts
// the id before Parse and documents why; its three siblings did not, and no
// test caught it because every existing test passes the flags FIRST — the
// order that works, not the order that is advertised.
//
// The live tier is what found it: driving the real binary the way the usage
// string reads produced exit 2 on `contract deprecate`.
func TestContractSubVerbsAcceptFlagsAfterTheID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"deprecate", []string{"deprecate", "XC-axon-depme", "--successor", "XC-axon-depme@2.0.0", "--sunset", "2026-12-31"}},
		{"publish", []string{"publish", "XC-axon-depme", "--bump", "minor"}},
		{"retire", []string{"retire", "XC-axon-depme", "--override"}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			writeContractDescriptor(t, mirrorDir, "depme", "1.0.0")
			writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-depme", "publish", "axon")

			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, errOut := newIO()
			code := cmd.Run(context.Background(), tc.args, io)

			// Exit 2 is the usage/parse failure specifically. A later
			// refusal (legality, preconditions) is a different question and
			// is covered by the tests above — this one asserts only that the
			// ARGUMENTS were understood.
			if code == 2 {
				t.Fatalf("`a2a contract %s <id> --flags...` exited 2 (usage) — the flags after the id were not parsed; stderr=%s",
					tc.verb, errOut.String())
			}
		})
	}
}

// The flags-first order must keep working — the fix lifts the id out, it does
// not move it. Both orders are legal; neither is the only one.
func TestContractSubVerbsStillAcceptFlagsBeforeTheID(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeContractDescriptor(t, mirrorDir, "depme", "1.0.0")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XC-axon-depme", "publish", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewContractCommand(nil, fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"deprecate", "--successor", "XC-axon-depme@2.0.0", "--sunset", "2026-12-31", "XC-axon-depme"}, io)
	if code == 2 {
		t.Fatalf("flags-before-id exited 2 (usage); stderr=%s", errOut.String())
	}
}

// --- P37 Wave I: `contract publish` carries the schema/fixtures change it
// is declaring a version for ------------------------------------------------
//
// The defect this wave fixes: internal/space/mirror.go's
// checkoutRemoteHead hard-resets the mirror working tree to
// origin/<branch> on EVERY `a2a` invocation, so by the time `contract
// publish` runs, the mirror can never carry an author's own schema/
// fixture edit — only .a2a/staging/ still can. The tests above this
// section (TestContractPublishComputedCompatibility et al.) prove F1/
// POL-007/POL-008/POL-009 by mutating the MIRROR's own working tree
// directly, which is exactly the scenario the real hard reset makes
// unreachable in production — they were green while the check was
// unreachable. These tests instead route the "new version's" schema
// through STAGING, the one place a real invocation could still find it,
// proving the overlay this wave adds.

// TestContractPublishOverlayCarriesStagedSchema is AC-970.1 (a mislabeled
// minor is refused, naming the fixture) and its --bump major counterpart,
// driven through a REAL staging dir rather than a mutated mirror working
// tree. TEETH: reverting runPublish to read newSchemas/newFixturesValid
// straight from contractReadWorkingTreeFiles(workDir, ...) (i.e. dropping
// the contractStagingOverlay fold-in) reds BOTH sub-tests — the staged
// narrowed schema is never seen, so the minor bump publishes clean instead
// of refusing, and the major bump's carried Files never contain the
// staged bytes.
// gitOutputForTest runs `git <args...>` with cwd=dir and returns stdout,
// failing the test loudly. The read-only sibling of gitRun, for assertions
// that must inspect a COMMIT rather than the working tree — since a write
// no longer leaves the mirror standing on its own branch.
func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v", args, dir, err)
	}
	return string(out)
}
