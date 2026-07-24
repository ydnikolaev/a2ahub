package livee2e

import (
	"strings"
	"testing"
)

func TestExecutedRefIsCurrentMatching(t *testing.T) {
	t.Parallel()

	sha := "abc123def456abc123def456abc123def456abc"
	ok, detail := ExecutedRefIsCurrent(sha, CheckRunRef{
		Name:       "a2a-validate / validate",
		HeadSHA:    sha,
		Status:     "completed",
		Conclusion: "success",
	})
	if !ok {
		t.Fatalf("ok = false, want true (matching SHAs); detail = %q", detail)
	}
	if detail != "" {
		t.Fatalf("detail = %q, want empty on a match", detail)
	}
}

func TestExecutedRefIsCurrentMatchingCaseInsensitive(t *testing.T) {
	t.Parallel()

	ok, detail := ExecutedRefIsCurrent(
		"ABC123DEF456ABC123DEF456ABC123DEF456ABC1",
		CheckRunRef{Name: "a2a-validate / validate", HeadSHA: "abc123def456abc123def456abc123def456abc1"},
	)
	if !ok {
		t.Fatalf("ok = false, want true (case-insensitive hex match); detail = %q", detail)
	}
	requireSingleLine(t, detail)
}

func TestExecutedRefIsCurrentMismatch(t *testing.T) {
	t.Parallel()

	prHead := "1111111111111111111111111111111111111111"
	runSHA := "2222222222222222222222222222222222222222"
	ok, detail := ExecutedRefIsCurrent(prHead, CheckRunRef{
		Name:    "a2a-validate / validate",
		HeadSHA: runSHA,
	})
	if ok {
		t.Fatal("ok = true, want false — the run executed a DIFFERENT commit than the PR's current head")
	}
	if detail == "" {
		t.Fatal("detail must not be empty on a mismatch — it is what the report row shows")
	}
	requireSingleLine(t, detail)
	for _, want := range []string{prHead, runSHA, "stale"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q, want it to name %q", detail, want)
		}
	}
}

func TestExecutedRefIsCurrentPrefixMatchIsNotEnough(t *testing.T) {
	t.Parallel()

	// Two distinct commits sharing an abbreviated prefix must NOT compare
	// as current — a prefix match here would be the exact stale-ref false
	// positive this function exists to prevent.
	ok, detail := ExecutedRefIsCurrent(
		"abc1234000000000000000000000000000000000",
		CheckRunRef{Name: "a2a-validate / validate", HeadSHA: "abc1234999999999999999999999999999999999"},
	)
	if ok {
		t.Fatalf("ok = true, want false — a shared prefix is not a full-SHA match; detail = %q", detail)
	}
	requireSingleLine(t, detail)
}

func TestExecutedRefIsCurrentEmptyPRHead(t *testing.T) {
	t.Parallel()

	ok, detail := ExecutedRefIsCurrent("", CheckRunRef{Name: "a2a-validate / validate", HeadSHA: "abc123"})
	if ok {
		t.Fatal("ok = true, want false — an unknown PR head SHA can never be 'current'")
	}
	if detail == "" {
		t.Fatal("detail must explain why an empty PR head SHA fails closed")
	}
	requireSingleLine(t, detail)
}

func TestExecutedRefIsCurrentEmptyRunHead(t *testing.T) {
	t.Parallel()

	ok, detail := ExecutedRefIsCurrent("abc123", CheckRunRef{Name: "a2a-validate / validate", HeadSHA: ""})
	if ok {
		t.Fatal("ok = true, want false — a check run reporting no head SHA of its own is never 'current'")
	}
	if detail == "" {
		t.Fatal("detail must explain why an empty run head SHA fails closed")
	}
	requireSingleLine(t, detail)
}

func TestExecutedRefIsCurrentBothEmpty(t *testing.T) {
	t.Parallel()

	ok, detail := ExecutedRefIsCurrent("", CheckRunRef{})
	if ok {
		t.Fatal("ok = true, want false — two empty SHAs must never compare as current")
	}
	if detail == "" {
		t.Fatal("detail must explain the fail-closed reading even when both sides are empty")
	}
	requireSingleLine(t, detail)
}

// requireSingleLine guards report.go's rendering contract: Render prints
// detail as `    detail:   %s\n`, so a "\n" inside detail would break the
// report table — a constraint report.go imposes, not one the brief states
// outright.
func requireSingleLine(t *testing.T, detail string) {
	t.Helper()
	if strings.Contains(detail, "\n") {
		t.Fatalf("detail must be a single line (report.go renders it inline): %q", detail)
	}
}
