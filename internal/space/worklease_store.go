package space

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const (
	workLeaseDirectory            = "work-leases"
	workLeaseLockWait             = 2 * time.Second
	workLeasePoll                 = 5 * time.Millisecond
	maximumWorkLeaseEntries       = 2048
	maximumWorkLeaseListingBytes  = 128 * 1024
	maximumWorkLeaseContentsBytes = 8 * 1024 * 1024
)

var (
	// ErrWorkSelectionAmbiguous refuses an under-specified local selection.
	// The error deliberately carries no candidate identities or cache paths.
	// ErrWorkSelectionAmbiguous is part of the public package API.
	ErrWorkSelectionAmbiguous = errors.New("local work selection is ambiguous")
	// ErrWorkLeaseEnumerationLimit refuses a cache directory whose entry or
	// byte bounds no longer permit a complete, fail-closed view.
	// ErrWorkLeaseEnumerationLimit is part of the public package API.
	ErrWorkLeaseEnumerationLimit = errors.New("local work lease enumeration limit exceeded")
)

type workLeaseStoreConfig struct {
	clock workreport.Clock
}

type wallClock struct{}

// Now is part of the public package API.
func (wallClock) Now() time.Time { return time.Now() }

// WorkLeaseStoreOption configures a WorkLeaseStore without weakening its
// rooted filesystem capability.
// WorkLeaseStoreOption is part of the public package API.
type WorkLeaseStoreOption func(*workLeaseStoreConfig) error

// WithWorkLeaseClock injects the clock used only for read-time expiry
// decisions. Lease mutation continues to receive its clock from workreport.
// WithWorkLeaseClock is part of the public package API.
func WithWorkLeaseClock(clock workreport.Clock) WorkLeaseStoreOption {
	return func(config *workLeaseStoreConfig) error {
		if clock == nil {
			return fmt.Errorf("space: work lease clock is required")
		}
		config.clock = clock
		return nil
	}
}

// WorkLeaseStore persists disposable work leases beneath one already-trusted
// cache root. The held os.Root capability keeps every later operation bound to
// the directory opened by the constructor even if its pathname is replaced.
// WorkLeaseStore is part of the public package API.
type WorkLeaseStore struct {
	root        *os.Root
	randomToken func() (string, error)
	clock       workreport.Clock
}

type heldWorkLeaseLock struct {
	file *os.File
}

