package livee2e

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	spacetemplate "github.com/ydnikolaev/a2ahub/space-template"
)

// realSpaceYAMLFragment is a literal copy of the exact bytes
// space-template/space.yaml emits around `participants: []` (verified
// against the real file, not invented) — the shape reset.sh's own regexp
// matches. Kept here as a literal string per the brief rather than under
// testdata/.
const realSpaceYAMLFragment = `schema: space/v1
space: REPLACE_WITH_SPACE_ID
min_binary_version: 0.6.4             # write FLOOR (D-013/CC-085): the funnel
                                      # rejects a writer older than this. A
                                      # SEPARATE axis from the CI validator
                                      # version, which lives in the
                                      # reusable-workflow ref, not here.
                                      # (` + "`a2a space init`" + ` sets this to the
                                      # scaffolding binary's own version.)
gates: default                    # G1-G5 per §3.7; operator refines at space creation
participants: []                  # zero participants at template time (AC-101.1
                                   # green-on-empty) — added one at a time via each
                                   # system's onboarding PR (US-102, §9.2 step 2)
notification_routes: []           # operator fills in chat/webhook routes at creation
vendored: []
`

func TestPatchSpaceParticipantsRoundTrip(t *testing.T) {
	t.Parallel()

	ps := []Participant{
		{System: "alpha", Org: "a2ahub-live-e2e", Section: "alpha/", Owner: "some-login-a", Joined: "2026-07-24"},
		{System: "bravo", Org: "a2ahub-live-e2e", Section: "bravo/", Owner: "some-login-b", Joined: "2026-07-24"},
	}

	got, err := PatchSpaceParticipants(realSpaceYAMLFragment, ps)
	if err != nil {
		t.Fatalf("PatchSpaceParticipants: %v", err)
	}

	// The placeholder and its comment continuation lines must be gone.
	if strings.Contains(got, "participants: []") {
		t.Fatalf("placeholder was not removed:\n%s", got)
	}
	if strings.Contains(got, "AC-101.1") {
		t.Fatalf("trailing comment continuation lines were not removed:\n%s", got)
	}

	// Every other field must survive untouched.
	for _, want := range []string{
		"schema: space/v1",
		"space: REPLACE_WITH_SPACE_ID",
		"notification_routes: []",
		"vendored: []",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to survive the patch, got:\n%s", want, got)
		}
	}

	// The new participants block must be in reset.sh's inline-mapping shape.
	wantLines := []string{
		"participants:",
		"  - {system: alpha, org: a2ahub-live-e2e, section: alpha/, owners: [some-login-a], status: active, joined: 2026-07-24}",
		"  - {system: bravo, org: a2ahub-live-e2e, section: bravo/, owners: [some-login-b], status: active, joined: 2026-07-24}",
	}
	for _, ln := range wantLines {
		if !strings.Contains(got, ln) {
			t.Fatalf("expected line %q in output:\n%s", ln, got)
		}
	}

	// Idempotent structural check: re-running against the ORIGINAL fragment
	// again (not the output) must still find exactly one placeholder.
	if n := strings.Count(realSpaceYAMLFragment, "participants: []"); n != 1 {
		t.Fatalf("fixture sanity: expected exactly one participants placeholder, found %d", n)
	}
}

func TestPatchSpaceParticipantsNotFound(t *testing.T) {
	t.Parallel()

	_, err := PatchSpaceParticipants("schema: space/v1\nno placeholder here\n", []Participant{
		{System: "alpha", Org: "org", Section: "alpha/", Owner: "login", Joined: "2026-07-24"},
	})
	if !errors.Is(err, ErrParticipantsPlaceholderNotFound) {
		t.Fatalf("err = %v, want ErrParticipantsPlaceholderNotFound", err)
	}
}

