package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadRemoteRecoveryCommitReturnsCompleteFirstParentDiff(t *testing.T) {
	t.Parallel()
	fixture := newRemoteRecoveryFixture(t)
	writeRecoveryFile(t, fixture.source, "changed.txt", []byte("before\n"))
	writeRecoveryFile(t, fixture.source, "deleted.txt", []byte("delete me\n"))
	fixture.commitAll("base")
	fixture.push("main")

	writeRecoveryFile(t, fixture.source, "changed.txt", []byte("after\n"))
	if err := os.Remove(filepath.Join(fixture.source, "deleted.txt")); err != nil {
		t.Fatalf("remove deleted.txt: %v", err)
	}
	writeRecoveryFile(t, fixture.source, "extra/evidence.json", []byte("{\"extra\":true}\n"))
	fixture.commitAll("recover\n\nbody")
	head := gitOutput(t, fixture.source, "rev-parse", "HEAD")
	fixture.push("a2a/atlas/recover-1")

	repo, branch, oid, parent, message, authorName, authorEmail, digests, exists, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
		context.Background(), fixture.probe, fixture.remote, Repo{Owner: "acme", Name: "space"},
		"a2a/atlas/recover-1", Credential{},
	)
	if err != nil {
		t.Fatalf("ReadRemoteRecoveryCommit: %v", err)
	}
	if !exists || repo != (Repo{Owner: "acme", Name: "space"}) || branch != "a2a/atlas/recover-1" {
		t.Fatalf("identity = (%+v, %q, exists=%v)", repo, branch, exists)
	}
	if oid != head || oid != strings.ToLower(oid) {
		t.Fatalf("head = %q, want exact lowercase %q", oid, head)
	}
	if wantParent := gitOutput(t, fixture.source, "rev-parse", "HEAD^"); parent != wantParent {
		t.Fatalf("parent = %q, want exact %q", parent, wantParent)
	}
	if message != "recover\n\nbody\n\n" {
		t.Fatalf("message = %q", message)
	}
	if authorName != "a2a-test" || authorEmail != "test@a2ahub.invalid" {
		t.Fatalf("author = %q <%s>", authorName, authorEmail)
	}
	want := map[string]RemoteRecoveryChange{
		"changed.txt": {
			BeforeDigest: recoveryTestDigest([]byte("before\n")), BeforeMode: "100644",
			AfterDigest: recoveryTestDigest([]byte("after\n")), AfterMode: "100644",
		},
		"deleted.txt": {
			BeforeDigest: recoveryTestDigest([]byte("delete me\n")), BeforeMode: "100644",
		},
		"extra/evidence.json": {
			AfterDigest: recoveryTestDigest([]byte("{\"extra\":true}\n")), AfterMode: "100644",
		},
	}
	if len(digests) != len(want) {
		t.Fatalf("digests = %#v, want complete set %#v", digests, want)
	}
	for filePath, change := range want {
		if digests[filePath] != change {
			t.Errorf("change[%q] = %+v, want %+v", filePath, digests[filePath], change)
		}
	}
	if refs := gitOutput(t, fixture.probe, "for-each-ref", "--format=%(refname)", "refs/a2a/recovery"); refs != "" {
		t.Fatalf("private recovery ref leaked: %q", refs)
	}
}

func TestReadRemoteRecoveryCommitAbsentBranch(t *testing.T) {
	t.Parallel()
	fixture := newRemoteRecoveryFixture(t)
	repo, branch, oid, parent, message, authorName, authorEmail, digests, exists, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
		context.Background(), fixture.probe, fixture.remote, Repo{Owner: "acme", Name: "space"},
		"a2a/atlas/absent", Credential{},
	)
	if err != nil {
		t.Fatalf("ReadRemoteRecoveryCommit: %v", err)
	}
	if exists || repo != (Repo{Owner: "acme", Name: "space"}) || branch != "a2a/atlas/absent" ||
		oid != "" || parent != "" || message != "" || authorName != "" || authorEmail != "" || digests != nil {
		t.Fatalf("absent result = (%+v, %q, %q, %q, %q, %q, %q, %#v, %v)", repo, branch, oid, parent, message, authorName, authorEmail, digests, exists)
	}
}

