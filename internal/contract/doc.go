// Package contract (types.go carries this package's own canonical doc
// comment; this file exists only for the ADR-001/ADR-023 import-boundary
// declaration below).
//
// ADR-001 import row, reconciled by ADR-023 (docs/decisions.md): artifact,
// plus operation and version, granted by ADR-023 where ADR-001 named
// neither. ADR-001's original cell also granted this package an import of
// internal/validate; no file ever used it, and ADR-023 prunes it — the
// direction the code actually needs runs the other way (see
// internal/validate/doc.go): validate is "the one validation engine" and
// judges this package's own descriptor/carried-set shapes, so it imports
// contract's types, not the reverse. This package carries no reciprocal
// import of internal/validate, so no cycle opens either way.
package contract
