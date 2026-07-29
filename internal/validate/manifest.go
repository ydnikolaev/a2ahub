package validate

import (
	"path"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// ValidateManifest validates a space's own `space.yaml` against the embedded
// `space/v1` schema.
//
// # Why this exists, given that a validator for it already did
//
// It did, twice over, and neither was reachable. `space.LoadManifest` takes a
// `space.ManifestValidator` seam, and `cli.ManifestValidatorAdapter` implements
// that seam against this very corpus — and on 2026-07-26 a grep established
// that `LoadManifest` has ZERO production callers and
// `NewManifestValidatorAdapter` is constructed nowhere outside its own tests.
// Every production path reads the manifest through `space.ParseManifest`, which
// is a shape-only decode by design and says so.
//
// So the schema's `required` list, its `space` id pattern, and its
// `min_binary_version` grammar were enforced by nothing at runtime. Proven, not
// inferred: a hand-written manifest whose participant omits the schema-REQUIRED
// `org` and `joined` fields was accepted by `a2a connect`, by `a2a doctor`, and
// by `a2a validate --ci --mode=v3-full-repo`, which answered `"valid": true` —
// while the corpus, asked directly, answers `participants.0: required`.
//
// That is worse than an ordinary unvalidated file. `space.yaml` is the document
// that decides WHO MAY WRITE WHERE: diff-authz reads its participants and
// sections to authorise every PR. A manifest can therefore be merged in a shape
// the schema forbids, and the guard that depends on it goes on reading it.
//
// # Where it is enforced, and where it deliberately is not
//
// At the CI gate, on the pull request — never in `ParseManifest`. The read path
// (`doctor`, `connect`, `inbox`, the statusline) calls ParseManifest, and making
// THAT strict would mean a space whose manifest drifted stops being READABLE:
// a participant who did nothing wrong gets a hard failure on every verb. An
// unvalidated manifest is a better product than an unreadable space. The gate
// belongs where a change is proposed.
//
// Shape and reporting mirror ValidateConsumes exactly — same Result, same
// Violation codes, same one-cycle version window — because every consumer of
// this engine parses one wire contract (D-011).
func (e *Engine) ValidateManifest(raw []byte) (Result, error) {
	const op = "ValidateManifest"

	instance, probe, parseable := decodeManifest(raw)
	if !parseable {
		return newResult(V2, "", []Violation{malformedManifestViolation()}), nil
	}

	// space.schema.json's own `schema` const is literally "space/v1", not
	// "manifest/v1" — a naming tension the schema file documents and this
	// function inherits rather than resolves.
	against := "space/v1"
	if probe.Schema != "" {
		n, ok := schema.ParseVersion(probe.Schema)
		if !ok || !schema.AcceptsManifestVersion(n) {
			return newResult(V2, probe.Space, []Violation{{
				Code:     "POL-005",
				Class:    ClassPolicy,
				Path:     "schema",
				Message:  "space.yaml schema version is outside the one-cycle overlap window this binary understands",
				CCRef:    "CC-005",
				Severity: SeverityReject,
			}}), nil
		}
		against = probe.Schema
	}

	fieldViolations, serr := e.corpus.ValidateManifest(against, instance)
	if serr != nil {
		return Result{}, &Error{Op: op, Err: serr}
	}
	violations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return Result{}, &Error{Op: op, Err: merr}
	}
	violations = append(violations, checkManifestPolicy(probe)...)
	return newResult(V2, probe.Space, violations), nil
}

// ValidateManifestPolicy runs the authority-map half of manifest validation
// without repeating the schema check. V3 uses it on every PR before consulting
// the manifest for diff authorization: an old schema-only defect may remain a
// changed-file tripwire, but an ambiguous authority map must never grant write
// permission just because space.yaml itself was not changed by this PR.
func (e *Engine) ValidateManifestPolicy(raw []byte) (Result, error) {
	_, probe, parseable := decodeManifest(raw)
	if !parseable {
		// The structural/schema path owns malformed YAML. Callers cannot have
		// parsed a Manifest from these bytes in the first place, so returning
		// that second verdict here would only duplicate POL-002.
		return newResult(V2, "", nil), nil
	}
	return newResult(V2, probe.Space, checkManifestPolicy(probe)), nil
}

// manifestProbe is validate's own minimal projection of the authority-bearing
// manifest fields. Keeping it here preserves ADR-001: validate owns policy and
// does not import the I/O-facing space package.
type manifestProbe struct {
	Schema       string                     `yaml:"schema"`
	Space        string                     `yaml:"space"`
	Participants []manifestParticipantProbe `yaml:"participants"`
}

type manifestParticipantProbe struct {
	System  string   `yaml:"system"`
	Section string   `yaml:"section"`
	Owners  []string `yaml:"owners"`
	Status  string   `yaml:"status"`
}

