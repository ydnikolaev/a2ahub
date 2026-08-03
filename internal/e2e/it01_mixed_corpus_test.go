package e2e

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// TestIT01MixedCorpusFoldAndReceiptFloor is P7 IT-01's exact composite. It
// deliberately keeps the assertions together: splitting schema identity,
// fold equivalence, rollout applicability and historical readability across
// package-local unit tests previously made it possible for each slice to be
// green without proving the integrated compatibility story.
func TestIT01MixedCorpusFoldAndReceiptFloor(t *testing.T) {
	t.Parallel()

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("load embedded corpus: %v", err)
	}
	engine := validate.New(corpus)
	membership := func(string) fold.MembershipStatus { return fold.MembershipMember }

	t.Run("distinct v1 and v2 documents select distinct field sets and fold equally", func(t *testing.T) {
		t.Parallel()

		v1 := contractPublishDocument("event/v1", false)
		v2 := contractPublishDocument("event/v2", true)
		v1Raw := mustJSON(t, v1)
		v2Raw := mustJSON(t, v2)
		if reflect.DeepEqual(v1, v2) || string(v1Raw) == string(v2Raw) {
			t.Fatal("event/v1 and event/v2 fixtures unexpectedly collapsed to one document")
		}

		if violations, err := corpus.ValidateEvent("v1", v1); err != nil || len(violations) != 0 {
			t.Fatalf("event/v1 rejected its historical document: violations=%+v err=%v", violations, err)
		}
		if violations, err := corpus.ValidateEvent("v2", v2); err != nil || len(violations) != 0 {
			t.Fatalf("event/v2 rejected its publication document: violations=%+v err=%v", violations, err)
		}
		if violations, err := corpus.ValidateEvent("v1", v2); err != nil ||
			(!it01HasFieldPath(violations, "digest_profile") && !it01HasFieldPath(violations, "publication")) {
			t.Fatalf("event/v1 did not reject a v2-only field: violations=%+v err=%v", violations, err)
		}
		if violations, err := corpus.ValidateEvent("v2", v1); err != nil || len(violations) == 0 {
			t.Fatalf("event/v2 accepted the historical v1 document as its own shape: violations=%+v err=%v", violations, err)
		}

		envelope := fold.Envelope{ID: "XC-axon-orders", Kind: fold.KindContract, From: "axon"}
		v1Fold := fold.Fold(fold.KindContract, envelope, []fold.Event{foldEventFromDocument(t, v1)}, membership)
		v2Fold := fold.Fold(fold.KindContract, envelope, []fold.Event{foldEventFromDocument(t, v2)}, membership)
		if !reflect.DeepEqual(v1Fold, v2Fold) {
			t.Fatalf("wire-version-only fields changed canonical fold\nv1: %+v\nv2: %+v", v1Fold, v2Fold)
		}
		if v1Fold.State != fold.StatePublished || v1Fold.Versions["1.0.0"] != fold.StatePublished {
			t.Fatalf("contract publish fold = %+v, want published 1.0.0", v1Fold)
		}
	})

	t.Run("receipt is required only for applicable post-floor first-party events", func(t *testing.T) {
		t.Parallel()

		envelope := fold.Envelope{
			ID: "XQ-axon-receipt", Kind: fold.KindQuestion, From: "axon", To: []string{"beta"},
		}
		prior := fold.Result{Kind: fold.KindQuestion, State: fold.StateSubmitted}
		applicableEvent := fold.Event{
			ULID: "01K2A2B3C4D5E6F7G8H9J0K1M7", Subject: envelope.ID,
			Transition: fold.TAcknowledge, Actor: fold.Actor{Kind: "agent", Name: "beta-worker", System: "beta"},
		}
		applicable := fold.EvaluateCandidate(fold.KindQuestion, prior, applicableEvent, envelope, membership)
		if applicable.Verdict != fold.VerdictLegal || !applicable.Applicable || applicable.Outcome == fold.StateNone {
			t.Fatalf("applicable evaluation precondition failed: %+v", applicable)
		}
		noteEvent := applicableEvent
		noteEvent.Transition = fold.TNote
		notApplicable := fold.EvaluateCandidate(fold.KindQuestion, prior, noteEvent, envelope, membership)
		if notApplicable.Verdict != fold.VerdictLegal || notApplicable.Applicable {
			t.Fatalf("note evaluation precondition failed: %+v", notApplicable)
		}

		matching := string(applicable.Outcome)
		cases := []struct {
			name        string
			transition  string
			tool        string
			version     string
			state       *string
			evaluation  fold.CandidateEvaluation
			wantValid   bool
			wantReceipt bool
		}{
			{
				name: "historical v1 pre-floor first-party remains readable", transition: fold.TAcknowledge,
				tool: "a2a", version: "0.18.9", evaluation: applicable, wantValid: true,
			},
			{
				name: "third-party applicable event does not inherit first-party receipt policy", transition: fold.TAcknowledge,
				tool: "partner-cli", version: "build-latest", evaluation: applicable, wantValid: true,
			},
			{
				name: "post-floor first-party scalar-N-A event omits receipt", transition: fold.TNote,
				tool: "a2a", version: "0.19.0", evaluation: notApplicable, wantValid: true,
			},
			{
				name: "post-floor first-party applicable event requires receipt", transition: fold.TAcknowledge,
				tool: "a2a", version: "0.19.0", evaluation: applicable, wantReceipt: true,
			},
			{
				name: "post-floor first-party matching receipt passes", transition: fold.TAcknowledge,
				tool: "a2a", version: "0.19.0", state: &matching, evaluation: applicable, wantValid: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				raw := receiptDocument(t, tc.transition, tc.tool, tc.version, tc.state)
				result, err := engine.ValidateEventWithEvaluation(raw, "0.19.0", tc.evaluation)
				if err != nil {
					t.Fatalf("ValidateEventWithEvaluation: %v", err)
				}
				if tc.wantReceipt {
					if result.Valid || !it01HasCode(result.Violations, "LFC-003") {
						t.Fatalf("missing applicable receipt result = %+v, want LFC-003 rejection", result)
					}
					return
				}
				if result.Valid != tc.wantValid || it01HasCode(result.Violations, "LFC-003") {
					t.Fatalf("rollout result = %+v, want valid=%t and no LFC-003", result, tc.wantValid)
				}
			})
		}
	})
}