// NewWorkLeaseStore opens cacheRoot and creates its private work-leases
// directory when absent. cacheRoot itself must already exist and must not be a
// symbolic link; selection and creation of that trusted cache root belongs to
// project configuration, not lease data.
// NewWorkLeaseStore is part of the public package API.
func NewWorkLeaseStore(cacheRoot string, options ...WorkLeaseStoreOption) (*WorkLeaseStore, error) {
	config := workLeaseStoreConfig{clock: wallClock{}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("space: nil work lease store option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}

	before, err := os.Lstat(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("space: open work lease cache root: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("%w: cache root must be a real directory", workreport.ErrInvalidLease)
	}

	cache, err := os.OpenRoot(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("space: open work lease cache root: %w", err)
	}
	closeCache := true
	defer func() {
		if closeCache {
			_ = cache.Close() // reason: preserve the constructor error that triggered cleanup
		}
	}()

	after, err := os.Lstat(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("space: re-inspect work lease cache root: %w", err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%w: cache root changed while opening", workreport.ErrInvalidLease)
	}
	opened, err := cache.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("space: inspect opened cache root: %w", err)
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("%w: opened cache capability does not match configured root", workreport.ErrInvalidLease)
	}

	if err := ensureWorkLeaseDirectory(cache); err != nil {
		return nil, err
	}
	dirInfo, err := cache.Lstat(workLeaseDirectory)
	if err != nil {
		return nil, fmt.Errorf("space: inspect work lease directory: %w", err)
	}
	if !validWorkLeaseDirectory(dirInfo) {
		return nil, fmt.Errorf("%w: work-leases failed platform directory checks", workreport.ErrInvalidLease)
	}

	leaseRoot, err := cache.OpenRoot(workLeaseDirectory)
	if err != nil {
		return nil, fmt.Errorf("space: open work lease directory: %w", err)
	}
	openedDir, err := leaseRoot.Stat(".")
	if err != nil {
		_ = leaseRoot.Close() // reason: preserve the stat error while releasing the incomplete capability
		return nil, fmt.Errorf("space: inspect opened work lease directory: %w", err)
	}
	if !validWorkLeaseDirectory(openedDir) || !os.SameFile(dirInfo, openedDir) {
		_ = leaseRoot.Close() // reason: the invalid capability is never returned
		return nil, fmt.Errorf("%w: work-leases changed while opening", workreport.ErrInvalidLease)
	}
	closeCache = true
	if err := cache.Close(); err != nil {
		_ = leaseRoot.Close() // reason: preserve the cache close error while releasing the child capability
		return nil, fmt.Errorf("space: close cache root: %w", err)
	}
	closeCache = false
	return &WorkLeaseStore{root: leaseRoot, randomToken: workLeaseRandomToken, clock: config.clock}, nil
}

func ensureWorkLeaseDirectory(root *os.Root) error {
	info, err := root.Lstat(workLeaseDirectory)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(workLeaseDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("space: create work lease directory: %w", err)
		}
		info, err = root.Lstat(workLeaseDirectory)
	}
	if err != nil {
		return fmt.Errorf("space: inspect work lease directory: %w", err)
	}
	if !validWorkLeaseDirectory(info) {
		return fmt.Errorf("%w: work-leases failed platform directory checks", workreport.ErrInvalidLease)
	}
	return nil
}

// Close releases the held directory capability. It does not delete leases.
func (s *WorkLeaseStore) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

// Load is part of the public package API.
func (s *WorkLeaseStore) Load(ctx context.Context, key string) (workreport.Lease, workreport.Revision, error) {
	if err := ctx.Err(); err != nil {
		return workreport.Lease{}, "", err
	}
	name, err := workLeaseFilename(key)
	if err != nil {
		return workreport.Lease{}, "", err
	}
	raw, err := s.readRegular(name)
	if errors.Is(err, os.ErrNotExist) {
		return workreport.Lease{}, "", workreport.ErrLeaseNotFound
	}
	if err != nil {
		return workreport.Lease{}, "", err
	}
	lease, err := workreport.UnmarshalLease(raw)
	if err != nil {
		return workreport.Lease{}, "", err
	}
	if lease.Identity.LeaseKey != key {
		return workreport.Lease{}, "", fmt.Errorf("%w: stored lease key does not match filename", workreport.ErrInvalidLease)
	}
	return lease, revisionOf(raw), nil
}

// ResolveWork selects one locally persisted work identity. An implicit
// selection (no work ID and no session) considers only unexpired leases;
// explicit selectors may resolve an expired owner so resume can replay its
// exact pending journal. Absence uses the repository's existing typed
// ErrLeaseNotFound refusal and ambiguity has its own typed refusal.
// ResolveWork is part of the public package API.
func (s *WorkLeaseStore) ResolveWork(
	ctx context.Context,
	projectID string,
	spaceID string,
	workID string,
	session string,
) (workreport.WorkIdentityInput, error) {
	if (workID == "") != (session == "") {
		return workreport.WorkIdentityInput{}, workreport.ErrLeaseNotOwned
	}
	leases, err := s.listStoredWorkLeases(ctx)
	if err != nil {
		return workreport.WorkIdentityInput{}, err
	}

	now := s.clock.Now()
	explicit := workID != ""
	matches := make([]workreport.Lease, 0, 1)
	for _, lease := range leases {
		if lease.Identity.ProjectID != projectID || lease.Identity.Space != spaceID ||
			(workID != "" && lease.Identity.WorkID != workID) ||
			(session != "" && lease.Identity.Actor.Session != session) {
			continue
		}
		if !explicit && lease.Expired(now) {
			continue
		}
		matches = append(matches, lease)
	}

	switch len(matches) {
	case 0:
		return workreport.WorkIdentityInput{}, workreport.ErrLeaseNotFound
	case 1:
		identity := matches[0].Identity
		return workreport.WorkIdentityInput{
			ProjectID: identity.ProjectID,
			Space:     identity.Space,
			Thread:    identity.Thread,
			WorkID:    identity.WorkID,
			Actor:     identity.Actor,
		}, nil
	default:
		return workreport.WorkIdentityInput{}, ErrWorkSelectionAmbiguous
	}
}

// ListWork returns detached, deterministically ordered leases scoped to one
// project and space. It never interprets pending or closing as activity:
// includeExpired=false applies the sole currentness rule now > expires_at.
// ListWork is part of the public package API.
func (s *WorkLeaseStore) ListWork(
	ctx context.Context,
	projectID string,
	spaceID string,
	workID string,
	includeExpired bool,
) ([]workreport.Lease, error) {
	leases, err := s.listStoredWorkLeases(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	selected := make([]workreport.Lease, 0, len(leases))
	for _, lease := range leases {
		if lease.Identity.ProjectID != projectID || lease.Identity.Space != spaceID ||
			(workID != "" && lease.Identity.WorkID != workID) ||
			(!includeExpired && lease.Expired(now)) {
			continue
		}
		selected = append(selected, lease.Clone())
	}
	sort.Slice(selected, func(i, j int) bool {
		left, right := selected[i].Identity, selected[j].Identity
		if left.WorkID != right.WorkID {
			return left.WorkID < right.WorkID
		}
		if left.Actor.Session != right.Actor.Session {
			return left.Actor.Session < right.Actor.Session
		}
		return left.LeaseKey < right.LeaseKey
	})
	return selected, nil
}

func (s *WorkLeaseStore) listStoredWorkLeases(ctx context.Context) ([]workreport.Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := s.root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("space: open work lease directory for listing: %w", err)
	}
	defer func() {
		_ = directory.Close() // reason: preserve the listing/read error
	}()
	entries, err := directory.ReadDir(maximumWorkLeaseEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("space: list work leases: %w", err)
	}
	if len(entries) > maximumWorkLeaseEntries {
		return nil, fmt.Errorf("%w: more than %d directory entries", ErrWorkLeaseEnumerationLimit, maximumWorkLeaseEntries)
	}
	if len(entries) <= maximumWorkLeaseEntries {
		more, readErr := directory.ReadDir(1)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("space: finish work lease listing: %w", readErr)
		}
		if len(more) != 0 {
			return nil, fmt.Errorf("%w: more than %d directory entries", ErrWorkLeaseEnumerationLimit, maximumWorkLeaseEntries)
		}
	}

	listingBytes := 0
	contentsBytes := int64(0)
	leases := make([]workreport.Lease, 0, len(entries)/2+1)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		listingBytes += len(name)
		if listingBytes > maximumWorkLeaseListingBytes {
			return nil, fmt.Errorf("%w: directory names exceed %d bytes", ErrWorkLeaseEnumerationLimit, maximumWorkLeaseListingBytes)
		}

		key, kind := classifyWorkLeaseEntry(name)
		if kind == workLeaseEntryInvalid {
			return nil, fmt.Errorf("%w: non-canonical entry in work lease directory", workreport.ErrInvalidLease)
		}
		info, statErr := s.root.Lstat(name)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("space: inspect listed work lease entry: %w", statErr)
		}
		if !validWorkLeaseRegularFile(info) {
			return nil, fmt.Errorf("%w: listed work lease entry failed platform regular-file checks", workreport.ErrInvalidLease)
		}
		switch kind {
		case workLeaseEntryLock:
			if info.Size() > 1024 {
				return nil, fmt.Errorf("%w: work lease lock payload exceeds 1024 bytes", workreport.ErrInvalidLease)
			}
			continue
		case workLeaseEntryTemporary:
			if info.Size() > workreport.MaximumEncodedLease {
				return nil, workreport.ErrLeaseTooLarge
			}
			continue
		}
		if info.Size() > workreport.MaximumEncodedLease {
			return nil, workreport.ErrLeaseTooLarge
		}
		raw, readErr := s.readRegularStable(ctx, name)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		contentsBytes += int64(len(raw))
		if contentsBytes > maximumWorkLeaseContentsBytes {
			return nil, fmt.Errorf("%w: lease contents exceed %d bytes", ErrWorkLeaseEnumerationLimit, maximumWorkLeaseContentsBytes)
		}
		lease, decodeErr := workreport.UnmarshalLease(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if lease.Identity.LeaseKey != key {
			return nil, fmt.Errorf("%w: stored lease key does not match filename", workreport.ErrInvalidLease)
		}
		leases = append(leases, lease.Clone())
	}
	return leases, nil
}

