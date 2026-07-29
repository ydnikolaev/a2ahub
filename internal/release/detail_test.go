package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type detailDecoderFunc func([]byte) (Detail, error)

func (f detailDecoderFunc) DecodeAndValidate(raw []byte) (Detail, error) {
	return f(raw)
}

func verifiedCohortForDetail(t *testing.T, dir string, notes []byte) VerifiedCohort {
	t.Helper()
	manifest := validCohortManifest()
	sum := sha256Hex(notes)
	manifest.Components[3].SHA256 = sum
	manifestPath := filepath.Join(dir, "cohort.json")
	if err := os.WriteFile(manifestPath, marshalCohortForTest(t, manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	verified, err := VerifyCohort(
		context.Background(),
		manifestPath,
		Release{Version: "1.2.3"},
		cohortVerifierFunc(func(context.Context, string, Release) error { return nil }),
	)
	if err != nil {
		t.Fatalf("VerifyCohort: %v", err)
	}
	return verified
}

func validDetail() Detail {
	return Detail{
		Schema:   ReleaseNotesSchemaV1,
		Version:  "1.2.3",
		Released: "2026-07-29",
		Headline: "Native notifications",
		Changes: []DetailChange{{
			ID: "RN-123-1", Kind: "feat", Impact: "normal", Subject: "Cohort",
			Detail: "Signed together", Action: DetailAction{Scope: "none", Why: "No action"},
		}},
	}
}

func TestDetailVerifiedAssetProducesStructuredCacheWithoutRawBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := []byte("schema: release-notes/v1\nsecret_raw_marker: do-not-cache\n")
	cohort := verifiedCohortForDetail(t, dir, raw)
	path := filepath.Join(dir, "release-notes-1.2.3.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	cache := ResolveDetail(context.Background(), DetailOptions{
		Cohort: cohort, AssetPath: path, Release: Release{Version: "1.2.3"},
		Verifier: cohortVerifierFunc(func(context.Context, string, Release) error { return nil }),
		Decoder:  detailDecoderFunc(func([]byte) (Detail, error) { return validDetail(), nil }),
		Now:      now,
	})
	if cache.Status != DetailVerified || cache.Detail == nil {
		t.Fatalf("cache = %+v", cache)
	}
	if cache.VerifiedAt != now || cache.TargetVersion != "1.2.3" {
		t.Fatalf("metadata = %+v", cache)
	}

	cachePath := filepath.Join(dir, "detail.json")
	if err := WriteDetailCache(cachePath, cache); err != nil {
		t.Fatalf("WriteDetailCache: %v", err)
	}
	stored, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if stringContains(stored, "secret_raw_marker") {
		t.Fatalf("raw source leaked into cache: %s", stored)
	}
	got, ok := ReadDetailCache(cachePath, "1.2.3")
	if !ok || got.Detail == nil || got.Detail.Headline != "Native notifications" {
		t.Fatalf("ReadDetailCache = %+v, %v", got, ok)
	}
}

