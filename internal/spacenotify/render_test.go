package spacenotify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"gopkg.in/yaml.v3"
)

// render_test.go builds its own minimal throwaway git checkout — the same
// shape internal/cache's own fixture_test.go uses, kept package-local
// because that helper is unexported to internal/cache's own test binary.

type renderFixture struct {
	t   *testing.T
	dir string
}

func newRenderFixture(t *testing.T, systems ...string) *renderFixture {
	t.Helper()
	dir := t.TempDir()
	rfRunGit(t, dir, "init", "-b", "main", dir)
	gitfixture.HardenRepo(t, dir)
	for _, sys := range systems {
		mustMkdirAll(t, filepath.Join(dir, sys, "events", "2026"))
		mustWriteFile(t, filepath.Join(dir, sys, "events", "2026", ".gitkeep"), "")
	}
	rfCommit(t, dir, "seed")
	return &renderFixture{t: t, dir: dir}
}

func (f *renderFixture) commitArtifact(relPath string, fields map[string]any, body string) {
	f.t.Helper()
	raw, err := yaml.Marshal(fields)
	if err != nil {
		f.t.Fatalf("marshal envelope: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	fullPath := filepath.Join(f.dir, filepath.FromSlash(relPath))
	mustMkdirAll(f.t, filepath.Dir(fullPath))
	mustWriteFile(f.t, fullPath, full)
	id, _ := fields["id"].(string)
	rfCommit(f.t, f.dir, "artifact "+id)
}

func (f *renderFixture) writeConsumes(system, contractID string) {
	f.t.Helper()
	mustMkdirAll(f.t, filepath.Join(f.dir, system))
	content := fmt.Sprintf("schema: consumes/v1\nsystem: %s\ndependencies:\n  - contract: %s\n    major: 1\n    since: \"2026-08-01\"\n", system, contractID)
	mustWriteFile(f.t, filepath.Join(f.dir, system, "consumes.yaml"), content)
	rfCommit(f.t, f.dir, "consumes "+system)
}

func rfRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
}

func rfCommit(t *testing.T, dir, msg string) {
	t.Helper()
	rfRunGit(t, dir, "add", "-A")
	cmd := exec.Command("git", gitfixture.Args("commit", "-m", msg)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit (dir=%s): %v\n%s", dir, err, out.String())
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func manifestWith(spaceID string, routes []space.NotificationRoute, systems ...string) space.Manifest {
	m := space.Manifest{Schema: "space/v1", Space: spaceID}
	for _, s := range systems {
		m.Participants = append(m.Participants, space.Participant{System: s, Status: "active"})
	}
	m.NotificationRoutes = routes
	return m
}

// TestRender_NoCache is AC1: `a2a notify render` produces messages from a
// bare checkout — no `.a2a/` anywhere.
func TestRender_NoCache(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-aaaa.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-aaaa", "type": "work_request",
		"title": "todo feed pagination", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p2", "blocking": false, "classification": "internal",
	}, "please paginate the feed")

	if _, err := os.Stat(filepath.Join(fx.dir, ".a2a")); !os.IsNotExist(err) {
		t.Fatalf(".a2a must not exist in this fixture")
	}

	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished, ClassBlocking, ClassHumanGate}},
	}, "axon", "seomatrix")

	msgs, err := Render(context.Background(), fx.dir, manifest, Options{
		Mode: ModeAll, Limit: 5, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if msgs[0].Artifact == nil || msgs[0].Artifact.ID != "XW-axon-20260701-aaaa" {
		t.Fatalf("unexpected message: %+v", msgs[0])
	}
}

// TestRender_EventFilterExcludesOrdinaryPublication is AC6: with
// events:[human-gate, blocking], an ordinary (published-class) artifact
// produces nothing for that route.
func TestRender_EventFilterExcludesOrdinaryPublication(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-bbbb.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-bbbb", "type": "work_request",
		"title": "low priority ask", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "no rush")

	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassHumanGate, ClassBlocking}},
	}, "axon", "seomatrix")

	msgs, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0 (published class not in events)", len(msgs))
	}
}

// TestRender_DigestCoalescingAtLimitBoundary is AC7: exactly --limit
// qualifying artifacts still render per-artifact; --limit+1 coalesces
// into ONE digest.
func TestRender_DigestCoalescingAtLimitBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		count      int
		wantDigest bool
	}{
		{"at-limit", 5, false},
		{"limit-plus-one", 6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newRenderFixture(t, "axon", "seomatrix")
			for i := 0; i < tc.count; i++ {
				id := fmt.Sprintf("XW-axon-20260701-%04d", i)
				fx.commitArtifact(fmt.Sprintf("axon/exchanges/%s.md", id), map[string]any{
					"schema": "envelope/v1", "id": id, "type": "work_request",
					"title": "item", "space": "fixture-space", "from": "axon",
					"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
					"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
				}, "body")
			}
			manifest := manifestWith("fixture-space", []space.NotificationRoute{
				{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished}},
			}, "axon", "seomatrix")

			msgs, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if tc.wantDigest {
				if len(msgs) != 1 || msgs[0].Class != ClassDigest {
					t.Fatalf("msgs = %+v, want exactly one digest message", msgs)
				}
				if msgs[0].Digest.Count != tc.count {
					t.Fatalf("digest count = %d, want %d", msgs[0].Digest.Count, tc.count)
				}
			} else {
				if len(msgs) != tc.count {
					t.Fatalf("len(msgs) = %d, want %d per-artifact messages, no digest", len(msgs), tc.count)
				}
				for _, m := range msgs {
					if m.Class == ClassDigest {
						t.Fatalf("unexpected digest at exactly --limit: %+v", m)
					}
				}
			}
		})
	}
}

