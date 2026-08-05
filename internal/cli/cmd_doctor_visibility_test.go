package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type doctorClassificationSummaryFake struct {
	bySpace map[string]DoctorClassificationSummary
	err     error
}

func (f doctorClassificationSummaryFake) ClassificationSummary(_ context.Context, spaceID string) (DoctorClassificationSummary, error) {
	if f.err != nil {
		return DoctorClassificationSummary{}, f.err
	}
	return f.bySpace[spaceID], nil
}

type doctorVisibilityHostFake struct {
	host.Host
	visibility host.RepositoryVisibility
	err        error
}

func (f doctorVisibilityHostFake) RepoVisibility(context.Context, host.RepoSettingsRequest) (host.RepositoryVisibility, error) {
	return f.visibility, f.err
}

func TestDoctorVisibilityDecisionMatrix(t *testing.T) {
	t.Parallel()

	base := doctorVisibilityFacts{VisibilityVerified: true, ClassificationVerified: true}
	tests := []struct {
		name   string
		facts  doctorVisibilityFacts
		status doctorVisibilityStatus
		want   string
	}{
		{name: "public_public", facts: withDoctorVisibility(base, host.RepositoryVisibilityPublic, "public"), status: doctorVisibilityPASS, want: "transport matches classification"},
		{name: "public_internal", facts: withDoctorVisibility(base, host.RepositoryVisibilityPublic, "internal"), status: doctorVisibilityWARN, want: "public repository carries higher-classified material"},
		{name: "public_restricted", facts: withDoctorVisibility(base, host.RepositoryVisibilityPublic, "restricted"), status: doctorVisibilityWARN, want: "public repository carries higher-classified material"},
		{name: "private_public", facts: withDoctorVisibility(base, host.RepositoryVisibilityPrivate, "public"), status: doctorVisibilityPASS, want: "no observed mismatch"},
		{name: "private_internal", facts: withDoctorVisibility(base, host.RepositoryVisibilityPrivate, "internal"), status: doctorVisibilityPASS, want: "no observed mismatch"},
		{name: "internal_public", facts: withDoctorVisibility(base, host.RepositoryVisibilityInternal, "public"), status: doctorVisibilityPASS, want: "no observed mismatch"},
		{name: "internal_internal", facts: withDoctorVisibility(base, host.RepositoryVisibilityInternal, "internal"), status: doctorVisibilityPASS, want: "no observed mismatch"},
		{name: "private_restricted_without_proof", facts: withDoctorVisibility(base, host.RepositoryVisibilityPrivate, "restricted"), status: doctorVisibilityUNVERIFIED, want: "repository privacy alone does not prove intended pairwise audience"},
		{name: "internal_restricted_without_proof", facts: withDoctorVisibility(base, host.RepositoryVisibilityInternal, "restricted"), status: doctorVisibilityUNVERIFIED, want: "repository privacy alone does not prove intended pairwise audience"},
		{name: "private_restricted_with_proof", facts: func() doctorVisibilityFacts {
			facts := withDoctorVisibility(base, host.RepositoryVisibilityPrivate, "restricted")
			facts.BilateralProofSource = "authoritative bilateral audience registry"
			return facts
		}(), status: doctorVisibilityPASS, want: "authoritative bilateral audience registry"},
		{name: "internal_restricted_with_proof", facts: func() doctorVisibilityFacts {
			facts := withDoctorVisibility(base, host.RepositoryVisibilityInternal, "restricted")
			facts.BilateralProofSource = "authoritative bilateral audience registry"
			return facts
		}(), status: doctorVisibilityPASS, want: "authoritative bilateral audience registry"},
		{name: "visibility_unreadable", facts: doctorVisibilityFacts{Classification: "public", ClassificationVerified: true}, status: doctorVisibilityUNVERIFIED, want: "visibility could not be verified"},
		{name: "visibility_unsupported", facts: withDoctorVisibility(base, host.RepositoryVisibility("unsupported"), "public"), status: doctorVisibilityUNVERIFIED, want: "visibility could not be verified"},
		{name: "classification_unreadable", facts: doctorVisibilityFacts{Visibility: host.RepositoryVisibilityPrivate, VisibilityVerified: true}, status: doctorVisibilityUNVERIFIED, want: "classification summary could not be verified"},
		{name: "incomplete_scan", facts: doctorVisibilityFacts{
			Visibility: host.RepositoryVisibilityPrivate, VisibilityVerified: true,
			Classification: "internal", ClassificationVerified: true,
			SkippedCount: 2, SkippedPaths: []string{"beta/exchanges/XQ-bad.md", "axon/events/2026/bad.yaml"},
		}, status: doctorVisibilityUNVERIFIED, want: "2 skipped: axon/events/2026/bad.yaml, beta/exchanges/XQ-bad.md"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := doctorDecideVisibility(tc.facts)
			if got.Status != tc.status || !strings.Contains(got.Detail, tc.want) {
				t.Fatalf("doctorDecideVisibility(%+v) = %+v, want status %s containing %q", tc.facts, got, tc.status, tc.want)
			}
			lower := strings.ToLower(got.Detail)
			if strings.Contains(lower, "encrypt") || strings.Contains(lower, "access control") {
				t.Fatalf("visibility wording makes a confidentiality claim: %q", got.Detail)
			}
		})
	}
}

