package validate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

const receiptTestEventULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type receiptEventOptions struct {
	transition string
	state      *string
	tool       string
	version    string
	model      *string
	session    *string
}

func TestValidateEventProducerStampUsesReservedCode(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	raw := receiptEventYAML(receiptEventOptions{transition: fold.TAcknowledge})

	result, err := engine.ValidateEvent(raw, "0.10.0")
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if result.Valid || !hasCode(result.Violations, "POL-012") {
		t.Fatalf("missing producer stamp at the active floor = %+v, want POL-012 rejection", result)
	}
	if hasCode(result.Violations, "POL-005") {
		t.Fatalf("producer stamp reused occupied POL-005: %+v", result.Violations)
	}

	legacy, err := engine.ValidateEvent(raw, "0.9.9")
	if err != nil {
		t.Fatalf("ValidateEvent legacy floor: %v", err)
	}
	if !legacy.Valid {
		t.Fatalf("pre-stamp-floor event became invalid: %+v", legacy.Violations)
	}
}

func TestValidateEventReceiptRolloutMatrix(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	applicable := questionEvaluation(fold.StateNone, fold.TAcknowledge)
	notApplicable := questionEvaluation(fold.StateNone, fold.TNote)

	tests := []struct {
		name       string
		opts       receiptEventOptions
		evaluation fold.CandidateEvaluation
		wantCode   string
	}{
		{
			name:       "pre_floor_first_party_omission_remains_readable",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "a2a", version: "0.18.9"},
			evaluation: applicable,
		},
		{
			name:       "floor_first_party_requires_applicable_receipt",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "a2a", version: "0.19.0"},
			evaluation: applicable,
			wantCode:   "LFC-003",
		},
		{
			name:       "future_first_party_requires_applicable_receipt",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "a2a", version: "1.3.0"},
			evaluation: applicable,
			wantCode:   "LFC-003",
		},
		{
			name:       "third_party_version_format_does_not_enter_rule",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "partner-cli", version: "build-latest"},
			evaluation: applicable,
		},
		{
			name:       "unknown_producer_omission_remains_readable",
			opts:       receiptEventOptions{transition: fold.TAcknowledge},
			evaluation: applicable,
		},
		{
			name:       "malformed_first_party_version_fails_closed",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "a2a", version: "not-semver"},
			evaluation: applicable,
			wantCode:   "POL-012",
		},
		{
			name:       "empty_first_party_version_fails_closed_without_space_floor",
			opts:       receiptEventOptions{transition: fold.TAcknowledge, tool: "a2a"},
			evaluation: applicable,
			wantCode:   "POL-012",
		},
		{
			name:       "floor_first_party_NA_event_omits_receipt",
			opts:       receiptEventOptions{transition: fold.TNote, tool: "a2a", version: "0.19.0"},
			evaluation: notApplicable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := engine.ValidateEventWithEvaluation(receiptEventYAML(tc.opts), "", tc.evaluation)
			if err != nil {
				t.Fatalf("ValidateEventWithEvaluation: %v", err)
			}
			if tc.wantCode == "" {
				if !result.Valid {
					t.Fatalf("unexpected rejection: %+v", result.Violations)
				}
				return
			}
			if result.Valid || !hasCode(result.Violations, tc.wantCode) {
				t.Fatalf("result = %+v, want %s rejection", result, tc.wantCode)
			}
		})
	}
}

func TestValidateEventReceiptMismatchIsNonBlockingEvidence(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	claimed := "closed"
	model := "gpt-5.6"
	session := "session:worker-17"
	evaluation := questionEvaluation(fold.State(claimed), fold.TAcknowledge)
	raw := receiptEventYAML(receiptEventOptions{
		transition: fold.TAcknowledge,
		state:      &claimed,
		tool:       "a2a", version: "0.19.0",
		model: &model, session: &session,
	})

	result, err := engine.ValidateEventWithEvaluation(raw, "", evaluation)
	if err != nil {
		t.Fatalf("ValidateEventWithEvaluation: %v", err)
	}
	if !result.Valid || len(result.Violations) != 0 {
		t.Fatalf("wrong receipt became blocking: %+v", result)
	}
	if evaluation.Result.State != fold.StateAcknowledged {
		t.Fatalf("producer claim changed authoritative fold state to %q", evaluation.Result.State)
	}
	if len(result.ConsistencyFlags) != 1 {
		t.Fatalf("consistency flags = %+v, want exactly one", result.ConsistencyFlags)
	}
	flag := result.ConsistencyFlags[0]
	if flag.Kind != ConsistencyStateClaimMismatch || flag.EventULID != receiptTestEventULID ||
		flag.Subject != "XQ-axon-receipt" || flag.Scope.Kind != "primary" ||
		flag.Scope.Subject != "XQ-axon-receipt" || flag.Claimed != claimed ||
		flag.Actual != string(fold.StateAcknowledged) || flag.Cause != "unknown" {
		t.Fatalf("incomplete mismatch evidence: %+v", flag)
	}
	if flag.Actor.Kind != "agent" || flag.Actor.Name != "validator-test" ||
		flag.Actor.System != "seomatrix" || flag.Actor.Model != model || flag.Actor.Session != session {
		t.Fatalf("actor evidence = %+v", flag.Actor)
	}
	if flag.Producer.Tool != "a2a" || flag.Producer.Version != "0.19.0" {
		t.Fatalf("producer evidence = %+v", flag.Producer)
	}

	matching := string(fold.StateAcknowledged)
	matchingEvaluation := questionEvaluation(fold.State(matching), fold.TAcknowledge)
	matchingResult, err := engine.ValidateEventWithEvaluation(receiptEventYAML(receiptEventOptions{
		transition: fold.TAcknowledge, state: &matching, tool: "a2a", version: "0.19.0",
	}), "", matchingEvaluation)
	if err != nil {
		t.Fatalf("ValidateEventWithEvaluation matching: %v", err)
	}
	if len(matchingResult.ConsistencyFlags) != 0 {
		t.Fatalf("matching receipt produced timeline noise: %+v", matchingResult.ConsistencyFlags)
	}
}

