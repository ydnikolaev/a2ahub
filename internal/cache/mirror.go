package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// canonicalEventVersion reformats a committed event's own `version` field
// through internal/version.Canonical, so two spellings of the same version
// ("1.0.0" and "01.0.0") produce the IDENTICAL string once threaded into
// fold.Event.Version — fold.Result.Versions is a map[string]State keyed on
// the raw string with no canonicalization of its own (P4,
// 04-per-version-lifecycle.plan.md; internal/fold is off-limits to this
// phase, so this is the caller's own half of keeping that map's keys
// consistent, at the one place every committed event's version enters
// fold's own input). Fails open (returns v unchanged) on an empty or
// unparseable v — canonicalization is never itself a refusal path; fold's
// own legality checks decide whether an unparseable version is legal.
func canonicalEventVersion(v string) string {
	if v == "" {
		return v
	}
	if c, err := version.Canonical(v); err == nil {
		return c
	}
	return v
}

// maxCacheReadBytes bounds every mirror file read this package performs
// (rails: "bounded reads everywhere").
const maxCacheReadBytes = 1 << 20 // 1 MiB

// rawArtifact is one *.md file found anywhere under a mirror's working
// tree (excluding vendored/ — a read-only mirror of a NON-participant's
// spec, out of this space's own lifecycle-exchange scope, and .git).
type rawArtifact struct {
	RelPath string
	Raw     []byte
	Env     envelopeProbe
	Digest  string
}

// rawEvent is one committed event/v1 YAML file found under any system's
// <system>/events/<year>/ directory.
type rawEvent struct {
	RelPath   string
	Ev        eventProbe
	CommitSeq int64
}