func TestReadRemoteRecoveryCommitRefusesRootAndMergeCommits(t *testing.T) {
	t.Parallel()
	t.Run("root", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "root.txt", []byte("root\n"))
		fixture.commitAll("root")
		fixture.push("root")
		assertRecoveryReadFails(t, fixture, "root")
	})
	t.Run("merge", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		runGit(t, fixture.source, nil, "branch", "side")
		writeRecoveryFile(t, fixture.source, "main.txt", []byte("main\n"))
		fixture.commitAll("main")
		runGit(t, fixture.source, nil, "switch", "side")
		writeRecoveryFile(t, fixture.source, "side.txt", []byte("side\n"))
		fixture.commitAll("side")
		runGit(t, fixture.source, nil, "switch", "main")
		runGit(t, fixture.source, recoveryGitIdentity(), "merge", "--no-ff", "-m", "merge", "side")
		fixture.push("merge")
		assertRecoveryReadFails(t, fixture, "merge")
	})
}

func TestReadRemoteRecoveryCommitExpandsRenameAndRefusesOversizedEvidence(t *testing.T) {
	t.Parallel()
	t.Run("rename", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "before.txt", []byte("same bytes\n"))
		fixture.commitAll("base")
		fixture.push("main")
		if err := os.Rename(filepath.Join(fixture.source, "before.txt"), filepath.Join(fixture.source, "after.txt")); err != nil {
			t.Fatalf("rename: %v", err)
		}
		fixture.commitAll("rename")
		fixture.push("rename")
		_, _, _, _, _, _, _, changes, exists, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
			context.Background(), fixture.probe, fixture.remote, Repo{Owner: "acme", Name: "space"},
			"rename", Credential{},
		)
		if err != nil || !exists {
			t.Fatalf("read rename: exists=%v err=%v", exists, err)
		}
		want := map[string]RemoteRecoveryChange{
			"before.txt": {BeforeDigest: recoveryTestDigest([]byte("same bytes\n")), BeforeMode: "100644"},
			"after.txt":  {AfterDigest: recoveryTestDigest([]byte("same bytes\n")), AfterMode: "100644"},
		}
		if !reflect.DeepEqual(changes, want) {
			t.Fatalf("rename changes = %#v, want delete+write %#v", changes, want)
		}
	})
	t.Run("oversized blob", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		fixture.push("main")
		writeRecoveryFile(t, fixture.source, "large.bin", bytes.Repeat([]byte{'x'}, maxRecoveryBlobBytes+1))
		fixture.commitAll("large")
		fixture.push("large")
		assertRecoveryReadFails(t, fixture, "large")
	})
}

func TestReadRemoteRecoveryCommitReportsExecutableModeAndRefusesSymlink(t *testing.T) {
	t.Parallel()

	t.Run("executable", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		fixture.push("main")
		writeRecoveryFile(t, fixture.source, "tool.sh", []byte("#!/bin/sh\n"))
		if err := os.Chmod(filepath.Join(fixture.source, "tool.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		fixture.commitAll("tool")
		fixture.push("tool")
		_, _, _, _, _, _, _, changes, exists, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
			context.Background(), fixture.probe, fixture.remote, Repo{Owner: "acme", Name: "space"}, "tool", Credential{},
		)
		if err != nil || !exists || changes["tool.sh"].AfterMode != "100755" {
			t.Fatalf("executable evidence = %#v exists=%v err=%v", changes, exists, err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		fixture.push("main")
		if err := os.Symlink("base.txt", filepath.Join(fixture.source, "link.txt")); err != nil {
			t.Fatal(err)
		}
		fixture.commitAll("link")
		fixture.push("link")
		assertRecoveryReadFails(t, fixture, "link")
	})
}

func TestReadRemoteRecoveryCommitRefusesMalformedOrTooManyPaths(t *testing.T) {
	t.Parallel()
	t.Run("non utf8 path", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		fixture.push("main")
		blob := strings.TrimSuffix(string(gitInputOutput(t, fixture.source, nil, []byte("bad\n"), "hash-object", "-w", "--stdin")), "\n")
		treeInput := gitInputOutput(t, fixture.source, nil, nil, "ls-tree", "-z", "HEAD^{tree}")
		treeInput = append(treeInput, []byte("100644 blob "+blob+"\tbad")...)
		treeInput = append(treeInput, 0xff, 0)
		tree := strings.TrimSuffix(string(gitInputOutput(t, fixture.source, nil, treeInput, "mktree", "-z")), "\n")
		parent := gitOutput(t, fixture.source, "rev-parse", "HEAD")
		commit := strings.TrimSuffix(string(gitInputOutput(
			t, fixture.source, recoveryGitIdentity(), nil, "commit-tree", tree, "-p", parent, "-m", "bad path",
		)), "\n")
		runGit(t, fixture.source, nil, "push", "--force", fixture.remote, commit+":refs/heads/bad-path")
		assertRecoveryReadFails(t, fixture, "bad-path")
	})
	t.Run("more than 256 paths", func(t *testing.T) {
		t.Parallel()
		fixture := newRemoteRecoveryFixture(t)
		writeRecoveryFile(t, fixture.source, "base.txt", []byte("base\n"))
		fixture.commitAll("base")
		fixture.push("main")
		for i := 0; i <= maxRecoveryChangedPaths; i++ {
			writeRecoveryFile(t, fixture.source, filepath.Join("many", leftPadRecoveryNumber(i)+".txt"), []byte("x"))
		}
		fixture.commitAll("many")
		fixture.push("many")
		assertRecoveryReadFails(t, fixture, "many")
	})

	for name, raw := range map[string][]byte{
		"duplicate": []byte("A\x00a\x00M\x00a\x00"),
		"oversized": append(append([]byte("A\x00"), bytes.Repeat([]byte{'a'}, maxRecoveryPathBytes+1)...), 0),
		"type":      []byte("T\x00a\x00"),
	} {
		t.Run("parser "+name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseRecoveryDiff(raw); err == nil {
				t.Fatal("parseRecoveryDiff unexpectedly accepted malformed input")
			}
		})
	}
}

