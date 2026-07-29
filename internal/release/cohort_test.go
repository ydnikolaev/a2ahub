package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type cohortVerifierFunc func(context.Context, string, Release) error

func (f cohortVerifierFunc) Verify(ctx context.Context, path string, rel Release) error {
	return f(ctx, path, rel)
}

func cohortAsset(name, kind string, content []byte) CohortComponent {
	sum := sha256.Sum256(content)
	return CohortComponent{
		Kind:            ComponentKind(kind),
		Name:            name,
		SHA256:          hex.EncodeToString(sum[:]),
		ProductVersion:  "1.2.3",
		ProtocolVersion: 2,
	}
}

func validCohortManifest() CohortManifest {
	cli := cohortAsset("a2a-darwin-arm64", "cli", []byte("cli"))
	app := cohortAsset("A2A-Notifier-1.2.3.zip", "macos", []byte("app"))
	app.BundleID = MacOSBundleID
	app.TeamID = "A2ADEVTEAM"
	vsix := cohortAsset("a2a-notifications-1.2.3.vsix", "vscode", []byte("vsix"))
	vsix.Publisher = "a2ahub"
	vsix.ExtensionID = "a2ahub.notifications"
	detail := cohortAsset("release-notes-1.2.3.yaml", "release-notes", []byte("notes"))
	detail.Schema = ReleaseNotesSchemaV1
	return CohortManifest{
		Schema:         CohortSchemaV1,
		ProductVersion: "1.2.3",
		Protocol:       ProtocolRange{Min: 1, Max: 2},
		Components:     []CohortComponent{cli, app, vsix, detail},
	}
}

func marshalCohortForTest(t *testing.T, manifest CohortManifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	return raw
}

func TestCohortParseBoundedStrictAndComplete(t *testing.T) {
	t.Parallel()
	raw := marshalCohortForTest(t, validCohortManifest())
	got, err := ParseCohort(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCohort: %v", err)
	}
	if got.ProductVersion != "1.2.3" || len(got.Components) != 4 {
		t.Fatalf("manifest = %+v", got)
	}

	withUnknown := bytes.Replace(raw, []byte(`"product_version"`), []byte(`"future":true,"product_version"`), 1)
	if _, err := ParseCohort(bytes.NewReader(withUnknown)); !errors.Is(err, ErrCohortMalformed) {
		t.Fatalf("unknown field error = %v, want ErrCohortMalformed", err)
	}

	oversized := strings.Repeat(" ", maxCohortBytes+1)
	if _, err := ParseCohort(strings.NewReader(oversized)); !errors.Is(err, ErrCohortTooLarge) {
		t.Fatalf("oversized error = %v, want ErrCohortTooLarge", err)
	}
}

func TestCohortAcceptsObservedAdHocMacIdentity(t *testing.T) {
	t.Parallel()
	manifest := validCohortManifest()
	manifest.Components[1].TeamID = MacOSAdHocTeamID
	if _, err := ParseCohort(bytes.NewReader(marshalCohortForTest(t, manifest))); err != nil {
		t.Fatalf("ParseCohort(ad-hoc mac identity): %v", err)
	}
}

func TestCohortRejectsIdentityVersionAndDuplicateFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*CohortManifest)
	}{
		{"wrong mac bundle", func(m *CohortManifest) { m.Components[1].BundleID = "example.bad" }},
		{"missing mac team", func(m *CohortManifest) { m.Components[1].TeamID = "" }},
		{"publisher mismatch", func(m *CohortManifest) { m.Components[2].ExtensionID = "other.notifications" }},
		{"missing notes schema", func(m *CohortManifest) { m.Components[3].Schema = "" }},
		{"component product skew", func(m *CohortManifest) { m.Components[0].ProductVersion = "1.2.2" }},
		{"protocol outside range", func(m *CohortManifest) { m.Components[0].ProtocolVersion = 3 }},
		{"wider than n-1", func(m *CohortManifest) { m.Protocol.Min = 0 }},
		{"duplicate name", func(m *CohortManifest) { m.Components[1].Name = m.Components[0].Name }},
		{"path name", func(m *CohortManifest) { m.Components[0].Name = "../a2a" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := validCohortManifest()
			tc.mutate(&m)
			if _, err := ParseCohort(bytes.NewReader(marshalCohortForTest(t, m))); !errors.Is(err, ErrCohortInvalid) {
				t.Fatalf("error = %v, want ErrCohortInvalid", err)
			}
		})
	}
}

func TestCohortCompatibilityIsCurrentOrPreviousOnly(t *testing.T) {
	t.Parallel()
	m := validCohortManifest()
	for protocol, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		if got := m.Compatible(protocol); got != want {
			t.Fatalf("Compatible(%d) = %v, want %v", protocol, got, want)
		}
	}
}

func TestCohortVerifyUsesExistingVerifierBeforeReturningManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a2a-cohort-1.2.3.json")
	if err := os.WriteFile(path, marshalCohortForTest(t, validCohortManifest()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var calls []string
	verifier := cohortVerifierFunc(func(_ context.Context, got string, _ Release) error {
		calls = append(calls, filepath.Base(got))
		return nil
	})
	verified, err := VerifyCohort(context.Background(), path, Release{Version: "1.2.3"}, verifier)
	if err != nil {
		t.Fatalf("VerifyCohort: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"a2a-cohort-1.2.3.json"}) {
		t.Fatalf("verifier calls = %v", calls)
	}
	if verified.Manifest().ProductVersion != "1.2.3" {
		t.Fatalf("verified manifest = %+v", verified.Manifest())
	}

	_, err = VerifyCohort(context.Background(), path, Release{Version: "9.9.9"}, verifier)
	if !errors.Is(err, ErrCohortInvalid) {
		t.Fatalf("release skew error = %v, want ErrCohortInvalid", err)
	}
}
