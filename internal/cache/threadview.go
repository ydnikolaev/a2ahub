package cache

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

// Spec 46 ("threads that hold") §T3/§T4 — the read model that replaces
// Store.Thread's flat, cross-space, LatestEventAt-ordered artifact list
// (D2/D3/D4/D6, store.go:450-475) with a per-space, commit-sequenced
// transcript of artifacts AND events plus an explicit "whose move is it"
// block. Store.Thread itself is left in place unchanged — its two
// callers (internal/cli, internal/mcp) were outside that wave; see v1-min
// spec 46 §11.

const (
	// ThreadOrderCommitted is set when the ordering rests on the space's
	// per-commit first-parent sequence (D-017/D-026) — the guarantee that
	// provably matches the fold beside it (§T3).
	ThreadOrderCommitted = "committed"
	// ThreadOrderDeclared is set when commitOrder returned no history
	// (unreadable/absent git log) and the reader fell back to the
	// envelope's own `created`/event's own `at` fields — a WEAKER,
	// author-supplied ordering, surfaced rather than silently substituted
	// (§T3 "Degradation is designed, not silent").
	ThreadOrderDeclared = "declared"

	// TranscriptKindDerived marks a transcript row that is NOT a git-
	// committed fact of THIS space — it is assembled from a counterparty's
	// own consumes.yaml registry (a contract adoption). Every other kind
	// ("artifact", "event") corresponds to something this space's own git
	// history actually recorded; a "derived" row does not, and a reader
	// checking the JSON `kind` field (not merely the surrounding page's
	// styling) must be able to tell the two apart — see this const's own
	// caller, buildTranscript, and the operator report that prompted it:
	// a counterparty's adoption was otherwise invisible in the thread
	// ("я как будто сам с собой разговариваю").
	TranscriptKindDerived = "derived"
)

// ErrThreadNotFound is returned by ThreadView when threadID (whether
// supplied directly or resolved from a member artifact id) matches no
// artifact in any space ThreadView searched — D6's "an unknown id exits 0
// with an empty list, indistinguishable from a real empty thread" closed
// for the bare thread-id case (ErrNotFound already closes it for the
// any-member/artifact-id case, reused below). Not spec-mandated by name;
// this package's own choice, consistent with every other "never a silent
// empty success" rule this phase enforces — see Deviations. When --space
// was supplied this error does NOT mean "no such thread anywhere": it may
// exist in a different connected space the caller did not ask about.
var ErrThreadNotFound = fmt.Errorf("cache: thread not found")

// ThreadOpener is the COMPUTED lowest-seq (or, in declared-order mode,
// earliest-created) member of a thread — never a declared root (§T3).
type ThreadOpener struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	From  string `json:"from"`
}

// ThreadRef is one artifact's `refs[]` entry as rendered in ThreadResult.
// Ref digest verdicts are deliberately NOT resolved here (§T3 "Not
// included" — `a2a show` owns that); this is the bare grammar string.
type ThreadRef struct {
	Ref string `json:"ref"`
}

// ThreadArtifact is one member's projection in ThreadResult.Artifacts —
// current folded state lives HERE and in OpenItems, never duplicated onto
// a TranscriptEntry (§T3 "one state per fact").
type ThreadArtifact struct {
	ID     string      `json:"id"`
	Type   string      `json:"type"`
	From   string      `json:"from"`
	To     []string    `json:"to,omitempty"`
	Title  string      `json:"title"`
	State  string      `json:"state"`
	Parent string      `json:"parent,omitempty"`
	Refs   []ThreadRef `json:"refs,omitempty"`
}

// TranscriptArtifact is a transcript entry's artifact-kind payload — a
// deliberately minimal projection (no state: see ThreadArtifact's doc).
type TranscriptArtifact struct {
	ID    string                `json:"id"`
	Type  string                `json:"type"`
	From  string                `json:"from"`
	To    []string              `json:"to,omitempty"`
	Title string                `json:"title"`
	Work  *TranscriptWorkReport `json:"work,omitempty"`
}

// TranscriptWorkReport is the durable semantic payload carried by a status
// announcement. It remains attached to the announcement's committed
// transcript position so human-facing readers can render one work-history row
// instead of the technical announcement + publish pair. Local leases never
// enter this type.
type TranscriptWorkReport struct {
	ArtifactID     string                 `json:"artifact_id"`
	WorkID         string                 `json:"work_id"`
	SubjectRef     string                 `json:"subject_ref"`
	Mode           workreport.Mode        `json:"mode"`
	Summary        string                 `json:"summary"`
	Actor          TranscriptEventActor   `json:"actor"`
	WaitingOn      []workreport.WaitingOn `json:"waiting_on,omitempty"`
	ReportedAt     time.Time              `json:"reported_at"`
	ValidUntil     time.Time              `json:"valid_until,omitempty"`
	CommitSequence uint64                 `json:"commit_sequence"`
}

// TranscriptEventActor is one event's actor block, as rendered in a
// transcript entry.
type TranscriptEventActor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// TranscriptEvent is a transcript entry's event-kind payload.
type TranscriptEvent struct {
	ULID         string               `json:"ulid"`
	Subject      string               `json:"subject"`
	Transition   string               `json:"transition"`
	ClaimedState string               `json:"claimed_state,omitempty"`
	Actor        TranscriptEventActor `json:"actor"`
	ProducedBy   provenance.Producer  `json:"produced_by,omitzero"`
	Consistency  *ReceiptMismatch     `json:"consistency,omitempty"`
	// ResponseID is set only on a `respond` event (D-024's newly attached
	// response id).
	ResponseID string `json:"response_id,omitempty"`
	// Version is the event's own §5.2.2 `version` field, carried so a reader
	// can tell one contract publication from the next.
	//
	// Without it, a contract with three `publish` events renders three
	// IDENTICAL transcript rows — "published the contract", three times — and
	// nothing on the surface says which version each one created, even though
	// fold builds Result.Versions from precisely this field. Observed on a
	// real space: XC-axon-getvisa-ingest carries three publishes and two
	// recorded versions, rendered as three indistinguishable lines whose links
	// all resolve to the version-less contract id.
	//
	// Empty on every transition that does not carry one, which is most of
	// them — this is an envelope-version fact, not a lifecycle fact.
	Version string `json:"version,omitempty"`
	Note    string `json:"note,omitempty"`
	// ReasonCode is the event's machine-readable reason — schemas/event/v1
	// and /v2 both define it, and the MCP `decline` tool requires it
	// (tools_lifecycle.go's RequireReasonCode). Until P0 this package's own
	// eventProbe did not decode it, so the one field an agent is REQUIRED to
	// supply when refusing reached no reader at all: the prose `note` beside
	// it was the only thing a receiver could act on, which is exactly the
	// substitution the code was introduced to end.
	//
	// Carried here rather than deferred to P6 because P6 builds
	// `blocked_by.reason_code` on this vocabulary, and a vocabulary that
	// reaches no reader cannot be built on.
	ReasonCode string `json:"reason_code,omitempty"`
}

