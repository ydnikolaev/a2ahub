package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// errNotFound is the shape git actually returns when GitHub refuses a fetch,
// verbatim down to the two lines: the relayed `remote:` line is the server's
// own English, the `fatal:` line is git's translated one.
var errNotFound = errors.New(`git [fetch origin]: exit status 128: remote: Repository not found.
fatal: repository 'https://github.com/o/r/' not found`)

func TestGitRepositoryNotFoundMatchesTheServerLineNotGitsTranslatedOne(t *testing.T) {
	t.Parallel()

	if !gitRepositoryNotFound(errNotFound) {
		t.Fatal("the real GitHub not-found error was not recognised")
	}
	// git's own die() string is localized; the server's is not. A build that
	// only matched git's line would stop recognising this under a non-C
	// locale, which is exactly when an operator is least able to debug it.
	if !gitRepositoryNotFound(errors.New("remote: Repository not found.")) {
		t.Fatal("the server line alone must be sufficient")
	}
	if gitRepositoryNotFound(errors.New("git [fetch origin]: exit status 128: could not read Username")) {
		t.Fatal("an unrelated git failure must not be classified as not-found")
	}
	if gitRepositoryNotFound(nil) {
		t.Fatal("nil is not a failure")
	}
}

func TestDoctorPrivateRepoHint(t *testing.T) {
	t.Parallel()

	ref := space.Ref{ID: "getvisa", RepoURL: "https://github.com/o/getvisa.git"}
	withToken := host.Credential{Token: "test-token"}

	tests := []struct {
		name       string
		visibility host.RepositoryVisibility
		visErr     error
		credential host.Credential
		err        error
		wantAny    []string
		wantEmpty  bool
	}{
		{
			name:       "private repo names what the 404 actually was",
			visibility: host.RepositoryVisibilityPrivate,
			credential: withToken,
			err:        errNotFound,
			wantAny:    []string{"EXISTS and is private"},
		},
		{
			name:       "internal repo is treated the same as private",
			visibility: host.RepositoryVisibilityInternal,
			credential: withToken,
			err:        errNotFound,
			wantAny:    []string{"EXISTS and is internal"},
		},
		{
			name:       "no credential names the possibility and the fix without asserting it",
			credential: host.Credential{},
			err:        errNotFound,
			wantAny:    []string{"ran unauthenticated", "A2A_TOKEN_GETVISA"},
		},
		{
			// The repo is public and the API can read it, so the 404 came from
			// something else — a typo'd URL, a deleted repo. Inventing a
			// privacy explanation would send the operator the wrong way.
			name:       "public repo gets no hint",
			visibility: host.RepositoryVisibilityPublic,
			credential: withToken,
			err:        errNotFound,
			wantEmpty:  true,
		},
		{
			name:       "a non-not-found failure gets no hint",
			visibility: host.RepositoryVisibilityPrivate,
			credential: withToken,
			err:        errors.New("git [fetch origin]: exit status 128: could not resolve host"),
			wantEmpty:  true,
		},
		{
			name:       "an unreadable visibility stays silent rather than guessing",
			credential: withToken,
			visErr:     errors.New("api down"),
			err:        errNotFound,
			wantEmpty:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := &DoctorCommand{
				machineConfigPath: "/home/u/.config/a2a/config.yaml",
				h:                 doctorVisibilityHostFake{Host: host.NewFakeHost(), visibility: tc.visibility, err: tc.visErr},
			}
			got := cmd.doctorPrivateRepoHint(context.Background(), ref, tc.credential, tc.err)

			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("want no hint, got %q", got)
				}
				return
			}
			for _, want := range tc.wantAny {
				if !strings.Contains(got, want) {
					t.Fatalf("hint %q does not mention %q", got, want)
				}
			}
		})
	}
}

// TestDoctorSpaceAccessAttachesTheHintToTheFailingSpace proves the hint
// reaches the row an operator actually reads, not just the helper.
func TestDoctorSpaceAccessAttachesTheHintToTheFailingSpace(t *testing.T) {
	t.Parallel()

	cmd := &DoctorCommand{
		machineConfigPath: "/home/u/.config/a2a/config.yaml",
		h:                 doctorVisibilityHostFake{Host: host.NewFakeHost(), visibility: host.RepositoryVisibilityPrivate},
		resolveMirror:     func(string, space.Ref, space.MachineConfig) string { return t.TempDir() },
		resolveCredential: func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{Token: "test-token"}, nil
		},
		cloneOrFetch: func(context.Context, string, string, host.Credential) error { return errNotFound },
	}
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/o/getvisa.git"}}}

	ok, detail := cmd.doctorCheckSpaceAccess(context.Background(), cfg, space.MachineConfig{})
	if ok {
		t.Fatal("a failing fetch must fail the check")
	}
	if !strings.Contains(detail, "getvisa:") {
		t.Fatalf("detail does not name the space: %q", detail)
	}
	if !strings.Contains(detail, "EXISTS and is private") {
		t.Fatalf("detail relays GitHub's ambiguous 404 without the fact doctor already had: %q", detail)
	}
}
