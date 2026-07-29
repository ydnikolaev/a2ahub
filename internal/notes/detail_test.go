package notes

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/release"
)

func TestDetailDecoderRejectsUntrustedShapes(t *testing.T) {
	t.Parallel()

	decoder, err := NewDetailDecoder()
	if err != nil {
		t.Fatalf("NewDetailDecoder: %v", err)
	}

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{
			name: "malformed yaml",
			raw:  "schema: [not valid",
			want: release.ErrDetailMalformed,
		},
		{
			name: "well formed future schema",
			raw: `schema: release-notes/v2
version: "2.0.0"
released: "2027-01-01"
headline: "future"
changes: []
`,
			want: release.ErrDetailUnknownSchema,
		},
		{
			name: "v1 missing required headline",
			raw: `schema: release-notes/v1
version: "1.2.3"
released: "2026-01-02"
changes:
  - id: RN-01203-1
    kind: feat
    impact: normal
    subject: "subject"
    detail: "detail"
    action:
      scope: none
      why: "nothing to do"
`,
			want: release.ErrDetailMalformed,
		},
		{
			name: "v1 rejects an unknown action scope",
			raw: `schema: release-notes/v1
version: "1.2.3"
released: "2026-01-02"
headline: "headline"
changes:
  - id: RN-01203-1
    kind: feat
    impact: normal
    subject: "subject"
    detail: "detail"
    action:
      scope: fleet
      why: "invalid scope"
`,
			want: release.ErrDetailMalformed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			detail, err := decoder.DecodeAndValidate([]byte(test.raw))
			if !errors.Is(err, test.want) {
				t.Fatalf("DecodeAndValidate error = %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(detail, release.Detail{}) {
				t.Fatalf("DecodeAndValidate returned partial untrusted detail: %+v", detail)
			}
		})
	}
}

func TestDetailDecoderProjectsValidatedV1(t *testing.T) {
	t.Parallel()

	decoder, err := NewDetailDecoder()
	if err != nil {
		t.Fatalf("NewDetailDecoder: %v", err)
	}
	raw := []byte(`schema: release-notes/v1
version: "1.2.3"
released: "2026-01-02"
headline: "A verified future release"
changes:
  - id: RN-01203-1
    kind: feat
    impact: high
    subject: "First change"
    detail: "Detailed first change"
    affects: ["a2a update", "a2a notifications"]
    action:
      scope: local
      why: "Refresh the local component"
      detect: ["a2a notifications status --json"]
      run: ["a2a notifications install --channel all"]
  - id: RN-01203-2
    kind: known-issue
    impact: low
    subject: "Second change"
    detail: "Detailed second change"
    action:
      scope: none
      why: "No action"
`)

	got, err := decoder.DecodeAndValidate(raw)
	if err != nil {
		t.Fatalf("DecodeAndValidate: %v", err)
	}
	want := release.Detail{
		Schema:   release.ReleaseNotesSchemaV1,
		Version:  "1.2.3",
		Released: "2026-01-02",
		Headline: "A verified future release",
		Changes: []release.DetailChange{
			{
				ID: "RN-01203-1", Kind: "feat", Impact: "high",
				Subject: "First change", Detail: "Detailed first change",
				Affects: []string{"a2a update", "a2a notifications"},
				Action: release.DetailAction{
					Scope: "local", Why: "Refresh the local component",
					Detect: []string{"a2a notifications status --json"},
					Run:    []string{"a2a notifications install --channel all"},
				},
			},
			{
				ID: "RN-01203-2", Kind: "known-issue", Impact: "low",
				Subject: "Second change", Detail: "Detailed second change",
				Action: release.DetailAction{Scope: "none", Why: "No action"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeAndValidate = %+v, want %+v", got, want)
	}
}
