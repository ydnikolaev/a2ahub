package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestDoctorFilterDataPackageReadmeSkips is the predicate-level proof for
// fb-20260812-f9cfac: a data package's own README.md
// ("<system>/data/<DP-id>/README.md") must never count against the
// classification scan's own completeness — its bytes are digest-bound
// package payload the tool itself wrote without frontmatter, on purpose.
func TestDoctorFilterDataPackageReadmeSkips(t *testing.T) {
	t.Parallel()

	t.Run("the packed README is removed and the count follows it down", func(t *testing.T) {
		t.Parallel()
		paths, count := doctorFilterDataPackageReadmeSkips(
			[]string{"seomatrix/data/DP-seomatrix-20260808-7xaa/README.md"}, 1)
		if len(paths) != 0 {
			t.Fatalf("paths = %v, want none", paths)
		}
		if count != 0 {
			t.Fatalf("count = %d, want 0", count)
		}
	})

	t.Run("a genuinely malformed artifact elsewhere is left alone", func(t *testing.T) {
		t.Parallel()
		paths, count := doctorFilterDataPackageReadmeSkips(
			[]string{"axon/exchanges/XQ-unsafe.md"}, 1)
		if len(paths) != 1 || paths[0] != "axon/exchanges/XQ-unsafe.md" {
			t.Fatalf("paths = %v, want the genuine skip left in place", paths)
		}
		if count != 1 {
			t.Fatalf("count = %d, want 1", count)
		}
	})

	t.Run("mixed: only the packed README is removed, the count reflects one removal", func(t *testing.T) {
		t.Parallel()
		paths, count := doctorFilterDataPackageReadmeSkips([]string{
			"seomatrix/data/DP-seomatrix-20260808-7xaa/README.md",
			"axon/exchanges/XQ-unsafe.md",
		}, 2)
		if len(paths) != 1 || paths[0] != "axon/exchanges/XQ-unsafe.md" {
			t.Fatalf("paths = %v, want only the genuine skip left in place", paths)
		}
		if count != 1 {
			t.Fatalf("count = %d, want 1", count)
		}
	})

	t.Run("count never goes negative even if it started under-reported", func(t *testing.T) {
		t.Parallel()
		paths, count := doctorFilterDataPackageReadmeSkips(
			[]string{"seomatrix/data/DP-seomatrix-20260808-7xaa/README.md"}, 0)
		if len(paths) != 0 {
			t.Fatalf("paths = %v, want none", paths)
		}
		if count != 0 {
			t.Fatalf("count = %d, want 0 (never negative)", count)
		}
	})
}

// TestDoctorVisibilityDataPackageReadmeIsNotUNVERIFIED is the end-to-end
// regression for fb-20260812-f9cfac: a classification summary whose ONLY
// skip is a data package's own packed README must reach a real verdict, not
// UNVERIFIED — and the "classification scan incomplete"/remediation line
// (which names an owner who cannot act on digest-bound bytes) must not
// print at all. Mirrors TestDoctorVisibilityUnverifiedIsAdvisoryOnSuccess's
// own shape (cmd_doctor_visibility_test.go) for the genuinely-skipped case
// this one is the counterpart of.
func TestDoctorVisibilityDataPackageReadmeIsNotUNVERIFIED(t *testing.T) {
	t.Parallel()
	cmd := newDoctorVisibilityRunCommand(t, DoctorClassificationSummary{
		Highest: "internal", SkippedCount: 1,
		SkippedPaths: []string{"seomatrix/data/DP-seomatrix-20260808-7xaa/README.md"},
	}, host.RepositoryVisibilityPrivate)

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("doctor exit = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := "repository visibility [getvisa]: PASS: no observed mismatch"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout missing %q, got a real verdict was expected:\n%s", want, stdout.String())
	}
	if strings.Contains(stdout.String(), "UNVERIFIED") || strings.Contains(stdout.String(), "classification scan incomplete") {
		t.Fatalf("stdout still carries the unactionable skip note:\n%s", stdout.String())
	}
}

// TestDoctorVisibilityRowsLocalSpaceIsNotApplicable is spec 03 §T1.4: a space on
// this machine is never shared, so its visibility row is n/a (a PASS saying why)
// and nothing is read for it. The n/a comes from the host's DECLARED
// OnThisMachine only; a space with no host stays UNVERIFIED, and a GitHub space
// is judged exactly as before.
func TestDoctorVisibilityRowsLocalSpaceIsNotApplicable(t *testing.T) {
	t.Parallel()
	const (
		ghURL    = "https://github.com/acme/hub"
		localURL = "/home/me/.local/share/a2a/spaces/loc.git"
	)
	cfg := space.ProjectConfig{Spaces: []space.Ref{
		{ID: "hub", RepoURL: ghURL},
		{ID: "loc", RepoURL: localURL},
		{ID: "odd", RepoURL: "https://github.com/onlyowner"},
	}}
	var credentialsFor, classifiedFor []string
	cmd := newTestDoctorCommand()
	cmd.open = func(raw string) (host.Host, host.Repo, error) {
		switch raw {
		case ghURL:
			return doctorVisibilityHostFake{Host: host.NewFakeHost(), visibility: host.RepositoryVisibilityPrivate}, host.Repo{Owner: "acme", Name: "hub"}, nil
		case localURL:
			return doctorLocalFake(), host.Repo{Owner: "spaces", Name: "loc"}, nil
		}
		return host.NewOpener(host.OpenConfig{})(raw)
	}
	cmd.resolveCredential = func(_ context.Context, envVar string, _ space.CredentialReference) (host.Credential, error) {
		credentialsFor = append(credentialsFor, envVar)
		return host.Credential{Token: "t"}, nil
	}
	cmd.ClassificationSummaryReader = doctorClassificationSummaryRecorder{record: &classifiedFor}

	rows := cmd.doctorVisibilityRows(context.Background(), cfg, space.MachineConfig{})
	got := map[string]doctorVisibilityDecision{}
	for _, row := range rows {
		got[row.SpaceID] = row.doctorVisibilityDecision
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want one per space", rows)
	}
	if want := (doctorVisibilityDecision{Status: doctorVisibilityPASS, Detail: "n/a: on this machine, never shared"}); got["loc"] != want {
		t.Errorf("local space row = %+v, want %+v", got["loc"], want)
	}
	if got["hub"].Status != doctorVisibilityPASS || got["hub"].Detail != "no observed mismatch" {
		t.Errorf("GitHub space row = %+v, want its own PASS unchanged", got["hub"])
	}
	if got["odd"].Status != doctorVisibilityUNVERIFIED {
		t.Errorf("a space with no host row = %+v, want UNVERIFIED, never n/a", got["odd"])
	}
	for _, id := range credentialsFor {
		if strings.Contains(id, "LOC") {
			t.Errorf("a credential was resolved for the local space (%s)", id)
		}
	}
	for _, id := range classifiedFor {
		if id == "loc" {
			t.Errorf("the local space's classification was read")
		}
	}
}

type doctorClassificationSummaryRecorder struct{ record *[]string }

func (r doctorClassificationSummaryRecorder) ClassificationSummary(_ context.Context, spaceID string) (DoctorClassificationSummary, error) {
	*r.record = append(*r.record, spaceID)
	return DoctorClassificationSummary{Highest: "internal"}, nil
}
