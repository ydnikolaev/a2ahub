package cache

import (
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// RuleIdentity is the cache-carried identity of the pendency row that produced
// an Item verdict. The alias keeps consumers on cache's read-model boundary;
// they do not need a direct import of the rule-owning pendency package.
type RuleIdentity = pendency.RuleIdentity

// DefaultStatuslineTTL is §7.5's default cache-age TTL (5 minutes) that
// triggers a detached background refresh.
const DefaultStatuslineTTL = 5 * time.Minute

// DefaultStalenessSLA is OP-208's default "no event for the space's
// staleness SLA" window (7 days) when space.yaml carries no override.
const DefaultStalenessSLA = 7 * 24 * time.Hour

// Item is the JSON shape `a2a inbox`/`a2a outbox`/`a2a search`/`a2a
// contracts` all guarantee (OP-207/OP-208 "JSON output guaranteed";
// snake_case tags, the P6/validate convention). Reasons names which
// normative condition(s) matched — debuggability, not part of the
// guaranteed-stable core fields.
type Item struct {
	Space    string   `json:"space"`
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	From     string   `json:"from"`
	To       []string `json:"to,omitempty"`
	State    string   `json:"state"`
	Priority string   `json:"priority,omitempty"`
	Blocking bool     `json:"blocking,omitempty"`
	NeededBy string   `json:"needed_by,omitempty"`
	Thread   string   `json:"thread,omitempty"`
	New      bool     `json:"new"`
	Reasons  []string `json:"reasons,omitempty"`
	// Overdue and ActivationOwed are dashboard-only semantic projections of
	// typed attention facts. Presentation branches on these booleans rather
	// than classifying raw reason-code strings; the stable inbox/outbox JSON
	// contract remains unchanged.
	Overdue        bool `json:"-"`
	ActivationOwed bool `json:"-"`

	// PendingMerge is true when a submitted-but-not-yet-visible-as-merged
	// marker exists for this artifact (the pending-merge overlay, §7.2
	// OP-205's "local cache marks pending-merge" step).
	PendingMerge bool `json:"pending_merge,omitempty"`
	// SyncStale is true when the item's own space mirror's sync-age
	// exceeds the statusline TTL — surfaced so `a2a inbox`/`outbox`
	// callers know the data may be behind (T1: "works offline ... with
	// sync age flagged when stale").
	SyncStale bool `json:"sync_stale,omitempty"`
	// CreatedAt and CreatedSeq identify when this artifact first entered the
	// space. They deliberately do not change when a later lifecycle event is
	// appended: Exchange uses them to keep the document feed chronological,
	// while LatestEvent* remains the activity/history clock.
	CreatedAt         time.Time `json:"-"`
	CreatedSeq        int64     `json:"-"`
	CreatedOrderKnown bool      `json:"-"`
	// LatestEventAt is the timestamp of the artifact's most recent folded
	// event — the "pending since" anchor. Zero when the artifact has no events
	// yet (a bare draft). `json:"-"` deliberately: the dashboard assembler reads
	// this as a Go field (formats it to an age like "5d"), while inbox/outbox
	// `--json` stay byte-stable for their existing consumers (a time.Time would
	// not honor omitempty anyway).
	LatestEventAt time.Time `json:"-"`
	// LatestEventSeq is the committed-order tie-breaker for LatestEventAt.
	// It identifies the same event as LatestEventID and remains outside the
	// stable inbox/outbox JSON contract.
	LatestEventSeq int64 `json:"-"`
	// LatestEventID is the current transition identity for local
	// notification projections. It stays outside the established inbox JSON
	// contract, just like LatestEventAt.
	LatestEventID string `json:"-"`
	// Description is a short human-readable summary from the artifact's body —
	// the "what is this" line the dashboard shows under an inbox/outbox item.
	// json:"-" (like LatestEventAt): a dashboard-only Go field, so inbox/outbox
	// `--json` stay byte-stable for their existing consumers.
	Description string `json:"-"`

	// Outcome and Terminal are the domain's own answer to what this state
	// MEANS and whether anything can follow it — fold.OutcomeOf and
	// fold.Terminal, computed once here instead of four times by four
	// renderers from three kind-agnostic literal sets.
	//
	// These GO ON THE WIRE, unlike LatestEventAt and Description above. The
	// byte-stability convention those fields honour is real but external:
	// no in-repo gate enforces it, there is no golden fixture over inbox,
	// outbox or show JSON, and `pending_merge`, `sync_stale` and `new` all
	// arrived exactly this way. seam.md §3 takes that decision explicitly
	// rather than inheriting it.
	//
	// Outcome is NOT isOpen (openStates, below). They disagree in one cell
	// and both are right: handoff/rejected is open there — §3.4.5 puts the
	// producer on the hook to resubmit — and refused here, because the
	// verification did not pass.
	Outcome  fold.Outcome `json:"outcome,omitempty"`
	Terminal bool         `json:"terminal"`
	// StateSince, StateBy and StateEvent carry the event that produced
	// State: when, by which system, and which event. Distinct from
	// LatestEvent* above, which is the ACTIVITY clock and moves for a
	// transition-free `note` too. The dashboard rendered the activity clock
	// under a "moved" label and told readers an artifact had moved when a
	// note was all that happened.
	//
	// `omitzero`, not `omitempty`: the latter has never applied to a struct
	// value, so a zero time would serialize as "0001-01-01T00:00:00Z" on
	// every artifact whose state nothing produced. seam.md §3 froze
	// `omitempty` and carries the amendment.
	StateSince time.Time `json:"state_since,omitzero"`
	StateBy    string    `json:"state_by,omitempty"`
	StateEvent string    `json:"state_event,omitempty"`
	// YourMove is the canonical "whose move is it" projection for this
	// artifact. It is dashboard-only for now: the stable inbox/outbox JSON
	// shape remains unchanged while presentation code stops inferring action
	// ownership from blocking/deadline labels.
	YourMove bool `json:"-"`
	// WaitingOn and ExpectedTransition mirror cache.OpenItem's own
	// identically-named fields — the pendency relation's verdict.Owners
	// and verdict.Expected for this artifact right now: WHO is owed a
	// move, and which transition. They exist so a consumer aggregating
	// many Items (the dashboard's exchange-overlay edges) can tell an
	// artifact that is merely LIVE (isOpen) from one where somebody
	// actually OWES the next move — Blocking above is a priority/gate
	// flag on the envelope, never a pendency verdict, and conflating the
	// two is the defect these fields exist to end (internal/pendency
	// stays the one place that computes the verdict; I7).
	//
	// Store.Inbox and Store.Outbox populate both fields unconditionally from
	// the same pendency.Resolve verdict their reason filters consume. The
	// verdict is computed once from the folded facts already in hand, then
	// projected onto Item at the two construction sites; internal/html only
	// copies that answer and never resolves pendency again. The JSON tags below
	// put the populated fields on inbox/outbox's wire shape, so Go-typed and
	// JSON consumers receive the same answer.
	WaitingOn          []string `json:"waiting_on,omitempty"`
	ExpectedTransition string   `json:"expected_transition,omitempty"`
	// Why is the pendency relation's own justification, ALWAYS populated —
	// "nobody owes anything" is a claim that has to be justified, never a
	// fall-through. `a2a thread --json` has carried it since the relation
	// shipped; inbox and outbox showed the conclusion and withheld the
	// reasoning, which republishes the ambiguity the relation was built to
	// remove.
	Why string `json:"why,omitempty"`
	// HumanGate is the pendency verdict's typed gate identity, carried from
	// the same Resolve call as WaitingOn/ExpectedTransition/Why. Empty means
	// the owed move is not human-gated.
	HumanGate string `json:"human_gate,omitempty"`
	// RuleIdentity is the typed pendency-table row that produced WaitingOn,
	// ExpectedTransition, Why, and HumanGate. The dashboard composer consumes
	// it to reject unknown rule/reason pairs; it is deliberately not another
	// stable inbox/outbox JSON field.
	RuleIdentity pendency.RuleIdentity `json:"-"`
	// OperationalItems is spec 05 AC4's per-item x_operational[] projection
	// (mirror.go's foldedArtifact.OperationalItems, itself
	// DeriveOperationalItems' own output) — carried whole, never re-derived
	// here, exactly like cache.OpenItem's identically-named field. nil for
	// every non-contract artifact (mirror.go gates the derivation on
	// fold.KindContract); for a contract carrying no declaration at all this
	// is still non-empty — every well-known name reads `undeclared`, never
	// omitted (P-1: silence is a live state, not an absence of information).
	// AC4 names `inbox`/`outbox` among the five surfaces that must read
	// `undeclared` distinctly from `absent`; this field is what closes the
	// two of the five that could not carry it before (epic-backlog B25).
	OperationalItems []OperationalItem `json:"operational_items,omitempty"`
}

// SpaceSyncInfo is the mirror snapshot fact the dashboard needs per connected
// space: exact HEAD revision plus freshness. The computation stays in cache
// because git inspection, mirrorSyncAge and Store.ttl are cache-owned policy;
// renderers must not re-read .git or duplicate the stale threshold.
type SpaceSyncInfo struct {
	Space    string
	Revision string
	Age      time.Duration
	Synced   bool
	Stale    bool
}

// RefFact is one envelope `refs[]` entry's resolved digest/staleness
// FACT (never a registry code — internal/cache stays validate-free per
// ADR-001; internal/cli/cmd_show.go maps this to the V5 registry code).
type RefFact struct {
	Ref            string `json:"ref"`
	ID             string `json:"id"`
	Version        string `json:"version,omitempty"`
	PinnedDigest   string `json:"pinned_digest,omitempty"`
	ResolvedDigest string `json:"resolved_digest,omitempty"`
	Resolved       bool   `json:"resolved"`
	DigestMismatch bool   `json:"digest_mismatch"`
}

// EventSummary is one folded event, for `a2a show`'s event-history
// rendering.
type EventSummary struct {
	ULID         string              `json:"ulid"`
	Subject      string              `json:"subject"`
	Transition   string              `json:"transition"`
	ClaimedState string              `json:"claimed_state,omitempty"`
	ActorKind    string              `json:"actor_kind,omitempty"`
	Actor        string              `json:"actor"`
	ActorSystem  string              `json:"actor_system"`
	ActorModel   string              `json:"actor_model,omitempty"`
	ActorSession string              `json:"actor_session,omitempty"`
	ProducedBy   provenance.Producer `json:"produced_by,omitzero"`
	Consistency  *ReceiptMismatch    `json:"consistency,omitempty"`
	At           time.Time           `json:"at"`
	Note         string              `json:"note,omitempty"`
	// ReasonCode is the event's machine-readable reason. `a2a show` is the
	// command an agent runs to inspect WHY something was declined, and the
	// MCP decline tool refuses a call without this field — so it reaching
	// the transcript but not here left the highest-traffic surface with the
	// prose note as its only actionable content, which is the substitution
	// the code exists to end.
	ReasonCode string `json:"reason_code,omitempty"`
	// TransitionFree marks an event that changes no artifact state — `note`
	// everywhere (D-025) and `acknowledge` on an announcement. Derived from
	// fold.TransitionFree, the same registry Apply dispatches on, so a
	// reader never has to infer it from the transition name: `acknowledge`
	// moves a requirement and moves nothing on a broadcast, and a name-only
	// guess is wrong for one of the two.
	TransitionFree bool `json:"transition_free,omitempty"`
	// Verdicts is F2's own gap (agent-exchange-2026-08 wave 36): event/v2's
	// `verdicts[]` DOES already reach cache.TranscriptEvent (threadview.go)
	// for the thread transcript, via decode.go's eventVerdictEntry and
	// mirror.go's own EventVerdicts side table (foldedArtifact) — but never
	// reached THIS type, so the artifact-detail event list (`a2a show`,
	// the dashboard detail panel) has no per-criterion verdict at all.
	// TranscriptVerdict (threadview.go, exported) is reused rather than a
	// second {index, verdict, cause_owner} type, the same "one exported
	// read-model shape" idiom that struct's own doc comment states.
	//
	// Populated in Store.buildShowResult (store.go), the one place that
	// builds an EventSummary literal, off the SAME
	// foldedArtifact.EventVerdicts that loop already reads for Note/
	// ReasonCode, through the SAME transcriptVerdicts conversion
	// threadview.go uses for its own TranscriptEvent.
	//
	// It does NOT yet reach html.ArtifactDetailEvent, and that ordering is
	// deliberate rather than forgotten: a field wired to the html surface
	// before its source existed would have been an always-nil slice,
	// indistinguishable from "no verdicts on this event" — the dead-prop
	// defect this wave's own C7 gate (internal/html/template_render_test.go)
	// exists to make unmergeable. The source exists now, so the html half
	// and its rendering are a normal follow-up, tracked as F2 in the epic's
	// wave log.
	Verdicts []TranscriptVerdict `json:"verdicts,omitempty"`
}

// ReceiptScope names the one scalar fold result compared with a producer's
// optional event.state receipt.
type ReceiptScope struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Version string `json:"version,omitempty"`
}