// foldedArtifact is one artifact's fully composed read-model: its
// envelope facts, the correctly-gathered event set (see gatherEvents),
// and the resulting fold.Result — the ONE folded-state computation this
// package ever performs (composed over internal/fold, never
// reimplemented, per spec §5).
type foldedArtifact struct {
	SpaceID string
	RelPath string
	Raw     []byte
	Digest  string
	Env     envelopeProbe
	Result  fold.Result
	Events  []fold.Event
	// EventEvidence preserves receipt/actor/producer diagnostics beside the
	// pure fold input. Model/session/producer never enter fold.Event.
	EventEvidence map[string]provenance.EventEvidence
	// ReceiptMismatches is produced during the same canonical event replay as
	// Result, so dynamic actual outcomes are never reconstructed from final state.
	ReceiptMismatches map[string]ReceiptMismatch
	// EventRefs preserves each committed event's refs[] beside the pure fold
	// input. fold.Event deliberately does not need relationship metadata, but
	// read models do: contract deprecate records its successor on the
	// deprecate event, and the dashboard must not lose that canonical link.
	EventRefs     map[string][]refEntry
	LatestEventAt time.Time
	// LatestEventSeq and LatestEventID identify the SAME own-subject event as
	// LatestEventAt. Keeping the three keys coherent matters to activity feeds:
	// two lifecycle events commonly share a second-level timestamp, while Git
	// commit order and the ULID still preserve their actual order.
	LatestEventSeq int64
	LatestEventID  string
	// StateEventID, StateBy and StateSince identify the event that produced
	// Result.State, which is NOT the same event as LatestEvent* whenever the
	// newest event is transition-free. All three are empty/zero when nothing
	// produced the state — the zero-events fallback, or a history whose
	// every event was flagged and moved nothing.
	StateEventID string
	StateBy      string
	StateSince   time.Time
	// EventAt maps a committed event's ULID to its `at` timestamp —
	// fold.Event itself carries none (fold is a pure, timestamp-free
	// package, §T1); this side table is this package's own way of
	// recovering it for show/thread rendering without extending fold's
	// input shape.
	EventAt map[string]time.Time
	// EventNotes preserves optional human-authored lifecycle commentary beside
	// the pure fold input. Notes never influence state, but read surfaces must
	// not reduce a committed note event to an unexplained status change.
	EventNotes map[string]string
	// EventReasonCodes maps a committed event's ULID to its machine-readable
	// `reason_code` — the field schemas/event/v1 defines and the MCP decline
	// tool REQUIRES, which this package decoded nowhere until P0.
	EventReasonCodes map[string]string
	// LatestPublishVersion is the most recent `publish` event's `version`
	// field for this artifact (D-023: contract versions resolve through
	// publish events) — empty when none recorded (never published, or a
	// non-contract kind).
	LatestPublishVersion string
	// Seq is this artifact's own file's per-space first-parent commit
	// sequence (commitOrder's own return value, keyed by RelPath) —
	// spec 46 §T3's primary thread-transcript ordering key, alongside
	// fold.Event.CommitSeq (already carried per event). Zero both for "no
	// commit found" (declared-order fallback, see OrderKnown) and for a
	// genuinely first-ever commit — OrderKnown is what disambiguates them.
	Seq int64
	// OrderKnown is true when this space's commit history was readable
	// (commitOrder returned a non-empty map) — every artifact in one
	// space's index shares the same value (space-level fact, carried
	// per-item the same way EventAt already is). False means Seq/CommitSeq
	// are meaningless zero values and a reader must fall back to
	// created/at ordering, reporting that degradation rather than
	// pretending the commit guarantee still holds (§T3 "Degradation is
	// designed, not silent").
	OrderKnown bool

	// DeprecatesMyDependency is P4's Edge 3, evaluated ONCE here rather
	// than at each read site: true when this artifact is a deprecation
	// announcement whose `deprecates:` names a contract listed in THIS
	// system's own consumes.yaml. addressedToMe reads it, so every caller
	// of that predicate — inbox, --actionable's condition 1, statusline,
	// overdue — inherits the rule from one evaluation instead of four
	// copies. This package has paid for one rule read in two places
	// enough times (see broadcastAckPermitted and contractVersionVerdict).
	DeprecatesMyDependency bool

	// FulfillingResponse is true when some response artifact in this space
	// names this one as its `parent`. internal/pendency's requirement row
	// splits on it (Input.HasFulfillingResponse), and it is resolved HERE
	// because the evidence lives on a different artifact.
	//
	// It deliberately does NOT read fold's Result.Responses, which was the
	// first attempt and could never work: fold populates that map only from
	// a `respond` event's ResponseID (fold.go's own respond branch), and
	// requirementRows carries no respond row — so a requirement's Responses
	// map is empty no matter how many responses actually name it. The split
	// the domain draws (§3.4.2: before a response the target owes the work,
	// after it the requester owes `satisfy`) was therefore unreachable in
	// production and lived only in a unit test. The `parent` field is the
	// fact; this reads the fact.
	FulfillingResponse bool

	// BlockedByOwner is P1's US-3 fact, resolved here for the same reason
	// FulfillingResponse is: the evidence (a response's `blocked_by.owner`)
	// lives on a DIFFERENT artifact than the one this field describes.
	// internal/pendency's `blocked` row reads it (Input.BlockedByOwner).
	//
	// Empty unless a response naming this artifact as `parent` carries a
	// non-empty `blocked_by.owner` AND that owner passed fold.CheckLegality
	// (P-2, checked against `note` — the floor "has ANY legal move on this
	// artifact" bar, since `unblock` itself is RoleTarget-only and would
	// refuse the very case this field exists for) against THIS artifact's
	// own envelope and current membership. A named owner who is neither
	// this envelope's `from` nor in its `to` fails that check and this
	// field stays empty — the caller-side narrowing pendency's own
	// BlockedByOwner doc comment describes.
	BlockedByOwner string

	// ParentDisputeReopenFailed is true, for a RESPONSE artifact only, when
	// the dispute event that put this response into `disputed` also
	// attempted, and failed, to reopen its parent to `in_progress` (fold.go's
	// D-024 comment: illegal when the parent was not `responded` at the
	// time — already closed, or already reopened by an earlier dispute).
	// internal/pendency's (response, disputed) row reads it
	// (Input.ParentDisputeReopenFailed) so its Why never claims a discharge
	// that never happened (spec 06's 2026-08-09 amendment).
	//
	// Resolved in the SAME closure-state overlay pass that already copies
	// the parent's Responses sub-state onto the response's own Result.State,
	// because the fact lives on the PARENT's own Result.Flags, not the
	// response's: applyResponseScoped (internal/fold/fold.go) appends the
	// failed-reopen Flag{Kind: FlagIllegalTransition, Subject: env.ID} to
	// the running Result it is called with, which is the PARENT's — the
	// response's own independent fold call never sees it, and cannot: its
	// own gatherEvents deliberately excludes verify/dispute from its own
	// stream (gatherEvents' own doc comment), so nothing in the response's
	// own Events/Result carries the dispute event at all.
	ParentDisputeReopenFailed bool

	// DeliveryUnresolvable is AC9's read half (spec 06 §8's 2026-08-09
	// amendment, from fb-20260808-d5740f): true when a response fulfilling
	// THIS question or work_request claims `result: delivered` while the
	// data package it names cannot be resolved in this space's own
	// mirror. internal/pendency's (question|work_request, responded) row
	// reads it (Input.DeliveryUnresolvable) so the computed next move
	// never hands the sender a close over an obligation nobody actually
	// discharged.
	//
	// A response carries no package reference of its own (spec 04's own
	// 2026-08-09 restatement — `response.schema.json` has no such field),
	// so this is resolved by correlating THIS artifact's id against every
	// handoff's own `fulfills[]` (cmd/a2a's dataHandoffEnvelope — the
	// artifact `a2a data deliver` actually writes the package reference
	// onto), then asking the SAME presence check `a2a data fetch` already
	// runs for each of that handoff's data-kind deliverables
	// (packageresolver.go's mirrorPackageResolver, composed via
	// delivery.go's already-shipped ResolveDeliveries/PackageResolver
	// rather than re-implemented here).
	//
	// False means either "no response here claims delivered" (the
	// overwhelmingly common case — this field is meaningless outside AC9's
	// one scenario) OR "at least one fulfilling handoff's data deliverable
	// resolved" — the two are not distinguished, the same fail-open
	// discipline every other caller-resolved fact in this struct
	// documents.
	DeliveryUnresolvable bool
}

func (f foldedArtifact) kind() fold.Kind { return fold.Kind(f.Env.Type) }

