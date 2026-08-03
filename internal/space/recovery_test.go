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

func mustDecodeRecovery(t *testing.T, raw string) RecoveryV1 {
	t.Helper()
	record, err := DecodeRecoveryV1([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeRecoveryV1: %v", err)
	}
	return record
}
