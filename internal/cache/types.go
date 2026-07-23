package cache

import (
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
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
	ULID         string    `json:"ulid"`
	Subject      string    `json:"subject"`
	Transition   string    `json:"transition"`
	ClaimedState string    `json:"claimed_state,omitempty"`
	Actor        string    `json:"actor"`
	ActorSystem  string    `json:"actor_system"`
	At           time.Time `json:"at"`
}

// ShowResult is `a2a show <ref>`'s full output shape (OP-209): artifact
// body + folded state + event list + facts a V5 code lookup needs.
type ShowResult struct {
	Space     string         `json:"space"`
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	From      string         `json:"from"`
	To        []string       `json:"to,omitempty"`
	State     string         `json:"state"`
	Body      string         `json:"body"`
	Digest    string         `json:"digest"`
	Events    []EventSummary `json:"events"`
	Flags     []string       `json:"flags,omitempty"`
	Refs      []RefFact      `json:"refs,omitempty"`
	SyncStale bool           `json:"sync_stale"`
	SyncAge   string         `json:"sync_age,omitempty"`
}

// ContractInfo is one entry in `a2a contracts`' listing (OP-221 second
// clause): provider, version, state — resolved via publish events (D-023).
type ContractInfo struct {
	Space    string `json:"space"`
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
	State    string `json:"state"`
}

// SearchFilters narrows `a2a search`'s free-text match.
type SearchFilters struct {
	Type  string
	Space string
	State string
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
