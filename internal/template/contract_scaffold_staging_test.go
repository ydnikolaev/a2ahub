package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The staging half of the D-D scaffold moved here from internal/cli so
// BOTH surfaces could call it — internal/mcp may not import internal/cli,
// so a helper left there would have meant a second implementation, which
// is exactly what the CLI/MCP parity suite exists to forbid. These tests
// moved with it.

func TestScaffoldContractInStagingWritesBothFilesAtTheLayoutShape(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()

	written, err := ScaffoldContractInStaging(staging, "axon", "widget", os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractInStaging: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("written = %v, want the schema and the fixture", written)
	}

	// The paths must match what a later publish and `submit`'s sidecar
	// carry both look for — the space layout's own shape, rooted at
	// staging. A scaffold at any other path is invisible to both.
	wantSchema := filepath.Join(staging, "axon", "provides", "widget", "schema", "widget.schema.json")
	wantFixture := filepath.Join(staging, "axon", "provides", "widget", "fixtures", "valid", "widget.json")
	for _, want := range []string{wantSchema, wantFixture} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected a scaffolded file at %s: %v", want, err)
		}
	}

	// D-E's mapping is by file stem, so the two names must share one.
	if filepath.Base(wantFixture) != "widget.json" || !strings.HasPrefix(filepath.Base(wantSchema), "widget.") {
		t.Fatalf("scaffold names break D-E's stem mapping: %s / %s", wantSchema, wantFixture)
	}
}

func TestScaffoldContractInStagingNeverOverwrites(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()

	schemaPath := filepath.Join(staging, "axon", "provides", "widget", "schema", "widget.schema.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const authored = `{"$comment":"the author's own work"}`
	if err := os.WriteFile(schemaPath, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := ScaffoldContractInStaging(staging, "axon", "widget", os.WriteFile)
	if err != nil {
		t.Fatalf("ScaffoldContractInStaging: %v", err)
	}
	for _, p := range written {
		if p == schemaPath {
			t.Fatalf("re-running the scaffold reported writing over an existing file: %s", p)
		}
	}
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != authored {
		t.Fatalf("the author's schema was clobbered: %s", raw)
	}
	// The fixture was absent, so it still gets written.
	if len(written) != 1 {
		t.Fatalf("written = %v, want only the missing fixture", written)
	}
}

func TestScaffoldContractInStagingRefusesAnUnusableSystem(t *testing.T) {
	t.Parallel()
	if _, err := ScaffoldContractInStaging(t.TempDir(), "", "widget", os.WriteFile); err == nil {
		t.Fatal("expected an error for a system the layout cannot be built for")
	}
}

func TestScaffoldContractInStagingSurfacesAWriteFailure(t *testing.T) {
	t.Parallel()
	boom := func(string, []byte, os.FileMode) error { return os.ErrPermission }
	written, err := ScaffoldContractInStaging(t.TempDir(), "axon", "widget", boom)
	if err == nil {
		t.Fatal("expected the write error to surface, not be swallowed")
	}
	if len(written) != 0 {
		t.Fatalf("nothing was written, so written must be empty; got %v", written)
	}
}

func TestContractDraftSchemaFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		draft   string
		want    string
		wantErr bool
	}{
		{
			name:  "reads what the RENDERED draft declares, not the template default",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\nschema_format: proto3\n---\nbody\n",
			want:  "proto3",
		},
		{
			name:  "the JSON-Schema dialect the scaffold is gated on",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\nschema_format: json-schema-2020-12\n---\nbody\n",
			want:  "json-schema-2020-12",
		},
		{
			name:  "an absent schema_format is empty, not an error",
			draft: "---\nschema: envelope/v1\nid: XC-axon-widget\n---\nbody\n",
			want:  "",
		},
		{
			name:    "no frontmatter at all is an error, never a silent empty",
			draft:   "just a body\n",
			wantErr: true,
		},
		{
			name:    "malformed YAML is an error",
			draft:   "---\nschema_format: [unclosed\n---\nbody\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ContractDraftSchemaFormat([]byte(tc.draft))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ContractDraftSchemaFormat: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
