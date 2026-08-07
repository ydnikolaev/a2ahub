// Package agentid is the ONE registry of coding-agent identities this repo
// recognizes, plus the detection that names the agent currently running.
//
// It exists because nothing ever established who acted. `actor.name` is one
// slot that had come to hold two different kinds of thing, and neither was
// checked:
//
//   - `kind: agent, name: yuranikolaev` — a person's OS login in the slot that
//     names a tool, because the resolver fell through to the OS username while
//     `kind` went on claiming a machine acted.
//   - `kind: agent, name: codex` — an agent id that is simply FALSE. The
//     getvisa contract's 02:47 publish carries it, and codex did not perform
//     that write. The name was self-reported at the command line
//     (`--actor-name` / A2A_ACTOR_NAME), nothing verified it, and the event
//     records no provenance for it. The commit author is the synthetic
//     `axon <axon@a2a.local>` the write funnel always stamps, so git cannot
//     settle it either.
//
// That second case is the argument for this package. An agent asked to name
// itself will sometimes name something else, and a shared append-only log then
// carries the mistake forever. An environment cannot be talked into lying
// about which binary is running.
//
// So detection OUTRANKS self-report: when the environment identifies the agent,
// that is what gets recorded, and a hand-typed `--actor-name` no longer decides
// who the machine was. An explicit `kind: human` remains the escape, because a
// person saying "this was me" is a claim about a different thing entirely.
//
// # What this package can and cannot say about events already committed
//
// Normalize reads a recorded name against the registry so a surface can tell
// an agent id from a person's login. That is all it establishes. For an event
// written before detection existed, a hit means THE RECORD SAYS so — not that
// it is true, as `codex` proves — and a surface must present it as the event's
// own claim rather than as an established fact. There is no field in event/v1
// distinguishing a detected identity from a typed one; carrying that
// distinction is a v2 change, and it is deferred with the rest of them.
//
// # Why this package holds no os.Getenv
//
// internal/cli and internal/mcp each pin env access to a single file by an
// explicit architectural rail stated in their own headers ("os.Getenv lives
// ONLY in this file"). Detection is env-shaped, so it takes a Lookup and those
// two sanctioned files are its only production callers. The in-repo precedent
// for that shape is the injected isTTY seam in internal/cli/cmd_update.go.
// It also makes every detection case an ordinary table test with no process
// environment involved.
//
// # Why detection covers one vendor and the registry covers twenty
//
// A vendor's environment is a fact about someone else's software, so it is
// READ before it is claimed, never recalled. This session's own environment
// was read on a real machine and Claude Code's variables are therefore wired.
// Every other vendor ships an id (so `A2A_ACTOR_AGENT=codex` records correctly
// and the dashboard renders its mark) with NO detector, because inventing one
// from memory would produce a confident wrong attribution — the exact failure
// this package exists to end. Adding a vendor is a small commit backed by that
// vendor's own documented environment.
package agentid

import (
	"path"
	"sort"
	"strings"
)

// Agent is what a detector could establish about the agent now running.
// Every field except ID may legitimately be empty: an environment that names
// the product need not also name its model or its effort setting.
type Agent struct {
	// ID is the normalized registry id — the key both the actor record and
	// the dashboard's icon set are written in terms of.
	ID string
	// Version is the agent product's own version, not the a2a binary's
	// (that is already recorded separately as produced_by.version).
	Version string
	// Model is the underlying model, when the environment names it.
	Model string
	// Effort is the reasoning-effort setting, when the environment names it.
	Effort string
	// Session identifies the agent's own session, for correlating several
	// events back to one conversation.
	Session string
}

// Lookup reads one environment variable. Production callers pass os.Getenv
// from the file their package allows it in; tests pass a map's accessor.
type Lookup func(string) string

// EnvOverride names the agent explicitly and outranks every detector, so a
// vendor with no wired detection is still recordable by hand today.
const EnvOverride = "A2A_ACTOR_AGENT"

// entry is one registry row. Aliases exist because a recorded name predates
// this registry: events already committed say `codex`, not `openai-codex`,
// and those events are immutable.
type entry struct {
	id       string
	display  string
	provider string
	aliases  []string
}

