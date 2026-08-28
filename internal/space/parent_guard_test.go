package space

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/schemas"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// parentGuardResponseBody renders a minimal envelope frontmatter — the
// exact four keys declaredParentRequiningEnvelope reads (schema, type, id,
// parent) — with NO other fields, mirroring
// delivery_possession_test.go's own deliveryPossessionResponseBody idiom.
// schemaVersion == "" omits the `schema` key entirely (the "this guard
// cannot classify the document" shape).
func parentGuardResponseBody(schemaVersion, id, parent string) string {
	body := "---\n"
	if schemaVersion != "" {
		body += "schema: " + schemaVersion + "\n"
	}
	body += "id: " + id + "\n" +
		"type: response\n"
	if parent != "" {
		body += "parent: " + parent + "\n"
	}
	body += "result: answered\n" +
		"---\nbody\n"
	return body
}

// parentGuardEventBody renders event/v1's own bare-YAML shape (NO
// frontmatter — layout.EventFile writes a plain YAML file;
// cmd_lifecycle.go's lifecycleEventDoc and internal/mcp's eventdoc.go
// both marshal the identical `subject`/`transition` keys). transition ==
// "" omits both — the "not an event at all" shape a plain placeholder
// like "event: submit\n" already exercises elsewhere in this package.
func parentGuardEventBody(subject, transition string) string {
	if transition == "" {
		return "event: submit\n"
	}
	return "schema: event/v1\n" +
		"event: 01J8QYK2Z3ABCDEFGHJKMNPQRS\n" +
		"space: axon\n" +
		"subject: " + subject + "\n" +
		"transition: " + transition + "\n" +
		"actor: {kind: agent, name: a2a}\n" +
		"at: 2026-08-28T00:00:00Z\n"
}

// TestSchemaRequiresParent_FixtureSchema is AC-4's fixture-schema table
// test: a SYNTHETIC schema corpus (fstest.MapFS, never schemas.FS) proves
// the derivation is generic — reads whatever schema file a (schemaVersion,
// type) pair names, never a hardcoded kind list. Adding a fixture kind
// whose own `required` array gains "parent" flips the answer with NO
// change to schemaRequiresParent itself.
func TestSchemaRequiresParent_FixtureSchema(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"fixture/v1/plain.schema.json":  {Data: []byte(`{"required": ["id", "type"]}`)},
		"fixture/v1/moved.schema.json":  {Data: []byte(`{"required": ["id", "parent"]}`)},
		"fixture/v1/nested.schema.json": {Data: []byte(`{"allOf":[{"required":["parent"]}], "required": ["id"]}`)},
	}

	tests := []struct {
		name          string
		schemaVersion string
		typ           string
		want          bool
	}{
		{"a kind whose own required array does not name parent", "fixture/v1", "plain", false},
		{"a kind whose own required array names parent", "fixture/v1", "moved", true},
		{"parent nested under allOf, absent from the TOP-LEVEL required array, does not count", "fixture/v1", "nested", false},
		{"an unknown type resolves no file and answers false, never an error", "fixture/v1", "no-such-type", false},
		{"an empty schema version answers false", "", "plain", false},
		{"an empty type answers false", "fixture/v1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := schemaRequiresParent(fsys, tt.schemaVersion, tt.typ); got != tt.want {
				t.Fatalf("schemaRequiresParent(%q, %q) = %v, want %v", tt.schemaVersion, tt.typ, got, tt.want)
			}
		})
	}
}

// TestSchemaRequiresParent_RealCorpus is AC-4's second half: production
// uses the SAME derivation over the REAL embedded corpus, both envelope
// versions the response kind ships as, plus one kind (work_request) whose
// own top-level `required` array does not name parent even though a
// DIFFERENT nesting level (its `if`/`then` conditional) uses the word
// "required" for unrelated fields — proving this guard reads the
// TOP-LEVEL array only, the exact fact spec 05 §0.5 points at
// (schemas/envelope/v1/response.schema.json:21).
func TestSchemaRequiresParent_RealCorpus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		schemaVersion string
		typ           string
		want          bool
	}{
		{"envelope/v1 response requires parent", "envelope/v1", "response", true},
		{"envelope/v2 response requires parent", "envelope/v2", "response", true},
		{"envelope/v1 work_request's TOP-LEVEL required does not name parent", "envelope/v1", "work_request", false},
		{"envelope/v1 announcement does not name parent", "envelope/v1", "announcement", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := schemaRequiresParent(schemas.FS, tt.schemaVersion, tt.typ); got != tt.want {
				t.Fatalf("schemaRequiresParent(%q, %q) = %v, want %v", tt.schemaVersion, tt.typ, got, tt.want)
			}
		})
	}
}