// TranscriptDerivedAdoption is a transcript entry's derived-kind payload: one
// counterparty's registered adoption of a contract this thread carries,
// read from THAT system's own committed consumes.yaml (never synthesised —
// see TranscriptKindDerived). ContractID names which rendered thread member
// was adopted, the same role Event.Subject plays for an event row.
type TranscriptDerivedAdoption struct {
	ContractID string `json:"contract_id"`
	System     string `json:"system"`
	Major      int    `json:"major"`
	// Since is the adopting system's own consumes.yaml `since:` date,
	// verbatim (YYYY-MM-DD). Omitted — never invented, never "now" — when
	// that registry entry carries no `since:` at all; see buildTranscript's
	// own doc comment for how such a row is still rendered, never dropped,
	// and where it lands without a real ordering key.
	Since string `json:"since,omitempty"`
}

// TranscriptEntry is ONE strictly seq-ordered transcript row — a
// discriminated union (Kind = "artifact" | "event" | "derived"), never two
// lists a reader must interleave mentally (§T3). "derived" (see
// TranscriptKindDerived) is the one kind that is not itself a fact this
// space's own git history recorded.
type TranscriptEntry struct {
	Seq      int64                      `json:"seq"`
	Kind     string                     `json:"kind"`
	At       time.Time                  `json:"at"`
	Artifact *TranscriptArtifact        `json:"artifact,omitempty"`
	Event    *TranscriptEvent           `json:"event,omitempty"`
	Derived  *TranscriptDerivedAdoption `json:"derived,omitempty"`
}

// NextAction is one legal transition and the SYSTEM IDS (never role
// names) that may make it — fold.LegalNext's Role resolved against the
// correct envelope (§T3's own "highest-stakes step": for a response
// member, the PARENT's envelope, never the response's own).
type NextAction struct {
	Transition string   `json:"transition"`
	By         []string `json:"by"`
}

// OpenItem answers "whose move is it" from facts for one non-terminal
// thread member (§T3). WaitingOn/YourMove/ExpectedTransition/Why are
// sourced from internal/pendency.Resolve — I7's one relation over
// (artifact, system) — never re-derived here; NextActions stays the
// SEPARATE, broader "possible moves" list (see buildOpenItems' own doc
// comment for why the two do not collapse into one).
type OpenItem struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	State       string       `json:"state"`
	Blocking    bool         `json:"blocking"`
	NeededBy    string       `json:"needed_by,omitempty"`
	NextActions []NextAction `json:"next_actions"`
	WaitingOn   []string     `json:"waiting_on"`
	YourMove    bool         `json:"your_move"`
	// ExpectedTransition is the pendency relation's own `expected` for
	// this item's WaitingOn — "" (and omitted) when WaitingOn is empty,
	// since nothing is owed.
	ExpectedTransition string `json:"expected_transition,omitempty"`
	// Why is the pendency relation's own rationale — ALWAYS populated
	// (internal/pendency.Verdict's own contract: "settled" is a claim
	// that must be justified, never a fall-through), including when
	// WaitingOn is empty.
	Why string `json:"why"`
	// HumanGate names the §3.7 gate ExpectedTransition sits behind ("G3"),
	// omitted when the owed move is one an agent makes on its own.
	//
	// It is rendered because fixing the relation alone would not have fixed
	// the defect (spec 11 §18e/J3): the whole failure was a SURFACE naming a
	// move the tool then refuses. An agent reading this item and seeing
	// `your_move: true` with `expected_transition: approve` and no further
	// signal will try it, and CC-021 says the fold ignores and flags exactly
	// that. The field is what lets it branch instead — escalate to the human
	// owner rather than emit an event that cannot land.
	HumanGate string `json:"human_gate,omitempty"`
	// Outcome, Terminal and the three State* fields mirror cache.Item's own
	// identically-named fields — see their doc comments there. They are on
	// OpenItem too because `a2a thread --json` is one of the four surfaces
	// spec 00's AC2 names, and a consumer must not have to fetch the same
	// artifact through `inbox` to learn what its state means.
	//
	// Outcome answers a different question than Why/WaitingOn above: those
	// are the pendency verdict (who owes a move), this is what the state
	// MEANS. An item can be `refused` and still owe one — handoff/rejected
	// is exactly that.
	Outcome    fold.Outcome `json:"outcome,omitempty"`
	Terminal   bool         `json:"terminal"`
	StateSince time.Time    `json:"state_since,omitzero"`
	StateBy    string       `json:"state_by,omitempty"`
	StateEvent string       `json:"state_event,omitempty"`
}

// ThreadFlag is one fold.Flag attributable to a thread member, rendered
// (never dropped — §T3 "flags ... ALWAYS present").
type ThreadFlag struct {
	Kind        string           `json:"kind"`
	Subject     string           `json:"subject"`
	EventULID   string           `json:"event_ulid,omitempty"`
	Consistency *ReceiptMismatch `json:"consistency,omitempty"`
}

// UnresolvedFact is one reference this thread's own rendered member set
// could not resolve — a response whose `parent` is not itself a rendered
// member, or an event whose `subject` is not (§T3 "rendered, never
// dropped"; CC-073/REF-009 mean this should be empty on a conforming
// thread — its presence signals a fork or a broken propagation).
type UnresolvedFact struct {
	Kind string `json:"kind"` // "parent" | "event-subject"
	ID   string `json:"id"`
}

// ThreadSpaceHit is one connected space's summary in a ThreadAmbiguityError
// (T4's disambiguation block: "each space, its member count, its opener").
type ThreadSpaceHit struct {
	Space       string
	MemberCount int
	Opener      ThreadOpener
}