func (s *WorkLeaseStore) readRegularStable(ctx context.Context, name string) ([]byte, error) {
	var err error
	for range 3 {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		var raw []byte
		raw, err = s.readRegular(name)
		if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, workreport.ErrLeaseTooLarge) {
			return raw, err
		}
		if !errors.Is(err, workreport.ErrInvalidLease) {
			return nil, err
		}
	}
	return nil, err
}

type workLeaseEntryKind uint8

const (
	workLeaseEntryInvalid workLeaseEntryKind = iota
	workLeaseEntryLease
	workLeaseEntryLock
	workLeaseEntryTemporary
)

func classifyWorkLeaseEntry(name string) (string, workLeaseEntryKind) {
	if key, ok := canonicalWorkLeaseName(name); ok {
		return key, workLeaseEntryLease
	}
	if strings.HasSuffix(name, ".lock") {
		if _, ok := canonicalWorkLeaseName(strings.TrimSuffix(name, ".lock")); ok {
			return "", workLeaseEntryLock
		}
	}
	const temporaryMarker = ".tmp-"
	if marker := strings.LastIndex(name, temporaryMarker); marker >= 0 {
		base, token := name[:marker], name[marker+len(temporaryMarker):]
		if _, ok := canonicalWorkLeaseName(base); ok && len(token) == 32 && isLowerHex(token) {
			return "", workLeaseEntryTemporary
		}
	}
	return "", workLeaseEntryInvalid
}