// TestDeclaredEventTransitionSubject_MutuallyExclusiveWithFrontmatter
// proves the "never misreads a response draft as an event" property the
// guard's own doc comment claims: a document WITH frontmatter is never
// even attempted as an event, regardless of what its body contains.
func TestDeclaredEventTransitionSubject_MutuallyExclusiveWithFrontmatter(t *testing.T) {
	t.Parallel()

	envelope := []byte(parentGuardResponseBody("envelope/v1", "XS-axon-1", "XQ-axon-1"))
	if _, ok := declaredEventTransitionSubject(envelope); ok {
		t.Fatalf("declaredEventTransitionSubject(envelope-with-frontmatter) = ok, want false")
	}

	event := []byte(parentGuardEventBody("XQ-axon-1", "respond"))
	subject, ok := declaredEventTransitionSubject(event)
	if !ok || subject != "XQ-axon-1" {
		t.Fatalf("declaredEventTransitionSubject(event) = (%q, %v), want (\"XQ-axon-1\", true)", subject, ok)
	}

	placeholder := []byte(parentGuardEventBody("", ""))
	if _, ok := declaredEventTransitionSubject(placeholder); ok {
		t.Fatalf("declaredEventTransitionSubject(placeholder) = ok, want false")
	}
}

// TestFunnelSubmitRefusesResponseWithNoParentTransitionBeforeAnyGitAction
// is US-1/AC-1/AC-2 driven through the real funnel — the idiom
// TestFunnelSubmitRefusesUnresolvedHandoffDeliverableBeforeAnyGitAction
// (delivery_possession_test.go) already establishes: a fake host records
// zero pushes/opens, so "before any branch or PR exists" is asserted, not
// merely claimed.
func TestFunnelSubmitRefusesResponseWithNoParentTransitionBeforeAnyGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	parentID := "XQ-axon-20260721-k3f9"
	responseID := "XS-axon-20260828-resp1"
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		// A LONE response draft — exactly `a2a submit`'s own shape today:
		// the response file, and an unrelated placeholder event that
		// names no subject/transition at all (the same
		// "event: submit\n" placeholder newTestSubmitRequest's own
		// default already uses elsewhere in this package).
		{Path: l.Exchange(responseID), Content: []byte(parentGuardResponseBody("envelope/v2", responseID, parentID))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte(parentGuardEventBody("", ""))},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrResponseParentTransitionMissing) {
		t.Fatalf("Submit error = %v, want ErrResponseParentTransitionMissing", err)
	}
	msg := err.Error()
	for _, want := range []string{"REF-027", parentID, "a2a respond"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %q", msg, want)
		}
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero pushes/opens before any git action, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSubmitAcceptsResponseWithAccompanyingParentTransition is the
// regression the guard's own doc comment promises: `a2a respond`'s own
// two-file write (the response draft PLUS the parent's own `respond`
// event, `subject` naming the parent) never trips this guard — the same
// batch shape RespondCommand.Run (cmd_lifecycle.go) and internal/mcp's
// respond tool both author.
func TestFunnelSubmitAcceptsResponseWithAccompanyingParentTransition(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	parentID := "XQ-axon-20260721-k3f9"
	responseID := "XS-axon-20260828-resp2"
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(responseID), Content: []byte(parentGuardResponseBody("envelope/v2", responseID, parentID))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRT"), Content: []byte(parentGuardEventBody(parentID, "respond"))},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSubmitKindWithNoParentRequirementIsUnaffected is AC-8's
// regression stanza: a kind whose own schema does NOT require `parent`
// (announcement) submits exactly as it does today, even when it declares
// its own `schema`/`type` explicitly (so this proves the derivation's
// FALSE branch end to end, not merely that an untagged fixture is
// skipped).
func TestFunnelSubmitKindWithNoParentRequirementIsUnaffected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	announcementID := "XA-axon-20260828-ann1"
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(announcementID), Content: []byte(
			"---\nschema: envelope/v1\nid: " + announcementID + "\ntype: announcement\n---\nbody\n")},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRU"), Content: []byte(parentGuardEventBody("", ""))},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}
