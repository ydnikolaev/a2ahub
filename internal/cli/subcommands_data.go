package cli

// DataSubcommand describes one `a2a data <sub>` sub-verb for external
// surface enumeration — mirrors ContractSubcommand's own shape
// (cmd_contract.go) so the same four consumers (help, completion, catalog,
// the MCP parity test) can read this family the same way they already read
// the contract one.
type DataSubcommand struct {
	Name     string // e.g. "pack"
	Synopsis string
}

// DataSubcommands is the SSOT list of the `a2a data` family's sub-verbs
// (spec 05a §T1) for surface enumeration. The data sub-verbs are
// dispatched by the bare switch in DataCommand.Run (they are NOT
// registered as individual cli.Command values / buildCommands keys),
// exactly as ContractSubcommands' own doc comment describes for contract —
// this list is their only machine-enumerable home. KEEP IN SYNC with that
// switch: a sub-verb added there without a row here (or vice versa) is
// exactly the drift the parity gate exists to catch.
//
// The name order here is also what cmd/a2a/mcp_parity_test.go's
// TestDataSubcommandsMatchMCPDataActions requires to be byte-identical,
// name-for-name and in order, to mcp.DataActions — the MCP a2a_data tool's
// own closed action enum.
func DataSubcommands() []DataSubcommand {
	return []DataSubcommand{
		{Name: "pack", Synopsis: "pack a local directory against an exact contract version, write-free (--contract, --from, --profile, --format, --expires)"},
		{Name: "deliver", Synopsis: "deliver a packed staging root as one commit: payload + manifest + handoff (--fulfills, --supersedes, --expect-pack)"},
		{Name: "fetch", Synopsis: "fetch and install a package by id, digest-verified before any byte is written (--to)"},
		{Name: "verify", Synopsis: "re-verify a fetched package against its pinned contract and write a verification report (--pass)"},
	}
}
