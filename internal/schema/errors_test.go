package schema

import (
	"errors"
	"testing"
)

func TestError_Format(t *testing.T) {
	t.Parallel()

	withInput := &Error{Op: "Load", Input: "envelope/v2", Err: ErrUnsupportedVersion}
	want := "schema: Load: envelope/v2: " + ErrUnsupportedVersion.Error()
	if got := withInput.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(withInput, ErrUnsupportedVersion) {
		t.Error("expected errors.Is to unwrap to ErrUnsupportedVersion")
	}

	withoutInput := &Error{Op: "Load", Err: ErrCorpusLoad}
	want = "schema: Load: " + ErrCorpusLoad.Error()
	if got := withoutInput.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
