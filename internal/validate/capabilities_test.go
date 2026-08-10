package validate

import "testing"

// TestCheckCapabilityMismatch is P5 AC5/US-3's own testing row: "sending a
// push-only payload to a file-only system is refused or warned before
// submit — the axon case." Three states, asserted on decoded Violation
// fields (Code/Class/Severity), never on rendered message text.
func TestCheckCapabilityMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		requiredDelivery string
		declaredDelivery []string
		wantCode         string
		wantSeverity     Severity
	}{
		{
			name:             "declared capability includes the required mode: clean",
			requiredDelivery: CapabilityDeliveryFile,
			declaredDelivery: []string{CapabilityDeliveryFile, CapabilityDeliveryHTTPPush},
		},
		{
			name:             "the axon case: push required, recipient declares file-only: reject",
			requiredDelivery: CapabilityDeliveryHTTPPush,
			declaredDelivery: []string{CapabilityDeliveryFile},
			wantCode:         "POL-019",
			wantSeverity:     SeverityReject,
		},
		{
			name:             "recipient capabilities undeclared (nil): warn, not reject",
			requiredDelivery: CapabilityDeliveryFile,
			declaredDelivery: nil,
			wantCode:         "POL-020",
			wantSeverity:     SeverityWarning,
		},
		{
			name:             "recipient capabilities undeclared (empty slice): warn, same as nil",
			requiredDelivery: CapabilityDeliveryFile,
			declaredDelivery: []string{},
			wantCode:         "POL-020",
			wantSeverity:     SeverityWarning,
		},
		{
			name:             "no required delivery resolved by the caller: nothing to check",
			requiredDelivery: "",
			declaredDelivery: []string{CapabilityDeliveryFile},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := CheckCapabilityMismatch(test.requiredDelivery, test.declaredDelivery)

			if test.wantCode == "" {
				if len(got) != 0 {
					t.Fatalf("CheckCapabilityMismatch(%q, %v) = %+v, want no violation", test.requiredDelivery, test.declaredDelivery, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("CheckCapabilityMismatch(%q, %v) = %+v, want exactly one violation", test.requiredDelivery, test.declaredDelivery, got)
			}
			v := got[0]
			if v.Code != test.wantCode {
				t.Fatalf("Code = %q, want %q", v.Code, test.wantCode)
			}
			if v.Class != ClassPolicy {
				t.Fatalf("Class = %q, want %q", v.Class, ClassPolicy)
			}
			if v.Severity != test.wantSeverity {
				t.Fatalf("Severity = %q, want %q", v.Severity, test.wantSeverity)
			}
			if v.Message == "" {
				t.Fatal("Message is empty, want a human-readable explanation")
			}
		})
	}
}

// TestCheckCapabilityMismatchUndeclaredNeverReadsAsAllowedOrDenied is the
// P-1 two-sided proof this epic's rails demand: the undeclared branch must
// be provably DIFFERENT from both the clean path and the reject path, not
// merely "some third code that happens to exist". Removing the mismatch
// comparison would make every declared case look undeclared (asserted via
// the "clean" fixture below never producing a warning); making undeclared
// unconditionally reject would collapse this test's central distinction.
func TestCheckCapabilityMismatchUndeclaredNeverReadsAsAllowedOrDenied(t *testing.T) {
	t.Parallel()

	clean := CheckCapabilityMismatch(CapabilityDeliveryFile, []string{CapabilityDeliveryFile})
	if len(clean) != 0 {
		t.Fatalf("declared+matching = %+v, want clean (no violation) — undeclared must not be the only silent path", clean)
	}

	reject := CheckCapabilityMismatch(CapabilityDeliveryHTTPPush, []string{CapabilityDeliveryFile})
	if len(reject) != 1 || reject[0].Severity != SeverityReject {
		t.Fatalf("declared+mismatched = %+v, want one SeverityReject violation", reject)
	}

	undeclared := CheckCapabilityMismatch(CapabilityDeliveryHTTPPush, nil)
	if len(undeclared) != 1 || undeclared[0].Severity != SeverityWarning {
		t.Fatalf("undeclared = %+v, want one SeverityWarning violation", undeclared)
	}
	if undeclared[0].Code == reject[0].Code {
		t.Fatalf("undeclared and declared-mismatch share code %q, want distinct codes (different consequences per manifest_policy.go's own REF-013 contrast)", undeclared[0].Code)
	}
}
