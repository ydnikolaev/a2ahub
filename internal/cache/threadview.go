package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/space"
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
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	From  string   `json:"from"`
	To    []string `json:"to,omitempty"`
	Title string   `json:"title"`
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
}

// TranscriptEntry is ONE strictly seq-ordered transcript row — a
// discriminated union (Kind = "artifact" | "event"), never two lists a
// reader must interleave mentally (§T3).
type TranscriptEntry struct {
	Seq      int64               `json:"seq"`
	Kind     string              `json:"kind"`
	At       time.Time           `json:"at"`
	Artifact *TranscriptArtifact `json:"artifact,omitempty"`
	Event    *TranscriptEvent    `json:"event,omitempty"`
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
// thread member (§T3).
type OpenItem struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	State       string       `json:"state"`
	Blocking    bool         `json:"blocking"`
	NeededBy    string       `json:"needed_by,omitempty"`
	NextActions []NextAction `json:"next_actions"`
	WaitingOn   []string     `json:"waiting_on"`
	YourMove    bool         `json:"your_move"`
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
}

// escapeHatchTransitions are always available to an artifact's owner and
// must never, by themselves, make `your_move` permanently true (§T3's
// `open_items` bullet: "a small explicit classification ... escape
// hatches like cancel/withdraw/supersede are always available to the
// owner"). internal/fold carries no such classification exported (and is
// off limits to this wave's allowlist) — this is cache's own display-only
// categorization over fold's exported transition-name constants, never a
// second reading of WHICH (kind,state,transition) triples are legal (that
// remains fold.LegalNext + fold.CheckLegality's job, composed over below).
// See v1-min spec 46 §11.
var escapeHatchTransitions = map[string]bool{
	fold.TCancel:    true,
	fold.TWithdraw:  true,
	fold.TSupersede: true,
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

	transcript, unresolvedEvents := buildTranscript(sorted, order)

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
		Flags: flags, Unresolved: unresolved,
	}, nil
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

// transcriptCandidate is buildTranscript's own internal sort unit — one
// artifact or one event, carrying whichever ordering key sortMembers'
// `order` selects plus the artifact-before-event/id tie-break key.
type transcriptCandidate struct {
	entry   TranscriptEntry
	seq     int64
	at      time.Time
	isEvent bool
	tieID   string
}

// buildTranscript merges sorted's own artifacts with every DISTINCT event
// (deduplicated by ULID — a verify/dispute event reaches the transcript
// via both the parent's own gathered event set AND the response's own,
// per mirror.go's gatherEvents doc) attached to any member, in ONE
// strictly-ordered array (§T3). It also returns the "event subject not a
// rendered member" half of ThreadResult.Unresolved (the other half,
// unresolved parents, is computed by the caller against the same member
// set).
func buildTranscript(sorted []foldedArtifact, order string) ([]TranscriptEntry, []UnresolvedFact) {
	memberIDs := make(map[string]bool, len(sorted))
	for _, fa := range sorted {
		memberIDs[fa.Env.ID] = true
	}

	var candidates []transcriptCandidate
	for _, fa := range sorted {
		candidates = append(candidates, transcriptCandidate{
			entry: TranscriptEntry{
				Seq: fa.Seq, Kind: "artifact", At: parseTimeField(fa.Env.Created),
				Artifact: &TranscriptArtifact{
					ID: fa.Env.ID, Type: fa.Env.Type, From: fa.Env.From,
					To: normalizeTo(fa.Env.To), Title: fa.Env.Title,
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
					},
				},
				seq: ev.CommitSeq, at: at, isEvent: true, tieID: ev.ULID,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if order == ThreadOrderCommitted {
			if a.seq != b.seq {
				return a.seq < b.seq
			}
		} else if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.isEvent != b.isEvent {
			return !a.isEvent // artifact before event, same commit/timestamp
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

// buildOpenItems answers "whose move is it" for every non-terminal
// member (§T3). For each legal next transition (fold.LegalNextFor, a pure
// filter over fold's own table — never re-derived here), the candidate
// `by` system set is computed by calling fold.CheckCandidate once per
// manifest participant with a CONSTANT fold.MembershipMember status — the
// spec's own documented V1 imprecision ("a suspended participant can
// still be listed as waiting_on"), and the only way to resolve a Role to
// concrete system ids without either a second copy of roleAuthorizes
// (unexported, and internal/fold is outside this wave's allowlist) or
// re-deriving the rule by hand.
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
// This function resolves authEnv accordingly.
//
// One transition is deliberately dropped from a NON-response member's own
// next_actions: `dispute`. table.go's exchangeRows() gives the parent
// exchange a second, real-but-informational `responded -> in_progress`
// row (D-024's reopen side effect) — but CheckLegality can never actually
// authorize firing "dispute" against the PARENT's own subject (its
// verify/dispute branch is hardcoded response-scoped no matter what kind
// is passed), so every candidate would legitimately fail and `by` would
// always render empty. Since dispute is not an escape hatch, an empty
// `by` would also silently exclude it from waiting_on either way — no
// information is lost by omitting it outright, and the response's OWN
// open item already carries `dispute` with its correct `by`. See
// legalnext.go:43-56's own doc comment ("LegalNext reports both,
// unmodified" — a caller-side choice, not a table bug) and v1-min spec 46
// §11.
//
// D-025's transition-free broadcast-acknowledge carries NO row in
// fold's table (legalnext.go:57-69's own doc comment: "A caller
// presenting 'whose move is it' for an announcement must add that case
// itself") — handled below via Result.Acks directly, membership-only
// (broadcastAckPermitted's own rule, fold.go), never a re-derived
// authorization check.
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
			// answer rather than no answer at all.
			if parent, ok := byID[fa.Env.Parent]; ok {
				authEnv = envelopeFrom(parent)
			}
		}

		var actions []NextAction
		var waiting []string
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
			if !escapeHatchTransitions[mv.Transition] {
				waiting = append(waiting, by...)
			}
		}

		// D-025's transition-free broadcast-acknowledge: no table row
		// exists (legalnext.go's own doc comment), so a NOT-YET-ACKED
		// participant is this function's own explicit case, membership
		// only (broadcastAckPermitted's rule, never re-derived here).
		if fa.kind() == fold.KindAnnouncement {
			var pending []string
			for _, p := range manifest.Participants {
				if !fa.Result.Acks[p.System] {
					pending = append(pending, p.System)
				}
			}
			sort.Strings(pending)
			if len(pending) > 0 {
				actions = append(actions, NextAction{Transition: fold.TAcknowledge, By: pending})
				waiting = append(waiting, pending...)
			}
		}

		waiting = dedupSorted(waiting)
		if actions == nil {
			actions = []NextAction{}
		}
		if waiting == nil {
			waiting = []string{}
		}

		out = append(out, OpenItem{
			ID: fa.Env.ID, Type: fa.Env.Type, State: string(fa.Result.State),
			Blocking: fa.Env.Blocking, NeededBy: fa.Env.NeededBy,
			NextActions: actions, WaitingOn: waiting,
			YourMove: containsString(waiting, ownSystem),
		})
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
