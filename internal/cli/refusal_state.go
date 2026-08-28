// refusal_state.go — P4 (answers-that-hold-2026-08, spec 04): a refusal
// constructor whose ARITY, not a house rule, makes a next-step-less
// refusal impossible to build. Three instances of the same defect shipped
// across three releases (v0.19.8, v0.24.0, v0.25.6-in-flight — spec 04 §0.5)
// even with the rule written down twice (scripts/classify-guard.sh's own
// header: "the message prints the FIX, not the symptom"). A gate that greps
// for banned prose is defeated in one commit; this generalises the shape
// `cmd_notify_setup.go` already ships for the not-a-space refusal
// (notifyNotASpaceMessage/notifyUnparseableManifestMessage: what was read,
// what was expected, the command that fixes it) into ONE constructor the
// rest of the package can call.
//
// ADR-019 (docs/decisions.md:1009, spec 04 §11 amendments): this stays in
// internal/cli, not a shared package below internal/cli and internal/mcp.
// The MCP tool surface's own input schema forbids the cwd/root/project_root
// vocabulary this constructor's three parts are made of
// (internal/mcp/tools_work_test.go:224), so ADR-019 half 1's trigger — "a
// vocabulary needed by BOTH surfaces" — is not met. See spec 04 §11 for the
// full analysis and the one condition that would flip it.
package cli

import (
	"fmt"
	"strings"
)

// Refusal is a three-part CLI refusal: what was ATTEMPTED, what was FOUND,
// and what to do NEXT. All three are required — see NewRefusal, the only
// constructor. A refusal built through this type can never omit the third
// part, which is the whole point: "cannot read space.yaml" alone reads as
// "this command does not exist" (spec 04 US-1); attempted+found+nextStep
// reads as a caller's next action.
type Refusal struct {
	attempted string
	found     string
	nextStep  string
}

// NewRefusal builds a Refusal. attempted, found and nextStep are all
// REQUIRED, fixed positional arguments — not an options struct, not a
// builder, not variadic. That is deliberate: an options struct or a builder
// lets a caller omit the next-step field and still compile; a fixed
// three-argument function does not. Calling NewRefusal with only two
// arguments is therefore a COMPILE ERROR, not a runtime one (spec 04 AC-4;
// the compile-fail fixture lives under scripts/check-refusal-ratchet.sh's
// own --teeth, not the Go build, precisely so it never has to compile
// clean).
//
// A caller can still pass an empty string as nextStep — arity alone cannot
// forbid that, only omitting the argument entirely. So the empty case is
// checked here, at construction, and returns an error rather than panicking:
// every call site this phase reaches (and the ones after it) already runs
// inside a command body that maps an error to a stderr write and a
// non-zero exit code — the same shape `runValidateCI` already uses for every
// other failure in this file — so an idiomatic error return gives the
// caller a place to react without a recover(), and it never turns a CLI
// refusal into a process crash.
func NewRefusal(attempted, found, nextStep string) (Refusal, error) {
	if strings.TrimSpace(nextStep) == "" {
		return Refusal{}, fmt.Errorf(
			"cli: refusal for %q (found: %q) was given an empty next step — every refusal must name what to do next",
			attempted, found,
		)
	}
	return Refusal{attempted: attempted, found: found, nextStep: nextStep}, nil
}

// Error implements the error interface, so a Refusal can be written
// straight to stderr (fmt.Fprintln(stdio.Stderr, refusal)) or wrapped like
// any other error.
func (r Refusal) Error() string {
	return fmt.Sprintf("%s: %s — %s", r.attempted, r.found, r.nextStep)
}

// The three accessors below expose the parts individually, so a caller that
// renders them on separate lines — or asserts on one part in a test — is not
// forced to parse Error()'s combined string back apart.

// Attempted returns what the refused operation was trying to do.
func (r Refusal) Attempted() string { return r.attempted }

// Found returns the state the operation actually met.
func (r Refusal) Found() string { return r.found }

// NextStep returns the action that resolves it, from where the caller stands.
// It is never empty: NewRefusal refuses to build a Refusal without one.
func (r Refusal) NextStep() string { return r.nextStep }
