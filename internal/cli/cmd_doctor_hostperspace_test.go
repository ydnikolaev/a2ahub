package cli

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// codeownersHostFake is a host that reads CODEOWNERS errors from a fixed list
// and records the repo it was asked about, so a test can see WHICH space's
// host answered.
type codeownersHostFake struct {
	host.Host
	errs []host.CodeownersError

	mu    sync.Mutex
	asked []host.Repo
}

func (f *codeownersHostFake) CodeownersErrors(_ context.Context, req host.RepoSettingsRequest) ([]host.CodeownersError, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, req.Repo)
	return f.errs, nil
}

// TestDoctor_HostPerSpace proves a capability row reads each connected space's
// OWN host through the injected opener: space "bad" reports CODEOWNERS errors,
// space "good" none, and each fake is asked about its own repo only.
func TestDoctor_HostPerSpace(t *testing.T) {
	t.Parallel()

	bad := &codeownersHostFake{Host: host.NewFakeHost(), errs: []host.CodeownersError{{Line: 3, Kind: "Unknown owner", Suggestion: "use a login"}}}
	good := &codeownersHostFake{Host: host.NewFakeHost()}
	byURL := map[string]host.Host{
		"https://github.com/acme/bad":  bad,
		"https://github.com/acme/good": good,
	}
	opened := map[string]int{}
	var mu sync.Mutex
	opener := func(raw string) (host.Host, host.Repo, error) {
		mu.Lock()
		opened[raw]++
		mu.Unlock()
		h, ok := byURL[raw]
		if !ok {
			// An unknown URL falls through to the production parse error.
			_, _, err := host.FixedOpener(nil)(raw)
			return nil, host.Repo{}, err
		}
		_, repo, err := host.FixedOpener(h)(raw)
		return h, repo, err
	}

	cmd := newTestDoctorCommand()
	cmd.open = opener
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "t"}, nil
	}
	cfg := space.ProjectConfig{Spaces: []space.Ref{
		{ID: "bad", RepoURL: "https://github.com/acme/bad"},
		{ID: "good", RepoURL: "https://github.com/acme/good"},
	}}

	ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
	if ok {
		t.Fatalf("a space with CODEOWNERS errors must fail the row; detail %q", detail)
	}
	if !strings.Contains(detail, "bad:") || strings.Contains(detail, "good:") {
		t.Errorf("only space bad may be named, got %q", detail)
	}
	if len(bad.asked) != 1 || bad.asked[0] != (host.Repo{Owner: "acme", Name: "bad"}) {
		t.Errorf("bad host asked %+v, want only acme/bad", bad.asked)
	}
	if len(good.asked) != 1 || good.asked[0] != (host.Repo{Owner: "acme", Name: "good"}) {
		t.Errorf("good host asked %+v, want only acme/good", good.asked)
	}

	// A second row over the same spaces reuses the opened hosts: one open per
	// space per run.
	_, _ = cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
	for raw, n := range opened {
		if n != 1 {
			t.Errorf("opener called %d times for %s, want once", n, raw)
		}
	}
}

// TestDoctor_HostPerSpace_OpenerErrorIsPerSpace pins that an opener failure for
// one space is reported on that space only, with the locator's own text.
func TestDoctor_HostPerSpace_OpenerErrorIsPerSpace(t *testing.T) {
	t.Parallel()

	good := &codeownersHostFake{Host: host.NewFakeHost()}
	cmd := newTestDoctorCommand()
	cmd.open = func(raw string) (host.Host, host.Repo, error) {
		if raw == "not-a-url" {
			_, _, err := host.FixedOpener(nil)(raw)
			return nil, host.Repo{}, err
		}
		return host.FixedOpener(good)(raw)
	}
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "t"}, nil
	}
	cfg := space.ProjectConfig{Spaces: []space.Ref{
		{ID: "broken", RepoURL: "not-a-url"},
		{ID: "good", RepoURL: "https://github.com/acme/good"},
	}}

	ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("an unverifiable space is an advisory, not a failure; detail %q", detail)
	}
	if !strings.Contains(detail, "unverified for broken") || strings.Contains(detail, "good") {
		t.Errorf("only space broken may be unverified, got %q", detail)
	}
	if len(good.asked) != 1 {
		t.Errorf("good host asked %d times, want 1", len(good.asked))
	}
}

// TestDoctor_NilOpenerSeesNoHost keeps the nil tolerance: with no opener wired
// every optional-capability row degrades to its global advisory, as a nil host
// did before.
func TestDoctor_NilOpenerSeesNoHost(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.open = nil
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "s", RepoURL: "https://github.com/acme/s"}}}
	ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
	if !ok || !strings.Contains(detail, "this build wires no GitHub CODEOWNERS reader") {
		t.Errorf("got ok=%v detail=%q, want the global advisory", ok, detail)
	}
}

// fullCapHost carries every optional capability the four network-backed rows
// read, answering "healthy" for each.
type fullCapHost struct {
	host.Host
}

func (fullCapHost) AutoMergeAllowed(context.Context, host.RepoSettingsRequest) (bool, error) {
	return true, nil
}