// ThreadAmbiguityError is returned by ThreadView when threadID has
// members in TWO OR MORE connected spaces — CC-073 forbids the silent
// merge; T4 requires a refusal carrying enough for the caller (the CLI
// wave) to print a recovery, never a partitioned render (T4: "sectioned
// output *will* be read as one conversation by a weak agent").
type ThreadAmbiguityError struct {
	Thread string
	Spaces []ThreadSpaceHit
}

func (e *ThreadAmbiguityError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cache: thread %q present in %d connected spaces: ", e.Thread, len(e.Spaces))
	for i, sh := range e.Spaces {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%d members, opener %s)", sh.Space, sh.MemberCount, sh.Opener.ID)
	}
	return b.String()
}

// ThreadResult is `a2a thread`'s full read model (spec 46 §T3/§T4): one
// causally-ordered transcript of artifacts AND events for ONE thread in
// ONE space, plus an "open_items" whose-move-is-it block — composed over
// Store's own index + internal/fold, never a second traversal or a second
// reading of the transition table. This is a NEW result type, deliberately
// independent of Item (inbox/outbox/search's guaranteed-stable shape is
// untouched).
type ThreadResult struct {
	Thread       string            `json:"thread"`
	Space        string            `json:"space"`
	Order        string            `json:"order"`
	ResolvedFrom string            `json:"resolved_from,omitempty"`
	Opener       ThreadOpener      `json:"opener"`
	Participants []string          `json:"participants"`
	SyncStale    bool              `json:"sync_stale"`
	Artifacts    []ThreadArtifact  `json:"artifacts"`
	Transcript   []TranscriptEntry `json:"transcript"`
	OpenItems    []OpenItem        `json:"open_items"`
	Flags        []ThreadFlag      `json:"flags"`
	Unresolved   []UnresolvedFact  `json:"unresolved"`

	// Deliveries carries this thread's handoff deliverables of kind "data"
	// (spec 05a AC-7), resolved via ResolveDeliveries against
	// packageresolver.go's mirror-backed PackageResolver — thread-side,
	// under the handoff artifact that names them (Delivery.HandoffID),
	// never a per-contract-version table (plan decision 2, deferred to
	// P8). `omitempty` is deliberate, matching internal/html's own
	// ThreadView.Deliveries doc comment: a thread with no data
	// deliverables must produce byte-identical output to before this
	// field existed, not a stray `"deliveries":[]`.
	Deliveries []Delivery `json:"deliveries,omitempty"`
}

// ThreadView computes `a2a thread <thread-id | any-member-artifact-id>
// [--space <id>]` (OP-210, spec 46 §T3/§T4). ref is either a `thread:...`
// value (used directly) or an artifact id (resolved to that artifact's
// thread, recorded in ResolvedFrom) — an id matching nothing in the
// searched space(s) is ErrNotFound, never an empty success (D6). spaceID,
// when non-empty, restricts BOTH the any-member resolution and the
// rendered space to that one connected space; when empty, resolution
// searches every connected space and membership is computed per-space
// (CC-073) — present in exactly one renders, present in two or more is a
// *ThreadAmbiguityError, present in zero is ErrThreadNotFound.
func (s *Store) ThreadView(ctx context.Context, ref string, spaceID string) (ThreadResult, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return ThreadResult{}, err
	}
	spaceIDs := make([]string, 0, len(idx))
	for id := range idx {
		spaceIDs = append(spaceIDs, id)
	}
	sort.Strings(spaceIDs)

	threadID := ref
	resolvedFrom := ""
	if !strings.HasPrefix(ref, "thread:") {
		found := false
	resolveLoop:
		for _, id := range spaceIDs {
			if spaceID != "" && id != spaceID {
				continue
			}
			for _, fa := range idx[id] {
				if fa.Env.ID == ref {
					threadID = fa.Env.Thread
					resolvedFrom = ref
					found = true
					break resolveLoop
				}
			}
		}
		if !found {
			return ThreadResult{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
		}
		// The artifact exists but carries NO thread — a document committed
		// before threads were minted. Refuse by name instead of rendering it.
		//
		// Found by reading real output against a live space, not by a test:
		// without this branch the empty thread value falls through and matches
		// every OTHER threadless artifact in the space, so the reader presents
		// a "conversation" with an empty id assembled from documents that have
		// nothing to do with each other. That is the exact failure this whole
		// feature exists to prevent — an authoritative-looking view of a
		// relationship that does not exist — and it is worse than the error,
		// because a caller cannot tell it from a real thread.
		if threadID == "" {
			return ThreadResult{}, fmt.Errorf("%w: %q carries no thread, so there is no conversation to read; that artifact predates thread propagation and cannot be repaired in place", ErrThreadNotFound, ref)
		}
	}

	type hit struct {
		spaceID string
		members []foldedArtifact
	}
	var hits []hit
	for _, id := range spaceIDs {
		if spaceID != "" && id != spaceID {
			continue
		}
		var members []foldedArtifact
		for _, fa := range idx[id] {
			if fa.Env.Thread == threadID {
				members = append(members, fa)
			}
		}
		if len(members) > 0 {
			hits = append(hits, hit{spaceID: id, members: members})
		}
	}

	if len(hits) == 0 {
		return ThreadResult{}, fmt.Errorf("%w: %q", ErrThreadNotFound, threadID)
	}
	if len(hits) > 1 {
		amb := &ThreadAmbiguityError{Thread: threadID}
		for _, h := range hits {
			sorted, _ := sortMembers(h.members)
			amb.Spaces = append(amb.Spaces, ThreadSpaceHit{
				Space: h.spaceID, MemberCount: len(h.members), Opener: openerOf(sorted[0]),
			})
		}
		return ThreadResult{}, amb
	}

	return s.renderThread(threadID, resolvedFrom, hits[0].spaceID, hits[0].members)
}

