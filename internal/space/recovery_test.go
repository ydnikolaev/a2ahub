package space

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const recoveryGolden = `{"artifact_ids":["XC-atlas-demo","XE-01K1A2B3C4D5E6F7G8H9J0K1M2"],"base_branch":"main","candidate_intent_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","flags":{"allow_fork_fallback":false,"allow_space_infrastructure":false,"replace_orphan_branch":false},"head_branch":"a2a/atlas/contract-publish/op-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","intent_key":"op-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","operation_key":"op-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","plan_digest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","pr_body":"Publish XC-atlas-demo@2.0.0","pr_title":"Publish XC-atlas-demo@2.0.0","prepared_digest":"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","repository":"acme/space","system":"atlas","target":"XC-atlas-demo@2.0.0","verb":"contract-publish","version":1,"version_selector":"auto:major"}`

const recoveryGoldenDigest = "sha256:526ae7a11adafde9bb062a3a03f4316bcb81eeb10ed4dc5986a449126e8ce183"

func TestRecoveryV1GoldenRoundTrip(t *testing.T) {
	t.Parallel()

	record, err := DecodeRecoveryV1([]byte(recoveryGolden))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1: %v", err)
	}
	encoded, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatalf("EncodeRecoveryV1: %v", err)
	}
	if string(encoded) != recoveryGolden {
		t.Fatalf("re-encoded recovery differs\n got: %s\nwant: %s", encoded, recoveryGolden)
	}
	if got := RecoveryDigest(encoded); got != recoveryGoldenDigest {
		t.Fatalf("RecoveryDigest = %q, want %q", got, recoveryGoldenDigest)
	}

	trailer, digest, err := EncodeRecoveryV1Trailer(record)
	if err != nil {
		t.Fatalf("EncodeRecoveryV1Trailer: %v", err)
	}
	if strings.Contains(trailer, "=") || digest != recoveryGoldenDigest {
		t.Fatalf("trailer/digest = %q/%q, want unpadded base64url and golden digest", trailer, digest)
	}
	decoded, err := DecodeRecoveryV1Trailer(trailer, digest)
	if err != nil {
		t.Fatalf("DecodeRecoveryV1Trailer: %v", err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded trailer = %+v, want %+v", decoded, record)
	}
}

func TestRecoveryV1StrictDecoderRejectsNonCanonicalWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{"unknown field", strings.Replace(recoveryGolden, `"version":1`, `"unknown":false,"version":1`, 1)},
		{"duplicate field", strings.Replace(recoveryGolden, `"version":1`, `"version":1,"version":1`, 1)},
		{"missing field", strings.Replace(recoveryGolden, `"pr_body":"Publish XC-atlas-demo@2.0.0",`, "", 1)},
		{"null field", strings.Replace(recoveryGolden, `"pr_body":"Publish XC-atlas-demo@2.0.0"`, `"pr_body":null`, 1)},
		{"wrong type", strings.Replace(recoveryGolden, `"version":1`, `"version":"1"`, 1)},
		{"noncanonical whitespace", " " + recoveryGolden},
		{"noncanonical field order", strings.Replace(recoveryGolden,
			`"artifact_ids":["XC-atlas-demo","XE-01K1A2B3C4D5E6F7G8H9J0K1M2"],"base_branch":"main"`,
			`"base_branch":"main","artifact_ids":["XC-atlas-demo","XE-01K1A2B3C4D5E6F7G8H9J0K1M2"]`, 1)},
		{"unsorted artifact ids", strings.Replace(recoveryGolden,
			`["XC-atlas-demo","XE-01K1A2B3C4D5E6F7G8H9J0K1M2"]`,
			`["XE-01K1A2B3C4D5E6F7G8H9J0K1M2","XC-atlas-demo"]`, 1)},
		{"duplicate artifact id", strings.Replace(recoveryGolden,
			`["XC-atlas-demo","XE-01K1A2B3C4D5E6F7G8H9J0K1M2"]`,
			`["XC-atlas-demo","XC-atlas-demo"]`, 1)},
		{"true flag", strings.Replace(recoveryGolden, `"allow_fork_fallback":false`, `"allow_fork_fallback":true`, 1)},
		{"unknown flag", strings.Replace(recoveryGolden, `"replace_orphan_branch":false`, `"replace_orphan_branch":false,"unknown":false`, 1)},
		{"wrong verb", strings.Replace(recoveryGolden, `"verb":"contract-publish"`, `"verb":"submit"`, 1)},
		{"wrong version", strings.Replace(recoveryGolden, `"version":1`, `"version":2`, 1)},
		{"noncanonical digest", strings.Replace(recoveryGolden, "sha256:cccc", "SHA256:cccc", 1)},
		{"bad operation key", strings.Replace(recoveryGolden, "op-v1-aaaaaaaa", "op-v1-zzzzzzzz", 1)},
		{"head mismatch", strings.Replace(recoveryGolden,
			`"head_branch":"a2a/atlas/contract-publish/op-v1-aaaaaaaa`,
			`"head_branch":"a2a/atlas/contract-publish/op-v1-bbbbbbbb`, 1)},
		{"noncanonical selector", strings.Replace(recoveryGolden, `"version_selector":"auto:major"`, `"version_selector":"auto:other"`, 1)},
		{"noncanonical target version", strings.Replace(recoveryGolden, `"target":"XC-atlas-demo@2.0.0"`, `"target":"XC-atlas-demo@02.0.0"`, 1)},
		{"nul title", strings.Replace(recoveryGolden, `"pr_title":"Publish`, `"pr_title":"\u0000Publish`, 1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeRecoveryV1([]byte(tc.raw)); !errors.Is(err, ErrRecoveryInvalid) {
				t.Fatalf("DecodeRecoveryV1 error = %v, want ErrRecoveryInvalid", err)
			}
		})
	}
}

