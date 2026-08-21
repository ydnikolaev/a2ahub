package feedback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// captureFunnel records the SubmitRequest it was handed and writes nothing.
// AC #2 and AC #3 are both assertions about FileWrite.Content and about
// whether the funnel was reached AT ALL — never about a return value.
type captureFunnel struct {
	calls []space.SubmitRequest
}

func (f *captureFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.calls = append(f.calls, req)
	return space.WriteResult{Branch: "a2a/feedback/submit/" + req.ArtifactID, PRNumber: 7, PRURL: "https://example.invalid/pr/7"}, nil
}

func newCapturingSubmitter(t *testing.T, funnel Funnel) *Submitter {
	t.Helper()
	sub := NewSubmitter(funnel, filepath.Join(t.TempDir(), "ledger.yaml"), t.TempDir(), "test-repo", SubmitConfig{
		RemoteURL:  "file:///dev/null",
		Repo:       host.Repo{Owner: "a2ahub", Name: "a2ahub"},
		BaseBranch: "feedback-hub",
	})
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	sub.SetMirrorDirForTest(func(string, string) string { return t.TempDir() })
	return sub
}

// TestSubmit_WritesTheNormalizedBytes is AC #2: the bytes that become
// FileWrite.Content carry no comment — not the header block, not a trailing
// comment on a top-level key, not one on a nested key, not a commented-out
// optional key — and they are exactly the corpus's clean twin.
func TestSubmit_WritesTheNormalizedBytes(t *testing.T) {
	t.Parallel()
	dirty := filepath.Join(commentHostileRoot, "scaffolded.dirty.yaml")
	raw := readFixture(t, dirty)

	funnel := &captureFunnel{}
	sub := newCapturingSubmitter(t, funnel)
	path := filepath.Join(t.TempDir(), "fb-20260818-76f29d.yaml")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if _, err := sub.Submit(context.Background(), path); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("expected exactly one funnel write, got %d", len(funnel.calls))
	}
	if len(funnel.calls[0].Files) != 1 {
		t.Fatalf("expected exactly one file in the write, got %d", len(funnel.calls[0].Files))
	}
	got := funnel.calls[0].Files[0].Content
	want := readFixture(t, filepath.Join(commentHostileRoot, "scaffolded.clean.yaml"))
	if string(got) != string(want) {
		t.Fatalf("the funnel was handed unnormalized bytes.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	for _, scaffolding := range []string{
		"# Feedback authoring skeleton",
		"# auto-filled",
		"# bug | feature | docs | friction | protocol",
		"# error_code",
		"# evidence is REQUIRED",
		"# 1-10 concrete repro steps",
		"# resolution is hub-mutated only",
	} {
		if strings.Contains(string(got), scaffolding) {
			t.Fatalf("authoring scaffolding %q reached the hub of record", scaffolding)
		}
	}
}

// TestSubmit_FailsClosedOnNormalizationRefusal is AC #3: a fault-injected
// normalization failure means NO bytes and NO write — the same fail-closed
// discipline applyStatusResolution already applies to its own round trip.
func TestSubmit_FailsClosedOnNormalizationRefusal(t *testing.T) {
	t.Parallel()
	funnel := &captureFunnel{}
	sub := newCapturingSubmitter(t, funnel)
	sentinel := errors.New("injected: the round trip did not agree")
	sub.SetNormalizeForTest(func([]byte) ([]byte, error) { return nil, sentinel })

	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	_, err := sub.Submit(context.Background(), path)
	if err == nil {
		t.Fatal("expected Submit to refuse when normalization fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the injected normalization error to surface, got: %v", err)
	}
	if len(funnel.calls) != 0 {
		t.Fatalf("a write reached the funnel after normalization refused: %d call(s)", len(funnel.calls))
	}
}