// buildIndex composes spaceID's full read-model: every artifact under
// dir's working tree, folded against its correctly-gathered event set
// (plan 07 Placement decision: parent events PLUS the events attached via
// that parent's own respond events — never a naive subject==id-only
// query, which silently misses verify/dispute).
func buildIndex(ctx context.Context, spaceID, dir, ownSystem string, manifest space.Manifest) ([]foldedArtifact, []SkippedFile, error) {
	artifacts, artifactSkips, err := walkArtifacts(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): walk artifacts: %w", spaceID, err)
	}
	events, eventSkips, err := walkEvents(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): walk events: %w", spaceID, err)
	}
	// skips is this space's SPACE-LEVEL fact (parallel to how OrderKnown
	// below is a space-level fact carried per-item): a skip can never be
	// "per-item" the way OrderKnown's per-artifact copy is, because the
	// whole point of a skip is that no folded item exists for it. Merging
	// two independently-sorted slices does not itself produce a sorted
	// slice, so this re-sorts the union rather than assuming order.
	skips := append(append([]SkippedFile{}, artifactSkips...), eventSkips...)
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	seq, err := commitOrder(ctx, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): commit order: %w", spaceID, err)
	}
	for i := range events {
		events[i].CommitSeq = seq[events[i].RelPath]
	}
	orderKnown := len(seq) > 0

	membership := membershipView(manifest)

	// myDependencies is Edge 3's own input: the contract ids THIS system
	// declares in its own consumes.yaml. An unreadable or absent registry
	// yields an empty set plus a reported skip, never an error — see
	// myDependencyContracts for why this direction of failure is the
	// opposite of the retire gate's.
	myDependencies, depSkip := myDependencyContracts(dir, ownSystem)
	if depSkip != nil {
		skips = append(skips, *depSkip)
		sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	}

	// parentOf: response artifact ID -> parent artifact ID
	// (response.schema.json's own `parent` field — the schema-grounded
	// fact this package composes over, rather than an invented refs[]
	// convention).
	parentOf := map[string]string{}
	// responsesBySeqAndParent: commit seq -> parent ID -> sorted response
	// IDs committed at that same seq. D-026 ("one commit, one event per
	// artifact") means a respond event on the parent and its paired
	// response artifact land in the SAME commit — that shared commit seq
	// is this package's correlation key (a schema-grounded fact, not an
	// invented convention). A batch submit committing >1 response to the
	// SAME parent in the SAME commit is a genuine ambiguity this
	// resolves best-effort (first response ID, deterministically sorted)
	// — see v1-min spec 07 §11.
	responsesBySeqAndParent := map[int64]map[string][]string{}
	// hasParentedResponse is parentOf read the other way round: "does any
	// response name ME". It is the requirement row's own split fact
	// (foldedArtifact.FulfillingResponse), built in the same pass rather than
	// by a second traversal.
	hasParentedResponse := map[string]bool{}
	// blockedOwnerCandidate: parent artifact ID -> the FIRST (walk order,
	// deterministic — filepath.WalkDir visits lexically) response's own
	// `blocked_by.owner` naming it. Legality is not checked here: this map
	// is only a candidate, resolved once membership and the parent's own
	// envelope are both in scope, below.
	blockedOwnerCandidate := map[string]string{}
	// deliveredResponseTo: parent artifact ID -> true when SOME response
	// naming it as `parent` claims `result: delivered` — AC9's read half
	// (spec 06 §8) own trigger condition. foldedArtifact.DeliveryUnresolvable
	// is meaningless (and stays false) for every parent not in this set.
	deliveredResponseTo := map[string]bool{}
	for _, a := range artifacts {
		if fold.Kind(a.Env.Type) == fold.KindResponse && a.Env.Parent != "" {
			parentOf[a.Env.ID] = a.Env.Parent
			hasParentedResponse[a.Env.Parent] = true
			if a.Env.Result == "delivered" {
				deliveredResponseTo[a.Env.Parent] = true
			}
			s := seq[a.RelPath]
			if responsesBySeqAndParent[s] == nil {
				responsesBySeqAndParent[s] = map[string][]string{}
			}
			responsesBySeqAndParent[s][a.Env.Parent] = append(responsesBySeqAndParent[s][a.Env.Parent], a.Env.ID)
			if a.Env.BlockedBy.Owner != "" {
				if _, exists := blockedOwnerCandidate[a.Env.Parent]; !exists {
					blockedOwnerCandidate[a.Env.Parent] = a.Env.BlockedBy.Owner
				}
			}
		}
	}
	for _, byParent := range responsesBySeqAndParent {
		for k := range byParent {
			sort.Strings(byParent[k])
		}
	}

	// handoffFulfills: fulfilled artifact ID -> every handoff artifact's own
	// raw bytes naming it in `fulfills[]` (cmd/a2a's dataHandoffEnvelope,
	// the artifact `a2a data deliver` writes the package reference onto —
	// AC9's read half correlates a `delivered` response back to bytes
	// through THIS field, because a response cannot itself name a package,
	// spec 04's own 2026-08-09 restatement).
	handoffFulfills := map[string][][]byte{}
	for _, a := range artifacts {
		if fold.Kind(a.Env.Type) != fold.KindHandoff {
			continue
		}
		for _, parentID := range decodeHandoffFulfills(a.Raw) {
			if parentID == "" {
				continue
			}
			handoffFulfills[parentID] = append(handoffFulfills[parentID], a.Raw)
		}
	}
	// packageResolver's participant set is never read by ResolvePackage
	// (the producing system is parsed out of the ref itself,
	// datapackage.ParsePackageID) — only ResolveReport uses it, and this
	// presence check never calls ResolveReport, so nil is exactly as
	// correct as the full manifest participant list would be.
	packageResolver := newMirrorPackageResolver(dir, nil)

	eventsBySubject := map[string][]fold.Event{}
	eventEvidence := make(map[string]provenance.EventEvidence, len(events))
	for _, re := range events {
		fe := fold.Event{
			ULID:         re.Ev.Event,
			CommitSeq:    re.CommitSeq,
			Subject:      re.Ev.Subject,
			Transition:   re.Ev.Transition,
			ClaimedState: fold.State(re.Ev.State),
			Actor:        fold.Actor{Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System},
			Version:      canonicalEventVersion(re.Ev.Version),
		}
		if re.Ev.Transition == fold.TRespond {
			if cands, ok := responsesBySeqAndParent[re.CommitSeq][re.Ev.Subject]; ok && len(cands) > 0 {
				fe.ResponseID = cands[0]
				responsesBySeqAndParent[re.CommitSeq][re.Ev.Subject] = cands[1:]
			}
		}
		eventsBySubject[fe.Subject] = append(eventsBySubject[fe.Subject], fe)
		eventEvidence[fe.ULID] = provenance.NewEventEvidence(
			re.Ev.State,
			provenance.Actor{
				Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System,
				Model: re.Ev.Actor.Model, Session: re.Ev.Actor.Session,
			},
			provenance.Producer{Tool: re.Ev.ProducedBy.Tool, Version: re.Ev.ProducedBy.Version},
		)
	}

	eventAt := make(map[string]time.Time, len(events))
	eventNotes := make(map[string]string, len(events))
	eventReasonCodes := make(map[string]string, len(events))
	for _, re := range events {
		if t, terr := time.Parse(time.RFC3339, re.Ev.At); terr == nil {
			eventAt[re.Ev.Event] = t
		}
		if re.Ev.Note != "" {
			eventNotes[re.Ev.Event] = re.Ev.Note
		}
		if re.Ev.ReasonCode != "" {
			eventReasonCodes[re.Ev.Event] = re.Ev.ReasonCode
		}
	}

	out := make([]foldedArtifact, 0, len(artifacts))
	receiptMismatches := map[string]ReceiptMismatch{}
	for _, a := range artifacts {
		if a.Env.ID == "" || a.Env.Type == "" {
			continue
		}
		env := fold.Envelope{
			ID:                a.Env.ID,
			Kind:              fold.Kind(a.Env.Type),
			From:              a.Env.From,
			To:                normalizeTo(a.Env.To),
			RequiredApprovers: a.Env.RequiredApprovers,
		}
		evs := gatherEvents(a.Env.ID, parentOf, eventsBySubject)
		result, mismatches, origin := foldWithReceiptEvidence(env.Kind, env, evs, membership, eventEvidence)
		for eventULID, mismatch := range mismatches {
			receiptMismatches[eventULID] = mismatch
		}

		var latest time.Time
		var latestEventSeq int64
		var latestEventID string
		hasLatestEvent := false
		var latestPublishSeq int64 = -1
		var latestPublishVersion string
		eventRefs := map[string][]refEntry{}
		for _, re := range events {
			if re.Ev.Subject != a.Env.ID {
				continue
			}
			if len(re.Ev.Refs) > 0 {
				eventRefs[re.Ev.Event] = append([]refEntry(nil), re.Ev.Refs...)
			}
			if t, terr := time.Parse(time.RFC3339, re.Ev.At); terr == nil {
				newer := !hasLatestEvent
				if orderKnown {
					newer = newer || re.CommitSeq > latestEventSeq || (re.CommitSeq == latestEventSeq && re.Ev.Event > latestEventID)
				} else {
					newer = newer || t.After(latest) || (t.Equal(latest) && re.Ev.Event > latestEventID)
				}
				if newer {
					hasLatestEvent = true
					latest = t
					latestEventSeq = re.CommitSeq
					latestEventID = re.Ev.Event
				}
			}
			if re.Ev.Transition == fold.TPublish && re.Ev.Version != "" && re.CommitSeq > latestPublishSeq {
				latestPublishSeq = re.CommitSeq
				latestPublishVersion = re.Ev.Version
			}
		}

		blockedByOwner := resolveBlockedByOwner(blockedOwnerCandidate[a.Env.ID], env, result.State, membership)

		out = append(out, foldedArtifact{
			SpaceID: spaceID, RelPath: a.RelPath, Raw: a.Raw, Digest: a.Digest,
			Env: a.Env, Result: result, Events: evs, EventEvidence: eventEvidence,
			ReceiptMismatches: receiptMismatches, LatestEventAt: latest,
			LatestEventSeq: latestEventSeq, LatestEventID: latestEventID,
			StateEventID: origin.EventULID, StateBy: origin.By, StateSince: eventAt[origin.EventULID],
			EventAt: eventAt, EventNotes: eventNotes, EventReasonCodes: eventReasonCodes, EventRefs: eventRefs, LatestPublishVersion: latestPublishVersion,
			Seq: seq[a.RelPath], OrderKnown: orderKnown,
			// Edge 3, evaluated once — see foldedArtifact's own comment.
			// The lookup is on the contract id alone; myDependencies is
			// empty for every system that consumes nothing, which makes
			// this false for every artifact, which is exactly today's
			// behaviour for such a system.
			DeprecatesMyDependency: myDependencies[a.Env.deprecatedContractID()],
			FulfillingResponse:     hasParentedResponse[a.Env.ID],
			BlockedByOwner:         blockedByOwner,
			DeliveryUnresolvable:   deliveryUnresolvable(a.Env.ID, deliveredResponseTo, handoffFulfills, packageResolver),
		})
	}

	// Response closure-state overlay: fold's own model (see
	// applyResponseScoped's doc comment in internal/fold/fold.go) is that
	// a response artifact carries NO separate envelope of its own for
	// verify/dispute authorization purposes — its authoritative
	// submitted/verified/disputed sub-state lives ONLY in its parent's
	// Result.Responses map (keyed by the response's own id), populated by
	// the SAME gather this function already performs for the parent.
	// A response artifact's own independent Fold call above therefore
	// only ever reaches create/submitted (RoleAny rows); this pass
	// overlays the parent's authoritative view onto the response's own
	// displayed State so `a2a show <response-id>` renders "verified"/
	// "disputed" rather than a stale "submitted" — cache's own
	// composition, not a second fold implementation (spec §5).
	byID := make(map[string]int, len(out))
	for i, fa := range out {
		byID[fa.Env.ID] = i
	}
	for respID, parentID := range parentOf {
		pIdx, ok := byID[parentID]
		if !ok {
			continue
		}
		rIdx, ok := byID[respID]
		if !ok {
			continue
		}
		if state, ok := out[pIdx].Result.Responses[respID]; ok {
			out[rIdx].Result.State = state
			if state == fold.StateDisputed {
				out[rIdx].ParentDisputeReopenFailed = parentReopenFailed(out[pIdx].Result.Flags, parentID, respID, eventsBySubject)
			}
		}
	}

	return out, skips, nil
}

