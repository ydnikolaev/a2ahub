package validate

import (
	"errors"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// checkIDForm is CC-003 (ID ↔ filename mismatch, or ID prefix ≠ type):
// internal/artifact owns the actual ID-grammar parse and the filename/
// section guard (§3.3) — this function never re-implements that check,
// it only translates artifact's typed errors into REF- violations.
// §0.5's domain table places this "ID-form check" under this phase's
// referential class explicitly.
func checkIDForm(env envelope, path string) []Violation {
	var out []Violation

	id, err := artifact.ParseID(env.ID)
	if err != nil {
		out = append(out, Violation{
			Code:     "REF-001",
			Class:    ClassReferential,
			Path:     "id",
			Message:  "artifact id does not match the §3.3 grammar",
			CCRef:    "CC-003",
			Severity: SeverityReject,
		})
		return out
	}

	if verr := artifact.ValidateAt(id, path, placementFor(env.Type)); verr != nil {
		// artifact.Validate returns errors.Join(...) of the two
		// independent guards; errors.Is walks a Join tree (stdlib,
		// Go 1.20+), so both checks below are independent, not
		// mutually exclusive.
		if errors.Is(verr, artifact.ErrIDMismatch) {
			out = append(out, Violation{
				Code:     "REF-001",
				Class:    ClassReferential,
				Path:     "id",
				Message:  "artifact id does not match its filename",
				CCRef:    "CC-003",
				Severity: SeverityReject,
			})
		}
		if errors.Is(verr, artifact.ErrSectionMismatch) {
			out = append(out, Violation{
				Code:     "REF-002",
				Class:    ClassReferential,
				Path:     "id",
				Message:  "artifact id's system component does not match its owning section",
				CCRef:    "CC-003",
				Severity: SeverityReject,
			})
		}
	}

	return out
}

// placementFor is the envelope-type → §4.2 path-shape table CC-003's guard
// needs. internal/artifact deliberately keeps no 8-type enum (ADR-001,
// Open Q2), so the type-aware half of the rule lives here, in the one
// package that already knows the 8 types — the mechanics of each shape
// stay in artifact.ValidateAt.
//
// Two types do not commit as <system>/<dir>/<id>.md:
//   - contract  -> <system>/provides/<slug>/contract.md (fixed filename;
//     identity is the provides/<slug>/ directory)
//   - decision  -> decisions/<id>.md (space-level; no owning section)
//
// Applying the default shape to either red every contract submission with
// REF-001 and every decision proposal with REF-002, at V2 AND at V3 —
// the whole contract/decision family was unpublishable (external-agent
// feedback fb-20260723-9ae145 against v0.2.0).
func placementFor(envType string) artifact.Placement {
	switch envType {
	case "contract":
		return artifact.PlacementProvidesContract
	case "decision":
		return artifact.PlacementSpaceLevel
	default:
		return artifact.PlacementSectionFile
	}
}

// checkRefs is the referential class's core §5.7/§3.8 rule: every entry
// in env.Refs must resolve via the local Resolver; a pinned ref
// (`id@version`, `id#digest`, or `id@version#digest`) additionally gets a
// digest-match check when a digest is present; a fully unpinned ref
// (bare id only) is a warning, not a reject.
func checkRefs(env envelope, resolver Resolver) []Violation {
	if resolver == nil {
		return nil
	}
	var out []Violation
	for _, r := range env.Refs {
		id, version, digest := parseRef(r.Ref)
		_ = version

		if !resolver.KnownArtifact(id) {
			out = append(out, Violation{
				Code:     "REF-003",
				Class:    ClassReferential,
				Path:     "refs",
				Message:  "ref " + r.Ref + " does not resolve to a known artifact",
				CCRef:    "CC-070",
				Severity: SeverityReject,
			})
			continue
		}

		if digest == "" && version == "" {
			out = append(out, Violation{
				Code:     "REF-007",
				Class:    ClassReferential,
				Path:     "refs",
				Message:  "ref " + r.Ref + " is unpinned (drift-prone)",
				CCRef:    "",
				Severity: SeverityWarning,
			})
			continue
		}

		if digest != "" {
			actual, found := resolver.Digest(r.Ref)
			switch {
			case found && actual != digest:
				out = append(out, Violation{
					Code:     "REF-004",
					Class:    ClassReferential,
					Path:     "refs",
					Message:  "ref " + r.Ref + " digest does not match the resolved target",
					Severity: SeverityReject,
				})
			case !found:
				// "Can't verify" is not "verified": a pinned digest whose
				// target cannot be resolved is flagged (warning), not
				// silently treated as matching.
				out = append(out, Violation{
					Code:     "REF-008",
					Class:    ClassReferential,
					Path:     "refs",
					Message:  "ref " + r.Ref + " is digest-pinned but its target could not be resolved to verify",
					Severity: SeverityWarning,
				})
			}
		}
	}
	return out
}

// parseRef splits a §5's ref grammar (`id`, `id@version`, `id#digest`,
// `id@version#digest`, or a `space:id...` cross-space form — the space
// prefix is left inside id here; cross-space resolution is Resolver's
// concern, not this parse) into its id/version/digest components.
func parseRef(ref string) (id, version, digest string) {
	rest := ref
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		version = rest[i+1:]
		rest = rest[:i]
	}
	id = rest
	return id, version, digest
}
