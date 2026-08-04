package datapackage

// Document is the Go shape of data-package/v1 (spec 05a §T2.1): one
// immutable delivery attempt. Field names and JSON tags ARE the wire
// shape — a name wrong here is a schema amendment after release, so every
// tag below is copied verbatim from
// schemas/data-package/v1/data-package.schema.json's own property names.
//
// Timestamps (CreatedAt, ExpiresAt, Provenance.ExtractedAt) are plain
// strings, not time.Time: this corpus deliberately never calls
// AssertFormat (internal/schema/corpus.go's Load doc comment), so
// "format": "date-time" is annotation-only, and a plain string preserves
// whatever RFC 3339 text a fixture or a producer wrote byte-for-byte
// through decode/re-encode. Parsing a timestamp to reason about it (e.g.
// ExpiresAt for the fetch-time refusal) is the caller's concern, not this
// package's.
type Document struct {
	Schema          string     `json:"schema"`
	ID              string     `json:"id"`
	Thread          string     `json:"thread"`
	Contract        string     `json:"contract"`
	Mode            string     `json:"mode"`
	Classification  string     `json:"classification"`
	DataProfile     string     `json:"data_profile"`
	Format          string     `json:"format"`
	TransportDriver string     `json:"transport_driver"`
	Locator         string     `json:"locator"`
	Entries         []Entry    `json:"entries"`
	AggregateDigest string     `json:"aggregate_digest"`
	SizeBytes       int64      `json:"size_bytes"`
	RecordCount     int64      `json:"record_count"`
	CreatedAt       string     `json:"created_at"`
	ExpiresAt       string     `json:"expires_at"`
	Supersedes      string     `json:"supersedes,omitempty"`
	Attempt         int        `json:"attempt"`
	Provenance      Provenance `json:"provenance"`
}

// Data profile values (§T2.1/§T2.4). "production" is deliberately absent:
// it is refused, not a fourth member — see ErrProductionProfile.
const (
	DataProfileSynthetic = "synthetic"
	DataProfileSanitized = "sanitized"
)

// Format values (§T2.1: closed set, extended by amendment only).
const (
	FormatJSON   = "json"
	FormatNDJSON = "ndjson"
)

// TransportDriverSpaceGit is the only driver this phase ships (§T2.1,
// CONFIRM-6).
const TransportDriverSpaceGit = "space-git"

// ModeSnapshot is the only mode implemented in this phase (§T2.1,
// CONFIRM-5). There is deliberately no ModeLive constant: "live" is
// reserved for the deferred remainder, and this package must not be able
// to emit a value it does not implement.
const ModeSnapshot = "snapshot"

// Entry role values (§T2.2).
const (
	RoleDataset = "dataset"
	RoleIndex   = "index"
	RoleReadme  = "readme"
)

// Entry is one member of entries[] (spec 05a §T2.2) — deliberately the
// same carried-set shape a contract uses, so one mental model and one
// digest discipline cover both.
//
// RecordCount is a pointer because its presence is conditional (required
// on a dataset entry, permitted-but-optional elsewhere per T2.2's own
// table — see data-package.schema.json's entry conditional): a nil pointer
// round-trips as an absent field, and a pointer to 0 round-trips as a
// present, legitimately-zero record count, which a plain int64 cannot
// distinguish from "field omitted." ConformsTo does not need the same
// treatment: it is required-and-non-empty on dataset entries and
// forbidden (never merely optional-and-empty) everywhere else, so a plain
// string with omitempty already round-trips both states exactly.
type Entry struct {
	Path        string `json:"path"`
	Role        string `json:"role"`
	MediaType   string `json:"media_type"`
	ConformsTo  string `json:"conforms_to,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	Digest      string `json:"digest"`
	RecordCount *int64 `json:"record_count,omitempty"`
}

// Provenance is the manifest's sanitized source description (§T2.1):
// origin system, cursor/watermark, extraction time.
type Provenance struct {
	OriginSystem string `json:"origin_system"`
	Cursor       string `json:"cursor,omitempty"`
	ExtractedAt  string `json:"extracted_at"`
}
