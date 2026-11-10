package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	spacetemplate "github.com/ydnikolaev/a2ahub/space-template"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

func clientLandSpaceCaps() host.Capabilities {
	return host.Capabilities{NeedsCredential: true, LandsSynchronously: true}
}

func TestSpaceInitDeclaresLocalValidationAndOmitsServerEnforcement(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "out")
	cmd := cli.NewSpaceCommand(spacetemplate.Files, "9.9.9")
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--dir", target}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}

	raw, err := os.ReadFile(filepath.Join(target, "space.yaml"))
	if err != nil {
		t.Fatalf("read space.yaml: %v", err)
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the scaffolded manifest does not parse: %v\n%s", err, raw)
	}
	if got := manifest.EffectiveValidation(); got != contract.ValidationSettingLocal {
		t.Errorf("EffectiveValidation = %q, want local", got)
	}
	if got := strings.Count(string(raw), "\nvalidation:"); got != 1 {
		t.Errorf("validation key appears %d times:\n%s", got, raw)
	}
	for _, absent := range []string{".github/workflows", "BRANCH-PROTECTION.md"} {
		if _, err := os.Stat(filepath.Join(target, absent)); !os.IsNotExist(err) {
			t.Errorf("%s was scaffolded (stat err %v)", absent, err)
		}
	}
	for _, present := range []string{"CODEOWNERS", ".github/dependabot.yml", "README.md"} {
		if _, err := os.Stat(filepath.Join(target, present)); err != nil {
			t.Errorf("%s was not scaffolded: %v", present, err)
		}
	}
}

// TestSpaceInitLocalDoesNotDeclareValidation: a space on this disk has one way to
// land, so the key would state nothing.
func TestSpaceInitLocalDoesNotDeclareValidation(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	target := filepath.Join(root, "acme.git")
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--dir", target}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	show := exec.Command("git", gitfixture.Args("--git-dir="+target, "show", "HEAD:space.yaml")...)
	raw, err := show.Output()
	if err != nil {
		t.Fatalf("read the seeded manifest: %v", err)
	}
	if strings.Contains(string(raw), "validation:") {
		t.Errorf("a local space declared validation:\n%s", raw)
	}
}

// TestSpaceInitLeavesNothingForUpdateToDo is the invariant between the two verbs:
// what init writes for this mode is what update expects of it.
func TestSpaceInitLeavesNothingForUpdateToDo(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "out")
	initCmd := cli.NewSpaceCommand(spacetemplate.Files, "9.9.9")
	io, out, errOut := newSpaceIO()
	if code := initCmd.Run(context.Background(), []string{"init", "getvisa", "--dir", target}, io); code != 0 {
		t.Fatalf("init: code = %d; %s%s", code, out.String(), errOut.String())
	}

	cmd := &cli.SpaceCommand{
		TemplateFiles: spacetemplate.Files,
		Version:       "9.9.9",
		Funnel:        &fakeSubmitFunnel{},
		MirrorDir:     target,
		SpaceID:       "getvisa",
		OwnSystem:     "a2a-hub",
		HostCfg:       spaceUpdateHostCfg(),
		Caps:          clientLandSpaceCaps(),
		Scopes:        failingScopeChecker{t: t},
	}
	io, out, errOut = newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--check"}, io); code != 0 {
		t.Fatalf("update --check on a fresh init: code = %d; %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "already current") {
		t.Errorf("stdout = %q, want already current", out.String())
	}
}

func TestSpaceUpdateUnderLocalValidationPlansNoServerEnforcement(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	spaceUpdateSeedMirror(t, mirror)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirror, funnel, "9.9.9")
	cmd.Caps = clientLandSpaceCaps()
	cmd.Scopes = failingScopeChecker{t: t}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--approve"}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel calls = %d, want 1", len(funnel.calls))
	}
	for _, f := range funnel.calls[0].Files {
		if strings.HasPrefix(f.Path, ".github/workflows/") || f.Path == "BRANCH-PROTECTION.md" {
			t.Errorf("planned %s under validation: local", f.Path)
		}
	}
	for _, text := range []string{out.String(), funnel.calls[0].PRBody} {
		if strings.Contains(text, "gh api") || strings.Contains(text, "branch protection") || strings.Contains(text, "required_status_checks") {
			t.Errorf("printed a branch-protection directive under validation: local:\n%s", text)
		}
	}
}

// TestSpaceUpdateUnderLocalValidationNeedsApproval: no review stands behind this
// write, so `gates: default` reads as confirm and G4 is the operator's.
func TestSpaceUpdateUnderLocalValidationNeedsApproval(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	spaceUpdateSeedMirror(t, mirror)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirror, funnel, "9.9.9")
	cmd.Caps = clientLandSpaceCaps()
	cmd.Scopes = failingScopeChecker{t: t}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 1 {
		t.Fatalf("code = %d, want 1; %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "POL-029: G4 requires --approve") {
		t.Errorf("stderr = %q, want the G4 refusal", errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Errorf("funnel calls = %d after a refusal", len(funnel.calls))
	}
}

// TestSpaceUpdateOnASpaceThatKeepsActionsIsUnchanged is the regression half: a
// space without `validation: local` still gets its workflow and its checklist.
func TestSpaceUpdateOnASpaceThatKeepsActionsIsUnchanged(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	spaceUpdateSeedMirror(t, mirror)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirror, funnel, "9.9.9")
	cmd.Scopes = &fakeScopeChecker{scopes: []string{"repo", "workflow"}, reported: true}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel calls = %d, want 1", len(funnel.calls))
	}
	planned := map[string]bool{}
	for _, f := range funnel.calls[0].Files {
		planned[f.Path] = true
	}
	for _, want := range []string{".github/workflows/a2a-validate.yml", "BRANCH-PROTECTION.md"} {
		if !planned[want] {
			t.Errorf("%s dropped from the plan of a space that keeps actions; planned %v", want, planned)
		}
	}
}

// TestSpaceJoinRefusesOnAHostWhoseIdentityIsGitHubs is what `join` does here: the
// binary cannot mint a login, so a participant is added the way GitHub spaces
// add one, and the refusal says so.
func TestSpaceJoinRefusesOnAHostWhoseIdentityIsGitHubs(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirror, &fakeSubmitFunnel{}, "9.9.9")
	cmd.Caps = clientLandSpaceCaps()
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"join"}, io); code != 1 {
		t.Fatalf("code = %d, want 1; %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "onboarding manifest PR") {
		t.Errorf("stderr = %q, want the onboarding route named", errOut.String())
	}
}
