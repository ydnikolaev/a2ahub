// Package contract owns the pure contract carried-set domain model.
//
// It deliberately performs no filesystem, Git, schema-registry, or network
// I/O. Callers collect bounded candidate bytes and modes, then pass them to
// BuildCarriedSet. This keeps authoring, CI, historical replay, and future
// server projections on one interpretation of a published contract.
package contract

const (
	// DescriptorPath is part of the public package API.
	DescriptorPath = "contract.md"
	// MaxExplicitFiles is part of the public package API.
	MaxExplicitFiles = 256
	// MaxFileBytes is part of the public package API.
	MaxFileBytes = 1 << 20 // 1 MiB
	// MaxAggregateBytes is part of the public package API.
	MaxAggregateBytes = 16 << 20 // 16 MiB
)

// DigestProfile fixes the immutable meaning of a publication digest.
type DigestProfile string

const (
	// ProfileContractTreeV1 is part of the public package API.
	ProfileContractTreeV1 DigestProfile = "contract-tree-v1"
	// ProfileContractSetV2 is part of the public package API.
	ProfileContractSetV2 DigestProfile = "contract-set-v2"
	// ProfileExportSourceV1 is part of the public package API.
	ProfileExportSourceV1 DigestProfile = "export-source-v1"
)

// DigestProfiles returns the three registered profiles, in the order they
// are declared. A caller enumerating them must not re-list the constants:
// that is the second hand-written list this type exists to prevent — the
// same argument fold.Outcomes() carries for its own closed vocabulary, and
// the reason answers-that-hold-2026-08 P10 could not build its
// digest-profile input axis without it (spec 10, universe 4: "the profile
// constants in internal/contract").
//
// Deliberately NOT derived from a schema, unlike Role: schemas/event/v2's
// own enum carries only the two EVENT-carried profiles, and
// export-source-v1 is asserted by a producer's generator rather than
// written into any envelope. A schema-derived list here would be a
// confident, specific, wrong answer. TestDigestProfilesMatchTheDeclarations
// is what keeps this in step instead: it parses this file's own const block,
// so a fourth profile declared without a row here reds.
func DigestProfiles() []DigestProfile {
	return []DigestProfile{ProfileContractTreeV1, ProfileContractSetV2, ProfileExportSourceV1}
}

// Role is the closed v2 carried-entry vocabulary.
type Role string

const (
	// RoleSchema is part of the public package API.
	RoleSchema Role = "schema"
	// RoleValidFixture is part of the public package API.
	RoleValidFixture Role = "valid-fixture"
	// RoleInvalidFixture is part of the public package API.
	RoleInvalidFixture Role = "invalid-fixture"
	// RoleErrors is part of the public package API.
	RoleErrors Role = "errors"
	// RoleVocabulary is part of the public package API.
	RoleVocabulary Role = "vocabulary"
	// RoleLimits is part of the public package API.
	RoleLimits Role = "limits"
	// RoleChangelog is part of the public package API.
	RoleChangelog Role = "changelog"
	// RoleExample is part of the public package API.
	RoleExample Role = "example"
	// RoleOther is part of the public package API.
	RoleOther Role = "other"
)

// Descriptor contains the contract-domain fields consumed by the carried-set
// core. Envelope/schema validation remains the responsibility of
// internal/validate; ParseDescriptor only decodes this typed projection.
// Descriptor is part of the public package API.
type Descriptor struct {
	SchemaFormat  string         `yaml:"schema_format" json:"schema_format"`
	GeneratedFrom *GeneratedFrom `yaml:"generated_from,omitempty" json:"generated_from,omitempty"`
	Artifacts     []Entry        `yaml:"artifacts" json:"artifacts"`
}

// GeneratedFrom is part of the public package API.
type GeneratedFrom struct {
	Tool         string `yaml:"tool" json:"tool"`
	SourceDigest string `yaml:"source_digest" json:"source_digest"`
}

// Entry declares one explicit file carried by contract-set-v2.
type Entry struct {
	Path       string `yaml:"path" json:"path"`
	Role       Role   `yaml:"role" json:"role"`
	Normative  bool   `yaml:"normative" json:"normative"`
	MediaType  string `yaml:"media_type" json:"media_type"`
	ConformsTo string `yaml:"conforms_to,omitempty" json:"conforms_to,omitempty"`
}

// CandidateKind is the resolved kind supplied by a filesystem or Git-tree
// collector. Executable and non-executable regular files both map to Regular;
// symlinks and submodules retain distinct kinds so the core can fail closed.
// CandidateKind is part of the public package API.
type CandidateKind string

const (
	// CandidateRegular is part of the public package API.
	CandidateRegular CandidateKind = "regular"
	// CandidateSymlink is part of the public package API.
	CandidateSymlink CandidateKind = "symlink"
	// CandidateSubmodule is part of the public package API.
	CandidateSubmodule CandidateKind = "submodule"
	// CandidateOther is part of the public package API.
	CandidateOther CandidateKind = "other"
)

// CandidateFile is a bounded, contract-root-relative input. Raw must contain
// the exact bytes to be committed or read from the historical Git object.
// CandidateFile is part of the public package API.
type CandidateFile struct {
	Path string
	Kind CandidateKind
	Raw  []byte
}

// CarriedSet is an immutable-by-convention result. BuildCarriedSet clones all
// carried bytes and returns sorted Entries plus a detached digest map, so
// caller mutation of candidate buffers cannot change the result.
// CarriedSet is part of the public package API.
type CarriedSet struct {
	Profile         DigestProfile
	Descriptor      Descriptor
	Entries         []Entry
	PerFileDigest   map[string]string
	AggregateDigest string
	Bytes           map[string][]byte
}

// DigestProjection is a named digest profile result, used to keep
// export-source-v1 provenance visibly separate from publication integrity.
// DigestProjection is part of the public package API.
type DigestProjection struct {
	Profile       DigestProfile
	PerFileDigest map[string]string
	Digest        string
}

// PublicationIntent is the closed event/v2 durable operation-identity wire
// object. P6 owns derivation and subject/version cross-checks.
// PublicationIntent is part of the public package API.
type PublicationIntent struct {
	IntentKey             string `json:"intent_key" yaml:"intent_key"`
	CandidateIntentDigest string `json:"candidate_intent_digest" yaml:"candidate_intent_digest"`
	VersionSelector       string `json:"version_selector" yaml:"version_selector"`
	OperationKey          string `json:"operation_key" yaml:"operation_key"`
}
