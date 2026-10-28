package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestTroubleshootingEnumeratesEveryDoctorCheck(t *testing.T) {
	t.Parallel()
	cmd := NewDoctorCommand(host.FixedOpener(host.NewFakeHost()), "0.16.0", "/unused/project.yaml", "/unused/machine.yaml", t.TempDir())
	// A connected space is REQUIRED here, not incidental: with zero spaces
	// doctorVisibilityRows returns nothing and the eighteenth printed row
	// (`repository visibility`) never appears, which is exactly how this
	// gate went blind to a wrong per-space cardinality claim in the doc.
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.cachePath = func() (string, error) { return "/unused/no-update-cache.json", nil }
	cmd.lookupGit = func() error { return nil }
	// Hermetic: doctorCheckSpaceAccess would otherwise run a real git
	// clone/fetch against the space's mirror URL.
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }

	var stdout, stderr bytes.Buffer
	// Do NOT gate on the exit code. With a connected space configured
	// against a fake host and no real credential, several of the seventeen
	// checks legitimately FAIL (credentials, CI presence, ...) and doctor
	// returns 1 — that is expected, not a test failure. This test is about
	// the roster the binary prints, not about a clean bill of health.
	cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	raw, err := os.ReadFile("../../skill/a2ahub/troubleshooting.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	fixedNames := make(map[string]bool)
	// Printed order, not just membership. The doc promises "the table below
	// is in output order" and NOTHING held that promise: it went stale in
	// 2026-08-12 (repository visibility sat eighth) and again in 2026-08-18
	// (the three notify-* rows sat ahead of the two skill-* rows, shipped by
	// the epic that added them). Both times it was found by accident, the
	// second time only because an unrelated change made the count wrong. A
	// membership set cannot see an order, so it never could have caught
	// either one.
	var printedOrder []string
	visibilityRows := 0
	for _, line := range strings.Split(stdout.String(), "\n") {
		name, rest, found := strings.Cut(line, ": ")
		if !found {
			continue
		}
		// Every check line carries one of these four statuses (repository
		// visibility never FAILs but can read PASS/WARN/UNVERIFIED; a fixed
		// check reads PASS/FAIL, and UNVERIFIED where it cannot tell).
		// Filtering on the status prefix, rather than reusing the old "PASS only" cut, is what lets
		// this test see checks that fail against the fake host instead of
		// silently dropping them from the roster.
		switch {
		case strings.HasPrefix(rest, "PASS"), strings.HasPrefix(rest, "FAIL"),
			strings.HasPrefix(rest, "WARN"), strings.HasPrefix(rest, "UNVERIFIED"):
		default:
			continue
		}
		// The visibility row's name carries a trailing " [<space-id>]" that
		// the documented table row does not (and must not: the table lists
		// CHECKS, one row each, not one row per connected space). Strip it
		// before the lookup so the row is still checked against the doc —
		// relaxing the "| **name** |" match instead would let a stray space
		// id silently defeat the lookup.
		rosterName := name
		if idx := strings.Index(rosterName, " ["); idx != -1 {
			rosterName = rosterName[:idx]
		}
		if !strings.Contains(doc, "| **"+rosterName+"** |") {
			t.Errorf("troubleshooting omits doctor check %q", rosterName)
		}
		if rosterName == "repository visibility" {
			visibilityRows++
			continue
		}
		fixedNames[rosterName] = true
		printedOrder = append(printedOrder, rosterName)
	}
	if len(fixedNames) != 25 {
		t.Fatalf("doctor emitted %d fixed checks (%v), want 25; update the documented count and this tripwire together", len(fixedNames), fixedNames)
	}

	// The doc's rows, in the order the doc lists them, filtered to the fixed
	// checks — then compared position by position against what the binary
	// printed. Read out of the doc rather than restated here: a second copy
	// of the order in this file would be one more thing to keep in step, and
	// keeping two copies in step is the failure this assertion exists for.
	var docOrder []string
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| **") {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimPrefix(line, "| **"), "** |")
		if !ok {
			continue
		}
		if fixedNames[name] {
			docOrder = append(docOrder, name)
		}
	}
	if len(docOrder) != len(printedOrder) {
		t.Fatalf("doc lists %d fixed rows, binary printed %d", len(docOrder), len(printedOrder))
	}
	for i := range printedOrder {
		if docOrder[i] != printedOrder[i] {
			t.Errorf("row %d: doc says %q, binary prints %q — the doc promises output order; move the row, do not reword the promise", i+1, docOrder[i], printedOrder[i])
		}
	}
	// One connected space was configured above, so exactly one visibility
	// row must have been printed (and checked against the doc) — a count of
	// zero would mean the fixture regressed back to proving nothing about
	// the eighteenth row.
	if visibilityRows != 1 {
		t.Fatalf("doctor emitted %d repository-visibility rows, want 1 (one connected space was configured)", visibilityRows)
	}
	// The heading's number, DERIVED from the roster rather than restated as a
	// literal. It used to be the literal string "## The seventeen checks",
	// which is why the heading read "seventeen" on 2026-08-18 while the doc's
	// own summary sentence forty lines above it read "twenty": the assertion
	// was in lockstep with a number frozen at the moment it was written, not
	// with the binary. A hardcoded expectation cannot notice that the thing it
	// describes has moved — it can only notice that somebody edited the doc
	// without editing this file, which is the opposite of what it is for.
	words := map[int]string{
		15: "fifteen", 16: "sixteen", 17: "seventeen", 18: "eighteen", 19: "nineteen",
		20: "twenty", 21: "twenty-one", 22: "twenty-two", 23: "twenty-three",
		24: "twenty-four", 25: "twenty-five",
	}
	word, ok := words[len(fixedNames)]
	if !ok {
		t.Fatalf("doctor emits %d fixed checks and this test has no spelling for it — add one to `words`", len(fixedNames))
	}
	heading := "## The " + word + " checks"
	if !strings.Contains(doc, heading) {
		t.Errorf("troubleshooting must carry the heading %q — the binary emits %d fixed checks", heading, len(fixedNames))
	}
	// The same number, spelled, in the summary sentence near the top. This one
	// had NO assertion at all, which is how the two drifted apart in the same
	// file without anything noticing.
	if !strings.Contains(doc, "All "+word+" fixed checks") {
		t.Errorf("troubleshooting's summary sentence must read \"All %s fixed checks\" — the binary emits %d", word, len(fixedNames))
	}
}