// ReceiptMismatch is non-blocking consistency evidence. Actual is the
// authoritative fold outcome; Claimed is retained only for comparison.
type ReceiptMismatch struct {
	Kind      string              `json:"kind"`
	EventULID string              `json:"event_ulid"`
	Subject   string              `json:"subject"`
	Scope     ReceiptScope        `json:"scope"`
	Actual    string              `json:"actual"`
	Claimed   string              `json:"claimed"`
	Actor     provenance.Actor    `json:"actor"`
	Producer  provenance.Producer `json:"producer,omitzero"`
	Cause     string              `json:"cause"`
}

// ClassificationSummary is cache's policy-free, per-space fact for the P4
// doctor consumer: the highest successfully decoded artifact classification
// plus enough bounded evidence to know whether the scan was complete.
type ClassificationSummary struct {
	Space           string        `json:"space"`
	Highest         string        `json:"highest,omitempty"`
	DecodedCount    int           `json:"decoded_count"`
	SkippedCount    int           `json:"skipped_count"`
	IncompleteCount int           `json:"incomplete_count"`
	Complete        bool          `json:"complete"`
	Skipped         []SkippedFile `json:"skipped,omitempty"`
	IncompletePaths []string      `json:"incomplete_paths,omitempty"`
}

// ShowResult is `a2a show <ref>`'s full output shape (OP-209): artifact
// body + folded state + event list + facts a V5 code lookup needs.
type ShowResult struct {
	Space string `json:"space"`
	// Path is the space-relative canonical artifact path. It is dashboard-only:
	// the stable CLI JSON contract does not need a repository layout detail,
	// while a human-facing source link must point at the exact committed file.
	Path  string   `json:"-"`
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Title string   `json:"title"`
	From  string   `json:"from"`
	To    []string `json:"to,omitempty"`
	State string   `json:"state"`
	Body  string   `json:"body"`
	// Thread is the §3.8 conversation this artifact belongs to. Surfaced by
	// `a2a show` so an agent holding one id can reach the whole exchange —
	// without it, the only way to find a conversation is to already know its
	// thread id, which is the discovery dead-end spec 46 records as D6.
	Thread    string         `json:"thread,omitempty"`
	Digest    string         `json:"digest"`
	Events    []EventSummary `json:"events"`
	Flags     []string       `json:"flags,omitempty"`
	Refs      []RefFact      `json:"refs,omitempty"`
	SyncStale bool           `json:"sync_stale"`
	SyncAge   string         `json:"sync_age,omitempty"`
	// MoveOwed is the per-document fact that decides an outcome for the two
	// pairs where state alone cannot: a published announcement and a published
	// contract are SETTLED unless somebody still owes a move on them
	// (docs/inbox/defects/01-open-without-a-waiter.md). toItem has read it
	// since that defect was fixed; ShowResult did not carry it, so the
	// artifact-detail projection fell back to fold.OutcomeOf and answered
	// "open" for exactly those pairs — the same document reading "Settled" in
	// the list and "Awaiting a move" in the pane beside it, on six of axon's
	// twenty-five documents.
	MoveOwed bool `json:"-"`
	// Attachments is the typed projection of the artifact's own committed
	// attachments[] (schemas/envelope/v2/work_request.schema.json), one
	// datapackage.AttachmentClaim per entry — ref/digest/role/conforms_to/
	// verification/retention/expires_at PLUS the two derived reader-facing
	// claims (VerificationClaim, Lapsed/LapsedOn/LapseClaim). Populated in
	// Store.buildShowResult using the Store's own injected Clock, never
	// time.Now(), so the lapse verdict is deterministic. Spec 04 §11's
	// 2026-08-09 amendment ("the attachment claim is structured, not
	// rendered-only"): this field is what makes `a2a show --json`, MCP and
	// the dashboard all carry the SAME derivation `a2a show`'s human-text
	// branch used to compute and print, and only print.
	//
	// `omitempty`, like Refs/Flags above: an artifact with no attachments[]
	// produces byte-identical `a2a show --json` output to before this field
	// existed.
	Attachments []datapackage.AttachmentClaim `json:"attachments,omitempty"`
	// Envelope is the heterogeneous, schema-owned frontmatter projection the
	// HTML detail panel needs. It remains outside `a2a show --json` so that
	// command's established output contract stays byte-compatible. Values are
	// untrusted presentation data; renderers must treat them as text only.
	Envelope map[string]any `json:"-"`
}

