package contract

import "testing"

func TestParseDescriptorProjection(t *testing.T) {
	t.Parallel()
	raw := []byte(`---
schema: envelope/v2
type: contract
id: XC-atlas-order
schema_format: json-schema-2020-12
generated_from:
  tool: schema-exporter
  source_digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
artifacts:
  - path: schema/order.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
---
# Contract
`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.SchemaFormat != "json-schema-2020-12" || len(descriptor.Artifacts) != 1 {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if descriptor.GeneratedFrom == nil || descriptor.GeneratedFrom.Tool != "schema-exporter" {
		t.Fatalf("generated_from = %+v", descriptor.GeneratedFrom)
	}
}

func TestParseDescriptorRejectsMalformedFrontmatter(t *testing.T) {
	t.Parallel()
	if _, err := ParseDescriptor([]byte("not frontmatter")); err == nil {
		t.Fatal("ParseDescriptor accepted missing frontmatter")
	}
}
