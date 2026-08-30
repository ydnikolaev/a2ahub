// Package validate is THE one validation engine (errors.go carries this
// package's own canonical doc comment; this file exists only for the
// ADR-001/ADR-023 import-boundary declaration below).
//
// ADR-001 import row, reconciled by ADR-023 (docs/decisions.md): artifact,
// fold, schema, sensitive — plus contract and version, granted by ADR-023
// where ADR-001 named neither, and provenance, likewise granted.
//
// The contract grant reverses ADR-001's original table cell, which is worth
// stating plainly because it reads backwards at a glance: the table once
// granted internal/contract an import of internal/validate. Nothing ever
// used that direction. The real edge runs the other way — this package
// imports internal/contract, in production, because judging "is this
// candidate's descriptor shape legal" (CheckContractDescriptorShape and its
// neighbors) requires the exact types internal/contract owns; a validator
// that judges a shape needs that shape's own type, not a copy of it. ADR-023
// is the decision that reconciled the direction; internal/contract carries
// no reciprocal import of this package, so no cycle is opened.
package validate
