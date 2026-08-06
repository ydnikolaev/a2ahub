package lane

import "fmt"

// Kind is a declaration's shape (D-1).
type Kind int

const (
	// KindScoped declarations carry Inputs and claim the paths they match.
	KindScoped Kind = iota
	// KindAlways runs in every lane and CLAIMS NO PATH — a gate whose
	// verdict is not a function of the changed-file set at all (e.g. it
	// reads the whole tracked set or judges commit-subject lines). If an
	// ALWAYS gate counted as claiming, §3.3's refusal could never fire.
	KindAlways
	// KindNever is never auto-selected by the derivation. It still enters
	// the corpus (a real run_phase/Makefile-target call site) and still
	// requires a declaration, just one that opts out with a reason
	// (D-8 — live-e2e).
	KindNever
)

func (k Kind) String() string {
	switch k {
	case KindScoped:
		return "scoped"
	case KindAlways:
		return "ALWAYS"
	case KindNever:
		return "NEVER"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Declaration is one `lane-inputs:` block, parsed from wherever the D-1
// grammar allows it to live.
type Declaration struct {
	// Phase is the corpus phase name this declaration describes, e.g.
	// "readme-lint", "go-test", "web-quality", or the derived
	// "go-test-scoped:./internal/notes/..." shape for a Go package's
	// doc.go (D-2's exemption).
	Phase string
	Kind  Kind
	// Inputs is non-empty only for KindScoped. A leading "!" excludes.
	Inputs []string
	// Reason is required and non-empty for KindAlways and KindNever.
	Reason string
	// Claims is legal ONLY on a KindAlways declaration (D-10) — the paths
	// an otherwise-universal gate genuinely reads (e.g. epic-drift reads
	// docs/status.md in checks B/C, even though check A's bare `git log`
	// is what makes the whole gate ALWAYS). Coverage() counts these paths
	// as claimed; Derive() does NOT consult them — an ALWAYS declaration
	// already runs in every lane regardless of what it claims, so Claims
	// never selects the gate.
	Claims []string
	// Source is "path:line" — where the block was found.
	Source string
}

// Phase is one corpus entry — a gate that MUST carry a declaration.
type Phase struct {
	Name   string
	Source string // "Makefile:REPO_GATES" or "scripts/verify.sh:507"
	// Optional marks a phase that MAY carry a declaration but is not
	// required to: a real Makefile target outside REPO_GATES, like
	// web-quality. Those exist so a lane the repo genuinely has can be
	// declared and derived without being forced into `make check` (spec 12
	// J5 keeps the ceiling byte-identical). Required phases — REPO_GATES
	// entries and verify.sh run_phase sites — are the ones a missing
	// declaration refuses on.
	Optional bool
}

// Refusal is the spec §3.3 shape: what is wrong, and what to do about it —
// never a silent empty result.
type Refusal struct {
	// Subject is the offending path or phase/declaration name.
	Subject string
	Problem string
	Fix     string
}

func (r Refusal) String() string {
	return fmt.Sprintf("lane: %s — %s", r.Problem, r.Fix)
}

// MatchedPath is one changed path a declaration claimed, together with the
// declared Inputs entry that decided it — the "last matching pattern wins"
// rule MatchingInput applies. This is what makes "why did this phase get
// selected" answerable: the pattern, not just the path.
type MatchedPath struct {
	Path    string
	Pattern string
}

// SelectedPhase is one phase Derive selected for a changed-file set.
type SelectedPhase struct {
	Declaration Declaration
	// Matched is the changed paths that intersected this declaration's
	// Inputs, each paired with the declared pattern that matched it (empty
	// for KindAlways, which claims nothing but still runs).
	Matched  []MatchedPath
	Estimate Estimate
}

// Selection is Derive's output: the phases the diff selects.
type Selection struct {
	Phases []SelectedPhase
}
