// Package html assembles the `a2a html` / `a2a dashboard` local view's data
// model (Data) from the shipped read surface (internal/cache Store + space
// manifests + consumes.yaml) and renders it into a self-contained static HTML
// page by injecting the model as JSON into an embedded, designed template.
//
// It is a pure read layer (ADR-001: no writes, no network in the render path —
// the Store already composed the mirrors). The model's JSON keys are camelCase
// because they are consumed by the page's client JS (the `DATA` global), not by
// the snake_case CLI --json surfaces.
package html

import (
	"time"

	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/viewvocab"
)

// Data is the full dashboard model — the `DATA` global the page renders from.
// Everything is space-tagged so the per-space tabs filter by space id.
type Data struct {
	Meta            DemoMeta             `json:"meta,omitempty"`
	Vocabulary      VocabularyTable      `json:"vocabulary"`
	GeneratedAt     time.Time            `json:"generatedAt"` // snapshot time (STATIC view)
	Self            string               `json:"self"`        // the viewing system (ego node)
	Tooling         Tooling              `json:"tooling"`
	Spaces          []SpaceHealth        `json:"spaces"`
	Nodes           []Node               `json:"nodes"`
	ContractEdges   []ContractEdge       `json:"contractEdges"`
	ExchangeEdges   []ExchangeEdge       `json:"exchangeEdges"`
	Threads         []Thread             `json:"threads"`
	Inbox           []Item               `json:"inbox"`
	Outbox          []Item               `json:"outbox"`
	Archive         []Item               `json:"archive"`
	Contracts       []Contract           `json:"contracts"`
	Flags           []Flag               `json:"flags"`
	ReleaseNotes    []ReleaseNote        `json:"releaseNotes"`
	UpdateDetail    UpdateDetail         `json:"updateDetail"`
	Focus           *Focus               `json:"focus,omitempty"`
	ThreadViews     []ThreadView         `json:"threadViews,omitempty"`
	WorkReports     []WorkReport         `json:"workReports"`
	Operational     operational.Snapshot `json:"operational"`
	ArtifactDetails []ArtifactDetail     `json:"artifactDetails,omitempty"`
	Unavailable     []UnavailableFact    `json:"unavailable,omitempty"`
	Aggregates      DashboardAggregates  `json:"aggregates"`
	Windows         DashboardWindows     `json:"windows"`
}

// VocabularyTable, VocabularyEntry, VocabularyFallback, VocabularyFamily and
// VocabularyTone moved to internal/viewvocab (spec space-notify-2026-08 P2)
// along with the vocabulary data table and DashboardVocabulary() itself, so a
// caller outside the presentation layer can read the same RU/EN words
// without importing internal/html and inverting the ADR-001 boundary this
// package's import set protects. Aliased here — not re-declared — so the
// dashboard's wire contract (JSON tags, field order, omitempty) is unchanged
// by construction.

// VocabularyTable is the complete payload-local dictionary for status-bearing
// values. Unknown is presentation fallback, not a fabricated domain entry.
type VocabularyTable = viewvocab.VocabularyTable

// VocabularyEntry binds one typed family/value pair to permanent bilingual
// meaning and to the closed presentation cues the browser may apply.
type VocabularyEntry = viewvocab.VocabularyEntry

// VocabularyFallback is the honest result for an unrecognized family/value.
// It deliberately carries neither field, so it cannot pose as catalogue data.
type VocabularyFallback = viewvocab.VocabularyFallback

// VocabularyFamily identifies one closed family in the dashboard's bilingual
// vocabulary.
type VocabularyFamily = viewvocab.VocabularyFamily

// VocabularyTone identifies the presentation intent and non-color cue assigned
// to a dashboard vocabulary entry.
type VocabularyTone = viewvocab.VocabularyTone

// DashboardItemSet is one admitted prefix of a cache-owned aggregate set.
// Window.Total remains the KPI value even when Items is bounded.
type DashboardItemSet struct {
	Items  []Item             `json:"items"`
	Window operational.Window `json:"window"`
}

// DashboardContractEdgeSet carries the admitted off-current dependency lines
// and the evidence needed to distinguish a complete scan from degradation.
type DashboardContractEdgeSet struct {
	Items        []ContractEdge          `json:"items"`
	Window       operational.Window      `json:"window"`
	Complete     bool                    `json:"complete"`
	Degradations []DependencyDegradation `json:"degradations"`
}

// DashboardAggregates carries the bounded attention and dependency summaries
// shown at the dashboard root.
type DashboardAggregates struct {
	NeedYou         DashboardItemSet         `json:"needYou"`
	NoHumanMove     DashboardItemSet         `json:"noHumanMove"`
	YouAwait        DashboardItemSet         `json:"youAwait"`
	LinesOffCurrent DashboardContractEdgeSet `json:"linesOffCurrent"`
}

