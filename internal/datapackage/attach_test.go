package datapackage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

func TestNewAttachmentFromBytes(t *testing.T) {
	t.Parallel()
	data := []byte("hello attachment bytes")

	att, err := NewAttachmentFromBytes(data, "evidence", "", VerificationOffered, "24h", DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromBytes: %v", err)
	}

	wantDigest := artifact.Digest(data)
	if att.Digest != wantDigest {
		t.Fatalf("Digest = %q, want %q", att.Digest, wantDigest)
	}
	if att.Ref != att.Digest {
		t.Fatalf("Ref = %q, want it to equal Digest %q (content-addressed)", att.Ref, att.Digest)
	}
	if att.Role != "evidence" {
		t.Fatalf("Role = %q, want %q", att.Role, "evidence")
	}
	if att.Verification != VerificationOffered {
		t.Fatalf("Verification = %q, want %q", att.Verification, VerificationOffered)
	}
	if att.Retention != "24h" {
		t.Fatalf("Retention = %q, want %q", att.Retention, "24h")
	}
}

func TestNewAttachmentFromBytes_Deterministic(t *testing.T) {
	t.Parallel()
	data := []byte("identical bytes twice")

	a1, err := NewAttachmentFromBytes(data, "", "", VerificationNone, RetentionPinned, DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromBytes (1): %v", err)
	}
	a2, err := NewAttachmentFromBytes(data, "", "", VerificationNone, RetentionPinned, DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromBytes (2): %v", err)
	}
	if a1.Ref != a2.Ref || a1.Digest != a2.Digest {
		t.Fatalf("same bytes produced different content-addressed ids: %+v vs %+v", a1, a2)
	}
}

func TestNewAttachmentFromBytes_RefusesOversized(t *testing.T) {
	t.Parallel()
	bounds := DefaultBounds()
	bounds.MaxEntryBytes = 4

	_, err := NewAttachmentFromBytes([]byte("this is way more than four bytes"), "", "", VerificationRequired, "1h", bounds)
	if !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("NewAttachmentFromBytes over bound = %v, want errors.Is ErrBoundExceeded", err)
	}
}

func TestNewAttachmentFromBytes_RefusesInvalidVerification(t *testing.T) {
	t.Parallel()
	_, err := NewAttachmentFromBytes([]byte("x"), "", "", "maybe", "1h", DefaultBounds())
	if !errors.Is(err, ErrAttachmentVerificationInvalid) {
		t.Fatalf("NewAttachmentFromBytes with bad verification = %v, want errors.Is ErrAttachmentVerificationInvalid", err)
	}
}

func TestNewAttachmentFromBytes_RefusesInvalidRetention(t *testing.T) {
	t.Parallel()
	_, err := NewAttachmentFromBytes([]byte("x"), "", "", VerificationRequired, "not-a-duration", DefaultBounds())
	if !errors.Is(err, ErrAttachmentRetentionInvalid) {
		t.Fatalf("NewAttachmentFromBytes with bad retention = %v, want errors.Is ErrAttachmentRetentionInvalid", err)
	}
}

func TestNewAttachmentFromDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile a.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(`{"b":2}`), 0o644); err != nil {
		t.Fatalf("WriteFile b.json: %v", err)
	}

	att, err := NewAttachmentFromDirectory(context.Background(), dir, "bundle", "", VerificationRequired, RetentionPinned, DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromDirectory: %v", err)
	}

	files, err := WalkLocalDirectory(context.Background(), dir, DefaultBounds())
	if err != nil {
		t.Fatalf("WalkLocalDirectory: %v", err)
	}
	entrySet, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	if att.Digest != entrySet.AggregateDigest {
		t.Fatalf("Digest = %q, want AggregateDigest %q", att.Digest, entrySet.AggregateDigest)
	}
	if att.Ref != att.Digest {
		t.Fatalf("Ref = %q, want it to equal Digest %q", att.Ref, att.Digest)
	}
}

func TestNewAttachmentFromDirectory_RoutesThroughBounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bounds := DefaultBounds()
	bounds.MaxEntryBytes = 4

	_, err := NewAttachmentFromDirectory(context.Background(), dir, "", "", VerificationRequired, "1h", bounds)
	if !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("NewAttachmentFromDirectory over bound = %v, want errors.Is ErrBoundExceeded", err)
	}
}

func TestVerifyAttachment_Success(t *testing.T) {
	t.Parallel()
	data := []byte("verified bytes")
	att, err := NewAttachmentFromBytes(data, "", "", VerificationRequired, "1h", DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromBytes: %v", err)
	}
	if err := VerifyAttachment(att, data); err != nil {
		t.Fatalf("VerifyAttachment on matching bytes = %v, want nil", err)
	}
}