// parentReopenFailed reports whether parentFlags (the PARENT's own
// Result.Flags) carries the failed-reopen flag applyResponseScoped raises
// for ONE OF respID's own dispute events — see foldedArtifact's own
// ParentDisputeReopenFailed doc comment for why the fact must be read off
// the parent's Flags rather than the response's own Result.
//
// Matched on (Kind, Subject, EventULID) together, never on Kind alone:
// applyResponseScoped raises FlagIllegalTransition on TWO distinct
// occasions with two distinct Subjects (fold.go) — Subject: event.Subject
// (the response id, a table miss on the response's OWN sub-state) and
// Subject: env.ID (the parent id, a failed reopen). Requiring
// Subject==parentID excludes the first; requiring EventULID to be one of
// respID's own dispute events excludes a different response's unrelated
// failed reopen on the SAME parent from being misattributed to this one.
func parentReopenFailed(parentFlags []fold.Flag, parentID, respID string, eventsBySubject map[string][]fold.Event) bool {
	disputeULIDs := make(map[string]bool)
	for _, ev := range eventsBySubject[respID] {
		if ev.Transition == fold.TDispute {
			disputeULIDs[ev.ULID] = true
		}
	}
	if len(disputeULIDs) == 0 {
		return false
	}
	for _, flag := range parentFlags {
		if flag.Kind == fold.FlagIllegalTransition && flag.Subject == parentID && disputeULIDs[flag.EventULID] {
			return true
		}
	}
	return false
}

