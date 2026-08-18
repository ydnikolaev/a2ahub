// Package spacenotify turns a bare space-repo checkout into P3's one typed
// message model (space-notify-2026-08 specs/03-notify-projection.md): one
// object per route per qualifying artifact, carrying the facts a human
// needs on a phone plus a copy-paste agent prompt.
//
// This package writes ZERO protocol rules. Every legality answer comes
// from internal/fold (through internal/cache's BuildNotifyIndex — the ONE
// exported entry point into the directory-shaped folding assembly, spec
// 03 §5); every "whose turn" comes from internal/pendency, also through
// that same projection. It performs no directory walk of its own — no
// second artifact/event traversal, no second membership view, no second
// commit-order pass (AC10).
package spacenotify

import "github.com/ydnikolaev/a2ahub/internal/agentprompt"

// Message classes (spec 03 "The message model" / specs/01 §T2's event
// vocabulary, bound to §11.3 rows). Digest is a MESSAGE shape, never an
// artifact class — see classify.go.
const (
	ClassHumanGate = "human-gate"
	ClassBlocking  = "blocking"
	ClassPublished = "published"
	ClassDigest    = "digest"
)

// DeclaredSecret is the ONE secret v1 declares (schemas/manifest/v1's own
// `secret` field default, space-notify-2026-08 P1 spec 01 §T2) — a route
// naming any other secret is refused at render time (spec 03 step 6).
//
// This is the secret's NAME, never its value: the whole epic's premise is that
// the token lives only in the space repo's Actions secrets and reaches the
// binary through the environment variable this names. G101 cannot tell the two
// apart, so the suppression is declaration-scoped — the same shape and the same
// reason as internal/livee2e/config.go's env-var name block — and G101 stays
// live everywhere a hardcoded secret would actually be one.
//
//nolint:gosec // G101: an env-var NAME, not a credential — rationale above.
const DeclaredSecret = "TG_BOT_TOKEN"

// Message is one object in `a2a notify render`'s output array — one per
// route per qualifying artifact. Fields are facts; sentences are P4's job
// (04-render-and-transport.md).
type Message struct {
	Route       Route                    `json:"route"`
	Class       string                   `json:"class"`
	Artifact    *Artifact                `json:"artifact,omitempty"`
	Parties     *Parties                 `json:"parties,omitempty"`
	Turn        *Turn                    `json:"turn,omitempty"`
	Urgency     *Urgency                 `json:"urgency,omitempty"`
	Description string                   `json:"description,omitempty"`
	Prompt      *agentprompt.AgentPrompt `json:"prompt,omitempty"`
	Links       *Links                   `json:"links,omitempty"`
	Truncated   []string                 `json:"truncated,omitempty"`
	// Digest is populated ONLY when Class == ClassDigest, in which case
	// Artifact/Parties/Turn/Urgency/Description/Prompt/Links all stay nil
	// — a digest carries "no" prompt because "a prompt that names no
	// single artifact is an instruction to go and do nothing" (spec 03).
	Digest *Digest `json:"digest,omitempty"`
}

// Route carries which route this message is for — schemas/manifest/v1's
// own notification_routes[] entry, defaults already applied (locale,
// renderer, secret) so a reader never has to know the schema's own
// default values. Secret carries the NAME only, never a value (P4's
// `notify send` reads it to know which env var holds the token).
type Route struct {
	Chat     string `json:"chat"`
	Topic    *int   `json:"topic,omitempty"`
	Locale   string `json:"locale"`
	Renderer string `json:"renderer"`
	Secret   string `json:"secret"`
	For      string `json:"for,omitempty"`
}

// Artifact is the artifact's own id/type/title/space/thread — absent for
// a digest message.
type Artifact struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Space  string `json:"space"`
	Thread string `json:"thread,omitempty"`
}

// Parties is from -> to, plus the participant THIS route serves (route.For,
// "" when the route serves every participant).
type Parties struct {
	From string   `json:"from"`
	To   []string `json:"to"`
	For  string   `json:"for,omitempty"`
}

// Turn is internal/pendency's own verdict for this artifact, carried
// whole — never re-derived. It is the SAME verdict regardless of which
// route/participant reads it (spec 03's own measured finding: whose turn
// it is is a property of the artifact).
type Turn struct {
	Owners    []string `json:"owners,omitempty"`
	Expected  string   `json:"expected,omitempty"`
	Why       string   `json:"why"`
	HumanGate string   `json:"human_gate,omitempty"`
}

// Urgency is priority/deadline/overdue — absent (nil) rather than guessed
// when the artifact carries neither a priority nor a needed_by.
type Urgency struct {
	Priority string `json:"priority,omitempty"`
	Deadline string `json:"deadline,omitempty"`
	Overdue  bool   `json:"overdue,omitempty"`
}

// Links is the artifact permalink in the space repo (repo-relative — this
// verb never learns the repo's own hosting URL) and the thread link, when
// resolvable.
type Links struct {
	Artifact string `json:"artifact,omitempty"`
}

// DigestItem is one (id, type, title) row inside a Digest's bounded list.
type DigestItem struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// Digest is the coalesced form a route's kept set takes once it exceeds
// --limit (spec 03 step 5) — count plus a bounded (id, type, title) list,
// never per-artifact facts and never a prompt.
type Digest struct {
	Count int          `json:"count"`
	Items []DigestItem `json:"items"`
}
