package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	commandFailed := &space.Error{Op: "ResolveCredential", Input: "configured cmd reference op read", Err: fmt.Errorf("%w: exit status 1", space.ErrCredentialCommandFailed)}
	brokenCommandThenNothing := errors.Join(commandFailed, otherUnresolved)

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
		{name: "a configured credential command that fails refuses, even when every fallback is merely missing", github: true, credErr: brokenCommandThenNothing, wantFatal: space.ErrCredentialCommandFailed},
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

// feedbackInboxTestDraft is a valid feedback report with no draft comments, so
// its normalized bytes are its raw bytes.
const feedbackInboxTestDraft = `feedback: v1
id: fb-20261028-c3c3c3
kind: bug
severity: major
title: "a broken credential command must refuse"
summary: "a grounded report used to drive the submit route selection"
context:
  a2a_version: v0.13.0
  os_arch: darwin/arm64
  surface: cli
evidence:
  steps:
    - "run the affected command"
  expected: "the documented result"
  actual: "the observed conflicting result"
checks:
  docs_consulted: true
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`

// TestFeedbackSubmitRefusesAFailingCredentialCommand is spec 08 US-4 / §T1.1
// row 1 through the real wiring: `credentials.feedback: cmd:<helper>` whose
// helper exits non-zero, or exits 0 and prints nothing, is a broken setting.
// Submit exits 1 naming the failure, files nothing to the inbox and records no
// ledger row; it never says "no GitHub credential". The control row (nothing
// configured) proves the same machine does reach the inbox route, so the
// refusals are about the setting and not about the harness.
//
// reason: not parallel: it sets HOME, PATH and the token variables and changes
// the working directory, all process-wide.
func TestFeedbackSubmitRefusesAFailingCredentialCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		helper     string // shell body; "" means no credentials.feedback at all
		wantCode   int
		wantStderr string
	}{
		{name: "a helper that exits non-zero", helper: "echo leaked-stdout-secret\necho leaked-stderr-secret >&2\nexit 3\n", wantCode: 1, wantStderr: "exit status 3"},
		{name: "a helper that prints nothing", helper: "exit 0\n", wantCode: 1, wantStderr: "printed no credential"},
		{name: "nothing configured still files to the inbox", helper: "", wantCode: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			project := t.TempDir()
			t.Setenv("HOME", home)
			for _, name := range []string{"A2A_FEEDBACK_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "A2A_FEEDBACK_REPO", "A2A_GITHUB_API"} {
				t.Setenv(name, "")
			}
			withGHLogin(t, "", false)
			t.Chdir(project)

			config := "credentials: {}\n"
			if tc.helper != "" {
				helper := filepath.Join(t.TempDir(), "credential-helper")
				if err := os.WriteFile(helper, []byte("#!/bin/sh\n"+tc.helper), 0o755); err != nil {
					t.Fatal(err)
				}
				config = "credentials:\n  feedback: \"cmd:" + helper + "\"\n"
			}
			writeFeedbackInboxTestFile(t, filepath.Join(home, ".config", "a2a", "config.yaml"), config)
			feedbackDir := filepath.Join(project, ".a2a", "feedback")
			draft := filepath.Join(feedbackDir, "fb-20261028-c3c3c3.yaml")
			writeFeedbackInboxTestFile(t, draft, feedbackInboxTestDraft)

			var stdout, stderr bytes.Buffer
			code := runFeedback([]string{"submit", draft}, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			filed := filepath.Join(home, ".local", "share", "a2a", "feedback", "inbox", "fb-20261028-c3c3c3.yaml")
			if tc.wantCode == 0 {
				if !strings.Contains(stdout.String(), "(no GitHub credential)") {
					t.Fatalf("stdout = %q, want an inbox filing for no GitHub credential", stdout.String())
				}
				if _, err := os.Stat(filed); err != nil {
					t.Fatalf("the control row filed nothing: %v", err)
				}
				return
			}
			if !strings.Contains(stderr.String(), "a2a feedback: credential:") || !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("stderr = %q, want the credential refusal naming %q", stderr.String(), tc.wantStderr)
			}
			if strings.Contains(stdout.String()+stderr.String(), "no GitHub credential") {
				t.Fatalf("a broken setting was reported as no credential\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "leaked-") {
				t.Fatalf("the refusal echoes the helper's output\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filed); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a broken setting filed to the inbox (stat err = %v)", err)
			}
			if _, err := os.Stat(filepath.Join(feedbackDir, "ledger.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a broken setting wrote a ledger (stat err = %v)", err)
			}
		})
	}
}

func writeFeedbackInboxTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