// TestSubmit_ValidatesTheBytesItSends is §T1.2's "validate the bytes you
// send": a normalization that produced an invalid document must be refused by
// validation, not shipped because `raw` happened to be valid.
func TestSubmit_ValidatesTheBytesItSends(t *testing.T) {
	t.Parallel()
	funnel := &captureFunnel{}
	sub := newCapturingSubmitter(t, funnel)
	sub.SetNormalizeForTest(func([]byte) ([]byte, error) {
		return []byte("feedback: v1\nid: fb-20260723-abc123\n"), nil
	})

	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if _, err := sub.Submit(context.Background(), path); err == nil {
		t.Fatal("expected Submit to validate the NORMALIZED bytes, not the input")
	}
	if len(funnel.calls) != 0 {
		t.Fatalf("an invalid normalized document reached the funnel: %d call(s)", len(funnel.calls))
	}
}

// TestSubmit_NeverNewlyRefusesAValidFixture is AC #7: every record that
// submits today still submits. Normalization may not be the reason a submit
// fails.
func TestSubmit_NeverNewlyRefusesAValidFixture(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "valid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one valid feedback fixture")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw := readFixture(t, f)
			funnel := &captureFunnel{}
			sub := newCapturingSubmitter(t, funnel)
			path := filepath.Join(t.TempDir(), filepath.Base(f))
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatalf("write draft: %v", err)
			}
			if _, err := sub.Submit(context.Background(), path); err != nil {
				t.Fatalf("a record that submits today was refused: %v", err)
			}
			if len(funnel.calls) != 1 {
				t.Fatalf("expected the record to reach the funnel, got %d call(s)", len(funnel.calls))
			}
		})
	}
}

// TestDraft_KeepsTheAuthoringScaffolding is AC #1, and it is the guard
// against §T1.1's REJECTED option shipping by accident: the fix for a
// submitted record must not delete the affordance the DRAFT exists to
// provide. A draft is a form and may carry instructions; a submitted record
// is evidence and may not.
func TestDraft_KeepsTheAuthoringScaffolding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := NewDrafter(dir, "0.23.0")
	path, err := d.Draft("bug", "a title long enough to satisfy the schema")
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	drafted := string(readFixture(t, path))

	headerLines := 0
	for _, line := range strings.Split(drafted, "\n") {
		if !strings.HasPrefix(line, "#") {
			break
		}
		headerLines++
	}
	if headerLines < 12 {
		t.Fatalf("the drafted skeleton's header block is %d lines, want at least 12:\n%s", headerLines, drafted)
	}
	for _, affordance := range []string{
		"# bug | feature | docs | friction | protocol",
		"# blocker | major | minor",
	} {
		if !strings.Contains(drafted, affordance) {
			t.Fatalf("the drafted skeleton lost the enum reminder %q:\n%s", affordance, drafted)
		}
	}
	for _, check := range []string{
		"docs_consulted: false",
		"grounded_in_real_work: false",
		"not_space_specific: false",
		"no_sensitive_content: false",
		"duplicates_checked: false",
	} {
		if !strings.Contains(drafted, check) {
			t.Fatalf("the drafted skeleton no longer starts %q:\n%s", check, drafted)
		}
	}
}

// TestDraftThenNormalize_TheRealTemplate closes the loop the fixtures only
// model: the skeleton that actually ships, drafted by the code that actually
// drafts it, normalizes to a comment-free document with every value intact.
// A fixture can drift from the template; this cannot.
func TestDraftThenNormalize_TheRealTemplate(t *testing.T) {
	t.Parallel()
	d := NewDrafter(t.TempDir(), "0.23.0")
	path, err := d.Draft("bug", "a title long enough to satisfy the schema")
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	drafted := readFixture(t, path)
	if !strings.Contains(string(drafted), "# auto-filled") {
		t.Fatal("the drafted skeleton carries no scaffolding, so this test proves nothing")
	}

	got, err := normalizeRecord(drafted)
	if err != nil {
		t.Fatalf("normalizeRecord over a real draft: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the normalized draft does not parse: %v", err)
	}
	if c := firstComment(&doc); c != "" {
		t.Fatalf("a comment survived normalization of a real draft: %q", c)
	}
	var before, after any
	if err := yaml.Unmarshal(drafted, &before); err != nil {
		t.Fatalf("unmarshal draft: %v", err)
	}
	if err := yaml.Unmarshal(got, &after); err != nil {
		t.Fatalf("unmarshal normalized draft: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("normalizing a real draft changed a VALUE.\nbefore: %#v\nafter:  %#v", before, after)
	}
}
