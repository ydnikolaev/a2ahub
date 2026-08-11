package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyShStampsWhatThisPackageStamps closes the hole B33 was made of,
// one level up from where B33 found it.
//
// This package builds the `a2a` binary its tests exec — but only when
// A2A_VERIFY_BINARY is unset. Every scripts/verify.sh mode except MODE=test
// exports that variable, so under `make check` and `make lane-run` the binary
// these tests drive is the one verify.sh built, not the one runTestMain would
// have. Two builds, one test run, and until 2026-08-11 they stamped different
// versions: this package's own build said 0.1.0 because a literal said so, and
// so did verify.sh's, in a file no test in this package reads.
//
// Raising one and not the other is not a cosmetic mismatch. `harnessStampedVersion`
// is contract.ContractPublicationFloor precisely so a rig raised to that floor
// can run the event/v2 verbs at all; a shared binary stamped below it trips
// CC-085 and refuses EVERY write in that rig. So the failure mode of a drift
// here is not "one assertion about a version string" — it is
// event_v2_binary_test.go going red under the ceiling and green under a scoped
// run, with nothing saying why.
//
// What this asserts, and the two halves matter separately:
//
//  1. verify.sh carries NO hardcoded version literal on its build line. That is
//     the defect itself, stated directly.
//  2. The derivation verify.sh actually contains, executed here, yields exactly
//     harnessStampedVersion. This is what makes the test more than a grep: if
//     `go doc`'s output format shifts, or ContractPublicationFloor stops being
//     a plain alias of OperationalConfidenceFloor, the extraction returns
//     something else (or nothing) and this fails — where verify.sh itself would
//     merely refuse to build, at a much later and more confusing moment.
//
// It deliberately does NOT execute verify.sh: that script runs a whole gate
// suite top to bottom and has no sourceable entry point. It reads the one
// pipeline out of it instead, which means the assertion is coupled to that
// pipeline's text — intentionally. Rewriting the derivation should have to come
// past this test.
func TestVerifyShStampsWhatThisPackageStamps(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	scriptPath := filepath.Join(root, "scripts", "verify.sh")
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read verify.sh: %v", err)
	}
	script := string(raw)

	// (1) No literal. `-X main.version=` may only ever be followed by a shell
	// expansion, never a digit.
	const stampFlag = "-X main.version="
	for idx := 0; ; {
		at := strings.Index(script[idx:], stampFlag)
		if at < 0 {
			break
		}
		at += idx
		rest := script[at+len(stampFlag):]
		if rest == "" || rest[0] != '$' {
			line := 1 + strings.Count(script[:at], "\n")
			t.Fatalf("scripts/verify.sh:%d stamps a LITERAL version into the shared binary (%.40q).\n"+
				"This package's own build derives its stamp from contract.ContractPublicationFloor; a second\n"+
				"build that hardcodes a different number makes the event/v2 verbs unreachable under the\n"+
				"ceiling while a scoped `go test ./internal/e2e/...` stays green (agent-exchange-2026-08 B33).",
				line, rest)
		}
		idx = at + len(stampFlag)
	}

	// (2) The derivation verify.sh contains must actually produce our stamp.
	const goDocCmd = `go doc github.com/ydnikolaev/a2ahub/internal/version OperationalConfidenceFloor`
	const sedExpr = `s/^const OperationalConfidenceFloor = "\(.*\)"$/\1/p`
	if !strings.Contains(script, goDocCmd) || !strings.Contains(script, sedExpr) {
		t.Fatalf("scripts/verify.sh no longer contains the stamp derivation this test knows how to run.\n"+
			"Expected both:\n  %s\n  sed -n '%s'\n"+
			"If the derivation moved or changed shape, update this test to run the NEW one — do not delete\n"+
			"the check: an underived stamp is what B33 was.", goDocCmd, sedExpr)
	}

	// The `go doc` half runs for real. The `sed` half is reproduced in Go
	// rather than shelled out — same anchored pattern, no shell interpreter in
	// a test — so what this proves is that the SOURCE verify.sh reads still
	// says what verify.sh expects it to say. The textual check just above is
	// what ties that back to the script's own text.
	cmd := exec.Command("go", "doc",
		"github.com/ydnikolaev/a2ahub/internal/version", "OperationalConfidenceFloor")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running verify.sh's own stamp derivation: %v", err)
	}
	var got string
	for _, line := range strings.Split(string(out), "\n") {
		const prefix = `const OperationalConfidenceFloor = "`
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, `"`) {
			got = line[len(prefix) : len(line)-1]
			break
		}
	}
	if got == "" {
		t.Fatalf("`go doc` no longer prints OperationalConfidenceFloor in the single-line form verify.sh's\n"+
			"sed anchors on. verify.sh would refuse to build rather than guess — which is correct, but it\n"+
			"would do so at gate time with no hint. Output was:\n%s", out)
	}
	if got != harnessStampedVersion {
		t.Fatalf("verify.sh's derivation yields %q; this package stamps %q.\n"+
			"The shared binary and this package's own build must agree, or the tier's verdict depends on\n"+
			"which one happened to be built.", got, harnessStampedVersion)
	}
}