// TestResolutionsQuoteTheLocalHostRefusals pins the three local-space rows of
// skill/a2ahub/resolutions.md to the strings the binary actually prints, read
// from their exports: a rewording in host or space reds here instead of
// leaving a row that quotes a message nobody sees.
func TestResolutionsQuoteTheLocalHostRefusals(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../skill/a2ahub/resolutions.md")
	if err != nil {
		t.Fatalf("read resolutions.md: %v", err)
	}
	doc := string(raw)
	// The printed land failure wraps the sentinel in a *host.Error, whose
	// Error() strips the sentinel's own "host: " prefix; the row must quote
	// that printed form, not the sentinel text.
	contended := (&host.Error{Op: "OpenPR", Input: "<head>", Err: host.ErrLandContended}).Error() + ":"
	for name, want := range map[string]string{
		"raw push refused":      fmt.Sprintf(host.PreReceiveRefusal, "refs/heads/main"),
		"land contended":        contended,
		"prepared target stale": space.ErrPreparedTargetStale.Error(),
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s: resolutions.md does not quote %q", name, want)
		}
	}
}

// TestOnboardingLocalSpaceNeverSendsTheAgentToGitHub holds the negative half
// of the local-space onboarding section: it teaches a path with no pull
// request and no gh, so a sentence instructing either is a regression.
func TestOnboardingLocalSpaceNeverSendsTheAgentToGitHub(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../skill/a2ahub/onboarding.md")
	if err != nil {
		t.Fatalf("read onboarding.md: %v", err)
	}
	const heading = "## Local space — one operator, several repositories"
	_, rest, found := strings.Cut(string(raw), heading)
	if !found {
		t.Fatalf("onboarding.md has no %q section", heading)
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	if regexp.MustCompile(`\bgh\b`).MatchString(section) {
		t.Errorf("local-space section names gh: %q", section)
	}
	prInstruction := regexp.MustCompile(`(?i)\b(open|create|merge|review|approve)\w*\s+(a\s+|the\s+|its\s+|your\s+)?(PR|pull request)`)
	if m := prInstruction.FindString(section); m != "" {
		t.Errorf("local-space section instructs a pull-request act: %q", m)
	}
}
