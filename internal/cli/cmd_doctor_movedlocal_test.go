package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestDoctorRunOnAMovedLocalSpaceNeitherDemandsATokenNorPasses is the whole
// doctor run over a local space whose directory was moved away, through the
// PRODUCTION opener. Before host.Open told "the path is gone" apart from "an
// unmarked repository", the space opened as GitHub: credentials demanded
// A2A_TOKEN_ACME, space access printed git's raw error with no local hint,
// and the three local rows and space policy printed PASS over a space nobody
// read.
func TestDoctorRunOnAMovedLocalSpaceNeitherDemandsATokenNorPasses(t *testing.T) {
	t.Parallel()
	ref, dir := doctorRealLocalSpace(t, "acme")
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Fatal(err)
	}

	cmd := doctorLocalCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{System: "axon", Spaces: []space.Ref{ref}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }
	// The production resolver's own refusal: if any row asks for a credential,
	// the env var it names reaches stdout and the assertion below sees it.
	cmd.resolveCredential = func(_ context.Context, envVar string, _ space.CredentialReference) (host.Credential, error) {
		return host.Credential{}, errors.New("env var " + envVar + " is not set")
	}
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error {
		return errors.New("fatal: '" + dir + "' does not appear to be a git repository")
	}

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	out := stdout.String()
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (space access FAILs); stdout:\n%s", code, out)
	}

	lines := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, rest, ok := strings.Cut(line, ": "); ok {
			lines[name] = rest
		}
	}
	wantPrefix := map[string]string{
		"credentials":          "UNVERIFIED: acme: cannot tell whether this space needs a credential",
		"space access":         "FAIL: acme: ",
		"local land guard":     "UNVERIFIED: acme: the space's host could not be opened",
		"local history shape":  "UNVERIFIED: acme: the space's host could not be opened",
		"local space writable": "UNVERIFIED: acme: the space's host could not be opened",
		"space policy":         "UNVERIFIED: acme: host not opened",
	}
	for row, want := range wantPrefix {
		got, present := lines[row]
		if !present {
			t.Errorf("no %q row in stdout:\n%s", row, out)
			continue
		}
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want it to start with %q", row, got, want)
		}
	}
	access := lines["space access"]
	for _, want := range []string{"does not exist", "restore it at " + dir, "a moved local space breaks the write journals"} {
		if !strings.Contains(access, want) {
			t.Errorf("space access %q lacks %q", access, want)
		}
	}
	if strings.Contains(out, "A2A_TOKEN_") {
		t.Errorf("a row demanded a GitHub credential for a moved local space:\n%s", out)
	}
}
