package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestFeedbackInboxFallback is spec 08 AC4: the route table of §T1.1 rows 1, 3
// and 4 as a table over the one pure helper that owns the detection.
func TestFeedbackInboxFallback(t *testing.T) {
	t.Parallel()

	unresolved := &space.Error{Op: "ResolveCredential", Input: "env var A2A_FEEDBACK_TOKEN", Err: space.ErrCredentialUnresolved}
	otherUnresolved := &space.Error{Op: "ResolveCredential", Input: "env var GITHUB_TOKEN", Err: space.ErrCredentialUnresolved}
	brokenSetting := fmt.Errorf("parse credentials.feedback: %w", space.ErrInvalidCredentialReference)
	disk := errors.New("read machine config: permission denied")
	mixed := errors.Join(disk, unresolved)

	for _, tc := range []struct {
		name       string
		github     bool
		credErr    error
		wantReason string
		wantFatal  error
	}{
		{name: "a credential resolved on a GitHub target is the hub route", github: true, credErr: nil},
		{name: "no error on a non-GitHub target is the hub route", github: false, credErr: nil},
		{name: "a non-GitHub target resolves nothing and stays on the hub route", github: false, credErr: unresolved},
		{name: "nothing configured or found on a GitHub target files to the inbox", github: true, credErr: unresolved, wantReason: "no GitHub credential"},
		{name: "the joined refusal of every source tried is still no credential", github: true, credErr: errors.Join(unresolved, otherUnresolved), wantReason: "no GitHub credential"},
		{name: "a joined error carrying the sentinel in a wrap is still no credential", github: true, credErr: fmt.Errorf("feedback: %w", errors.Join(unresolved, otherUnresolved)), wantReason: "no GitHub credential"},
		{name: "a malformed credentials.feedback refuses", github: true, credErr: brokenSetting, wantFatal: brokenSetting},
		{name: "any other failure refuses", github: true, credErr: disk, wantFatal: disk},
		{name: "one unresolved leaf does not excuse a real failure beside it", github: true, credErr: mixed, wantFatal: mixed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason, fatal := feedbackInboxFallback(tc.github, tc.credErr)
			if reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
			}
			if !errors.Is(fatal, tc.wantFatal) {
				t.Fatalf("fatal = %v, want %v", fatal, tc.wantFatal)
			}
		})
	}
}
