package cli

import (
	"flag"
	"reflect"
	"testing"
)

// TestParseArgsAnyOrder is the direct, package-internal proof for Wave K's
// live run 6 finding ("thirteen verbs refuse a flag written after their
// positional argument"): parseArgsAnyOrder is the ONE shared mechanism
// every converted call site now uses instead of a bare fs.Parse(args), so
// this table proves the primitive itself — one row per call SHAPE the
// sweep actually found (flag kind x positional count), named after the
// concrete command it stands in for. The individual command-level tests
// (cmd_show_test.go's TestShowCommand_FlagAfterPositional and its
// siblings) additionally prove each real call site still invokes this
// correctly; this file proves the invariant they all rely on.
//
// TEETH: reverting parseArgsAnyOrder's own body (cli.go) to a bare
// `fs.Parse(args); return fs.Args(), err` reds every row below whose
// positionals precede its flag — the exact shape `a2a show <id> --json`
// needed and a bare fs.Parse(args) refuses (Go's flag package stops
// parsing at the first non-flag token).
func TestParseArgsAnyOrder(t *testing.T) {
	t.Parallel()

	type row struct {
		// name documents which real call site this shape stands in for.
		name string
		// register declares the flag(s) this call site declares and
		// returns a check func asserting they parsed to the expected
		// value — called once per parse.
		register func(fs *flag.FlagSet) func(t *testing.T)
		// flagTokens is the flag's own token(s) as written on the command
		// line, e.g. ["--json"] (bool) or ["--reason", "text"] (string).
		flagTokens  []string
		positionals []string
	}

	rows := []row{
		{
			name:        "show <ref> --json (ShowCommand.Run)",
			flagTokens:  []string{"--json"},
			positionals: []string{"XQ-axon-20260721-a001"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.Bool("json", false, "")
				return func(t *testing.T) {
					t.Helper()
					if !*v {
						t.Fatalf("--json did not parse to true")
					}
				}
			},
		},
		{
			name:        "thread <thread-id> --json (ThreadCommand.Run)",
			flagTokens:  []string{"--json"},
			positionals: []string{"TH-axon-thread1"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.Bool("json", false, "")
				return func(t *testing.T) {
					t.Helper()
					if !*v {
						t.Fatalf("--json did not parse to true")
					}
				}
			},
		},
		{
			name:        "search <query> --type question (SearchCommand.Run)",
			flagTokens:  []string{"--type", "question"},
			positionals: []string{"clarify pricing"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.String("type", "", "")
				return func(t *testing.T) {
					t.Helper()
					if *v != "question" {
						t.Fatalf("--type = %q, want %q", *v, "question")
					}
				}
			},
		},
		{
			name:        "validate <path> --author octocat (ValidateCommand.Run)",
			flagTokens:  []string{"--author", "octocat"},
			positionals: []string{".a2a/staging/XQ-axon-1.md"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.String("author", "", "")
				return func(t *testing.T) {
					t.Helper()
					if *v != "octocat" {
						t.Fatalf("--author = %q, want %q", *v, "octocat")
					}
				}
			},
		},
		{
			name:        "submit XQ-axon-1 XQ-axon-2 --batch (ResolveSubmitTargets)",
			flagTokens:  []string{"--batch"},
			positionals: []string{"XQ-axon-1", "XQ-axon-2"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.Bool("batch", false, "")
				return func(t *testing.T) {
					t.Helper()
					if !*v {
						t.Fatalf("--batch did not parse to true")
					}
				}
			},
		},
		{
			name:        "feedback validate <file> --ci (FeedbackCommand.runValidate)",
			flagTokens:  []string{"--ci"},
			positionals: []string{"fb-20260701-1a2b3c.yaml"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.Bool("ci", false, "")
				return func(t *testing.T) {
					t.Helper()
					if !*v {
						t.Fatalf("--ci did not parse to true")
					}
				}
			},
		},
		{
			name:        "ack XQ-1 XQ-2 --reason text (LifecycleCommand.Run, the shared batch verb)",
			flagTokens:  []string{"--reason", "not needed"},
			positionals: []string{"XQ-axon-20260721-a001", "XQ-axon-20260721-a002"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.String("reason", "", "")
				return func(t *testing.T) {
					t.Helper()
					if *v != "not needed" {
						t.Fatalf("--reason = %q, want %q", *v, "not needed")
					}
				}
			},
		},
		{
			name:        "respond XQ-1 --result answered (RespondCommand.Run)",
			flagTokens:  []string{"--result", "answered"},
			positionals: []string{"XQ-axon-20260721-r001"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.String("result", "", "")
				return func(t *testing.T) {
					t.Helper()
					if *v != "answered" {
						t.Fatalf("--result = %q, want %q", *v, "answered")
					}
				}
			},
		},
		{
			name:        "note XQ-1 --note text (NoteCommand.Run)",
			flagTokens:  []string{"--note", "context"},
			positionals: []string{"XQ-axon-20260721-n001"},
			register: func(fs *flag.FlagSet) func(t *testing.T) {
				v := fs.String("note", "", "")
				return func(t *testing.T) {
					t.Helper()
					if *v != "context" {
						t.Fatalf("--note = %q, want %q", *v, "context")
					}
				}
			},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()

			t.Run("flags_first (already legal before Wave K)", func(t *testing.T) {
				fs := flag.NewFlagSet("t", flag.ContinueOnError)
				check := r.register(fs)
				args := append(append([]string{}, r.flagTokens...), r.positionals...)
				got, err := parseArgsAnyOrder(fs, args)
				if err != nil {
					t.Fatalf("parseArgsAnyOrder(%v): %v", args, err)
				}
				if !reflect.DeepEqual(got, r.positionals) {
					t.Fatalf("positionals = %v, want %v", got, r.positionals)
				}
				check(t)
			})

			t.Run("positionals_first (what a bare fs.Parse(args) refused)", func(t *testing.T) {
				fs := flag.NewFlagSet("t", flag.ContinueOnError)
				check := r.register(fs)
				args := append(append([]string{}, r.positionals...), r.flagTokens...)
				got, err := parseArgsAnyOrder(fs, args)
				if err != nil {
					t.Fatalf("parseArgsAnyOrder(%v): %v", args, err)
				}
				if !reflect.DeepEqual(got, r.positionals) {
					t.Fatalf("positionals = %v, want %v", got, r.positionals)
				}
				check(t)
			})
		})
	}
}
