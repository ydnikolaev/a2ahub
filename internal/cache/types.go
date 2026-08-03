package cache

import (
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
)

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

	// PendingMerge is true when a submitted-but-not-yet-visible-as-merged
	// marker exists for this artifact (the pending-merge overlay, §7.2
	// OP-205's "local cache marks pending-merge" step).
	PendingMerge bool `json:"pending_merge,omitempty"`
	// SyncStale is true when the item's own space mirror's sync-age
	// exceeds the statusline TTL — surfaced so `a2a inbox`/`outbox`
	// callers know the data may be behind (T1: "works offline ... with
	// sync age flagged when stale").
	SyncStale bool `json:"sync_stale,omitempty"`
	// LatestEventAt is the timestamp of the artifact's most recent folded
	// event — the "pending since" anchor. Zero when the artifact has no events
	// yet (a bare draft). `json:"-"` deliberately: the dashboard assembler reads
	// this as a Go field (formats it to an age like "5d"), while inbox/outbox
	// `--json` stay byte-stable for their existing consumers (a time.Time would
	// not honor omitempty anyway).
	LatestEventAt time.Time `json:"-"`
	// LatestEventID is the current transition identity for local
	// notification projections. It stays outside the established inbox JSON
	// contract, just like LatestEventAt.
	LatestEventID string `json:"-"`
	// Description is a short human-readable summary from the artifact's body —
	// the "what is this" line the dashboard shows under an inbox/outbox item.
	// json:"-" (like LatestEventAt): a dashboard-only Go field, so inbox/outbox
	// `--json` stay byte-stable for their existing consumers.
	Description string `json:"-"`
	// YourMove is the canonical "whose move is it" projection for this
	// artifact. It is dashboard-only for now: the stable inbox/outbox JSON
	// shape remains unchanged while presentation code stops inferring action
	// ownership from blocking/deadline labels.
	YourMove bool `json:"-"`
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

// preAckState is the kind's own "not yet acknowledged" state — the exact
// value fold's own table (internal/fold/table.go) assigns as fromState
// for that kind's `acknowledge` row. KindAnnouncement is deliberately
// absent: every announcement (broadcast or single-target) uses D-025's
// transition-free per-recipient ack set (fold.go's own kind-only switch
// guard), never a State transition, so its "acked by me" fact reads
// Result.Acks, not State.
var preAckState = map[fold.Kind]fold.State{
	fold.KindRequirement: fold.StatePublished,
	fold.KindQuestion:    fold.StateSubmitted,
	fold.KindWorkRequest: fold.StateSubmitted,
	fold.KindHandoff:     fold.StateSubmitted,
}

// openStates enumerates, per kind, every State internal/fold/table.go's
// own rows treat as non-terminal ("any open state", OP-207 condition 4 /
// OP-208's own base "open items" scope) — a deliberate per-kind
// allowlist (rather than a blanket "not in {closed,...}" deny-list) so
// this stays correct if a kind's terminal-state set ever grows
// asymmetrically.
var openStates = map[fold.Kind]map[fold.State]bool{
	fold.KindContract:     {fold.StateDraft: true, fold.StatePublished: true},
	fold.KindRequirement:  {fold.StateDraft: true, fold.StatePublished: true, fold.StateAcknowledged: true},
	fold.KindQuestion:     openExchangeStates,
	fold.KindWorkRequest:  openExchangeStates,
	fold.KindDecision:     {fold.StateDraft: true, fold.StateProposed: true},
	fold.KindHandoff:      {fold.StateDraft: true, fold.StateSubmitted: true, fold.StateAcknowledged: true},
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
