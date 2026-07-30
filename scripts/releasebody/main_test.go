package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunRendersAuthoredReleaseBody(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--version", "v0.16.1",
		"--verification", "50 of 50 declared live cells passed.",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code = %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"# v0.16.1 —",
		"## What changed",
		"## Release verification",
		"50 of 50 declared live cells passed.",
		"## Install or update",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("release body missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("successful run wrote stderr: %s", stderr.String())
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		nil,
		{"--version", "../../etc/passwd"},
		{"--version", "0.16.1", "extra"},
		{"--unknown"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Errorf("run(%q) code = %d, want 2; stderr: %s", args, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%q) wrote stdout on refusal: %s", args, stdout.String())
		}
	}
}

func TestRunRejectsVersionWithoutAuthoredNotes(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version", "99.0.0"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("run code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no authored release notes for 99.0.0") {
		t.Fatalf("missing actionable refusal: %s", stderr.String())
	}
}

func TestRunReportsStdoutFailure(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if code := run([]string{"--version", "0.16.1"}, strings.NewReader(""), failingWriter{}, &stderr); code != 1 {
		t.Fatalf("run code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write stdout") {
		t.Fatalf("missing stdout failure: %s", stderr.String())
	}
}

func TestRunVerifiesProviderNormalizedTrailingNewlines(t *testing.T) {
	t.Parallel()

	var rendered, renderErr bytes.Buffer
	if code := run([]string{"--version", "0.16.1"}, strings.NewReader(""), &rendered, &renderErr); code != 0 {
		t.Fatalf("render code = %d, stderr: %s", code, renderErr.String())
	}

	var stdout, stderr bytes.Buffer
	published := rendered.String() + "\r\n"
	if code := run(
		[]string{"--version", "0.16.1", "--verify-stdin"},
		strings.NewReader(published),
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("verify code = %d, stderr: %s", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("successful verify wrote output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRejectsPublishedBodyContentMismatch(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run(
		[]string{"--version", "0.16.1", "--verify-stdin"},
		strings.NewReader("different body\n"),
		&stdout,
		&stderr,
	); code != 1 {
		t.Fatalf("verify code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "differs from the authored corpus") {
		t.Fatalf("missing mismatch diagnostic: %s", stderr.String())
	}
}

func TestRunBoundsPublishedBody(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	oversized := strings.NewReader(strings.Repeat("x", maxPublishedBodyBytes+1))
	if code := run(
		[]string{"--version", "0.16.1", "--verify-stdin"},
		oversized,
		&stdout,
		&stderr,
	); code != 1 {
		t.Fatalf("verify code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exceeds") {
		t.Fatalf("missing size diagnostic: %s", stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("closed")
}
