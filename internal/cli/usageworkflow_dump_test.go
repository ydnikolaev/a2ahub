package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/skill"
)

// usageworkflow_dump_test.go is scripts/check-usage-workflow.sh's own "ask
// the program" entry point (the check-render-ledger.sh precedent:
// cmd_contract_p6_test.go's TestRenderLedgerSurfaceDump). The gate cannot
// duplicate this extraction in bash — answers-that-hold-2026-08 spec 08 T1
// forbids finding the workflow lines by grepping a fixed prefix over file
// bytes ("a string that does not match the pattern escapes silently — the
// same defect class as a gate whose regex was inert for weeks") — so the
// walk has to be real Go source analysis, and the only place that can run
// is inside this module.
//
// THE 2026-08-29 WIDENING (spec 08's own amendment): the universe is no
// longer "verb name == manifest section id" (that rule yielded exactly
// {feedback, notify, notifications} and missed the phase's own motivating
// incident, `contract publish`). It is now, per verb V drawn from the
// binary's own `a2a __catalog` roster:
//
//	{ section id of every loopCorpus page whose text names `a2a V` }
//	  UNION
//	{ the section id equal to V, when one exists }
//
// which means this file now answers TWO different questions, both fed by
// the SAME AST walk over the SAME source directory:
//
//  1. usageWorkflowExtract: the flat, order-independent SET of topics
//     passed to workflowLine(...) anywhere in the walked source — UNCHANGED
//     since before the widening, and still what the topic-driven check
//     (every extracted topic is a real section AND accepted by `a2a docs`)
//     reads.
//
//  2. usageWorkflowExtractPairs: WHICH VERB each workflowLine(...) call
//     belongs to — new for the widening, because the old universe never
//     needed it (a call's own topic argument WAS the verb name). Two
//     attribution mechanisms, because the real tree has two shapes of
//     usage text:
//
//     MECHANISM 1 — an individually-authored verb whose own usage string is
//     a literal (or a package-level `const` it resolves through) starting
//     with the anchor "usage: a2a ". The verb is the LONGEST catalogue verb
//     matching immediately after that anchor, boundary-checked so a shorter
//     verb is never credited from a longer one it happens to prefix — e.g.
//     "contract" must not be credited by text naming only "contract
//     publish" (spec 08's own named trap), and "verify" must not be
//     credited by text naming "verify-pass"/"verify-fail" (the same trap,
//     hyphen-joined rather than space-joined, and a REAL collision in this
//     catalogue — see usageWorkflowIsIdentByte). The scan is anchored on
//     "usage: a2a ", not any "a2a V" mention: a cross-reference like
//     "see `a2a inbox`" inside the same function must not attribute a
//     DIFFERENT verb's workflowLine call to "inbox".
//
//     MECHANISM 2 — a table-driven command shared by several verbs (e.g.
//     cmd_lifecycle.go's LifecycleCommand.Run, printed with a runtime
//     "usage: a2a %s ..." Sprintf) has NO per-verb literal usage text to
//     anchor on. Its own workflowLine call cannot even be resolved to a
//     topic if the argument is a runtime field expression (e.g.
//     `c.spec.WorkflowTopic`) — the extractor deliberately refuses to
//     evaluate that (fail-closed, same rule AC-6 already states). The fix
//     is a package-level `map[string]string` literal keyed by the verb name
//     itself, e.g. `map[string]string{"ack": workflowLine("loop-receive")}`
//     — a workflowLine call whose nearest enclosing *ast.KeyValueExpr has a
//     STRING-LITERAL key (true of a map literal's keys, never of a struct
//     literal's field keys, which are *ast.Ident) attributes to that key
//     directly, verified against the catalogue so a typo'd key fails loudly
//     rather than silently going unattributed.
//
// Mechanism 2 takes precedence over mechanism 1 within the same scope (it
// cannot both live inside a map literal AND be attributed by surrounding
// usage text). An unresolvable scope — no anchor match and no forcing key —
// drops the call from PAIRS but the topic still surfaces in the flat set
// (usageWorkflowExtract), so the topic-driven check still polices it; a
// scope naming MORE THAN ONE distinct verb via mechanism 1 is refused
// outright (ambiguous attribution is worse than none).
//
// usageWorkflowHelperName is the ONE call the walk looks for. cli.go's
// workflowLine is this package's only place that composes the
// "workflow: ..." text; every verb whose usage output should carry it calls
// that function by name, so a call expression naming it is exactly the
// signal to collect — never a literal "workflow: " substring, which a
// Synopsis string or an unrelated comment could carry by coincidence.
const usageWorkflowHelperName = "workflowLine"

