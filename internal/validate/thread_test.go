package validate

import "testing"

// forkedResponseBody is a schema-valid response/v1 artifact whose own
// `thread` deliberately differs from its parent's — the exact fork shape
// TestCheckFork's "fork: thread differs from resolved parent's thread"
// case exercises directly against checkFork, reused here to exercise the
// SAME fork through the actual V2 entry point, engine.ValidateForSubmit.
const forkedResponseBody = "---\n" + `
schema: envelope/v1
id: XS-seomatrix-20260805-b6n2
type: response
title: Country vocabulary delivered
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude}
created: 2026-08-05T10:30:00Z
priority: p3
blocking: false
parent: XR-axon-country-vocabulary
result: delivered
thread: thread:seomatrix-20260805-b6n2
classification: internal
` + "---\nBody text.\n"

// foreignMintBody is a schema-valid work_request/v1 artifact whose
// `thread` is grammar-valid but minted under a DIFFERENT system than its
// own `from` — REF-010's foreign-mint shape — and names no artifact any
// resolver below knows about.
const foreignMintBody = "---\n" + `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: A valid work request
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without normalization."
acceptance_criteria:
  - "Every code exists in the registry."
thread: thread:seomatrix-20260805-b6n2
classification: internal
` + "---\nBody.\n"

// TestValidateForSubmitComposesThreadChecks is the composition teeth: a
// unit test against checkFork/checkForeignMint directly (above) cannot
// distinguish a working guard from one wired to nothing, because a
// fail-open guard and a clean call both return zero violations. This
// test drives the actual V2 entry point, engine.ValidateForSubmit, so
// that "REF-009/REF-010 fire" and "nothing calls checkFork/
// checkForeignMint at all" are observably different outcomes.
func TestValidateForSubmitComposesThreadChecks(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	t.Run("resolver implements ThreadResolver: forked document is REF-009-rejected", func(t *testing.T) {
		t.Parallel()
		resolver := &fakeThreadResolver{
			known:   map[string]bool{"XR-axon-country-vocabulary": true},
			threads: map[string]string{"XR-axon-country-vocabulary": "thread:axon-20260729-c7q2"},
		}
		result, err := engine.ValidateForSubmit(
			Draft{Path: "seomatrix/exchanges/XS-seomatrix-20260805-b6n2.md", Raw: []byte(forkedResponseBody)},
			nil,
			LocalContext{OwnSystem: "seomatrix", Resolver: resolver},
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if !hasCode(result.Violations, "REF-009") {
			t.Fatalf("expected REF-009 among violations when the resolver implements ThreadResolver, got %+v", result.Violations)
		}
	})

	t.Run("resolver implements ThreadResolver: foreign-mint thread is REF-010-rejected", func(t *testing.T) {
		t.Parallel()
		resolver := &fakeThreadResolver{exists: map[string]bool{}}
		result, err := engine.ValidateForSubmit(
			Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: []byte(foreignMintBody)},
			nil,
			LocalContext{OwnSystem: "axon", Resolver: resolver},
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if !hasCode(result.Violations, "REF-010") {
			t.Fatalf("expected REF-010 among violations when the resolver implements ThreadResolver, got %+v", result.Violations)
		}
	})

	// This is the documented LIMITATION, not a bug: thread.go's own doc
	// comments on checkFork/checkForeignMint spell out why an absent
	// ThreadResolver capability degrades to "no violation" rather than
	// firing on incomplete information (parent-thread and thread-
	// existence are both genuinely unknowable through the narrower
	// Resolver alone). Pinning it here means a REGRESSION that makes the
	// guard fail-open on a resolver that DOES implement ThreadResolver
	// would be caught by the two subtests above, while a caller that
	// hands ValidateForSubmit a plain Resolver still gets exactly this,
	// on purpose.
	t.Run("resolver lacks ThreadResolver: the same forked document produces NO thread violation (documented fail-open)", func(t *testing.T) {
		t.Parallel()
		resolver := &fakeResolver{known: map[string]bool{"XR-axon-country-vocabulary": true}}
		result, err := engine.ValidateForSubmit(
			Draft{Path: "seomatrix/exchanges/XS-seomatrix-20260805-b6n2.md", Raw: []byte(forkedResponseBody)},
			nil,
			LocalContext{OwnSystem: "seomatrix", Resolver: resolver},
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if hasCode(result.Violations, "REF-009") {
			t.Fatalf("expected NO REF-009 when the resolver does not implement ThreadResolver (fail-open is deliberate), got %+v", result.Violations)
		}
		if hasCode(result.Violations, "REF-010") {
			t.Fatalf("expected NO REF-010 either, got %+v", result.Violations)
		}
	})
}

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