// ContractInfo is one entry in `a2a contracts`' listing (OP-221 second
// clause): provider, version, state — resolved via publish events (D-023).
type ContractInfo struct {
	Space    string `json:"space"`
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
	State    string `json:"state"`
	// Contract-specific envelope facts are dashboard-only today. json:"-"
	// preserves the established `a2a contracts --json` shape while allowing
	// the richer HTML assembler to stop guessing whether a contract is
	// code-generated and what compatibility vocabulary it declares.
	Category      string `json:"-"`
	SchemaFormat  string `json:"-"`
	CompatPolicy  string `json:"-"`
	GeneratedTool string `json:"-"`
	SourceDigest  string `json:"-"`
	// NonAdoptable is F4's own pre-`a2a contract adopt` visibility fact
	// (agent-exchange-2026-08 wave 36): mirror.go's own xBindingProbe
	// decode of the descriptor's `x_binding` field, off the same raw bytes
	// Description above already reads — the SAME refusal
	// internal/cli/cmd_contract.go's `contract adopt` computes before
	// pinning, surfaced here so a reader can see it will refuse before
	// running the command. json:"-" for the same reason as the fields
	// above: `a2a contracts --json` stays byte-stable, the HTML assembler
	// reads this as a Go field.
	//
	// Populated in Store.Contracts (store.go), the one place that builds a
	// ContractInfo literal, off the same foldedArtifact that loop already
	// has in scope. False is the correct reading of a descriptor that never
	// declared `x_binding` at all — P-1's own undeclared-is-not-a-refusal
	// default — so the surface never invents a refusal, and the dashboard
	// deliberately shows no affirmative "adoptable" badge for the same
	// reason: false is silence, not a claim.
	NonAdoptable bool `json:"-"`

	// OperationalItems is the contract's own x_operational[] projection —
	// carried whole from foldedArtifact.OperationalItems, mirror.go's
	// DeriveOperationalItems output, never re-derived here. Same discipline
	// cache.Item.OperationalItems already follows.
	//
	// It exists because the Contracts SCREEN had no seat for the fact: a
	// contract could declare `endpoint: absent` in machine-readable form and
	// the card a reader goes to for "what does this give me" could not say so
	// (defects-fix-2026-08 P8, docs/inbox/defects/09).
	OperationalItems []OperationalItem `json:"-"`
	// Description is a short human-readable summary from the contract's body,
	// for the dashboard's dependency map. json:"-" keeps `a2a contracts --json`
	// byte-stable for its existing consumers (read as a Go field only).
	Description string `json:"-"`
	// Versions is the ROLLING WINDOW: every version this contract has ever
	// published, with the state it holds now, oldest first (P4,
	// agent-ops-2026-07). `Version`/`State` above are the summary — newest
	// published version, and the subject-level projection over these — and
	// they stay exactly what they were. This is the detail neither of them
	// can carry: "1.0 retired, 1.2 published, 2.0 published" is three facts,
	// and a reader shown only the projection cannot tell a contract with one
	// live version from one with three.
	//
	// `omitempty`, so a contract whose history records no version at all —
	// the shape every history had before P4 — produces byte-identical
	// `a2a contracts --json` output. For a contract that HAS versions this
	// is a new field in that output, which is additive and deliberate: a
	// consumer that could not see the window could not act on it.
	Versions []ContractVersion `json:"versions,omitempty"`
}

