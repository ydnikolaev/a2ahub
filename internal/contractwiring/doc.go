// Package contractwiring is the one production assembly of the contract
// publication service: the event builder, the submit validator, and the
// glue that wires a WriteFunnel and a ContractPublicationRecovery into a
// space.ContractPublicationService, plus the candidate-freezing step that
// feeds it. cmd/a2a's contractP6Core.publish and cmd/a2a's own offline
// integration test (contract_lifecycle_integration_test.go) both call this
// package rather than each carrying their own copy of the assembly — the
// duplication that let a hand-copied branch drift out of step with
// production once already.
//
// It lives outside internal/space because two of its own dependencies
// (internal/cache and internal/template/contract_scaffold.go) already
// import internal/space; placing this package under internal/space would
// close an import cycle back to itself.
package contractwiring