// usageWorkflowAnchor is mechanism 1's anchor text. A literal must contain
// this exact substring for the text immediately following it to be
// considered a verb mention for attribution purposes — never a bare "a2a V"
// occurrence anywhere in the scope (see this file's own doc comment).
const usageWorkflowAnchor = "usage: a2a "

// usageWorkflowIsIdentByte reports whether b can appear inside, or extend,
// a dispatch-verb token: letters, digits, underscore, and hyphen (a
// hyphenated verb like verify-pass/verify-fail is ONE catalogue token, not
// two). It is the single definition of "word boundary" this file uses, in
// both directions a longest-match candidate can go wrong: matched but
// immediately followed by one of these bytes means the REAL token in the
// text is longer than the candidate (a2a verify is a byte-for-byte prefix
// of a2a verify-pass), so the candidate must be rejected there even though
// the bytes up to that point are equal.
func usageWorkflowIsIdentByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_', b == '-':
		return true
	default:
		return false
	}
}

// usageWorkflowLongestMatch returns the longest string in candidates that
// matches text starting at pos, followed by a real word boundary (the end
// of text, or a byte usageWorkflowIsIdentByte rejects) — never a bare
// substring test. A candidate may itself contain a single interior space (a
// catalogue sub-verb row like "contract publish"); matching still requires
// an exact byte match of the WHOLE candidate, space included. Returns "" if
// no candidate matches at pos.
func usageWorkflowLongestMatch(text string, pos int, candidates []string) string {
	best := ""
	for _, c := range candidates {
		if c == "" || len(c) <= len(best) {
			continue
		}
		end := pos + len(c)
		if end > len(text) || text[pos:end] != c {
			continue
		}
		if end < len(text) && usageWorkflowIsIdentByte(text[end]) {
			continue
		}
		best = c
	}
	return best
}

// usageWorkflowFindVerbsInText returns the distinct catalogue verbs named by
// ANY "a2a V" occurrence anywhere in s (longest-match/boundary aware). This
// is the topic-SET derivation's own scan (a loop-corpus page's whole prose),
// deliberately wider than usageWorkflowFindVerbsForAttribution's anchored
// scan — a doc page is allowed to mention a verb in a sentence, not only in
// a literal "usage:" line.
func usageWorkflowFindVerbsInText(s string, candidates []string) []string {
	seen := map[string]bool{}
	for i := 0; i+4 <= len(s); i++ {
		if s[i:i+4] != "a2a " {
			continue
		}
		if i > 0 && usageWorkflowIsIdentByte(s[i-1]) {
			continue
		}
		if v := usageWorkflowLongestMatch(s, i+4, candidates); v != "" {
			seen[v] = true
		}
	}
	return usageWorkflowSortedKeys(seen)
}