func TestValidateEventMismatchRedactsUnsafeLegacySession(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	claimed := "closed"
	unsafeSession := "legacy session with spaces"
	result, err := engine.ValidateEventWithEvaluation(receiptEventYAML(receiptEventOptions{
		transition: fold.TAcknowledge, state: &claimed,
		tool: "partner-cli", version: "release/old", session: &unsafeSession,
	}), "", questionEvaluation(fold.State(claimed), fold.TAcknowledge))
	if err != nil {
		t.Fatalf("ValidateEventWithEvaluation: %v", err)
	}
	if !result.Valid || len(result.ConsistencyFlags) != 1 {
		t.Fatalf("legacy mismatch result = %+v", result)
	}
	got := result.ConsistencyFlags[0].Actor.Session
	if got == unsafeSession || !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("unsafe legacy session evidence = %q, want deterministic sha256 reference", got)
	}

	again, err := engine.ValidateEventWithEvaluation(receiptEventYAML(receiptEventOptions{
		transition: fold.TAcknowledge, state: &claimed,
		tool: "partner-cli", version: "release/old", session: &unsafeSession,
	}), "", questionEvaluation(fold.State(claimed), fold.TAcknowledge))
	if err != nil {
		t.Fatalf("ValidateEventWithEvaluation repeat: %v", err)
	}
	if again.ConsistencyFlags[0].Actor.Session != got {
		t.Fatalf("legacy session digest is not deterministic: %q vs %q", got, again.ConsistencyFlags[0].Actor.Session)
	}
}

func TestCheckEventReceiptPreservesContractVersionScope(t *testing.T) {
	t.Parallel()

	env := fold.Envelope{ID: "XC-axon-receipt", Kind: fold.KindContract, From: "axon"}
	claimed := "retired"
	evaluation := fold.EvaluateCandidate(
		fold.KindContract,
		fold.Result{Kind: fold.KindContract, State: fold.StatePublished, Versions: map[string]fold.State{"1.0.0": fold.StatePublished}},
		fold.Event{
			ULID: receiptTestEventULID, Subject: env.ID, Transition: fold.TDeprecate, Version: "1.0.0",
			ClaimedState: fold.State(claimed), Actor: fold.Actor{System: "axon"},
		},
		env,
		func(string) fold.MembershipStatus { return fold.MembershipMember },
	)
	probe := eventProducerProbe{Event: receiptTestEventULID, Subject: env.ID, State: &claimed}

	violations, flags := checkEventReceipt(probe, false, evaluation)
	if len(violations) != 0 || len(flags) != 1 {
		t.Fatalf("receipt result: violations=%+v flags=%+v", violations, flags)
	}
	if flags[0].Scope.Kind != "contract-version" || flags[0].Scope.Subject != env.ID ||
		flags[0].Scope.Version != "1.0.0" || flags[0].Actual != "deprecated" {
		t.Fatalf("contract-version mismatch scope was lost: %+v", flags[0])
	}
}