// foldWithReceiptEvidence performs the same canonical (CommitSeq, ULID) replay
// as fold.Fold and captures evidence only when Apply itself appends the stable
// mismatch flag. This avoids reconstructing dynamic actual outcomes from the
// final result and avoids diagnosing duplicate/idempotent replays that Apply
// correctly treats as no-ops.
func foldWithReceiptEvidence(kind fold.Kind, env fold.Envelope, events []fold.Event, membership fold.MembershipView, evidence map[string]provenance.EventEvidence) (fold.Result, map[string]ReceiptMismatch, stateOrigin) {
	if len(events) == 0 {
		// The zero-events fallback is a state nothing produced — no event,
		// no actor, no instant. An empty origin is the honest answer, and
		// the read model renders it as absence rather than inventing a
		// timestamp from the artifact's own file.
		return fold.Fold(kind, env, nil, membership), nil, stateOrigin{}
	}
	sorted := append([]fold.Event(nil), events...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CommitSeq != sorted[j].CommitSeq {
			return sorted[i].CommitSeq < sorted[j].CommitSeq
		}
		return sorted[i].ULID < sorted[j].ULID
	})

	result := fold.NewResult(kind)
	var mismatches map[string]ReceiptMismatch
	var origin stateOrigin
	for _, event := range sorted {
		evaluation := fold.EvaluateCandidate(kind, result, event, env, membership)
		next := fold.Apply(kind, env, result, event, membership)
		// Which event produced the CURRENT state, observed rather than
		// inferred. Asking fold.TransitionFree instead would answer a
		// different question: a state-moving transition that was illegal or
		// unauthorized is flagged and changes nothing, and it must not
		// claim authorship of a state it never moved.
		if next.State != result.State {
			origin = stateOrigin{EventULID: event.ULID, By: event.Actor.System}
		}
		if evaluation.Applicable && event.ClaimedState != fold.StateNone &&
			event.ClaimedState != evaluation.Outcome && appendedReceiptMismatch(result, next, event.ULID) {
			if mismatches == nil {
				mismatches = map[string]ReceiptMismatch{}
			}
			eventEvidence := evidence[event.ULID]
			if eventEvidence.Receipt == "" {
				eventEvidence.Receipt = string(event.ClaimedState)
			}
			if eventEvidence.Actor.Kind == "" {
				eventEvidence.Actor.Kind = event.Actor.Kind
			}
			if eventEvidence.Actor.Name == "" {
				eventEvidence.Actor.Name = event.Actor.Name
			}
			if eventEvidence.Actor.System == "" {
				eventEvidence.Actor.System = event.Actor.System
			}
			mismatches[event.ULID] = ReceiptMismatch{
				Kind: string(fold.FlagStateClaimMismatch), EventULID: event.ULID, Subject: event.Subject,
				Scope:  ReceiptScope{Kind: string(evaluation.Scope.Kind), Subject: evaluation.Scope.Subject, Version: evaluation.Scope.Version},
				Actual: string(evaluation.Outcome), Claimed: string(event.ClaimedState),
				Actor: eventEvidence.Actor, Producer: eventEvidence.Producer, Cause: "unknown",
			}
		}
		result = next
	}
	return result, mismatches, origin
}

// stateOrigin identifies the event that produced an artifact's current
// folded state — distinct from its LATEST event, which may be a `note` or
// any other transition-free one.
//
// The dashboard rendered `movedAt` from the latest event of any kind and
// said the artifact moved when nothing had. Both facts are real and both
// are kept: LatestEvent* stays the activity clock, this is the state clock.
type stateOrigin struct {
	EventULID string
	By        string // the acting system, as the event recorded it
}

