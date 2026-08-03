package space

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

const (
	recoveryVersion         = 1
	recoveryMaxJSONBytes    = 12 * 1024
	recoveryMaxArtifactIDs  = 16
	recoveryMaxPRTitleRunes = 256
	recoveryMaxPRBodyBytes  = 8 * 1024
)

var (
	ErrRecoveryInvalid        = errors.New("space: invalid contract-publish recovery-v1 record")
	ErrRecoveryTooLarge       = errors.New("space: contract-publish recovery-v1 record exceeds 12 KiB")
	ErrRecoveryDigestMismatch = errors.New("space: contract-publish recovery-v1 digest mismatch")
)

// RecoveryFlagsV1 is the closed behavior flag set frozen into a recovery-v1
// record. Contract publication v1 requires every flag to be false.
type RecoveryFlagsV1 struct {
	AllowForkFallback        bool `json:"allow_fork_fallback"`
	AllowSpaceInfrastructure bool `json:"allow_space_infrastructure"`
	ReplaceOrphanBranch      bool `json:"replace_orphan_branch"`
}

// RecoveryV1 is the closed, non-secret contract-publish lost-ack wire record.
// It intentionally contains no credential, absolute path, provider response,
// or arbitrary commit trailer.
type RecoveryV1 struct {
	ArtifactIDs           []string        `json:"artifact_ids"`
	BaseBranch            string          `json:"base_branch"`
	CandidateIntentDigest string          `json:"candidate_intent_digest"`
	Flags                 RecoveryFlagsV1 `json:"flags"`
	HeadBranch            string          `json:"head_branch"`
	IntentKey             string          `json:"intent_key"`
	OperationKey          string          `json:"operation_key"`
	PlanDigest            string          `json:"plan_digest"`
	PRBody                string          `json:"pr_body"`
	PRTitle               string          `json:"pr_title"`
	PreparedDigest        string          `json:"prepared_digest"`
	Repository            string          `json:"repository"`
	System                string          `json:"system"`
	Target                string          `json:"target"`
	Verb                  string          `json:"verb"`
	Version               int             `json:"version"`
	VersionSelector       string          `json:"version_selector"`
}

var recoveryV1Fields = map[string]struct{}{
	"artifact_ids": {}, "base_branch": {}, "candidate_intent_digest": {}, "flags": {},
	"head_branch": {}, "intent_key": {}, "operation_key": {}, "plan_digest": {},
	"pr_body": {}, "pr_title": {}, "prepared_digest": {}, "repository": {},
	"system": {}, "target": {}, "verb": {}, "version": {}, "version_selector": {},
}

// EncodeRecoveryV1 validates and emits the one canonical JSON representation.
func EncodeRecoveryV1(record RecoveryV1) ([]byte, error) {
	if err := validateRecoveryV1(record); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("%w: encode: %w", ErrRecoveryInvalid, err)
	}
	raw := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(raw) > recoveryMaxJSONBytes {
		return nil, ErrRecoveryTooLarge
	}
	return append([]byte(nil), raw...), nil
}

