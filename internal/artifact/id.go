package artifact

import (
	"crypto/rand"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

// base32Alphabet is Crockford's base32 alphabet, lowercase — the exact
// charset the §3.3 exchange/broadcast rand4 suffix example uses
// (`XQ-axon-20260721-k3f9`). It excludes i/l/o/u to avoid visual
// ambiguity with 1/0.
const base32Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// Class distinguishes the two §3.3 artifact-ID shapes.
type Class int

const (
	// ClassStanding is <PREFIX>-<system>-<slug>.
	ClassStanding Class = iota
	// ClassExchangeBroadcast is <PREFIX>-<system>-<YYYYMMDD>-<rand4>.
	ClassExchangeBroadcast
)

func (c Class) String() string {
	switch c {
	case ClassStanding:
		return "standing"
	case ClassExchangeBroadcast:
		return "exchange-broadcast"
	default:
		return "unknown"
	}
}

// ID is a parsed §3.3 artifact ID. Prefix is an opaque string at this
// layer (Open Q2, spec §01-foundation): no 8-type enum here —
// enum-closedness at mint/parse time is P2/P3's concern.
type ID struct {
	Raw    string
	Class  Class
	Prefix string
	System string
	Slug   string // standing only
	Date   string // exchange/broadcast only, YYYYMMDD
	Rand   string // exchange/broadcast only, 4-char lowercase base32
}

var (
	// prefixShape: opaque but shape-constrained — uppercase ASCII
	// letters only (matches every §3.1 example prefix; not an enum
	// check, a grammar check).
	prefixShape = regexp.MustCompile(`^[A-Z]+$`)

	// systemShape: the ID grammar's <system> token is treated as
	// hyphen-free at this layer so Parse can unambiguously separate it
	// from <slug> (which MAY contain hyphens, e.g. `country-vocabulary`).
	// See MintStandingID/MintExchangeID doc comments for the resulting
	// constraint on system names.
	systemShape = regexp.MustCompile(`^[a-z0-9]+$`)

	// exchangeRestShape: the strict <YYYYMMDD>-<rand4> shape.
	exchangeRestShape = regexp.MustCompile(`^([0-9]{8})-([` + base32Alphabet + `]{4})$`)

	// exchangeIntentShape: rest LOOKS LIKE an attempted date+rand (an
	// all-digit token, a hyphen, then anything) — once this shape is
	// seen, ParseID commits to the exchange-broadcast branch and
	// requires an exact exchangeRestShape match, rather than silently
	// falling back to treating it as a standing slug. This is how
	// "wrong date length" / "non-base32 suffix" (§6) are rejected
	// instead of accepted as odd-looking standing slugs.
	exchangeIntentShape = regexp.MustCompile(`^[0-9]+-`)

	// standingSlugShape: the <slug> token of a standing id — lowercase
	// alphanumeric kebab (`country-vocabulary`, `dep-a`), never a path
	// separator, `.` or `..`. This is a SECURITY guard, not just grammar:
	// without it ParseID accepts a slug like `../../../../etc/passwd`,
	// which layout.ProvidesContract/Exchange (path.Join) then collapse into
	// an escaping path — a local file-read oracle reachable through any
	// caller's `id`/`ids` (D-014 untrusted input, newly exposed via the
	// stdio MCP surface). Constraining the slug at the parse boundary closes
	// it for every consumer at once.
	standingSlugShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// MintStandingID mints a standing §3.3 ID: <PREFIX>-<system>-<slug>.
// prefix, system and slug MUST be non-empty; system MUST NOT contain a
// hyphen (see systemShape) — a hyphenated system name would not
// round-trip through ParseID, so Mint rejects it up front rather than
// minting an ID Parse cannot recover.
func MintStandingID(prefix, system, slug string) (string, error) {
	const op = "MintStandingID"
	if err := validateMintTriple(op, prefix, system, slug); err != nil {
		return "", err
	}
	return prefix + "-" + system + "-" + slug, nil
}

// MintExchangeID mints an exchange/broadcast §3.3 ID using the current
// UTC date and a crypto-random 4-char base32 suffix (no central counter —
// federation-safe).
func MintExchangeID(prefix, system string) (string, error) {
	return MintExchangeIDAt(prefix, system, time.Now(), rand.Reader)
}

// MintExchangeIDAt is the testable variant of MintExchangeID: the caller
// supplies the timestamp (converted to UTC) and the entropy source.
func MintExchangeIDAt(prefix, system string, at time.Time, entropy io.Reader) (string, error) {
	const op = "MintExchangeID"
	if prefix == "" || system == "" {
		return "", &Error{Op: op, Err: ErrEmptyField}
	}
	if !prefixShape.MatchString(prefix) {
		return "", &Error{Op: op, Input: prefix, Err: ErrMalformedID}
	}
	if !systemShape.MatchString(system) {
		return "", &Error{Op: op, Input: system, Err: ErrMalformedID}
	}
	date := at.UTC().Format("20060102")
	suffix, err := randomBase32(entropy, 4)
	if err != nil {
		return "", &Error{Op: op, Err: err}
	}
	return prefix + "-" + system + "-" + date + "-" + suffix, nil
}

func validateMintTriple(op, prefix, system, variable string) error {
	if prefix == "" || system == "" || variable == "" {
		return &Error{Op: op, Err: ErrEmptyField}
	}
	if !prefixShape.MatchString(prefix) {
		return &Error{Op: op, Input: prefix, Err: ErrMalformedID}
	}
	if !systemShape.MatchString(system) {
		return &Error{Op: op, Input: system, Err: ErrMalformedID}
	}
	return nil
}

func randomBase32(entropy io.Reader, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(entropy, buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = base32Alphabet[int(b)%len(base32Alphabet)]
	}
	return string(out), nil
}

// ParseID parses an ID string into its class, prefix, system and
// slug-or-date+rand. It rejects malformed strings with a typed error
// wrapping ErrMalformedID; it never panics.
func ParseID(s string) (ID, error) {
	const op = "ParseID"
	if s == "" {
		return ID{}, &Error{Op: op, Err: ErrMalformedID}
	}
	parts := strings.SplitN(s, "-", 3)
	if len(parts) != 3 {
		return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	prefix, system, rest := parts[0], parts[1], parts[2]
	if prefix == "" || !prefixShape.MatchString(prefix) {
		return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	if system == "" || !systemShape.MatchString(system) {
		return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	if rest == "" {
		return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	if exchangeIntentShape.MatchString(rest) {
		m := exchangeRestShape.FindStringSubmatch(rest)
		if m == nil {
			return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
		}
		return ID{
			Raw:    s,
			Class:  ClassExchangeBroadcast,
			Prefix: prefix,
			System: system,
			Date:   m[1],
			Rand:   m[2],
		}, nil
	}
	if !standingSlugShape.MatchString(rest) {
		return ID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	return ID{
		Raw:    s,
		Class:  ClassStanding,
		Prefix: prefix,
		System: system,
		Slug:   rest,
	}, nil
}

// Placement is the §4.2 layout shape an artifact's committed path takes.
// The §5.2 `id`-row guards are not uniform across the tree: three shapes
// exist, and each one carries the id's identity somewhere else. The
// mapping from an envelope TYPE to its Placement is deliberately NOT made
// here — this package keeps no 8-type enum (Open Q2, ADR-001) — it is the
// caller's (internal/validate's) one-line table.
type Placement int

const (
	// PlacementSectionFile is the default shape: <system>/<dir>/<id>.<ext>
	// — the id is the filename stem AND path's first segment is the owning
	// system's section (exchanges, requires, ...).
	PlacementSectionFile Placement = iota

	// PlacementProvidesContract is the contract descriptor's shape:
	// <system>/provides/<slug>/contract.md (§4.2). The filename is the
	// FIXED literal "contract.md" for every contract in every space, so the
	// stem can never equal the XC id — identity is carried by the
	// provides/<slug>/ DIRECTORY instead, and that is what gets checked.
	PlacementProvidesContract

	// PlacementSpaceLevel is the space-level shape: <dir>/<id>.<ext> under
	// a directory owned by NO single system (decisions/, §4.2's multi-party
	// exception). The stem still carries the id; the section guard does not
	// apply, since the id's <system> token names the DRAFTING system, not a
	// section owner.
	PlacementSpaceLevel
)

// Validate confirms the §5.2 `id`-row guards for an artifact assigned to
// id and stored at path (a space-relative path, e.g.
// "axon/exchanges/XQ-axon-20260721-k3f9.md") under the DEFAULT
// PlacementSectionFile shape:
//
//   - the filename stem (basename without extension) equals id.Raw exactly
//   - id.System equals the owning section — path's first segment, per the
//     "section" glossary term (00-meta.md §0.4: "the subtree of a space
//     owned and writable by exactly one system")
//
// Both guards are checked independently (neither short-circuits the
// other); a caller can discriminate which failed via errors.Is against
// ErrIDMismatch / ErrSectionMismatch. Artifacts whose §4.2 path is NOT
// that shape (contracts, decisions) go through ValidateAt with their own
// Placement — feeding them here reds them unconditionally.
func Validate(id ID, path string) error {
	return ValidateAt(id, path, PlacementSectionFile)
}

// ValidateAt is Validate's placement-aware form: it applies the §5.2
// `id`-row guards that ACTUALLY hold for placement's §4.2 path shape, and
// reports the same two sentinels for the same two substantive failures —
// "this path does not identify this artifact" (ErrIDMismatch) and "this
// path is not in this artifact's owning section" (ErrSectionMismatch) —
// so every caller's error mapping stays unchanged across placements.
func ValidateAt(id ID, path string, placement Placement) error {
	const op = "Validate"
	var errs []error

	switch placement {
	case PlacementProvidesContract:
		// Identity lives in the directory, not the stem: the path must be
		// exactly <system>/provides/<slug>/contract.md for THIS id. An
		// exchange-class id has no Slug, so it can never match — correct:
		// a contract id is a standing id by §3.3.
		if id.Slug == "" || path != ProvidesContractPath(id.System, id.Slug) {
			errs = append(errs, &Error{Op: op, Input: path, Err: ErrIDMismatch})
		}
		if section := sectionOf(path); section != id.System {
			errs = append(errs, &Error{Op: op, Input: path, Err: ErrSectionMismatch})
		}
	case PlacementSpaceLevel:
		// No owning section to check (that is the point of this shape);
		// the stem still carries the id.
		if stem := stemOf(path); stem != id.Raw {
			errs = append(errs, &Error{Op: op, Input: path, Err: ErrIDMismatch})
		}
	default:
		if stem := stemOf(path); stem != id.Raw {
			errs = append(errs, &Error{Op: op, Input: path, Err: ErrIDMismatch})
		}
		if section := sectionOf(path); section != id.System {
			errs = append(errs, &Error{Op: op, Input: path, Err: ErrSectionMismatch})
		}
	}

	return errors.Join(errs...)
}

// ProvidesContractPath renders §4.2's contract-descriptor path for a
// system+slug pair — <system>/provides/<slug>/contract.md, space-relative,
// forward slashes. It lives HERE, next to the guard that checks it
// (ValidateAt's PlacementProvidesContract branch), so the renderer and the
// guard can never drift apart; internal/space's Layout.ProvidesContract is
// the layout-facing name for this exact function, not a second copy.
func ProvidesContractPath(system, slug string) string {
	return path.Join(system, "provides", slug, "contract.md")
}

func stemOf(p string) string {
	base := path.Base(p)
	return strings.TrimSuffix(base, path.Ext(base))
}

func sectionOf(p string) string {
	clean := strings.TrimPrefix(path.Clean(p), "/")
	seg, _, _ := strings.Cut(clean, "/")
	return seg
}
