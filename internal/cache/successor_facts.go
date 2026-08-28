package cache

// successor_facts.go — no-silent-yes-2026-08 closeout P6 auditor finding,
// ADR-019 (docs/decisions.md): MirrorResolver.Successor was BYTE-IDENTICAL
// across internal/cli/adapters.go and internal/mcp/adapters.go, and each
// surface's own membership-view helper (deleted with this move) was a
// third and fourth copy of this package's own already-existing unexported
// membershipView (mirror.go) — the exact "no cli<->mcp sharing, so this
// package carries its own copy of the same gate" reasoning ADR-019 quotes
// and retires.
// SuccessorFacts moves the substance DOWN into the one package both
// surfaces already import and neither is forbidden to import (ADR-001's
// table), the same shape AcceptanceCriteriaCount/AcceptanceCriteriaIDs/
// ParentOf (parent_criteria.go) already took for the parent-criteria
// resolution one phase earlier.

import (
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// SuccessorFacts implements the read behind validate.SuccessorResolver
// (no-silent-yes-2026-08/P6, D7/D9; extended wave 2c, D-1/D-2 — that wave's
// own report): it resolves successorID's own envelope `from` (author),
// `required_approvers` (decision only — the fact quorum arithmetic needs,
// D-1) and current folded lifecycle state — the facts internal/fold's own
// declared decision-supersede row preconditions check (table.go's
// SuccessorPrecondition). Before wave 2c this method decoded `from` alone
// and folded through a synthetic fold.Envelope with no RequiredApprovers,
// so quorumReached (fold.go) was always false and StateApproved
// (PreconditionSuccessorApproved's own target) was unreachable through
// this resolver by construction, no matter how many real approve events a
// successor actually carried — that gap is D-1.
//
// successorID is parsed with internal/artifact.ParseID purely as a
// validity guard now (an id that does not round-trip the §3.3 id grammar
// cannot name a real committed artifact, so ok=false covers it alike) —
// its System field is NOT used to scope the committed-history read: D-2's
// own fix. An `approve` event on a successor decision is committed under
// the APPROVING participant's own section, not necessarily the successor
// id's own home system's section — the subject's home system and an
// event's own committing system are two different facts, and the
// prior single-section cache.CommittedEvents(mirrorDir, id.System, ...)
// read could see the successor's own entry event but never a real
// approve authored by anyone else. CommittedEventsAllSections
// (mirrorDir/*/events/<year>/*.yaml, every participant's own section,
// subject-filtered) closes that: D-1 and D-2 compound, and either alone
// already blocked a realistic decision from ever resolving `approved`
// through this resolver.
//
// The author/kind/required_approvers read (the caller's own index gives
// Path, Thread, Digest only — never envelope fields) mirrors
// AcceptanceCriteriaCount's own established shape one function up in this
// same package (bounded read -> ParseFrontmatter -> minimal YAML probe),
// and membership is resolved through this package's own membershipView
// (mirror.go) — the SAME closure LegalityAdapter.membershipView and every
// other membership-consulting fold in this package already use, never a
// second, independently typed copy of it (ADR-019, docs/decisions.md).
//
// ok=false covers every "cannot resolve" case alike — successorID absent
// from index, its file failing to read/parse/decode/parse-as-an-id, or its
// committed history failing to read — never a synthesized author/state
// (AcceptanceCriteriaCount's own "cannot check" discipline, applied to
// this fact).
//
// Deviation from the cli/mcp methods this replaced: the file re-read below
// uses this package's own bounded readBounded (mirror.go,
// maxCacheReadBytes, 1 MiB) rather than each surface's former
// readBoundedFile/maxMirrorEventBytes pair — same 1 MiB bound, same
// os.Open/io.LimitReader shape, only the error-message prefix differs, and
// that message is never observed (a read failure alone collapses to
// ok=false here). AcceptanceCriteriaCount's own doc comment above notes the
// same class of deviation (bounded read replacing a surface-local helper)
// for its own move.
func SuccessorFacts(dir string, index map[string]ArtifactIndexEntry, manifest space.Manifest, successorID string) (author, state string, ok bool) {
	entry, found := index[successorID]
	if !found {
		return "", "", false
	}
	raw, err := readBounded(filepath.Join(dir, filepath.FromSlash(entry.Path)), maxCacheReadBytes)
	if err != nil {
		return "", "", false
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return "", "", false
	}
	var probe struct {
		Type              string   `yaml:"type"`
		From              string   `yaml:"from"`
		RequiredApprovers []string `yaml:"required_approvers"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return "", "", false
	}
	if _, err := artifact.ParseID(successorID); err != nil {
		return "", "", false
	}
	events, err := CommittedEventsAllSections(dir, successorID)
	if err != nil {
		return "", "", false
	}
	kind := fold.Kind(probe.Type)
	prior := fold.NewResult(kind)
	if len(events) > 0 {
		env := fold.Envelope{ID: successorID, Kind: kind, From: probe.From, RequiredApprovers: probe.RequiredApprovers}
		prior = fold.Fold(kind, env, events, membershipView(manifest))
	}
	return probe.From, string(prior.State), true
}

// SuccessorFactsForCandidate is the GATE around SuccessorFacts above, and
// it moved down for the same reason and in the same wave — the closeout
// auditor found the read duplicated, and the implementer that moved the
// read reported that its gate was duplicated too, one frame out, in a
// shape the ADR-019 detection gate cannot see.
//
// `internal/cli/cmd_lifecycle.go`'s lifecycleResolveSuccessorFacts and
// `internal/mcp/eventdoc.go`'s resolveSuccessorFacts were structurally
// identical: the same three-clause guard, the same fresh resolver, the same
// *fold.SuccessorFacts construction. Their ONLY real difference was the
// per-surface ref-entry type they unpacked (`[]lifecycleRefEntry` vs
// `[]refEntry`), which is why this signature takes the already-extracted
// successor ref as a plain string: the surface keeps its own type, the RULE
// lives once.
//
// The guard is a COST optimization, not a correctness requirement, and the
// distinction is load-bearing. Every fold row but the two decision-supersede
// rows carries PreconditionNone and never consults these facts
// (preconditionTable, fold/table.go), and CheckCandidateWithSuccessor's own
// dispatch ignores successor facts for such a row — so skipping the mirror
// read here changes cost, never a verdict.
//
// A nil return is D9's own "unresolved" case, NEVER a granted negative:
// CheckCandidateWithSuccessor refuses a Precondition-bearing row uniformly
// rather than silently granting when resolution fails. That is the whole
// point of the epic this function was written in — a capability miss must
// not read as a resolved answer.
//
// successorRef == "" means the caller had no ref to offer (an empty refs
// list on either surface), which resolves to nil by the same rule.
func SuccessorFactsForCandidate(dir string, manifest space.Manifest, kind fold.Kind, transition, successorRef string) *fold.SuccessorFacts {
	if transition != fold.TSupersede || kind != fold.KindDecision || successorRef == "" {
		return nil
	}
	index, _, err := BuildArtifactIndex(dir)
	if err != nil {
		// Best-effort index build, the same degradation
		// cli/mcp MirrorResolver.ensureIndex already applies: a walk-root
		// failure yields an empty index, and an empty index yields
		// ok=false below — "could not check", never a synthesized fact.
		return nil
	}
	author, state, ok := SuccessorFacts(dir, index, manifest, successorRef)
	if !ok {
		return nil
	}
	return &fold.SuccessorFacts{Author: author, State: fold.State(state)}
}
