package notes

import (
	"os"
	"strings"
	"testing"
)

func TestRenderMarkdownUsesAuthoredNotesAsReleaseBody(t *testing.T) {
	t.Parallel()

	rn := ReleaseNotes{
		Version:  "1.2.3",
		Released: "2026-07-30",
		Headline: "Agents can finish the handoff",
		Changes: []Change{
			{
				Kind: "fix", Impact: "high", Subject: "Retries keep their identity",
				Detail:  "A retry now recovers the original operation.",
				Affects: []string{"a2a submit", "a2a respond"},
				Action: Action{
					Scope: "local", Why: "Retry the same command after updating.",
					Run: []string{"a2a update --yes", "a2a doctor"},
				},
			},
			{
				Kind: KindKnownIssue, Impact: "normal", Subject: "macOS may ask once",
				Detail: "The companion is ad-hoc signed.",
				Action: Action{Scope: "none", Why: "Approve only the verified release in Privacy & Security."},
			},
		},
	}

	got := RenderMarkdown(rn, MarkdownOptions{
		RepositoryURL: "https://github.com/example/a2a/",
		Verification:  "50/50 declared live cells passed.",
	})
	for _, want := range []string{
		"# v1.2.3 — Agents can finish the handoff",
		"## What changed",
		"### Retries keep their identity",
		"`fix` · `high impact`",
		"**Affects:** `a2a submit`, `a2a respond`",
		"**What to do (local):** Retry the same command after updating.",
		"a2a update --yes",
		"## Known issues",
		"### macOS may ask once",
		"**Current guidance:** Approve only the verified release in Privacy & Security.",
		"## Release verification",
		"50/50 declared live cells passed.",
		"## Install or update",
		"https://raw.githubusercontent.com/example/a2a/main/scripts/install.sh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderMarkdown output missing %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{"Which file do I download?", "xattr -d com.apple.quarantine", "## Changelog"} {
		if strings.Contains(got, stale) {
			t.Errorf("RenderMarkdown output contains retired release filler %q:\n%s", stale, got)
		}
	}
}

func TestRenderMarkdownOmitsEmptyOptionalSections(t *testing.T) {
	t.Parallel()

	got := RenderMarkdown(ReleaseNotes{
		Version: "1.0.0", Released: "2026-07-30", Headline: "Small release",
	}, MarkdownOptions{})
	if strings.Contains(got, "## What changed") || strings.Contains(got, "## Known issues") ||
		strings.Contains(got, "## Release verification") {
		t.Fatalf("empty optional section rendered:\n%s", got)
	}
	if !strings.Contains(got, "## Install or update") {
		t.Fatalf("stable install section missing:\n%s", got)
	}
}

func TestReleaseWorkflowPublishesAndVerifiesRenderedNotes(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(raw)
	for _, want := range []string{
		"go run ./scripts/releasebody",
		`> "$RUNNER_TEMP/release-notes.md"`,
		"--release-notes=${{ runner.temp }}/release-notes.md",
		"--template '{{.body}}'",
		"--verify-stdin",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release workflow no longer proves %q is wired:\n%s", want, workflow)
		}
	}
}
