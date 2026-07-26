package livee2e

// probeenvelope.go holds the boundary family's probe artifact renderer.
//
// It lives in an UNTAGGED file on purpose. Every scenario file in this
// package is behind `//go:build livee2e`, which means `make check` cannot
// see any of it — and that is precisely how this function shipped for weeks
// rendering an artifact with NO frontmatter delimiter. Nothing hermetic
// could look at it. A pure string renderer has no reason to hide behind a
// build tag, and out here its own test runs on every commit.

import "time"

// The OPENING `---` is not cosmetic and its absence was a live defect. An
// artifact whose frontmatter has no opening delimiter parses as no
// frontmatter at all, so this probe used to render a file that fails POL-002
// ("artifact frontmatter is missing or is not valid YAML"). Two consequences,
// both found on 2026-07-26 from the space's own failure notifications rather
// than from any assertion in this tier:
//
//   - executed-ref-not-stale pushes this file straight to main, so every
//     later post-merge full-repo audit on that space failed. Nothing in the
//     matrix asserts that job (it is flag-only and never a required check by
//     design), so the tier stayed green while the space's CI was red.
//   - cross-section-retrigger-stays-red asserts only that the required check
//     is `failure`. It was — but for TWO reasons, POL-002 and the
//     section-authz violation the row exists to prove. Both fired, so the row
//     was not vacuous; it was, however, unable to tell the difference, which
//     means diff-authz could have regressed silently.
func boundaryProbeEnvelope(id string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: announcement\n" +
		"title: xsec\n" +
		"space: livee2e\n" +
		"from: alpha\n" +
		"to: [bravo]\n" +
		"actor: {kind: agent, name: probe}\n" +
		"created: " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"category: notice\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"---\n" +
		"body\n"
}
