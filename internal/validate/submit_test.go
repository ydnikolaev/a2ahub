package validate

import (
	"errors"
	"os"
	"strings"
	"testing"
)

const validWorkRequestYAML = `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: A valid work request
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without normalization."
acceptance_criteria:
  - "Every code exists in the registry."
classification: internal
`

const validWorkRequestBody = "---\n" + validWorkRequestYAML + "---\nBody text.\n"

// TestAuthzFromOwnSection is AC-201.3: an artifact whose `from` doesn't
// match the submitting system's own configured section is refused at V2
// (CC-002), including the decision-type exception (§5.2: decisions route
// authz via the decision flow, not the generic from==section check).
func TestAuthzFromOwnSection(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	t.Run("wrong section is rejected", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateForSubmit(
			Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: []byte(validWorkRequestBody)},
			nil,
			LocalContext{OwnSystem: "seomatrix"}, // env `from: axon` != configured own system
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if result.Valid {
			t.Fatal("expected authz rejection for from != own system, got Valid=true")
		}
		if !hasCode(result.Violations, "REF-005") {
			t.Fatalf("expected REF-005 among violations, got %+v", result.Violations)
		}
	})

	t.Run("matching own system passes authz", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateForSubmit(
			Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: []byte(validWorkRequestBody)},
			nil,
			LocalContext{OwnSystem: "axon"},
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if hasCode(result.Violations, "REF-005") {
			t.Fatalf("expected no authz violation when from == own system, got %+v", result.Violations)
		}
	})

	t.Run("empty OwnSystem fails closed (never fail-open authz)", func(t *testing.T) {
		t.Parallel()
		_, err := engine.ValidateForSubmit(
			Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: []byte(validWorkRequestBody)},
			nil,
			LocalContext{}, // OwnSystem unset — a caller/wiring misconfiguration
		)
		if !errors.Is(err, ErrNoOwnSystem) {
			t.Fatalf("ValidateForSubmit with empty OwnSystem: err = %v, want ErrNoOwnSystem", err)
		}
	})

	t.Run("decision-type exception skips the generic check", func(t *testing.T) {
		t.Parallel()
		decisionBody := "---\n" + `
schema: envelope/v1
id: XD-axon-20260731-p9d3
type: decision
title: A decision authored by a drafting system in decisions/
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
priority: p3
blocking: false
interim_behavior: "n/a"
required_approvers: [seomatrix]
context: "why"
options_considered: ["a", "b"]
classification: internal
` + "---\nBody.\n"
		result, err := engine.ValidateForSubmit(
			Draft{Path: "decisions/XD-axon-20260731-p9d3.md", Raw: []byte(decisionBody)},
			nil,
			LocalContext{OwnSystem: "seomatrix"}, // != `from: axon`, but decision-exempt
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if hasCode(result.Violations, "REF-005") {
			t.Fatalf("expected the decision-type exception to skip REF-005, got %+v", result.Violations)
		}
	})
}

// TestSecretScan is AC-203.1: the §13.4 secret-pattern corpus is blocked
// by ValidateForSubmit's policy class; benign lookalikes pass.
func TestSecretScan(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	positiveFiles := globFixtures(t, corpusRoot+"/fixtures/secret-corpus/positive/*.md")
	if len(positiveFiles) == 0 {
		t.Fatal("expected at least one positive secret-corpus fixture")
	}
	for _, f := range positiveFiles {
		t.Run("positive/"+baseName(f), func(t *testing.T) {
			t.Parallel()
			raw := readFileForTest(t, f)
			// §10.4: the scan covers ALL text content crossing the
			// boundary (envelopes, bodies, event notes, ...) — this
			// corpus's fixtures are standalone bodies, so the scan is
			// exercised directly against their raw bytes; the same
			// scanForSecrets call is what ValidateForSubmit runs
			// internally over an artifact's full raw bytes.
			violations := scanForSecrets(raw)
			if len(violations) == 0 {
				t.Fatalf("expected the secret scanner to block %s", f)
			}
			if !hasCode(violations, "POL-001") {
				t.Fatalf("expected POL-001, got %+v", violations)
			}
		})
	}

	// End-to-end: a full artifact whose body embeds a positive-corpus
	// fixture's content is blocked by ValidateForSubmit (the actual
	// AC-203.1 entry point), not just the scanForSecrets helper above.
	t.Run("end-to-end via ValidateForSubmit", func(t *testing.T) {
		t.Parallel()
		secretBody := readFileForTest(t, positiveFiles[0])
		artifact := append([]byte(nil), validWorkRequestBody...)
		artifact = append(artifact, secretBody...)
		result, err := engine.ValidateForSubmit(
			Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: artifact},
			nil,
			LocalContext{OwnSystem: "axon"},
		)
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if result.Valid {
			t.Fatal("expected ValidateForSubmit to block an artifact whose body embeds a secret pattern")
		}
		if !hasCode(result.Violations, "POL-001") {
			t.Fatalf("expected POL-001 among violations, got %+v", result.Violations)
		}
	})

	negativeFiles := globFixtures(t, corpusRoot+"/fixtures/secret-corpus/negative/*.md")
	if len(negativeFiles) == 0 {
		t.Fatal("expected at least one negative (benign lookalike) secret-corpus fixture")
	}
	for _, f := range negativeFiles {
		t.Run("negative/"+baseName(f), func(t *testing.T) {
			t.Parallel()
			raw := readFileForTest(t, f)
			if v := scanForSecrets(raw); len(v) != 0 {
				t.Fatalf("expected benign lookalike %s to pass, got %+v", f, v)
			}
		})
	}
}

