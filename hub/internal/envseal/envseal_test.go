package envseal

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Fixed KAT inputs. These are the interop contract: desktop/vault-core (Rust)
// and lib/services/vault (Dart) must, sealing with the SAME host key, ephemeral
// key and nonce, reproduce testdata/envseal_kat.json byte-for-byte (E3b).
const (
	katHostSeedB64 = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" // bytes 0x01..0x20
	katEphSeedB64  = "IB8eHRwbGhkYFxYVFBMSERAPDg0MCwoJCAcGBQQDAgE=" // bytes 0x20..0x01
	katNonceB64    = "AAECAwQFBgcICQoL"                             // bytes 0x00..0x0b
	katTeamID      = "team_kat"
	katHostID      = "host_kat"
	katProfileID   = "envp_kat"
)

func katSecrets() map[string]string {
	return map[string]string{
		"OPENAI_API_KEY": "sk-kat-0123456789",
		"DATABASE_URL":   "postgres://kat/db",
	}
}

func mustKey(t *testing.T, seedB64 string) *ecdh.PrivateKey {
	t.Helper()
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	k, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		t.Fatalf("new private key: %v", err)
	}
	return k
}

// katEnvelope re-seals the fixture deterministically from the fixed inputs.
func katEnvelope(t *testing.T) string {
	t.Helper()
	host := mustKey(t, katHostSeedB64)
	eph := mustKey(t, katEphSeedB64)
	nonce, err := base64.StdEncoding.DecodeString(katNonceB64)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	hostPub := base64.StdEncoding.EncodeToString(host.PublicKey().Bytes())
	env, err := sealWith(katSecrets(), hostPub, katTeamID, katHostID, katProfileID, eph, nonce)
	if err != nil {
		t.Fatalf("sealWith: %v", err)
	}
	return env
}

func TestSealOpenRoundTrip(t *testing.T) {
	seed, pub, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	secrets := map[string]string{"A": "1", "B": "two", "C": ""}
	env, err := Seal(secrets, pub, "team1", "hostA", "envp1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := Open(env, seed, "team1", "hostA")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(got) != len(secrets) {
		t.Fatalf("secret count: got %d want %d", len(got), len(secrets))
	}
	for k, v := range secrets {
		if got[k] != v {
			t.Errorf("secret %q: got %q want %q", k, got[k], v)
		}
	}
}

