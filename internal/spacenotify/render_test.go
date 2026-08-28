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

	msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{
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

	msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0 (published class not in events)", len(msgs))
	}
}

// TestRender_Accounting_ZeroKeepIsDistinguishableFromZeroQualifying is
// ACs 8-9: a route that matched nothing (Qualified > 0, that route's own
// Kept == 0) must be distinguishable from a run over zero qualifying
// artifacts at all (Qualified == 0) — both are visible on the returned
// Accounting, never inferred from an absent line.
func TestRender_Accounting_ZeroKeepIsDistinguishableFromZeroQualifying(t *testing.T) {
	t.Parallel()

	// Stanza 1: one qualifying artifact, a route whose events exclude it
	// (the 2026-08-27 incident's own shape) — Qualified: 1, Kept: 0.
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-cccc.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-cccc", "type": "work_request",
		"title": "ordinary ask", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "no rush")
	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassHumanGate, ClassBlocking}},
	}, "axon", "seomatrix")

	_, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if acc.Qualified != 1 {
		t.Fatalf("Qualified = %d, want 1 (one artifact exists and qualifies)", acc.Qualified)
	}
	if len(acc.PerRoute) != 1 || acc.PerRoute[0].Kept != 0 {
		t.Fatalf("PerRoute = %+v, want exactly one route with Kept: 0", acc.PerRoute)
	}

	// Stanza 2: a genuinely empty space (no artifacts committed at all) —
	// Qualified: 0. Same route declaration; the SAME PerRoute shape (one
	// entry, Kept: 0) must still be present, but Qualified now says WHY.
	emptyFx := newRenderFixture(t, "axon", "seomatrix")
	emptyManifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassHumanGate, ClassBlocking}},
	}, "axon", "seomatrix")
	_, emptyAcc, err := Render(context.Background(), emptyFx.dir, emptyManifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render (empty space): %v", err)
	}
	if emptyAcc.Qualified != 0 {
		t.Fatalf("Qualified = %d, want 0 (no artifacts exist at all)", emptyAcc.Qualified)
	}
	if len(emptyAcc.PerRoute) != 1 || emptyAcc.PerRoute[0].Kept != 0 {
		t.Fatalf("PerRoute = %+v, want exactly one route with Kept: 0", emptyAcc.PerRoute)
	}

	// The two runs' PerRoute shapes are identical (Kept: 0 either way) —
	// Qualified is the ONLY field distinguishing "matched nothing" from
	// "nothing to match", which is exactly the property AC-9 asks for.
	if acc.Qualified == emptyAcc.Qualified {
		t.Fatalf("both stanzas report Qualified = %d — a zero-keep run is NOT distinguishable from a zero-qualifying run", acc.Qualified)
	}
}

// TestRender_Accounting_SelectorNarrowsKeptButNotQualified proves
// Accounting.Qualified counts candidates BEFORE per-route filtering (mode
// selection only), while PerRoute.Kept reflects the selector/events
// narrowing — a route whose selector excludes everything still reports the
// true (nonzero) Qualified count, not its own Kept count.
func TestRender_Accounting_SelectorNarrowsKeptButNotQualified(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-dddd.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-dddd", "type": "work_request",
		"title": "item", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")
	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished}},
	}, "axon", "seomatrix")

	_, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if acc.Qualified != 1 {
		t.Fatalf("Qualified = %d, want 1", acc.Qualified)
	}
	if len(acc.PerRoute) != 1 || acc.PerRoute[0].Kept != 1 {
		t.Fatalf("PerRoute = %+v, want Kept: 1 (events: [published] keeps the work_request)", acc.PerRoute)
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

			msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
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

	msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{
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

	msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
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
	a, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: now})
	if err != nil {
		t.Fatalf("Render (a): %v", err)
	}
	b, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: now})
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