// DependencyDegradation names a dependency registry input that could not be
// included honestly in LinesOffCurrent.
type DependencyDegradation struct {
	Space  string `json:"space"`
	System string `json:"system"`
	Path   string `json:"path"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// DashboardWindows records every root collection whose embedded prefix may be
// smaller than its domain total. Threads covers the paired list/detail slices.
type DashboardWindows struct {
	Inbox           operational.Window `json:"inbox"`
	Outbox          operational.Window `json:"outbox"`
	Archive         operational.Window `json:"archive"`
	Threads         operational.Window `json:"threads"`
	ContractEdges   operational.Window `json:"contractEdges"`
	ExchangeEdges   operational.Window `json:"exchangeEdges"`
	Flags           operational.Window `json:"flags"`
	WorkReports     operational.Window `json:"workReports"`
	ArtifactDetails operational.Window `json:"artifactDetails"`
}

// DemoMeta labels the canonical dense design fixture. Live data leaves this
// empty; renderers must never infer that a live snapshot is synthetic.
type DemoMeta struct {
	Schema            string   `json:"schema,omitempty"`
	Synthetic         bool     `json:"synthetic,omitempty"`
	Notice            string   `json:"notice,omitempty"`
	ReleaseNotesScope string   `json:"releaseNotesScope,omitempty"`
	SourceClasses     []string `json:"sourceClasses,omitempty"`
}

// Focus is a trusted route hint supplied by the CLI after resolving a local
// notification route. It contains only qualified protocol identity.
type Focus struct {
	Space      string `json:"space"`
	ArtifactID string `json:"artifactID"`
	Update     bool   `json:"update,omitempty"`
	Found      bool   `json:"found"`
}

// UpdateDetail is the safe, network-free future-release card. Headline and
// changes are populated only from a verified release-cohort detail cache.
type UpdateDetail struct {
	Status   string         `json:"status"`
	Version  string         `json:"version"`
	Headline string         `json:"headline,omitempty"`
	Changes  []UpdateChange `json:"changes"`
}

// UpdateChange is one safe, schema-validated future-release summary row.
type UpdateChange struct {
	Kind    string   `json:"kind"`
	Impact  string   `json:"impact"`
	Subject string   `json:"subject"`
	Detail  string   `json:"detail"`
	Run     []string `json:"run,omitempty"`
}

// Tooling is the version/update strip (from cache.UpdateNotice).
type Tooling struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"updateAvailable"`
	Required        bool      `json:"required"` // local binary below a space's min floor
	Floor           string    `json:"floor"`
	FloorSpace      string    `json:"floorSpace"`
	CheckedAt       time.Time `json:"checkedAt"`
	CacheAge        string    `json:"cacheAge"`
	Source          string    `json:"source"`
	Fresh           bool      `json:"fresh"`
	ReleaseURL      string    `json:"releaseURL"`
}

// SpaceHealth is one connected space's row (per-space health panel).
type SpaceHealth struct {
	ID               string `json:"id"`
	RepoURL          string `json:"repoURL"`
	Synced           bool   `json:"synced"`
	SyncAge          string `json:"syncAge"` // pre-formatted (e.g. "3m"), "" if never synced
	Stale            bool   `json:"stale"`
	Revision         string `json:"revision,omitempty"` // exact mirror HEAD for this static snapshot
	ParticipantCount int    `json:"participantCount"`
	Readable         bool   `json:"readable"` // false = mirror/manifest unreadable → degrade
	SchemaVersion    string `json:"schemaVersion,omitempty"`
	MinBinaryVersion string `json:"minBinaryVersion,omitempty"`
	WorkflowVersion  string `json:"workflowVersion,omitempty"`
	WorkflowRef      string `json:"workflowRef,omitempty"`
}

// Node is a graph node = a system (deduped across the spaces it is in).
type Node struct {
	System string   `json:"system"`
	Self   bool     `json:"self"`
	Org    string   `json:"org"`
	Status string   `json:"status"` // active | left
	Owners []string `json:"owners,omitempty"`
	Avatar string   `json:"avatar,omitempty"` // validated local data URI for Owners[0]
	Spaces []string `json:"spaces"`           // which of your spaces it participates in
}

// ContractEdge is a STRUCTURAL edge: `from` consumes `to`'s contract.
type ContractEdge struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Space           string `json:"space"`
	Contract        string `json:"contract"`
	PinnedMajor     int    `json:"pinnedMajor"`
	PinnedVersion   string `json:"pinnedVersion,omitempty"`
	PinnedState     string `json:"pinnedState,omitempty"`
	ProviderVersion string `json:"providerVersion,omitempty"`
	State           string `json:"state,omitempty"` // published | deprecated | retired
	AvailableMajors []int  `json:"availableMajors,omitempty"`
	// Drift: current | behind | deprecated | retired | missing | dangling.
	Drift     string `json:"drift"`
	Sunset    string `json:"sunset,omitempty"` // ISO date, if the provider set one
	Successor string `json:"successor,omitempty"`
	// Description is the provider contract's short summary (from its body) —
	// UNTRUSTED, textContent-only (D-002). Omitted when unknown/empty.
	Description string `json:"description,omitempty"`
}

// ExchangeEdge is a TRANSIENT overlay edge: open exchanges aggregated per
// (from, to, space) direction. Count is a LIVENESS fact — how many
// aggregated items are simply open (cache's own isOpen); OwedCount is the
// narrower PENDENCY fact — how many of those items have somebody actually
// owing the next move (cache.Item.WaitingOn non-empty). Liveness ⊋
// pendency: an edge can be entirely live and have OwedCount == 0 (every
// document sent, none of them owing anybody anything), and the map must be
// able to say that rather than implying the opposite.
type ExchangeEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Space       string `json:"space"`
	Count       int    `json:"count"`
	OwedCount   int    `json:"owedCount,omitempty"`
	MaxPriority string `json:"maxPriority,omitempty"`
	Blocking    bool   `json:"blocking"`
	MaxStale    string `json:"maxStale,omitempty"` // pre-formatted age of the oldest
}