func contractPublishDocument(schemaID string, v2Fields bool) map[string]any {
	doc := map[string]any{
		"schema":     schemaID,
		"event":      "01K1A2B3C4D5E6F7G8H9J0K1M7",
		"space":      "fixture-space",
		"subject":    "XC-axon-orders",
		"transition": fold.TPublish,
		"state":      string(fold.StatePublished),
		"actor": map[string]any{
			"kind": "agent", "name": "contract-publisher", "system": "axon",
			"model": "e2e-agent", "session": "session:it01-contract",
		},
		"at":          "2026-08-03T12:00:00Z",
		"version":     "1.0.0",
		"digest":      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"produced_by": map[string]any{"tool": "a2a", "version": "0.19.0"},
	}
	if v2Fields {
		doc["digest_profile"] = "contract-set-v2"
		doc["publication"] = map[string]any{
			"intent_key":              "op-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"candidate_intent_digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			"version_selector":        "explicit:1.0.0",
			"operation_key":           "op-v1-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		}
	}
	return doc
}

func foldEventFromDocument(t *testing.T, doc map[string]any) fold.Event {
	t.Helper()
	actor, ok := doc["actor"].(map[string]any)
	if !ok {
		t.Fatalf("document actor has type %T", doc["actor"])
	}
	return fold.Event{
		ULID: doc["event"].(string), CommitSeq: 1, Subject: doc["subject"].(string),
		Transition: doc["transition"].(string), ClaimedState: fold.State(doc["state"].(string)),
		Version: doc["version"].(string), Actor: fold.Actor{
			Kind: actor["kind"].(string), Name: actor["name"].(string), System: actor["system"].(string),
		},
	}
}

func receiptDocument(t *testing.T, transition, tool, version string, state *string) []byte {
	t.Helper()
	doc := map[string]any{
		"schema":     "event/v1",
		"event":      "01K2A2B3C4D5E6F7G8H9J0K1M7",
		"space":      "fixture-space",
		"subject":    "XQ-axon-receipt",
		"transition": transition,
		"actor": map[string]any{
			"kind": "agent", "name": "beta-worker", "system": "beta",
			"model": "e2e-agent", "session": "session:it01-receipt",
		},
		"at":          "2026-08-03T12:00:00Z",
		"produced_by": map[string]any{"tool": tool, "version": version},
	}
	if state != nil {
		doc["state"] = *state
	}
	return mustJSON(t, doc)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}

func it01HasCode(violations []validate.Violation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

func it01HasFieldPath(violations []schema.FieldViolation, path string) bool {
	for _, violation := range violations {
		if violation.Path == path {
			return true
		}
	}
	return false
}