// usageWorkflowFindVerbsForAttribution returns the distinct catalogue verbs
// named IMMEDIATELY AFTER the usageWorkflowAnchor literal in s — never any
// bare "a2a V" mention. Attribution deliberately does not reuse
// usageWorkflowFindVerbsInText's wider scan: a cross-reference like
// "see `a2a inbox`" or "the array `notify render` produces" inside the same
// enclosing scope must not attribute that scope's workflowLine call to a
// verb it only mentions in passing.
func usageWorkflowFindVerbsForAttribution(s string, candidates []string) []string {
	seen := map[string]bool{}
	rest := s
	for {
		idx := strings.Index(rest, usageWorkflowAnchor)
		if idx < 0 {
			break
		}
		pos := idx + len(usageWorkflowAnchor)
		if v := usageWorkflowLongestMatch(rest, pos, candidates); v != "" {
			seen[v] = true
		}
		rest = rest[pos:]
	}
	return usageWorkflowSortedKeys(seen)
}

func usageWorkflowSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func usageWorkflowContains(list []string, v string) bool {
	for _, c := range list {
		if c == v {
			return true
		}
	}
	return false
}

// usageWorkflowParseDir parses every non-test .go file directly inside
// dir — no recursion, matching "internal/cli/*.go" (spec 08's own
// Footprint) or a --teeth fixture directory built the same shape — with
// go/parser in syntax-only mode (no type-check, no build, no go.mod
// needed), and collects every top-level `const NAME = "literal"`
// declaration for identifier resolution (spec 08 AC-6). Shared by
// usageWorkflowExtract (the flat topic set) and usageWorkflowExtractPairs
// (the per-verb attribution) so both read the exact same const table.
func usageWorkflowParseDir(dir string) ([]*ast.File, map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("usage-workflow: read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	consts := map[string]string{}
	var files []*ast.File

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, nil, fmt.Errorf("usage-workflow: parse %s: %w", path, perr)
		}
		files = append(files, f)

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if v, uerr := strconv.Unquote(lit.Value); uerr == nil {
						consts[ident.Name] = v
					}
				}
			}
		}
	}

	if len(files) == 0 {
		return nil, nil, fmt.Errorf("usage-workflow: %s carries no non-test .go file", dir)
	}
	return files, consts, nil
}

// usageWorkflowExtract walks dir and returns the sorted, de-duplicated FLAT
// set of topic strings passed as workflowLine's sole argument anywhere in
// that source — unchanged behaviour from before the 2026-08-29 widening,
// and still what the topic-driven check (every extracted topic is a real
// manifest section AND accepted by `a2a docs`) reads.
//
// An identifier argument is resolved through the same directory's own
// top-level `const NAME = "literal"` declarations (spec 08 AC-6) — an
// argument that is neither a string literal nor a const-resolved identifier
// (a runtime expression, a function call, a struct-field selector) is
// silently NOT collected: it is not this walk's job to evaluate it, and
// failing to collect a topic it cannot prove is the fail-closed direction.
func usageWorkflowExtract(dir string) ([]string, error) {
	files, consts, err := usageWorkflowParseDir(dir)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != usageWorkflowHelperName {
				return true
			}
			if len(call.Args) != 1 {
				return true
			}
			switch arg := call.Args[0].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					if v, uerr := strconv.Unquote(arg.Value); uerr == nil {
						seen[v] = true
					}
				}
			case *ast.Ident:
				if v, ok := consts[arg.Name]; ok {
					seen[v] = true
				}
			}
			return true
		})
	}

	return usageWorkflowSortedKeys(seen), nil
}

// usageWorkflowPair is one (verb, topic) attribution usageWorkflowExtractPairs
// found: verb V's own workflowLine call names topic.
type usageWorkflowPair struct {
	Verb  string
	Topic string
}

// usageWorkflowCallSite is one workflowLine(...) call usageWorkflowVisitor
// resolved within a single scope, before attribution.
type usageWorkflowCallSite struct {
	topic      string
	forcedVerb string // set by mechanism 2 (see this file's own doc comment); "" otherwise
}

// usageWorkflowScope accumulates every resolved string literal and every
// resolved workflowLine call found while walking ONE scope root (a
// *ast.FuncDecl body, or one top-level var/const value expression).
type usageWorkflowScope struct {
	literals []string
	calls    []usageWorkflowCallSite
}