// registry is the closed set. The dashboard's AgentIcon component is keyed by
// exactly these ids, so an id can never exist in one and not the other.
//
// Membership rule: a tool that AUTHORS protocol writes on someone's behalf.
// A model vendor is not an agent, which is why the list carries `claude-code`
// and not `anthropic` — the actor is the tool that acted, and the model it
// used is a separate field.
var registry = []entry{
	{id: "claude-code", display: "Claude Code", provider: "Anthropic", aliases: []string{"claude", "claudecode"}},
	{id: "codex", display: "Codex", provider: "OpenAI", aliases: []string{"openai-codex"}},
	{id: "github-copilot", display: "GitHub Copilot", provider: "GitHub", aliases: []string{"copilot", "githubcopilot"}},
	{id: "cursor", display: "Cursor", provider: "Cursor"},
	{id: "windsurf", display: "Windsurf", provider: "Codeium"},
	{id: "gemini-cli", display: "Gemini CLI", provider: "Google", aliases: []string{"gemini"}},
	{id: "antigravity", display: "Antigravity", provider: "Google"},
	{id: "cline", display: "Cline", provider: "Cline"},
	{id: "roo-code", display: "Roo Code", provider: "Roo", aliases: []string{"roo"}},
	{id: "kilo-code", display: "Kilo Code", provider: "Kilo", aliases: []string{"kilo"}},
	{id: "opencode", display: "OpenCode", provider: "OpenCode"},
	{id: "aider", display: "Aider", provider: "Aider"},
	{id: "continue", display: "Continue", provider: "Continue"},
	{id: "amp", display: "Amp", provider: "Sourcegraph"},
	{id: "goose", display: "Goose", provider: "Block"},
	{id: "devin", display: "Devin", provider: "Cognition"},
	{id: "zed", display: "Zed", provider: "Zed"},
	{id: "jetbrains", display: "JetBrains AI", provider: "JetBrains", aliases: []string{"junie"}},
	{id: "replit", display: "Replit Agent", provider: "Replit"},
	{id: "warp", display: "Warp", provider: "Warp"},
	{id: "trae", display: "Trae", provider: "ByteDance"},
	{id: "augment", display: "Augment", provider: "Augment"},
}

var byKey = func() map[string]entry {
	m := make(map[string]entry, len(registry)*2)
	for _, e := range registry {
		m[e.id] = e
		for _, alias := range e.aliases {
			m[alias] = e
		}
	}
	return m
}()

// IDs returns the closed registry, sorted. The dashboard's icon coverage test
// reads this rather than a second hand-kept list.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for _, e := range registry {
		out = append(out, e.id)
	}
	sort.Strings(out)
	return out
}

// Normalize maps a recorded actor name to a registry id.
//
// A hit means the record NAMES an agent, so the human it acted for must come
// from the manifest owner rather than from this field. A miss means the name
// is a person's and stands on its own.
//
// It establishes nothing about truth. The getvisa contract's first publish
// records `name: codex` and codex did not write it; Normalize will still
// answer "codex", because that is what the event says. Callers rendering a
// pre-detection event must say whose claim it is (see this package's own doc
// comment), not present it as verified.
//
// It is deliberately exact-match after case folding, never a prefix or
// substring test. `codexal` is somebody's login, not Codex, and a surface that
// guesses otherwise attributes a person's act to a machine.
func Normalize(name string) (string, bool) {
	e, ok := byKey[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", false
	}
	return e.id, true
}

// Contradicts reports whether a caller-supplied actor name claims to be a
// DIFFERENT agent than the one actually running.
//
// This is the exact boundary of what an environment is allowed to overrule,
// and getting it wrong costs real money in both directions.
//
// Detection is authoritative about WHICH AGENT IS EXECUTING — nothing else can
// know that, and a model asked to say so will sometimes say `codex` when it is
// not. It is NOT authoritative about who the actor is in general. A caller
// naming a person, a service account or a test fixture is making a different
// kind of claim, and overruling it is simply wrong.
//
// The first version of this rule had no such boundary: detection beat every
// caller-supplied name outright. That broke the conformance harness, which
// deliberately writes as `live-e2e` and then LOOKS THE OPERATION UP by that
// name — every lookup missed, each burning 18 visibility attempts, and the
// logic tier went from ~5 minutes to past a 20-minute timeout. The harness was
// not misusing the flag; the rule was too wide.
//
// So: a claimed name that is not a registry id stands (it names something else
// entirely), an empty claim is filled by detection, and only a claim naming a
// rival AGENT is overruled.
func Contradicts(claimedName, detectedID string) bool {
	if detectedID == "" {
		return false
	}
	claimed, isAgent := Normalize(claimedName)
	return isAgent && claimed != detectedID
}

