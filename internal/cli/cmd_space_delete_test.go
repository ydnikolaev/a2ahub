package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// deleteManifest is a minimal local space.yaml naming id with the given
// participant rows (system -> status). It is parsed, never validated, by
// `space delete`.
func deleteManifest(id string, participants map[string]string) string {
	var b strings.Builder
	b.WriteString("schema: space/v1\nspace: " + id + "\nmin_binary_version: 0.27.0\ngates: confirm\nflow: express\n")
	if len(participants) == 0 {
		b.WriteString("participants: []\n")
		return b.String()
	}
	b.WriteString("participants:\n")
	for _, system := range []string{"axon", "beta", "gamma"} {
		status, ok := participants[system]
		if !ok {
			continue
		}
		b.WriteString("  - {system: " + system + ", org: local, section: " + system + ", owners: [local:" + system + "], status: " + status + ", joined: 2026-10-01}\n")
	}
	return b.String()
}

// deleteSpaceRepo creates a bare local space at <root>/<id>.git holding
// manifest, with extra commits stacked on main (git plumbing, so the land guard
// is not involved). marked controls the a2a.host=local marker, the only thing
// that tells a2a the repository is its own.
func deleteSpaceRepo(t *testing.T, root, id, manifest string, extraCommits int, marked bool) string {
	t.Helper()
	dir := filepath.Join(root, id+".git")
	if _, err := space.InitLocalSpaceRepo(context.Background(), dir, []space.FileWrite{{Path: "space.yaml", Content: []byte(manifest)}}); err != nil {
		t.Fatalf("InitLocalSpaceRepo: %v", err)
	}
	for range extraCommits {
		tree := spaceGit(t, dir, "rev-parse", "main^{tree}")
		parent := spaceGit(t, dir, "rev-parse", "main")
		commit := spaceGit(t, dir, "commit-tree", tree, "-p", parent, "-m", "later write")
		spaceGit(t, dir, "update-ref", "refs/heads/main", commit)
	}
	if marked {
		if err := host.PrepareLocalSpace(context.Background(), dir, space.LocalSpaceBaseBranch); err != nil {
			t.Fatalf("PrepareLocalSpace: %v", err)
		}
	}
	return dir
}

// deleteProject is a throwaway project (system, config, machine config, mirror
// cache) under temp dirs only.
type deleteProject struct {
	root, cfgPath, machinePath string
}

func newDeleteProject(t *testing.T, system string) deleteProject {
	t.Helper()
	root := t.TempDir()
	p := deleteProject{
		root:        root,
		cfgPath:     filepath.Join(root, ".a2a", "config.yaml"),
		machinePath: filepath.Join(root, "machine.yaml"),
	}
	writeProjectConfig(t, p.cfgPath, system)
	return p
}

// connect registers the local space at repo in the project, cloning its mirror.
func (p deleteProject) connect(t *testing.T, repo string) {
	t.Helper()
	cmd := cli.NewConnectCommand(p.cfgPath, p.machinePath, p.root)
	cmd.Open = localSpaceOpener()
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{repo}, io); code != 0 {
		t.Fatalf("connect %s: code = %d; stdout=%s stderr=%s", repo, code, out.String(), errOut.String())
	}
}

