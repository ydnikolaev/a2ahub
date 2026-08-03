package feedback

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestFinalSubmitValidatorRevalidatesExactFeedbackBytes(t *testing.T) {
	t.Parallel()
	valid := []byte(validFeedbackYAML)
	validator := FinalSubmitValidator{}
	if err := validator.ValidateSubmit(context.Background(), []space.FileWrite{{
		Path: "feedback/inbox/XF-01K1A2B3C4D5E6F7G8H9J0K1M2.yaml", Content: valid,
	}}); err != nil {
		t.Fatalf("valid final feedback: %v", err)
	}
	invalid := append([]byte(nil), valid...)
	invalid = append(invalid, []byte("\nsecret: token\n")...)
	if err := validator.ValidateSubmit(context.Background(), []space.FileWrite{{
		Path: "feedback/inbox/XF-01K1A2B3C4D5E6F7G8H9J0K1M2.yaml", Content: invalid,
	}}); err == nil {
		t.Fatal("invalid final feedback passed")
	} else {
		var refused *ValidationRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("invalid final feedback error = %T %v", err, err)
		}
	}
}

func TestFinalSubmitValidatorRejectsNonFeedbackShape(t *testing.T) {
	t.Parallel()
	validator := FinalSubmitValidator{}
	for _, files := range [][]space.FileWrite{
		nil,
		{{Path: "feedback/inbox/a.yaml", Content: []byte("x")}, {Path: "feedback/inbox/b.yaml", Content: []byte("x")}},
		{{Path: "other/inbox/a.yaml", Content: []byte("x")}},
		{{Path: "feedback/inbox/../a.yaml", Content: []byte("x")}},
	} {
		if err := validator.ValidateSubmit(context.Background(), files); err == nil {
			t.Fatalf("files %+v passed", files)
		}
	}
}
