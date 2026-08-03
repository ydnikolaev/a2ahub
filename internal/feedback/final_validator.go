package feedback

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// FinalSubmitValidator revalidates the exact feedback bytes frozen by the
// shared funnel. Feedback is not an envelope/event pair, so it cannot use the
// envelope submit validator; this adapter keeps final-byte validation explicit
// instead of wiring a nil dependency.
type FinalSubmitValidator struct{}

// ValidateSubmit validates the exact feedback bytes frozen by the shared funnel.
func (FinalSubmitValidator) ValidateSubmit(_ context.Context, files []space.FileWrite) error {
	if len(files) != 1 {
		return fmt.Errorf("feedback: final submission must contain exactly one report")
	}
	file := files[0]
	clean := path.Clean(file.Path)
	if clean != file.Path || !strings.HasPrefix(clean, "feedback/inbox/") || path.Ext(clean) != ".yaml" {
		return fmt.Errorf("feedback: final submission path %q is not an inbox report", file.Path)
	}
	report := Validate(file.Content, Options{CI: true, Path: file.Path})
	if !report.Valid {
		return &ValidationRefusedError{Violations: report.Violations}
	}
	return nil
}

var _ space.SubmitValidator = FinalSubmitValidator{}