func withDoctorVisibility(base doctorVisibilityFacts, visibility host.RepositoryVisibility, classification string) doctorVisibilityFacts {
	base.Visibility = visibility
	base.Classification = classification
	return base
}

func TestDoctorVisibilityUnreadableOmitsProviderDetail(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.h = doctorVisibilityHostFake{
		Host: host.NewFakeHost(), err: errors.New("raw provider body with secret-token"),
	}
	cmd.ClassificationSummaryReader = doctorClassificationSummaryFake{bySpace: map[string]DoctorClassificationSummary{
		"getvisa": {Highest: "internal"},
	}}
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "test-token"}, nil
	}

	rows := cmd.doctorVisibilityRows(context.Background(), space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://example.invalid/org/getvisa.git",
	}}}, space.MachineConfig{})
	if len(rows) != 1 || rows[0].Status != doctorVisibilityUNVERIFIED {
		t.Fatalf("rows = %+v, want one UNVERIFIED row", rows)
	}
	if !strings.Contains(rows[0].Detail, "visibility could not be verified") {
		t.Fatalf("detail = %q", rows[0].Detail)
	}
	if strings.Contains(rows[0].Detail, "secret-token") || strings.Contains(rows[0].Detail, "raw provider") {
		t.Fatalf("provider detail leaked into stable output: %q", rows[0].Detail)
	}
}

func TestDoctorVisibilityWarnIsAdvisoryOnSuccess(t *testing.T) {
	t.Parallel()
	cmd := newDoctorVisibilityRunCommand(t, DoctorClassificationSummary{Highest: "internal"}, host.RepositoryVisibilityPublic)

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0 for advisory WARN; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := "repository visibility [getvisa]: WARN: public repository carries higher-classified material"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
	}
}

func TestDoctorVisibilityUnverifiedIsAdvisoryOnSuccess(t *testing.T) {
	t.Parallel()
	cmd := newDoctorVisibilityRunCommand(t, DoctorClassificationSummary{
		Highest: "restricted", SkippedCount: 1,
		SkippedPaths: []string{"axon/exchanges/XQ-unsafe.md"},
	}, host.RepositoryVisibilityPrivate)

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0 for advisory UNVERIFIED; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := "repository visibility [getvisa]: UNVERIFIED: classification scan incomplete · 1 skipped: axon/exchanges/XQ-unsafe.md"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
	}
}

func newDoctorVisibilityRunCommand(t *testing.T, summary DoctorClassificationSummary, visibility host.RepositoryVisibility) *DoctorCommand {
	t.Helper()
	dir := t.TempDir()
	writeDoctorSkippedFilesSpaceYAML(t, dir)
	workflow := filepath.Join(dir, ".github", "workflows", "a2a-validate.yml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte("name: a2a-validate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newTestDoctorCommand()
	cmd.h = doctorVisibilityHostFake{Host: host.NewFakeHost(), visibility: visibility}
	cmd.ClassificationSummaryReader = doctorClassificationSummaryFake{bySpace: map[string]DoctorClassificationSummary{
		"getvisa": summary,
	}}
	cmd.readFile = os.ReadFile
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return dir }
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "test-token"}, nil
	}
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/org/getvisa.git"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }
	return cmd
}