func TestReadRemoteRecoveryCommitNeverLeaksCredential(t *testing.T) {
	t.Parallel()
	token := "recovery-secret-token"
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	probe := filepath.Join(t.TempDir(), "probe.git")
	runGit(t, "", nil, "init", "--bare", probe)
	_, _, _, _, _, _, _, _, _, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
		context.Background(), probe, filepath.Join(t.TempDir(), "missing.git"),
		Repo{Owner: "acme", Name: "space"}, "recovery", Credential{Token: token},
	)
	if err == nil || !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("error = %v, want ErrRequestFailed", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), encoded) || strings.Contains(strings.ToLower(err.Error()), "authorization") {
		t.Fatalf("credential leaked in error: %v", err)
	}
}

type remoteRecoveryFixture struct {
	t      *testing.T
	remote string
	source string
	probe  string
}

func newRemoteRecoveryFixture(t *testing.T) remoteRecoveryFixture {
	t.Helper()
	root := t.TempDir()
	fixture := remoteRecoveryFixture{
		t: t, remote: filepath.Join(root, "remote.git"),
		source: filepath.Join(root, "source"), probe: filepath.Join(root, "probe.git"),
	}
	runGit(t, "", nil, "init", "--bare", fixture.remote)
	runGit(t, "", nil, "init", "-b", "main", fixture.source)
	runGit(t, "", nil, "init", "--bare", fixture.probe)
	return fixture
}

func (f remoteRecoveryFixture) commitAll(message string) {
	f.t.Helper()
	runGit(f.t, f.source, nil, "add", "-A")
	runGit(f.t, f.source, recoveryGitIdentity(), "commit", "-m", message)
}

func (f remoteRecoveryFixture) push(branch string) {
	f.t.Helper()
	runGit(f.t, f.source, nil, "push", "--force", f.remote, "HEAD:refs/heads/"+branch)
}

func assertRecoveryReadFails(t *testing.T, fixture remoteRecoveryFixture, branch string) {
	t.Helper()
	_, _, _, _, _, _, _, _, _, err := NewGitHubHost(nil, "").ReadRemoteRecoveryCommit(
		context.Background(), fixture.probe, fixture.remote, Repo{Owner: "acme", Name: "space"}, branch, Credential{},
	)
	if err == nil || !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("ReadRemoteRecoveryCommit error = %v, want ErrRequestFailed", err)
	}
}

func writeRecoveryFile(t testing.TB, root, relative string, content []byte) {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %q: %v", full, err)
	}
}

func recoveryGitIdentity() []string {
	return []string{
		"GIT_AUTHOR_NAME=a2a-test", "GIT_AUTHOR_EMAIL=test@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-test", "GIT_COMMITTER_EMAIL=test@a2ahub.invalid",
	}
}

func gitOutput(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output.String())
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func gitInputOutput(t testing.TB, dir string, env []string, input []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...)
}

func recoveryTestDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func leftPadRecoveryNumber(value int) string {
	if value < 10 {
		return "00" + string(rune('0'+value))
	}
	if value < 100 {
		return "0" + string(rune('0'+value/10)) + string(rune('0'+value%10))
	}
	return string(rune('0'+value/100)) + string(rune('0'+value/10%10)) + string(rune('0'+value%10))
}