// usageWorkflowVisitor implements ast.Visitor over a single scope.
// nearestKey carries mechanism 2's forcing verb name while descending into
// a map literal's value expression — set once, by the *ast.KeyValueExpr
// case, and handed to a child visitor so only calls WITHIN that one
// key's value inherit it.
type usageWorkflowVisitor struct {
	consts     map[string]string
	nearestKey string
	scope      *usageWorkflowScope
}

func (v *usageWorkflowVisitor) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			if s, err := strconv.Unquote(node.Value); err == nil {
				v.scope.literals = append(v.scope.literals, s)
			}
		}
	case *ast.Ident:
		if s, ok := v.consts[node.Name]; ok {
			v.scope.literals = append(v.scope.literals, s)
		}
	case *ast.KeyValueExpr:
		// A map literal's key is *ast.BasicLit; a struct literal's field
		// key (e.g. `lifecycleVerbSpec{Verb: "ack"}`) is *ast.Ident — this
		// case fires ONLY for the former, which is mechanism 2's whole
		// point (a struct-literal table row never forces an attribution).
		if lit, ok := node.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				child := &usageWorkflowVisitor{consts: v.consts, nearestKey: s, scope: v.scope}
				ast.Walk(child, node.Value)
				return nil
			}
		}
	case *ast.CallExpr:
		if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == usageWorkflowHelperName && len(node.Args) == 1 {
			var topic string
			var resolved bool
			switch arg := node.Args[0].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					if s, err := strconv.Unquote(arg.Value); err == nil {
						topic, resolved = s, true
					}
				}
			case *ast.Ident:
				if s, ok := v.consts[arg.Name]; ok {
					topic, resolved = s, true
				}
			}
			if resolved {
				v.scope.calls = append(v.scope.calls, usageWorkflowCallSite{topic: topic, forcedVerb: v.nearestKey})
			}
		}
	}
	return v
}

// usageWorkflowExtractPairs walks dir (the same shape usageWorkflowExtract
// reads) and returns every (verb, topic) attribution it can prove, sorted
// by (verb, topic). See this file's own doc comment for the two attribution
// mechanisms. It returns an error (never a partial/best-effort result) when
// a scope's usage text names more than one distinct catalogue verb, or when
// a mechanism-2 map key names none — both are authoring mistakes, not
// drift this gate should silently absorb.
func usageWorkflowExtractPairs(dir string, catalogueVerbs []string) ([]usageWorkflowPair, error) {
	files, consts, err := usageWorkflowParseDir(dir)
	if err != nil {
		return nil, err
	}

	var pairs []usageWorkflowPair
	for _, f := range files {
		for _, decl := range f.Decls {
			var roots []ast.Node
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					roots = append(roots, d.Body)
				}
			case *ast.GenDecl:
				if d.Tok == token.CONST || d.Tok == token.VAR {
					for _, spec := range d.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for _, val := range vs.Values {
							roots = append(roots, val)
						}
					}
				}
			}

			for _, root := range roots {
				scope := &usageWorkflowScope{}
				ast.Walk(&usageWorkflowVisitor{consts: consts, scope: scope}, root)
				if len(scope.calls) == 0 {
					continue
				}

				anchored := map[string]bool{}
				for _, lit := range scope.literals {
					for _, verb := range usageWorkflowFindVerbsForAttribution(lit, catalogueVerbs) {
						anchored[verb] = true
					}
				}
				distinct := usageWorkflowSortedKeys(anchored)

				for _, call := range scope.calls {
					switch {
					case call.forcedVerb != "":
						if !usageWorkflowContains(catalogueVerbs, call.forcedVerb) {
							return nil, fmt.Errorf(
								"usage-workflow: workflowLine(%q) is keyed by %q in a map literal, which names no catalogue verb — a typo in the key?",
								call.topic, call.forcedVerb,
							)
						}
						pairs = append(pairs, usageWorkflowPair{Verb: call.forcedVerb, Topic: call.topic})
					case len(distinct) == 1:
						pairs = append(pairs, usageWorkflowPair{Verb: distinct[0], Topic: call.topic})
					case len(distinct) > 1:
						return nil, fmt.Errorf(
							"usage-workflow: a workflowLine(%q) call's enclosing scope names more than one catalogue verb (%v) — cannot attribute it to exactly one",
							call.topic, distinct,
						)
					default:
						// Unattributed: no anchor match, no forcing key.
						// Dropped from pairs (fail-closed for the
						// verb-driven check), but usageWorkflowExtract's
						// own flat set still carries the topic, so the
						// topic-driven check still polices it.
					}
				}
			}
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Verb != pairs[j].Verb {
			return pairs[i].Verb < pairs[j].Verb
		}
		return pairs[i].Topic < pairs[j].Topic
	})
	return pairs, nil
}

