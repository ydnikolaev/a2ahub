package space

import (
	"context"
	"errors"
	"testing"
)

const validManifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
gates: default
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
  - system: seomatrix
    org: seomatrix
    section: seomatrix/
    owners: [misha-gh]
    status: active
    joined: 2026-07-28
vendored: []
`

func TestParseManifestValid(t *testing.T) {
	t.Parallel()

	m, err := ParseManifest([]byte(validManifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Space != "getvisa" || m.MinBinaryVersion != "0.1.0" {
		t.Fatalf("Manifest = %+v, want space=getvisa min_binary_version=0.1.0", m)
	}
	if len(m.Participants) != 2 {
		t.Fatalf("len(Participants) = %d, want 2", len(m.Participants))
	}

	sys, ok := m.SystemForLogin("ydnikolaev")
	if !ok || sys != "axon" {
		t.Fatalf("SystemForLogin(ydnikolaev) = (%q, %v), want (axon, true)", sys, ok)
	}
	if _, ok := m.SystemForLogin("nobody"); ok {
		t.Fatal("SystemForLogin(nobody) = true, want false (CC-097 unmapped identity)")
	}
}

func TestParseManifestInvalidYAML(t *testing.T) {
	t.Parallel()

	_, err := ParseManifest([]byte("not: [valid: yaml"))
	if !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("ParseManifest error = %v, want ErrManifestInvalid", err)
	}
}

// fakeManifestValidator is a hand-written test double for the
// ManifestValidator seam (rails: hand-written mocks, no codegen).
type fakeManifestValidator struct {
	err error
}

func (f *fakeManifestValidator) ValidateManifest(_ context.Context, _ []byte) error {
	return f.err
}

// TestLoadManifestPropagatesValidatorError exercises LoadManifest — the
// composed parse+validate operation — through the package's own code
// path, not the fake echoing itself: a validator error must surface from
// LoadManifest even though the YAML parsed fine.
func TestLoadManifestPropagatesValidatorError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("missing participant map entry for login x (CC-097 precondition)")
	v := &fakeManifestValidator{err: wantErr}

	_, err := LoadManifest(context.Background(), []byte(validManifestYAML), v)
	if !errors.Is(err, wantErr) {
		t.Fatalf("LoadManifest = %v, want wrapping %v", err, wantErr)
	}
}

// TestLoadManifestValidatorApprovesValidManifest is the success path: a
// validator that approves lets LoadManifest return the parsed Manifest.
func TestLoadManifestValidatorApprovesValidManifest(t *testing.T) {
	t.Parallel()

	v := &fakeManifestValidator{}
	m, err := LoadManifest(context.Background(), []byte(validManifestYAML), v)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Space != "getvisa" {
		t.Fatalf("Manifest.Space = %q, want getvisa", m.Space)
	}
}

// TestLoadManifestParseFailureShortCircuitsValidator confirms a
// structural YAML failure never even reaches the validator seam.
func TestLoadManifestParseFailureShortCircuitsValidator(t *testing.T) {
	t.Parallel()

	v := &fakeManifestValidator{err: errors.New("should never be called")}
	_, err := LoadManifest(context.Background(), []byte("not: [valid: yaml"), v)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("LoadManifest error = %v, want ErrManifestInvalid", err)
	}
}

var _ ManifestValidator = (*fakeManifestValidator)(nil)