// LocalizedText carries one server-authored sentence in both permanently
// supported dashboard locales. The browser may select a locale; it must not
// assemble protocol-shaped prose from domain fields.
type LocalizedText struct {
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Item is one open inbox/outbox row (mapped from cache.Item + derived age/severity).
type Item struct {
	Space       string   `json:"space"`
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	From        string   `json:"from"`
	To          []string `json:"to,omitempty"`
	State       string   `json:"state"`
	Priority    string   `json:"priority,omitempty"`
	Blocking    bool     `json:"blocking"`
	GatePending bool     `json:"gatePending"`
	Thread      string   `json:"thread,omitempty"`
	Age         string   `json:"age,omitempty"` // pre-formatted age since document creation
	NeededBy    string   `json:"neededBy,omitempty"`
	// CreatedAt plus the per-space committed sequence are the immutable
	// Exchange ordering key. A late acknowledge/note changes activity history,
	// never the document's place in the feed. Sequence zero is valid when
	// CreatedOrderKnown is true (the first commit in a space).
	CreatedAt         string `json:"createdAt,omitempty"`
	CreatedSeq        int64  `json:"createdSeq,omitempty"`
	CreatedOrderKnown bool   `json:"createdOrderKnown,omitempty"`
	// FeedRank is this item's 1-based place in the merged Exchange feed, cut
	// once on the server from the creation key above. The Exchange screen
	// selects direction ACROSS inbox, outbox and archive, so no single carried
	// collection describes the order the reader sees; rather than re-deriving
	// the rule in the browser — which is how a newest-by-activity re-sort got
	// in once — the server hands over the answer and the view reads it.
	FeedRank int64 `json:"feedRank,omitempty"`
	// MovedAt is the latest lifecycle activity instant. It remains available
	// for freshness and history surfaces, but is not an Exchange sort key.
	MovedAt string `json:"movedAt,omitempty"`
	// ActivitySeq and ActivityEventID identify that latest transition.
	ActivitySeq     int64    `json:"activitySeq,omitempty"`
	ActivityEventID string   `json:"activityEventId,omitempty"`
	New             bool     `json:"new"`
	Severity        string   `json:"severity"` // blocking | attention | normal
	Reasons         []string `json:"reasons,omitempty"`
	// Overdue and ActivationOwed are cache-computed attention semantics. The
	// browser presents them but never classifies Reasons to recover them.
	Overdue        bool `json:"overdue,omitempty"`
	ActivationOwed bool `json:"activationOwed,omitempty"`
	PendingMerge   bool `json:"pendingMerge,omitempty"`
	SyncStale      bool `json:"syncStale,omitempty"`
	// Archived means the exchange is operationally complete for this system.
	// It is intentionally separate from State: announcements remain formally
	// `published` even after every intended recipient has acknowledged them.
	Archived bool `json:"archived,omitempty"`
	YourMove bool `json:"yourMove"`
	// WaitingOn, ExpectedTransition, Why, HumanGate and OperationalItems are
	// the domain's already-computed verdict, carried whole. Why remains
	// technical evidence; ReasonSentence is the human explanation composed by
	// the server from typed reason/rule identity plus these structured facts.
	WaitingOn          []string                `json:"waitingOn,omitempty"`
	ExpectedTransition string                  `json:"expectedTransition,omitempty"`
	Why                string                  `json:"why,omitempty"`
	HumanGate          string                  `json:"humanGate,omitempty"`
	OperationalItems   []cache.OperationalItem `json:"operationalItems,omitempty"`
	ReasonSentence     LocalizedText           `json:"reasonSentence,omitzero"`
	RuleIdentity       cache.RuleIdentity      `json:"ruleIdentity,omitempty"`
	// Description is a short human-readable summary (from the artifact body) —
	// UNTRUSTED, rendered via textContent (D-001). Omitted when the body is empty.
	Description string `json:"description,omitempty"`
	// Prompt carries the facts behind the "prompt for the agent" button. Nil
	// when this system has no legal move on the artifact, or when the artifact
	// belongs to no thread and there is therefore nothing to fold it against.
	Prompt *AgentPrompt `json:"prompt,omitempty"`

	// Outcome, Terminal and the three State* fields — the domain's answer to
	// what this state means and what produced it. See ThreadOpenItem's own
	// comment on the same five; the component reads them instead of deciding
	// from state names, which is the whole point of the phase that added them.
	Outcome    fold.Outcome `json:"outcome,omitempty"`
	Terminal   bool         `json:"terminal"`
	StateSince time.Time    `json:"stateSince,omitzero"`
	StateBy    string       `json:"stateBy,omitempty"`
	StateEvent string       `json:"stateEvent,omitempty"`
}

// AgentPrompt is the fact set a copied agent prompt is assembled from. The
// facts are composed here and the sentences in the browser, for one reason:
// which moves this system may legally make next is the fold engine's answer,
// and restating it client-side would make the page a second source of protocol
// truth — the rule SKILL.md puts above every other rule it carries.
//
// The struct and the composer that builds it moved to internal/agentprompt
// (spec space-notify-2026-08 P2) so a caller outside the presentation layer
// (a chat notifier, the CI plane) can compose the same prompt without
// importing internal/html and inverting the ADR-001 boundary this package's
// import set protects. Aliased here — not re-declared — so the JSON tags,
// field order and omitempty behaviour on the wire are unchanged by
// construction.
type AgentPrompt = agentprompt.AgentPrompt

// Thread is one conversation on the dashboard (spec 46 §T6.1) —
// Store.ThreadView's read model projected for render. This includes a newly
// submitted one-member thread: it is the conversation's first turn, not noise,
// and omitting it hides new work until somebody replies. Sorted by
// LastActivity, most-recent-first (the conversation-list convention).
type Thread struct {
	ID           string       `json:"id"`
	Space        string       `json:"space"`
	Participants []string     `json:"participants"`
	MemberCount  int          `json:"memberCount"`
	OpenCount    int          `json:"openCount"`
	LastActivity string       `json:"lastActivity"` // pre-formatted age (e.g. "5d"), "" if no timestamped member
	Opener       ThreadOpener `json:"opener"`
	YourMove     bool         `json:"yourMove"` // true when an open member is waiting on Data.Self

	// WaitingOthers names every system OTHER than Data.Self that still owes a
	// move on this thread, sorted. A reader asking "whose move is it" needs the
	// name; "someone else" is not an answer they can act on.
	WaitingOthers []string `json:"waitingOthers"`

	Members []ThreadMember `json:"members"`
	Links   []DocLink      `json:"links"`
	Windows ThreadWindows  `json:"windows"`

	// Settled reports that no member of this thread still owes anyone a move.
	// The protocol has no thread-level "closed" state — closure is DERIVED:
	// members reach their own terminal states (close/cancel/withdraw/verify),
	// and a thread whose every remaining open item is escape-hatch-only is
	// finished in every sense a reader cares about. Without this the list
	// showed "waiting on others" on threads nobody was waiting on.
	Settled bool `json:"settled"`
}

// ThreadWindows records admission metadata for a thread's member and link
// collections.
type ThreadWindows struct {
	Members operational.Window `json:"members"`
	Links   operational.Window `json:"links"`
}

// ThreadOpener is a thread's computed opener — the brief's own {id,title}
// shape (cache.ThreadOpener also carries `from`; deliberately dropped here,
// the member list already carries every member's `from`).
type ThreadOpener struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ThreadMember is one document inside a Thread's transcript, in transcript
// order (Store.ThreadView's own committed/declared ordering — never
// re-derived here).
type ThreadMember struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Title string   `json:"title"`
	From  string   `json:"from"`
	To    []string `json:"to,omitempty"`
	State string   `json:"state"`
	Seq   int64    `json:"seq"`
}

