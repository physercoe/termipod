package hostrunner

import (
	"os"
	"testing"

	"github.com/termipod/hub/internal/envseal"
)

func TestLoadOrCreateHostIdentity_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()

	pub1, seed1, err := loadOrCreateHostIdentity(dir, false)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if pub1 == "" || seed1 == "" {
		t.Fatal("expected a generated identity, got empty")
	}
	// The public key must derive from the persisted seed.
	if got, _ := envseal.PublicFromSeed(seed1); got != pub1 {
		t.Fatalf("pubkey/seed mismatch: %q vs %q", got, pub1)
	}
	// Seed file exists and is 0600.
	fi, err := os.Stat(identityPath(dir))
	if err != nil {
		t.Fatalf("stat seed file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("seed file perm = %o, want 600", perm)
	}

	// A second load returns the SAME identity (no churn — clients pin it).
	pub2, seed2, err := loadOrCreateHostIdentity(dir, false)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if pub2 != pub1 || seed2 != seed1 {
		t.Fatalf("identity changed across loads: %q/%q vs %q/%q", pub2, seed2, pub1, seed1)
	}
}

func TestLoadOrCreateHostIdentity_Rekey(t *testing.T) {
	dir := t.TempDir()
	pub1, _, err := loadOrCreateHostIdentity(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, err := loadOrCreateHostIdentity(dir, true) // rekey
	if err != nil {
		t.Fatal(err)
	}
	if pub2 == pub1 {
		t.Fatal("rekey produced the same identity")
	}
	// The rotated identity persists (no rekey flag → stable again).
	pub3, _, _ := loadOrCreateHostIdentity(dir, false)
	if pub3 != pub2 {
		t.Fatalf("post-rekey identity not stable: %q vs %q", pub3, pub2)
	}
}

func TestLoadOrCreateHostIdentity_NoStateDir(t *testing.T) {
	pub, seed, err := loadOrCreateHostIdentity("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub != "" || seed != "" {
		t.Fatal("expected no identity without a state dir")
	}
}

func TestLoadOrCreateHostIdentity_CorruptSeedRegenerates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(identityPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, seed, err := loadOrCreateHostIdentity(dir, false)
	if err != nil {
		t.Fatalf("corrupt file should regenerate, got err: %v", err)
	}
	if pub == "" || seed == "" {
		t.Fatal("expected a regenerated identity")
	}
	if got, _ := envseal.PublicFromSeed(seed); got != pub {
		t.Fatal("regenerated pubkey/seed mismatch")
	}
}
