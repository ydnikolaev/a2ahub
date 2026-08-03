package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func lifecycleReceiptEvent(t *testing.T, files []space.FileWrite, transition string) string {
	t.Helper()
	needle := "transition: " + transition
	for _, file := range files {
		content := string(file.Content)
		if strings.Contains(file.Path, "/events/") && strings.Contains(content, needle) {
			return content
		}
	}
	t.Fatalf("no %s event found in %+v", transition, files)
	return ""
}

func TestLifecycleReceiptGenericApplicable(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-ra01"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
		t.Fatalf("ack: code = %d; stderr=%s", code, errOut.String())
	}
	event := lifecycleReceiptEvent(t, fake.calls[0].Files, "acknowledge")
	if !strings.Contains(event, "state: acknowledged") {
		t.Fatalf("applicable acknowledgement omitted its evaluator receipt:\n%s", event)
	}
}

func TestLifecycleReceiptBroadcastAckOmitted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XA-axon-20260721-ra02"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", "---\n"+
		"schema: envelope/v1\n"+
		"id: "+id+"\n"+
		"type: announcement\n"+
		"space: fixture-space\n"+
		"from: axon\n"+
		"to: [beta]\n"+
		"actor: {kind: agent, name: bot}\n"+
		"---\nbody\n")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
		t.Fatalf("ack announcement: code = %d; stderr=%s", code, errOut.String())
	}
	event := lifecycleReceiptEvent(t, fake.calls[0].Files, "acknowledge")
	if strings.Contains(event, "state:") {
		t.Fatalf("broadcast acknowledgement is receipt-N/A and must omit state:\n%s", event)
	}
}

func TestLifecycleReceiptRespondOmitted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-ra03"
	seedAcceptedQuestion(t, mirrorDir, id, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--result", "answered", id}, io); code != 0 {
		t.Fatalf("respond: code = %d; stderr=%s", code, errOut.String())
	}
	event := lifecycleReceiptEvent(t, fake.calls[0].Files, "respond")
	if strings.Contains(event, "state:") {
		t.Fatalf("respond is receipt-N/A and must omit state:\n%s", event)
	}
}

func TestLifecycleReceiptVerifyAndAutoClose(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ra04"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{responseID}, io); code != 0 {
		t.Fatalf("verify: code = %d; stderr=%s", code, errOut.String())
	}
	verifyEvent := lifecycleReceiptEvent(t, fake.calls[0].Files, "verify")
	if !strings.Contains(verifyEvent, "state: verified") {
		t.Fatalf("verify omitted its evaluator receipt:\n%s", verifyEvent)
	}
	closeEvent := lifecycleReceiptEvent(t, fake.calls[0].Files, "close")
	if !strings.Contains(closeEvent, "state: closed") {
		t.Fatalf("auto-close omitted its evaluator receipt:\n%s", closeEvent)
	}
}

func TestLifecycleReceiptDisputeOmitted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ra05"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewDisputeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--reason", "incorrect", responseID}, io); code != 0 {
		t.Fatalf("dispute: code = %d; stderr=%s", code, errOut.String())
	}
	event := lifecycleReceiptEvent(t, fake.calls[0].Files, "dispute")
	if strings.Contains(event, "state:") {
		t.Fatalf("dispute is receipt-N/A and must omit state:\n%s", event)
	}
}

func TestLifecycleReceiptNoteOmitted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-ra06"
	writeQuestionArtifact(t, mirrorDir, id, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewNoteCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--note", "fyi", id}, io); code != 0 {
		t.Fatalf("note: code = %d; stderr=%s", code, errOut.String())
	}
	event := lifecycleReceiptEvent(t, fake.calls[0].Files, "note")
	if strings.Contains(event, "state:") {
		t.Fatalf("note is receipt-N/A and must omit state:\n%s", event)
	}
}