func (p deleteProject) refs(t *testing.T) []space.Ref {
	t.Helper()
	cfg, err := space.LoadProjectConfig(p.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Spaces
}

// command wires `space delete` the way cmd/a2a does, over temp dirs only: the
// production-shaped opener, an injected data root, and the project's own
// DisconnectCommand as the connection seam.
func (p deleteProject) command(t *testing.T, system string) *cli.SpaceCommand {
	t.Helper()
	cmd := newLocalSpaceCommand(t, filepath.Join(p.root, "data"))
	cmd.OwnSystem = system
	cmd.Disconnect = cli.NewDisconnectCommand(p.cfgPath, p.machinePath, p.root, cli.NewNoopCacheRemover())
	return cmd
}

func runDelete(cmd *cli.SpaceCommand, args ...string) (code int, stdout, stderr string) {
	io, out, errOut := newSpaceIO()
	code = cmd.Run(context.Background(), append([]string{"delete"}, args...), io)
	return code, out.String(), errOut.String()
}

// TestSpaceDelete_Refuses is AC-8: without --approve nothing is changed (the
// repository tree, the project's connection and its mirror are byte-identical
// afterwards), and the refusal lists the path, the commit count on main, every
// other participant not marked left, and the remedy.
func TestSpaceDelete_Refuses(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	repo := deleteSpaceRepo(t, root, "acme", deleteManifest("acme", map[string]string{"axon": "active", "beta": "active", "gamma": "left"}), 2, true)
	p := newDeleteProject(t, "axon")
	p.connect(t, repo)
	cmd := p.command(t, "axon")

	repoBefore, projectBefore := snapshotTree(t, repo), snapshotTree(t, p.root)
	code, stdout, stderr := runDelete(cmd, "--local", "acme")
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		repo,
		"3 commits on main",
		"beta",
		"a2a space delete --local acme --approve",
		"a2a disconnect acme",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	if strings.Contains(stderr, "gamma") {
		t.Errorf("stderr = %q, want the participant marked left not listed", stderr)
	}
	if strings.Contains(stderr, "axon") {
		t.Errorf("stderr = %q, want the caller's own system not listed as another participant", stderr)
	}
	if got := snapshotTree(t, repo); got != repoBefore {
		t.Errorf("the bare repository changed without --approve:\nbefore:\n%s\nafter:\n%s", repoBefore, got)
	}
	if got := snapshotTree(t, p.root); got != projectBefore {
		t.Errorf("the project (config, mirror) changed without --approve")
	}

	t.Run("no other participants: no disconnect advice", func(t *testing.T) {
		t.Parallel()
		root := canonicalTempDir(t)
		repo := deleteSpaceRepo(t, root, "solo", deleteManifest("solo", map[string]string{"axon": "active"}), 0, true)
		p := newDeleteProject(t, "axon")
		p.connect(t, repo)
		code, _, stderr := runDelete(p.command(t, "axon"), "--local", "solo")
		if code != 1 || !strings.Contains(stderr, "1 commit on main") || strings.Contains(stderr, "a2a disconnect") {
			t.Errorf("code = %d, stderr = %q, want 1, the commit count and no disconnect advice", code, stderr)
		}
	})
	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		p := newDeleteProject(t, "axon")
		cmd := p.command(t, "axon")
		for name, args := range map[string][]string{
			"no id":       {"--local"},
			"not --local": {"acme"},
			"extra arg":   {"acme", "--local", "stray"},
		} {
			if code, _, stderr := runDelete(cmd, args...); code != 2 || !strings.Contains(stderr, "space delete") {
				t.Errorf("%s: code = %d, stderr = %q, want 2 and a usage line", name, code, stderr)
			}
		}
	})
}

