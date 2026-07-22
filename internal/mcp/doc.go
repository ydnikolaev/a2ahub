// Package mcp implements P14 (`a2a mcp`, OP-216): a stdio JSON-RPC 2.0 MCP
// server whose tool registry maps 1:1 to the §7.7-enumerated OP subset.
//
// ADR-001 boundary (binding, never relaxed): this package NEVER imports
// internal/cli. It re-wires the same core packages
// (artifact/schema/fold/validate/host/space/cache/template) that
// cmd/a2a/wire.go wires for the CLI — an MCP server is a long-lived stdio
// session, its wiring legitimately differs from the CLI's per-invocation
// wiring, so the construction is duplicated here rather than extracted into
// a shared layer (plan 14 Placement decisions).
//
// Event-doc construction is ALSO duplicated, not extracted: this package
// builds its own event/v1 projection structs (eventDoc, eventActor, ...)
// matching internal/cli's cmd_lifecycle.go/cmd_submit.go/cmd_contract.go
// shape field-for-field, never importing them. The
// cmd/a2a/mcp_equivalence_test.go suite is the anti-drift gate that proves
// byte-identical output between the two surfaces.
package mcp