// DocLink is one document-to-document edge inside a thread — the piece
// Item.Thread's old flat mono-text rendering could never show: `parent`
// comes from response.schema.json's own `parent` field, `ref` from an
// envelope's `refs[]` entries resolved against the SAME thread's own
// rendered member set (an edge whose target is not itself a rendered
// member of this thread is not drawn — the forward ref already renders;
// see this phase's Deviations report for `supersede`, never emitted: the
// event refs it needs are not exposed past internal/cache today).
type DocLink struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // parent | ref | supersede
}

// Contract is one contract in the catalog (from Store.Contracts).
type Contract struct {
	Space         string   `json:"space"`
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	Version       string   `json:"version"`
	State         string   `json:"state"`
	CodeBacked    bool     `json:"codeBacked"`
	Category      string   `json:"category,omitempty"`
	SchemaFormat  string   `json:"schemaFormat,omitempty"`
	CompatPolicy  string   `json:"compatPolicy,omitempty"`
	GeneratedTool string   `json:"generatedTool,omitempty"`
	SourceDigest  string   `json:"sourceDigest,omitempty"`
	Consumers     []string `json:"consumers,omitempty"`
	// Description is a short human-readable summary from the contract body —
	// UNTRUSTED, textContent-only (D-002). Omitted when the body is empty.
	Description string `json:"description,omitempty"`
	// Versions is the rolling window: every published version and the state
	// it holds now, semver-ascending (P4, agent-ops-2026-07). Version/State
	// above stay the summary — newest published, and the projection over
	// these. The dashboard needs both: the summary answers "is this contract
	// alive", the window answers "which of its lines is".
	//
	// These come from the fold, not from a contract body, so unlike
	// Description they are OURS rather than artifact-controlled — but the
	// template still renders them as textContent, because the rule is per
	// SURFACE, not per field, and one exception is how the next one gets
	// made.
	Versions []ContractVersion `json:"versions,omitempty"`
	// NonAdoptable carries cache.ContractInfo.NonAdoptable straight through
	// (F4, agent-exchange-2026-08 wave 36): the descriptor's own
	// `x_binding` refusal reaches the dashboard from cache.Store.Contracts.
	// False (the zero value) reads as "adoptable" whether the field is
	// genuinely declared adoptable or simply undeclared — P-1's own default;
	// see the cache field's doc comment for that source distinction.
	NonAdoptable bool `json:"nonAdoptable,omitempty"`
	// Operational carries the contract's declared x_operational[] state —
	// spec 08 (defects-fix-2026-08) "a contract tells its operational
	// truth", the SAME cache.OperationalItemsFromEnvelope/
	// DeriveOperationalItems projection Item.OperationalItems and
	// ArtifactDetail.OperationalItems already carry (P5,
	// agent-exchange-2026-08), never a second derivation here.
	//
	// P-1: nil means the descriptor never declared the field at all —
	// "not declared", the Contracts card's third case. Non-nil means the
	// field WAS declared, and (DeriveOperationalItems' own contract, see
	// internal/cache/mirror.go) always covers every well-known name
	// (endpoint, credential-channel, registration) with its own state —
	// ready, absent, or per-item undeclared for a name this document
	// never mentioned. Collapsing "never declared" and "declared empty"
	// into the same zero value is exactly what P-1 forbids, so this is
	// deliberately never given `omitempty`'s sibling meaning by any
	// caller that flattens absence and an empty slice — see this field's
	// own assembly site for the source of the distinction, which is the
	// presence of the envelope's x_operational KEY, not the length of
	// what cache derives from it.
	Operational []cache.OperationalItem `json:"operational,omitempty"`
}

// ContractVersion is one version of a contract and the state it holds — the
// page's contracts[].versions[] entry shape, mirroring cache.ContractVersion.
type ContractVersion struct {
	Version       string `json:"version"`
	State         string `json:"state"`
	Sunset        string `json:"sunset,omitempty"`
	Successor     string `json:"successor,omitempty"`
	DeprecationID string `json:"deprecationID,omitempty"`
	// Detail is the optional version-scoped read model. It is additive so old
	// snapshots remain readable; when present, every field belongs to this
	// exact immutable version and must switch atomically with the selector.
	Detail *ContractVersionDetail `json:"detail,omitempty"`
}

// ContractVersionDetailStatus says whether the version's immutable carried
// set could be resolved. Unavailable is an honest result, never an empty
// available package.
type ContractVersionDetailStatus string

const (
	// ContractVersionDetailAvailable means the immutable carried set was resolved.
	ContractVersionDetailAvailable ContractVersionDetailStatus = "available"
	// ContractVersionDetailUnavailable preserves the reason the carried set could not be resolved.
	ContractVersionDetailUnavailable ContractVersionDetailStatus = "unavailable"
)