func (s *Store) renderThread(threadID, resolvedFrom, spaceID string, members []foldedArtifact) (ThreadResult, error) {
	sorted, order := sortMembers(members)

	byID := make(map[string]foldedArtifact, len(sorted))
	for _, fa := range sorted {
		byID[fa.Env.ID] = fa
	}

	artifacts := make([]ThreadArtifact, 0, len(sorted))
	participantSet := map[string]bool{}
	for _, fa := range sorted {
		var refs []ThreadRef
		for _, r := range fa.Env.Refs {
			refs = append(refs, ThreadRef{Ref: r.Ref})
		}
		artifacts = append(artifacts, ThreadArtifact{
			ID: fa.Env.ID, Type: fa.Env.Type, From: fa.Env.From, To: normalizeTo(fa.Env.To),
			Title: fa.Env.Title, State: string(fa.Result.State), Parent: fa.Env.Parent, Refs: refs,
		})
		if fa.Env.From != "" {
			participantSet[fa.Env.From] = true
		}
		for _, to := range normalizeTo(fa.Env.To) {
			if to != "" && to != "all" {
				participantSet[to] = true
			}
		}
	}

	// Adoption facts, per contract member this thread actually renders —
	// resolved ONCE here (never per-candidate inside buildTranscript) so
	// the read stays a single mirror-wide glob per contract rather than
	// one per transcript candidate.
	adoptions := map[string][]adoptionFact{}
	for _, fa := range sorted {
		if fa.kind() != fold.KindContract {
			continue
		}
		if facts := s.contractAdoptions(spaceID, fa.Env.ID); len(facts) > 0 {
			adoptions[fa.Env.ID] = facts
		}
	}
	transcript, unresolvedEvents := buildTranscript(sorted, order, adoptions)

	var unresolved []UnresolvedFact
	for _, fa := range sorted {
		if fa.Env.Parent != "" {
			if _, ok := byID[fa.Env.Parent]; !ok {
				unresolved = append(unresolved, UnresolvedFact{Kind: "parent", ID: fa.Env.ID})
			}
		}
	}
	unresolved = append(unresolved, unresolvedEvents...)

	// byID is deliberately the RENDERED MEMBER set only, not the whole
	// space's index: `unresolved` above and buildOpenItems' parent lookup
	// below share the same definition of "resolvable" — a parent this
	// thread does not itself render (REF-009 violation / pre-validator
	// fork) is a real degradation, named in `unresolved` AND reflected in
	// open_items' best-effort fallback (see buildOpenItems' own doc
	// comment), never silently patched over by reaching outside the
	// thread for a "correct" answer a reader has no way to see.
	openItems := buildOpenItems(sorted, byID, s.manifestFor(spaceID), s.ownSystem)

	var flags []ThreadFlag
	seenFlag := map[string]bool{}
	for _, fa := range sorted {
		for _, f := range fa.Result.Flags {
			key := string(f.Kind) + "|" + f.Subject + "|" + f.EventULID
			if seenFlag[key] {
				continue
			}
			seenFlag[key] = true
			flags = append(flags, ThreadFlag{
				Kind: string(f.Kind), Subject: f.Subject, EventULID: f.EventULID,
				Consistency: receiptMismatchFor(fa, f.EventULID),
			})
		}
	}

	participants := make([]string, 0, len(participantSet))
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	deliveries := buildDeliveries(sorted, s.deliveryResolverFor(spaceID))

	if artifacts == nil {
		artifacts = []ThreadArtifact{}
	}
	if transcript == nil {
		transcript = []TranscriptEntry{}
	}
	if openItems == nil {
		openItems = []OpenItem{}
	}
	if flags == nil {
		flags = []ThreadFlag{}
	}
	if unresolved == nil {
		unresolved = []UnresolvedFact{}
	}
	if participants == nil {
		participants = []string{}
	}

	return ThreadResult{
		Thread: threadID, Space: spaceID, Order: order, ResolvedFrom: resolvedFrom,
		Opener: openerOf(sorted[0]), Participants: participants, SyncStale: s.spaceSyncStale(spaceID),
		Artifacts: artifacts, Transcript: transcript, OpenItems: openItems,
		Flags: flags, Unresolved: unresolved, Deliveries: deliveries,
	}, nil
}

// adoptionFact is one OTHER system's own consumes.yaml dependency entry
// naming a contract this thread renders — contractAdoptions' return unit,
// carrying exactly the fields TranscriptDerivedAdoption needs and nothing
// this package would have to re-derive.
type adoptionFact struct {
	System string
	Major  int
	// Since is the registry's own `since:` value, verbatim — possibly
	// empty (see TranscriptDerivedAdoption.Since's own doc comment).
	Since string
}

// contractAdoptions returns spaceID's own mirror-wide adoption facts for
// contractID: one adoptionFact per OTHER system whose committed
// consumes.yaml names it, read straight off the mirror's checked-out
// working tree (glob mirror.Dir/*/consumes.yaml) — the SAME shape
// registered_consumers.go's own findRegisteredConsumers walks for the
// D-022 union, and the SAME parseConsumesStrict validation
// myDependencyContracts (mirror.go) and findRegisteredConsumers both
// already call, reused here rather than a second consumes.yaml parser.
// This function differs from both only in what it keeps: they collapse
// down to a bare system-id set; a thread transcript row needs the pinned
// major and the declared `since` date as well, so this reads the same
// files and keeps the extra fields.
//
// A missing mirror, an unreadable glob, or one participant's malformed
// registry degrades to fewer facts (or none) rather than failing the
// whole thread render — this file's own established convention
// (buildDeliveries, buildOpenItems' response-parent fallback): a
// counterparty's adoption is significant to SHOW, but its absence from one
// broken file must never take down the rest of the transcript.
func (s *Store) contractAdoptions(spaceID, contractID string) []adoptionFact {
	mirror, ok := s.mirrorFor(spaceID)
	if !ok {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(mirror.Dir, "*", "consumes.yaml"))
	if err != nil {
		return nil
	}
	var out []adoptionFact
	for _, m := range matches {
		raw, rerr := readBounded(m, maxCacheReadBytes)
		if rerr != nil {
			continue
		}
		registry, cerr := parseConsumesStrict(raw, m)
		if cerr != nil {
			continue
		}
		for _, dep := range registry.Dependencies {
			if dep.Contract != contractID {
				continue
			}
			out = append(out, adoptionFact{System: registry.System, Major: dep.Major, Since: dep.Since})
		}
	}
	return out
}

// mirrorFor returns spaceID's own connected-space mirror (dir + manifest),
// alongside manifestFor's own lookup but returning the WHOLE mirror —
// buildDeliveries' resolver needs the mirror's checked-out directory,
// which manifestFor's own narrower return type does not carry.
func (s *Store) mirrorFor(spaceID string) (SpaceMirror, bool) {
	for _, sm := range s.spaceMirrorsSnapshot() {
		if sm.SpaceID == spaceID {
			return sm, true
		}
	}
	return SpaceMirror{}, false
}

