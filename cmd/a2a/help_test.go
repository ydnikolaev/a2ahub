package main

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// TestHelpForEveryVerbNeedsNoConfig is the standing gate over the DX defect
// an external consumer reported: `a2a contract publish --help` (and every
// other config-dependent verb) died on dependency resolution — "no project
// config", or a credential lookup — instead of explaining itself. Help is
// documentation; it must never require state.
//
// It drives EVERY buildCommands() key through the real dispatcher from a
// directory with NO .a2a/config.yaml, so a newly registered verb that
// resolves deps before parsing flags reds here.
func TestHelpForEveryVerbNeedsNoConfig(t *testing.T) {
	// Not parallel: t.Chdir mutates process state.
	t.Chdir(t.TempDir()) // no .a2a/config.yaml anywhere above a temp dir

	verbs := make([]string, 0, len(buildCommands()))
	for name := range buildCommands() {
		verbs = append(verbs, name)
	}
	sort.Strings(verbs)

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{verb, "--help"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("`a2a %s --help` exit = %d, want 0; stdout=%q stderr=%q", verb, code, stdout.String(), stderr.String())
			}
			if stdout.Len() == 0 {
				t.Fatalf("`a2a %s --help` printed nothing to stdout", verb)
			}
			// The failure mode being guarded: a dependency error instead of
			// usage. Those messages are unmistakable.
			for _, forbidden := range []string{"no project config", "cannot load", "cannot write", "no such file or directory", "credential unresolved"} {
				if strings.Contains(strings.ToLower(stdout.String()+stderr.String()), forbidden) {
					t.Fatalf("`a2a %s --help` leaked a setup error (%q): stdout=%q stderr=%q", verb, forbidden, stdout.String(), stderr.String())
				}
			}
		})
	}
}

// TestDataSubverbHelpNeedsNoConfig is spec 05 §8 row 17: `a2a data <sub> --help`
// for every DataSubcommands() entry explains itself from a directory with no
// project config. The verb-level test above stops at `data --help`; the
// sub-verb is where the service-not-configured refusal used to answer, and
// where the express flags (--for, --producer) must be named.
func TestDataSubverbHelpNeedsNoConfig(t *testing.T) {
	// Not parallel: t.Chdir mutates process state.
	t.Chdir(t.TempDir())

	mustName := map[string]string{"deliver": "--for", "verify": "--for", "request": "--producer"}
	for _, sub := range cli.DataSubcommands() {
		t.Run(sub.Name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"data", sub.Name, "--help"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("`a2a data %s --help` exit = %d, want 0; stdout=%q stderr=%q", sub.Name, code, stdout.String(), stderr.String())
			}
			out := stdout.String() + stderr.String()
			if strings.Contains(out, "service is not configured") {
				t.Fatalf("`a2a data %s --help` answered with the missing-service refusal: %q", sub.Name, out)
			}
			if !strings.Contains(stdout.String(), "a2a data "+sub.Name) {
				t.Errorf("`a2a data %s --help` usage does not name its own verb: %q", sub.Name, stdout.String())
			}
			if flag, ok := mustName[sub.Name]; ok && !strings.Contains(stdout.String(), flag) {
				t.Errorf("`a2a data %s --help` usage does not name %s: %q", sub.Name, flag, stdout.String())
			}
		})
	}
}

// TestHelpRequested covers the token grammar: the three spellings Go's own
// flag package accepts, and the `--` terminator after which a token is data.
// TestTopLevelHelp pins `a2a --help`, `-h`, `-help` and `a2a help`: each prints
// the same verb list bare `a2a` prints, to stdout, exit 0 (feedback
// fb-20261107-37ce55: they answered `unknown command`).
func TestTopLevelHelp(t *testing.T) {
	t.Chdir(t.TempDir())

	var want bytes.Buffer
	printUsage(&want)

	for _, args := range [][]string{{"--help"}, {"-h"}, {"-help"}, {"help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("a2a %v exit = %d, want 0; stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != want.String() {
			t.Errorf("a2a %v stdout = %q, want the bare usage %q", args, stdout.String(), want.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("a2a %v wrote to stderr: %q", args, stderr.String())
		}
	}

	var viaHelp, direct, stderr bytes.Buffer
	if code := run([]string{"help", "show"}, &viaHelp, &stderr); code != 0 {
		t.Fatalf("a2a help show exit = %d; stderr=%q", code, stderr.String())
	}
	run([]string{"show", "--help"}, &direct, &stderr)
	if viaHelp.String() != direct.String() {
		t.Errorf("`a2a help show` differs from `a2a show --help`:\n%q\n%q", viaHelp.String(), direct.String())
	}

	var out, errb bytes.Buffer
	if code := run([]string{"help", "nosuchverb"}, &out, &errb); code != 2 {
		t.Errorf("a2a help nosuchverb exit = %d, want 2", code)
	}
}

func TestHelpRequested(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args []string
		want bool
	}{
		"long":               {[]string{"--help"}, true},
		"short":              {[]string{"-h"}, true},
		"single-dash long":   {[]string{"-help"}, true},
		"after a sub-verb":   {[]string{"publish", "--help"}, true},
		"none":               {[]string{"publish", "--version", "1.0.0"}, false},
		"after a terminator": {[]string{"--", "--help"}, false},
		"empty":              {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := helpRequested(tc.args); got != tc.want {
				t.Fatalf("helpRequested(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