// ContractVersionDetail is the strict, version-scoped dashboard projection.
// Text fields and previews are untrusted DATA and are rendered textContent-only.
type ContractVersionDetail struct {
	Status            ContractVersionDetailStatus  `json:"status"`
	UnavailableReason string                       `json:"unavailableReason,omitempty"`
	Description       string                       `json:"description,omitempty"`
	SchemaFormat      string                       `json:"schemaFormat,omitempty"`
	CompatPolicy      string                       `json:"compatPolicy,omitempty"`
	PublishedAt       string                       `json:"publishedAt,omitempty"`
	PublishedBy       string                       `json:"publishedBy,omitempty"`
	ChangeSummary     string                       `json:"changeSummary,omitempty"`
	ConsumerPins      []ContractVersionConsumerPin `json:"consumerPins,omitempty"`
	// TotalDocumentCount is the exact verified package inventory before the
	// bounded dashboard projection. OmittedDocumentCount says how many of
	// those files are not embedded in this static snapshot.
	TotalDocumentCount   int                        `json:"totalDocumentCount"`
	OmittedDocumentCount int                        `json:"omittedDocumentCount,omitempty"`
	Documents            []ContractVersionDocument  `json:"documents,omitempty"`
	History              []ContractVersionHistory   `json:"history,omitempty"`
	Provenance           *ContractVersionProvenance `json:"provenance,omitempty"`
}

// ContractVersionConsumerPin is current observed consumer usage of this
// version. It deliberately does not claim historical usage.
type ContractVersionConsumerPin struct {
	System        string `json:"system"`
	Space         string `json:"space,omitempty"`
	PinnedMajor   int    `json:"pinnedMajor,omitempty"`
	PinnedVersion string `json:"pinnedVersion,omitempty"`
	PinnedState   string `json:"pinnedState,omitempty"`
	Drift         string `json:"drift,omitempty"`
}

// ContractVersionDocument is one carried file from the immutable version
// package. Preview is optional bounded safe text, never executable markup.
type ContractVersionDocument struct {
	Path            string `json:"path"`
	Role            string `json:"role"`
	Normative       bool   `json:"normative"`
	MediaType       string `json:"mediaType,omitempty"`
	ConformsTo      string `json:"conformsTo,omitempty"`
	Digest          string `json:"digest,omitempty"`
	SizeBytes       int64  `json:"sizeBytes,omitempty"`
	Preview         string `json:"preview,omitempty"`
	PreviewLanguage string `json:"previewLanguage,omitempty"`
	ShownBytes      int64  `json:"shownBytes"`
	TotalBytes      int64  `json:"totalBytes"`
	Truncated       bool   `json:"truncated"`
}

// ContractVersionHistory is a compact version-lifecycle fact. EventID, Actor,
// and At are populated only when the exact committed event evidence is
// available. A fold-proven lifecycle state may therefore intentionally omit
// them; consumers must render that as unavailable evidence, never invent it.
type ContractVersionHistory struct {
	EventID    string `json:"eventID"`
	Transition string `json:"transition"`
	State      string `json:"state"`
	Actor      string `json:"actor,omitempty"`
	At         string `json:"at,omitempty"`
}

// ContractVersionProvenance identifies the exact Git objects and digest
// verification evidence used to resolve one immutable version.
type ContractVersionProvenance struct {
	Profile                string `json:"profile,omitempty"`
	CommitSHA              string `json:"commitSHA,omitempty"`
	TreeObjectID           string `json:"treeObjectID,omitempty"`
	DescriptorPath         string `json:"descriptorPath,omitempty"`
	PublishEventPath       string `json:"publishEventPath,omitempty"`
	EventSchema            string `json:"eventSchema,omitempty"`
	DigestProfile          string `json:"digestProfile,omitempty"`
	PublishedDigest        string `json:"publishedDigest,omitempty"`
	AggregateVerification  string `json:"aggregateVerification,omitempty"`
	DescriptorVerification string `json:"descriptorVerification,omitempty"`
	GeneratedTool          string `json:"generatedTool,omitempty"`
	SourceDigest           string `json:"sourceDigest,omitempty"`
}

// Flag is one read-health or committed protocol flag surfaced per
// space/system. Source distinguishes fold from read-index; it never pretends
// these are V4/V5 results unless that engine was actually mounted.
type Flag struct {
	Space       string                 `json:"space"`
	System      string                 `json:"system"`
	Code        string                 `json:"code"`
	Message     string                 `json:"message"`
	Severity    string                 `json:"severity"`
	Source      string                 `json:"source,omitempty"`
	Artifact    string                 `json:"artifact,omitempty"`
	Event       string                 `json:"event,omitempty"`
	Consistency *cache.ReceiptMismatch `json:"consistency,omitempty"`
}

// ReleaseNote is one embedded releasenotes/<version>.yaml document projected
// into DATA. Its authored strings stay verbatim; RU/EN localizes the shell and
// typed labels, never rewrites release claims.
type ReleaseNote struct {
	Version  string          `json:"version"`
	Released string          `json:"released"`
	Headline string          `json:"headline"`
	Changes  []ReleaseChange `json:"changes"`
}

// ReleaseChange is one authored change entry inside an embedded release note.
// The assembler preserves its source language and structured impact/action
// fields so the renderer can explain consequence without parsing prose.
type ReleaseChange struct {
	ID      string        `json:"id"`
	Kind    string        `json:"kind"`
	Impact  string        `json:"impact"`
	Subject string        `json:"subject"`
	Detail  string        `json:"detail"`
	Affects []string      `json:"affects,omitempty"`
	Action  ReleaseAction `json:"action"`
}

// ReleaseAction describes the authored follow-up for a release change:
// where action is required, why, how to detect exposure and which commands to
// run. Empty command lists are meaningful for scope "none".
type ReleaseAction struct {
	Scope  string   `json:"scope"`
	Why    string   `json:"why"`
	Detect []string `json:"detect,omitempty"`
	Run    []string `json:"run,omitempty"`
}

