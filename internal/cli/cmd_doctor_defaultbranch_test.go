package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// doctorHostWithoutRefReader satisfies host.Host by delegating to an
// embedded host.Host INTERFACE value (not a concrete *FakeHost) — Go only
// promotes the embedded interface's own method set, so RefCheckStatus is
// deliberately NOT promoted even though the FakeHost backing it now
// implements RefStatusReader. This is the same shape
// cmd_doctor_visibility_test.go's doctorVisibilityHostFake already uses to
// drive an "unwired capability" branch; it exists here because, unlike
// AutoMergeAllowed/CodeownersErrors, RefCheckStatus IS present on FakeHost
// once this phase adds it, so exercising "this build wires no ref status
// reader" needs a host that has genuinely never implemented it.
type doctorHostWithoutRefReader struct {
	host.Host
}

func TestDoctorCheckDefaultBranchHealthy(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}
	resolve := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	t.Run("no connected spaces vacuously passes", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), space.ProjectConfig{}, space.MachineConfig{})
		if !ok || detail != "" {
			t.Fatalf("zero spaces = (%v, %q), want (true, \"\")", ok, detail)
		}
	})

	t.Run("no wired ref status reader is an advisory PASS, never a FAIL", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.h = doctorHostWithoutRefReader{Host: host.NewFakeHost()}
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want advisory PASS, got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "unverified") {
			t.Fatalf("detail = %q, want the unverified note", detail)
		}
	})

	t.Run("a completed successful required check passes", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "success", Name: "a2a-validate"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		if ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{}); !ok {
			t.Fatalf("healthy default branch failed: %s", detail)
		}
	})

	t.Run("a completed failing required check FAILs, naming the space and conclusion", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want FAIL, got PASS")
		}
		if !strings.Contains(detail, "getvisa") {
			t.Fatalf("detail = %q, want the space named", detail)
		}
		if !strings.Contains(detail, "failure") {
			t.Fatalf("detail = %q, want the conclusion named", detail)
		}
	})

	t.Run("a completed non-success/non-failure conclusion (e.g. timed_out) still FAILs", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "timed_out", Name: "a2a-validate"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatalf("want FAIL on timed_out, got PASS: %s", detail)
		}
		if !strings.Contains(detail, "timed_out") {
			t.Fatalf("detail = %q, want the conclusion named", detail)
		}
	})

	t.Run("an in-flight run (not yet completed) is an advisory PASS, never a red", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "in_progress", Name: "a2a-validate"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want advisory PASS for an in-flight run, got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "still running") {
			t.Fatalf("detail = %q, want the in-flight advisory", detail)
		}
	})

	t.Run("a read error is unverifiable, not a FAIL", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{}, errors.New("forbidden")
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if !ok || !strings.Contains(detail, "unverified") || strings.Contains(detail, "forbidden") {
			t.Fatalf("unreadable state = (%v, %q), want a stable advisory that never interpolates the raw error", ok, detail)
		}
	})

	t.Run("no required check run matched is unverifiable, not a FAIL", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "queued"}, nil // Name == "" — CheckStatus's own "no match" shape
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if !ok || !strings.Contains(detail, "unverified") {
			t.Fatalf("no-match state = (%v, %q), want an unverified advisory", ok, detail)
		}
	})

	t.Run("credential resolution failure is unverifiable, not a FAIL", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.h = host.NewFakeHost()
		cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{}, errors.New("no credential configured")
		}
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		if !ok || !strings.Contains(detail, "unverified") {
			t.Fatalf("credential failure = (%v, %q), want an unverified advisory", ok, detail)
		}
	})

	t.Run("an unparsable repo URL is unverifiable, not a FAIL", func(t *testing.T) {
		t.Parallel()
		badCfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "bad", RepoURL: "not-a-url"}}}
		cmd := newTestDoctorCommand()
		cmd.h = host.NewFakeHost()
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), badCfg, space.MachineConfig{})
		if !ok || !strings.Contains(detail, "unverified") {
			t.Fatalf("unparsable URL = (%v, %q), want an unverified advisory", ok, detail)
		}
	})
}

// TestDoctorRunExitsNonZeroOnARedDefaultBranch is the acceptance-level proof:
// a genuinely red default branch must make `a2a doctor` exit 1 and print the
// failing conclusion — the exact defect fb-20260812-ee6dcd reported (fourteen
// PASS over a four-hour-red default branch).
func TestDoctorRunExitsNonZeroOnARedDefaultBranch(t *testing.T) {
	t.Parallel()
	fake := host.NewFakeHost()
	fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate"}, nil
	}

	cmd := newTestDoctorCommand()
	cmd.h = fake
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }
	// Hermetic: doctorCheckSpaceAccess would otherwise run a real git
	// clone/fetch against the space's mirror URL.
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 on a red default branch; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "default branch healthy: FAIL") {
		t.Fatalf("stdout = %q, want a FAIL line for \"default branch healthy\"", stdout.String())
	}
	if !strings.Contains(stdout.String(), "failure") {
		t.Fatalf("stdout = %q, want the failing conclusion named", stdout.String())
	}
}
