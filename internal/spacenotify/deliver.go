// deliver.go turns a rendered []Message into P5's own delivery-record
// stdout shape (spec 04 T1 "The delivery record, because P5 depends on
// it"): send order preserved, one DeliveryRecord per message, --dry-run
// and a real run sharing this ONE function (US-3's own point — "a dry run
// a usable rehearsal instead of a different code path").
package spacenotify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ydnikolaev/a2ahub/internal/sensitive"
)

// DeliveryRecord is spec 04 T1's own contract — the exact shape P5's job
// summary reads off this verb's stdout. Field set and order are pinned by
// the spec table; nothing here is this package's own invention.
type DeliveryRecord struct {
	// Artifact is the artifact id, or — for a coalesced digest message — a
	// "digest (<count>)" label (spec 04 T1: "the artifact id, or `digest`
	// plus its count"). This exact formatting is this package's own choice
	// (§7: "None on the wire" — the record has no byte-frozen schema).
	Artifact string `json:"artifact"`
	Class    string `json:"class"`
	Chat     string `json:"chat"`
	Renderer string `json:"renderer"`
	// Outcome is "sent", "dry-run", or "failed" — never any other value.
	Outcome string `json:"outcome"`
	// Truncated carries through Message.Truncated's own "was this message
	// shortened" fact — true iff Message.Truncated is non-empty.
	Truncated bool `json:"truncated"`
	// Error is present only when Outcome == "failed": the API's own
	// description (already redacted) or a transport/refusal reason. Never
	// the token, by construction — see telegram.go's own Token/
	// errTransportFailed doc comments.
	Error string `json:"error,omitempty"`
	// Payload is the EXACT bytes a real send would have put on the wire,
	// present only when Outcome == "dry-run".
	//
	// Without it a dry run proves nothing about the one thing it exists to
	// answer. Spec 04 T1 promises `--dry-run` "renders and prints exactly
	// what would be sent"; spec 05's requirement 3 promises the payloads
	// reach the job summary so a layout can be reviewed BEFORE it reaches a
	// chat. An earlier version returned this record before the renderer ran
	// at all, so the operator's own review path — "send me these two
	// artifacts, I want to see the layout" — would have printed a manifest of
	// what was skipped and none of what it looked like.
	//
	// Rendering here also makes the dry run a real rehearsal: a renderer
	// panic, an over-budget message or a bad block shape surfaces in the
	// dry run rather than on the first live send.
	Payload string `json:"payload,omitempty"`
}

// Send dispatches every message in msgs IN ORDER, returning one
// DeliveryRecord per message in that same order (spec 04 T1: "in send
// order"). dryRun performs ZERO API calls — every record's Outcome is
// "dry-run" — and is otherwise the identical code path a real run takes
// (US-3).
func Send(ctx context.Context, client *Client, msgs []Message, dryRun bool) []DeliveryRecord {
	records := make([]DeliveryRecord, 0, len(msgs))
	for _, msg := range msgs {
		records = append(records, sendOne(ctx, client, msg, dryRun))
	}
	return records
}

func sendOne(ctx context.Context, client *Client, msg Message, dryRun bool) DeliveryRecord {
	rec := DeliveryRecord{
		Artifact:  artifactLabel(msg),
		Class:     msg.Class,
		Chat:      msg.Route.Chat,
		Renderer:  msg.Route.Renderer,
		Truncated: len(msg.Truncated) > 0,
	}
	if dryRun {
		rec.Outcome = "dry-run"
		rec.Payload = renderPayload(msg)
		return rec
	}

	// AC13: the token is read from the env var route.secret names IN THE
	// MESSAGE — the ONLY source; this verb never re-reads space.yaml (that
	// read, and the "only v1's declared secret is legal" refusal, already
	// happened once in `notify render`, spec 03 step 6).
	raw := os.Getenv(msg.Route.Secret)
	if raw == "" {
		// AC5: refuse BEFORE sending anything, naming the secret.
		rec.Outcome = "failed"
		rec.Error = fmt.Sprintf("environment variable %s is not set", msg.Route.Secret)
		return rec
	}
	token := NewToken(raw)

	var err error
	if msg.Route.Renderer == "rich" {
		err = client.SendRich(ctx, token, msg)
	} else {
		err = client.SendHTML(ctx, token, msg)
	}
	if err != nil {
		rec.Outcome = "failed"
		rec.Error = safeErrorText(err)
		return rec
	}
	rec.Outcome = "sent"
	return rec
}

// renderPayload produces exactly what the corresponding live send would put
// on the wire, through the SAME renderer the real path picks — never a
// second formatting of the same facts.
//
// For the rich path that is the JSON body of `rich_message`; for HTML it is
// the message text itself. Both are what a reviewer needs to judge a layout,
// and neither can drift from the live path without the live path changing
// too, because the branch below is the same branch sendOne takes.
func renderPayload(msg Message) string {
	if msg.Route.Renderer == "rich" {
		encoded, err := json.Marshal(RenderRich(msg, msg.Route.Locale))
		if err != nil {
			// A rich block set that cannot be encoded is a real finding, and
			// a dry run is exactly where it should surface — say so rather
			// than printing an empty payload that reads as "nothing to send".
			return fmt.Sprintf("rich payload could not be encoded: %v", err)
		}
		return string(encoded)
	}
	return RenderHTML(msg, msg.Route.Locale)
}

// artifactLabel is spec 04 T1's own "the artifact id, or `digest` plus its
// count" rule.
func artifactLabel(msg Message) string {
	if msg.Class == ClassDigest && msg.Digest != nil {
		return fmt.Sprintf("digest (%d)", msg.Digest.Count)
	}
	if msg.Artifact != nil {
		return msg.Artifact.ID
	}
	return ""
}

// safeErrorText is a defensive second layer over telegram.go's own
// already-token-free errors (AC6): every error sendOne can receive was
// built by telegram.go without ever touching a raw transport error or the
// token value, but this still re-checks the FINAL text through
// internal/sensitive before it reaches a delivery record — the same
// "redact before printing" discipline apiError applies once already.
func safeErrorText(err error) string {
	text := err.Error()
	if sensitive.ContainsCredentialText(text) {
		return "[redacted]"
	}
	return text
}