// ThreadView is the explanation-grade projection for one conversation. The
// fold/authority engine supplies order and legal actions; browser code only
// filters and presents these fields.
type ThreadView struct {
	Thread       string                `json:"thread"`
	Space        string                `json:"space"`
	Order        string                `json:"order"`
	Opener       ThreadViewOpener      `json:"opener"`
	Participants []string              `json:"participants"`
	SyncStale    bool                  `json:"sync_stale"`
	Artifacts    []ThreadViewArtifact  `json:"artifacts"`
	Transcript   []ThreadTranscriptRow `json:"transcript"`
	OpenItems    []ThreadOpenItem      `json:"open_items"`
	Flags        []ThreadViewFlag      `json:"flags"`
	Unresolved   []ThreadUnresolvedRef `json:"unresolved"`
	Windows      ThreadViewWindows     `json:"windows"`

	// Settled mirrors Thread.Settled for the detail pane, so the "whose move"
	// panel can be replaced with a finished state instead of rendering an
	// empty prompt over zero open items.
	Settled bool `json:"settled"`

	// Deliveries carries the thread's handoff deliverables of kind "data"
	// (spec 05a AC-7), rendered under the handoff artifact that names them
	// (Delivery.HandoffID joins against a ThreadViewArtifact/artifact id
	// above — spec 05a's own "thread-side, under the handoff that carries
	// it" scope). IS wired into the production assembly path: toThreadView
	// (assemble.go) sets this field via ProjectDeliveries(delivery.go) over
	// cache.ThreadResult.Deliveries — resolved by
	// internal/cache/packageresolver.go's filesystem-backed resolver, wired
	// into Store.ThreadView at mirror.go:419 — and the client renders it
	// (corrected 2026-08-11, wave 36 phase A S1: this comment and
	// delivery.go's own previously both claimed the opposite — "deliberately
	// NOT wired" — which had gone stale). `omitempty` stays: a thread with
	// no data deliverables emits no `"deliveries"` key at all, never a
	// stray `"deliveries":null`.
	Deliveries []Delivery `json:"deliveries,omitempty"`
}

// ThreadViewWindows records admission metadata for every bounded collection in
// the thread detail view.
type ThreadViewWindows struct {
	Artifacts  operational.Window `json:"artifacts"`
	Transcript operational.Window `json:"transcript"`
	OpenItems  operational.Window `json:"openItems"`
	Flags      operational.Window `json:"flags"`
	Unresolved operational.Window `json:"unresolved"`
	Deliveries operational.Window `json:"deliveries"`
}

// ThreadViewOpener identifies the artifact that began a thread.
type ThreadViewOpener struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	From  string `json:"from"`
}

// ThreadViewArtifact is a compact artifact projection within a thread.
type ThreadViewArtifact struct {
	ID     string           `json:"id"`
	Type   string           `json:"type"`
	From   string           `json:"from"`
	To     []string         `json:"to,omitempty"`
	Title  string           `json:"title"`
	State  string           `json:"state"`
	Parent string           `json:"parent,omitempty"`
	Refs   []map[string]any `json:"refs,omitempty"`
	// Adopters names the systems that have registered as consumers of this
	// artifact, each with the version it pinned ("seomatrix · 1.0.1"). Set
	// only for a contract; empty everywhere else.
	//
	// A counterparty adopting a contract is invisible in a thread otherwise,
	// which is what the operator reported. It is deliberately NOT a transcript
	// row: adoption is REGISTRY STATE — a `consumes.yaml` entry — not an event
	// on this thread, and the transcript's promise is that everything in it
	// happened and is in git. Synthesising an event to fill the gap would
	// trade a missing fact for a false one. State belongs beside state.
	Adopters []string `json:"adopters,omitempty"`
}

// ThreadTranscriptRow is one ordered artifact, lifecycle event, or derived
// fact in a thread. Kind = "artifact" | "event" | "derived" — a "derived"
// row (Derived set, Artifact and Event both nil) is NOT an event this
// space's own git history recorded; it is assembled from a counterparty's
// own consumes.yaml (a contract adoption). The kind field itself carries
// that distinction, in the data, not merely in how a page happens to style
// the row — see cache.TranscriptKindDerived's own doc comment.
type ThreadTranscriptRow struct {
	Seq      int64               `json:"seq"`
	Kind     string              `json:"kind"`
	At       string              `json:"at"`
	Artifact *TranscriptArtifact `json:"artifact,omitempty"`
	Event    *TranscriptEvent    `json:"event,omitempty"`
	Derived  *TranscriptDerived  `json:"derived,omitempty"`
}

// TranscriptDerived carries a transcript row's derived-kind payload — the
// projection of cache.TranscriptDerivedAdoption for `a2a html --json`.
type TranscriptDerived struct {
	ContractID string `json:"contract_id"`
	System     string `json:"system"`
	Major      int    `json:"major"`
	// Since is the adopting system's own consumes.yaml `since:` date,
	// verbatim — omitted (never invented) when that registry entry
	// carried none. See cache.TranscriptDerivedAdoption.Since's own doc
	// comment for the honesty rule this preserves.
	Since string `json:"since,omitempty"`
}

// TranscriptArtifact carries the artifact fields shown in a transcript row.
type TranscriptArtifact struct {
	ID    string      `json:"id"`
	Type  string      `json:"type"`
	From  string      `json:"from"`
	To    []string    `json:"to,omitempty"`
	Title string      `json:"title"`
	Work  *WorkReport `json:"work,omitempty"`
}

// WorkReport is one durable semantic checkpoint from a committed status
// announcement. It is history evidence, not current presence: local leases
// remain exclusively in Operational.
type WorkReport struct {
	Space          string           `json:"space"`
	Thread         string           `json:"thread"`
	ArtifactID     string           `json:"artifact_id"`
	WorkID         string           `json:"work_id"`
	SubjectRef     string           `json:"subject_ref"`
	Mode           string           `json:"mode"`
	Summary        string           `json:"summary"`
	Actor          WorkReportActor  `json:"actor"`
	WaitingOn      []WorkReportWait `json:"waiting_on"`
	ReportedAt     string           `json:"reported_at"`
	ValidUntil     string           `json:"valid_until,omitempty"`
	CommitSequence uint64           `json:"commit_sequence"`
}

