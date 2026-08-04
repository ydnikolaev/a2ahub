package datapackage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// dataPackageFixtureRoot is this package's own path back to the shipped
// data-package/v1 fixture corpus (schemas/data-package/v1/fixtures), the
// same fixtures schemas/data-package/v1/schema_test.go and
// internal/schema/corpus_datapackage_test.go already prove schema-valid —
// this file additionally proves the Go Document type round-trips them
// byte-identically and that a fixture this package decodes still validates
// against its own schema.
const dataPackageFixtureRoot = "../../schemas/data-package/v1/fixtures"

func TestDocument_RoundTripsFixturesByteIdentically(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(dataPackageFixtureRoot, "valid", "*.json"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no valid data-package fixtures found")
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			original := readFile(t, path)

			var doc Document
			if err := json.Unmarshal(original, &doc); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}

			reencoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}

			assertJSONByteIdentical(t, original, reencoded)

			var instance any
			if err := json.Unmarshal(reencoded, &instance); err != nil {
				t.Fatalf("json.Unmarshal(reencoded) for schema check: %v", err)
			}
			violations, vErr := corpus.ValidateDataPackage(instance)
			if vErr != nil {
				t.Fatalf("ValidateDataPackage: %v", vErr)
			}
			if len(violations) != 0 {
				t.Fatalf("re-encoded document failed its own schema: %+v", violations)
			}
		})
	}
}

// readFile is a small local helper (os.ReadFile plus t.Fatal on error) —
// entryset_test.go and id_test.go do not need file I/O, so this lives here
// rather than in a shared _test.go helper file this package does not have.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// assertJSONByteIdentical compares a and b after canonicalizing both
// through json.Compact — the standard interpretation of "byte-identical"
// for a JSON wire round trip: whitespace is not part of the wire shape,
// field values and field presence are.
func assertJSONByteIdentical(t *testing.T, a, b []byte) {
	t.Helper()
	var bufA, bufB bytes.Buffer
	if err := json.Compact(&bufA, a); err != nil {
		t.Fatalf("json.Compact(original): %v", err)
	}
	if err := json.Compact(&bufB, b); err != nil {
		t.Fatalf("json.Compact(re-encoded): %v", err)
	}
	if bufA.String() != bufB.String() {
		t.Fatalf("round trip is not byte-identical after compaction:\noriginal:  %s\nre-encoded: %s", bufA.String(), bufB.String())
	}
}
