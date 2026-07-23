package main

import (
	"bytes"
	"sort"
	"strings"
	"testing"
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

// TestHelpRequested covers the token grammar: the three spellings Go's own
// flag package accepts, and the `--` terminator after which a token is data.
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