func TestPatchSpaceParticipantsEmptyList(t *testing.T) {
	t.Parallel()

	got, err := PatchSpaceParticipants(realSpaceYAMLFragment, nil)
	if err != nil {
		t.Fatalf("PatchSpaceParticipants: %v", err)
	}
	if !strings.Contains(got, "participants:\n") {
		t.Fatalf("expected an explicit participants: key even with zero rows, got:\n%s", got)
	}
	// No stray "- {" row should appear for an empty participant list.
	if strings.Contains(got, "- {system:") {
		t.Fatalf("expected no participant rows, got:\n%s", got)
	}
}

func TestRenderCODEOWNERS(t *testing.T) {
	t.Parallel()

	const syntheticOwner = "totally-synthetic-test-login"
	got := RenderCODEOWNERS(syntheticOwner)

	for _, wantPath := range []string{"/space.yaml", "/CODEOWNERS", "/.github/**"} {
		if !strings.Contains(got, wantPath+"   @"+syntheticOwner) {
			t.Fatalf("expected %q gated to @%s, got:\n%s", wantPath, syntheticOwner, got)
		}
	}
	if !strings.Contains(got, "# Trust-root paths only") {
		t.Fatalf("expected the explanatory header comment to be preserved, got:\n%s", got)
	}
}

// TestRenderCODEOWNERSCarriesNoRealLogin proves the package source contains
// no hardcoded GitHub login: every `@<login>` token the renderer emits, for
// a synthetic owner, is exactly `@<synthetic owner>` — none of reset.sh's
// real logins (`ydnikolaev`, `a2a-e2e-test-bot`) leak in regardless of what
// caller supplies.
func TestRenderCODEOWNERSCarriesNoRealLogin(t *testing.T) {
	t.Parallel()

	const syntheticOwner = "synthetic-owner-xyz"
	got := RenderCODEOWNERS(syntheticOwner)

	ownerToken := regexp.MustCompile(`@[A-Za-z0-9-]+`)
	matches := ownerToken.FindAllString(got, -1)
	if len(matches) == 0 {
		t.Fatalf("expected at least one @owner token, got none in:\n%s", got)
	}
	for _, m := range matches {
		if m != "@"+syntheticOwner {
			t.Fatalf("unexpected owner token %q — every owner token must equal the supplied parameter @%s", m, syntheticOwner)
		}
	}
	// Deliberately no literal-login-absence check here: typing reset.sh's
	// real logins into THIS file to assert their absence would reintroduce
	// them into the exact untagged source the constraint keeps clean. The
	// token-extraction assertion above is strictly stronger anyway — it
	// proves every owner token in the output is the parameter, which rules
	// out ANY leaked literal, not just these two.
}

// The live tier stamps the binary it builds with the template's own write
// floor, so the two can never diverge. Read against the REAL embedded
// template rather than a fixture: a fixture would keep passing on the day the
// template stops carrying the pin at all, which is the only way this can
// actually break.
func TestTemplateMinBinaryVersionReadsTheRealTemplate(t *testing.T) {
	t.Parallel()

	raw, err := fs.ReadFile(spacetemplate.Files, "space.yaml")
	if err != nil {
		t.Fatalf("read the embedded space.yaml: %v", err)
	}
	got, err := TemplateMinBinaryVersion(string(raw))
	if err != nil {
		t.Fatalf("TemplateMinBinaryVersion against the real template: %v", err)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(got) {
		t.Fatalf("floor = %q, want a bare major.minor.patch — an unparseable stamp breaks CC-085 for every write scenario", got)
	}
}

func TestTemplateMinBinaryVersionRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, yaml string }{
		{"absent", "id: livee2e\nparticipants: []\n"},
		{"commented out", "id: livee2e\n# min_binary_version: 1.2.3\n"},
		{"indented under another key", "id: livee2e\nnested:\n  min_binary_version: 1.2.3\n"},
		{"not a version", "min_binary_version: latest\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := TemplateMinBinaryVersion(tc.yaml); !errors.Is(err, ErrNoTemplateFloor) {
				t.Fatalf("err = %v, want ErrNoTemplateFloor — guessing a floor builds a binary that cannot write to the space it scaffolded", err)
			}
		})
	}
}