// TestSecretScanHardenedPatterns is spec 25 §11's "sensitive-content
// hardening" sub-wave (13f, operator review 2026-07-23): the additional
// prefix/shape-anchored credential patterns appended to secretPatterns
// (policy.go). Every positive case is a synthetic, non-live fake token
// sized to actually clear its pattern's length floor; every negative case
// proves the SAME false-positive budget internal/validate/golden_test.go's
// secret-corpus negatives already document — a legitimate hex hash, a
// plain GitHub repo URL, an ordinary sentence using the word "bearer", and
// a base64 blob that is not a JWT must all pass clean, because this
// scanner is shared by envelope submit too (a false positive there blocks
// a legitimate artifact, not just a legitimate feedback report).
func TestSecretScanHardenedPatterns(t *testing.T) {
	t.Parallel()

	positives := map[string]string{
		"github oauth token (gho_)":          "gho_" + strings.Repeat("a1B2c3", 6),
		"github user-to-server (ghu_)":       "ghu_" + strings.Repeat("d4E5f6", 6),
		"github server-to-server (ghs_)":     "ghs_" + strings.Repeat("g7H8i9", 6),
		"github refresh token (ghr_)":        "ghr_" + strings.Repeat("j0K1l2", 6),
		"github fine-grained PAT":            "github_pat_" + strings.Repeat("A", 60),
		"slack bot token (xoxb-)":            "xoxb-" + strings.Repeat("1", 12),
		"slack legacy token (xoxp-)":         "xoxp-" + strings.Repeat("2", 12),
		"slack incoming webhook":             "https://hooks.slack.com/services/" + strings.Repeat("T", 20),
		"gitlab PAT (glpat-)":                "glpat-" + strings.Repeat("a", 22),
		"jwt (three base64url segments)":     "eyJ" + strings.Repeat("A", 10) + ".eyJ" + strings.Repeat("B", 10) + "." + strings.Repeat("C", 10),
		"google API key (AIza)":              "AIza" + strings.Repeat("A", 35),
		"stripe live secret key (sk_live_)":  "sk_live_" + strings.Repeat("1", 24),
		"bearer authorization header inline": "Authorization: Bearer " + strings.Repeat("x", 25),
	}
	for name, body := range positives {
		t.Run("positive/"+name, func(t *testing.T) {
			t.Parallel()
			violations := scanForSecrets([]byte(body))
			if len(violations) == 0 {
				t.Fatalf("expected the hardened scanner to block %q", body)
			}
			if !hasCode(violations, "POL-001") {
				t.Fatalf("expected POL-001, got %+v", violations)
			}
		})
	}

	negatives := map[string]string{
		"plain 40-char hex hash":         strings.Repeat("ab12cd34", 5),
		"ordinary github repo URL":       "See https://github.com/ydnikolaev/a2ahub for the source.",
		"sentence using the word bearer": "This bearer of good news arrived early, well ahead of schedule.",
		"base64 blob that is not a JWT":  "SGVsbG8gV29ybGQsIHRoaXMgaXMgYSB0ZXN0IGJsb2Igbm90IGEgSldULg==",
	}
	for name, body := range negatives {
		t.Run("negative/"+name, func(t *testing.T) {
			t.Parallel()
			if v := scanForSecrets([]byte(body)); len(v) != 0 {
				t.Fatalf("expected benign content %q to pass, got %+v", body, v)
			}
		})
	}
}

func hasCode(vs []Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func readFileForTest(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