func TestValidateEventFirstPartyActorBounds(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	oneModel := "🛠"
	maxModel := strings.Repeat("界", 96)
	empty := ""
	overModel := strings.Repeat("界", 97)
	oneSession := "a"
	maxSession := strings.Repeat("a", 128)
	overSession := strings.Repeat("a", 129)
	spaceSession := "session with space"
	unicodeSession := "сессия"
	controlSession := "run\x01id"

	tests := []struct {
		name     string
		model    *string
		session  *string
		wantCode string
	}{
		{name: "optional_fields_absent"},
		{name: "one_scalar_model", model: &oneModel},
		{name: "ninety_six_scalar_model", model: &maxModel},
		{name: "empty_model", model: &empty, wantCode: "POL-016"},
		{name: "ninety_seven_scalar_model", model: &overModel, wantCode: "POL-016"},
		{name: "one_character_session", session: &oneSession},
		{name: "one_hundred_twenty_eight_character_session", session: &maxSession},
		{name: "empty_session", session: &empty, wantCode: "POL-016"},
		{name: "one_hundred_twenty_nine_character_session", session: &overSession, wantCode: "POL-016"},
		{name: "whitespace_session", session: &spaceSession, wantCode: "POL-016"},
		{name: "non_ascii_session", session: &unicodeSession, wantCode: "POL-016"},
		{name: "control_character_session", session: &controlSession, wantCode: "POL-016"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := string(fold.StateAcknowledged)
			result, err := engine.ValidateEvent(receiptEventYAML(receiptEventOptions{
				transition: fold.TAcknowledge, state: &state,
				tool: "a2a", version: "0.19.0", model: tc.model, session: tc.session,
			}), "")
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if tc.wantCode == "" {
				if !result.Valid {
					t.Fatalf("unexpected actor rejection: %+v", result.Violations)
				}
				return
			}
			if result.Valid || !hasCode(result.Violations, tc.wantCode) {
				t.Fatalf("result = %+v, want %s", result, tc.wantCode)
			}
		})
	}

	t.Run("legacy_and_third_party_actor_values_remain_readable", func(t *testing.T) {
		t.Parallel()
		for _, producer := range []struct{ tool, version string }{{"a2a", "0.18.9"}, {"partner", "anything"}} {
			result, err := engine.ValidateEvent(receiptEventYAML(receiptEventOptions{
				transition: fold.TAcknowledge, tool: producer.tool, version: producer.version,
				model: &empty, session: &spaceSession,
			}), "")
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if !result.Valid || hasCode(result.Violations, "POL-016") {
				t.Fatalf("producer %s/%s lost legacy readability: %+v", producer.tool, producer.version, result)
			}
		}
	})
}

func TestValidateEventCredentialShapedSessionsUsePOL001(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	prefixes := []string{"token:", "PASSWORD:", "Bearer:", "ghp_", "github_pat_", "glpat-", "sk-", "xoxb-", "xoxp-"}
	for _, prefix := range prefixes {
		t.Run(strings.ReplaceAll(prefix, ":", "_"), func(t *testing.T) {
			t.Parallel()
			session := prefix + "fixture"
			result, err := engine.ValidateEvent(receiptEventYAML(receiptEventOptions{
				transition: fold.TAcknowledge, tool: "a2a", version: "0.19.0", session: &session,
			}), "")
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if result.Valid || !hasCode(result.Violations, "POL-001") {
				t.Fatalf("credential prefix %q = %+v, want POL-001", prefix, result)
			}
			if hasCode(result.Violations, "POL-016") {
				t.Fatalf("credential prefix %q was misclassified as POL-016: %+v", prefix, result.Violations)
			}
		})
	}

	t.Run("non_prefix_shape_from_the_canonical_scanner", func(t *testing.T) {
		t.Parallel()
		session := "run-AKIAIOSFODNN7EXAMPLE"
		result, err := engine.ValidateEvent(receiptEventYAML(receiptEventOptions{
			transition: fold.TAcknowledge, tool: "a2a", version: "0.19.0", session: &session,
		}), "")
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if result.Valid || !hasCode(result.Violations, "POL-001") {
			t.Fatalf("canonical non-prefix secret shape = %+v, want POL-001", result)
		}
		if hasCode(result.Violations, "POL-016") {
			t.Fatalf("canonical secret shape was misclassified as POL-016: %+v", result.Violations)
		}
	})
}

func receiptEventYAML(opts receiptEventOptions) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: event/v1\nevent: %s\nspace: getvisa\nsubject: XQ-axon-receipt\ntransition: %s\n", receiptTestEventULID, opts.transition)
	if opts.state != nil {
		fmt.Fprintf(&b, "state: %q\n", *opts.state)
	}
	b.WriteString("actor:\n  kind: agent\n  name: validator-test\n  system: seomatrix\n")
	if opts.model != nil {
		fmt.Fprintf(&b, "  model: %q\n", *opts.model)
	}
	if opts.session != nil {
		fmt.Fprintf(&b, "  session: %q\n", *opts.session)
	}
	b.WriteString("at: 2026-08-03T10:00:00Z\n")
	if opts.tool != "" || opts.version != "" {
		fmt.Fprintf(&b, "produced_by:\n  tool: %q\n  version: %q\n", opts.tool, opts.version)
	}
	return []byte(b.String())
}

func questionEvaluation(claimed fold.State, transition string) fold.CandidateEvaluation {
	env := fold.Envelope{ID: "XQ-axon-receipt", Kind: fold.KindQuestion, From: "axon", To: []string{"seomatrix"}}
	prior := fold.Result{Kind: fold.KindQuestion, State: fold.StateSubmitted}
	if transition == fold.TNote {
		prior.State = fold.StateClosed
	}
	return fold.EvaluateCandidate(
		fold.KindQuestion,
		prior,
		fold.Event{
			ULID: receiptTestEventULID, Subject: env.ID, Transition: transition,
			ClaimedState: claimed, Actor: fold.Actor{Kind: "agent", Name: "validator-test", System: "seomatrix"},
		},
		env,
		func(string) fold.MembershipStatus { return fold.MembershipMember },
	)
}
