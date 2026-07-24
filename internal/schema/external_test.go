package schema

import (
	"errors"
	"testing"
)

const externalValidSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "name": { "type": "string" }
  },
  "required": ["name"],
  "additionalProperties": false
}`

func TestCompileExternal_ValidatesInstances(t *testing.T) {
	t.Parallel()

	sch, err := CompileExternal("widget", []byte(externalValidSchema))
	if err != nil {
		t.Fatalf("CompileExternal: %v", err)
	}

	if err := sch.ValidateInstance([]byte(`{"name":"Widget"}`)); err != nil {
		t.Errorf("expected a matching instance to validate, got %v", err)
	}

	err = sch.ValidateInstance([]byte(`{"description":"no name"}`))
	if err == nil {
		t.Fatal("expected a missing-required-field instance to be rejected")
	}
	if !errors.Is(err, ErrExternalSchemaViolation) {
		t.Errorf("expected errors.Is(err, ErrExternalSchemaViolation), got %v", err)
	}
}

func TestCompileExternal_MalformedSchemaFailsToCompile(t *testing.T) {
	t.Parallel()

	_, err := CompileExternal("broken", []byte(`{"type": `))
	if err == nil {
		t.Fatal("expected a syntactically broken schema to fail to compile")
	}
	if !errors.Is(err, ErrExternalSchemaCompile) {
		t.Errorf("expected errors.Is(err, ErrExternalSchemaCompile), got %v", err)
	}
}

func TestCompileExternal_InvalidInstanceJSONIsAnError(t *testing.T) {
	t.Parallel()

	sch, err := CompileExternal("widget", []byte(externalValidSchema))
	if err != nil {
		t.Fatalf("CompileExternal: %v", err)
	}

	err = sch.ValidateInstance([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected a non-JSON instance to be an error, not a silent pass")
	}
	if !errors.Is(err, ErrExternalInstanceDecode) {
		t.Errorf("expected errors.Is(err, ErrExternalInstanceDecode), got %v", err)
	}
}

func TestCompileExternal_UnresolvableRemoteRefFailsToCompile(t *testing.T) {
	t.Parallel()

	const withRemoteRef = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "widget": { "$ref": "https://example.invalid/other.schema.json" }
  }
}`

	_, err := CompileExternal("remote-ref", []byte(withRemoteRef))
	if err == nil {
		t.Fatal("expected a schema with an unresolvable remote $ref to fail to compile")
	}
	if !errors.Is(err, ErrExternalSchemaCompile) {
		t.Errorf("expected errors.Is(err, ErrExternalSchemaCompile), got %v", err)
	}
}

func TestCompileExternal_UnresolvableRelativeRefNeverReadsLocalDisk(t *testing.T) {
	t.Parallel()

	// A relative $ref with no scheme is the case that would, under
	// jsonschema/v6's own default loader (FileLoader), be turned into an
	// absolute "file://" path built from the process's working directory
	// and actually read off local disk if such a file happened to exist
	// (see external.go's refusingLoader doc comment). This schema's own
	// $id is deliberately a bare relative filename, and errors.go (a real
	// file in this package's own directory, guaranteed to exist) is named
	// as the $ref target — if this test ever fails to error, it means the
	// loader silently read a real local file instead of refusing.
	const withRelativeRef = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "widget.schema.json",
  "type": "object",
  "properties": {
    "widget": { "$ref": "errors.go" }
  }
}`

	_, err := CompileExternal("relative-ref", []byte(withRelativeRef))
	if err == nil {
		t.Fatal("expected a schema with an unresolvable relative $ref to fail to compile, not read local disk")
	}
	if !errors.Is(err, ErrExternalSchemaCompile) {
		t.Errorf("expected errors.Is(err, ErrExternalSchemaCompile), got %v", err)
	}
}
