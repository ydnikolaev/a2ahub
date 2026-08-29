package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// usageworkflow_test.go proves AC-1 directly (answers-that-hold-2026-08
// spec 08, "How to verify: run each such verb bare and assert the line"):
// every verb in today's derived universe — the binary's catalogue
// intersected with docs-manifest.json's section ids, {feedback, notify,
// notifications} — prints workflowLine's own topic line on a bare
// invocation. scripts/check-usage-workflow.sh's AST walk (§ that gate's own
// header) proves the SOURCE names the topic; this proves the RUNTIME
// BEHAVIOR the source produces actually reaches stdio.

func TestUsageWorkflowLine_Feedback_Bare(t *testing.T) {
	c := NewFeedbackCommand(nil, nil, "", "", nil)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("feedback"); !strings.Contains(errOut.String(), want) {
		t.Fatalf("a bare `a2a feedback` did not print %q on stderr; got: %s", want, errOut.String())
	}
}

func TestUsageWorkflowLine_Feedback_UnknownSubcommand(t *testing.T) {
	c := NewFeedbackCommand(nil, nil, "", "", nil)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"not-a-real-subcommand"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("feedback"); !strings.Contains(errOut.String(), want) {
		t.Fatalf("a malformed `a2a feedback <bogus>` did not print %q on stderr; got: %s", want, errOut.String())
	}
}

func TestUsageWorkflowLine_Notify_Bare(t *testing.T) {
	c := NewNotifyCommand()
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (bare notify prints usage and succeeds); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("notify"); !strings.Contains(out.String(), want) {
		t.Fatalf("a bare `a2a notify` did not print %q on stdout; got: %s", want, out.String())
	}
}

func TestUsageWorkflowLine_Notify_UnknownSubcommand(t *testing.T) {
	c := NewNotifyCommand()
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"not-a-real-subcommand"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("notify"); !strings.Contains(errOut.String(), want) {
		t.Fatalf("a malformed `a2a notify <bogus>` did not print %q on stderr; got: %s", want, errOut.String())
	}
}

func TestUsageWorkflowLine_Notifications_Bare(t *testing.T) {
	c := NewNotificationsCommand(nil)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("notifications"); !strings.Contains(errOut.String(), want) {
		t.Fatalf("a bare `a2a notifications` did not print %q on stderr; got: %s", want, errOut.String())
	}
}

// stubContractPublicationOps is a no-op ContractPublicationOperations —
// enough to make ContractCommand.runPublish's own `c.publication == nil`
// guard pass, so a malformed-args test reaches parseContractPublicationArgs'
// usage/workflow print instead of the "service unavailable" refusal.
// Neither method is ever invoked by the tests below: a malformed invocation
// (no --version/--bump) is refused before either would be called.
type stubContractPublicationOps struct{}

func (stubContractPublicationOps) Preflight(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return space.ContractPublicationResult{}, nil
}

func (stubContractPublicationOps) Publish(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return space.ContractPublicationResult{}, nil
}

// TestUsageWorkflowLine_MechanismTwo proves spec 08's mechanism 2
// (usageworkflow_dump_test.go's own doc comment) actually PRINTS, not just
// DECLARES: scripts/check-usage-workflow.sh's AST walk only proves a
// map[string]string literal like lifecycleWorkflowLines/
// contractPublicationWorkflowLines/htmlWorkflowLines exists and is keyed
// correctly — it cannot see whether the runtime lookup that reads it back
// is still wired into Run. Deleting that lookup leaves the gate green while
// the verb prints nothing, which is exactly the defect class spec 08 exists
// to catch (AC-1: "run each such verb bare and assert the line").
func TestUsageWorkflowLine_MechanismTwo(t *testing.T) {
	t.Run("ack (lifecycleWorkflowLines)", func(t *testing.T) {
		c := NewAckCommand(nil, "", "", "", space.Manifest{}, SubmitHostConfig{}, nil)
		var out, errOut bytes.Buffer
		code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
		}
		if want := workflowLine("loop-receive"); !strings.Contains(errOut.String(), want) {
			t.Fatalf("a bare `a2a ack` did not print %q on stderr (lifecycleWorkflowLines is declared but not looked up?); got: %s", want, errOut.String())
		}
	})

	t.Run("contract publish (contractPublicationWorkflowLines)", func(t *testing.T) {
		c := NewContractCommand(nil, nil, "", "", "", space.Manifest{}, SubmitHostConfig{}, nil)
		c.SetP6Operations(stubContractPublicationOps{}, nil, nil)
		var out, errOut bytes.Buffer
		code := c.Run(context.Background(), []string{"publish"}, IO{Stdout: &out, Stderr: &errOut})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
		}
		if want := workflowLine("loop-contract-change"); !strings.Contains(errOut.String(), want) {
			t.Fatalf("a malformed `a2a contract publish` did not print %q on stderr (contractPublicationWorkflowLines is declared but not looked up?); got: %s", want, errOut.String())
		}
	})

	t.Run("html (htmlWorkflowLines)", func(t *testing.T) {
		c := NewHtmlCommand(nil)
		var out, errOut bytes.Buffer
		code := c.Run(context.Background(), []string{"unexpected-positional"}, IO{Stdout: &out, Stderr: &errOut})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
		}
		if want := workflowLine("loop-watch"); !strings.Contains(errOut.String(), want) {
			t.Fatalf("a malformed `a2a html` did not print %q on stderr (htmlWorkflowLines is declared but not looked up?); got: %s", want, errOut.String())
		}
	})

	// Negative control (AC-3): approve shares LifecycleCommand.Run with ack
	// but carries NO docs-manifest.json topic set at all, so
	// lifecycleWorkflowLines deliberately omits it — a bare `a2a approve`
	// must print no "workflow:" line, proving the map MISS is a real miss
	// and not a fallback to some other topic.
	t.Run("approve (negative control — no line)", func(t *testing.T) {
		c := NewApproveCommand(nil, "", "", "", space.Manifest{}, SubmitHostConfig{}, nil)
		var out, errOut bytes.Buffer
		code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
		}
		if strings.Contains(errOut.String(), "workflow:") {
			t.Fatalf("a bare `a2a approve` printed a workflow: line, but approve carries no docs-manifest.json topic set (AC-3); got: %s", errOut.String())
		}
	})
}

func TestUsageWorkflowLine_Notifications_UnknownSubcommand(t *testing.T) {
	c := NewNotificationsCommand(nil)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"not-a-real-subcommand"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
	if want := workflowLine("notifications"); !strings.Contains(errOut.String(), want) {
		t.Fatalf("a malformed `a2a notifications <bogus>` did not print %q on stderr; got: %s", want, errOut.String())
	}
}
