package provenance

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewEventEvidencePreservesSafeBoundedFields(t *testing.T) {
	t.Parallel()

	got := NewEventEvidence(
		"acknowledged",
		Actor{
			Kind: "agent", Name: "worker", System: "axon",
			Model: "gpt-5.6", Session: "session:worker-17",
		},
		Producer{Tool: "a2a", Version: "0.19.0"},
	)

	want := EventEvidence{
		Receipt: "acknowledged",
		Actor: Actor{
			Kind: "agent", Name: "worker", System: "axon",
			Model: "gpt-5.6", Session: "session:worker-17",
		},
		Producer: Producer{Tool: "a2a", Version: "0.19.0"},
	}
	if got != want {
		t.Fatalf("NewEventEvidence() = %+v, want %+v", got, want)
	}
}

func TestSafeSessionEvidenceDigestRedactsUnsafeValuesDeterministically(t *testing.T) {
	t.Parallel()

	const unsafe = "token:super-secret"
	const want = "sha256:360f0b0743ceb50fd947fd94c0137516a7ddc7b52bbc5e9a4e6e855174e63a6a"

	first := SafeSessionEvidence(unsafe)
	second := SafeSessionEvidence(unsafe)
	if first != want || second != want {
		t.Fatalf("SafeSessionEvidence(%q) = %q, %q; want stable %q", unsafe, first, second, want)
	}
	if first == unsafe {
		t.Fatal("unsafe raw session survived projection")
	}
}

func TestSafeSessionEvidenceRejectsCredentialShapesInsideSafeAlphabet(t *testing.T) {
	t.Parallel()

	tests := []string{
		"password:not-a-session",
		"ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		"eyJheader.eyJpayload.signature",
		"sk_live_TEST_FIXTURE",
	}
	for _, session := range tests {
		t.Run(session, func(t *testing.T) {
			t.Parallel()
			if got := SafeSessionEvidence(session); got == session || len(got) != len("sha256:")+64 {
				t.Fatalf("SafeSessionEvidence(%q) = %q, want full digest reference", session, got)
			}
		})
	}
}

func TestSafeSessionEvidenceBoundsAndOmission(t *testing.T) {
	t.Parallel()

	if got := SafeSessionEvidence(""); got != "" {
		t.Fatalf("empty optional session = %q, want omitted", got)
	}
	if got := SafeSessionEvidence("session with spaces"); got == "session with spaces" {
		t.Fatal("session containing whitespace survived projection")
	}
	if got := SafeSessionEvidence("session:worker-17"); got != "session:worker-17" {
		t.Fatalf("safe session = %q, want exact preservation", got)
	}
	if tooLong := strings.Repeat("a", 129); SafeSessionEvidence(tooLong) == tooLong {
		t.Fatal("session longer than the frozen 128-character bound survived projection")
	}
}

func TestEventEvidenceJSONOmitsAbsentOptionalFields(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(NewEventEvidence("", Actor{Kind: "agent", Name: "worker", System: "axon"}, Producer{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)
	for _, absent := range []string{"state", "model", "session", "produced_by"} {
		if strings.Contains(got, `"`+absent+`"`) {
			t.Fatalf("zero optional field %q was emitted in %s", absent, got)
		}
	}
}