// usageWorkflowDeriveTopicSets computes, for every verb in catalogueVerbs,
// the topic set answers-that-hold-2026-08 spec 08 (2026-08-29 widening)
// defines: the section id of every page in manifest.LoopCorpus whose text
// names `a2a V` (usageWorkflowFindVerbsInText — the wide scan, prose
// mentions included), unioned with V's own id when V is itself a section
// id in manifest.Sections. A verb absent from the returned map has an EMPTY
// set (spec AC-3: no obligation).
//
// tree is an fs.FS rather than a hardcoded skill.Files read (mirroring
// skill.LoadDocsManifest's own signature) so a fixture tree can prove the
// derivation's boundary/longest-match behaviour in isolation; the gate's
// own real (and --teeth) invocations always pass the real embedded
// skill.Files — the manifest is never synthetic under --teeth (this
// script's own header states that rule, and this function upholds it by
// construction: nothing in scripts/check-usage-workflow.sh can override
// the tree it is called with).
func usageWorkflowDeriveTopicSets(manifest skill.DocsManifest, tree fs.FS, catalogueVerbs []string) (map[string][]string, error) {
	fileToSection := make(map[string]string, len(manifest.Sections))
	sectionIDs := make(map[string]bool, len(manifest.Sections))
	for _, s := range manifest.Sections {
		fileToSection[s.File] = s.ID
		sectionIDs[s.ID] = true
	}

	sets := map[string]map[string]bool{}
	for _, lf := range manifest.LoopCorpus {
		raw, err := fs.ReadFile(tree, lf)
		if err != nil {
			return nil, fmt.Errorf("usage-workflow: read loop corpus page %s: %w", lf, err)
		}
		sectionID, ok := fileToSection[lf]
		if !ok {
			return nil, fmt.Errorf("usage-workflow: loop corpus page %s carries no docs-manifest.json section", lf)
		}
		for _, v := range usageWorkflowFindVerbsInText(string(raw), catalogueVerbs) {
			if sets[v] == nil {
				sets[v] = map[string]bool{}
			}
			sets[v][sectionID] = true
		}
	}
	for _, v := range catalogueVerbs {
		if sectionIDs[v] {
			if sets[v] == nil {
				sets[v] = map[string]bool{}
			}
			sets[v][v] = true
		}
	}

	out := make(map[string][]string, len(sets))
	for v, topics := range sets {
		out[v] = usageWorkflowSortedKeys(topics)
	}
	return out, nil
}