func TestRecoveryV1BoundsAndDigest(t *testing.T) {
	t.Parallel()

	record, err := DecodeRecoveryV1([]byte(recoveryGolden))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1: %v", err)
	}
	record.PRTitle = strings.Repeat("界", 257)
	if _, err := EncodeRecoveryV1(record); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("257-scalar title error = %v, want ErrRecoveryInvalid", err)
	}
	record = mustDecodeRecovery(t, recoveryGolden)
	record.PRBody = strings.Repeat("x", recoveryMaxPRBodyBytes+1)
	if _, err := EncodeRecoveryV1(record); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("over-bound body error = %v, want ErrRecoveryInvalid", err)
	}

	if _, err := DecodeRecoveryV1(make([]byte, recoveryMaxJSONBytes+1)); !errors.Is(err, ErrRecoveryTooLarge) {
		t.Fatalf("over-bound JSON error = %v, want ErrRecoveryTooLarge", err)
	}
	trailer, _, err := EncodeRecoveryV1Trailer(mustDecodeRecovery(t, recoveryGolden))
	if err != nil {
		t.Fatalf("EncodeRecoveryV1Trailer: %v", err)
	}
	if _, err := DecodeRecoveryV1Trailer(trailer, "sha256:"+strings.Repeat("0", 64)); !errors.Is(err, ErrRecoveryDigestMismatch) {
		t.Fatalf("digest mismatch error = %v, want ErrRecoveryDigestMismatch", err)
	}
}

// TestRecoveryV1DecodesLegacyRecordWithoutMintedByVersion is the
// compatibility guarantee this work exists to protect: recoveryGolden is the
// pre-existing wire shape, minted before minted_by_version existed, and it
// MUST keep decoding — an old head must not become permanently unrecoverable
// the moment a new optional field is added. Absence decodes to the empty
// string, which a verifier reads as "this record predates version
// recording", not as an unknown/rejected field.
func TestRecoveryV1DecodesLegacyRecordWithoutMintedByVersion(t *testing.T) {
	t.Parallel()

	record, err := DecodeRecoveryV1([]byte(recoveryGolden))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1(legacy, no minted_by_version) error = %v, want nil", err)
	}
	if record.MintedByVersion != "" {
		t.Fatalf("MintedByVersion = %q, want empty (absent) for a pre-existing record", record.MintedByVersion)
	}
	// The compatibility guarantee is round-trip, not just decode: an absent
	// optional field must re-encode to the exact original legacy bytes, or
	// DecodeRecoveryV1's own "JSON is not canonical" self-check would make
	// this same legacy record unable to decode at all.
	encoded, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatalf("EncodeRecoveryV1(legacy): %v", err)
	}
	if string(encoded) != recoveryGolden {
		t.Fatalf("re-encoded legacy record differs\n got: %s\nwant: %s", encoded, recoveryGolden)
	}
}

// TestRecoveryV1MintedByVersionOptionalFieldClosure proves the closure this
// work must not weaken: recoveryV1OptionalFields adds exactly ONE new
// acceptable key. A present, non-null minted_by_version decodes; a present,
// null minted_by_version is refused exactly like a null required field; and
// any OTHER never-declared key is refused exactly as before, even sitting
// right beside a valid minted_by_version.
func TestRecoveryV1MintedByVersionOptionalFieldClosure(t *testing.T) {
	t.Parallel()

	withMintedByVersion := strings.Replace(recoveryGolden,
		`"intent_key":"op-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","operation_key"`,
		`"intent_key":"op-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","minted_by_version":"0.19.0","operation_key"`, 1)
	if withMintedByVersion == recoveryGolden {
		t.Fatal("test fixture did not insert minted_by_version — golden layout changed")
	}

	record, err := DecodeRecoveryV1([]byte(withMintedByVersion))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1(with minted_by_version) error = %v, want nil", err)
	}
	if record.MintedByVersion != "0.19.0" {
		t.Fatalf("MintedByVersion = %q, want %q", record.MintedByVersion, "0.19.0")
	}
	encoded, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatalf("EncodeRecoveryV1(with minted_by_version): %v", err)
	}
	if string(encoded) != withMintedByVersion {
		t.Fatalf("re-encoded record differs\n got: %s\nwant: %s", encoded, withMintedByVersion)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{"null minted_by_version is refused like a null required field",
			strings.Replace(withMintedByVersion, `"minted_by_version":"0.19.0"`, `"minted_by_version":null`, 1)},
		{"an unrelated unknown field beside a valid minted_by_version is still refused",
			strings.Replace(withMintedByVersion, `"operation_key"`, `"unknown_field":"x","operation_key"`, 1)},
		{"minted_by_version over its byte bound is refused",
			strings.Replace(withMintedByVersion, `"minted_by_version":"0.19.0"`,
				`"minted_by_version":"`+strings.Repeat("9", recoveryMaxMintedByVersionLen+1)+`"`, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeRecoveryV1([]byte(tc.raw)); !errors.Is(err, ErrRecoveryInvalid) {
				t.Fatalf("DecodeRecoveryV1 error = %v, want ErrRecoveryInvalid", err)
			}
		})
	}
}

func mustDecodeRecovery(t *testing.T, raw string) RecoveryV1 {
	t.Helper()
	record, err := DecodeRecoveryV1([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1: %v", err)
	}
	return record
}