// TestSpaceDelete_Approve is AC-9: --approve removes the bare repository, the
// caller's config entry and its mirror, and names the disconnect each other
// participant still owes; a rerun exits 0 with "already deleted", and still
// disconnects a ref that remains.
func TestSpaceDelete_Approve(t *testing.T) {
	t.Parallel()
	t.Run("removes the repository, the entry and the mirror; the rerun is a no-op", func(t *testing.T) {
		t.Parallel()
		root := canonicalTempDir(t)
		repo := deleteSpaceRepo(t, root, "acme", deleteManifest("acme", map[string]string{"axon": "active", "beta": "active"}), 1, true)
		p := newDeleteProject(t, "axon")
		p.connect(t, repo)
		refs := p.refs(t)
		if len(refs) != 1 {
			t.Fatalf("precondition: Spaces = %+v, want one connection", refs)
		}
		mirror := space.ResolveMirrorLocation(p.root, refs[0], space.MachineConfig{})
		if _, err := os.Stat(filepath.Join(mirror, ".git")); err != nil {
			t.Fatalf("precondition: no mirror at %s: %v", mirror, err)
		}
		cmd := p.command(t, "axon")

		code, stdout, stderr := runDelete(cmd, "--local", "acme", "--approve")
		if code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
		}
		if _, err := os.Stat(repo); !os.IsNotExist(err) {
			t.Errorf("the bare repository %s is still there (stat err = %v)", repo, err)
		}
		if got := p.refs(t); len(got) != 0 {
			t.Errorf("Spaces = %+v, want the connection removed", got)
		}
		if _, err := os.Stat(mirror); !os.IsNotExist(err) {
			t.Errorf("the mirror %s is still there (stat err = %v)", mirror, err)
		}
		for _, want := range []string{"removed the local space \"acme\" at " + repo, "disconnected \"acme\"", "in beta's repository run `a2a disconnect acme`"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, want)
			}
		}

		code, stdout, stderr = runDelete(cmd, "--local", "acme", "--approve")
		if code != 0 || !strings.Contains(stdout, `space "acme" already deleted`) {
			t.Errorf("rerun: code = %d, stdout = %q, stderr = %q, want 0 and \"already deleted\"", code, stdout, stderr)
		}
	})
	t.Run("a rerun still disconnects a ref that remains", func(t *testing.T) {
		t.Parallel()
		root := canonicalTempDir(t)
		repo := deleteSpaceRepo(t, root, "acme", deleteManifest("acme", map[string]string{"axon": "active"}), 0, true)
		p := newDeleteProject(t, "axon")
		p.connect(t, repo)
		// The repository is gone but the connection is not: the state a delete
		// leaves when it is interrupted between its two steps.
		if err := os.RemoveAll(repo); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runDelete(p.command(t, "axon"), "--local", "acme", "--approve")
		if code != 0 || !strings.Contains(stdout, `space "acme" already deleted`) || !strings.Contains(stdout, "disconnected") {
			t.Errorf("code = %d, stdout = %q, stderr = %q, want 0, \"already deleted\" and a disconnect", code, stdout, stderr)
		}
		if got := p.refs(t); len(got) != 0 {
			t.Errorf("Spaces = %+v, want the remaining connection removed", got)
		}
	})
	t.Run("the data root default and no connection", func(t *testing.T) {
		t.Parallel()
		p := newDeleteProject(t, "axon")
		cmd := p.command(t, "axon")
		spaces := filepath.Join(p.root, "data", "spaces")
		if err := os.MkdirAll(spaces, 0o755); err != nil {
			t.Fatal(err)
		}
		repo := deleteSpaceRepo(t, spaces, "acme", deleteManifest("acme", nil), 0, true)
		if code, stdout, stderr := runDelete(cmd, "--local", "acme", "--approve"); code != 0 {
			t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
		}
		if _, err := os.Stat(repo); !os.IsNotExist(err) {
			t.Errorf("the repository under the data root %s is still there (stat err = %v)", repo, err)
		}
	})
	t.Run("a GitHub connection is refused", func(t *testing.T) {
		t.Parallel()
		p := newDeleteProject(t, "axon")
		cfg := "system: axon\nspaces:\n  - id: acme\n    repo_url: https://example.invalid/org/acme.git\n    mirror_location: acme\n"
		if err := os.WriteFile(p.cfgPath, []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runDelete(p.command(t, "axon"), "--local", "acme", "--approve")
		if code != 1 || !strings.Contains(stderr, "not a space on this machine") {
			t.Errorf("code = %d, stdout = %q, stderr = %q, want 1 and a refusal naming that it is not a local space", code, stdout, stderr)
		}
		if raw, err := os.ReadFile(p.cfgPath); err != nil || string(raw) != cfg {
			t.Errorf("the project config changed: %q (err %v)", raw, err)
		}
	})
}

// TestSpaceDelete_Foreign is AC-10: a bare repository without the
// a2a.host=local marker, one naming another space, and a directory that is not
// a repository are all refused and left byte-identical, even with --approve.
func TestSpaceDelete_Foreign(t *testing.T) {
	t.Parallel()
	type tc struct {
		name  string
		setup func(t *testing.T, root string) string
		want  string
	}
	for _, c := range []tc{
		{
			name: "no a2a.host=local marker",
			setup: func(t *testing.T, root string) string {
				return deleteSpaceRepo(t, root, "acme", deleteManifest("acme", nil), 0, false)
			},
			want: "a2a.host=local",
		},
		{
			name: "manifest names another id",
			setup: func(t *testing.T, root string) string {
				return deleteSpaceRepo(t, root, "acme", deleteManifest("other", nil), 0, true)
			},
			want: `names the space "other", not "acme"`,
		},
		{
			name: "not a bare repository",
			setup: func(t *testing.T, root string) string {
				dir := filepath.Join(root, "acme.git")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: "not a bare git repository",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := canonicalTempDir(t)
			dir := c.setup(t, root)
			before := snapshotTree(t, dir)
			p := newDeleteProject(t, "axon")
			code, stdout, stderr := runDelete(p.command(t, "axon"), "--local", "acme", "--dir", dir, "--approve")
			if code != 1 || !strings.Contains(stderr, c.want) {
				t.Errorf("code = %d, stdout = %q, stderr = %q, want 1 and a refusal containing %q", code, stdout, stderr, c.want)
			}
			if got := snapshotTree(t, dir); got != before {
				t.Errorf("the directory changed:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}
}
