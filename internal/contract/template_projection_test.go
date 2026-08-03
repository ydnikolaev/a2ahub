package contract_test

import (
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

func TestCanonicalV2TemplateRendersSchemaValidCarriedSet(t *testing.T) {
	t.Parallel()

	raw, err := template.Render(template.Input{
		Type:           "contract",
		EnvelopeSchema: "envelope/v2",
		ID:             "XC-axon-contract",
		Actor:          template.Actor{Kind: "agent", Name: "contract-bot", Model: "gpt-5"},
		Created:        time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Fields: map[string]string{
			"title":  "Rendered contract carried set",
			"space":  "checkout-core",
			"from":   "axon",
			"to":     "seomatrix",
			"thread": "thread:axon-20260803-c5h9",
		},
	})
	if err != nil {
		t.Fatalf("render canonical contract/v2 template: %v", err)
	}

	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("parse rendered frontmatter: %v", err)
	}
	instance, err := schema.DecodeYAMLInstance(fm.YAML)
	if err != nil {
		t.Fatalf("decode rendered frontmatter: %v", err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("load schema corpus: %v", err)
	}
	violations, err := corpus.ValidateEnvelope("contract", "envelope/v2", instance)
	if err != nil {
		t.Fatalf("validate rendered contract/v2: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("canonical render is not schema-valid: %+v\n%s", violations, raw)
	}

	descriptor, err := contract.ParseDescriptor(raw)
	if err != nil {
		t.Fatalf("parse rendered descriptor projection: %v", err)
	}
	schemaRaw, validRaw, invalidRaw := template.ContractScaffold("contract")
	set, issues := contract.ValidateCarriedSet(contract.ProfileContractSetV2, descriptor, contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: raw},
		Files: []contract.CandidateFile{
			{Path: "schema/contract.schema.json", Kind: contract.CandidateRegular, Raw: schemaRaw},
			{Path: "fixtures/valid/example.json", Kind: contract.CandidateRegular, Raw: validRaw},
			{Path: "fixtures/invalid/example.json", Kind: contract.CandidateRegular, Raw: invalidRaw},
		},
	})
	if len(issues) != 0 {
		t.Fatalf("rendered template carried-set issues: %+v", issues)
	}
	if set.AggregateDigest == "" || len(set.PerFileDigest) != 4 {
		t.Fatalf("rendered template projection did not produce descriptor + three starter files: %+v", set)
	}
}
