package cache

import "testing"

// TestContractNonAdoptableFromRaw pins the SECOND reader of `x_binding`.
//
// The rule that decides whether `a2a contract adopt` refuses lives in two
// places on purpose — internal/cli/cmd_contract.go computes it to refuse the
// pin, and mirror.go's own copy computes it so the dashboard can say so
// BEFORE the command is ever run (F4, agent-exchange-2026-08 wave 36).
// ADR-001's import table forbids the shared home that would make one copy
// impossible, and the duplication is argued for at xBindingProbe's own doc
// comment. What the duplication BUYS is a drift channel, and this file is the
// price of it: a second implementation of a refusal rule with no test is how
// two surfaces come to disagree about whether a contract may be pinned, with
// the CLI refusing and the dashboard silent.
//
// The one asymmetry worth stating, because it is what a reader will doubt:
// `false` is the answer for a descriptor that never declared `x_binding` at
// all AND for one that declared itself adoptable. That is not a lost
// distinction — it is P-1 applied to a refusal. Silence is not a refusal, so
// the surface must never invent one, and the dashboard correspondingly shows
// no affirmative "adoptable" badge (a green pill would assert a declaration
// the data cannot back). Only `unmet` cases here are true.
func TestContractNonAdoptableFromRaw(t *testing.T) {
	t.Parallel()

	const body = "\n\nBody.\n"
	head := func(extra string) []byte {
		return []byte("---\nschema: envelope/v2\nid: XC-axon-20260811-aa11\n" +
			"type: contract\ntitle: A contract\nspace: demo\nfrom: axon\n" +
			extra + "---" + body)
	}

	cases := []struct {
		name string
		raw  []byte
		want bool
	}{
		{
			// The short form. `none` is the only schema-valid scalar.
			name: "bare none sentinel refuses",
			raw:  head("x_binding: none\n"),
			want: true,
		},
		{
			name: "long form adoptable false refuses",
			raw: head("x_binding:\n  artifact_class: reference\n" +
				"  compatibility_status: none\n  adoptable: false\n" +
				"  runtime_pinnable: false\n"),
			want: true,
		},
		{
			name: "long form adoptable true permits",
			raw: head("x_binding:\n  artifact_class: interface\n" +
				"  compatibility_status: semver\n  adoptable: true\n" +
				"  runtime_pinnable: true\n"),
			want: false,
		},
		{
			// P-1: the field never declared at all is UNDECLARED, and
			// undeclared is not a refusal. Every contract published before
			// the 2026-08-10 amendment is this case.
			name: "x_binding absent entirely permits",
			raw:  head(""),
			want: false,
		},
		{
			// A long form that omits `adoptable` is schema-invalid, but this
			// probe is best-effort and runs on documents schema validation
			// has not necessarily judged. A missing bool must not read as
			// false — that would fabricate a refusal out of an omission.
			name: "long form omitting adoptable permits",
			raw:  head("x_binding:\n  artifact_class: reference\n"),
			want: false,
		},
		{
			// A scalar the schema would refuse decodes harmlessly rather
			// than erroring here; only `none` sets the sentinel.
			name: "unknown scalar is not the sentinel",
			raw:  head("x_binding: sometimes\n"),
			want: false,
		},
		{
			// Degrade toward permissive: never fabricate a refusal out of a
			// document this package could not read.
			name: "undecodable document permits",
			raw:  []byte("not a document at all"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := contractNonAdoptableFromRaw(tc.raw); got != tc.want {
				t.Fatalf("contractNonAdoptableFromRaw = %v, want %v", got, tc.want)
			}
		})
	}
}