// deliveryResolverFor builds spaceID's own mirror-backed PackageResolver
// (packageresolver.go) — nil when spaceID's mirror cannot be found, which
// buildDeliveries treats as "no deliveries resolved" rather than a panic
// or a fabricated resolver over an empty directory.
func (s *Store) deliveryResolverFor(spaceID string) PackageResolver {
	mirror, ok := s.mirrorFor(spaceID)
	if !ok {
		return nil
	}
	participants := make([]string, 0, len(mirror.Manifest.Participants))
	for _, p := range mirror.Manifest.Participants {
		participants = append(participants, p.System)
	}
	return newMirrorPackageResolver(mirror.Dir, participants)
}

// buildDeliveries resolves every rendered handoff member's data-kind
// deliverables[] (spec 05a AC-7) against resolver, in member order —
// composed over ResolveDeliveries (delivery.go), never a second
// deliverables[] decode here. A nil resolver (no mirror found for this
// thread's own space — should not happen for a thread ThreadView itself
// already resolved to one connected space, but guarded rather than
// assumed) or a handoff whose deliverables[] cannot even be decoded
// degrades that handoff's own deliveries away, mirroring this file's own
// "degrade, never fail the whole thread render" convention elsewhere
// (buildOpenItems' response-parent fallback, unresolved parents/events).
func buildDeliveries(sorted []foldedArtifact, resolver PackageResolver) []Delivery {
	if resolver == nil {
		return nil
	}
	var out []Delivery
	for _, fa := range sorted {
		if fa.kind() != fold.KindHandoff {
			continue
		}
		resolved, err := ResolveDeliveries(fa.Env.ID, fa.Raw, resolver)
		if err != nil {
			continue
		}
		out = append(out, resolved...)
	}
	return out
}

func openerOf(fa foldedArtifact) ThreadOpener {
	return ThreadOpener{ID: fa.Env.ID, Title: fa.Env.Title, From: fa.Env.From}
}

// sortMembers returns members sorted for display — primary key the
// per-space commit sequence (fa.Seq) when the space's history was
// readable, else the envelope's own `created` field — with the SAME
// order both a bare artifact list and the merged transcript use (§T3).
// members must be non-empty (caller's own contract: ThreadView never
// calls this with a zero-member hit).
func sortMembers(members []foldedArtifact) ([]foldedArtifact, string) {
	order := ThreadOrderDeclared
	if len(members) > 0 && members[0].OrderKnown {
		order = ThreadOrderCommitted
	}
	sorted := append([]foldedArtifact(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if order == ThreadOrderCommitted {
			if a.Seq != b.Seq {
				return a.Seq < b.Seq
			}
		} else {
			at, bt := parseTimeField(a.Env.Created), parseTimeField(b.Env.Created)
			if !at.Equal(bt) {
				return at.Before(bt)
			}
		}
		return a.Env.ID < b.Env.ID
	})
	return sorted, order
}