func TestDetailFallbackNeverExposesPartiallyParsedProse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		writeAsset bool
		verifyErr  error
		decode     detailDecoderFunc
		want       DetailStatus
	}{
		{"missing", false, nil, nil, DetailMissing},
		{"unverified", true, ErrSignatureUnverified, nil, DetailUnverified},
		{"tampered checksum", true, ErrChecksumMismatch, nil, DetailTampered},
		{"tampered signature", true, ErrSignatureInvalid, nil, DetailTampered},
		{"malformed", true, nil, func([]byte) (Detail, error) {
			return Detail{Headline: "must disappear"}, ErrDetailMalformed
		}, DetailMalformed},
		{"unknown schema error", true, nil, func([]byte) (Detail, error) {
			return Detail{Headline: "must disappear"}, ErrDetailUnknownSchema
		}, DetailUnknownSchema},
		{"unknown schema result", true, nil, func([]byte) (Detail, error) {
			d := validDetail()
			d.Schema = "release-notes/v2"
			d.Headline = "must disappear"
			return d, nil
		}, DetailUnknownSchema},
		{"wrong target version", true, nil, func([]byte) (Detail, error) {
			d := validDetail()
			d.Version = "1.2.2"
			d.Headline = "must disappear"
			return d, nil
		}, DetailMalformed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			raw := []byte("future prose that must never survive fallback")
			cohort := verifiedCohortForDetail(t, dir, raw)
			path := filepath.Join(dir, "release-notes-1.2.3.yaml")
			if tc.writeAsset {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatalf("write notes: %v", err)
				}
			}
			cache := ResolveDetail(context.Background(), DetailOptions{
				Cohort: cohort, AssetPath: path, Release: Release{Version: "1.2.3"},
				Verifier: cohortVerifierFunc(func(context.Context, string, Release) error { return tc.verifyErr }),
				Decoder:  tc.decode,
			})
			if cache.Status != tc.want {
				t.Fatalf("status = %q, want %q", cache.Status, tc.want)
			}
			if cache.Detail != nil {
				t.Fatalf("fallback retained prose: %+v", cache.Detail)
			}
			if cache.TargetVersion != "1.2.3" || cache.AssetName != "release-notes-1.2.3.yaml" {
				t.Fatalf("fallback metadata = %+v", cache)
			}
		})
	}
}

func TestDetailCacheMalformedOrWrongTargetFallsBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "detail.json")
	if err := os.WriteFile(path, []byte(`{"status":"verified","target_version":"1.2.3","detail":{"headline":"partial"}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok := ReadDetailCache(path, "1.2.3")
	if ok || got.Status != DetailMalformed || got.Detail != nil {
		t.Fatalf("malformed cache = %+v, %v", got, ok)
	}

	cache := DetailCache{
		Status: DetailVerified, TargetVersion: "1.2.2", Schema: ReleaseNotesSchemaV1,
		Detail: ptrDetail(validDetail()),
	}
	cache.Detail.Version = "1.2.2"
	if err := WriteDetailCache(path, cache); err != nil {
		t.Fatalf("WriteDetailCache: %v", err)
	}
	got, ok = ReadDetailCache(path, "1.2.3")
	if ok || got.Status != DetailMissing || got.Detail != nil {
		t.Fatalf("wrong-target cache = %+v, %v", got, ok)
	}
}

func TestDetailUnknownFutureSchemaInSignedCohortFallsBackWithoutFetching(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := validCohortManifest()
	manifest.Components[3].Schema = "release-notes/v2"
	manifestPath := filepath.Join(dir, "cohort.json")
	if err := os.WriteFile(manifestPath, marshalCohortForTest(t, manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	verified, err := VerifyCohort(
		context.Background(),
		manifestPath,
		Release{Version: "1.2.3"},
		cohortVerifierFunc(func(context.Context, string, Release) error { return nil }),
	)
	if err != nil {
		t.Fatalf("future detail schema invalidated whole cohort: %v", err)
	}
	calls := 0
	cache := ResolveDetail(context.Background(), DetailOptions{
		Cohort: verified, AssetPath: filepath.Join(dir, manifest.Components[3].Name),
		Release: Release{Version: "1.2.3"},
		Verifier: cohortVerifierFunc(func(context.Context, string, Release) error {
			calls++
			return nil
		}),
		Decoder: detailDecoderFunc(func([]byte) (Detail, error) {
			t.Fatal("unknown schema must not be parsed best-effort")
			return Detail{}, nil
		}),
	})
	if cache.Status != DetailUnknownSchema || cache.Detail != nil {
		t.Fatalf("cache = %+v", cache)
	}
	if calls != 0 {
		t.Fatalf("unknown-schema detail verifier calls = %d, want 0", calls)
	}
}

func stringContains(b []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(b); i++ {
		if string(b[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

func ptrDetail(d Detail) *Detail { return &d }
