package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

func fixedActorResolver(cli.ActorFlags) template.Actor {
	return template.Actor{Kind: "agent", Name: "test-bot", Model: "test-model"}
}

// TestNewDraftsEveryTypeV1Valid is AC-401.1, the real cli-layer
// integration: for every type in the P2 corpus, `a2a new <type>` with
// placeholder-only fills (plus --slug for the two standing types) then
// `a2a validate` on the drafted file returns V1-pass — driven against
// the real validate.Engine (schema.Load), not a fake.
func TestNewDraftsEveryTypeV1Valid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	for _, typ := range schema.EnvelopeTypes() {
		typ := typ
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			stagingDir := filepath.Join(t.TempDir(), "staging")
			cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver)

			args := []string{typ}
			if typ == "contract" || typ == "requirement" {
				args = append(args, "--slug", "ingest")
			}

			io, out, errOut := newIO()
			code := cmd.Run(context.Background(), args, io)
			if code != 0 {
				t.Fatalf("new %s: code = %d; stdout=%s stderr=%s", typ, code, out.String(), errOut.String())
			}

			entries, err := os.ReadDir(stagingDir)
			if err != nil {
				t.Fatalf("ReadDir(%s): %v", stagingDir, err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected exactly one staged draft, got %d", len(entries))
			}

			raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			result, err := engine.ValidateDraft(validate.Draft{Path: filepath.Join(stagingDir, entries[0].Name()), Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if !result.Valid {
				t.Fatalf("draft for %s is V1-invalid: %+v\n---\n%s", typ, result.Violations, raw)
			}
		})
	}
}

func TestNewStandingTypeRequiresSlug(t *testing.T) {
	t.Parallel()
	cmd := cli.NewNewCommand(t.TempDir(), "axon", fixedActorResolver)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"contract"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error, missing --slug)", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable stderr message")
	}
}

func TestNewUnknownType(t *testing.T) {
	t.Parallel()
	cmd := cli.NewNewCommand(t.TempDir(), "axon", fixedActorResolver)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"bogus"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (unknown type)", code)
	}
}

func TestNewFieldOverrideAndBodyFile(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("custom body content\n"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"question", "--field", "category=defect", "--body-file", bodyFile}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, entries=%v", err, entries)
	}
	raw, err := os.ReadFile(filepath.Join(stagingDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("category: defect")) {
		t.Fatalf("expected the --field override to land; got:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte("custom body content")) {
		t.Fatalf("expected the --body-file content to land; got:\n%s", raw)
	}
}

// TestNewMintsValidIDAndSectionsFromOwnSystem checks the minted id's
// <system> token matches the configured own system.
func TestNewMintsIDFromOwnSystem(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	cmd := cli.NewNewCommand(stagingDir, "axon", fixedActorResolver)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"question"}, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, %v", err, entries)
	}
	stem := entries[0].Name()[:len(entries[0].Name())-len(".md")]
	id, err := artifact.ParseID(stem)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", stem, err)
	}
	if id.System != "axon" {
		t.Fatalf("System = %q, want axon", id.System)
	}
}

// --- template list/show ----------------------------------------------------

func TestTemplateListAndShow(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()

	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"list"}, io); code != 0 {
		t.Fatalf("template list: code = %d", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("question")) {
		t.Fatalf("expected 'question' in template list output; got %q", out.String())
	}

	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"show", "question"}, io2); code != 0 {
		t.Fatalf("template show question: code = %d", code)
	}
	if !bytes.Contains(out2.Bytes(), []byte("type: question")) {
		t.Fatalf("expected the canonical question template body; got %q", out2.String())
	}
}

func TestTemplateShowUnknownType(t *testing.T) {
	t.Parallel()
	cmd := cli.NewTemplateCommand()
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"show", "bogus"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (unknown type)", code)
	}
}