// ContractVersion is one version of a contract and the state it holds, for
// ContractInfo.Versions. A struct rather than a map so the order is part of
// the value — semver ascending, which a map cannot express and which every
// renderer would otherwise have to re-derive.
type ContractVersion struct {
	Version       string `json:"version"`
	State         string `json:"state"`
	Sunset        string `json:"sunset,omitempty"`
	Successor     string `json:"successor,omitempty"`
	DeprecationID string `json:"deprecation_id,omitempty"`
}

// ProtocolFlagInfo is one committed fold violation surfaced by the read
// model. It deliberately carries fold's own stable vocabulary rather than
// pretending to be a V4/V5 validation result: those invocations are a
// different engine and require their own mounted context.
type ProtocolFlagInfo struct {
	Space       string
	System      string
	Artifact    string
	Code        string
	EventULID   string
	Message     string
	Severity    string
	Source      string
	Consistency *ReceiptMismatch
}

// SearchFilters narrows `a2a search`'s free-text match.
type SearchFilters struct {
	Type  string
	Space string
	State string
	// Thread narrows to one conversation. It lives HERE rather than as a
	// client-side pass over the returned items so both surfaces filter with
	// one piece of code — the CLI and MCP read paths are otherwise
	// independent by ADR-001, and a filter written twice is a filter that
	// can disagree with itself.
	Thread string
}