// decodeManifest decodes raw twice — once as a schema instance, once into the
// version/space probe — and reports ok=false when the document is not YAML at
// all.
//
// It returns a BOOL rather than an error deliberately. "This file is not YAML"
// is a verdict about the CONTENT, which belongs in the Result as a POL-002
// violation; an error returned from ValidateManifest means the ENGINE failed,
// which is a different thing a caller handles differently (validateCIConsumes'
// twin reports it as `Error` on the report, not as a violation). Letting the
// decode error cross that boundary and then discarding it is what a linter
// correctly objects to, and silencing the linter would have hidden a real
// distinction rather than a false positive.
//
// schema.DecodeYAMLInstance, NOT a plain yaml.Unmarshal into `any`, and the
// difference is not stylistic: a manifest carries `joined: 2026-07-28`, and
// yaml.v3 auto-types an unquoted date as a time.Time — which the JSON-schema
// validator cannot represent, so it aborts with "unmapped schema-class keyword
// … InvalidJsonValue" instead of validating anything. Caught by this package's
// own test against the FIXTURE manifest before it could reach a real space; the
// failure read as schema/registry drift and would have sent the next reader
// hunting in the corpus.
func decodeManifest(raw []byte) (instance any, probe manifestProbe, ok bool) {
	decoded, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return nil, manifestProbe{}, false
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, manifestProbe{}, false
	}
	return decoded, probe, true
}

// malformedManifestViolation is the space.yaml twin of
// malformedConsumesViolation, and reuses POL-002 for the same reason: it is the
// registry's one "this document is not valid YAML" code. Its title names
// frontmatter because artifacts were the only YAML the engine saw when the
// registry was authored; the substance is identical.
func malformedManifestViolation() Violation {
	return Violation{
		Code:     "POL-002",
		Class:    ClassPolicy,
		Path:     "",
		Message:  "space.yaml is not valid YAML",
		CCRef:    "CC-001",
		Severity: SeverityReject,
	}
}

// checkManifestPolicy is the one canonical authority-map policy check. REF-013
// deliberately groups the invariant as one stable machine code: every branch
// has the same consequence — the map is ambiguous and therefore cannot
// authorize a write — while Path and Message identify the repair.
func checkManifestPolicy(probe manifestProbe) []Violation {
	var violations []Violation
	systems := make(map[string]int, len(probe.Participants))
	sections := make(map[string]int, len(probe.Participants))
	activeOwners := make(map[string]string)

	for i, participant := range probe.Participants {
		base := "participants." + strconv.Itoa(i)
		system := strings.TrimSpace(participant.System)
		if system == "" {
			violations = append(violations, manifestAuthorityViolation(base+".system", "participant system must be non-empty"))
		} else if prior, exists := systems[system]; exists {
			violations = append(violations, manifestAuthorityViolation(
				base+".system",
				"participant system duplicates participants."+strconv.Itoa(prior)+".system",
			))
		} else {
			systems[system] = i
		}

		section, clean := cleanTopLevelSection(participant.Section)
		if !clean {
			violations = append(violations, manifestAuthorityViolation(
				base+".section",
				"participant section must be one clean relative top-level directory",
			))
		} else if prior, exists := sections[section]; exists {
			violations = append(violations, manifestAuthorityViolation(
				base+".section",
				"participant section overlaps participants."+strconv.Itoa(prior)+".section",
			))
		} else {
			sections[section] = i
		}

		// A participant that left remains historical manifest data, but it
		// grants no current authority and its owners are not considered in
		// the active login map.
		if participant.Status != "active" {
			continue
		}
		if len(participant.Owners) == 0 {
			violations = append(violations, manifestAuthorityViolation(
				base+".owners",
				"active participant must name at least one owner",
			))
		}
		for ownerIndex, rawOwner := range participant.Owners {
			owner := strings.TrimSpace(rawOwner)
			ownerPath := base + ".owners." + strconv.Itoa(ownerIndex)
			if owner == "" {
				violations = append(violations, manifestAuthorityViolation(ownerPath, "active owner login must be non-empty"))
				continue
			}
			if priorSystem, exists := activeOwners[owner]; exists && priorSystem != system {
				violations = append(violations, manifestAuthorityViolation(
					ownerPath,
					"active owner login is already assigned to system "+priorSystem,
				))
				continue
			}
			activeOwners[owner] = system
		}
	}
	return violations
}

func cleanTopLevelSection(raw string) (string, bool) {
	if raw == "" || strings.Contains(raw, `\`) {
		return "", false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" || strings.HasSuffix(trimmed, "/") || path.IsAbs(trimmed) || strings.Contains(trimmed, "/") {
		return "", false
	}
	cleaned := path.Clean(trimmed)
	return cleaned, cleaned == trimmed && cleaned != "." && cleaned != ".."
}

func manifestAuthorityViolation(path, message string) Violation {
	return Violation{
		Code:     "REF-013",
		Class:    ClassReferential,
		Path:     path,
		Message:  message,
		Severity: SeverityReject,
	}
}