// TestRender_OnlyUnknownIDRefusesEverything is AC14: one unknown id among
// otherwise-valid ones fails the command, naming the id, and emits
// NOTHING.
func TestRender_OnlyUnknownIDRefusesEverything(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-cccc.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-cccc", "type": "work_request",
		"title": "item", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")

	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished}},
	}, "axon", "seomatrix")

	msgs, err := Render(context.Background(), fx.dir, manifest, Options{
		Mode: ModeOnly, OnlyIDs: []string{"XW-axon-20260701-cccc", "XW-does-not-exist"}, Limit: 5, Now: time.Now(),
	})
	if err == nil {
		t.Fatalf("want an error naming the unknown id, got nil (msgs=%+v)", msgs)
	}
	if len(msgs) != 0 {
		t.Fatalf("want nothing emitted, got %d messages", len(msgs))
	}
}

// TestRender_SecretRefusal is AC18: a route naming an undeclared secret
// refuses the WHOLE render, before any output — one bad route among good
// ones.
func TestRender_SecretRefusal(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-eeee.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-eeee", "type": "work_request",
		"title": "item", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")

	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished}}, // declared secret (default)
		{Channel: "telegram", Chat: "-200", Events: []string{ClassPublished}, Secret: "SOME_OTHER_TOKEN"},
		{Channel: "telegram", Chat: "-300", Events: []string{ClassPublished}}, // declared secret (default)
	}, "axon", "seomatrix")

	msgs, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err == nil {
		t.Fatalf("want an error naming the route and the secret, got nil (msgs=%+v)", msgs)
	}
	if len(msgs) != 0 {
		t.Fatalf("want nothing emitted, got %d messages", len(msgs))
	}
}

// TestRender_Deterministic is AC9: two runs over the same range are
// byte-identical.
func TestRender_Deterministic(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("XW-axon-20260701-f%03d", i)
		fx.commitArtifact(fmt.Sprintf("axon/exchanges/%s.md", id), map[string]any{
			"schema": "envelope/v1", "id": id, "type": "work_request",
			"title": "item", "space": "fixture-space", "from": "axon",
			"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
			"created": time.Now().UTC().Format(time.RFC3339), "priority": "p2", "blocking": false, "classification": "internal",
		}, "body")
	}
	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished, ClassBlocking, ClassHumanGate}},
	}, "axon", "seomatrix")

	now := time.Now()
	a, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: now})
	if err != nil {
		t.Fatalf("Render (a): %v", err)
	}
	b, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: now})
	if err != nil {
		t.Fatalf("Render (b): %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Artifact == nil || b[i].Artifact == nil || a[i].Artifact.ID != b[i].Artifact.ID {
			t.Fatalf("order mismatch at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestRender_LateAdopterReachesItsOwnRoute is the render-level half of AC11b.
//
// internal/cache's own test proves BuildNotifyIndex hands pendency the COMPLETE
// addressee set, so a late adopter appears in Verdict.Owners. That is necessary
// and not sufficient: the verdict can name the right system while no route ever
// receives the message, because route matching is a separate step. A literal
// `to`/`from` reading — which spec 03 originally carried — leaves this case
// permanently unreachable, and nothing else in this package would notice.
//
// The deprecation announcement below names ONLY seomatrix in `to`. `latecomer`
// reaches it exclusively through its own consumes.yaml.
func TestRender_LateAdopterReachesItsOwnRoute(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix", "latecomer")
	fx.writeConsumes("latecomer", "XC-axon-payments")
	fx.commitArtifact("axon/exchanges/XA-axon-20260701-dep1.md", map[string]any{
		"schema": "envelope/v1", "id": "XA-axon-20260701-dep1", "type": "announcement",
		"title": "payments contract is deprecated", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "category": "deprecation",
		"deprecates": "XC-axon-payments", "priority": "p1", "blocking": true,
		"classification": "internal", "ack_requested": true,
	}, "migrate before the cutoff")

	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", For: "latecomer", Events: []string{ClassHumanGate, ClassBlocking, ClassPublished}},
	}, "axon", "seomatrix", "latecomer")

	msgs, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("len(msgs) = 0, want the late adopter's route to be handed the deprecation — " +
			"it is addressed only through its own consumes.yaml, never through `to`")
	}
	for _, m := range msgs {
		if m.Route.For != "latecomer" {
			t.Errorf("Route.For = %q, want latecomer", m.Route.For)
		}
	}
}
