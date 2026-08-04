package main

import (
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// dataTargetArgs is `a2a data`'s analogue of contractTargetArgs (wire.go):
// it removes the `data` subcommand and reduces the target-selection input
// to an actual artifact id, so a flag VALUE (a destination, a digest, a
// staging path) can never masquerade as the routing target — the same
// discipline contractTargetArgs' own doc comment describes.
//
// Its flag grammar differs per sub-verb from contract's, and so does the
// SOURCE of the routing id, per this wave's own brief:
//   - pack routes on --contract (an XC- contract ref, the exact pinned
//     version being packed against);
//   - deliver routes on --fulfills (an XW- work_request id — spec 05a §T1
//     "the work_request this delivery answers" — never the positional
//     staging root, which is a local filesystem path with no space
//     identity of its own);
//   - fetch and verify route on their bare positional DP- package id.
//
// Wired into buildCommands()/runData() (wire.go's resolveDataDeps) via one
// call to resolveLifecycleDepsWithPolicy(ctx, p, dataTargetArgs(args), ...)
// — the same shape runContract already uses. Kept deliberately
// self-contained (no reference to any wire.go-only symbol) so it still
// compiles standing alone.
func dataTargetArgs(args []string) []string {
	if len(args) < 2 {
		return nil
	}
	action := args[0]
	valueFlags, booleanFlags, ok := dataTargetFlagGrammar(action)
	if !ok {
		return nil
	}

	flagValues := make(map[string]string, len(valueFlags))
	var positionals []string
	positionalOnly := false
	for index := 1; index < len(args); index++ {
		candidate := args[index]
		if !positionalOnly && candidate == "--" {
			positionalOnly = true
			continue
		}
		if !positionalOnly && strings.HasPrefix(candidate, "-") {
			name, value, hasValue := strings.Cut(strings.TrimLeft(candidate, "-"), "=")
			switch {
			case booleanFlags[name]:
				continue
			case valueFlags[name] && hasValue:
				flagValues[name] = value
				continue
			case valueFlags[name]:
				index++
				if index >= len(args) {
					return nil
				}
				flagValues[name] = args[index]
				continue
			default:
				// The command will report the unknown flag. Target routing
				// must not guess whether its next token is a value or an
				// artifact.
				return nil
			}
		}
		positionals = append(positionals, candidate)
	}

	switch action {
	case "pack":
		ref, ok := flagValues["contract"]
		if !ok {
			return nil
		}
		id, _, _ := strings.Cut(ref, "@")
		parsed, err := artifact.ParseID(id)
		if err != nil || parsed.Prefix != "XC" {
			return nil
		}
		return []string{id}
	case "deliver":
		id, ok := flagValues["fulfills"]
		if !ok {
			return nil
		}
		parsed, err := artifact.ParseID(id)
		if err != nil || parsed.Prefix != "XW" {
			return nil
		}
		return []string{id}
	case "fetch", "verify":
		if len(positionals) == 0 {
			return nil
		}
		id := positionals[0]
		parsed, err := artifact.ParseID(id)
		if err != nil || parsed.Prefix != datapackage.PackagePrefix {
			return nil
		}
		return []string{id}
	default:
		return nil
	}
}

// dataTargetFlagGrammar describes only the flags relevant while locating
// each `a2a data <sub>` verb's routing target — mirrors
// contractTargetFlagGrammar's own doc comment (wire.go): the command
// itself remains the authority for parsing and validation; this earlier
// pass merely skips known flag values so they cannot influence space
// selection. Kept in sync with cmd_data.go's own per-verb flag.FlagSet
// definitions (internal/cli) — a flag added there without a row here would
// let that flag's VALUE be misread as the routing target whenever it
// happens to look like an artifact id.
func dataTargetFlagGrammar(action string) (valueFlags, booleanFlags map[string]bool, ok bool) {
	values := func(names ...string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, name := range names {
			out[name] = true
		}
		return out
	}
	booleans := values
	actorFlags := []string{"actor-kind", "actor-name", "actor-model"}

	switch action {
	case "pack":
		return values("contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max-attempts"), booleans("json"), true
	case "deliver":
		return values(append(actorFlags, "fulfills", "supersedes", "expect-pack")...), booleans("json"), true
	case "fetch":
		return values("to"), booleans("json"), true
	case "verify":
		return values(actorFlags...), booleans("pass", "json"), true
	default:
		return nil, nil, false
	}
}
