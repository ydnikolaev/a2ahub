package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDetailBytes      = 1 << 20
	maxDetailCacheBytes = 2 << 20
)

var (
	// ErrDetailMalformed is returned by a DetailDecoder when the standalone
	// notes asset is syntactically or schema-invalid.
	ErrDetailMalformed = errors.New("release: release detail is malformed")
	// ErrDetailUnknownSchema distinguishes a well-formed future schema from a
	// malformed v1 document. Both render the same prose-free fallback.
	ErrDetailUnknownSchema = errors.New("release: release detail schema is unknown")
)

// DetailStatus is safe fallback metadata. It intentionally carries no error
// text because verifier/parser errors may contain untrusted input.
type DetailStatus string

const (
	// DetailVerified permits rendering the validated structured detail.
	DetailVerified DetailStatus = "verified"
	// DetailMissing means the target asset was unavailable.
	DetailMissing DetailStatus = "missing"
	// DetailMalformed means schema validation failed.
	DetailMalformed DetailStatus = "malformed"
	// DetailUnknownSchema means this binary cannot interpret a future schema.
	DetailUnknownSchema DetailStatus = "unknown-schema"
	// DetailUnverified means signer trust was not established.
	DetailUnverified DetailStatus = "unverified"
	// DetailTampered means target hash or signature mismatched.
	DetailTampered DetailStatus = "tampered"
)

// DetailAction and DetailChange mirror the public release-notes/v1 data
// contract but deliberately omit any raw source representation.
type DetailAction struct {
	Scope  string   `json:"scope"`
	Why    string   `json:"why"`
	Detect []string `json:"detect,omitempty"`
	Run    []string `json:"run,omitempty"`
}

// DetailChange is one validated future-release change.
type DetailChange struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	Impact  string       `json:"impact"`
	Subject string       `json:"subject"`
	Detail  string       `json:"detail"`
	Affects []string     `json:"affects,omitempty"`
	Action  DetailAction `json:"action"`
}

// Detail is the structured, schema-validated projection permitted in
// the local cache and update card. It has no Raw field by design.
type Detail struct {
	Schema   string         `json:"schema"`
	Version  string         `json:"version"`
	Released string         `json:"released"`
	Headline string         `json:"headline"`
	Changes  []DetailChange `json:"changes"`
}

// DetailDecoder is the adapter seam to the existing notes parser and
// release-notes schema validator. release stays stdlib-only and does not grow a
// second YAML/schema implementation.
type DetailDecoder interface {
	DecodeAndValidate([]byte) (Detail, error)
}

// DetailCache is the complete network-free update-card detail state.
// Detail is present only when Status is verified.
type DetailCache struct {
	Status        DetailStatus `json:"status"`
	TargetVersion string       `json:"target_version"`
	AssetName     string       `json:"asset_name,omitempty"`
	Schema        string       `json:"schema,omitempty"`
	SHA256        string       `json:"sha256,omitempty"`
	VerifiedAt    time.Time    `json:"verified_at,omitempty"`
	Detail        *Detail      `json:"detail,omitempty"`
}

// DetailOptions supplies already resolved release/cohort facts and the two
// existing policy seams needed to trust and decode the standalone notes asset.
type DetailOptions struct {
	Cohort    VerifiedCohort
	AssetPath string
	Release   Release
	Verifier  Verifier
	Decoder   DetailDecoder
	Now       time.Time
}

