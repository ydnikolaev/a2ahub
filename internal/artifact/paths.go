package artifact

import (
	"path"
	"sort"
	"strings"
)

// This file holds the pure path predicates shared by the contract side
// (internal/contract's cleanPortablePath and caseCollisionIssues) and the
// data-package side (internal/datapackage's safe-walk and install). Neither
// caller's vocabulary lives here: no contract.Issue, no datapackage sentinel
// — plain bool/string/slice in, plain bool/string/slice out, so each side can
// wrap the answer in its own refusal without a second implementation to
// drift from the first.
//
// Spec: docs/features/archive/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md §5
// Plan: docs/features/archive/agent-ops-2026-07/plans/05a-contract-data-exchange-loop.plan.md D-3

// CleanRelativePath reports whether value is a portable, contained,
// slash-separated relative path: not empty, not ".", not rooted, no
// backslash, no NUL byte, no ".." component anywhere, exactly equal to its
// own path.Clean form, and built only from ASCII letters, digits, ".", "_",
// "/" and "-".
//
// That last, most restrictive rule is deliberate and does double duty: it
// is what makes a Unicode homoglyph or a normalization mismatch (NFC vs
// NFD) impossible to smuggle through this predicate — a path that differs
// only by Unicode form contains a non-ASCII byte either way, so both forms
// are refused by the same rule rather than by a second, easier-to-miss
// normalization step. It also makes a Windows drive letter (e.g. "C:foo")
// refused for free: ":" is not in the allowed character set, so no separate
// drive-letter check is needed alongside it.
func CleanRelativePath(value string) bool {
	if value == "" || value == "." || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("._/-", rune(c)):
		default:
			return false
		}
	}
	return true
}

// dataPackagePrefix mirrors internal/datapackage's own PackagePrefix ("DP",
// id.go) and internal/space's own private dataPackagePrefix constant
// (data_resolve.go) — the same literal, duplicated a THIRD time here for the
// same layering reason internal/space's own copy documents ("mirroring
// datapackage.ParsePackageID's own check ... without importing it"):
// internal/space already imports this package (layout.go), so this package
// importing internal/space or internal/datapackage back would cycle. This
// is a recorded deviation, not an oversight — see IsDataPackageReadmePath's
// doc comment for the concrete follow-up once internal/space/data_delivery.go
// is grantable to a brief.
const dataPackagePrefix = "DP"

// IsDataPackageReadmePath reports whether p is the exact shape `a2a data
// pack`/`a2a data deliver` write a data package's own readme entry at:
// "<system>/data/<DP-id>/README.md" — the one payload file inside a data
// package's own directory that carries NO frontmatter by design (spec 04
// §11's 2026-08-09 AC8 amendment; the incident this closes, fb-20260812-
// d31acb / fb-20260812-f9cfac). The basename match is case-insensitive:
// cmd/a2a's classifyPackEntries matches "README.md" with strings.EqualFold
// and keys the declared package entry by the exact on-disk name it found,
// which DeliverDataPackage then commits verbatim — so a package packed from
// a source directory holding "readme.md" is delivered at that same
// lower-case name, and a predicate that only recognised the upper-case
// spelling would still fail that package the same way.
//
// This mirrors internal/space's own DataPackageForPath (data_delivery.go)
// structurally: the same "<system>/data/<DP-id>/<rest>" grammar, the same
// refusal of an empty/"."/".." segment anywhere in the path, the same DP-id
// validation via ParseID + the ClassExchangeBroadcast shape, and the SAME
// choice to take system from the path alone, never cross-checked against
// the id's own embedded system token (DataPackageForPath's own doc comment:
// a verifying system's report.json is committed under the CONSUMER's own
// section while the id still names the PRODUCER, so a same-system check
// here would disagree with that real write). It cannot call
// DataPackageForPath directly (see dataPackagePrefix's own doc comment for
// why), so the grammar exists twice in this repository — recorded here as a
// deviation, not silently forked; reducing DataPackageForPath to a thin call
// into this predicate is the natural follow-up once
// internal/space/data_delivery.go is grantable to a brief.
//
// It is deliberately narrower than "any file under the package's own
// directory" (DataPackageForPath's own scope): it recognises specifically
// the readme's own path shape — exactly one segment beneath the package
// directory, exactly the README name — because that is the only payload
// shape a *.md-candidate artifact walk can ever be asked to decode as an
// envelope in the first place. Every other package file (manifest.json,
// report.json, every dataset/index entry) is JSON or NDJSON, never
// markdown, so it never reaches an envelope decoder to begin with.
func IsDataPackageReadmePath(p string) bool {
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	if len(parts) != 4 || parts[1] != "data" || !strings.EqualFold(parts[3], "README.md") {
		return false
	}
	parsed, err := ParseID(parts[2])
	if err != nil || parsed.Prefix != dataPackagePrefix || parsed.Class != ClassExchangeBroadcast {
		return false
	}
	return true
}

// CaseCollisions reports, from paths, every path whose ASCII-lowercase form
// collides with a lexicographically earlier path in the set — the same
// predicate internal/contract's caseCollisionIssues computes, without the
// Issue vocabulary. An exact duplicate (the identical string appearing
// twice) is not reported as a collision; only a *different* path that
// lowercases to the same key is. The result is sorted for determinism.
//
// This matters wherever a set of paths may later be written to a
// case-insensitive filesystem (macOS's default, Windows): two declared
// entries that differ only by case name one file there, not two.
func CaseCollisions(paths []string) []string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	canonical := make(map[string]string, len(sorted))
	var collisions []string
	for _, p := range sorted {
		lower := strings.ToLower(p)
		prior, exists := canonical[lower]
		if !exists {
			canonical[lower] = p
			continue
		}
		if prior == p {
			continue
		}
		collisions = append(collisions, p)
	}
	return collisions
}