// openStates enumerates, per kind, every State internal/fold/table.go's
// own rows treat as non-terminal ("any open state", OP-207 condition 4 /
// OP-208's own base "open items" scope) — a deliberate per-kind
// allowlist (rather than a blanket "not in {closed,...}" deny-list) so
// this stays correct if a kind's terminal-state set ever grows
// asymmetrically.
var openStates = map[fold.Kind]map[fold.State]bool{
	fold.KindContract:    {fold.StateDraft: true, fold.StatePublished: true, fold.StateDeprecated: true},
	fold.KindRequirement: {fold.StateDraft: true, fold.StatePublished: true, fold.StateAcknowledged: true},
	fold.KindQuestion:    openExchangeStates,
	fold.KindWorkRequest: openExchangeStates,
	fold.KindDecision:    {fold.StateDraft: true, fold.StateProposed: true},
	// `rejected` is live, and its omission was a real defect: §3.4.5 puts the
	// PRODUCER on the hook from there (resubmit as a new XH superseding this
	// one) and internal/pendency's table says so, but with the state missing
	// here buildOpenItems filtered the handoff out before ever asking — so the
	// producer was never told they owed the resubmission. Found by W3's
	// data-loop conformance path; guarded by
	// TestOpenStatesReachesEveryStateSomebodyOwesAMoveFrom.
	fold.KindHandoff:      {fold.StateDraft: true, fold.StateSubmitted: true, fold.StateAcknowledged: true, fold.StateRejected: true},
	fold.KindResponse:     {fold.StateDraft: true, fold.StateSubmitted: true},
	fold.KindAnnouncement: {fold.StateDraft: true, fold.StatePublished: true},
}