// WorkReportActor is safe authored attribution for one committed report.
type WorkReportActor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// WorkReportWait names a dependency declared by the reporting actor.
type WorkReportWait struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
}

// TranscriptEvent carries the lifecycle fields shown in a transcript row.
type TranscriptEvent struct {
	ULID         string                 `json:"ulid"`
	Subject      string                 `json:"subject"`
	Transition   string                 `json:"transition"`
	ClaimedState string                 `json:"claimed_state,omitempty"`
	Actor        map[string]any         `json:"actor"`
	ProducedBy   provenance.Producer    `json:"produced_by,omitzero"`
	Consistency  *cache.ReceiptMismatch `json:"consistency,omitempty"`
	ResponseID   string                 `json:"response_id,omitempty"`
	// Version is the event's own §5.2.2 version, carried so a contract's
	// repeated `publish` rows are distinguishable. Three identical
	// "published the contract" lines were the reported defect.
	Version string `json:"version,omitempty"`
	Note    string `json:"note,omitempty"`
	// ReasonCode and TransitionFree are carried from cache.TranscriptEvent.
	// Both were dropped at this boundary when they were first added one
	// layer down, which is the same silent-field-loss the Item and
	// OpenItem reflection gates exist to prevent — this projection had no
	// such gate until transcriptevent_projection_test.go.
	ReasonCode     string `json:"reason_code,omitempty"`
	TransitionFree bool   `json:"transition_free,omitempty"`
	// Verdicts carries cache.TranscriptEvent.Verdicts straight through — no
	// second decode, no html-local re-declaration of the same three-field
	// shape (P6 wave C, threat-model.md T5). Nil on every event that
	// carries none, the same as at cache's own layer.
	Verdicts []cache.TranscriptVerdict `json:"verdicts,omitempty"`
}

// ThreadOpenItem describes one unresolved item and its legal next actions.
type ThreadOpenItem struct {
	ID          string             `json:"id"`
	Type        string             `json:"type"`
	State       string             `json:"state"`
	Blocking    bool               `json:"blocking"`
	NeededBy    string             `json:"needed_by,omitempty"`
	NextActions []ThreadNextAction `json:"next_actions"`
	WaitingOn   []string           `json:"waiting_on"`
	YourMove    bool               `json:"your_move"`
	// Prompt is the same fact set Item.Prompt carries, for the copy button on
	// the thread panel. Nil when this system has no legal move here.
	Prompt *AgentPrompt `json:"prompt,omitempty"`

	// Pending separates "someone still owes a move" from "the item is merely
	// not in a terminal state". cache already answers this: it drops the
	// owner's escape hatches (cancel/withdraw/supersede) out of WaitingOn,
	// so an item whose only remaining affordance is an escape hatch arrives
	// with an EMPTY WaitingOn. Without this flag the renderer printed
	// "waiting on <nobody>" for exactly that case (a fully acknowledged
	// announcement is the common one). Derived here, never re-derived in the
	// browser, per the "compute nothing protocol-shaped client-side" rule.
	Pending bool `json:"pending"`

	// ExpectedTransition, Why and HumanGate are internal/pendency's own
	// verdict, carried through rather than dropped.
	//
	// This projection used to stop at WaitingOn/YourMove, which meant the
	// dashboard could say WHO owes a move but never WHICH move or WHY — and
	// "why" is not decoration in this model: pendency's contract is that
	// "nobody owes anything" is a CLAIM that must be justified, never a
	// fall-through. A surface that shows the conclusion and withholds the
	// justification republishes exactly the ambiguity the relation was built
	// to remove. Found by auditing the real axon space against a HEAD binary
	// (P11 W2 audit, plans/11-…plan.md).
	//
	// HumanGate is the sharpest of the three. Without it an agent reading this
	// dashboard sees `your_move: true` with `expected_transition: approve` and
	// no signal that approving a decision is G3 — a human gate whose event the
	// fold IGNORES AND FLAGS when an agent emits it (CC-021). That is the one
	// thing skill/a2ahub/reference/threads.md promises can never happen: a
	// surface naming a move the tool would then refuse. Fixing the relation
	// alone would not have fixed it, because the failure was always on the
	// surface.
	ExpectedTransition string             `json:"expected_transition,omitempty"`
	Why                string             `json:"why"`
	HumanGate          string             `json:"human_gate,omitempty"`
	ReasonSentence     LocalizedText      `json:"reasonSentence,omitzero"`
	RuleIdentity       cache.RuleIdentity `json:"ruleIdentity,omitempty"`

	// OperationalItems carries cache.OpenItem.OperationalItems straight
	// through (spec 05 AC4, agent-exchange-2026-08 P5) — the same "reuse
	// the cache type directly" idiom Verdicts above already draws for
	// cache.TranscriptVerdict, never a html-local re-declaration of the
	// same {name, state} shape. Nil for every non-contract item.
	OperationalItems []cache.OperationalItem `json:"operational_items,omitempty"`

	// Outcome, Terminal and the three State* fields are the domain's own
	// answer, carried rather than recomputed. Every one of them existed in
	// the browser before this, as three kind-agnostic literal sets and a
	// guess — which is how `retired` rendered as cancelled and a `note`
	// rendered as the latest protocol event.
	//
	// StateSince is NOT MovedAt. MovedAt is the activity clock and moves for
	// a transition-free event too; this is the state clock. Both are carried,
	// under distinct names, because a renderer choosing between them by feel
	// is exactly what spec 00's final acceptance criterion forbids.
	Outcome    fold.Outcome `json:"outcome,omitempty"`
	Terminal   bool         `json:"terminal"`
	StateSince time.Time    `json:"stateSince,omitzero"`
	StateBy    string       `json:"stateBy,omitempty"`
	StateEvent string       `json:"stateEvent,omitempty"`
}

