package agentid

import (
	"sort"
	"strings"
	"testing"
)

// envMap turns a table row into a Lookup. Detection is tested entirely through
// this rather than through the process environment, so every case is parallel
// -safe and none of them depends on the machine the suite runs on.
func envMap(kv map[string]string) Lookup {
	return func(k string) string { return kv[k] }
}

func TestDetect(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		env  map[string]string
		want Agent
		ok   bool
	}{
		{
			// The environment read off a real Claude Code session, which is
			// the only reason this detector exists in the shape it does.
			name: "claude code, full environment",
			env: map[string]string{
				"CLAUDECODE":               "1",
				"CLAUDE_CODE_ENTRYPOINT":   "cli",
				"CLAUDE_CODE_EXECPATH":     "/Users/x/.local/share/claude/versions/2.1.223",
				"CLAUDE_CODE_EFFORT_LEVEL": "high",
				"CLAUDE_CODE_SESSION_ID":   "1493423a-96af",
			},
			want: Agent{ID: "claude-code", Version: "2.1.223", Effort: "high", Session: "1493423a-96af"},
			ok:   true,
		},
		{
			// CLAUDECODE is absent but the entrypoint survives — the re-exec
			// case the two-marker check exists for.
			name: "claude code, entrypoint only",
			env:  map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"},
			want: Agent{ID: "claude-code"},
			ok:   true,
		},
		{
			// A non-version install directory must yield no version rather
			// than the directory's name.
			name: "claude code, unversioned install path",
			env: map[string]string{
				"CLAUDECODE":           "1",
				"CLAUDE_CODE_EXECPATH": "/opt/homebrew/bin/claude",
			},
			want: Agent{ID: "claude-code"},
			ok:   true,
		},
		{
			name: "explicit override outranks a running detector",
			env: map[string]string{
				"CLAUDECODE":      "1",
				EnvOverride:       "codex",
				"A2A_ACTOR_MODEL": "gpt-5",
			},
			want: Agent{ID: "codex", Model: "gpt-5"},
			ok:   true,
		},
		{
			// An operator naming a tool this build does not know is honoured
			// verbatim. Dropping it would record nothing at all, which is
			// strictly worse than recording a name with no icon.
			name: "unregistered override is honoured verbatim",
			env:  map[string]string{EnvOverride: "SomeNewAgent"},
			want: Agent{ID: "somenewagent"},
			ok:   true,
		},
		{
			name: "generic AI_AGENT hint",
			env:  map[string]string{"AI_AGENT": "claude-code_2-1-223_agent"},
			want: Agent{ID: "claude-code", Version: "2.1.223"},
			ok:   true,
		},
		{
			// The variable's format was observed on one machine, not read
			// from a spec. An unrecognized leading segment must produce no
			// detection rather than an invented id.
			name: "generic hint naming nothing in the registry",
			env:  map[string]string{"AI_AGENT": "some-other-tool_1-0-0_agent"},
			ok:   false,
		},
		{
			name: "a human at a terminal is not a failure",
			env:  map[string]string{"USER": "ydnikolaev", "TERM": "xterm"},
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Detect(envMap(tc.env))
			if ok != tc.ok {
				t.Fatalf("Detect ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Fatalf("Detect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDetectWithNoLookupIsNotAPanic pins the nil case explicitly: a caller
// that has not wired an environment gets "no agent", never a crash inside a
// write path that is already committing to a shared log.
func TestDetectWithNoLookupIsNotAPanic(t *testing.T) {
	t.Parallel()
	if _, ok := Detect(nil); ok {
		t.Fatal("Detect(nil) claimed to have identified an agent")
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "claude-code", want: "claude-code", ok: true},
		{in: "claude", want: "claude-code", ok: true}, // alias
		{in: "  Codex  ", want: "codex", ok: true},    // trimmed and folded
		{in: "COPILOT", want: "github-copilot", ok: true},
		{in: "junie", want: "jetbrains", ok: true},

		// The events already committed to the getvisa space record exactly
		// this, and it is the whole reason the legacy read exists.
		{in: "codex", want: "codex", ok: true},

		// Matching is exact after folding, never prefix or substring. These
		// are people's logins; a surface that treats them as agents
		// attributes a person's act to a machine.
		{in: "codexal", ok: false},
		{in: "yuranikolaev", ok: false},
		{in: "ydnikolaev", ok: false},
		{in: "xpressmike", ok: false},
		{in: "claude-code-user", ok: false},
		{in: "", ok: false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := Normalize(tc.in)
			if ok != tc.ok {
				t.Fatalf("Normalize(%q) ok = %v, want %v (got %q)", tc.in, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRegistryKeysAreUnambiguous is the registry's own coherence gate.
//
// Two rows claiming the same id, or an alias that collides with another row's
// id, would make Normalize's answer depend on map iteration and slice order —
// a silent, intermittent misattribution rather than a visible failure. The
// map is built once at init, so nothing else would ever report it.
func TestRegistryKeysAreUnambiguous(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	claim := func(key, by string) {
		t.Helper()
		if key != strings.ToLower(strings.TrimSpace(key)) {
			t.Fatalf("registry key %q is not already normalized; Normalize folds its input, so this key is unreachable", key)
		}
		if prev, dup := seen[key]; dup {
			t.Fatalf("registry key %q is claimed by both %q and %q", key, prev, by)
		}
		seen[key] = by
	}
	for _, e := range registry {
		if e.id == "" || e.display == "" || e.provider == "" {
			t.Fatalf("registry row %+v leaves id, display or provider empty", e)
		}
		claim(e.id, e.id)
		for _, alias := range e.aliases {
			claim(alias, e.id)
		}
	}

	ids := IDs()
	if len(ids) != len(registry) {
		t.Fatalf("IDs() returned %d entries for %d registry rows", len(ids), len(registry))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("IDs() is not sorted; the icon-coverage gate diffs it against a sorted list")
	}
}

// TestProviderRefusesToGuess pins the empty answer for an unknown id. A
// display surface would rather show nothing than invent an organization to
// attribute an act to.
func TestProviderRefusesToGuess(t *testing.T) {
	t.Parallel()
	if got := Provider("no-such-agent"); got != "" {
		t.Fatalf("Provider(unknown) = %q, want empty", got)
	}
	if got := Provider("claude-code"); got != "Anthropic" {
		t.Fatalf("Provider(claude-code) = %q, want Anthropic", got)
	}
	// Display falls back to the id so an unknown value renders as itself.
	if got := Display("no-such-agent"); got != "no-such-agent" {
		t.Fatalf("Display(unknown) = %q, want the id back", got)
	}
}

// TestContradictsOnlyFiresOnARivalAgent is the boundary that the first version
// of this rule did not have, written from what its absence cost.
//
// Detection beat every caller-supplied name outright. The conformance harness
// writes as `live-e2e` and then looks the operation up by that name; every
// lookup missed, each burning 18 visibility attempts at 5s, and the logic tier
// went from ~5 minutes to past a 20-minute timeout. The harness was using the
// flag correctly — the rule was too wide.
func TestContradictsOnlyFiresOnARivalAgent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, claimed, detected string
		want                    bool
	}{
		// The defect this whole package exists for: a caller naming a
		// DIFFERENT agent than the one executing.
		{name: "rival agent id", claimed: "codex", detected: "claude-code", want: true},
		{name: "rival via alias", claimed: "copilot", detected: "claude-code", want: true},

		// Not rivals. Each of these names something that is not an agent, so
		// the caller is making a different kind of claim entirely.
		{name: "conformance harness fixture", claimed: "live-e2e", detected: "claude-code"},
		{name: "the responder fixture it looks up by", claimed: "a2a-live-e2e-responder", detected: "claude-code"},
		{name: "an operational scenario fixture", claimed: "oc-contract", detected: "claude-code"},
		{name: "a person", claimed: "ydnikolaev", detected: "claude-code"},
		{name: "a service account", claimed: "ci-bot", detected: "claude-code"},

		// Agreeing with the environment is not a contradiction.
		{name: "same agent", claimed: "claude-code", detected: "claude-code"},
		{name: "same agent via alias", claimed: "claude", detected: "claude-code"},

		// Nothing claimed, or nothing detected.
		{name: "empty claim", claimed: "", detected: "claude-code"},
		{name: "nothing detected", claimed: "codex", detected: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Contradicts(tc.claimed, tc.detected); got != tc.want {
				t.Fatalf("Contradicts(%q, %q) = %v, want %v", tc.claimed, tc.detected, got, tc.want)
			}
		})
	}
}