func appendedReceiptMismatch(before, after fold.Result, eventULID string) bool {
	for _, flag := range after.Flags[len(before.Flags):] {
		if flag.Kind == fold.FlagStateClaimMismatch && flag.EventULID == eventULID {
			return true
		}
	}
	return false
}

func receiptMismatchFor(fa foldedArtifact, eventULID string) *ReceiptMismatch {
	mismatch, ok := fa.ReceiptMismatches[eventULID]
	if !ok {
		return nil
	}
	return &mismatch
}

// gatherEvents assembles the FULL event set fold.Fold needs to compute
// id's correct Result: every event whose subject IS id (primary-scoped,
// including the respond event that seeds Result.Responses), PLUS every
// event whose subject is a response id known (via parentOf) to be
// attached to id — the verify/dispute events D-024's closure model
// requires fold to apply against the SAME running Result as the parent's
// own primary-scoped events (plan 07 Placement decision: "a naive
// subject==X-only query silently misses them").
// A RESPONSE artifact is the one case where "every event whose subject IS
// id" over-collects. Its own verify/dispute events are, by fold's explicit
// model (applyResponseScoped's doc comment), scoped to the PARENT: they
// authorize against the parent's owner and they write the parent's
// Result.Responses map. Handing them to the response's OWN independent fold
// makes that fold resolve RoleOwner against the response's `from` — the
// responder — who is never the party authorized to verify their own answer.
// The result was a spurious `unauthorized-actor` flag on EVERY verified
// response in the product, invisible because the closure-state overlay below
// only overwrites Result.State and never Result.Flags.
//
// That mattered more than a cosmetic wrong flag: spec 46's thread reader
// promises that a conversation carrying an illegal-transition or
// unauthorized-actor flag never renders clean, so a healthy, correctly
// verified exchange would have reported itself as suspect — the
// authoritative-looking wrong answer this phase exists to prevent, produced
// by the phase's own reader.
//
// Found while writing P46's e2e chain fixture, empirically: a two-line
// fold.Fold(KindResponse, respEnv, []Event{verify}) probe reproduces it with
// no cache involved at all.
func gatherEvents(id string, parentOf map[string]string, eventsBySubject map[string][]fold.Event) []fold.Event {
	own := eventsBySubject[id]
	if _, isResponse := parentOf[id]; isResponse {
		filtered := make([]fold.Event, 0, len(own))
		for _, ev := range own {
			if ev.Transition == fold.TVerify || ev.Transition == fold.TDispute {
				continue
			}
			filtered = append(filtered, ev)
		}
		own = filtered
	}
	out := append([]fold.Event(nil), own...)
	// Only the RESPONSE-SCOPED events cross over, which is the exact mirror
	// image of the filter above: verify/dispute leave the response's own
	// stream and join the parent's, and nothing else does. A response's own
	// create/submit belong to the response alone.
	//
	// Handing the parent everything was a real defect, and the one kind it
	// bit is the one kind with no `respond` verb. For a question or a
	// work_request, `a2a respond` authors the PARENT's own respond event and
	// no response-subject event at all, so the unfiltered append happened to
	// carry nothing extra. A requirement has no respond row (requirementRows
	// carries none), so its only shipped fulfilment path is
	// `a2a new response --field parent=<XR-id>` + `a2a submit` — and that
	// submit event, keyed on the response, was being applied against the
	// REQUIREMENT, which has no (requirement, *, submit) row. Result: a
	// permanent illegal-transition flag on the requirement's own thread, so
	// the domain's only prescribed way to fulfil a requirement left its
	// thread unable to render clean forever (spec 46's own promise, quoted
	// above). Found by W3c's requirement conformance path.
	for respID, parentID := range parentOf {
		if parentID != id {
			continue
		}
		for _, ev := range eventsBySubject[respID] {
			if ev.Transition == fold.TVerify || ev.Transition == fold.TDispute {
				out = append(out, ev)
			}
		}
	}
	return out
}

// resolveBlockedByOwner is P1's US-3 (AC4-transferred) legality gate:
// candidate is a fulfilling response's own `blocked_by.owner`
// (blockedOwnerCandidate, above), and it is returned unchanged ONLY when
// fold.CheckLegality says candidate has a legal move on env — checked
// against `note` (RoleEitherParty) rather than `unblock` itself, because
// unblock's own row is Role: RoleTarget always (table.go), so asking
// CheckLegality about `unblock` specifically would refuse every candidate
// this field exists to name. `note`'s RoleEitherParty is the narrowest
// available floor that still admits env.From (the common case this US
// names: "the requester is what blocks me") and any addressed target,
// while refusing a genuine bystander (P-2, spec 01 §6: "a third-party
// member: note is RoleEitherParty, so even a note is unauthorized").
//
// The check needs env AND state (arguments, not the FulfillingResponse
// pattern's zero extra input) because — unlike DeprecatesMyDependency and
// FulfillingResponse, which are pure existence facts — a legality verdict
// is a function of what the row's OWN artifact would let candidate do, and
// pendency itself may not read the manifest membership CheckLegality
// needs (Input's own doc comment). Doing it here, once, is this
// package's half of P-2 the same way membershipView's own read is: cache
// has the manifest, pendency never does.
func resolveBlockedByOwner(candidate string, env fold.Envelope, state fold.State, membership fold.MembershipView) string {
	if candidate == "" {
		return ""
	}
	status := membership(candidate)
	verdict := fold.CheckLegality(env.Kind, state, fold.TNote, env, fold.Actor{System: candidate}, status)
	if verdict != fold.VerdictLegal {
		return ""
	}
	return candidate
}

