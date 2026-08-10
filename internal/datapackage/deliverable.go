package datapackage

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

// This file holds Deliverable, DeliverableKindData and DecodeDeliverables —
// moved here from internal/cache/delivery.go by wave 23 of
// agent-exchange-2026-08 (spec 06 §11's 2026-08-10 "AC9 wire decision"),
// which needs this decode from BOTH internal/cache (the read half, already
// wired) and internal/space (the new submit-time refusal, part 3 of this
// wave). internal/space cannot import internal/cache — cache/mirror.go
// imports space, so that would be a cycle — but internal/cache already
// imports internal/datapackage (ADR-001's table: `artifact`, `schema`), so
// this is the cycle-free common ground, exactly the precedent set for
// AttachmentClaim (spec 04 §11, "the attachment claim is structured, not
// rendered-only").
//
// internal/space, however, does NOT appear in ADR-001's own row for
// internal/datapackage's importers (docs/decisions.md:34 lists internal/
// space's own allowed imports as artifact, schema, validate, host,
// workreport, contract — datapackage is not among them). So internal/space
// does NOT import this file's decoder; it carries its own package-local
// mirror instead (internal/space/delivery_possession.go's own doc comment
// names this explicitly, the same ADR-001-mirror idiom
// internal/space/possession.go's Attachment and internal/space/
// data_resolve.go's dataPackagePrefix already use). This file's move is
// still correct and still required: it is what makes internal/cache's own
// decode legal without a NEW yaml probe growing beside DecodeDeliverables
// in cache/mirror.go, exactly as the brief's "one home, one copy" rule
// requires.
//
// No compatibility alias is left behind in internal/cache — wave 21 left a
// temporary re-export plus duplicated tests for the equivalent
// AttachmentClaim move and the lead had to delete both; this move does not
// repeat it.

// DeliverableKindData is handoff.schema.json's deliverables[].kind value
// this package resolves. The other four kind values (code, contract,
// config, doc) carry no data-package/verification-report chain and are
// left to their own renderer — DecodeDeliverables returns all of them, and
// it is each caller's job, not the decoder's, to filter.
const DeliverableKindData = "data"

// Deliverable is one handoff.schema.json deliverables[] entry ({name, ref,
// kind}), decoded directly from the raw envelope bytes.
type Deliverable struct {
	Name string `yaml:"name" json:"name"`
	Ref  string `yaml:"ref" json:"ref"`
	Kind string `yaml:"kind" json:"kind"`
}

// deliverablesProbe is DecodeDeliverables' own frontmatter decode target —
// the handoff envelope carries other fields (verification,
// acceptance_criteria, fulfills, ...) that this file has no use for, so
// only deliverables[] is named.
type deliverablesProbe struct {
	Deliverables []Deliverable `yaml:"deliverables"`
}

// DecodeDeliverables decodes a handoff artifact's raw `.md` bytes (the same
// bytes foldedArtifact.Raw / rawArtifact.Raw carries) into its
// deliverables[] array. raw must be frontmatter-shaped
// (artifact.ParseFrontmatter's own contract); a non-handoff document simply
// decodes to an empty slice, since deliverables is not a field it carries.
func DecodeDeliverables(raw []byte) ([]Deliverable, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("datapackage: DecodeDeliverables: %w", err)
	}
	var probe deliverablesProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil, fmt.Errorf("datapackage: DecodeDeliverables: %w", err)
	}
	return probe.Deliverables, nil
}
