// Package localserver is the one loopback-only read transport for
// operational snapshots and injected retained dashboard view-model bytes:
// revision plus view-model-digest SSE, no domain rules, no direct path
// reads, no durable state and no inbound writes. Dashboard rendering is
// injected behind a local consumer interface, so its implementation stays
// outside this package.
//
// ADR-001 import row, unchanged by ADR-023 (docs/decisions.md): operational
// alone, reached only through injected consumer interfaces — this is the
// sole loopback HTTP exception in the tree (ADR-001's Rules paragraph), and
// nothing else may depend on it (see .golangci.yml's
// internal-frontend-boundary depguard rule, which denies every internal/
// package except cli/mcp/localserver's own and internal/livee2e's
// build-tagged live drivers from importing internal/localserver at all).
package localserver
