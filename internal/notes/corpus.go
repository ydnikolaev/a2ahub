package notes

import "github.com/ydnikolaev/go-foundation/units/adoption"

// CorpusPattern and StandingPath are where a2a's corpus lives inside
// releasenotes.FS: one root *.yaml file per release, and the one standing
// known-issues document. They are the two arguments adoption.Load and
// adoption.LoadStanding need, spelled once so every surface reads the same
// files.
const (
	CorpusPattern = "*.yaml"
	StandingPath  = "current/known-issues.yaml"
)

// Policy is a2a's adoption policy: a `scope: space` change obliges the
// reader, because the space is the other party a2a writes to. It is the one
// definition the CLI and MCP surfaces share; internal/mcp may not import
// internal/cli (ADR-001), and two copies of a predicate is how the two
// surfaces once ordered the same projection differently.
func Policy() adoption.Policy {
	return adoption.Policy{PartyScopes: []string{"space"}}
}

// Verbs names a2a's read and record commands as the adoption notice prints
// them.
func Verbs() adoption.Verbs {
	return adoption.Verbs{Tool: "a2a", Adapt: "a2a adapt", Done: "a2a adapt --done"}
}
