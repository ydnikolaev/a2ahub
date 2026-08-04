package datapackage

import (
	"errors"
	"testing"
)

func TestDefaultBoundsAreHonestNumbers(t *testing.T) {
	t.Parallel()

	b := DefaultBounds()
	if b.MaxEntryBytes <= 0 || b.MaxPackageBytes <= 0 || b.MaxEntries <= 0 || b.MaxRecords <= 0 {
		t.Fatalf("DefaultBounds() = %+v, want every field positive", b)
	}
	if b.MaxEntryBytes > b.MaxPackageBytes {
		t.Fatalf("DefaultBounds() per-entry bound %d exceeds package bound %d", b.MaxEntryBytes, b.MaxPackageBytes)
	}
}

func TestCheckEntryBytesRefusesAtBoundPassesOneUnder(t *testing.T) {
	t.Parallel()
	b := Bounds{MaxEntryBytes: 100}

	if err := b.CheckEntryBytes(100); err != nil {
		t.Fatalf("CheckEntryBytes(100) = %v, want nil (at the limit passes)", err)
	}
	if err := b.CheckEntryBytes(99); err != nil {
		t.Fatalf("CheckEntryBytes(99) = %v, want nil (one under passes)", err)
	}
	if err := b.CheckEntryBytes(101); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("CheckEntryBytes(101) = %v, want ErrBoundExceeded", err)
	}
	if err := b.CheckEntryBytes(-1); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("CheckEntryBytes(-1) = %v, want ErrBoundExceeded", err)
	}
}

func TestCheckPackageBytesRefusesAtBoundPassesOneUnder(t *testing.T) {
	t.Parallel()
	b := Bounds{MaxPackageBytes: 1000}

	if err := b.CheckPackageBytes(1000); err != nil {
		t.Fatalf("CheckPackageBytes(1000) = %v, want nil (at the limit passes)", err)
	}
	if err := b.CheckPackageBytes(999); err != nil {
		t.Fatalf("CheckPackageBytes(999) = %v, want nil (one under passes)", err)
	}
	if err := b.CheckPackageBytes(1001); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("CheckPackageBytes(1001) = %v, want ErrBoundExceeded", err)
	}
}

func TestCheckEntryCountRefusesAtBoundPassesOneUnder(t *testing.T) {
	t.Parallel()
	b := Bounds{MaxEntries: 5}

	if err := b.CheckEntryCount(5); err != nil {
		t.Fatalf("CheckEntryCount(5) = %v, want nil (at the limit passes)", err)
	}
	if err := b.CheckEntryCount(4); err != nil {
		t.Fatalf("CheckEntryCount(4) = %v, want nil (one under passes)", err)
	}
	if err := b.CheckEntryCount(6); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("CheckEntryCount(6) = %v, want ErrBoundExceeded", err)
	}
}

func TestCheckRecordCountRefusesAtBoundPassesOneUnder(t *testing.T) {
	t.Parallel()
	b := Bounds{MaxRecords: 10}

	if err := b.CheckRecordCount(10); err != nil {
		t.Fatalf("CheckRecordCount(10) = %v, want nil (at the limit passes)", err)
	}
	if err := b.CheckRecordCount(9); err != nil {
		t.Fatalf("CheckRecordCount(9) = %v, want nil (one under passes)", err)
	}
	if err := b.CheckRecordCount(11); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("CheckRecordCount(11) = %v, want ErrBoundExceeded", err)
	}
}
