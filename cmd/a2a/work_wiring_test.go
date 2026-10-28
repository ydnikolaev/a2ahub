package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestExtractWorkSpaceRoutesWithoutLeakingConfigFlagToTransport(t *testing.T) {
	t.Parallel()
	selected, remaining, err := extractWorkSpace([]string{"status", "--space", "customer-ops", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if selected != "customer-ops" || !reflect.DeepEqual(remaining, []string{"status", "--json"}) {
		t.Fatalf("selected=%q remaining=%v", selected, remaining)
	}
	if _, _, err := extractWorkSpace([]string{"status", "--space=a", "--space=b"}); err == nil {
		t.Fatal("conflicting spaces accepted")
	}
}

func TestBuildMCPWorkDepsSharesCmdCompositionAndRoutesExplicitSpaceOffline(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths{projectRoot: root, projectConfig: filepath.Join(root, ".a2a", "config.yaml"), machineConfig: filepath.Join(root, "machine.yaml"), staging: filepath.Join(root, ".a2a", "staging")}
	refs := []space.Ref{{ID: "one", RepoURL: "https://github.com/acme/one.git"}, {ID: "two", RepoURL: "https://github.com/acme/two.git"}}
	deps, err := buildMCPWorkDeps(p, space.ProjectConfig{System: "axon", Spaces: refs}, space.MachineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one", "two"} {
		resolved, resolveErr := deps.ResolveSpace(want)
		if resolveErr != nil || resolved.Space != want {
			t.Fatalf("ResolveSpace(%q) = %q, %v", want, resolved.Space, resolveErr)
		}
	}
	spec, err := mcp.NewWorkTool(deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := spec.Handler(t.Context(), json.RawMessage(`{"action":"status"}`)); err == nil {
		t.Fatal("ambiguous space accepted")
	}
	if _, _, err := spec.Handler(t.Context(), json.RawMessage(`{"action":"status","space":"two"}`)); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if _, err := os.Stat(space.ResolveMirrorLocation(root, ref, space.MachineConfig{})); !os.IsNotExist(err) {
			t.Fatalf("mirror created for %s: %v", ref.ID, err)
		}
	}
}

// TestBuildMCPWorkDepsAMovedLocalSpaceCostsOnlyItself: a space whose kind the
// opener cannot tell (a local space whose directory was moved) does not fail
// the work deps, which would stop `a2a mcp` from starting. A request that
// reaches it gets its open error; the other space routes as before.
func TestBuildMCPWorkDepsAMovedLocalSpaceCostsOnlyItself(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths{projectRoot: root, projectConfig: filepath.Join(root, ".a2a", "config.yaml"), machineConfig: filepath.Join(root, "machine.yaml"), staging: filepath.Join(root, ".a2a", "staging")}
	moved := space.Ref{ID: "lone", RepoURL: filepath.Join(t.TempDir(), "moved.git")}
	deps, err := buildMCPWorkDeps(p, space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "one", RepoURL: "https://github.com/acme/one.git"}, moved}}, space.MachineConfig{})
	if err != nil {
		t.Fatalf("buildMCPWorkDeps with one moved space: %v", err)
	}
	if resolved, resolveErr := deps.ResolveSpace("one"); resolveErr != nil || resolved.Space != "one" {
		t.Fatalf(`ResolveSpace("one") = %q, %v`, resolved.Space, resolveErr)
	}
	if _, resolveErr := deps.ResolveSpace("lone"); !errors.Is(resolveErr, host.ErrSpacePathMissing) {
		t.Fatalf(`ResolveSpace("lone") = %v, want its open error (ErrSpacePathMissing)`, resolveErr)
	}
	if _, resolveErr := deps.ResolveSpace(""); resolveErr == nil || !strings.Contains(resolveErr.Error(), "space is required") {
		t.Fatalf(`ResolveSpace("") over two spaces = %v, want the ambiguity refusal`, resolveErr)
	}

	only, err := buildMCPWorkDeps(p, space.ProjectConfig{System: "axon", Spaces: []space.Ref{moved}}, space.MachineConfig{})
	if err != nil {
		t.Fatalf("buildMCPWorkDeps with only a moved space: %v", err)
	}
	if _, err := mcp.NewWorkTool(only); err != nil {
		t.Fatalf("NewWorkTool over only a moved space: %v", err)
	}
	if _, resolveErr := only.ResolveSpace(""); !errors.Is(resolveErr, host.ErrSpacePathMissing) {
		t.Fatalf(`ResolveSpace("") over the one moved space = %v, want its open error`, resolveErr)
	}
}

func TestConfiguredWorkSpaceRequiresUnambiguousBinding(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "one"}, {ID: "two"}}}
	if _, err := configuredWorkSpace(cfg, ""); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ambiguous error = %v", err)
	}
	ref, err := configuredWorkSpace(cfg, "two")
	if err != nil || ref.ID != "two" {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}
}

func TestBuildWorkCommandStatusIsOfflineAndLocalOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths{
		projectRoot: root, projectConfig: filepath.Join(root, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(root, "machine.yaml"), staging: filepath.Join(root, ".a2a", "staging"),
	}
	ref := space.Ref{ID: "customer-ops", RepoURL: "https://github.com/acme/customer-ops.git"}
	cmd, err := buildWorkCommand(p, space.ProjectConfig{System: "axon", Spaces: []space.Ref{ref}}, space.MachineConfig{}, ref)
	if err != nil {
		t.Fatalf("buildWorkCommand: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), []string{"status", "--json"}, cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != 0 || stdout.String() != "{\"items\":[]}\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	mirror := space.ResolveMirrorLocation(root, ref, space.MachineConfig{})
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Fatalf("status unexpectedly created/refreshed mirror %q: %v", mirror, err)
	}
}
