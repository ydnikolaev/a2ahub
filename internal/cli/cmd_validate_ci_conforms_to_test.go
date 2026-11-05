package cli

import (
	"strings"
	"testing"
)

// The V3 compatibility check maps each prior fixture to its schema by the
// descriptor's conforms_to (feedback fb-20261104-32440f): a multi-schema
// contract names its fixtures `<schema>--<case>.json`, so no file stem matches
// a schema, and before this the stem rule alone refused every such minor with
// POL-008. The prior descriptor is read AT THE BASELINE COMMIT, not at the
// tip, so these tests build real git history with landGateCandidateWithHistory
// (land_check_test.go, same package) and drive the V3 check over it.

const (
	ciMultiOrderSchemaV1   = `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`
	ciMultiOrderSchemaV2   = `{"type":"object","properties":{"id":{"type":"string"},"note":{"type":"string"}},"required":["id"]}`
	ciMultiInvoiceSchemaV1 = `{"type":"object","properties":{"total":{"type":"number"}},"required":["total"]}`
	ciMultiInvoiceSchemaV2 = `{"type":"object","properties":{"total":{"type":"number"},"memo":{"type":"string"}},"required":["total"]}`
)

// ciMultiSchemaContract is a declared-v2 contract (XC-axon-widget) carrying an
// order and an invoice schema, each with a valid and an invalid fixture named
// `<schema>--<case>.json`. invoiceSchema == "" leaves the invoice half out
// (its schema, fixtures and descriptor entries). declare=false omits
// conforms_to from the fixture entries.
func ciMultiSchemaContract(version, orderSchema, invoiceSchema string, declare bool) map[string]string {
	conformsTo := func(schema string) string {
		if !declare {
			return ""
		}
		return ", conforms_to: schema/" + schema + ".schema.json"
	}
	artifacts := "artifacts:\n" +
		"  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/order--minimal.json, role: valid-fixture, normative: true, media_type: application/json" + conformsTo("order") + "}\n" +
		"  - {path: fixtures/invalid/order--empty.json, role: invalid-fixture, normative: true, media_type: application/json" + conformsTo("order") + "}\n"
	if invoiceSchema != "" {
		artifacts += "  - {path: schema/invoice.schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
			"  - {path: fixtures/valid/invoice--minimal.json, role: valid-fixture, normative: true, media_type: application/json" + conformsTo("invoice") + "}\n" +
			"  - {path: fixtures/invalid/invoice--empty.json, role: invalid-fixture, normative: true, media_type: application/json" + conformsTo("invoice") + "}\n"
	}
	head, _, found := strings.Cut(declaredV2Contract("axon", "widget"), "artifacts:\n")
	if !found {
		panic("declaredV2Contract no longer carries an artifacts block")
	}
	head = strings.Replace(head, `version: "0.0.0"`, `version: "`+version+`"`, 1)

	files := map[string]string{
		"space.yaml":             ciManifestWithFloor("0.19.3"),
		ciContractDescriptorPath: head + artifacts + "---\nBody.\n",
		ciContractDescriptorDir + "/schema/order.schema.json":           orderSchema,
		ciContractDescriptorDir + "/fixtures/valid/order--minimal.json": `{"id":"o-1"}`,
		ciContractDescriptorDir + "/fixtures/invalid/order--empty.json": `null`,
	}
	if invoiceSchema != "" {
		files[ciContractDescriptorDir+"/schema/invoice.schema.json"] = invoiceSchema
		files[ciContractDescriptorDir+"/fixtures/valid/invoice--minimal.json"] = `{"id":"i-1","total":12.5}`
		files[ciContractDescriptorDir+"/fixtures/invalid/invoice--empty.json"] = `null`
	}
	return files
}

// The invoice fixture also satisfies the order schema on purpose: a mapping
// that ignored conforms_to and fell back to "the one remaining schema" would
// then pass it, which is the false pass the removed-schema test must catch.

// ciRunV3Compat runs the V3 PR check over a candidate and returns the exit
// code, the violation codes reported against the descriptor, the first POL-007
// message, and the descriptor's own error text.
func ciRunV3Compat(t *testing.T, base []landCommit, head landCommit) (code int, codes []string, pol007, descriptorErr string) {
	t.Helper()
	c := landGateCandidateWithHistory(t, base, head, "publish\n")
	code, rep, errOut := runCI(t, ciEngine(t), c.CheckoutDir, gitDiffNameOnly, "v3-pr", c.BaseSHA, c.Author)
	for _, a := range rep.Artifacts {
		if a.Path == ciContractDescriptorPath {
			descriptorErr = a.Error
		}
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			codes = append(codes, v.Code)
			if v.Code == "POL-007" && pol007 == "" {
				pol007 = v.Message
			}
		}
	}
	if code != 0 && len(codes) == 0 && descriptorErr == "" {
		t.Fatalf("exit = %d with no violation and no descriptor error; report=%+v stderr=%s", code, rep, errOut)
	}
	return code, codes, pol007, descriptorErr
}

func ciHasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// A multi-schema minor whose baseline is the descriptor at base (nothing
// published yet) passes: the descriptor's conforms_to maps every fixture.
func TestValidateCI_ContractCompat_ConformsToMapsMultiSchemaMinor(t *testing.T) {
	t.Parallel()
	code, codes, _, descErr := ciRunV3Compat(t,
		[]landCommit{{files: ciMultiSchemaContract("1.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, true)}},
		landCommit{files: ciMultiSchemaContract("1.1.0", ciMultiOrderSchemaV2, ciMultiInvoiceSchemaV2, true)})
	if code != 0 || ciHasCode(codes, "POL-008") || ciHasCode(codes, "POL-007") {
		t.Fatalf("exit = %d, codes = %v, descriptor error = %q; want an additive multi-schema minor to pass", code, codes, descErr)
	}
}

// Dropping a schema a prior fixture declared conformance to is the breaking
// change itself: POL-007 naming the fixture, not an unmappable POL-008.
func TestValidateCI_ContractCompat_ConformsToRemovedSchemaIsPOL007(t *testing.T) {
	t.Parallel()
	code, codes, pol007, descErr := ciRunV3Compat(t,
		[]landCommit{{files: ciMultiSchemaContract("1.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, true)}},
		landCommit{
			files: ciMultiSchemaContract("1.1.0", ciMultiOrderSchemaV2, "", true),
			deleted: []string{
				ciContractDescriptorDir + "/schema/invoice.schema.json",
				ciContractDescriptorDir + "/fixtures/valid/invoice--minimal.json",
				ciContractDescriptorDir + "/fixtures/invalid/invoice--empty.json",
			},
		})
	if code != 1 || !ciHasCode(codes, "POL-007") || ciHasCode(codes, "POL-008") {
		t.Fatalf("exit = %d, codes = %v, descriptor error = %q; want POL-007 and no POL-008", code, codes, descErr)
	}
	if !strings.Contains(pol007, "invoice--minimal.json") || strings.Contains(pol007, "order--minimal.json") {
		t.Fatalf("POL-007 message %q must name only the invoice fixture", pol007)
	}
	if !strings.Contains(pol007, "schema/invoice.schema.json") {
		t.Fatalf("POL-007 message %q must name the removed schema schema/invoice.schema.json", pol007)
	}
}

// Without conforms_to anywhere the stem rule alone applies, so the same
// multi-schema layout is still refused as unmappable (POL-008) - this change
// adds a source of truth, it does not loosen the fallback.
func TestValidateCI_ContractCompat_NoConformsToKeepsTheStemRefusal(t *testing.T) {
	t.Parallel()
	code, codes, _, descErr := ciRunV3Compat(t,
		[]landCommit{{files: ciMultiSchemaContract("1.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, false)}},
		landCommit{files: ciMultiSchemaContract("1.1.0", ciMultiOrderSchemaV2, ciMultiInvoiceSchemaV2, false)})
	if code != 1 || !ciHasCode(codes, "POL-008") {
		t.Fatalf("exit = %d, codes = %v, descriptor error = %q; want POL-008 without any conforms_to", code, codes, descErr)
	}
}

// The baseline is a PUBLISHED version here, and the declaration must come from
// the descriptor at the commit that published it. The tip's descriptor was
// reset to 0.0.0 with no conforms_to, so reading the tip would find no mapping.
func TestValidateCI_ContractCompat_ConformsToIsReadAtThePublishedBaseline(t *testing.T) {
	t.Parallel()
	published := ciMultiSchemaContract("1.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, true)
	eventPath, event := landPublishEvent("0", "1.0.0")
	published[eventPath] = event
	code, codes, _, descErr := ciRunV3Compat(t,
		[]landCommit{
			{files: published},
			{files: ciMultiSchemaContract("0.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, false)},
		},
		landCommit{files: ciMultiSchemaContract("1.1.0", ciMultiOrderSchemaV2, ciMultiInvoiceSchemaV2, true)})
	if code != 0 || ciHasCode(codes, "POL-008") || ciHasCode(codes, "POL-007") {
		t.Fatalf("exit = %d, codes = %v, descriptor error = %q; want the published baseline's conforms_to to map the fixtures", code, codes, descErr)
	}
}

// A baseline descriptor whose artifacts cannot be read is an error, never "no
// mapping": nil would hand its fixtures back to the stem rule and report a
// verdict the declaration might have contradicted.
func TestValidateCI_ContractCompat_UnreadablePriorArtifactsIsAnError(t *testing.T) {
	t.Parallel()
	published := ciMultiSchemaContract("1.0.0", ciMultiOrderSchemaV1, ciMultiInvoiceSchemaV1, true)
	// artifacts must be a list of entries; a scalar parses as YAML but not as
	// the descriptor's inventory.
	descriptor := published[ciContractDescriptorPath]
	head, _, found := strings.Cut(descriptor, "artifacts:\n")
	if !found {
		t.Fatalf("fixture descriptor has no artifacts list to corrupt:\n%s", descriptor)
	}
	published[ciContractDescriptorPath] = head + "artifacts: not-a-list\n---\nBody.\n"
	eventPath, event := landPublishEvent("0", "1.0.0")
	published[eventPath] = event
	code, codes, _, descErr := ciRunV3Compat(t,
		[]landCommit{{files: published}},
		landCommit{files: ciMultiSchemaContract("1.1.0", ciMultiOrderSchemaV2, ciMultiInvoiceSchemaV2, true)})
	if code != 1 || !strings.Contains(descErr, "conforms_to declarations") {
		t.Fatalf("exit = %d, codes = %v, descriptor error = %q; want an error naming the unreadable conforms_to declarations", code, codes, descErr)
	}
}
