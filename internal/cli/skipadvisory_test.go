package cli_test

// skipadvisory_test.go covers the stage-2 fix for the defect filed
// 2026-07-26: a mirror file internal/cache's best-effort walk could not
// decode used to drop out of `a2a search`/`inbox`/`outbox`/`thread` with no
// word anywhere. These tests prove the shared skipAdvisory helper
// (internal/cli/skipadvisory.go) is wired into all four verbs, on stderr
// ONLY, and that stdout stays byte-identical to the same space without the
// bad file — the shipped byte-identity guarantee this phase's brief calls
// out explicitly (see inboxWriteUpdateAdvisory's own doc comment,
// cmd_inbox.go, for the precedent this mirrors).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// skippedFixtureThread is the well-formed good artifact's thread id in
// every skipadvisory fixture below — a real §3.8-shaped value (system-
// YYYYMMDD-4alnum), reused so the thread-command case can address it
// directly.
const skippedFixtureThread = "thread:seomatrix-20260701-t1a2"

// skippedFixtureBadRelPath is the malformed artifact's space-relative path
// every case below asserts is NAMED in the advisory — computed once so the
// fixture writer and the assertions can never drift apart.
const skippedFixtureBadRelPath = "seomatrix/exchanges/XW-seomatrix-20260701-bad.md"

// buildSkippedFixture writes a bare (non-git) mirror tree — same convention
// as cachetest_helpers_test.go's own fixtures ("buildIndex needs no real git
// history") — containing one well-formed work_request from seomatrix to
// axon (submitted, so it reaches inbox/outbox/search/thread alike) plus,
// when bad is true, one artifact whose `thread:` key is written twice: the
// exact malformed shape internal/cache/skipped_test.go's own fixtures use
// (SkipReasonUndecodableYAML), written with a raw os.WriteFile-backed
// helper (writeMirrorFile, cmd_lifecycle_test.go) rather than
// cliWriteArtifact, which always marshals well-formed YAML and so can never
// reproduce a duplicate-key document.
func buildSkippedFixture(t *testing.T, bad bool) (dir string, manifest space.Manifest, base time.Time) {
	t.Helper()
	dir = t.TempDir()
	manifest = cliWriteManifest(t, dir, "axon", "seomatrix")
	base = time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	good := cliWR("XW-seomatrix-20260701-good", "good item", "seomatrix", []string{"axon"}, "p2", false)
	good["thread"] = skippedFixtureThread
	cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-good.md", good, "body")
	cliWriteEvent(t, dir, "seomatrix", "01HFXSKIPGOOD0000000001", cliEvt("XW-seomatrix-20260701-good", "submit", "seomatrix", base))

	if bad {
		writeMirrorFile(t, dir, skippedFixtureBadRelPath,
			"---\nid: XW-seomatrix-20260701-bad\nthread: thread:seomatrix:one\nthread: thread:seomatrix:two\n---\nbad body\n")
	}
	return dir, manifest, base
}

// skipAdvisoryCase is one of the four wired verbs: a uniform NewXCommand(store)
// constructor (every one of search/inbox/outbox/thread takes exactly a
// *cache.Store) plus the args this test runs it with.
type skipAdvisoryCase struct {
	name string
	ctor func(*cache.Store) cli.Command
	args func(jsonMode bool) []string
}

func skipAdvisoryCases() []skipAdvisoryCase {
	return []skipAdvisoryCase{
		{
			name: "search",
			ctor: func(s *cache.Store) cli.Command { return cli.NewSearchCommand(s) },
			args: func(jsonMode bool) []string {
				if jsonMode {
					return []string{"", "--json"}
				}
				return []string{""}
			},
		},
		{
			name: "inbox",
			ctor: func(s *cache.Store) cli.Command { return cli.NewInboxCommand(s) },
			args: func(jsonMode bool) []string {
				if jsonMode {
					return []string{"--json"}
				}
				return nil
			},
		},
		{
			name: "outbox",
			ctor: func(s *cache.Store) cli.Command { return cli.NewOutboxCommand(s) },
			args: func(jsonMode bool) []string {
				if jsonMode {
					return []string{"--json"}
				}
				return nil
			},
		},
		{
			name: "thread",
			ctor: func(s *cache.Store) cli.Command { return cli.NewThreadCommand(s) },
			args: func(jsonMode bool) []string {
				if jsonMode {
					return []string{"--json", skippedFixtureThread}
				}
				return []string{skippedFixtureThread}
			},
		},
	}
}