// handoffFulfillsProbe decodes a handoff artifact's own `fulfills[]` field —
// a package-local, single-consumer decode (internal/datapackage's own
// DecodeDeliverables, deliverable.go, draws the identical probe-a-single-
// field idiom one package over, for the same reason: `fulfills[]` is
// outside this wave's allowlist — envelopeProbe has no field for it and
// decode.go is not in this brief's allowlist).
type handoffFulfillsProbe struct {
	Fulfills []string `yaml:"fulfills"`
}

// decodeHandoffFulfills best-effort decodes raw (a handoff artifact's own
// committed bytes, the same bytes foldedArtifact.Raw/rawArtifact.Raw
// carries) into its `fulfills[]` array — cmd/a2a's
// dataHandoffEnvelope.Fulfills, the one field this package needs from a
// handoff to correlate it back to the exchange it answers (AC9, spec 06
// §8's 2026-08-09 amendment). A document that fails to parse, or simply
// carries no `fulfills[]`, decodes to nil — the same best-effort, no
// second error path convention walkArtifacts itself already uses.
func decodeHandoffFulfills(raw []byte) []string {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil
	}
	var probe handoffFulfillsProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil
	}
	return probe.Fulfills
}

// deliveryUnresolvable is foldedArtifact.DeliveryUnresolvable's own
// resolver (AC9, spec 06 §8). It is true for exactly one situation: a
// fulfilling handoff EXISTS in this space, names a data-kind deliverable,
// and that deliverable's package does not resolve. A dangling reference —
// which is worse than no reference, in spec 04's own words.
//
// NARROWED 2026-08-10, and the reason is a real semantic error the
// conformance matrix caught that every unit test in its wave agreed with.
// The first version also returned true when NO handoff fulfilled parentID,
// on the reasoning that "a payload PR that never merged is exactly a
// handoff this space never received". That reasoning rests on
// `result: delivered` meaning "bytes were promised" — and it does not.
// `delivered` is simply the natural result word for a work_request, driven
// by SIX call sites in the conformance catalogue on ordinary exchanges with
// no data package anywhere near them. So the first version flipped the
// next move on every plain answered work_request, and five declared paths
// went red saying so: `waiting_on=[bravo] want [alpha]`,
// `expected_transition "respond" want "close"`.
//
// What this costs, stated rather than glossed: the incident AC9 was written
// from (fb-20260808-d5740f) is the case where the response merged and the
// deliver PR carrying BOTH the handoff and the package stayed open. No
// handoff, so this returns false, so that exact incident is NOT covered by
// this half. It cannot be — nothing in the space distinguishes "delivered,
// bytes pending" from "delivered, no bytes were ever involved" until a
// response can name its package, which is the same wire decision spec 04's
// §11 already defers AC9's submit half on. Both halves wait on it; this
// half ships the subset that is decidable today, which is the partial-write
// shape: the handoff landed and its payload did not.
func deliveryUnresolvable(parentID string, delivered map[string]bool, handoffFulfills map[string][][]byte, resolver PackageResolver) bool {
	if !delivered[parentID] {
		return false
	}
	// A data-kind deliverable must EXIST before its absence can mean
	// anything. `sawDataDeliverable` is what makes "no handoff at all" and
	// "a handoff whose package is missing" different answers instead of the
	// same one.
	sawDataDeliverable := false
	for _, raw := range handoffFulfills[parentID] {
		deliverables, err := datapackage.DecodeDeliverables(raw)
		if err != nil {
			continue
		}
		for _, d := range deliverables {
			if d.Kind != datapackage.DeliverableKindData {
				continue
			}
			sawDataDeliverable = true
			if _, ok := resolver.ResolvePackage(d.Ref); ok {
				return false
			}
		}
	}
	return sawDataDeliverable
}

