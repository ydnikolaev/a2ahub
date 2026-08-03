package space

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

// WorkIDSource mints coordinator identities from caller-owned entropy. It has
// no clock of its own: the coordinator supplies the timestamp shared by the
// semantic operation.
type WorkIDSource struct {
	mu      sync.Mutex
	entropy io.Reader
}

// NewWorkIDSource constructs the production work/session ID adapter.
func NewWorkIDSource(entropy io.Reader) (*WorkIDSource, error) {
	if entropy == nil {
		return nil, fmt.Errorf("space: work identity entropy is required")
	}
	return &WorkIDSource{entropy: entropy}, nil
}

// NewWorkID returns work:<ULID> using artifact's canonical ULID minter.
func (s *WorkIDSource) NewWorkID(at time.Time) (string, error) {
	id, err := s.mint(at)
	if err != nil {
		return "", fmt.Errorf("space: mint work id: %w", err)
	}
	return "work:" + id, nil
}

// NewSessionID returns session:<ULID> using artifact's canonical ULID minter.
func (s *WorkIDSource) NewSessionID(at time.Time) (string, error) {
	id, err := s.mint(at)
	if err != nil {
		return "", fmt.Errorf("space: mint work session id: %w", err)
	}
	return "session:" + id, nil
}

func (s *WorkIDSource) mint(at time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := artifact.MintULIDAt(at, s.entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// WorkLeaseKeySource binds lease keys to one configuration-trusted canonical
// project root. Only its digest leaves this adapter; the path never enters a
// lease identity or durable checkpoint.
type WorkLeaseKeySource struct {
	canonicalRoot string
	projectID     string
}

// NewWorkLeaseKeySource canonicalizes projectRoot once and retains it only in
// this machine-local adapter.
func NewWorkLeaseKeySource(projectRoot string) (*WorkLeaseKeySource, error) {
	canonical, err := canonicalWorkProjectRoot(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("space: canonicalize work project root: %w", err)
	}
	return &WorkLeaseKeySource{
		canonicalRoot: canonical,
		projectID:     typedWorkDigest(canonical),
	}, nil
}

// CanonicalProjectID returns the path-free identity of an existing canonical
// project root. It is the sole project-ID derivation used by work reporting.
func CanonicalProjectID(projectRoot string) (string, error) {
	canonical, err := canonicalWorkProjectRoot(projectRoot)
	if err != nil {
		return "", fmt.Errorf("space: canonical project id: %w", err)
	}
	return typedWorkDigest(canonical), nil
}

// LeaseKey hashes the exact P2 tuple:
//
//	canonical root NUL space NUL thread NUL actor.system NUL actor.name
//	NUL actor.session NUL work.id
//
// Actor kind/model are deliberately absent because they are attribution, not
// ownership identity. ProjectID must match this adapter's bound root.
func (s *WorkLeaseKeySource) LeaseKey(identity workreport.Identity) (string, error) {
	if identity.ProjectID != s.projectID {
		return "", fmt.Errorf("space: work identity project does not match canonical root")
	}
	fields := []string{
		s.canonicalRoot,
		identity.Space,
		identity.Thread,
		identity.Actor.System,
		identity.Actor.Name,
		identity.Actor.Session,
		identity.WorkID,
	}
	for _, field := range fields {
		if strings.ContainsRune(field, '\x00') {
			return "", fmt.Errorf("space: work lease identity contains NUL")
		}
	}
	return typedWorkDigest(strings.Join(fields, "\x00")), nil
}

func canonicalWorkProjectRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root is not a directory")
	}
	return filepath.Clean(real), nil
}

func typedWorkDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
