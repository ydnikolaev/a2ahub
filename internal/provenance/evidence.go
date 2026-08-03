// Package provenance owns the bounded, diagnostic-only projection of event
// actor and producer attribution. These values are self-asserted evidence for
// readers; lifecycle authorization continues to consume only fold.Actor.System.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"

	"github.com/ydnikolaev/a2ahub/internal/sensitive"
)

// SafeSessionPattern is the frozen first-party session identifier contract.
// Credential-shaped values matching the alphabet are still unsafe; callers
// should use IsSafeSession rather than matching this expression alone.
const SafeSessionPattern = `^[A-Za-z0-9._:-]{1,128}$`

var safeSession = regexp.MustCompile(SafeSessionPattern)

// Actor is the fixed event actor evidence shape. Model and Session are
// diagnostic attribution only and must never be translated into fold.Actor.
type Actor struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	System  string `json:"system,omitempty"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// Producer is the self-asserted tool/version stamp attached to an event.
type Producer struct {
	Tool    string `json:"tool,omitempty"`
	Version string `json:"version,omitempty"`
}

// EventEvidence carries optional receipt and provenance fields beside the
// fold-owned event. Keeping this value separate makes it impossible for actor
// model/session or producer metadata to participate in fold authorization or
// ordering accidentally.
type EventEvidence struct {
	Receipt  string   `json:"state,omitempty"`
	Actor    Actor    `json:"actor"`
	Producer Producer `json:"produced_by,omitzero"`
}

// NewEventEvidence constructs the safe public/read projection of one event's
// diagnostic fields. Safe session identifiers survive byte-for-byte; an
// unsafe legacy value is represented only by a stable full SHA-256 reference.
func NewEventEvidence(receipt string, actor Actor, producer Producer) EventEvidence {
	actor.Session = SafeSessionEvidence(actor.Session)
	return EventEvidence{Receipt: receipt, Actor: actor, Producer: producer}
}

// SafeSessionEvidence preserves a safe identifier exactly, omits an absent
// value, and replaces every unsafe legacy value with sha256:<full-lower-hex>.
func SafeSessionEvidence(session string) string {
	if session == "" || IsSafeSession(session) {
		return session
	}
	sum := sha256.Sum256([]byte(session))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IsSafeSession applies both the bounded identifier grammar and the closed
// credential-shape denylist. The latter matters because many real credential
// formats use only characters permitted by the identifier grammar.
func IsSafeSession(session string) bool {
	return safeSession.MatchString(session) && !sensitive.Identifier(session)
}