// membershipView adapts a space.Manifest's participant list into a
// fold.MembershipView (D-017: membership resolved against the manifest,
// cache reads it once per space rather than per-commit — a known
// simplification vs. "as of the event's own commit"; see v1-min spec 07
// §11).
func membershipView(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, p := range manifest.Participants {
			if p.System == system {
				if p.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

// walkArtifacts walks dir for every artifact-candidate *.md file (excluding
// .git, vendored/, and the space infrastructure paths the write funnel owns),
// best-effort decoding each as an envelope/v1 document — a
// file that fails to parse is silently skipped from the returned
// []rawArtifact, never fails the whole walk (mirrors internal/cli's
// MirrorResolver.ensureIndex convention), but is reported (never dropped
// without a trace) via the returned []SkippedFile — see skipped.go's own
// doc comment for why that report exists at all.
func walkArtifacts(dir string) ([]rawArtifact, []SkippedFile, error) {
	var out []rawArtifact
	var skips []SkippedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skips = append(skips, SkippedFile{Path: reportPath(dir, path), Reason: SkipReasonUnreadable})
			return nil //nolint:nilerr // reason: best-effort walk — skip an inaccessible entry, don't abort the whole walk (see func doc)
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendored" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		// Space infrastructure is not an artifact and must not poison the
		// diagnostic whose whole job is to distinguish a genuinely malformed
		// artifact from a missing one. Use the write funnel's exported predicate
		// rather than teaching the read walk a second infrastructure list.
		rel, relErr := filepath.Rel(dir, path)
		if relErr == nil && filepath.ToSlash(rel) == "README.md" {
			return nil
		}
		if relErr == nil && space.IsInfrastructurePath(filepath.ToSlash(rel)) {
			return nil
		}
		a, skip := decodeArtifactFile(dir, path)
		if skip != nil {
			skips = append(skips, *skip)
			return nil
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	return out, skips, nil
}

// decodeArtifactFile attempts every stage of one candidate *.md file's
// best-effort decode (path relativization, bounded read, frontmatter
// split, envelope decode, `id` presence) and returns EITHER the
// successfully decoded rawArtifact OR a SkippedFile naming the first stage
// that rejected it — never both. Extracted from walkArtifacts as its own
// function so each stage's failure path is independently testable without
// going through a real filepath.WalkDir traversal.
func decodeArtifactFile(dir, path string) (rawArtifact, *SkippedFile) {
	rel, relErr := filepath.Rel(dir, path)
	if relErr != nil {
		return rawArtifact{}, &SkippedFile{Path: path, Reason: SkipReasonUnrelativizable}
	}
	relSlash := filepath.ToSlash(rel)

	raw, rerr := readBounded(path, maxCacheReadBytes)
	if rerr != nil {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUnreadable}
	}

	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		reason := SkipReasonNotFrontmatterShaped
		if errors.Is(ferr, artifact.ErrMalformedFrontmatter) {
			// The delimiter pair IS present and well-formed; it is the YAML
			// inside it that fails to decode (e.g. a duplicate mapping key)
			// — that is an undecodable-YAML fact, not a "not shaped like
			// frontmatter at all" one (see SkipReasonUndecodableYAML's doc).
			reason = SkipReasonUndecodableYAML
		}
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: reason}
	}

	env, everr := decodeEnvelope(fm.YAML)
	if everr != nil {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUndecodableYAML}
	}
	if env.ID == "" {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonNoID}
	}
	return rawArtifact{RelPath: relSlash, Raw: raw, Env: env, Digest: artifact.Digest(raw)}, nil
}

// walkEvents walks dir for every committed event/v1 YAML file under any
// system's events/ directory (best-effort skip on decode failure, same
// convention — and same skip-reporting — as walkArtifacts).
func walkEvents(dir string) ([]rawEvent, []SkippedFile, error) {
	var out []rawEvent
	var skips []SkippedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skips = append(skips, SkippedFile{Path: reportPath(dir, path), Reason: SkipReasonUnreadable})
			return nil //nolint:nilerr // reason: best-effort walk — skip an inaccessible entry, don't abort the whole walk (see func doc)
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			skips = append(skips, SkippedFile{Path: path, Reason: SkipReasonUnrelativizable})
			return nil //nolint:nilerr // reason: best-effort walk — an unrelativizable path is silently skipped (but reported, see skipped.go), not fatal (see func doc)
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.Contains(relSlash, "/events/") {
			return nil
		}
		ev, skip := decodeEventFile(path, relSlash)
		if skip != nil {
			skips = append(skips, *skip)
			return nil
		}
		out = append(out, ev)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	return out, skips, nil
}

// decodeEventFile is walkEvents' own per-file stage runner (bounded read,
// event decode, `event` id presence) — same "one function, independently
// testable, no real WalkDir needed" shape as decodeArtifactFile.
func decodeEventFile(path, relSlash string) (rawEvent, *SkippedFile) {
	raw, rerr := readBounded(path, maxCacheReadBytes)
	if rerr != nil {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUnreadable}
	}
	ev, everr := decodeEvent(raw)
	if everr != nil {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUndecodableYAML}
	}
	if ev.Event == "" {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonNoID}
	}
	return rawEvent{RelPath: relSlash, Ev: ev}, nil
}

// reportPath best-effort relativizes path against dir for a SkippedFile
// report where the walk has already failed before this package's own
// filepath.Rel-based decode stage would run (the filepath.WalkDir
// traversal-error branch) — falls back to the raw path so the report is
// never simply dropped, even though it may then be absolute rather than
// space-relative like every other SkippedFile.Path.
func reportPath(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// commitOrder recovers D-017's first-parent commit order on `main` for
// every path in dir's history, in exactly ONE git subprocess call (never
// a per-file call — the statusline <100ms budget and every other verb's
// responsiveness depends on this). A path's sequence number is the index
// of the FIRST commit that introduced it (event/artifact files are
// committed exactly once and never modified thereafter, so "first" and
// "only" coincide). An empty/absent history (fresh clone with nothing on
// main yet, or a non-git dir in a test double) returns an empty map
// rather than an error — every event then falls back to ULID-only
// ordering, a documented degradation, not a hard failure.
func commitOrder(ctx context.Context, dir string) (map[string]int64, error) {
	out, err := runGitOutput(ctx, dir, "log", "--first-parent", "--reverse", "--name-only", "--format=%x02%H")
	if err != nil {
		return map[string]int64{}, nil //nolint:nilerr // reason: absent/failed git history degrades to ULID-only ordering by design (see func doc)
	}
	seq := map[string]int64{}
	var idx int64
	for _, chunk := range strings.Split(out, "\x02") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		for _, p := range lines[1:] {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, exists := seq[p]; !exists {
				seq[p] = idx
			}
		}
		idx++
	}
	return seq, nil
}

// runGitOutput runs `git <args...>` with cwd=dir via explicit argv (never
// sh -c), returning stdout on success — this package's own copy of the
// same minimal git-plumbing helper internal/space/mirror.go keeps
// unexported to that package.
func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cache: git %v: %w: %s", args, err, stderr.String())
	}
	return out.String(), nil
}

// readBounded reads path with a size cap (rails: bounded reads
// everywhere).
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("cache: %s exceeds %d byte read bound", path, max)
	}
	return raw, nil
}
