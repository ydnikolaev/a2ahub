package main

import (
	"slices"
	"testing"
)

// TestDataTargetArgsSelectsOnlyRoutingTarget is dataTargetArgs' own parity
// test with contract_p6_wiring_test.go's TestContractTargetArgsSelectsOnlyXCArtifact
// — one table per data sub-verb's own routing source (data_command_wiring.go's
// own doc comment: pack routes on --contract, deliver on --fulfills, fetch/
// verify on their bare positional package id).
//
// The "verify with --record" case is a regression test for a real bug this
// wave's change #2 (Pass -> Record rename) surfaced: cmd_data.go's own
// `data verify` flag was renamed from --pass to --record, but
// dataTargetFlagGrammar's own boolean-flag table for "verify" still listed
// "pass" — so `a2a data verify <id> --record` hit the unrecognized-flag
// `default: return nil` branch in dataTargetArgs' parsing loop, and routing
// silently fell back to resolveLifecycleDepsWithPolicy's own default-space
// selection instead of the id's own space. In a multi-space project that is
// the WRONG mirror, silently. Seeded-red receipt: revert
// dataTargetFlagGrammar's "verify" case back to booleans("pass", "json") and
// this case fails (dataTargetArgs returns nil instead of the package id).
func TestDataTargetArgsSelectsOnlyRoutingTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "pack routes on --contract", args: []string{"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders", "--profile", "synthetic", "--format", "json"}, want: []string{"XC-axon-orders"}},
		{name: "pack --from value cannot masquerade", args: []string{"pack", "--from", "XC-beta-wrong", "--contract", "XC-axon-orders@1.0.0", "--profile", "synthetic", "--format", "json"}, want: []string{"XC-axon-orders"}},
		{name: "deliver routes on --fulfills", args: []string{"deliver", "staging/attempt-1", "--fulfills", "XW-axon-20260804-ef56"}, want: []string{"XW-axon-20260804-ef56"}},
		{name: "fetch routes on the positional package id", args: []string{"fetch", "DP-axon-20260804-ab12", "--to", "vendor/orders"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify routes on the positional package id", args: []string{"verify", "DP-axon-20260804-ab12"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify with --record still routes on the positional package id", args: []string{"verify", "DP-axon-20260804-ab12", "--record"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "verify with --record --json still routes", args: []string{"verify", "DP-axon-20260804-ab12", "--record", "--json"}, want: []string{"DP-axon-20260804-ab12"}},
		{name: "pack subcommand only", args: []string{"pack"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := dataTargetArgs(test.args); !slices.Equal(got, test.want) {
				t.Fatalf("dataTargetArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}