// TestRender_OrderingSurvivesTheWidenedSelector is AC-14, re-run over
// P11's own widened selector: a route carrying a `kind:` selector
// (manifest.Raw, decoded via decodeSelectors) must still produce a message
// per artifact — each artifact still gets exactly ONE class — and the
// total order (class rank, then deadline, then id) is unchanged from a
// legacy `events:`-only route.
func TestRender_OrderingSurvivesTheWidenedSelector(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-e001.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-e001", "type": "work_request",
		"title": "blocking one", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p1", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitArtifact("axon/announcements/XA-axon-20260701-e002.md", map[string]any{
		"schema": "envelope/v1", "id": "XA-axon-20260701-e002", "type": "announcement",
		"title": "published one", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "classification": "internal",
	}, "body")

	raw := []byte(`
schema: space/v1
space: fixture-space
participants:
  - {system: axon, status: active}
  - {system: seomatrix, status: active}
notification_routes:
  - channel: telegram
    chat: "-100"
    events: [blocking, published]
    kind: [work_request, announcement]
`)
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	msgs, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2 (one message per artifact, kind: selector widened rather than narrowed the legacy events match)", len(msgs))
	}
	if len(acc.PerRoute) != 1 || acc.PerRoute[0].Kept != 2 {
		t.Fatalf("PerRoute = %+v, want Kept: 2", acc.PerRoute)
	}
	// Total order: blocking (classRank 1) sorts before published
	// (classRank 2) — the exact rule order.go already applies, unaffected
	// by the selector widening.
	if msgs[0].Class != ClassBlocking || msgs[1].Class != ClassPublished {
		t.Fatalf("order = [%s, %s], want [blocking, published]", msgs[0].Class, msgs[1].Class)
	}
	if msgs[0].Artifact.ID != "XW-axon-20260701-e001" || msgs[1].Artifact.ID != "XA-axon-20260701-e002" {
		t.Fatalf("unexpected artifact ids: %+v", msgs)
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

	msgs, _, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
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

// TestRender_KindSelectorExcludesViaRealManifestBytes is the negative,
// NARROWING half TestRender_OrderingSurvivesTheWidenedSelector does not
// cover: that test's `kind:` list names every kind already present, so it
// proves the selector does not spuriously DROP anything, but never proves
// the selector can actually EXCLUDE an artifact `events:` alone would have
// kept, decoded from real `space.ParseManifest` bytes (manifest.Raw) rather
// than a hand-built Selector passed directly to selectorMatches
// (selector_test.go's own unit tests). Both artifacts below reach class
// `published` (no gate, no priority, no blocking flag) and both satisfy
// `events: [published]`; only `kind: [announcement]` decides which one the
// route keeps.
func TestRender_KindSelectorExcludesViaRealManifestBytes(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-g001.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-g001", "type": "work_request",
		"title": "ordinary ask", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitArtifact("axon/announcements/XA-axon-20260701-g002.md", map[string]any{
		"schema": "envelope/v1", "id": "XA-axon-20260701-g002", "type": "announcement",
		"title": "published one", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "classification": "internal",
	}, "body")

	raw := []byte(`
schema: space/v1
space: fixture-space
participants:
  - {system: axon, status: active}
  - {system: seomatrix, status: active}
notification_routes:
  - channel: telegram
    chat: "-100"
    events: [published]
    kind: [announcement]
`)
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	msgs, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if acc.Qualified != 2 {
		t.Fatalf("Qualified = %d, want 2 (both artifacts qualify before per-route filtering)", acc.Qualified)
	}
	if len(acc.PerRoute) != 1 || acc.PerRoute[0].Kept != 1 {
		t.Fatalf("PerRoute = %+v, want exactly Kept: 1 — `kind: [announcement]` decoded from real manifest.Raw bytes must EXCLUDE the work_request `events: [published]` alone would have kept", acc.PerRoute)
	}
	if len(msgs) != 1 || msgs[0].Artifact == nil || msgs[0].Artifact.ID != "XA-axon-20260701-g002" {
		t.Fatalf("msgs = %+v, want exactly the announcement XA-axon-20260701-g002", msgs)
	}
}

// TestRender_Accounting_NoRoutesDeclared is spec 11 §6's "no routes at all"
// accounting edge case: a manifest with an empty notification_routes still
// reports the true Qualified count, with an empty (not nil-vs-populated
// ambiguous) PerRoute.
func TestRender_Accounting_NoRoutesDeclared(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-h001.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-h001", "type": "work_request",
		"title": "item", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")
	manifest := manifestWith("fixture-space", nil, "axon", "seomatrix")

	msgs, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if acc.Qualified != 1 {
		t.Fatalf("Qualified = %d, want 1 (the artifact still qualifies; there is simply no route to keep it)", acc.Qualified)
	}
	if len(acc.PerRoute) != 0 {
		t.Fatalf("PerRoute = %+v, want empty (no routes declared)", acc.PerRoute)
	}
	if len(msgs) != 0 {
		t.Fatalf("msgs = %+v, want none (no route to emit through)", msgs)
	}
}

// TestRender_Accounting_OneRouteKeepsAnotherDoesNot is spec 11 §6's "one
// route keeping and another not" accounting edge case: two declared routes
// over the SAME candidate set, one whose `events:` keeps the artifact and
// one whose `events:` excludes it — PerRoute must report each route's own
// Kept count independently, in manifest order.
func TestRender_Accounting_OneRouteKeepsAnotherDoesNot(t *testing.T) {
	t.Parallel()
	fx := newRenderFixture(t, "axon", "seomatrix")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-h002.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-h002", "type": "work_request",
		"title": "item", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": "p3", "blocking": false, "classification": "internal",
	}, "body")
	manifest := manifestWith("fixture-space", []space.NotificationRoute{
		{Channel: "telegram", Chat: "-100", Events: []string{ClassPublished}},
		{Channel: "telegram", Chat: "-200", Events: []string{ClassHumanGate, ClassBlocking}},
	}, "axon", "seomatrix")

	_, acc, err := Render(context.Background(), fx.dir, manifest, Options{Mode: ModeAll, Limit: 5, Now: time.Now()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if acc.Qualified != 1 {
		t.Fatalf("Qualified = %d, want 1", acc.Qualified)
	}
	if len(acc.PerRoute) != 2 {
		t.Fatalf("PerRoute = %+v, want exactly 2 entries (one per declared route)", acc.PerRoute)
	}
	if acc.PerRoute[0].Kept != 1 {
		t.Fatalf("PerRoute[0].Kept = %d, want 1 (chat -100's events: [published] keeps the work_request)", acc.PerRoute[0].Kept)
	}
	if acc.PerRoute[1].Kept != 0 {
		t.Fatalf("PerRoute[1].Kept = %d, want 0 (chat -200's events: [human-gate, blocking] excludes an ordinary p3 work_request)", acc.PerRoute[1].Kept)
	}
}
