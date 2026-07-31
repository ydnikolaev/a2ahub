package notes

import (
	"fmt"
	"strings"
)

// MarkdownOptions carries release-page facts that do not belong to the
// release-notes data model itself.
type MarkdownOptions struct {
	RepositoryURL string
	ProjectURL    string
	Verification  string
}

// RenderMarkdown projects one authored release-notes document into the human
// GitHub Release body. The YAML corpus remains the SSOT; this renderer adds only
// stable navigation and install/verification framing.
func RenderMarkdown(rn ReleaseNotes, opts MarkdownOptions) string {
	repositoryURL := strings.TrimRight(opts.RepositoryURL, "/")
	if repositoryURL == "" {
		repositoryURL = "https://github.com/ydnikolaev/a2ahub"
	}
	rawRepositoryURL := strings.Replace(repositoryURL, "https://github.com/", "https://raw.githubusercontent.com/", 1)
	projectURL := strings.TrimRight(opts.ProjectURL, "/")
	if projectURL == "" {
		projectURL = "https://a2ahub.dev"
	}

	var changed, known []Change
	for _, change := range rn.Changes {
		if change.Kind == KindKnownIssue {
			known = append(known, change)
			continue
		}
		changed = append(changed, change)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# v%s — %s\n\n", rn.Version, rn.Headline)
	fmt.Fprintf(&b, "_Released %s._\n\n", rn.Released)

	if len(changed) > 0 {
		b.WriteString("## What changed\n\n")
		for _, change := range changed {
			renderMarkdownChange(&b, change, false)
		}
	}

	if len(known) > 0 {
		b.WriteString("## Known issues\n\n")
		for _, change := range known {
			renderMarkdownChange(&b, change, true)
		}
	}

	if verification := strings.TrimSpace(opts.Verification); verification != "" {
		b.WriteString("## Release verification\n\n")
		b.WriteString(verification)
		b.WriteString("\n\n")
	}

	b.WriteString("## Install or update\n\n")
	b.WriteString("macOS and Linux:\n\n")
	b.WriteString("```sh\n")
	fmt.Fprintf(&b, "curl -fsSL %s/main/scripts/install.sh | sh\n", rawRepositoryURL)
	b.WriteString("```\n\n")
	b.WriteString("Already installed? Run `a2a update --yes`. Windows archives and manual downloads are in the assets below.\n\n")
	fmt.Fprintf(&b, "Release artifacts include checksums, per-asset Sigstore bundles, SBOMs, and SLSA provenance. See [SECURITY.md](%s/blob/main/SECURITY.md) for verification commands.\n", repositoryURL)
	fmt.Fprintf(&b, "\nRead the product overview, documentation, dashboard example, and complete changelog at [a2ahub.dev](%s/).\n", projectURL)

	return b.String()
}

func renderMarkdownChange(b *strings.Builder, change Change, known bool) {
	fmt.Fprintf(b, "### %s\n\n", change.Subject)
	if known {
		fmt.Fprintf(b, "`known issue` · `%s impact`\n\n", change.Impact)
	} else {
		fmt.Fprintf(b, "`%s` · `%s impact`\n\n", change.Kind, change.Impact)
	}
	b.WriteString(strings.TrimSpace(change.Detail))
	b.WriteString("\n\n")

	if len(change.Affects) > 0 {
		quoted := make([]string, 0, len(change.Affects))
		for _, affected := range change.Affects {
			quoted = append(quoted, "`"+affected+"`")
		}
		fmt.Fprintf(b, "**Affects:** %s\n\n", strings.Join(quoted, ", "))
	}

	if change.Action.Scope != "none" {
		fmt.Fprintf(b, "**What to do (%s):** %s\n\n", change.Action.Scope, strings.TrimSpace(change.Action.Why))
		if len(change.Action.Detect) > 0 {
			fmt.Fprintf(b, "**Check:** %s\n\n", strings.Join(change.Action.Detect, "; "))
		}
		if len(change.Action.Run) > 0 {
			b.WriteString("```sh\n")
			for _, command := range change.Action.Run {
				b.WriteString(command)
				b.WriteByte('\n')
			}
			b.WriteString("```\n\n")
		}
	} else if known {
		fmt.Fprintf(b, "**Current guidance:** %s\n\n", strings.TrimSpace(change.Action.Why))
	}
}