var openExchangeStates = map[fold.State]bool{
	fold.StateDraft: true, fold.StateSubmitted: true, fold.StateAcknowledged: true,
	fold.StateAccepted: true, fold.StateInProgress: true, fold.StateBlocked: true, fold.StateResponded: true,
}

// isOpen reports whether kind/state is "open" per openStates — an
// unknown kind conservatively answers false (never actionable/attention
// by default).
func isOpen(kind fold.Kind, state fold.State) bool {
	m, ok := openStates[kind]
	return ok && m[state]
}

// exchangeActive narrows the protocol's durable "open" states to the
// operational question answered by the dashboard: is this document still in
// flight for the current system? Announcements intentionally remain
// `published` until superseded, but delivery itself is complete once every
// intended recipient has acknowledged it. Treating published as permanently
// active made delivered announcements linger in Outgoing forever.
//
// For every kind but a published announcement that is a pure STATE question,
// answered above. For a published announcement it is an OBLIGATION question —
// "does anybody still owe an acknowledgement" — and this function used to
// answer it itself, from `ack_requested`, `to:` and the folded ack set.
//
// That was a fourth derivation of a relation this repo keeps in exactly one
// place (I7), and it reproduced, silently, a defect internal/pendency had
// already fixed: `ack_requested` gates the `to:`-matched half of the rule
// ONLY. A deprecation announcement addresses every REGISTERED consumer,
// registry-matched, with no flag qualifier anywhere in its statement — which
// is also how `internal/validate.CheckRetirePrecondition` reads it. So a
// flagless deprecation was archived out of the consumer's exchange feed while
// `a2a contract retire` refused on that same consumer's missing acknowledge.
//
// The relation answers now. `resolveVerdict` is the package's single
// pendency.Input call site, so this question and `--actionable`'s cannot
// drift apart again.
//
// needsLivenessVerdict names the SECOND pair this same question applies
// to (defects/01, design question 3's own "this commit owns both or
// neither"): a published contract with no registered consumer is the case
// contractPublishedRow already answers "alive and settled: neither is a
// move anyone waits for", and it stayed live in the exchange feed forever
// until now for exactly the same reason a flagless deprecation did —
// isOpen's own unconditional allowlist cannot see the third fact.
func needsLivenessVerdict(fa foldedArtifact) bool {
	switch {
	case fa.kind() == fold.KindAnnouncement && fa.Result.State == fold.StatePublished:
		return true
	case fa.kind() == fold.KindContract && fa.Result.State == fold.StatePublished:
		return true
	}
	return false
}