// ResolveDetail verifies and decodes a future release-notes asset into a
// safe cache record. Every failure returns explicit fallback metadata and a nil
// Detail; raw or partially decoded prose is never returned.
func ResolveDetail(ctx context.Context, opts DetailOptions) DetailCache {
	component, ok := releaseNotesComponent(opts.Cohort)
	if !ok {
		return detailFallback(DetailUnverified, opts.Release.Version, "", "", "")
	}
	target := opts.Cohort.manifest.ProductVersion
	if target == "" {
		target = opts.Release.Version
	}
	base := detailFallback(DetailMissing, target, component.Name, component.Schema, component.SHA256)
	if opts.Release.Version != target || opts.AssetPath == "" {
		return base
	}
	if component.Schema != ReleaseNotesSchemaV1 {
		base.Status = DetailUnknownSchema
		return base
	}
	if filepath.Base(opts.AssetPath) != component.Name {
		base.Status = DetailTampered
		return base
	}

	info, err := os.Stat(opts.AssetPath)
	if err != nil {
		return base
	}
	if !info.Mode().IsRegular() || info.Size() > maxDetailBytes {
		base.Status = DetailMalformed
		return base
	}
	got, err := sha256File(opts.AssetPath)
	if err != nil || !strings.EqualFold(got, component.SHA256) {
		base.Status = DetailTampered
		return base
	}
	if opts.Verifier == nil {
		base.Status = DetailUnverified
		return base
	}
	if err := opts.Verifier.Verify(ctx, opts.AssetPath, opts.Release); err != nil {
		switch {
		case errors.Is(err, ErrChecksumMismatch), errors.Is(err, ErrSignatureInvalid):
			base.Status = DetailTampered
		default:
			base.Status = DetailUnverified
		}
		return base
	}

	raw, err := readBoundedDetail(opts.AssetPath)
	if err != nil {
		base.Status = DetailMalformed
		return base
	}
	if opts.Decoder == nil {
		base.Status = DetailMalformed
		return base
	}
	detail, err := opts.Decoder.DecodeAndValidate(raw)
	if err != nil {
		if errors.Is(err, ErrDetailUnknownSchema) {
			base.Status = DetailUnknownSchema
		} else {
			base.Status = DetailMalformed
		}
		return base
	}
	if detail.Schema != ReleaseNotesSchemaV1 {
		base.Status = DetailUnknownSchema
		return base
	}
	if detail.Version != target {
		base.Status = DetailMalformed
		return base
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	base.Status = DetailVerified
	base.VerifiedAt = now.UTC()
	base.Detail = cloneDetail(&detail)
	return base
}

func releaseNotesComponent(cohort VerifiedCohort) (CohortComponent, bool) {
	if cohort.manifest.Schema != CohortSchemaV1 {
		return CohortComponent{}, false
	}
	for _, component := range cohort.manifest.Components {
		if component.Kind == ComponentReleaseNotes {
			return component, true
		}
	}
	return CohortComponent{}, false
}

func detailFallback(status DetailStatus, target, name, schema, digest string) DetailCache {
	return DetailCache{
		Status: status, TargetVersion: target, AssetName: name, Schema: schema, SHA256: digest,
	}
}

func readBoundedDetail(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxDetailBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDetailBytes {
		return nil, ErrDetailMalformed
	}
	return raw, nil
}

// WriteDetailCache atomically writes only validated cache shapes. It never
// serializes source bytes because DetailCache has no raw field.
func WriteDetailCache(path string, cache DetailCache) error {
	if err := validateDetailCache(cache, cache.TargetVersion); err != nil {
		return &Error{Op: "WriteDetailCache", Input: path, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	raw, err := json.Marshal(cache)
	if err != nil {
		return &Error{Op: "WriteDetailCache", Input: path, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &Error{Op: "WriteDetailCache", Input: dir, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	tmp, err := os.CreateTemp(dir, ".release-detail-*")
	if err != nil {
		return &Error{Op: "WriteDetailCache", Input: dir, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return &Error{Op: "WriteDetailCache", Input: tmpPath, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	if _, err := tmp.Write(raw); err != nil {
		return &Error{Op: "WriteDetailCache", Input: tmpPath, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	if err := tmp.Sync(); err != nil {
		return &Error{Op: "WriteDetailCache", Input: tmpPath, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	if err := tmp.Close(); err != nil {
		return &Error{Op: "WriteDetailCache", Input: tmpPath, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return &Error{Op: "WriteDetailCache", Input: path, Err: fmt.Errorf("%w: %w", ErrCacheUnavailable, err)}
	}
	cleanup = false
	return nil
}

// ReadDetailCache strictly and boundedly reads a network-free cache record.
// A corrupt cache returns malformed fallback metadata; a cache for another
// target behaves as absent. Neither path leaks a partially decoded Detail.
func ReadDetailCache(path, targetVersion string) (DetailCache, bool) {
	file, err := os.Open(path)
	if err != nil {
		return detailFallback(DetailMissing, targetVersion, "", "", ""), false
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxDetailCacheBytes+1))
	if err != nil || len(raw) > maxDetailCacheBytes {
		return detailFallback(DetailMalformed, targetVersion, "", "", ""), false
	}
	var cache DetailCache
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cache); err != nil {
		return detailFallback(DetailMalformed, targetVersion, "", "", ""), false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return detailFallback(DetailMalformed, targetVersion, "", "", ""), false
	}
	if cache.TargetVersion != targetVersion {
		return detailFallback(DetailMissing, targetVersion, cache.AssetName, cache.Schema, cache.SHA256), false
	}
	if err := validateDetailCache(cache, targetVersion); err != nil {
		return detailFallback(DetailMalformed, targetVersion, cache.AssetName, cache.Schema, cache.SHA256), false
	}
	return cloneDetailCache(cache), true
}

func validateDetailCache(cache DetailCache, targetVersion string) error {
	if cache.TargetVersion == "" || cache.TargetVersion != targetVersion {
		return errors.New("target version mismatch")
	}
	switch cache.Status {
	case DetailVerified:
		if cache.Schema != ReleaseNotesSchemaV1 || cache.Detail == nil ||
			cache.Detail.Schema != ReleaseNotesSchemaV1 || cache.Detail.Version != cache.TargetVersion {
			return errors.New("verified detail binding is incomplete")
		}
	case DetailMissing, DetailMalformed, DetailUnknownSchema, DetailUnverified, DetailTampered:
		if cache.Detail != nil {
			return errors.New("fallback cache contains detail prose")
		}
	default:
		return errors.New("unknown detail status")
	}
	return nil
}

func cloneDetailCache(cache DetailCache) DetailCache {
	cache.Detail = cloneDetail(cache.Detail)
	return cache
}

func cloneDetail(detail *Detail) *Detail {
	if detail == nil {
		return nil
	}
	out := *detail
	out.Changes = append([]DetailChange(nil), detail.Changes...)
	for i := range out.Changes {
		out.Changes[i].Affects = append([]string(nil), detail.Changes[i].Affects...)
		out.Changes[i].Action.Detect = append([]string(nil), detail.Changes[i].Action.Detect...)
		out.Changes[i].Action.Run = append([]string(nil), detail.Changes[i].Action.Run...)
	}
	return &out
}
