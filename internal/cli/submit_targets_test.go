package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestResolveSubmitTargets(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	for _, n := range []string{"XQ-axon-1.md", "XQ-axon-2.md", "note.txt"} {
		if err := os.WriteFile(filepath.Join(staging, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("bare id resolves to stagingDir/<id>.md", func(t *testing.T) {
		t.Parallel()
		got, err := cli.ResolveSubmitTargets(staging, []string{"XQ-axon-1"})
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(staging, "XQ-axon-1.md")
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want [%s]", got, want)
		}
	})

	t.Run("explicit path passes through", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(staging, "XQ-axon-1.md")
		got, err := cli.ResolveSubmitTargets(staging, []string{p})
		if err != nil || len(got) != 1 || got[0] != p {
			t.Errorf("got %v, %v", got, err)
		}
	})

	t.Run("--drafts lists only .md under staging", func(t *testing.T) {
		t.Parallel()
		got, err := cli.ResolveSubmitTargets(staging, []string{"--drafts"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 { // the two .md, not note.txt
			t.Errorf("got %v, want 2 .md files", got)
		}
	})

	t.Run("--batch resolves each arg", func(t *testing.T) {
		t.Parallel()
		got, err := cli.ResolveSubmitTargets(staging, []string{"--batch", "XQ-axon-1", "XQ-axon-2"})
		if err != nil || len(got) != 2 {
			t.Errorf("got %v, %v", got, err)
		}
	})

	t.Run("--batch with no args is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := cli.ResolveSubmitTargets(staging, []string{"--batch"})
		var ue *cli.SubmitUsageError
		if !errors.As(err, &ue) {
			t.Errorf("want SubmitUsageError, got %v", err)
		}
	})

	t.Run("bare submit with zero args is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := cli.ResolveSubmitTargets(staging, nil)
		var ue *cli.SubmitUsageError
		if !errors.As(err, &ue) {
			t.Errorf("want SubmitUsageError, got %v", err)
		}
	})
}

// TestSubmitBatchCrossSpaceRefused is the wave-3 audit MED: a batch spanning
// two spaces must be refused before the funnel (one submit = one space = one
// PR), so no artifact gets committed into the wrong space with a falsified
// `space` field.
func TestSubmitBatchCrossSpaceRefused(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	writeSpacedDraft(t, staging, "XQ-axon-20260721-aaaa", "axon", "getvisa")
	writeSpacedDraft(t, staging, "XQ-axon-20260721-bbbb", "axon", "otherspace")

	funnel := &fakeSubmitFunnel{}
	legality := cli.NewLegalityAdapter(t.TempDir(), "axon", testManifest())
	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), t.TempDir(), "getvisa", "axon", staging, testHostConfig())

	var out, errb bytes.Buffer
	code := cmd.Run(context.Background(), []string{"--drafts"}, cli.IO{Stdout: &out, Stderr: &errb})
	if code == 0 {
		t.Fatalf("cross-space batch: want non-zero exit, got 0 (out=%q)", out.String())
	}
	if len(funnel.calls) != 0 {
		t.Fatalf("cross-space batch: funnel must NOT be called, got %d calls", len(funnel.calls))
	}
	if !bytes.Contains(errb.Bytes(), []byte("multiple spaces")) {
		t.Errorf("want a multiple-spaces refusal message, got %q", errb.String())
	}
}

func writeSpacedDraft(t *testing.T, dir, id, from, spaceID string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\n" +
		"space: " + spaceID + "\nfrom: " + from + "\nto: [seomatrix]\n" +
		"actor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n" +
		"category: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
}