func exchangeActive(fa foldedArtifact, me string, manifest space.Manifest) bool {
	if !isOpen(fa.kind(), fa.Result.State) {
		return false
	}
	if !needsLivenessVerdict(fa) {
		return true
	}
	verdict := resolveVerdict(fa, me, manifest, "")
	return exchangeActiveFromVerdict(fa, me, verdict)
}

// exchangeActiveFromVerdict is exchangeActive's carried-answer form. It is
// used when a caller already resolved the artifact's verdict and must not pay a
// second relation evaluation merely to classify a published announcement.
func exchangeActiveFromVerdict(fa foldedArtifact, me string, verdict pendency.Verdict) bool {
	if !isOpen(fa.kind(), fa.Result.State) {
		return false
	}
	if !needsLivenessVerdict(fa) {
		return true
	}
	if fa.kind() == fold.KindContract {
		// Liveness here is a property of the DOCUMENT, not the reader
		// (spec 03-declared-nature.md's own finding: "whose move it is is
		// a property of the artifact, not the reader"): contractPublishedRow
		// only ever names the PRODUCER (never a consumer specifically), so
		// splitting on ownedByMe the way the announcement branch below does
		// would leave every non-owner's own containsString(verdict.Owners,
		// me) permanently false — silently dropping a contract merely
		// addressed to a consumer via `to:` out of THEIR active feed too, a
		// change beyond this defect. len(verdict.Owners) > 0 answers "is
		// this document still in flight" for everyone asking about it.
		return len(verdict.Owners) > 0
	}
	// The author asks about the artifact — "is anyone still to acknowledge
	// this" — while a recipient asks about itself. Both were already the
	// two branches this function had; only the rule behind them changed.
	if ownedByMe(fa, me) {
		return len(verdict.Owners) > 0
	}
	return containsString(verdict.Owners, me)
}
