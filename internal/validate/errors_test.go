package validate

import (
	"errors"
	"testing"
)

func TestError_Format(t *testing.T) {
	t.Parallel()

	withInput := &Error{Op: "ValidateDraft", Input: "work_request", Err: ErrUnknownEnvelopeType}
	want := "validate: ValidateDraft: work_request: " + ErrUnknownEnvelopeType.Error()
	if got := withInput.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(withInput, ErrUnknownEnvelopeType) {
		t.Error("expected errors.Is to unwrap to ErrUnknownEnvelopeType")
	}

	withoutInput := &Error{Op: "ValidateForSubmit", Err: ErrOversizedArtifact}
	want = "validate: ValidateForSubmit: " + ErrOversizedArtifact.Error()
	if got := withoutInput.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
