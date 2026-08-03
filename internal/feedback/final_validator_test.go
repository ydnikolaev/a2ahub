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
		Path: "feedback/inbox/fb-20260723-abc123.yaml", Content: valid,
	}}); err != nil {
		t.Fatalf("valid final feedback: %v", err)
	}
	invalid := append([]byte(nil), valid...)
	invalid = append(invalid, []byte("\nsecret: token\n")...)
	if err := validator.ValidateSubmit(context.Background(), []space.FileWrite{{
		Path: "feedback/inbox/fb-20260723-abc123.yaml", Content: invalid,
	}}); err == nil {
		t.Fatal("invalid final feedback passed")
	} else {
		var refused *ValidationRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("invalid final feedback error = %T %v", err, err)
		}
	}
}

func TestFinalSubmitValidatorBindsContentIDToFilename(t *testing.T) {
	t.Parallel()
	validator := FinalSubmitValidator{}
	err := validator.ValidateSubmit(context.Background(), []space.FileWrite{{
		Path: "feedback/inbox/fb-20260723-different.yaml", Content: []byte(validFeedbackYAML),
	}})
	if err == nil {
		t.Fatal("feedback whose content id differs from its filename passed final validation")
	}
	var refused *ValidationRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("filename mismatch error = %T %v, want ValidationRefusedError", err, err)
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
