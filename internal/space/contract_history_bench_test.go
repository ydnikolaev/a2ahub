package space

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// contractScaleFixture is a space whose first-parent history carries Events
// event-adding commits, Versions of which publish versions of contractTestID
// (the rest are other subjects' events, the realistic bulk of a space's log).
type contractScaleFixture struct {
	Repo     string
	Anchor   string
	Versions []string
}

// buildContractScaleRepo writes the whole history in ONE `git fast-import`
// stream, so a ten-thousand-commit fixture costs a couple of seconds rather
// than three git processes per commit. The filler events are spread over one
// hundred year directories so no single tree object grows with the history.
func buildContractScaleRepo(tb testing.TB, events, versions int) contractScaleFixture {
	tb.Helper()
	if versions < 1 || versions > events {
		tb.Fatalf("scale fixture: need 1 <= versions (%d) <= events (%d)", versions, events)
	}
	repo := newContractHistoryRepo(tb)
	var stream bytes.Buffer
	published := make([]string, 0, versions)
	spacing := events / versions
	mark := 0
	emit := func(message string, files map[string]string, order []string) {
		mark++
		fmt.Fprintf(&stream, "commit refs/heads/main\nmark :%d\ncommitter a-fixture <fixture@a2ahub.invalid> %d +0000\ndata %d\n%s\n",
			mark, 1_700_000_000+mark, len(message), message)
		if mark > 1 {
			fmt.Fprintf(&stream, "from :%d\n", mark-1)
		}
		for _, name := range order {
			fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n%s\n", name, len(files[name]), files[name])
		}
		stream.WriteString("\n")
	}
	for index := 0; index < events; index++ {
		remaining := events - index
		if len(published) < versions && (remaining <= versions-len(published) || index%spacing == spacing-1) {
			number := len(published)
			versionName := fmt.Sprintf("1.%d.0", number)
			descriptor, files, digest := contractV2Publication(tb, versionName, fmt.Sprintf("schema revision %d\n", number))
			eventID := "01K1A2B3C4D5E6F7G8H9J0K1" + contractScaleCrockford(number)
			eventPath := "axon/events/2026/" + eventID + ".yaml"
			event := strings.Replace(contractEventV2(versionName, digest), "01K1A2B3C4D5E6F7G8H9J0K1M7", eventID, 1)
			tree := map[string]string{contractTestRoot + "/contract.md": descriptor, eventPath: event}
			order := []string{contractTestRoot + "/contract.md"}
			for name, raw := range files {
				tree[contractTestRoot+"/"+name] = raw
				order = append(order, contractTestRoot+"/"+name)
			}
			order = append(order, eventPath)
			emit("publish "+versionName, tree, order)
			published = append(published, versionName)
			continue
		}
		eventPath := fmt.Sprintf("axon/events/%04d/filler-%05d.yaml", 1000+index/100, index)
		exchangePath := fmt.Sprintf("axon/exchange/%02d/f%05d.md", index%100, index)
		tree := map[string]string{
			eventPath:    fmt.Sprintf("schema: event/v1\nevent: filler-%05d\nspace: test\nsubject: XR-axon-20260801-%04x\ntransition: submit\nactor: {kind: agent, name: filler, system: axon}\nat: 2026-08-03T11:00:00Z\n", index, index),
			exchangePath: fmt.Sprintf("filler exchange %d\n", index),
		}
		emit(fmt.Sprintf("filler %d", index), tree, []string{eventPath, exchangePath})
	}

	cmd := exec.Command("git", gitfixture.Args("fast-import", "--quiet")...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	cmd.Stdin = &stream
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git fast-import: %v\n%s", err, out)
	}
	contractTestGit(tb, repo, "reset", "-q", "--hard", "main")
	contractTestGit(tb, repo, "push", "-q", "-u", "origin", "main")
	return contractScaleFixture{
		Repo:     repo,
		Anchor:   strings.TrimSpace(contractTestGitOutput(tb, repo, "rev-parse", "HEAD")),
		Versions: published,
	}
}

// contractScaleCrockford renders n as the two Crockford-base32 characters that
// end a 26-character event id, so every version gets a distinct valid id.
func contractScaleCrockford(n int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	return string([]byte{alphabet[(n/32)%32], alphabet[n%32]})
}

// BenchmarkContractPublicationHistory times one full publication-history read
// (the walk Preflight and Publish pay) over a space with ten thousand event
// commits and fourteen published versions, the shape a reporter measured at
// 1,222 s with 41,208 git processes before the walk was made one pass.
func BenchmarkContractPublicationHistory(b *testing.B) {
	const events, versions = 10_000, 14
	fixture := buildContractScaleRepo(b, events, versions)
	validator := contractHistoryValidator(b)
	var spawns *atomic.Int64
	b.ResetTimer()
	for b.Loop() {
		var counted context.Context
		counted, spawns = withContractGitSpawnCounter(b.Context())
		history, err := publicationHistoryAt(counted, fixture.Repo, fixture.Anchor, contractTestID, validator)
		if err != nil {
			b.Fatalf("publicationHistoryAt: %v", err)
		}
		if len(history) != versions {
			b.Fatalf("history has %d versions, want %d", len(history), versions)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(spawns.Load()), "git-spawns/op")
}