// TestUsageWorkflowDump is scripts/check-usage-workflow.sh's own "ask the
// program" entry point. The extraction assertion below runs unconditionally
// against this package's own real source directory ("."), never behind a
// skip, so a plain `go test ./internal/cli/...` still exercises real
// coverage even with no gate involved.
//
// USAGE_WORKFLOW_SRC_DIR redirects the walk to a --teeth fixture directory
// (an absolute path, so this test's own working directory — always this
// package's source directory under `go test`, never the fixture — is
// irrelevant). USAGE_WORKFLOW_CATALOGUE (newline-separated verb names, read
// from the REAL `a2a __catalog` by the gate — never hand-listed here, AC-5)
// additionally drives the pairs/topic-set computation. USAGE_WORKFLOW_DUMP
// writes both as plain lines (this repo's scripts deliberately avoid a jq
// dependency — see check-loop-reachability.sh's own convention — so the
// dump is grep/sed-friendly, not JSON):
//
//	TOPIC\t<topic>                 — one per usageWorkflowExtract entry
//	PAIR\t<verb>\t<topic>          — one per usageWorkflowExtractPairs entry
//	SET\t<verb>\t<topic1>,<topic2> — one per non-empty usageWorkflowDeriveTopicSets entry
func TestUsageWorkflowDump(t *testing.T) {
	dir := os.Getenv("USAGE_WORKFLOW_SRC_DIR")
	real := dir == ""
	if real {
		dir = "."
	}

	topics, err := usageWorkflowExtract(dir)
	if err != nil {
		t.Fatalf("usageWorkflowExtract(%q): %v", dir, err)
	}

	if real {
		// A weak, non-exhaustive sanity check — NOT the universe assertion.
		// scripts/check-usage-workflow.sh derives and checks the real
		// universe independently; a roster of every expected topic here
		// would be exactly the hand-maintained list AC-5 forbids, and it
		// would churn with every loop-corpus prose edit. These three
		// predate the 2026-08-29 widening and are cheap, stable proof the
		// extractor still runs over the real tree with no gate involved.
		for _, want := range []string{"feedback", "notify", "notifications"} {
			if !usageWorkflowContains(topics, want) {
				t.Fatalf("usageWorkflowExtract(.) = %v, want at least %q", topics, want)
			}
		}
	}

	var catalogueVerbs []string
	if raw := os.Getenv("USAGE_WORKFLOW_CATALOGUE"); raw != "" {
		for _, v := range strings.Split(raw, "\n") {
			if v != "" {
				catalogueVerbs = append(catalogueVerbs, v)
			}
		}
	}

	var pairs []usageWorkflowPair
	var sets map[string][]string
	if len(catalogueVerbs) > 0 {
		pairs, err = usageWorkflowExtractPairs(dir, catalogueVerbs)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs(%q): %v", dir, err)
		}
		// The manifest/loop-corpus tree is ALWAYS the real embedded one —
		// never USAGE_WORKFLOW_SRC_DIR, which only redirects the Go-source
		// AST walk. See this rule stated in scripts/check-usage-workflow.sh
		// and in usageWorkflowDeriveTopicSets's own doc comment.
		sets, err = usageWorkflowDeriveTopicSets(skill.EmbeddedDocsManifest(), skill.Files, catalogueVerbs)
		if err != nil {
			t.Fatalf("usageWorkflowDeriveTopicSets: %v", err)
		}
	}

	if path := os.Getenv("USAGE_WORKFLOW_DUMP"); path != "" {
		var b strings.Builder
		for _, top := range topics {
			fmt.Fprintf(&b, "TOPIC\t%s\n", top)
		}
		for _, p := range pairs {
			fmt.Fprintf(&b, "PAIR\t%s\t%s\n", p.Verb, p.Topic)
		}
		for _, v := range usageWorkflowSortedKeys(setsHaveKey(sets)) {
			fmt.Fprintf(&b, "SET\t%s\t%s\n", v, strings.Join(sets[v], ","))
		}
		if werr := os.WriteFile(path, []byte(b.String()), 0o600); werr != nil {
			t.Fatalf("write usage-workflow dump: %v", werr)
		}
	}
}