func TestVerifyAttachment_DigestMismatch(t *testing.T) {
	t.Parallel()
	data := []byte("original bytes")
	att, err := NewAttachmentFromBytes(data, "", "", VerificationRequired, "1h", DefaultBounds())
	if err != nil {
		t.Fatalf("NewAttachmentFromBytes: %v", err)
	}

	corrupted := bytes.Clone(data)
	corrupted[0] ^= 0xFF

	err = VerifyAttachment(att, corrupted)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("VerifyAttachment on corrupted bytes = %v, want errors.Is ErrDigestMismatch", err)
	}
	wantComputed := artifact.Digest(corrupted)
	if !strings.Contains(err.Error(), att.Digest) || !strings.Contains(err.Error(), wantComputed) {
		t.Fatalf("VerifyAttachment error %q does not name both declared %q and computed %q digests", err, att.Digest, wantComputed)
	}
}

func TestResolveAttachmentExpiry_Pinned(t *testing.T) {
	t.Parallel()
	got, err := ResolveAttachmentExpiry(RetentionPinned, time.Now())
	if err != nil {
		t.Fatalf("ResolveAttachmentExpiry(pinned): %v", err)
	}
	if got != "" {
		t.Fatalf("ResolveAttachmentExpiry(pinned) = %q, want empty string (no expiry)", got)
	}
}

func TestResolveAttachmentExpiry_Duration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	got, err := ResolveAttachmentExpiry("24h", now)
	if err != nil {
		t.Fatalf("ResolveAttachmentExpiry(24h): %v", err)
	}
	want := now.Add(24 * time.Hour).UTC().Format(time.RFC3339)
	if got != want {
		t.Fatalf("ResolveAttachmentExpiry(24h) = %q, want %q", got, want)
	}
}

func TestResolveAttachmentExpiry_RefusesInvalid(t *testing.T) {
	t.Parallel()
	_, err := ResolveAttachmentExpiry("banana", time.Now())
	if !errors.Is(err, ErrAttachmentRetentionInvalid) {
		t.Fatalf("ResolveAttachmentExpiry(banana) = %v, want errors.Is ErrAttachmentRetentionInvalid", err)
	}
}

func TestResolveAttachmentExpiry_RefusesNegativeDuration(t *testing.T) {
	t.Parallel()
	_, err := ResolveAttachmentExpiry("-1h", time.Now())
	if !errors.Is(err, ErrAttachmentRetentionInvalid) {
		t.Fatalf("ResolveAttachmentExpiry(-1h) = %v, want errors.Is ErrAttachmentRetentionInvalid", err)
	}
}

func TestNewAttachmentManifestEntry_PinnedCarriesNoExpiry(t *testing.T) {
	t.Parallel()
	att := Attachment{
		Ref:          "sha256:" + strings.Repeat("a", 64),
		Digest:       "sha256:" + strings.Repeat("a", 64),
		Verification: VerificationOffered,
		Retention:    RetentionPinned,
	}

	entry, err := NewAttachmentManifestEntry(att, time.Now())
	if err != nil {
		t.Fatalf("NewAttachmentManifestEntry: %v", err)
	}
	if entry.ExpiresAt != "" {
		t.Fatalf("ExpiresAt = %q, want empty (pinned retention carries no expiry)", entry.ExpiresAt)
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), "expires_at") {
		t.Fatalf("marshaled pinned entry %s carries an expires_at key, want the field entirely absent", raw)
	}
}

func TestNewAttachmentManifestEntry_DurationCarriesExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	att := Attachment{
		Ref:          "sha256:" + strings.Repeat("b", 64),
		Digest:       "sha256:" + strings.Repeat("b", 64),
		Verification: VerificationRequired,
		Retention:    "168h",
	}

	entry, err := NewAttachmentManifestEntry(att, now)
	if err != nil {
		t.Fatalf("NewAttachmentManifestEntry: %v", err)
	}
	want := now.Add(168 * time.Hour).UTC().Format(time.RFC3339)
	if entry.ExpiresAt != want {
		t.Fatalf("ExpiresAt = %q, want %q", entry.ExpiresAt, want)
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"expires_at":"`+want+`"`) {
		t.Fatalf("marshaled entry %s does not carry expires_at %q", raw, want)
	}
}

func TestVerificationEnum_ClosedSet(t *testing.T) {
	t.Parallel()
	valid := []string{VerificationRequired, VerificationOffered, VerificationNone}
	for _, v := range valid {
		if err := validateVerification(v); err != nil {
			t.Fatalf("validateVerification(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{"", "unverified", "pass", "PASS"}
	for _, v := range invalid {
		if err := validateVerification(v); !errors.Is(err, ErrAttachmentVerificationInvalid) {
			t.Fatalf("validateVerification(%q) = %v, want errors.Is ErrAttachmentVerificationInvalid", v, err)
		}
	}
}