// Display returns the human-facing product name for an id ("Claude Code"),
// falling back to the id itself so an unknown value renders as itself rather
// than as an empty string.
func Display(id string) string {
	if e, ok := byKey[id]; ok {
		return e.display
	}
	return id
}

// Provider returns the organization behind an id ("Anthropic"), or "" when the
// id is not in the registry. Empty is the honest answer: this package will not
// infer a provider from a name it does not know.
func Provider(id string) string {
	if e, ok := byKey[id]; ok {
		return e.provider
	}
	return ""
}

// Detect names the agent now running, or reports that it could not.
//
// Order: the explicit override, then per-vendor detection, then the generic
// AI_AGENT hint. A false return is a normal outcome — a human at a terminal
// produces one — and callers must treat it as "no agent", never as an error.
func Detect(env Lookup) (Agent, bool) {
	if env == nil {
		return Agent{}, false
	}

	// The override outranks detection so a vendor with no detector is still
	// recordable, and so a detector that ever guesses wrong can be corrected
	// from the outside without a release.
	if raw := strings.TrimSpace(env(EnvOverride)); raw != "" {
		id, ok := Normalize(raw)
		if !ok {
			// An unregistered override is honoured verbatim rather than
			// dropped: the operator has named something this build does not
			// know about, and silently recording nothing would be worse than
			// recording a name with no icon.
			id = strings.ToLower(raw)
		}
		return Agent{
			ID:      id,
			Version: strings.TrimSpace(env("A2A_ACTOR_AGENT_VERSION")),
			Model:   strings.TrimSpace(env("A2A_ACTOR_MODEL")),
			Effort:  strings.TrimSpace(env("A2A_ACTOR_EFFORT")),
		}, true
	}

	if a, ok := detectClaudeCode(env); ok {
		return a, true
	}
	return detectGeneric(env)
}

// detectClaudeCode reads the variables observed on a real Claude Code session
// (2.1.223, macOS): CLAUDECODE=1, CLAUDE_CODE_ENTRYPOINT=cli,
// CLAUDE_CODE_EXECPATH=<...>/versions/2.1.223, CLAUDE_CODE_EFFORT_LEVEL=high,
// CLAUDE_CODE_SESSION_ID=<uuid>.
//
// Two independent markers are accepted because CLAUDECODE is the documented
// flag while CLAUDE_CODE_ENTRYPOINT survives in contexts that re-exec. The
// model is NOT read: no variable observed in that environment names it, and
// this package does not invent one.
func detectClaudeCode(env Lookup) (Agent, bool) {
	if env("CLAUDECODE") != "1" && strings.TrimSpace(env("CLAUDE_CODE_ENTRYPOINT")) == "" {
		return Agent{}, false
	}
	return Agent{
		ID:      "claude-code",
		Version: versionFromExecPath(env("CLAUDE_CODE_EXECPATH")),
		Effort:  strings.TrimSpace(env("CLAUDE_CODE_EFFORT_LEVEL")),
		Session: strings.TrimSpace(env("CLAUDE_CODE_SESSION_ID")),
	}, true
}

// versionFromExecPath pulls the version out of an install path whose last
// segment is the version directory (".../versions/2.1.223"). A segment that
// does not look like a version yields "" rather than a guess — an install laid
// out differently should report no version, not a directory name.
func versionFromExecPath(execPath string) string {
	base := path.Base(strings.TrimSpace(strings.TrimSuffix(execPath, "/")))
	if base == "" || base == "." || base == "/" {
		return ""
	}
	if !looksLikeVersion(base) {
		return ""
	}
	return base
}

func looksLikeVersion(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// detectGeneric reads AI_AGENT, a cross-vendor hint observed in the form
// "claude-code_2-1-223_agent" — id, then a dash-separated version, then a
// role segment.
//
// It is the LAST resort and is deliberately strict: the leading segment is
// accepted only when it normalizes to a registry id. The variable's format was
// observed on one machine, not read from a specification, so a value this
// package does not recognize yields no detection rather than an invented one.
func detectGeneric(env Lookup) (Agent, bool) {
	raw := strings.TrimSpace(env("AI_AGENT"))
	if raw == "" {
		return Agent{}, false
	}
	parts := strings.Split(raw, "_")
	id, ok := Normalize(parts[0])
	if !ok {
		return Agent{}, false
	}
	a := Agent{ID: id}
	if len(parts) > 1 {
		if v := strings.ReplaceAll(parts[1], "-", "."); looksLikeVersion(v) {
			a.Version = v
		}
	}
	return a, true
}
