package space

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/operation"
)

const (
	MutationWrite  MutationOperation = "write"
	MutationDelete MutationOperation = "delete"

	contractSidecarDeleteDomain = "contract-sidecar-v1"
	maxMutationCount            = 256
	maxMutationBytes            = 16 * 1024 * 1024
	maxMutationAggregateBytes   = 32 * 1024 * 1024
)

var (
	ErrMutationInvalid       = errors.New("space: invalid mutation")
	ErrMutationDuplicatePath = errors.New("space: duplicate mutation path")
	ErrMutationUnauthorized  = errors.New("space: delete mutation is not authorized by an invocation-local permit")
	ErrMutationPrecondition  = errors.New("space: mutation precondition does not match the current file")
)

// MutationOperation is the closed shared-funnel operation set.
type MutationOperation string

// MutationPrecondition binds a reviewed replacement or deletion to the exact
// prior regular file. Mode is a canonical Git mode (100644 or 100755).
type MutationPrecondition struct {
	Digest string `json:"digest"`
	Mode   string `json:"mode"`
}

// Mutation is one exact desired tree change. The delete capability is
// deliberately unexported and therefore absent from every JSON round trip.
// External callers may describe delete intent, but cannot authorize it.
type Mutation struct {
	Path      string            `json:"path"`
	Operation MutationOperation `json:"operation"`
	Bytes     []byte            `json:"bytes,omitempty"`
	// Mode is empty for the canonical 100644 default and 100755 for an
	// executable result. It is independent of ExpectedBefore so a publication
	// can intentionally change a file mode.
	Mode           string                `json:"mode,omitempty"`
	ExpectedBefore *MutationPrecondition `json:"expected_before,omitempty"`
	ContractRoot   string                `json:"contract_root,omitempty"`
	PlanDigest     string                `json:"plan_digest,omitempty"`

	deletePermit *contractSidecarDeletePermit
}

type contractSidecarDeleteBinding struct {
	OperationKey string
	PlanDigest   string
	ContractRoot string
	Path         string
	PriorDigest  string
	PriorMode    string
}

type contractSidecarDeletePermit struct {
	authority *mutationAuthority
	mac       [sha256.Size]byte
}

type mutationAuthority struct {
	secret [sha256.Size]byte
	err    error
}

func newMutationAuthority() *mutationAuthority {
	authority := &mutationAuthority{}
	_, authority.err = rand.Read(authority.secret[:])
	return authority
}

// mintContractSidecarDelete constructs the only authorized delete shape.
// It is package-private so the P6 publication adapter can call it only after
// accepting and re-verifying an immutable publication plan.
func (f *WriteFunnel) mintContractSidecarDelete(binding contractSidecarDeleteBinding) (Mutation, error) {
	if err := validateDeleteBinding(binding); err != nil {
		return Mutation{}, err
	}
	if f.mutationAuthority == nil || f.mutationAuthority.err != nil {
		return Mutation{}, fmt.Errorf("%w: permit entropy unavailable", ErrMutationUnauthorized)
	}
	permit := &contractSidecarDeletePermit{authority: f.mutationAuthority}
	permit.mac = mutationPermitMAC(f.mutationAuthority.secret[:], binding)
	return Mutation{
		Path: binding.Path, Operation: MutationDelete,
		ExpectedBefore: &MutationPrecondition{Digest: binding.PriorDigest, Mode: binding.PriorMode},
		ContractRoot:   binding.ContractRoot, PlanDigest: binding.PlanDigest,
		deletePermit: permit,
	}, nil
}

func normalizeMutations(files []FileWrite, mutations []Mutation) ([]Mutation, error) {
	if len(files)+len(mutations) == 0 || len(files)+len(mutations) > maxMutationCount {
		return nil, fmt.Errorf("%w: mutation count must be 1 to %d", ErrMutationInvalid, maxMutationCount)
	}
	out := make([]Mutation, 0, len(files)+len(mutations))
	for _, file := range files {
		out = append(out, Mutation{
			Path: file.Path, Operation: MutationWrite,
			Bytes: append([]byte{}, file.Content...),
		})
	}
	for _, mutation := range mutations {
		out = append(out, cloneMutation(mutation))
	}
	seen := make(map[string]struct{}, len(out))
	for i := range out {
		if _, duplicate := seen[out[i].Path]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrMutationDuplicatePath, out[i].Path)
		}
		seen[out[i].Path] = struct{}{}
	}
	return out, nil
}

func cloneMutation(mutation Mutation) Mutation {
	clone := mutation
	clone.Bytes = append([]byte(nil), mutation.Bytes...)
	if mutation.Bytes != nil && clone.Bytes == nil {
		clone.Bytes = []byte{}
	}
	if mutation.ExpectedBefore != nil {
		precondition := *mutation.ExpectedBefore
		clone.ExpectedBefore = &precondition
	}
	return clone
}

