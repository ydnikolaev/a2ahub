package space

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestWorkIDSourceUsesInjectedEntropyForWorkAndSessionULIDs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 10, 15, 0, 0, time.FixedZone("test", 2*60*60))
	entropy := append(bytes.Repeat([]byte{0x11}, 10), bytes.Repeat([]byte{0x22}, 10)...)
	source, err := NewWorkIDSource(bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("NewWorkIDSource: %v", err)
	}

	workID, err := source.NewWorkID(now)
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	sessionID, err := source.NewSessionID(now)
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	wantWork, err := artifact.MintULIDAt(now, bytes.NewReader(entropy[:10]))
	if err != nil {
		t.Fatalf("mint expected work ULID: %v", err)
	}
	wantSession, err := artifact.MintULIDAt(now, bytes.NewReader(entropy[10:]))
	if err != nil {
		t.Fatalf("mint expected session ULID: %v", err)
	}
	if workID != "work:"+wantWork.String() || sessionID != "session:"+wantSession.String() {
		t.Fatalf("IDs = (%q, %q), want (%q, %q)", workID, sessionID, "work:"+wantWork.String(), "session:"+wantSession.String())
	}
}

func TestWorkIDSourceRequiresEntropyAndPreservesMintFailure(t *testing.T) {
	t.Parallel()

	if _, err := NewWorkIDSource(nil); err == nil {
		t.Fatal("NewWorkIDSource(nil) succeeded")
	}
	source, err := NewWorkIDSource(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.NewWorkID(time.Now()); err == nil {
		t.Fatal("NewWorkID with exhausted entropy succeeded")
	}
}

func TestWorkLeaseKeySourceBindsCanonicalProjectAndExactTuple(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	noncanonical := filepath.Join(nested, "..", "nested")
	projectID, err := CanonicalProjectID(noncanonical)
	if err != nil {
		t.Fatalf("CanonicalProjectID: %v", err)
	}
	source, err := NewWorkLeaseKeySource(noncanonical)
	if err != nil {
		t.Fatalf("NewWorkLeaseKeySource: %v", err)
	}

	identity := workIdentityFixture(projectID)
	key, err := source.LeaseKey(identity)
	if err != nil {
		t.Fatalf("LeaseKey: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(noncanonical)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		t.Fatal(err)
	}
	tuple := strings.Join([]string{
		filepath.Clean(canonical), identity.Space, identity.Thread, identity.Actor.System,
		identity.Actor.Name, identity.Actor.Session, identity.WorkID,
	}, "\x00")
	want := sha256.Sum256([]byte(tuple))
	if key != fmt.Sprintf("sha256:%x", want) {
		t.Fatalf("LeaseKey = %q, want exact canonical tuple digest %x", key, want)
	}
	if strings.Contains(key, canonical) || strings.Contains(projectID, canonical) {
		t.Fatalf("digest leaked project root: key=%q project=%q root=%q", key, projectID, canonical)
	}
}

func TestWorkLeaseKeyTupleSeparatesEveryIdentityField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projectID, err := CanonicalProjectID(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewWorkLeaseKeySource(root)
	if err != nil {
		t.Fatal(err)
	}
	base := workIdentityFixture(projectID)
	baseKey, err := source.LeaseKey(base)
	if err != nil {
		t.Fatal(err)
	}

	variants := []workreport.Identity{
		withWorkIdentity(base, func(i *workreport.Identity) { i.Space = "other" }),
		withWorkIdentity(base, func(i *workreport.Identity) { i.Thread = "thread:atlas-20260804-bcde" }),
		withWorkIdentity(base, func(i *workreport.Identity) { i.Actor.System = "beta" }),
		withWorkIdentity(base, func(i *workreport.Identity) { i.Actor.Name = "other" }),
		withWorkIdentity(base, func(i *workreport.Identity) { i.Actor.Session = "session:01K1Z6Y8Q9C7G5M3N2P4R6T8VX" }),
		withWorkIdentity(base, func(i *workreport.Identity) { i.WorkID = "work:01K1Z6Y8Q9C7G5M3N2P4R6T8VX" }),
	}
	seen := map[string]bool{baseKey: true}
	for _, variant := range variants {
		key, err := source.LeaseKey(variant)
		if err != nil {
			t.Fatalf("LeaseKey(%+v): %v", variant, err)
		}
		if seen[key] {
			t.Fatalf("tuple variant collided at %q: %+v", key, variant)
		}
		seen[key] = true
	}
}

func TestWorkLeaseKeyRefusesProjectMismatchAndNULAmbiguity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projectID, err := CanonicalProjectID(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewWorkLeaseKeySource(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := workIdentityFixture(projectID)
	identity.ProjectID = "sha256:" + strings.Repeat("0", 64)
	if _, err := source.LeaseKey(identity); err == nil {
		t.Fatal("LeaseKey accepted identity for a different canonical project")
	}
	identity = workIdentityFixture(projectID)
	identity.Actor.Name = "codex\x00other"
	if _, err := source.LeaseKey(identity); err == nil {
		t.Fatal("LeaseKey accepted NUL-delimited identity ambiguity")
	}
}

func workIdentityFixture(projectID string) workreport.Identity {
	return workreport.Identity{
		ProjectID: projectID,
		Space:     "checkout-core",
		Thread:    "thread:atlas-20260803-abcd",
		WorkID:    "work:01K1Z6Y8Q9C7G5M3N2P4R6T8VW",
		Actor: workreport.Actor{
			Kind: "agent", Name: "codex", System: "atlas",
			Model: "gpt-5", Session: "session:01K1Z6Y8Q9C7G5M3N2P4R6T8VW",
		},
	}
}

func withWorkIdentity(identity workreport.Identity, edit func(*workreport.Identity)) workreport.Identity {
	copy := identity
	edit(&copy)
	return copy
}
