package artifact

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/schemas"
)

// TestMintedThreadIDMatchesTheSchemasOwnPattern closes the hole P46 wave 2's
// propagation probe named: `thread_test.go` asserts the minted shape against a
// regex TRANSCRIBED BY HAND from the spec's prose, which proves only that a
// human typed the same thing twice. If the pattern committed to
// base.schema.json ever differs — one wrong character in the class is enough —
// both literals stay green while disagreeing, and the disagreement first
// surfaces as a rejected write on a COUNTERPARTY's CI, where nobody can debug
// it.
//
// So this test does not restate the pattern. It READS it out of the shipped
// schema. Exactly one literal of that regex survives in the repository, and it
// is the one the product actually validates against.
//
// It is an in-package test on purpose: the check that matters is EXHAUSTIVE
// over `base32Alphabet`, not a sample of a few minted values. A sample is what
// a dropped character survives — mint four ids, none happens to contain `z`,
// everything stays green. Every character the minter can emit is asserted
// individually instead.
func TestMintedThreadIDMatchesTheSchemasOwnPattern(t *testing.T) {
	t.Parallel()

	raw, err := schemas.FS.ReadFile("envelope/v1/base.schema.json")
	if err != nil {
		t.Fatalf("read base.schema.json from the embedded corpus: %v", err)
	}
	var doc struct {
		Properties struct {
			Thread struct {
				Pattern string `json:"pattern"`
			} `json:"thread"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode base.schema.json: %v", err)
	}

	if doc.Properties.Thread.Pattern == "" {
		t.Fatal("base.schema.json declares no pattern for `thread` — the grammar is unenforced, " +
			"and this test exists to notice that rather than to pass vacuously")
	}
	var required bool
	for _, r := range doc.Required {
		if r == "thread" {
			required = true
			break
		}
	}
	if !required {
		t.Error("base.schema.json does not require `thread`: an artifact may be drafted off every " +
			"conversation, which is the state P46 exists to make unreachable")
	}

	re, err := regexp.Compile(doc.Properties.Thread.Pattern)
	if err != nil {
		t.Fatalf("base.schema.json's `thread` pattern does not compile in Go's regexp: %v", err)
	}

	// 1. A real mint must satisfy the shipped pattern.
	at := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	got, mintErr := MintThreadIDAt("axon", at, strings.NewReader("\x01\x02\x03\x04"))
	if mintErr != nil {
		t.Fatalf("MintThreadIDAt: %v", mintErr)
	}
	if !re.MatchString(got) {
		t.Errorf("MintThreadIDAt produced %q, which the SHIPPED pattern %q rejects — the minter and "+
			"the validator disagree and a write would be refused on a counterparty's CI",
			got, doc.Properties.Thread.Pattern)
	}

	// 2. EVERY character the minter can emit must be accepted. This is the
	//    half that catches a dropped or reordered character in the schema's
	//    class, which sampling cannot: the minter would keep producing it and
	//    the validator would keep rejecting it, on roughly one write in nine.
	for _, c := range base32Alphabet {
		id := "thread:axon-20260726-" + strings.Repeat(string(c), 4)
		if !re.MatchString(id) {
			t.Errorf("the shipped pattern rejects %q, but %q is in this package's own minting "+
				"alphabet — the schema's character class and base32Alphabet have diverged",
				id, string(c))
		}
	}

	// 3. And a character the minter can NEVER emit must be rejected, so the
	//    pattern is not accidentally permissive (Crockford drops i/l/o/u to
	//    keep ids unambiguous when a human retypes one; a pattern that let
	//    them through would silently reintroduce the confusion).
	for _, c := range "ilou" {
		if strings.ContainsRune(base32Alphabet, c) {
			t.Fatalf("test premise broken: %q IS in base32Alphabet", string(c))
		}
		id := "thread:axon-20260726-" + strings.Repeat(string(c), 4)
		if re.MatchString(id) {
			t.Errorf("the shipped pattern accepts %q, but %q is deliberately excluded from the "+
				"minting alphabet", id, string(c))
		}
	}
}
