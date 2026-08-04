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

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
)

// Data is the full dashboard model — the `DATA` global the page renders from.
// Everything is space-tagged so the per-space tabs filter by space id.
type Data struct {
	Meta            DemoMeta             `json:"meta,omitempty"`
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
// (from, to, space) direction.
type ExchangeEdge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Space       string `json:"space"`
	Count       int    `json:"count"`
	MaxPriority string `json:"maxPriority,omitempty"`
	Blocking    bool   `json:"blocking"`
	MaxStale    string `json:"maxStale,omitempty"` // pre-formatted age of the oldest
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
	// MovedAt is the latest lifecycle activity instant. It remains available
	// for freshness and history surfaces, but is not an Exchange sort key.
	MovedAt string `json:"movedAt,omitempty"`
	// ActivitySeq and ActivityEventID identify that latest transition.
	ActivitySeq     int64    `json:"activitySeq,omitempty"`
	ActivityEventID string   `json:"activityEventId,omitempty"`
	New             bool     `json:"new"`
	Severity        string   `json:"severity"` // blocking | attention | normal
	Reasons         []string `json:"reasons,omitempty"`
	PendingMerge    bool     `json:"pendingMerge,omitempty"`
	SyncStale       bool     `json:"syncStale,omitempty"`
	// Archived means the exchange is operationally complete for this system.
	// It is intentionally separate from State: announcements remain formally
	// `published` even after every intended recipient has acknowledged them.
	Archived bool `json:"archived,omitempty"`
	YourMove bool `json:"yourMove"`
	// Description is a short human-readable summary (from the artifact body) —
	// UNTRUSTED, rendered via textContent (D-001). Omitted when the body is empty.
	Description string `json:"description,omitempty"`
	// Prompt carries the facts behind the "prompt for the agent" button. Nil
	// when this system has no legal move on the artifact, or when the artifact
	// belongs to no thread and there is therefore nothing to fold it against.
	Prompt *AgentPrompt `json:"prompt,omitempty"`
}

// AgentPrompt is the fact set a copied agent prompt is assembled from. The
// facts are composed here and the sentences in the browser, for one reason:
// which moves this system may legally make next is the fold engine's answer,
// and restating it client-side would make the page a second source of protocol
// truth — the rule SKILL.md puts above every other rule it carries.
type AgentPrompt struct {
	// Moves are the transitions Data.Self may take on this artifact right now,
	// in the fold table's own order.
	Moves []string `json:"moves"`
	// AskFirst is the subset of Moves an agent must put to its human before
	// taking, because they commit the system's resources or foreclose an option
	// that cannot be reopened. The discriminator is the transition, never the
	// document type.
	AskFirst []string `json:"askFirst,omitempty"`
	// Doc and Loop are paths inside the installed a2ahub skill: the file that
	// owns this artifact type's shape, and the loop this move belongs to. Empty
	// rather than guessed when the type is one the skill does not document.
	Doc  string `json:"doc,omitempty"`
	Loop string `json:"loop,omitempty"`
	// Drafts are the authoring files for what TAKING one of these moves
	// produces, which is a different document from the one being read:
	// responding to a work request drafts a response. Empty when every move
	// available here only records an event against the document already there.
	Drafts []string `json:"drafts,omitempty"`
}

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

	// Settled reports that no member of this thread still owes anyone a move.
	// The protocol has no thread-level "closed" state — closure is DERIVED:
	// members reach their own terminal states (close/cancel/withdraw/verify),
	// and a thread whose every remaining open item is escape-hatch-only is
	// finished in every sense a reader cares about. Without this the list
	// showed "waiting on others" on threads nobody was waiting on.
	Settled bool `json:"settled"`
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

	// Settled mirrors Thread.Settled for the detail pane, so the "whose move"
	// panel can be replaced with a finished state instead of rendering an
	// empty prompt over zero open items.
	Settled bool `json:"settled"`

	// Deliveries carries the thread's handoff deliverables of kind "data"
	// (spec 05a AC-7), projected via ProjectDeliveries(cache.ResolveDeliveries(...))
	// and rendered under the handoff artifact that names them
	// (Delivery.HandoffID joins against a ThreadViewArtifact/artifact id
	// above — spec 05a's own "thread-side, under the handoff that carries
	// it" scope). `omitempty` deliberately: nothing in this codebase's
	// production assembly path (internal/html/assemble.go's toThreadView,
	// internal/cache/threadview.go's Store.ThreadView) constructs this
	// field yet — see this wave's own deviations report — and an
	// un-populated ThreadView must keep emitting byte-identical JSON
	// (no stray `"deliveries":null`) rather than silently changing every
	// existing snapshot's shape.
	Deliveries []Delivery `json:"deliveries,omitempty"`
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
}

// ThreadTranscriptRow is one ordered artifact or lifecycle event in a thread.
type ThreadTranscriptRow struct {
	Seq      int64               `json:"seq"`
	Kind     string              `json:"kind"`
	At       string              `json:"at"`
	Artifact *TranscriptArtifact `json:"artifact,omitempty"`
	Event    *TranscriptEvent    `json:"event,omitempty"`
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
	Note         string                 `json:"note,omitempty"`
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
	SyncStale   bool                  `json:"sync_stale"`
	SyncAge     string                `json:"sync_age"`
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