// ThreadNextAction identifies an available transition and its allowed actors.
type ThreadNextAction struct {
	Transition string   `json:"transition"`
	By         []string `json:"by"`
}

// ThreadViewFlag identifies an integrity warning attached to a thread.
type ThreadViewFlag struct {
	Kind        string                 `json:"kind"`
	Subject     string                 `json:"subject"`
	EventULID   string                 `json:"event_ulid,omitempty"`
	Consistency *cache.ReceiptMismatch `json:"consistency,omitempty"`
}

// ThreadUnresolvedRef identifies a reference the snapshot cannot resolve.
type ThreadUnresolvedRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// ArtifactDetail carries bounded canonical record/evidence for the dashboard
// detail panel. Envelope remains a map because the eight artifact bodies have
// intentionally different schema-owned fields. BodyHTML is a server-rendered,
// safe GFM projection: raw HTML is escaped, dangerous URLs are rejected and
// remote images never cross into it.
type ArtifactDetail struct {
	SourceClass string                `json:"sourceClass"`
	Space       string                `json:"space"`
	Path        string                `json:"path,omitempty"`
	ID          string                `json:"id"`
	Type        string                `json:"type"`
	Title       string                `json:"title"`
	From        string                `json:"from"`
	To          []string              `json:"to,omitempty"`
	State       string                `json:"state"`
	Thread      string                `json:"thread,omitempty"`
	Envelope    map[string]any        `json:"envelope"`
	Body        string                `json:"body"`
	BodyHTML    string                `json:"bodyHTML"`
	Digest      string                `json:"digest"`
	Events      []ArtifactDetailEvent `json:"events"`
	Flags       []string              `json:"flags"`
	Refs        []ArtifactDetailRef   `json:"refs"`
	// Attachments carries cache.ShowResult.Attachments straight through —
	// spec 04's (agent-exchange-2026-08) §11 amendment ("the attachment
	// claim is structured, not rendered-only"): one
	// datapackage.AttachmentClaim per committed attachments[] entry,
	// already fully derived (VerificationClaim, Lapsed/LapsedOn/
	// LapseClaim) by internal/cache/store.go. toArtifactDetail performs no
	// second decode and no second derivation — reusing the type directly
	// is what "one decode, one place" means at this layer.
	Attachments []datapackage.AttachmentClaim `json:"attachments"`
	SyncStale   bool                          `json:"sync_stale"`
	SyncAge     string                        `json:"sync_age"`
	// Outcome and Terminal are the domain's reading of State, carried here
	// too because the detail panel renders from THIS type and may have no
	// matching exchange row to borrow them from — an artifact can be shown
	// from a space whose inbox/outbox projection does not list it.
	//
	// Without them the panel fell back to classifying State by membership
	// in its own name lists, which is the defect the whole phase removes.
	Outcome  fold.Outcome `json:"outcome,omitempty"`
	Terminal bool         `json:"terminal"`
	// OperationalItems is spec 05 AC4's per-item x_operational[]
	// projection (cache.OperationalItemsFromEnvelope, composed over the
	// SAME DeriveOperationalItems rule internal/cache/mirror.go applies
	// for thread/html's ThreadOpenItem) — set only for a contract; nil for
	// every other type. Named here so the dashboard detail panel's own
	// hand-picked prop literal (web/design-source's `detailFor`) has
	// something to name — a field the Go layer adds is otherwise invisible
	// there until it is (see that file's own comment on the attachments
	// prop for the trap this avoids).
	OperationalItems []cache.OperationalItem `json:"operational_items,omitempty"`
}

// ArtifactDetailEvent carries one lifecycle event in the artifact detail panel.
type ArtifactDetailEvent struct {
	ULID         string                 `json:"ulid"`
	Subject      string                 `json:"subject"`
	Transition   string                 `json:"transition"`
	ClaimedState string                 `json:"claimed_state,omitempty"`
	ActorKind    string                 `json:"actor_kind,omitempty"`
	Actor        string                 `json:"actor"`
	ActorSystem  string                 `json:"actor_system"`
	ActorModel   string                 `json:"actor_model,omitempty"`
	ActorSession string                 `json:"actor_session,omitempty"`
	ProducedBy   provenance.Producer    `json:"produced_by,omitzero"`
	Consistency  *cache.ReceiptMismatch `json:"consistency,omitempty"`
	At           string                 `json:"at"`
	Note         string                 `json:"note,omitempty"`
	// Verdicts carries cache.EventSummary.Verdicts straight through — no
	// second decode, no html-local re-declaration of the same
	// {index, verdict, cause_owner} shape (B34, agent-exchange-2026-08),
	// the same "reuse the cache type directly" idiom TranscriptEvent.Verdicts
	// above already draws for the thread transcript. Nil on every event that
	// carries none, the same as at cache's own layer.
	Verdicts []cache.TranscriptVerdict `json:"verdicts,omitempty"`
}

// ArtifactDetailRef carries the resolution and digest evidence for one reference.
type ArtifactDetailRef struct {
	Ref            string `json:"ref"`
	ID             string `json:"id,omitempty"`
	Version        string `json:"version,omitempty"`
	PinnedDigest   string `json:"pinned_digest,omitempty"`
	Resolved       bool   `json:"resolved"`
	ResolvedDigest string `json:"resolved_digest,omitempty"`
	DigestMismatch bool   `json:"digest_mismatch,omitempty"`
}

// UnavailableFact explains a value the current snapshot cannot establish.
type UnavailableFact struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	Title       string `json:"title"`
	Reason      string `json:"reason"`
	SourceClass string `json:"sourceClass"`
}
