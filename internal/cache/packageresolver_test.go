package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// prWriteManifest writes a minimal, schema-tagged data-package/v1
// manifest.json at the exact path DeliverDataPackage would have written it
// to (internal/space/data_delivery.go's dataPackageManifestPath), so this
// file's tests exercise the SAME layout the real writer produces, not a
// convention invented locally.
func prWriteManifest(t *testing.T, dir, system, packageID string, fields map[string]any) {
	t.Helper()
	base := map[string]any{"schema": dataPackageSchema, "id": packageID, "attempt": 1}
	for k, v := range fields {
		base[k] = v
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("prWriteManifest: marshal: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(dataPackageManifestPath(system, packageID)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("prWriteManifest: mkdir: %v", err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatalf("prWriteManifest: write: %v", err)
	}
}

// prWriteReport writes a minimal, schema-tagged verification-report/v1
// report.json at the exact path RecordVerificationReport would have
// written it to, rooted under the VERIFYING system's own section (never
// the package's own producer — see mirrorPackageResolver's own doc
// comment).
func prWriteReport(t *testing.T, dir, verifyingSystem, packageID string, fields map[string]any) {
	t.Helper()
	base := map[string]any{
		"schema": dataVerificationReportSchema, "id": "VR-" + verifyingSystem + "-20260101-aaaa",
		"package": map[string]any{"id": packageID, "aggregate_digest": "sha256:deadbeef"},
		"result":  "pass",
		"checks": []map[string]any{
			{"id": "chk", "path": "dataset/records.json", "status": "pass", "violations": []map[string]any{}},
		},
	}
	for k, v := range fields {
		base[k] = v
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("prWriteReport: marshal: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(dataPackageReportPath(verifyingSystem, packageID)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("prWriteReport: mkdir: %v", err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatalf("prWriteReport: write: %v", err)
	}
}

func TestMirrorPackageResolver_ResolvePackage_ReadsManifestAtWriterPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	prWriteManifest(t, dir, "alpha", packageID, map[string]any{"attempt": 2})

	resolver := newMirrorPackageResolver(dir, []string{"alpha", "beta"})
	doc, ok := resolver.ResolvePackage(packageID)
	if !ok {
		t.Fatal("want manifest resolved")
	}
	if doc.ID != packageID || doc.Attempt != 2 {
		t.Fatalf("unexpected document: %+v", doc)
	}
}

func TestMirrorPackageResolver_ResolvePackage_MalformedIDIsNotFound(t *testing.T) {
	t.Parallel()
	resolver := newMirrorPackageResolver(t.TempDir(), nil)
	if _, ok := resolver.ResolvePackage("not-a-package-id"); ok {
		t.Fatal("want ok=false for a non-DP ref, never a panic or a partial document")
	}
}

func TestMirrorPackageResolver_ResolvePackage_MissingFileIsNotFound(t *testing.T) {
	t.Parallel()
	resolver := newMirrorPackageResolver(t.TempDir(), []string{"alpha"})
	if _, ok := resolver.ResolvePackage("DP-alpha-20260101-aaaa"); ok {
		t.Fatal("want ok=false when nothing is committed at the manifest path")
	}
}

func TestMirrorPackageResolver_ResolvePackage_MalformedManifestIsNotFoundNeverPanic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	full := filepath.Join(dir, filepath.FromSlash(dataPackageManifestPath("alpha", packageID)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := newMirrorPackageResolver(dir, []string{"alpha"})
	if _, ok := resolver.ResolvePackage(packageID); ok {
		t.Fatal("want ok=false for undecodable manifest bytes, never a panic")
	}
}

func TestMirrorPackageResolver_ResolvePackage_WrongSchemaIsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	prWriteManifest(t, dir, "alpha", packageID, map[string]any{"schema": "something-else/v1"})
	resolver := newMirrorPackageResolver(dir, []string{"alpha"})
	if _, ok := resolver.ResolvePackage(packageID); ok {
		t.Fatal("want ok=false when the decoded document's schema literal does not match data-package/v1")
	}
}

func TestMirrorPackageResolver_ResolvePackage_IDMismatchIsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	prWriteManifest(t, dir, "alpha", packageID, map[string]any{"id": "DP-alpha-20260101-zzzz"})
	resolver := newMirrorPackageResolver(dir, []string{"alpha"})
	if _, ok := resolver.ResolvePackage(packageID); ok {
		t.Fatal("want ok=false when the decoded document names a different id than requested")
	}
}

// TestMirrorPackageResolver_ResolveReport_SearchesEveryParticipant proves
// the load-bearing asymmetry this file's own doc comment names: the
// report is rooted under the VERIFYING (consumer) system's own section,
// never the package's producer section that ResolvePackage itself uses —
// so a report recorded by a THIRD participant (neither the package's own
// producer nor the first alphabetical participant) must still resolve.
func TestMirrorPackageResolver_ResolveReport_SearchesEveryParticipant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa" // producer: alpha
	prWriteReport(t, dir, "gamma", packageID, nil)

	resolver := newMirrorPackageResolver(dir, []string{"alpha", "beta", "gamma"})
	report, ok := resolver.ResolveReport(packageID)
	if !ok {
		t.Fatal("want report resolved from the verifying (gamma) section, not the producer's (alpha)")
	}
	if report.Package.ID != packageID {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestMirrorPackageResolver_ResolveReport_NoneRecordedIsNotFound(t *testing.T) {
	t.Parallel()
	resolver := newMirrorPackageResolver(t.TempDir(), []string{"alpha", "beta"})
	if _, ok := resolver.ResolveReport("DP-alpha-20260101-aaaa"); ok {
		t.Fatal("want ok=false, never treated as pass/fail, when no participant recorded a report")
	}
}

func TestMirrorPackageResolver_ResolveReport_MalformedIsNotFoundNeverPanic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	full := filepath.Join(dir, filepath.FromSlash(dataPackageReportPath("beta", packageID)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := newMirrorPackageResolver(dir, []string{"beta"})
	if _, ok := resolver.ResolveReport(packageID); ok {
		t.Fatal("want ok=false for undecodable report bytes, never a panic")
	}
}

func TestNewMirrorPackageResolver_ParticipantsSortedAndDeduplicated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const packageID = "DP-alpha-20260101-aaaa"
	// Two participants both record a report; deterministic sort means
	// "alpha" (sorts first) always wins the search, regardless of the
	// caller's own participant-slice order.
	prWriteReport(t, dir, "alpha", packageID, map[string]any{"id": "VR-alpha-20260101-aaaa"})
	prWriteReport(t, dir, "zulu", packageID, map[string]any{"id": "VR-zulu-20260101-aaaa"})

	resolver := newMirrorPackageResolver(dir, []string{"zulu", "", "alpha", "alpha", ""})
	if len(resolver.participants) != 2 || resolver.participants[0] != "alpha" || resolver.participants[1] != "zulu" {
		t.Fatalf("participants not sorted+deduped: %+v", resolver.participants)
	}
	report, ok := resolver.ResolveReport(packageID)
	if !ok || report.ID != "VR-alpha-20260101-aaaa" {
		t.Fatalf("want the alphabetically-first participant's report to win deterministically, got ok=%v report=%+v", ok, report)
	}
}
