package space

import (
	"path"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// Layout resolves the §4.2 normative tree's path constructors for one
// system's section within a space repo. Paths are space-relative (no
// leading slash, forward slashes always — safe to join onto any repo
// root with filepath.Join at the call site).
type Layout struct {
	// System is this layout's owning section (§4.2: "the subtree of a
	// space owned and writable by exactly one system").
	System string
}

// NewLayout constructs a Layout for system, rejecting an invalid system id
// via internal/artifact's own id-grammar shape check (reused, not
// re-implemented, per the anti-duplication rule) — a real §3.3 artifact
// id is round-tripped through ParseID with system as its <system> token;
// a hyphenated or otherwise malformed system id fails that shape check.
func NewLayout(system string) (Layout, error) {
	const op = "NewLayout"
	if !validSystemID(system) {
		return Layout{}, &Error{Op: op, Input: system, Err: ErrInvalidSystemID}
	}
	return Layout{System: system}, nil
}

// validSystemID reuses internal/artifact.ParseID's <system> token shape
// check by round-tripping system through a synthetic probe id, instead of
// duplicating the regex here (rails: one validation surface, D-011's
// spirit extended to id-shape reuse).
func validSystemID(system string) bool {
	if system == "" {
		return false
	}
	probe := "X-" + system + "-y"
	parsed, err := artifact.ParseID(probe)
	return err == nil && parsed.System == system
}

// ProvidesContract returns <system>/provides/<slug>/contract.md (the XC
// contract descriptor). It delegates to internal/artifact, which owns this
// one path shape next to the id-placement guard that checks it, so a
// contract's committed path and the guard V2/V3 run over it can never
// drift apart (the REF-001-on-every-contract defect, fb-20260723-9ae145).
func (l Layout) ProvidesContract(slug string) string {
	return artifact.ProvidesContractPath(l.System, slug)
}

// ProvidesSchemaDir returns <system>/provides/<slug>/schema/.
func (l Layout) ProvidesSchemaDir(slug string) string {
	return path.Join(l.System, "provides", slug, "schema")
}

// ProvidesFixturesValidDir returns <system>/provides/<slug>/fixtures/valid/.
func (l Layout) ProvidesFixturesValidDir(slug string) string {
	return path.Join(l.System, "provides", slug, "fixtures", "valid")
}

// ProvidesFixturesInvalidDir returns
// <system>/provides/<slug>/fixtures/invalid/.
func (l Layout) ProvidesFixturesInvalidDir(slug string) string {
	return path.Join(l.System, "provides", slug, "fixtures", "invalid")
}

// ProvidesArtifactsDir returns <system>/provides/<slug>/artifacts/ — the ONLY
// legal home contract-set-v2 gives a companion role (errors, vocabulary,
// limits, changelog, example, other), enforced by the envelope schema's
// role-conditional path pattern.
//
// It was missing entirely, and its absence was the second half of
// fb-20260806-c6ad38: with no forward constructor, the sidecar collector that
// `a2a submit` uses to carry a staged contract's files into the space had no
// artifacts/ arm either, so a declared companion never travelled at all. The
// reverse mapping (ContractForPath) and the forward constructors are kept in
// one file precisely so a directory cannot be legal in one direction and
// unknown in the other — this is the constructor that makes that true again.
func (l Layout) ProvidesArtifactsDir(slug string) string {
	return path.Join(l.System, "provides", slug, "artifacts")
}

// ContractForPath recognises the §4.2 path family owned by one contract:
//
//	<system>/provides/<slug>/contract.md
//	<system>/provides/<slug>/schema/**
//	<system>/provides/<slug>/fixtures/{valid,invalid}/**
//	<system>/provides/<slug>/artifacts/**
//
// It returns the standing contract ID and descriptor path. Keeping this reverse
// mapping beside Layout's forward constructors prevents CLI, MCP and CI from
// growing different definitions of "contract baseline file".
//
// # Why artifacts/ is here, and what its absence cost
//
// contract-set-v2 requires every COMPANION role — errors, vocabulary, limits,
// changelog, example, other — to live under artifacts/, and the envelope schema
// enforces that with a role-conditional path pattern, so a companion has no
// other legal location. This function did not know the directory existed.
//
// Everything downstream followed from that one gap: IsContractBaselinePath
// returned false for every artifacts/** path, so the submit validator's
// file-dispatch treated each companion as an ENVELOPE DRAFT and ran
// artifact.ParseFrontmatter over it — which a JSON companion cannot satisfy.
// `a2a contract publish` died in PrepareSubmission with "missing or malformed
// frontmatter delimiters", naming the companion. `a2a validate` and
// `a2a contract preflight` both passed, so the whole companion half of
// contract-set-v2 was unpublishable and only the irreversible verb said so.
//
// Reported as fb-20260806-c6ad38 from a real first publication. Two surfaces of
// one rule disagreed: the descriptor grammar mandated a directory the path
// layout was never widened to admit.
func ContractForPath(p string) (id, descriptorPath string, ok bool) {
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	if len(parts) < 4 || parts[1] != "provides" {
		return "", "", false
	}

	system, slug := parts[0], parts[2]
	contractID, err := artifact.MintStandingID("XC", system, slug)
	if err != nil {
		return "", "", false
	}
	layout, err := NewLayout(system)
	if err != nil {
		return "", "", false
	}
	descriptorPath = layout.ProvidesContract(slug)

	switch {
	case p == descriptorPath:
	case len(parts) >= 5 && parts[3] == "schema":
	case len(parts) >= 6 && parts[3] == "fixtures" && (parts[4] == "valid" || parts[4] == "invalid"):
	case len(parts) >= 5 && parts[3] == "artifacts":
	default:
		return "", "", false
	}
	return contractID, descriptorPath, true
}

// IsContractBaselinePath reports whether p is schema/fixture data carried
// beside a contract descriptor, rather than an envelope artifact itself.
func IsContractBaselinePath(p string) bool {
	_, descriptorPath, ok := ContractForPath(p)
	return ok && p != descriptorPath
}

// Requires returns <system>/requires/<id>.md (an XR requirement).
func (l Layout) Requires(id string) string {
	return path.Join(l.System, "requires", id+".md")
}

// ConsumesYAML returns <system>/consumes.yaml (the registered-consumer
// registry, D-022).
func (l Layout) ConsumesYAML() string {
	return path.Join(l.System, "consumes.yaml")
}

// Exchange returns <system>/exchanges/<id>.md — the shared location for
// every exchange/broadcast/response type authored by this system (XQ, XW,
// XH, XA, XS; §4.2 groups them under one directory).
func (l Layout) Exchange(id string) string {
	return path.Join(l.System, "exchanges", id+".md")
}

// EventFile returns <system>/events/<year>/<ulid>.yaml (a lifecycle
// event, sharded by year to keep directories bounded).
func (l Layout) EventFile(year, ulid string) string {
	return path.Join(l.System, "events", year, ulid+".yaml")
}

// DocsDir returns <system>/docs/ (free-form, non-normative).
func (l Layout) DocsDir() string {
	return path.Join(l.System, "docs")
}

// Decision returns decisions/<id>.md — a space-LEVEL location (not under
// any one system's section; XD decisions are multi-party, §4.2).
func Decision(id string) string {
	return path.Join("decisions", id+".md")
}

// VendoredDir returns vendored/<vendor>/ — a space-level, read-only
// mirror location (§4.4).
func VendoredDir(vendor string) string {
	return path.Join("vendored", vendor)
}