// parseTimeField parses an RFC3339 envelope/event timestamp field,
// degrading to the zero time on a parse failure — display metadata only,
// never a hard error (mirrors mirror.go's own `time.Parse(..., re.Ev.At)`
// convention).
func parseTimeField(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseDateField parses a consumes.yaml `since:` value — the date-only
// (YYYY-MM-DD) format `a2a contract adopt` writes (cmd_contract.go's own
// `deps.now().UTC().Format("2006-01-02")`) — into a UTC midnight
// time.Time. An empty or unparseable value degrades to the zero time, the
// SAME "display metadata only, never a hard error" convention
// parseTimeField already uses for RFC3339 fields — never invented, never
// substituted with "now" (the honesty rule this function exists to keep:
// a dated row that is WRONG is worse than one that is honestly undated).
func parseDateField(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// transcriptCandidate is buildTranscript's own internal sort unit — one
// artifact, one event, or one derived adoption row, carrying whichever
// ordering key sortMembers' `order` selects plus the
// artifact-before-event/id tie-break key.
type transcriptCandidate struct {
	entry   TranscriptEntry
	seq     int64
	at      time.Time
	isEvent bool
	// isDerived marks a TranscriptKindDerived candidate. Its seq is its
	// OWN contract member's seq (see the derived-candidate loop below,
	// buildTranscript's own doc comment) — never a real, independently
	// earned commit position — so at an equal seq it is ranked AFTER the
	// artifact/event candidates that share that position (candidateRank),
	// never before: an adoption can never render as though it preceded
	// the very contract it names.
	isDerived bool
	tieID     string
}

// candidateRank orders three candidates that land at the same seq/at
// position: the contract's own artifact row first, then its lifecycle
// events, then any derived adoption row naming it — never the reverse,
// which would render "adopted" ahead of "exists".
func candidateRank(c transcriptCandidate) int {
	switch {
	case c.isDerived:
		return 2
	case c.isEvent:
		return 1
	default:
		return 0
	}
}

// buildTranscript merges sorted's own artifacts with every DISTINCT event
// (deduplicated by ULID — a verify/dispute event reaches the transcript
// via both the parent's own gathered event set AND the response's own,
// per mirror.go's gatherEvents doc) attached to any member, PLUS one
// TranscriptKindDerived row per (contract member, adopting system) pair in
// adoptions, in ONE strictly-ordered array (§T3). It also returns the
// "event subject not a rendered member" half of ThreadResult.Unresolved
// (the other half, unresolved parents, is computed by the caller against
// the same member set).
//
// A derived row whose consumes.yaml carried no `since:` (parseDateField's
// zero-time case) is still emitted, never dropped — the honesty rule this
// wave was built to keep cuts the OTHER way: inventing a date is worse
// than an undated fact, but DROPPING the fact of adoption entirely would
// recreate the exact defect this feature exists to fix. Such a row sorts
// to the very END of the transcript, deliberately — NOT to the front the
// way a naive "unparsed date degrades to the zero time, zero sorts first"
// rule (parseTimeField's own established convention elsewhere in this
// file) would place it. A missing date carries no ordering claim at all;
// placing it first would assert "this adoption happened before anything
// else in the thread", which is exactly as dishonest as inventing the
// date itself — the same rule the brief states for the date applies to
// the row's POSITION.
func buildTranscript(sorted []foldedArtifact, order string, adoptions map[string][]adoptionFact) ([]TranscriptEntry, []UnresolvedFact) {
	memberIDs := make(map[string]bool, len(sorted))
	for _, fa := range sorted {
		memberIDs[fa.Env.ID] = true
	}

	var candidates []transcriptCandidate
	for _, fa := range sorted {
		var work *TranscriptWorkReport
		if checkpoint, recognized, err := classifyOperationalCheckpoint(fa); recognized && err == nil {
			work = &TranscriptWorkReport{
				ArtifactID: checkpoint.ArtifactID, WorkID: checkpoint.WorkID,
				SubjectRef: checkpoint.SubjectRef, Mode: checkpoint.Mode, Summary: checkpoint.Summary,
				Actor: TranscriptEventActor{
					Kind: checkpoint.Actor.Kind, Name: checkpoint.Actor.Name, System: checkpoint.Actor.System,
					Model: checkpoint.Actor.Model, Session: checkpoint.Actor.Session,
				},
				WaitingOn:  append([]workreport.WaitingOn(nil), checkpoint.WaitingOn...),
				ReportedAt: checkpoint.ReportedAt, ValidUntil: checkpoint.ValidUntil,
				CommitSequence: checkpoint.CommitSequence,
			}
		}
		candidates = append(candidates, transcriptCandidate{
			entry: TranscriptEntry{
				Seq: fa.Seq, Kind: "artifact", At: parseTimeField(fa.Env.Created),
				Artifact: &TranscriptArtifact{
					ID: fa.Env.ID, Type: fa.Env.Type, From: fa.Env.From,
					To: normalizeTo(fa.Env.To), Title: fa.Env.Title, Work: work,
				},
			},
			seq: fa.Seq, at: parseTimeField(fa.Env.Created), isEvent: false, tieID: fa.Env.ID,
		})
	}

	seenEvent := map[string]bool{}
	var unresolved []UnresolvedFact
	for _, fa := range sorted {
		for _, ev := range fa.Events {
			if seenEvent[ev.ULID] {
				continue
			}
			seenEvent[ev.ULID] = true
			if !memberIDs[ev.Subject] {
				unresolved = append(unresolved, UnresolvedFact{Kind: "event-subject", ID: ev.Subject})
			}
			at := fa.EventAt[ev.ULID]
			evidence := fa.EventEvidence[ev.ULID]
			candidates = append(candidates, transcriptCandidate{
				entry: TranscriptEntry{
					Seq: ev.CommitSeq, Kind: "event", At: at,
					Event: &TranscriptEvent{
						ULID: ev.ULID, Subject: ev.Subject, Transition: ev.Transition,
						ClaimedState: string(ev.ClaimedState),
						Actor: TranscriptEventActor{
							Kind: evidence.Actor.Kind, Name: evidence.Actor.Name, System: evidence.Actor.System,
							Model: evidence.Actor.Model, Session: evidence.Actor.Session,
						},
						ProducedBy:  evidence.Producer,
						Consistency: receiptMismatchFor(fa, ev.ULID),
						ResponseID:  ev.ResponseID,
						Version:     ev.Version,
						Note:        fa.EventNotes[ev.ULID],
						ReasonCode:  fa.EventReasonCodes[ev.ULID],
					},
				},
				seq: ev.CommitSeq, at: at, isEvent: true, tieID: ev.ULID,
			})
		}
	}

	for _, fa := range sorted {
		if fa.kind() != fold.KindContract {
			continue
		}
		for _, af := range adoptions[fa.Env.ID] {
			at := parseDateField(af.Since)
			candidates = append(candidates, transcriptCandidate{
				entry: TranscriptEntry{
					// Seq mirrors the NAMED CONTRACT's own seq — a derived
					// row earns no independent commit position, but
					// anchoring it to its contract's own position (rather
					// than a bare, meaningless 0) keeps it from outranking
					// facts that actually precede the contract in commit
					// order, and tells a consumer sorting by Seq alone
					// exactly what it is anchored to.
					Seq: fa.Seq, Kind: TranscriptKindDerived, At: at,
					Derived: &TranscriptDerivedAdoption{
						ContractID: fa.Env.ID, System: af.System, Major: af.Major, Since: af.Since,
					},
				},
				seq: fa.Seq, at: at, isDerived: true, tieID: fa.Env.ID + "|" + af.System,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]

		// An undated derived row (adoptionFact.Since carried no `since:`
		// at all, parseDateField's zero-time case) makes NO ordering
		// claim — see buildTranscript's own doc comment. It always sorts
		// after every dated/committed candidate, regardless of order
		// mode, rather than letting its degraded zero time win a
		// chronological comparison it has no evidence for.
		aUndated := a.isDerived && a.at.IsZero()
		bUndated := b.isDerived && b.at.IsZero()
		if aUndated != bUndated {
			return !aUndated
		}
		if aUndated && bUndated {
			return a.tieID < b.tieID
		}

		if order == ThreadOrderCommitted {
			if a.seq != b.seq {
				return a.seq < b.seq
			}
		} else if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		// At an equal position, rank by kind (candidateRank) before
		// falling through to a tie-break: an artifact sorts before its
		// own lifecycle events, and both sort before a derived adoption
		// row naming the same contract (never the reverse — "adopted"
		// cannot precede "exists").
		if ar, br := candidateRank(a), candidateRank(b); ar != br {
			return ar < br
		}
		// Two dated derived rows for the SAME contract (same anchor seq)
		// are ordered by their OWN `since` dates — the brief's own "order
		// it into the transcript by that date" — even though the
		// surrounding order mode's primary key (seq, above) is what
		// placed them at this shared position.
		if a.isDerived && b.isDerived && !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		return a.tieID < b.tieID
	})

	out := make([]TranscriptEntry, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.entry)
	}
	return out, unresolved
}

// envelopeFrom adapts a foldedArtifact's decoded envelope facts into
// fold.Envelope — the same minimal conversion buildIndex itself performs
// (mirror.go), repeated here (ISP idiom already established across this
// package's layer boundaries, decode.go's own doc comment) rather than
// exposing fold.Envelope construction as new shared plumbing for a single
// caller.
func envelopeFrom(fa foldedArtifact) fold.Envelope {
	return fold.Envelope{
		ID: fa.Env.ID, Kind: fold.Kind(fa.Env.Type), From: fa.Env.From,
		To: normalizeTo(fa.Env.To), RequiredApprovers: fa.Env.RequiredApprovers,
	}
}

// buildOpenItems answers TWO deliberately separate questions for every
// non-terminal member (§T3), and does not let either one compute the
// other's answer (P11 W1, I7):
//
//   - "Whose move is it" — WaitingOn/YourMove/ExpectedTransition/Why — is
//     sourced ENTIRELY from internal/pendency.Resolve, the one relation
//     over (artifact, system) every surface asks (I7). This function
//     builds pendency.Input from the same facts it already has (envelope,
//     folded Result, manifest, and — for a response — the PARENT's own
//     `from`) and renders the Verdict unmodified. It does not re-derive
//     "is this a settled/alive state" or "who is still owed an ack" by
//     hand; that would be a second implementation of the same question,
//     which is this wave's own definition of the defect.
//   - "What COULD still happen here" — NextActions — stays the full,
//     unfiltered list of legal moves and the systems that may make them,
//     unrelated to whether anyone is presently owed one of them. This is
//     the operator's own principle: nothing disappears from the record,
//     it only stops being counted as a debt. A settled, published
//     contract still renders `publish`/`deprecate` in next_actions with
//     an EMPTY WaitingOn.
//
// For each legal next transition (fold.LegalNextFor, a pure filter over
// fold's own table — never re-derived here), the candidate `by` system set
// is computed by calling fold.CheckCandidate once per manifest participant
// with a CONSTANT fold.MembershipMember status — the spec's own documented
// V1 imprecision ("a suspended participant can still be listed as
// waiting_on"), and the only way to resolve a Role to concrete system ids
// without either a second copy of roleAuthorizes (unexported, and
// internal/fold is outside this wave's allowlist) or re-deriving the rule
// by hand.
//
// P4 (04-per-version-lifecycle.plan.md): this view is one OpenItem per
// ARTIFACT, not per version, so it asks fold.LegalNextFor/CheckCandidate
// the WHOLE-CONTRACT form (version ""), never a specific version — the
// same "" this file passes to BOTH halves below, which is the one
// invariant that matters here: fold.LegalNextFor's own doc comment warns
// that a caller composing it with a legality check must ask both the
// SAME version, or reproduce, one layer up, exactly the split brain that
// function exists inside internal/fold to close. The whole-form questions
// this view actually asks are the ones it can answer: "may the owner
// publish the next version", "is anything left published to deprecate",
// "is everything deprecated so the whole contract may retire". A
// per-version "whose move for 1.2 specifically" view is a different
// question, needing one OpenItem per version, and is out of scope here.
//
// One asymmetry worth knowing before it reads as a bug: `publish` IS
// offered for a version-less ask, while fold.CheckCandidate refuses a
// version-less publish EVENT. Both are right, and the first revision of
// this migration got it wrong in the expensive direction — deriving the
// affordance from the event predicate dropped `publish` from the next
// actions of every contract that had ever published anything, i.e. from
// the commonest move an owner makes, on every contract in real use. See
// fold's contractMoveAvailable for the full statement.
//
// The response-scoped trap (§T3's own "highest-stakes step"): for a
// KindResponse member, CheckLegality's `verify`/`dispute` branch ALWAYS
// keys on KindResponse regardless of the `kind` argument and resolves
// RoleOwner against the supplied `env` — which MUST be the PARENT's
// envelope (its `from`), never the response's own (legality.go:20-45).
// This function resolves authEnv accordingly, for NextActions' role
// checks; the SAME resolved `From` is handed to pendency.Input.ParentFrom
// below, so WaitingOn inherits the identical degradation (see there) when
// the parent cannot be resolved, instead of a second, differently-shaped
// fallback.
//
// One transition is deliberately dropped from a NON-response member's own
// next_actions: `dispute`. table.go's exchangeRows() gives the parent
// exchange a second, real-but-informational `responded -> in_progress`
// row (D-024's reopen side effect) — but CheckLegality can never actually
// authorize firing "dispute" against the PARENT's own subject (its
// verify/dispute branch is hardcoded response-scoped no matter what kind
// is passed), so every candidate would legitimately fail and `by` would
// always render empty. No information is lost by omitting it outright,
// and the response's OWN open item already carries `dispute` with its
// correct `by`. See legalnext.go:43-56's own doc comment ("LegalNext
// reports both, unmodified" — a caller-side choice, not a table bug) and
// v1-min spec 46 §11.
//
// D-025's transition-free broadcast-acknowledge carries NO row in fold's
// table (legalnext.go:57-69's own doc comment: "A caller presenting
// 'whose move is it' for an announcement must add that case itself") —
// NextActions still needs an explicit `acknowledge` entry for this case
// (nothing else produces one), so it is built from pendency's own Verdict
// for this same item rather than a second, locally re-derived scan of
// Result.Acks — pendency's unackedTargets resolver IS that scan now, in
// its one home.
func buildOpenItems(sorted []foldedArtifact, byID map[string]foldedArtifact, manifest space.Manifest, ownSystem string) []OpenItem {
	var out []OpenItem
	for _, fa := range sorted {
		if !isOpen(fa.kind(), fa.Result.State) {
			continue
		}

		authEnv := envelopeFrom(fa)
		if fa.kind() == fold.KindResponse {
			// The trap: role checks for verify/dispute resolve against
			// the PARENT's envelope, never the response's own (see
			// buildOpenItems' own doc comment). If the parent cannot be
			// resolved (already recorded in ThreadResult.Unresolved),
			// degrade to the response's own envelope — a best-effort
			// answer rather than no answer at all. pendency.Input.ParentFrom
			// below reuses authEnv.From, so WaitingOn inherits this exact
			// same degradation.
			if parent, ok := byID[fa.Env.Parent]; ok {
				authEnv = envelopeFrom(parent)
			}
		}

		var actions []NextAction
		for _, mv := range fold.LegalNextFor(fa.kind(), fa.Result, "") {
			if fa.kind() != fold.KindResponse && mv.Transition == fold.TDispute {
				// Non-actionable duplicate — see buildOpenItems' own doc
				// comment (the exchange-kind dispute row never resolves
				// through CheckLegality against the parent's own
				// subject).
				continue
			}
			by := legalSystems(mv, authEnv, manifest)
			actions = append(actions, NextAction{Transition: mv.Transition, By: by})
		}

		in := pendency.Input{
			Kind: fa.kind(), State: fa.Result.State,
			From: fa.Env.From, To: normalizeTo(fa.Env.To),
			Broadcast: fa.Env.isBroadcast(), AckRequested: fa.Env.AckRequested,
			Acks: fa.Result.Acks, Approvals: fa.Result.Approvals,
			RequiredApprovers:  fa.Env.RequiredApprovers,
			ActiveParticipants: activeParticipants(manifest, fa.Env.From),
			LeftParticipants:   leftParticipants(manifest),
			// P4 Edge 3, handed in exactly as inbox.go's call site hands it
			// in. Both call sites MUST supply this: the moment one of them
			// omits it, the two disagree about who a frozen `to:` addresses,
			// which is this wave's own defect in miniature.
			//
			// It names only ownSystem, because mirror.go resolved
			// DeprecatesMyDependency from this system's own consumes.yaml —
			// so this view answers "who owes an ack" completely for me and
			// narrowly for everybody else. That gap is in the FACT, not the
			// rule (pendency.Input.ExtraAddressees' own doc comment); W2 must
			// not render this as authoritative for another system until a
			// caller can read every participant's registry.
			ExtraAddressees: extraAddressees(fa, ownSystem),
			// The requirement row splits on this (pendency.Input's own doc):
			// before a fulfilling response lands the target owes the work,
			// after it lands the requester owes `satisfy`.
			HasFulfillingResponse: hasFulfillingResponse(fa),
		}
		if fa.kind() == fold.KindResponse {
			in.ParentFrom = authEnv.From
		}
		verdict, err := pendency.Resolve(in)
		if err != nil {
			// isOpen(fa.kind(), fa.Result.State) above already filtered to
			// exactly the (kind,state) pairs the pendency table carries a
			// row for (I8's totality gate over fold.SubjectStates,
			// mirrored 1:1 by this package's own openStates allowlist —
			// see types.go's own doc comment). Reaching here would mean
			// the two tables have drifted apart; degrade to "nobody"
			// rather than lose the whole thread render over one item —
			// but Why still names the degradation rather than silently
			// rendering an empty string (OpenItem.Why's own "ALWAYS
			// populated" contract).
			verdict = pendency.Verdict{Why: "pendency carries no row for (" + string(fa.kind()) + ", " + string(fa.Result.State) + "); this should be unreachable — see buildOpenItems' own doc comment"}
		}

		// D-025's transition-free broadcast-acknowledge: no table row
		// exists in fold (legalnext.go's own doc comment), so NextActions
		// needs an explicit `acknowledge` entry built here — from the
		// SAME Verdict WaitingOn already carries, never a second scan.
		// Gated on verdict.Expected == TAcknowledge specifically (not
		// merely len(Owners) > 0): a DRAFT announcement's row is
		// ownerRow(TPublish), which fold.LegalNextFor above ALREADY
		// emitted as its own `publish` NextAction — without this gate
		// this branch would append a second, duplicate `publish` entry
		// for every draft announcement.
		if fa.kind() == fold.KindAnnouncement && verdict.Expected == fold.TAcknowledge && len(verdict.Owners) > 0 {
			actions = append(actions, NextAction{Transition: verdict.Expected, By: verdict.Owners})
		}

		if actions == nil {
			actions = []NextAction{}
		}
		waiting := verdict.Owners
		if waiting == nil {
			waiting = []string{}
		}

		out = append(out, OpenItem{
			ID: fa.Env.ID, Type: fa.Env.Type, State: string(fa.Result.State),
			Blocking: fa.Env.Blocking, NeededBy: fa.Env.NeededBy,
			NextActions: actions, WaitingOn: waiting,
			YourMove:           containsString(waiting, ownSystem),
			ExpectedTransition: verdict.Expected,
			Why:                verdict.Why,
			HumanGate:          verdict.HumanGate,
			Outcome:            fold.OutcomeOf(fa.kind(), fa.Result.State),
			Terminal:           fold.Terminal(fa.kind(), fa.Result.State),
			StateSince:         fa.StateSince,
			StateBy:            fa.StateBy,
			StateEvent:         fa.StateEventID,
		})
	}
	return out
}

// activeParticipants returns manifest's own ACTIVE participant systems,
// excluding exclude (the envelope's own `from`) — the broadcast-expansion
// set both pendency.Input.ActiveParticipants (unackedTargets' resolver)
// and, historically, this file's own now-deleted local ack scan used.
func activeParticipants(manifest space.Manifest, exclude string) []string {
	var out []string
	for _, p := range manifest.Participants {
		if p.Status == "active" && p.System != exclude {
			out = append(out, p.System)
		}
	}
	return out
}

// leftParticipants returns the manifest systems whose membership status is
// `left` — the caller-resolved FACT behind pendency.Input.LeftParticipants
// (CC-062). It is the same read internal/validate does when it builds
// RegisteredConsumer.Left, drawn here because pendency reads no manifest.
//
// Deliberately keyed on `left` and nothing else, rather than on "not
// active": a status this function has never heard of must not silently
// orphan a live counterparty. Membership vocabulary is the manifest
// schema's to widen, and widening it here — where the consequence is
// "somebody's debt is transferred away" — is how a schema addition turns
// into a wrong verdict nobody wrote.
func leftParticipants(manifest space.Manifest) []string {
	var out []string
	for _, p := range manifest.Participants {
		if p.Status == "left" {
			out = append(out, p.System)
		}
	}
	return out
}

// legalSystems resolves ONE already-legal move to the manifest
// participants it belongs to — `fold.LegalNextFor` above established that
// the move is legal for the artifact, so all that is left is turning its
// fold.Role into system ids.
//
// It used to do that by calling fold.CheckLegality once per participant,
// because the role resolution was unexported — a workaround this
// function's own doc comment described as one. It broke on the first move
// that is offered as an affordance but refused as an event (the
// whole-contract `publish`: fold.CheckCandidate correctly refuses a
// version-less publish EVENT, so every participant came back
// unauthorized and `publish.by` rendered empty on every live contract).
// fold.RoleAuthorizes is the resolution the workaround was approximating,
// now exported for exactly this caller.
//
// Membership stays the spec's own documented V1 imprecision: every
// manifest participant is treated as a current member ("a suspended
// participant can still be listed as waiting_on"), which is what the
// constant fold.MembershipMember expressed before.
func legalSystems(mv fold.NextMove, env fold.Envelope, manifest space.Manifest) []string {
	var out []string
	for _, p := range manifest.Participants {
		if fold.RoleAuthorizes(mv.Role, env, p.System) {
			out = append(out, p.System)
		}
	}
	sort.Strings(out)
	return out
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
