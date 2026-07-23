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

import "time"

// Data is the full dashboard model — the `DATA` global the page renders from.
// Everything is space-tagged so the per-space tabs filter by space id.
type Data struct {
	GeneratedAt   time.Time      `json:"generatedAt"` // snapshot time (STATIC view)
	Self          string         `json:"self"`        // the viewing system (ego node)
	Tooling       Tooling        `json:"tooling"`
	Spaces        []SpaceHealth  `json:"spaces"`
	Nodes         []Node         `json:"nodes"`
	ContractEdges []ContractEdge `json:"contractEdges"`
	ExchangeEdges []ExchangeEdge `json:"exchangeEdges"`
	Inbox         []Item         `json:"inbox"`
	Outbox        []Item         `json:"outbox"`
	Contracts     []Contract     `json:"contracts"`
	Flags         []Flag         `json:"flags"`
}

// Tooling is the version/update strip (from cache.UpdateNotice).
type Tooling struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Required        bool   `json:"required"` // local binary below a space's min floor
	Floor           string `json:"floor"`
	FloorSpace      string `json:"floorSpace"`
}

// SpaceHealth is one connected space's row (per-space health panel).
type SpaceHealth struct {
	ID               string `json:"id"`
	RepoURL          string `json:"repoURL"`
	SyncAge          string `json:"syncAge"` // pre-formatted (e.g. "3m"), "" if unknown
	Stale            bool   `json:"stale"`
	ParticipantCount int    `json:"participantCount"`
	Readable         bool   `json:"readable"` // false = mirror/manifest unreadable → degrade
}

// Node is a graph node = a system (deduped across the spaces it is in).
type Node struct {
	System string   `json:"system"`
	Self   bool     `json:"self"`
	Org    string   `json:"org"`
	Status string   `json:"status"` // active | left
	Owners []string `json:"owners,omitempty"`
	Spaces []string `json:"spaces"` // which of your spaces it participates in
}

// ContractEdge is a STRUCTURAL edge: `from` consumes `to`'s contract.
type ContractEdge struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Space           string `json:"space"`
	Contract        string `json:"contract"`
	PinnedMajor     int    `json:"pinnedMajor"`
	ProviderVersion string `json:"providerVersion,omitempty"`
	State           string `json:"state,omitempty"` // published | deprecated | retired
	// Drift: current | behind | deprecated | retired | dangling.
	Drift  string `json:"drift"`
	Sunset string `json:"sunset,omitempty"` // ISO date, if the provider set one
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
	Age         string   `json:"age,omitempty"` // pre-formatted (e.g. "5d"), "" if no events
	New         bool     `json:"new"`
	Severity    string   `json:"severity"` // blocking | attention | normal
	Reasons     []string `json:"reasons,omitempty"`
}

// Contract is one contract in the catalog (from Store.Contracts).
type Contract struct {
	Space      string   `json:"space"`
	ID         string   `json:"id"`
	Provider   string   `json:"provider"`
	Version    string   `json:"version"`
	State      string   `json:"state"`
	CodeBacked bool     `json:"codeBacked"`
	Consumers  []string `json:"consumers,omitempty"`
}

// Flag is one validation flag (V4/V5) surfaced per space/system.
type Flag struct {
	Space    string `json:"space"`
	System   string `json:"system"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
