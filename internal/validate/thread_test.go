package validate

import "testing"

// fakeThreadResolver is a hand-written mock implementing BOTH Resolver and
// the optional ThreadResolver capability (thread.go) — the shape a
// concrete cache-backed Resolver is expected to satisfy once it wires
// thread lookups. Kept separate from registry_test.go's fakeResolver
// (which does NOT implement ThreadResolver, by design: several subtests
// below pass a plain *fakeResolver to exercise exactly that "capability
// absent" degradation).
type fakeThreadResolver struct {
	known   map[string]bool
	threads map[string]string // artifact id -> its thread
	exists  map[string]bool   // thread value -> already carried by some artifact
}

func (f *fakeThreadResolver) KnownArtifact(id string) bool { return f.known[id] }
func (f *fakeThreadResolver) Digest(string) (string, bool) { return "", false }
func (f *fakeThreadResolver) System(string) (bool, bool)   { return false, false }
func (f *fakeThreadResolver) ThreadOf(id string) (string, bool) {
	t, ok := f.threads[id]
	return t, ok
}
func (f *fakeThreadResolver) ThreadExists(thread string) bool { return f.exists[thread] }

// TestCheckFork is REF-009: table-driven over the fork/vacuity matrix.
func TestCheckFork(t *testing.T) {
	t.Parallel()

	baseResolver := &fakeThreadResolver{
		known: map[string]bool{
			"XR-axon-country-vocabulary": true,
			"XR-axon-threadless-parent":  true,
		},
		threads: map[string]string{
			"XR-axon-country-vocabulary": "thread:axon-20260729-c7q2",
			"XR-axon-threadless-parent":  "", // parent resolves but carries no thread
		},
	}

	cases := []struct {
		name      string
		env       envelope
		resolver  Resolver
		wantCode  bool
		wantEmpty bool
	}{
		{
			name: "fork: thread differs from resolved parent's thread",
			env: envelope{
				Parent: "XR-axon-country-vocabulary",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver: baseResolver,
			wantCode: true,
		},
		{
			name: "agreement: thread matches resolved parent's thread",
			env: envelope{
				Parent: "XR-axon-country-vocabulary",
				Thread: "thread:axon-20260729-c7q2",
			},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name:      "vacuous: no thread and no parent (the entire existing corpus)",
			env:       envelope{},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name: "vacuous: parent set, own thread empty",
			env: envelope{
				Parent: "XR-axon-country-vocabulary",
			},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name: "vacuous: own thread set, no parent",
			env: envelope{
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name: "vacuous: parent resolves but is itself threadless",
			env: envelope{
				Parent: "XR-axon-threadless-parent",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name: "vacuous: parent unresolvable (REF-003's territory, not REF-009's)",
			env: envelope{
				Parent: "XR-axon-does-not-exist",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  baseResolver,
			wantEmpty: true,
		},
		{
			name: "vacuous: nil resolver",
			env: envelope{
				Parent: "XR-axon-country-vocabulary",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  nil,
			wantEmpty: true,
		},
		{
			name: "vacuous: resolver lacks the ThreadResolver capability",
			env: envelope{
				Parent: "XR-axon-country-vocabulary",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  &fakeResolver{known: map[string]bool{"XR-axon-country-vocabulary": true}},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkFork(tc.env, tc.resolver)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("checkFork(%+v) = %+v, want none", tc.env, got)
				}
				return
			}
			if tc.wantCode && !hasCode(got, "REF-009") {
				t.Fatalf("checkFork(%+v) = %+v, want REF-009", tc.env, got)
			}
			for _, v := range got {
				if v.Severity != SeverityReject {
					t.Fatalf("REF-009 severity = %q, want reject", v.Severity)
				}
				if v.Class != ClassReferential {
					t.Fatalf("REF-009 class = %q, want referential", v.Class)
				}
			}
		})
	}
}

// TestCheckForeignMint is REF-010: a foreign mint (grammar-invalid or
// wrong <system>) is rejected; a same-system mint and an already-existing
// thread are both accepted.
func TestCheckForeignMint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		env       envelope
		resolver  Resolver
		wantCode  bool
		wantEmpty bool
	}{
		{
			name: "foreign mint: grammar-valid but <system> != from",
			env: envelope{
				From:   "axon",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver: &fakeThreadResolver{exists: map[string]bool{}},
			wantCode: true,
		},
		{
			name: "foreign mint: grammar-invalid (typo in someone else's thread id)",
			env: envelope{
				From:   "axon",
				Thread: "thread:not-a-valid-shape",
			},
			resolver: &fakeThreadResolver{exists: map[string]bool{}},
			wantCode: true,
		},
		{
			name: "legal mint: grammar-valid and <system> == from",
			env: envelope{
				From:   "axon",
				Thread: "thread:axon-20260805-b6n2",
			},
			resolver:  &fakeThreadResolver{exists: map[string]bool{}},
			wantEmpty: true,
		},
		{
			name: "already exists: accepted regardless of system or shape",
			env: envelope{
				From:   "axon",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  &fakeThreadResolver{exists: map[string]bool{"thread:seomatrix-20260805-b6n2": true}},
			wantEmpty: true,
		},
		{
			name:      "vacuous: no thread",
			env:       envelope{From: "axon"},
			resolver:  &fakeThreadResolver{},
			wantEmpty: true,
		},
		{
			name: "vacuous: nil resolver (existence unknowable)",
			env: envelope{
				From:   "axon",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  nil,
			wantEmpty: true,
		},
		{
			name: "vacuous: resolver lacks the ThreadResolver capability (existence unknowable)",
			env: envelope{
				From:   "axon",
				Thread: "thread:seomatrix-20260805-b6n2",
			},
			resolver:  &fakeResolver{},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkForeignMint(tc.env, tc.resolver)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("checkForeignMint(%+v) = %+v, want none", tc.env, got)
				}
				return
			}
			if tc.wantCode && !hasCode(got, "REF-010") {
				t.Fatalf("checkForeignMint(%+v) = %+v, want REF-010", tc.env, got)
			}
			for _, v := range got {
				if v.Severity != SeverityReject {
					t.Fatalf("REF-010 severity = %q, want reject", v.Severity)
				}
			}
		})
	}
}

// TestCheckSupersedeThreadContinuity is REF-012: a warning, never a
// reject, fired only when both sides are known and differ.
func TestCheckSupersedeThreadContinuity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                               string
		successorThread, predecessorThread string
		wantWarning                        bool
	}{
		{
			name:              "same thread: no violation",
			successorThread:   "thread:axon-20260805-b6n2",
			predecessorThread: "thread:axon-20260805-b6n2",
			wantWarning:       false,
		},
		{
			name:              "different thread: warns",
			successorThread:   "thread:axon-20270101-a1a1",
			predecessorThread: "thread:axon-20260805-b6n2",
			wantWarning:       true,
		},
		{
			name:              "vacuous: successor empty",
			successorThread:   "",
			predecessorThread: "thread:axon-20260805-b6n2",
			wantWarning:       false,
		},
		{
			name:              "vacuous: predecessor empty",
			successorThread:   "thread:axon-20260805-b6n2",
			predecessorThread: "",
			wantWarning:       false,
		},
		{
			name:              "vacuous: both empty",
			successorThread:   "",
			predecessorThread: "",
			wantWarning:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkSupersedeThreadContinuity(tc.successorThread, tc.predecessorThread)
			if !tc.wantWarning {
				if len(got) != 0 {
					t.Fatalf("checkSupersedeThreadContinuity(%q, %q) = %+v, want none", tc.successorThread, tc.predecessorThread, got)
				}
				return
			}
			if !hasCode(got, "REF-012") {
				t.Fatalf("checkSupersedeThreadContinuity(%q, %q) = %+v, want REF-012", tc.successorThread, tc.predecessorThread, got)
			}
			for _, v := range got {
				if v.Severity != SeverityWarning {
					t.Fatalf("REF-012 severity = %q, want warning", v.Severity)
				}
			}
		})
	}
}
