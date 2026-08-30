// Package notification (types.go carries this package's own canonical doc
// comment; this file exists only for the ADR-001/ADR-023 import-boundary
// declaration below).
//
// ADR-001 import row, unchanged by ADR-023 (docs/decisions.md): artifact,
// cache, space, plus stdlib. ADR-001's Rules paragraph names this package
// (beside cache and avatar) as one of the few holding explicitly authorized
// bounded, disposable local I/O — not a precedent for durable authority.
package notification