func TestOpenRejectsForeignHost(t *testing.T) {
	seed, pub, _ := GenerateIdentity()
	env, err := Seal(map[string]string{"X": "y"}, pub, "team1", "hostA", "envp1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Same private key, but the host now believes it is "hostB": the explicit
	// host_id guard must refuse before any decrypt is attempted.
	if _, err := Open(env, seed, "team1", "hostB"); err == nil {
		t.Fatal("expected foreign-host rejection, got nil")
	}
}

func TestOpenRejectsWrongTeam(t *testing.T) {
	seed, pub, _ := GenerateIdentity()
	env, _ := Seal(map[string]string{"X": "y"}, pub, "team1", "hostA", "envp1")
	// team_id is not carried in the envelope; a host in the wrong team
	// reconstructs a different AAD and the GCM tag check fails.
	if _, err := Open(env, seed, "team2", "hostA"); err == nil {
		t.Fatal("expected wrong-team rejection, got nil")
	}
}

func TestOpenRejectsTamperedProfile(t *testing.T) {
	seed, pub, _ := GenerateIdentity()
	env, _ := Seal(map[string]string{"X": "y"}, pub, "team1", "hostA", "envp1")
	var e Envelope
	if err := json.Unmarshal([]byte(env), &e); err != nil {
		t.Fatal(err)
	}
	e.ProfileID = "envp_evil" // change AAD input without the key
	tampered, _ := json.Marshal(e)
	if _, err := Open(string(tampered), seed, "team1", "hostA"); err == nil {
		t.Fatal("expected tampered-profile rejection, got nil")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	seed, pub, _ := GenerateIdentity()
	env, _ := Seal(map[string]string{"X": "y"}, pub, "team1", "hostA", "envp1")
	var e Envelope
	_ = json.Unmarshal([]byte(env), &e)
	ct, _ := base64.StdEncoding.DecodeString(e.CT)
	ct[0] ^= 0xff
	e.CT = base64.StdEncoding.EncodeToString(ct)
	tampered, _ := json.Marshal(e)
	if _, err := Open(string(tampered), seed, "team1", "hostA"); err == nil {
		t.Fatal("expected tampered-ciphertext rejection, got nil")
	}
}

func TestPublicFromSeed(t *testing.T) {
	seed, pub, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	got, err := PublicFromSeed(seed)
	if err != nil {
		t.Fatalf("PublicFromSeed: %v", err)
	}
	if got != pub {
		t.Fatalf("derived pubkey mismatch: got %q want %q", got, pub)
	}
}

// TestKATFixture locks the byte-for-byte interop contract. The committed
// fixture is what the Rust and Dart implementations reproduce (E3b): a drift
// in AAD encoding, HKDF info, JSON framing, or base64 alphabet fails here.
//
// Regenerate intentionally with: ENVSEAL_WRITE_KAT=1 go test ./internal/envseal
func TestKATFixture(t *testing.T) {
	path := filepath.Join("testdata", "envseal_kat.json")
	env := katEnvelope(t)

	if os.Getenv("ENVSEAL_WRITE_KAT") == "1" {
		writeKAT(t, path, env)
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture (set ENVSEAL_WRITE_KAT=1 to create): %v", err)
	}
	var fx katFixture
	if err := json.Unmarshal(want, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if fx.Envelope != env {
		t.Fatalf("envelope drift from fixture:\n got %s\nwant %s", env, fx.Envelope)
	}
	// And it must open back to the fixture's plaintext with the fixture's key.
	got, err := Open(fx.Envelope, fx.HostSeed, fx.TeamID, fx.HostID)
	if err != nil {
		t.Fatalf("Open fixture: %v", err)
	}
	if len(got) != len(fx.Secrets) {
		t.Fatalf("secret count: got %d want %d", len(got), len(fx.Secrets))
	}
	for k, v := range fx.Secrets {
		if got[k] != v {
			t.Errorf("secret %q: got %q want %q", k, got[k], v)
		}
	}
}

type katFixture struct {
	Note      string            `json:"note"`
	HostSeed  string            `json:"host_seed_b64"`
	HostPub   string            `json:"host_pub_b64"`
	EphSeed   string            `json:"eph_seed_b64"`
	Nonce     string            `json:"nonce_b64"`
	TeamID    string            `json:"team_id"`
	HostID    string            `json:"host_id"`
	ProfileID string            `json:"profile_id"`
	Secrets   map[string]string `json:"secrets"`
	Envelope  string            `json:"envelope"`
}

func writeKAT(t *testing.T, path, env string) {
	t.Helper()
	host := mustKey(t, katHostSeedB64)
	fx := katFixture{
		Note: "Env-secret envelope KAT (ADR-056 D-3). Rust (desktop/vault-core) " +
			"and Dart (lib/services/vault) must reproduce `envelope` byte-for-byte " +
			"sealing with these fixed host_seed/eph_seed/nonce. Regenerate with " +
			"ENVSEAL_WRITE_KAT=1 go test ./internal/envseal",
		HostSeed:  katHostSeedB64,
		HostPub:   base64.StdEncoding.EncodeToString(host.PublicKey().Bytes()),
		EphSeed:   katEphSeedB64,
		Nonce:     katNonceB64,
		TeamID:    katTeamID,
		HostID:    katHostID,
		ProfileID: katProfileID,
		Secrets:   katSecrets(),
		Envelope:  env,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(fx, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