// TestSkipAdvisory_FiresOnDirtySpace_StdoutByteIdentical is this phase's own
// two-in-one gate: run each of search/inbox/outbox/thread against a clean
// space and against the SAME space plus one undecodable artifact, and
// assert (a) stdout is byte-identical between the two runs (the shipped
// guarantee — tested, not assumed) and (b) the dirty run's stderr names the
// skipped path and reason while the clean run's stderr is empty (the
// "nothing on a clean space" gate — asserted in the SAME test so it cannot
// silently rot into a vacuous pass).
//
// The two runs share ONE fixture, and that is a correctness requirement, not
// a tidiness preference. The first version built two independent fixtures and
// compared their stdout: it passed in isolation and failed under a loaded
// full-package run, because the transcript's artifact `at` is derived from
// real time and the two fixtures were created a second apart. A byte-identity
// assertion across two separately-built repositories cannot hold, and what it
// would have reported is "the skip advisory changed stdout" — a false accusation
// against the code. Adding the bad file to the SAME space leaves the file
// itself as the only variable, which is exactly the property under test.
func TestSkipAdvisory_FiresOnDirtySpace_StdoutByteIdentical(t *testing.T) {
	t.Parallel()
	for _, tc := range skipAdvisoryCases() {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.name+"/json="+boolString(jsonMode), func(t *testing.T) {
				t.Parallel()

				dir, manifest, base := buildSkippedFixture(t, false)
				clock := func() time.Time { return base.Add(time.Hour) }
				newStore := func() *cache.Store {
					return cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, clock, 0)
				}

				cleanCmd := tc.ctor(newStore())
				cleanIO, cleanOut, cleanErr := newIO()
				cleanCode := cleanCmd.Run(context.Background(), tc.args(jsonMode), cleanIO)
				if cleanCode != 0 {
					t.Fatalf("%s (clean): exit code = %d, want 0; stdout=%s stderr=%s", tc.name, cleanCode, cleanOut.String(), cleanErr.String())
				}
				if cleanErr.Len() != 0 {
					t.Fatalf("%s (clean): stderr = %q, want empty — a gate that fires on a clean space is a gate people silence", tc.name, cleanErr.String())
				}

				// The ONE variable: the same space now also holds a file whose
				// frontmatter carries `thread:` twice, which is the exact
				// artifact that produced the filed defect.
				writeMirrorFile(t, dir, skippedFixtureBadRelPath,
					"---\nid: XW-seomatrix-20260701-bad\nthread: thread:seomatrix:one\nthread: thread:seomatrix:two\n---\nbad body\n")

				dirtyCmd := tc.ctor(newStore())
				dirtyIO, dirtyOut, dirtyErr := newIO()
				dirtyCode := dirtyCmd.Run(context.Background(), tc.args(jsonMode), dirtyIO)
				if dirtyCode != 0 {
					t.Fatalf("%s (dirty): exit code = %d, want 0; stdout=%s stderr=%s", tc.name, dirtyCode, dirtyOut.String(), dirtyErr.String())
				}

				if dirtyOut.String() != cleanOut.String() {
					t.Fatalf("%s: stdout diverged between the clean and dirty space (want byte-identical):\nclean=%q\ndirty=%q",
						tc.name, cleanOut.String(), dirtyOut.String())
				}

				if dirtyErr.Len() == 0 {
					t.Fatalf("%s (dirty): stderr is empty, want the skip advisory to fire", tc.name)
				}
				if !strings.Contains(dirtyErr.String(), skippedFixtureBadRelPath) {
					t.Fatalf("%s (dirty): stderr = %q, want it to NAME the skipped path %q", tc.name, dirtyErr.String(), skippedFixtureBadRelPath)
				}
				if !strings.Contains(dirtyErr.String(), "undecodable-yaml") {
					t.Fatalf("%s (dirty): stderr = %q, want the skip reason named", tc.name, dirtyErr.String())
				}

				if jsonMode {
					var payload struct {
						Skipped []cache.SkippedFile `json:"skipped"`
					}
					line := strings.TrimSpace(dirtyErr.String())
					if err := json.Unmarshal([]byte(line), &payload); err != nil {
						t.Fatalf("%s (dirty, json): stderr line did not decode as one JSON object: %v (stderr=%q)", tc.name, err, dirtyErr.String())
					}
					if len(payload.Skipped) != 1 || payload.Skipped[0].Path != skippedFixtureBadRelPath || payload.Skipped[0].Reason != "undecodable-yaml" {
						t.Fatalf("%s (dirty, json): decoded skipped = %+v, want exactly one entry naming %q/undecodable-yaml", tc.name, payload.Skipped, skippedFixtureBadRelPath)
					}
					if strings.Count(dirtyErr.String(), "\n") != 1 {
						t.Fatalf("%s (dirty, json): stderr = %q, want exactly one JSON line", tc.name, dirtyErr.String())
					}
				} else if !strings.HasPrefix(strings.TrimSpace(dirtyErr.String()), "note: ") {
					t.Fatalf("%s (dirty, human): stderr = %q, want the \"note: \" prose prefix", tc.name, dirtyErr.String())
				}
			})
		}
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestSkipAdvisory_StatuslineNeverAdvises is this phase's own explicit
// negative: `a2a statusline`'s entire output is at most one line for a
// shell prompt, so it must NEVER emit the skip advisory even over the same
// dirty fixture every other case above fires on. The call site itself
// carries the "why" as a comment (cmd_statusline.go); this is the behavioral
// proof.
func TestSkipAdvisory_StatuslineNeverAdvises(t *testing.T) {
	t.Parallel()
	dir, manifest, base := buildSkippedFixture(t, true)
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}},
		func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewStatuslineCommand(store)

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 0 && code != 10 && code != 11 {
		t.Fatalf("unexpected exit code %d", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty — statusline must never carry the skip advisory (its whole output is one prompt line)", errOut.String())
	}
}
