package validate

import "testing"

// TestCheckParentResolves is computed-not-listed-2026-08 P7's own new
// test, spec §8 rows 4/5/6: REF-003, widened onto `parent`
// (registry.yaml's own applies_to row), fires for an unresolvable parent
// and reports SeverityUnmeasured — never a silent clean verdict — when no
// resolver was supplied at all. A new file rather than an edit to
// thread_test.go: thread_test.go pins the PRE-fix behavior this phase
// deliberately overturns (see this package's own checkFork doc comment),
// and thread_test.go is outside this phase's allowlist — see this
// package's phase report for the two pre-existing thread_test.go subtests
// this change reds, named exactly, with the reasoning for why they now
// assert a superseded expectation rather than a live one.
func TestCheckParentResolves(t *testing.T) {
	t.Parallel()

	knownResolver := &fakeThreadResolver{known: map[string]bool{"XR-axon-known-parent": true}}

	cases := []struct {
		name     string
		env      envelope
		resolver Resolver
		want     string // "" for none, else the expected Code
		severity Severity
	}{
		{
			name:     "no parent named: nothing to resolve",
			env:      envelope{},
			resolver: knownResolver,
			want:     "",
		},
		{
			name:     "parent resolves: no violation",
			env:      envelope{Parent: "XR-axon-known-parent"},
			resolver: knownResolver,
			want:     "",
		},
		{
			name:     "parent resolves to nothing: REF-003 reject",
			env:      envelope{Parent: "XR-axon-does-not-exist-anywhere"},
			resolver: knownResolver,
			want:     "REF-003",
			severity: SeverityReject,
		},
		{
			name:     "no resolver at all: unmeasurable, not clean",
			env:      envelope{Parent: "XR-axon-known-parent"},
			resolver: nil,
			want:     "REF-003",
			severity: SeverityUnmeasured,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkParentResolves(tc.env, tc.resolver)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("checkParentResolves(%+v) = %+v, want none", tc.env, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("checkParentResolves(%+v) = %+v, want exactly one violation", tc.env, got)
			}
			if got[0].Code != tc.want {
				t.Fatalf("Code = %q, want %q", got[0].Code, tc.want)
			}
			if got[0].Severity != tc.severity {
				t.Fatalf("Severity = %q, want %q", got[0].Severity, tc.severity)
			}
			if got[0].Class != ClassReferential {
				t.Fatalf("Class = %q, want referential", got[0].Class)
			}
			if got[0].Path != "parent" {
				t.Fatalf("Path = %q, want %q", got[0].Path, "parent")
			}
		})
	}
}

// TestCheckFork_WrapsCheckParentResolves is the composition half: checkFork
// (the one call site engine.go actually reaches) surfaces
// checkParentResolves' own violation rather than silently swallowing it —
// the exact silent-degrade §0.5's "measured facts" section reproduced with
// the built binary, closed at the unit level here.
func TestCheckFork_WrapsCheckParentResolves(t *testing.T) {
	t.Parallel()

	t.Run("unresolvable parent: checkFork reports REF-003, not silence", func(t *testing.T) {
		t.Parallel()
		resolver := &fakeThreadResolver{known: map[string]bool{}}
		env := envelope{Parent: "XR-axon-does-not-exist-anywhere", Thread: "thread:axon-20260830-a1b2"}
		got := checkFork(env, resolver)
		if !hasCode(got, "REF-003") {
			t.Fatalf("checkFork(%+v) = %+v, want REF-003", env, got)
		}
	})

	t.Run("nil resolver: checkFork reports unmeasured, not silence", func(t *testing.T) {
		t.Parallel()
		env := envelope{Parent: "XR-axon-known-parent", Thread: "thread:axon-20260830-a1b2"}
		got := checkFork(env, nil)
		if len(got) != 1 || got[0].Code != "REF-003" || got[0].Severity != SeverityUnmeasured {
			t.Fatalf("checkFork(%+v, nil) = %+v, want a single REF-003 unmeasured violation", env, got)
		}
	})

	t.Run("resolved parent, agreeing threads: checkFork stays silent (unaffected by the widening)", func(t *testing.T) {
		t.Parallel()
		resolver := &fakeThreadResolver{
			known:   map[string]bool{"XR-axon-known-parent": true},
			threads: map[string]string{"XR-axon-known-parent": "thread:axon-20260830-a1b2"},
		}
		env := envelope{Parent: "XR-axon-known-parent", Thread: "thread:axon-20260830-a1b2"}
		got := checkFork(env, resolver)
		if len(got) != 0 {
			t.Fatalf("checkFork(%+v) = %+v, want none", env, got)
		}
	})
}