// DecodeRecoveryV1 accepts only canonical JSON. Unknown, duplicate, missing,
// nullable, reordered, or otherwise noncanonical input is rejected.
func DecodeRecoveryV1(raw []byte) (RecoveryV1, error) {
	if len(raw) > recoveryMaxJSONBytes {
		return RecoveryV1{}, ErrRecoveryTooLarge
	}
	if len(raw) == 0 {
		return RecoveryV1{}, fmt.Errorf("%w: empty input", ErrRecoveryInvalid)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return RecoveryV1{}, fmt.Errorf("%w: %w", ErrRecoveryInvalid, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return RecoveryV1{}, fmt.Errorf("%w: decode object: %w", ErrRecoveryInvalid, err)
	}
	if len(fields) != len(recoveryV1Fields) {
		return RecoveryV1{}, fmt.Errorf("%w: field set is not closed", ErrRecoveryInvalid)
	}
	for name := range recoveryV1Fields {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return RecoveryV1{}, fmt.Errorf("%w: required field %q is missing or null", ErrRecoveryInvalid, name)
		}
	}
	for name := range fields {
		if _, ok := recoveryV1Fields[name]; !ok {
			return RecoveryV1{}, fmt.Errorf("%w: unknown field %q", ErrRecoveryInvalid, name)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record RecoveryV1
	if err := decoder.Decode(&record); err != nil {
		return RecoveryV1{}, fmt.Errorf("%w: decode: %w", ErrRecoveryInvalid, err)
	}
	if err := validateRecoveryV1(record); err != nil {
		return RecoveryV1{}, err
	}
	canonical, err := EncodeRecoveryV1(record)
	if err != nil {
		return RecoveryV1{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return RecoveryV1{}, fmt.Errorf("%w: JSON is not canonical", ErrRecoveryInvalid)
	}
	return record, nil
}

// RecoveryDigest returns the typed SHA-256 of exact canonical recovery bytes.
func RecoveryDigest(canonical []byte) string {
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// EncodeRecoveryV1Trailer returns the unpadded base64url trailer payload and
// its digest. Both derive from the same exact canonical JSON bytes.
func EncodeRecoveryV1Trailer(record RecoveryV1) (encoded, digest string, err error) {
	raw, err := EncodeRecoveryV1(record)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), RecoveryDigest(raw), nil
}

// DecodeRecoveryV1Trailer verifies the separately carried digest before
// accepting the strict canonical record.
func DecodeRecoveryV1Trailer(encoded, expectedDigest string) (RecoveryV1, error) {
	if len(encoded) > base64.RawURLEncoding.EncodedLen(recoveryMaxJSONBytes) {
		return RecoveryV1{}, ErrRecoveryTooLarge
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return RecoveryV1{}, fmt.Errorf("%w: invalid base64url", ErrRecoveryInvalid)
	}
	if RecoveryDigest(raw) != expectedDigest {
		return RecoveryV1{}, ErrRecoveryDigestMismatch
	}
	return DecodeRecoveryV1(raw)
}

func validateRecoveryV1(record RecoveryV1) error {
	invalid := func(field string) error {
		return fmt.Errorf("%w: %s", ErrRecoveryInvalid, field)
	}
	if record.Version != recoveryVersion {
		return invalid("version must be integer 1")
	}
	if len(record.ArtifactIDs) < 1 || len(record.ArtifactIDs) > recoveryMaxArtifactIDs {
		return invalid("artifact_ids must contain 1 to 16 values")
	}
	for i, id := range record.ArtifactIDs {
		if !validRecoveryArtifactID(id) {
			return invalid("artifact_ids contains a noncanonical id")
		}
		if i > 0 && record.ArtifactIDs[i-1] >= id {
			return invalid("artifact_ids must be unique and bytewise sorted")
		}
	}
	if !validRecoveryBranch(record.BaseBranch) {
		return invalid("base_branch is not canonical")
	}
	if !validSystemID(record.System) {
		return invalid("system is not canonical")
	}
	if record.Verb != "contract-publish" {
		return invalid("verb must be contract-publish")
	}
	if !operation.Valid(record.OperationKey) || !operation.Valid(record.IntentKey) {
		return invalid("operation_key and intent_key must be canonical op-v1 keys")
	}
	wantHead := BranchName(record.System, record.Verb, record.OperationKey)
	if record.HeadBranch != wantHead || !validRecoveryBranch(record.HeadBranch) {
		return invalid("head_branch does not match the operation-derived branch")
	}
	for _, digest := range []string{
		record.CandidateIntentDigest, record.PlanDigest, record.PreparedDigest,
	} {
		if !validTypedSHA256(digest) {
			return invalid("digest is not canonical lowercase sha256")
		}
	}
	if record.Flags.AllowForkFallback || record.Flags.AllowSpaceInfrastructure || record.Flags.ReplaceOrphanBranch {
		return invalid("contract-publish recovery-v1 flags must all be false")
	}
	if !validRecoveryRepository(record.Repository) {
		return invalid("repository is not canonical owner/name")
	}
	if !validRecoveryTarget(record.Target) {
		return invalid("target is not canonical XC-id@semver")
	}
	if !utf8.ValidString(record.PRTitle) || record.PRTitle == "" || strings.ContainsRune(record.PRTitle, '\x00') ||
		utf8.RuneCountInString(record.PRTitle) > recoveryMaxPRTitleRunes {
		return invalid("pr_title is outside its UTF-8/scalar bounds")
	}
	if !utf8.ValidString(record.PRBody) || strings.ContainsRune(record.PRBody, '\x00') || len(record.PRBody) > recoveryMaxPRBodyBytes {
		return invalid("pr_body is outside its UTF-8/byte bounds")
	}
	if !validVersionSelector(record.VersionSelector) {
		return invalid("version_selector is not canonical")
	}
	return nil
}

func validRecoveryArtifactID(id string) bool {
	if _, err := artifact.ParseID(id); err == nil {
		return true
	}
	if !strings.HasPrefix(id, "XE-") {
		return false
	}
	parsed, err := artifact.ParseULID(strings.TrimPrefix(id, "XE-"))
	return err == nil && "XE-"+parsed.String() == id
}

func validTypedSHA256(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, ch := range value[len(prefix):] {
		if !('0' <= ch && ch <= '9') && !('a' <= ch && ch <= 'f') {
			return false
		}
	}
	return true
}

func validRecoveryTarget(target string) bool {
	idRaw, versionRaw, ok := strings.Cut(target, "@")
	if !ok || strings.Contains(versionRaw, "@") {
		return false
	}
	id, err := artifact.ParseID(idRaw)
	if err != nil || id.Prefix != "XC" || id.Class != artifact.ClassStanding {
		return false
	}
	canonical, err := version.Canonical(versionRaw)
	return err == nil && canonical == versionRaw
}

func validVersionSelector(selector string) bool {
	switch selector {
	case "auto:patch", "auto:minor", "auto:major":
		return true
	}
	versionRaw, ok := strings.CutPrefix(selector, "explicit:")
	if !ok {
		return false
	}
	canonical, err := version.Canonical(versionRaw)
	return err == nil && canonical == versionRaw
}

func validRecoveryRepository(repository string) bool {
	if len(repository) == 0 || len(repository) > 256 || strings.Count(repository, "/") != 1 {
		return false
	}
	owner, name, _ := strings.Cut(repository, "/")
	for _, part := range []string{owner, name} {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, ch := range part {
			if !('a' <= ch && ch <= 'z') && !('A' <= ch && ch <= 'Z') &&
				!('0' <= ch && ch <= '9') && ch != '-' && ch != '_' && ch != '.' {
				return false
			}
		}
	}
	return true
}

func validRecoveryBranch(branch string) bool {
	if branch == "" || len(branch) > 255 || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") || strings.Contains(branch, "@{") || branch == "@" {
		return false
	}
	for _, ch := range branch {
		if ch <= ' ' || ch == 0x7f || strings.ContainsRune("~^:?*[\\", ch) {
			return false
		}
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}
