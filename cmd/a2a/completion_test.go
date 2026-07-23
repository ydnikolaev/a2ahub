package main

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// completion_test.go is P23's parity guard, a sibling of catalog_test.go's
// name-parity check: a verb added to buildCommands() but never surfaced to
// `a2a completion` (because completionCmds() drifted from the dispatch map) is
// exactly the silent gap this test exists to red. It independently recomputes
// the expected completion vocabulary from buildCommands() + the hidden-verb
// rule, then renders the real bash script and asserts every name is present —
// so completion tracks the dispatch surface automatically as verbs are added.

// hiddenFromCompletion is the ONE verb `a2a completion` omits: the machine-only
// __catalog meta verb (never listed in printUsage, so never completed).
var hiddenFromCompletion = map[string]bool{"__catalog": true}

func TestCompletionCoversEveryDispatchVerb(t *testing.T) {
	t.Parallel()

	script, err := cli.RenderCompletion("bash", completionCmds(), completionContractSubs())
	if err != nil {
		t.Fatalf("render bash completion: %v", err)
	}

	// Every non-hidden dispatch verb must be offered.
	for name := range buildCommands() {
		present := containsWord(script, name)
		if hiddenFromCompletion[name] {
			if present {
				t.Errorf("hidden verb %q leaked into completion", name)
			}
			continue
		}
		if !present {
			t.Errorf("dispatch verb %q missing from completion — completionCmds() has drifted from buildCommands()", name)
		}
	}

	// Every contract sub-verb must be offered.
	for _, sub := range cli.ContractSubcommands() {
		if !containsWord(script, sub.Name) {
			t.Errorf("contract sub-verb %q missing from completion", sub.Name)
		}
	}
}

// containsWord reports whether the script mentions name as a whole,
// space/quote/paren-delimited token — so "new" doesn't spuriously match inside
// "renew" and a substring can't mask a genuinely missing verb.
func containsWord(script, name string) bool {
	for _, line := range strings.Split(script, "\n") {
		for _, field := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == '"' || r == '(' || r == ')' || r == '\t'
		}) {
			if field == name {
				return true
			}
		}
	}
	return false
}
