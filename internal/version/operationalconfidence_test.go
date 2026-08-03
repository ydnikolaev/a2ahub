package version

import "testing"

func TestOperationalConfidenceFloors(t *testing.T) {
	t.Parallel()

	if OperationalConfidenceFloor != "0.19.0" {
		t.Fatalf("OperationalConfidenceFloor = %q, want 0.19.0", OperationalConfidenceFloor)
	}
	if ClaimReceiptFloor != OperationalConfidenceFloor {
		t.Fatalf("ClaimReceiptFloor = %q, want common floor %q", ClaimReceiptFloor, OperationalConfidenceFloor)
	}
	if ProducerStampFloor != "0.10.0" {
		t.Fatalf("ProducerStampFloor = %q, want preserved 0.10.0", ProducerStampFloor)
	}
}