func canonicalWorkLeaseName(name string) (string, bool) {
	if len(name) != sha256.Size*2+len(".json") || !strings.HasSuffix(name, ".json") {
		return "", false
	}
	hexPart := strings.TrimSuffix(name, ".json")
	if !isLowerHex(hexPart) {
		return "", false
	}
	return "sha256:" + hexPart, true
}

func isLowerHex(value string) bool {
	for _, character := range []byte(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// CompareAndSwap is part of the public package API.
func (s *WorkLeaseStore) CompareAndSwap(ctx context.Context, key string, expected workreport.Revision, next *workreport.Lease) (revision workreport.Revision, retErr error) {
	name, err := workLeaseFilename(key)
	if err != nil {
		return "", err
	}
	var encoded []byte
	if next != nil {
		if next.Identity.LeaseKey != key {
			return "", fmt.Errorf("%w: next lease key does not match repository key", workreport.ErrInvalidLease)
		}
		encoded, err = workreport.MarshalLease(next.Clone())
		if err != nil {
			return "", err
		}
	}

	lock, err := s.acquire(ctx, name+".lock")
	if err != nil {
		return "", err
	}
	defer func() {
		if releaseErr := s.release(lock); releaseErr != nil {
			retErr = errors.Join(retErr, releaseErr)
		}
	}()

	current, err := s.readRegular(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	actual := workreport.Revision("")
	if err == nil {
		stored, decodeErr := workreport.UnmarshalLease(current)
		if decodeErr != nil {
			return "", decodeErr
		}
		if stored.Identity.LeaseKey != key {
			return "", fmt.Errorf("%w: stored lease key does not match filename", workreport.ErrInvalidLease)
		}
		actual = revisionOf(current)
	}
	if actual != expected {
		return actual, workreport.ErrSequenceConflict
	}
	if next == nil {
		if err != nil {
			return "", workreport.ErrLeaseNotFound
		}
		if err := s.root.Remove(name); err != nil {
			return actual, fmt.Errorf("space: delete work lease: %w", err)
		}
		if err := s.syncDirectory(); err != nil {
			return actual, err
		}
		return "", nil
	}
	if err := s.writeAtomic(name, encoded); err != nil {
		return actual, err
	}
	return revisionOf(encoded), nil
}

func workLeaseFilename(key string) (string, error) {
	if len(key) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(key, "sha256:") {
		return "", fmt.Errorf("%w: lease key is not sha256", workreport.ErrInvalidLease)
	}
	hexPart := strings.TrimPrefix(key, "sha256:")
	decoded, err := hex.DecodeString(hexPart)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(hexPart) != hexPart {
		return "", fmt.Errorf("%w: lease key is not lowercase sha256", workreport.ErrInvalidLease)
	}
	return hexPart + ".json", nil
}

func revisionOf(raw []byte) workreport.Revision {
	digest := sha256.Sum256(raw)
	return workreport.Revision(fmt.Sprintf("sha256:%x", digest))
}

func (s *WorkLeaseStore) readRegular(name string) ([]byte, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !validWorkLeaseRegularFile(info) {
		return nil, fmt.Errorf("%w: lease file failed platform regular-file checks", workreport.ErrInvalidLease)
	}
	if info.Size() > workreport.MaximumEncodedLease {
		return nil, workreport.ErrLeaseTooLarge
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close() // reason: reads report their primary error; the descriptor is read-only
	}()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("space: inspect opened work lease: %w", err)
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: lease file changed while opening", workreport.ErrInvalidLease)
	}
	raw, err := io.ReadAll(io.LimitReader(f, workreport.MaximumEncodedLease+1))
	if err != nil {
		return nil, fmt.Errorf("space: read work lease: %w", err)
	}
	if len(raw) > workreport.MaximumEncodedLease {
		return nil, workreport.ErrLeaseTooLarge
	}
	return raw, nil
}

func (s *WorkLeaseStore) writeAtomic(name string, raw []byte) error {
	token, err := s.randomToken()
	if err != nil {
		return fmt.Errorf("space: generate work lease temp name: %w", err)
	}
	tempName := name + ".tmp-" + token
	if info, err := s.root.Lstat(tempName); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: work lease temp path is a symlink", workreport.ErrInvalidLease)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("space: inspect work lease temp: %w", err)
	}
	f, err := s.root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("space: create work lease temp: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = s.root.Remove(tempName) // reason: best-effort removal must not replace the write error
		}
	}()
	if _, err := f.Write(raw); err != nil {
		_ = f.Close() // reason: preserve the write error
		return fmt.Errorf("space: write work lease temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // reason: preserve the sync error
		return fmt.Errorf("space: sync work lease temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("space: close work lease temp: %w", err)
	}
	info, err := s.root.Lstat(tempName)
	if err != nil {
		return fmt.Errorf("space: inspect written work lease temp: %w", err)
	}
	if !validWorkLeaseRegularFile(info) {
		return fmt.Errorf("%w: work lease temp failed platform regular-file checks", workreport.ErrInvalidLease)
	}
	if err := s.root.Rename(tempName, name); err != nil {
		return fmt.Errorf("space: publish work lease: %w", err)
	}
	cleanup = false
	return s.syncDirectory()
}

func (s *WorkLeaseStore) acquire(ctx context.Context, name string) (heldWorkLeaseLock, error) {
	file, err := s.openLockFile(name)
	if err != nil {
		return heldWorkLeaseLock{}, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = file.Close() // reason: preserve the acquisition or context error
		}
	}()
	deadline := time.Now().Add(workLeaseLockWait)
	for {
		if err := ctx.Err(); err != nil {
			return heldWorkLeaseLock{}, err
		}
		err := tryLockWorkLeaseFile(file)
		if err == nil {
			locked = true
			return heldWorkLeaseLock{file: file}, nil
		}
		if !isWorkLeaseLockContended(err) {
			return heldWorkLeaseLock{}, fmt.Errorf("space: lock work lease: %w", err)
		}
		if !time.Now().Before(deadline) {
			return heldWorkLeaseLock{}, workreport.ErrSequenceConflict
		}
		timer := time.NewTimer(workLeasePoll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return heldWorkLeaseLock{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *WorkLeaseStore) openLockFile(name string) (*os.File, error) {
	before, statErr := s.root.Lstat(name)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("space: inspect work lease lock: %w", statErr)
	}
	if statErr == nil && !validWorkLeaseRegularFile(before) {
		return nil, fmt.Errorf("%w: work lease lock failed platform regular-file checks", workreport.ErrInvalidLease)
	}
	file, err := s.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	expected := before
	if errors.Is(err, os.ErrExist) {
		expected, err = s.root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("space: inspect existing work lease lock: %w", err)
		}
		if !validWorkLeaseRegularFile(expected) {
			return nil, fmt.Errorf("%w: work lease lock failed platform regular-file checks", workreport.ErrInvalidLease)
		}
		file, err = s.root.OpenFile(name, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("space: open work lease lock: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close() // reason: preserve the stat error
		return nil, fmt.Errorf("space: inspect opened work lease lock: %w", err)
	}
	after, err := s.root.Lstat(name)
	if err != nil {
		_ = file.Close() // reason: preserve the rooted re-inspection error
		return nil, fmt.Errorf("space: re-inspect work lease lock: %w", err)
	}
	if !validWorkLeaseRegularFile(opened) || !validWorkLeaseRegularFile(after) || !os.SameFile(opened, after) ||
		(expected != nil && !os.SameFile(expected, opened)) || (statErr == nil && !os.SameFile(before, opened)) {
		_ = file.Close() // reason: the invalid lock handle is never returned
		return nil, fmt.Errorf("%w: work lease lock changed while opening", workreport.ErrInvalidLease)
	}
	return file, nil
}

func (s *WorkLeaseStore) release(lock heldWorkLeaseLock) error {
	if lock.file == nil {
		return nil
	}
	unlockErr := unlockWorkLeaseFile(lock.file)
	closeErr := lock.file.Close()
	if unlockErr != nil || closeErr != nil {
		return fmt.Errorf("space: release work lease lock: %w", errors.Join(unlockErr, closeErr))
	}
	return nil
}

func (s *WorkLeaseStore) readLock(name string) ([]byte, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !validWorkLeaseRegularFile(info) {
		return nil, fmt.Errorf("%w: work lease lock failed platform regular-file checks", workreport.ErrInvalidLease)
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close() // reason: reads report their primary error; the descriptor is read-only
	}()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("space: inspect opened work lease lock: %w", err)
	}
	if !validWorkLeaseRegularFile(opened) || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%w: work lease lock changed while opening", workreport.ErrInvalidLease)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1024 {
		return nil, fmt.Errorf("%w: work lease lock payload exceeds 1024 bytes", workreport.ErrInvalidLease)
	}
	return raw, nil
}

func workLeaseRandomToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}