func (fullCapHost) ListOpenPRs(context.Context, host.ListOpenPRsRequest) ([]host.OpenPRSummary, error) {
	return nil, nil
}

func (fullCapHost) RefCheckStatus(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
	return host.CheckStatusResult{Name: "a2a-validate", State: "completed", Conclusion: "success"}, nil
}

func (fullCapHost) CodeownersErrors(context.Context, host.RepoSettingsRequest) ([]host.CodeownersError, error) {
	return nil, nil
}

// bareHost forwards the required host.Host methods through a NAMED field, so
// none of the wrapped host's optional methods are promoted: the capability is
// genuinely absent from the method set, as it would be on a host that has no
// such reader.
type bareHost struct {
	inner host.Host
}

func (b bareHost) PushBranch(ctx context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
	return b.inner.PushBranch(ctx, req)
}

func (b bareHost) OpenPR(ctx context.Context, req host.OpenPRRequest) (host.PRInfo, error) {
	return b.inner.OpenPR(ctx, req)
}

func (b bareHost) CheckStatus(ctx context.Context, req host.StatusRequest) (host.CheckStatusResult, error) {
	return b.inner.CheckStatus(ctx, req)
}

func (b bareHost) TokenScopes(ctx context.Context, cred host.Credential) ([]string, bool, error) {
	return b.inner.TokenScopes(ctx, cred)
}

func (b bareHost) ReviewStatus(ctx context.Context, req host.StatusRequest) (host.ReviewStatusResult, error) {
	return b.inner.ReviewStatus(ctx, req)
}

func (b bareHost) FindPRByHeadBranch(ctx context.Context, req host.FindPRRequest) (*host.PRInfo, error) {
	return b.inner.FindPRByHeadBranch(ctx, req)
}

func (b bareHost) Capabilities() host.Capabilities { return b.inner.Capabilities() }

// TestDoctor_MixedProject_ReaderlessSpaceIsUnverifiable pins the four
// capability rows over a MIXED project (space "alpha" has every optional
// reader, space "bare" has none): the readerless space is listed as
// unverifiable in the host's own words, never blamed on the token, and never
// silently healthy.
func TestDoctor_MixedProject_ReaderlessSpaceIsUnverifiable(t *testing.T) {
	t.Parallel()

	var (
		_ host.Host = bareHost{}
		_ host.Host = fullCapHost{}
	)
	// The fixture must really lack the capabilities.
	if _, ok := host.Host(bareHost{}).(host.RefStatusReader); ok {
		t.Fatal("bareHost must not implement host.RefStatusReader")
	}
	if _, ok := host.Host(bareHost{}).(host.OpenPRLister); ok {
		t.Fatal("bareHost must not implement host.OpenPRLister")
	}

	rows := []struct {
		name string
		run  func(*DoctorCommand, context.Context, space.ProjectConfig, space.MachineConfig) (bool, string)
		want string
	}{
		{"auto-merge", (*DoctorCommand).doctorCheckAutoMerge,
			"auto-merge unverified for bare: this host does not expose repo settings; cannot verify"},
		{"stuck-green", (*DoctorCommand).doctorCheckStuckGreenPRs,
			"stuck-green state unverified for bare: this host does not expose open pull requests; cannot verify"},
		{"default-branch", (*DoctorCommand).doctorCheckDefaultBranchHealthy,
			"default-branch health unverified for bare: this host does not expose ref check status; cannot verify"},
		{"codeowners", (*DoctorCommand).doctorCheckCodeownersResolvable,
			"CODEOWNERS unverified for bare: this host does not expose CODEOWNERS errors; cannot verify"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			byURL := map[string]host.Host{
				"https://github.com/acme/alpha": fullCapHost{Host: host.NewFakeHost()},
				"https://github.com/acme/bare":  bareHost{inner: host.NewFakeHost()},
			}
			cmd := newTestDoctorCommand()
			cmd.open = func(raw string) (host.Host, host.Repo, error) {
				return host.FixedOpener(byURL[raw])(raw)
			}
			cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
				return host.Credential{Token: "t"}, nil
			}
			cfg := space.ProjectConfig{Spaces: []space.Ref{
				{ID: "alpha", RepoURL: "https://github.com/acme/alpha"},
				{ID: "bare", RepoURL: "https://github.com/acme/bare"},
			}}

			ok, detail := row.run(cmd, context.Background(), cfg, space.MachineConfig{})
			if !ok {
				t.Fatalf("an unverifiable space is an advisory, not a failure; detail %q", detail)
			}
			if !strings.Contains(detail, row.want) {
				t.Errorf("detail %q does not contain %q", detail, row.want)
			}
			if strings.Contains(detail, "Repository metadata") {
				t.Errorf("a readerless host must not be blamed on the token: %q", detail)
			}
			if strings.Contains(detail, "unverified for alpha") || strings.Contains(detail, "unverified for alpha, ") {
				t.Errorf("the space WITH the reader must not be unverifiable: %q", detail)
			}
		})
	}
}
