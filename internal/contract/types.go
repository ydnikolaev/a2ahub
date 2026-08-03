// Package contract owns the pure contract carried-set domain model.
//
// It deliberately performs no filesystem, Git, schema-registry, or network
// I/O. Callers collect bounded candidate bytes and modes, then pass them to
// BuildCarriedSet. This keeps authoring, CI, historical replay, and future
// server projections on one interpretation of a published contract.
package contract

const (
	DescriptorPath    = "contract.md"
	MaxExplicitFiles  = 256
	MaxFileBytes      = 1 << 20  // 1 MiB
	MaxAggregateBytes = 16 << 20 // 16 MiB
)

// DigestProfile fixes the immutable meaning of a publication digest.
type DigestProfile string

const (
	ProfileContractTreeV1 DigestProfile = "contract-tree-v1"
	ProfileContractSetV2  DigestProfile = "contract-set-v2"
	ProfileExportSourceV1 DigestProfile = "export-source-v1"
)

// Role is the closed v2 carried-entry vocabulary.
type Role string

const (
	RoleSchema         Role = "schema"
	RoleValidFixture   Role = "valid-fixture"
	RoleInvalidFixture Role = "invalid-fixture"
	RoleErrors         Role = "errors"
	RoleVocabulary     Role = "vocabulary"
	RoleLimits         Role = "limits"
	RoleChangelog      Role = "changelog"
	RoleExample        Role = "example"
	RoleOther          Role = "other"
)

// Descriptor contains the contract-domain fields consumed by the carried-set
// core. Envelope/schema validation remains the responsibility of
// internal/validate; ParseDescriptor only decodes this typed projection.
type Descriptor struct {
	SchemaFormat  string         `yaml:"schema_format" json:"schema_format"`
	GeneratedFrom *GeneratedFrom `yaml:"generated_from,omitempty" json:"generated_from,omitempty"`
	Artifacts     []Entry        `yaml:"artifacts" json:"artifacts"`
}

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
type CandidateKind string

const (
	CandidateRegular   CandidateKind = "regular"
	CandidateSymlink   CandidateKind = "symlink"
	CandidateSubmodule CandidateKind = "submodule"
	CandidateOther     CandidateKind = "other"
)

// CandidateFile is a bounded, contract-root-relative input. Raw must contain
// the exact bytes to be committed or read from the historical Git object.
type CandidateFile struct {
	Path string
	Kind CandidateKind
	Raw  []byte
}

// CarriedSet is an immutable-by-convention result. BuildCarriedSet clones all
// carried bytes and returns sorted Entries plus a detached digest map, so
// caller mutation of candidate buffers cannot change the result.
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
type DigestProjection struct {
	Profile       DigestProfile
	PerFileDigest map[string]string
	Digest        string
}

// PublicationIntent is the closed event/v2 durable operation-identity wire
// object. P6 owns derivation and subject/version cross-checks.
type PublicationIntent struct {
	IntentKey             string `json:"intent_key" yaml:"intent_key"`
	CandidateIntentDigest string `json:"candidate_intent_digest" yaml:"candidate_intent_digest"`
	VersionSelector       string `json:"version_selector" yaml:"version_selector"`
	OperationKey          string `json:"operation_key" yaml:"operation_key"`
}
