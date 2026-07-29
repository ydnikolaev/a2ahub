// Package notification owns the canonical local notification projection,
// personal project registry, independent delivery leases, and trusted route
// ledger. Native adapters only render this protocol.
package notification

import (
	"context"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// SchemaVersion is the notification adapter protocol version.
const SchemaVersion = 1

// Channel identifies a native presentation surface.
type Channel string

const (
	// ChannelMacOS is the signed native macOS application.
	ChannelMacOS Channel = "macos"
	// ChannelVSCode is the local VS Code UI extension.
	ChannelVSCode Channel = "vscode"
)

// ValidChannel reports whether channel is implemented by P49.
func ValidChannel(channel Channel) bool {
	return channel == ChannelMacOS || channel == ChannelVSCode
}

// ProjectSource is the consumer-side interface implemented by cache.Store.
type ProjectSource interface {
	NotificationSnapshot(context.Context) (cache.NotificationSnapshot, error)
}

// SourceFactory resolves cursor-neutral cache truth for one project.
type SourceFactory func(project Project) (ProjectSource, error)

// Entry is one sanitized, qualified, lease-delivered transition signal.
type Entry struct {
	SchemaVersion  int       `json:"schema_version"`
	EntryID        string    `json:"entry_id"`
	ProjectID      string    `json:"project_id"`
	SpaceID        string    `json:"space_id,omitempty"`
	ArtifactID     string    `json:"artifact_id,omitempty"`
	EventID        string    `json:"event_id,omitempty"`
	Kind           string    `json:"kind"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	OccurredAt     time.Time `json:"occurred_at"`
	SourceRevision string    `json:"source_revision,omitempty"`
	CoalesceKey    string    `json:"coalesce_key"`
	RouteToken     string    `json:"route_token"`
}

// Level is the persistent aggregate status rendered by every channel.
type Level struct {
	Line       string `json:"line"`
	New        int    `json:"new"`
	Urgent     int    `json:"urgent"`
	Stale      int    `json:"stale"`
	Update     string `json:"update"`
	Severity   string `json:"severity"`
	Quiet      bool   `json:"quiet"`
	RouteToken string `json:"route_token,omitempty"`
}

// Offer is the effective notification setup prompt state.
type Offer struct {
	State       string     `json:"state"`
	Scope       string     `json:"scope"`
	RemindAfter *time.Time `json:"remind_after"`
}

// Project is one stable machine-local notification registration.
type Project struct {
	ID       string    `json:"id"`
	Root     string    `json:"root"`
	Channels []Channel `json:"channels"`
	LastSeen time.Time `json:"last_seen"`
}

// Status is the additive JSON contract shared by skill and adapters.
type Status struct {
	AvailableChannels []Channel         `json:"available_channels"`
	InstalledChannels []Channel         `json:"installed_channels"`
	Project           *Project          `json:"project"`
	Level             Level             `json:"level"`
	Offer             Offer             `json:"offer"`
	Components        []ComponentHealth `json:"components"`
}

// Lease grants one consumer temporary ownership of a project batch.
type Lease struct {
	Token      string    `json:"token"`
	ConsumerID string    `json:"consumer_id"`
	Channel    Channel   `json:"channel"`
	ProjectID  string    `json:"project_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ClaimResult contains an optional lease and its bounded entries.
type ClaimResult struct {
	Lease   *Lease  `json:"lease"`
	Entries []Entry `json:"entries"`
}

// AckResult distinguishes newly and previously accepted entries.
type AckResult struct {
	Acked        []string `json:"acked"`
	AlreadyAcked []string `json:"already_acked"`
}

// Route is a core-minted local HTML destination.
type Route struct {
	Token      string    `json:"token"`
	ProjectID  string    `json:"project_id"`
	SpaceID    string    `json:"space_id,omitempty"`
	ArtifactID string    `json:"artifact_id,omitempty"`
	Kind       string    `json:"kind"`
	CreatedAt  time.Time `json:"created_at"`
}

// ComponentHealth is additive detail behind the stable installed-channel
// summary. Platform probes may leave fields empty when a bounded status read
// cannot determine them without launching UI or prompting.
type ComponentHealth struct {
	Channel    Channel `json:"channel"`
	Available  bool    `json:"available"`
	Installed  bool    `json:"installed"`
	Version    string  `json:"version,omitempty"`
	Protocol   int     `json:"protocol,omitempty"`
	Profile    string  `json:"profile,omitempty"`
	CodePath   string  `json:"code_path,omitempty"`
	Permission string  `json:"permission,omitempty"`
	LoginItem  string  `json:"login_item,omitempty"`
	Handshake  string  `json:"handshake,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
	Detail     string  `json:"detail,omitempty"`
}