func (f *WriteFunnel) validateMutationSet(operationKey string, mutations []Mutation) error {
	if len(mutations) == 0 || len(mutations) > maxMutationCount {
		return fmt.Errorf("%w: mutation count must be 1 to %d", ErrMutationInvalid, maxMutationCount)
	}
	seen := make(map[string]struct{}, len(mutations))
	totalBytes := 0
	for i := range mutations {
		mutation := &mutations[i]
		if _, duplicate := seen[mutation.Path]; duplicate {
			return fmt.Errorf("%w: %s", ErrMutationDuplicatePath, mutation.Path)
		}
		seen[mutation.Path] = struct{}{}
		if !isCleanRelativePath(mutation.Path) {
			return fmt.Errorf("%w: path %q is not clean and relative", ErrMutationInvalid, mutation.Path)
		}
		switch mutation.Operation {
		case MutationWrite:
			if mutation.Bytes == nil || len(mutation.Bytes) > maxMutationBytes || mutation.deletePermit != nil ||
				mutation.ContractRoot != "" || mutation.PlanDigest != "" || (mutation.Mode != "" && mutation.Mode != "100644" && mutation.Mode != "100755") {
				return fmt.Errorf("%w: write mutation %q has an invalid shape", ErrMutationInvalid, mutation.Path)
			}
			totalBytes += len(mutation.Bytes)
			if totalBytes > maxMutationAggregateBytes {
				return fmt.Errorf("%w: aggregate write bytes exceed %d", ErrMutationInvalid, maxMutationAggregateBytes)
			}
			if mutation.ExpectedBefore != nil && !validMutationPrecondition(*mutation.ExpectedBefore) {
				return fmt.Errorf("%w: write mutation %q has an invalid precondition", ErrMutationInvalid, mutation.Path)
			}
		case MutationDelete:
			if mutation.Bytes != nil || mutation.ExpectedBefore == nil || !validMutationPrecondition(*mutation.ExpectedBefore) ||
				!validContractSidecarPath(mutation.ContractRoot, mutation.Path) || !validTypedSHA256(mutation.PlanDigest) {
				return fmt.Errorf("%w: delete mutation %q has an invalid shape", ErrMutationInvalid, mutation.Path)
			}
			binding := contractSidecarDeleteBinding{
				OperationKey: operationKey, PlanDigest: mutation.PlanDigest, ContractRoot: mutation.ContractRoot,
				Path: mutation.Path, PriorDigest: mutation.ExpectedBefore.Digest, PriorMode: mutation.ExpectedBefore.Mode,
			}
			if err := f.verifyDeletePermit(binding, mutation.deletePermit); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown operation %q", ErrMutationInvalid, mutation.Operation)
		}
	}
	return nil
}

func (f *WriteFunnel) verifyDeletePermit(binding contractSidecarDeleteBinding, permit *contractSidecarDeletePermit) error {
	if permit == nil || f.mutationAuthority == nil || permit.authority != f.mutationAuthority || f.mutationAuthority.err != nil {
		return ErrMutationUnauthorized
	}
	want := mutationPermitMAC(f.mutationAuthority.secret[:], binding)
	if !hmac.Equal(permit.mac[:], want[:]) {
		return ErrMutationUnauthorized
	}
	return nil
}

func validateDeleteBinding(binding contractSidecarDeleteBinding) error {
	if !operation.Valid(binding.OperationKey) || !validTypedSHA256(binding.PlanDigest) ||
		!validMutationPrecondition(MutationPrecondition{Digest: binding.PriorDigest, Mode: binding.PriorMode}) ||
		!validContractSidecarPath(binding.ContractRoot, binding.Path) {
		return fmt.Errorf("%w: invalid contract-sidecar delete binding", ErrMutationInvalid)
	}
	return nil
}

func mutationPermitMAC(secret []byte, binding contractSidecarDeleteBinding) [sha256.Size]byte {
	mac := hmac.New(sha256.New, secret)
	for _, value := range []string{
		contractSidecarDeleteDomain, binding.OperationKey, binding.PlanDigest, binding.ContractRoot,
		binding.Path, binding.PriorDigest, binding.PriorMode,
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = mac.Write(size[:])       // hash.Hash.Write never returns an error.
		_, _ = mac.Write([]byte(value)) // hash.Hash.Write never returns an error.
	}
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func validMutationPrecondition(precondition MutationPrecondition) bool {
	return validTypedSHA256(precondition.Digest) &&
		(precondition.Mode == "100644" || precondition.Mode == "100755")
}

func validContractSidecarPath(root, target string) bool {
	if !isCleanRelativePath(root) || !isCleanRelativePath(target) || !hasPathPrefix(target, root+"/") {
		return false
	}
	rootParts := strings.Split(root, "/")
	if len(rootParts) != 3 || rootParts[1] != "provides" || !validSystemID(rootParts[0]) {
		return false
	}
	contractID := "XC-" + rootParts[0] + "-" + rootParts[2]
	if !validRecoveryTarget(contractID + "@0.0.0") {
		return false
	}
	relative := strings.TrimPrefix(target, root+"/")
	first, _, _ := strings.Cut(relative, "/")
	if relative == first {
		return false
	}
	switch first {
	case "schema", "fixtures", "artifacts":
		return true
	default:
		return false
	}
}

func mutationPreconditionError(path string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrMutationPrecondition, path)
	}
	return fmt.Errorf("%w: %s: %v", ErrMutationPrecondition, path, cause)
}