// setsHaveKey adapts a map[string][]string's key set to
// usageWorkflowSortedKeys' map[string]bool shape, so TestUsageWorkflowDump
// writes SET lines in deterministic order with no second sort helper.
func setsHaveKey(m map[string][]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// TestUsageWorkflowLongestMatch is the dedicated regression spec 08's
// widening names: "a2a contract must NOT be credited by a page that only
// says `a2a contract publish`" — and its real-catalogue analogue, the
// hyphen-joined verify/verify-pass/verify-fail collision.
func TestUsageWorkflowLongestMatch(t *testing.T) {
	candidates := []string{"contract", "contract publish", "verify", "verify-pass", "verify-fail"}
	cases := []struct {
		name string
		text string
		pos  int
		want string
	}{
		{"longest multi-word candidate wins over its own shorter prefix", "a2a contract publish --version", 4, "contract publish"},
		{"shorter candidate matches when the longer one is absent", "a2a contract check", 4, "contract"},
		{"hyphen boundary: verify is not a false-credited prefix of verify-pass", "a2a verify-pass <id>", 4, "verify-pass"},
		{"hyphen boundary: verify-fail is its own distinct token", "a2a verify-fail <id>", 4, "verify-fail"},
		{"bare verify still matches when nothing longer is present", "a2a verify <id>", 4, "verify"},
		{"no candidate matches", "a2a something-else", 4, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usageWorkflowLongestMatch(tc.text, tc.pos, candidates)
			if got != tc.want {
				t.Fatalf("usageWorkflowLongestMatch(%q, %d, %v) = %q, want %q", tc.text, tc.pos, candidates, got, tc.want)
			}
		})
	}
}

// TestUsageWorkflowExtractPairs covers both attribution mechanisms and
// their failure modes over synthetic, isolated fixtures — the mechanics
// coverage that must NOT depend on (and therefore churn with) the real
// tree's current content.
func TestUsageWorkflowExtractPairs(t *testing.T) {
	catalogue := []string{"feedback", "verify", "verify-pass", "contract", "contract publish", "ack", "accept"}

	writeFixture := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("mechanism 1: usage anchor plus const resolution", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

const feedbackTopic = "feedback"

func workflowLine(topic string) string { return "workflow: " + topic }

func usageFeedback() string {
	return "usage: a2a feedback <new|validate> ...\n" + workflowLine(feedbackTopic)
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		want := []usageWorkflowPair{{Verb: "feedback", Topic: "feedback"}}
		if !reflect.DeepEqual(pairs, want) {
			t.Fatalf("pairs = %v, want %v", pairs, want)
		}
	})

	t.Run("mechanism 1: longest match distinguishes verify-pass from verify", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }

func usageVerifyPass() string {
	return "usage: a2a verify-pass <handoff-id>\n" + workflowLine("loop-send")
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		want := []usageWorkflowPair{{Verb: "verify-pass", Topic: "loop-send"}}
		if !reflect.DeepEqual(pairs, want) {
			t.Fatalf("pairs = %v, want %v (a boundary-blind matcher would wrongly attribute this to \"verify\")", pairs, want)
		}
	})

	t.Run("longest-match trap: bare contract is not credited by a page naming only contract publish", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }

func usageContractPublish() string {
	return "usage: a2a contract publish --version <semver>\n" + workflowLine("loop-contract-change")
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		want := []usageWorkflowPair{{Verb: "contract publish", Topic: "loop-contract-change"}}
		if !reflect.DeepEqual(pairs, want) {
			t.Fatalf("pairs = %v, want %v (bare \"contract\" must not be credited)", pairs, want)
		}
	})

	t.Run("mechanism 2: map-literal key attributes with no usage text at all", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }

var lifecycleWorkflowLines = map[string]string{
	"ack": workflowLine("loop-receive"),
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		want := []usageWorkflowPair{{Verb: "ack", Topic: "loop-receive"}}
		if !reflect.DeepEqual(pairs, want) {
			t.Fatalf("pairs = %v, want %v", pairs, want)
		}
	})

	t.Run("mechanism 2: a struct-literal field key never forces an attribution", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

type spec struct {
	Verb  string
	Topic string
}

func workflowLine(topic string) string { return "workflow: " + topic }

var table = []spec{
	{Verb: "ack", Topic: "loop-receive"},
}

func usageAck(s spec) string {
	return workflowLine(s.Topic)
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		if len(pairs) != 0 {
			t.Fatalf("pairs = %v, want none (workflowLine(s.Topic) is a selector expression, not resolvable, and the struct literal's \"Verb\" key is an *ast.Ident, not a forcing key)", pairs)
		}
	})

	t.Run("mechanism 2: an unknown map key fails loudly", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }

var lifecycleWorkflowLines = map[string]string{
	"akc": workflowLine("loop-receive"),
}
`)
		if _, err := usageWorkflowExtractPairs(dir, catalogue); err == nil {
			t.Fatal("usageWorkflowExtractPairs: want an error for a map key naming no catalogue verb, got nil")
		}
	})

	t.Run("unattributed: an unresolvable dynamic argument drops the call silently", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }
func computeTopic() string { return "feedback" }

func usageDynamic() string {
	return "some other message\n" + workflowLine(computeTopic())
}
`)
		pairs, err := usageWorkflowExtractPairs(dir, catalogue)
		if err != nil {
			t.Fatalf("usageWorkflowExtractPairs: %v", err)
		}
		if len(pairs) != 0 {
			t.Fatalf("pairs = %v, want none (workflowLine's argument is a call expression, not resolvable)", pairs)
		}
	})

	t.Run("ambiguous scope: two distinct anchored verbs in one function fails loudly", func(t *testing.T) {
		dir := writeFixture(t, `package fixture

func workflowLine(topic string) string { return "workflow: " + topic }

func usageAmbiguous(bad bool) string {
	if bad {
		return "usage: a2a feedback ..."
	}
	return "usage: a2a ack <id...>\n" + workflowLine("loop-receive")
}
`)
		if _, err := usageWorkflowExtractPairs(dir, catalogue); err == nil {
			t.Fatal("usageWorkflowExtractPairs: want an error when one scope's usage text names more than one catalogue verb, got nil")
		}
	})
}

// TestUsageWorkflowDeriveTopicSets covers the loop-corpus scan in isolation,
// over a synthetic manifest + fixture tree — including the same hyphen
// boundary trap TestUsageWorkflowLongestMatch checks at the pure-function
// level, here exercised through the full derivation.
func TestUsageWorkflowDeriveTopicSets(t *testing.T) {
	manifest := skill.DocsManifest{
		Schema:     skill.DocsManifestSchema,
		Groups:     []string{"Reference"},
		LoopCorpus: []string{"a2ahub/loops/one.md", "a2ahub/loops/two.md"},
		Sections: []skill.DocSectionEntry{
			{ID: "loop-one", Group: "Reference", Title: "One", File: "a2ahub/loops/one.md"},
			{ID: "loop-two", Group: "Reference", Title: "Two", File: "a2ahub/loops/two.md"},
			{ID: "notify", Group: "Reference", Title: "Notify", File: "a2ahub/reference/notify.md"},
		},
	}
	tree := fstest.MapFS{
		"a2ahub/loops/one.md": &fstest.MapFile{Data: []byte("Run `a2a verify-pass <id>` then `a2a contract publish`.")},
		"a2ahub/loops/two.md": &fstest.MapFile{Data: []byte("Only `a2a verify` (not -pass) appears here.")},
	}
	catalogueVerbs := []string{"verify", "verify-pass", "contract publish", "notify"}

	got, err := usageWorkflowDeriveTopicSets(manifest, tree, catalogueVerbs)
	if err != nil {
		t.Fatalf("usageWorkflowDeriveTopicSets: %v", err)
	}

	want := map[string][]string{
		"verify":           {"loop-two"},
		"verify-pass":      {"loop-one"},
		"contract publish": {"loop-one"},
		"notify":           {"notify"}, // union: own section id, named by no loop page
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageWorkflowDeriveTopicSets = %v, want %v", got, want)
	}
}
