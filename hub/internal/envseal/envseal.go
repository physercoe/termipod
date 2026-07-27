// Package envseal implements the env-secret envelope (ADR-056 D-3): the
// host-sealed delivery of an env profile's resolved secret_refs.
//
// An env profile's secret_refs point at zero-knowledge vault items; the hub
// must never hold the values in usable form. A client (the only party holding
// the vault key) resolves the refs, builds a {KEY: value} map, and seals it to
// a *specific* target host's public key. The hub stores the opaque envelope on
// the spawn row (env_secret_envelope) and forwards it; the host-runner unseals
// at launch and injects via process env only. Go implements the OPEN side
// host-side and nothing hub-side (the hub only carries bytes it cannot read).
//
// # Construction
//
// The envelope reuses the vault sealed-box primitive (ADR-052 D-4:
// X25519 -> HKDF-SHA256 -> AES-256-GCM; see lib/services/vault/vault_crypto.dart
// and desktop/vault-core) with TWO deliberate deltas from the vault's
// device-wrap envelope, both for domain separation and hub-retarget resistance:
//
//   - HKDF info = "termipod-env-host-v1" (the vault device wrap uses
//     "termipod-vault-device-v1").
//   - The AEAD AAD binds context and is NON-empty (the vault device wrap uses
//     empty AAD). AAD = "tp-env1" 0x1F team_id 0x1F host_id 0x1F profile_id.
//     0x1F is ASCII Unit Separator; team/host/profile IDs never contain it.
//     Binding team+host+profile into the AAD is what stops a malicious hub
//     re-targeting the ciphertext to a host it controls: the host reconstructs
//     the AAD from its OWN trusted team_id and host_id, so an envelope minted
//     for a different context fails the GCM tag check.
//
// Seal(secrets, hostPub, teamID, hostID, profileID):
//
//	pt      = compact-JSON(secrets)                         // {"KEY":"value",...}
//	eph     = X25519 ephemeral keypair
//	shared  = X25519(eph.priv, hostPub)                     // 32 bytes
//	wrapKey = HKDF-SHA256(salt=0, ikm=shared, info=info, L=32)
//	nonce   = 12 random bytes
//	ct      = AES-256-GCM.Seal(wrapKey, nonce, pt, AAD)     // ciphertext||tag
//	envelope = {"v":1,"host_id":hostID,"profile_id":profileID,
//	            "epk":b64(eph.pub),"nonce":b64(nonce),"ct":b64(ct)}
//
// All base64 is standard (padded), matching the vault's b64e / base64Encode.
//
// This construction is the interop contract cross-checked by three
// implementations: desktop/vault-core (Rust), lib/services/vault (Dart), and
// this package (Go, open side) — locked by the KAT fixture in
// testdata/envseal_kat.json, which every implementation must reproduce
// byte-for-byte.
package envseal

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// Version is the only envelope schema version understood so far.
	Version = 1

	hkdfInfo  = "termipod-env-host-v1"
	aadPrefix = "tp-env1"
	aadSep    = 0x1f // ASCII Unit Separator
	nonceLen  = 12
	keyLen    = 32
	pubLen    = 32
)

var b64 = base64.StdEncoding

// Envelope is the opaque object stored on the spawn row (env_secret_envelope)
// and forwarded by the hub. Every field except the two context IDs is base64.
type Envelope struct {
	V         int    `json:"v"`
	HostID    string `json:"host_id"`
	ProfileID string `json:"profile_id"`
	EPK       string `json:"epk"`   // base64 ephemeral X25519 public key (32B)
	Nonce     string `json:"nonce"` // base64 AES-GCM nonce (12B)
	CT        string `json:"ct"`    // base64 ciphertext||tag
}

// GenerateIdentity returns a fresh X25519 identity as base64 (seed, public).
// The seed is the 32-byte private scalar a host persists in StateDir (0600);
// the public key rides capabilities_json. Same shape as the vault device
// keypair (generate_device), so the three crypto implementations agree.
func GenerateIdentity() (seedB64, pubB64 string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return b64.EncodeToString(priv.Bytes()), b64.EncodeToString(priv.PublicKey().Bytes()), nil
}

// Fingerprint is the human-comparable short code for a host public key, used
// by the operator trust step (ADR-056 D-2): the host-runner prints it on its
// console banner and the client shows it in the trust dialog; the operator
// confirms they match before the client seals secrets to the host. Format:
// base32(SHA-256(pubkey)[:10]) in four dash-separated groups of four, e.g.
// "K5J2-8QH4-M3PX-9ZTB". Must be identical across the Go, Rust, and Dart
// implementations (there is no security in the truncation itself — the pin is
// the pubkey; the code is only what a human eyeballs).
func Fingerprint(pubB64 string) (string, error) {
	pub, err := b64.DecodeString(pubB64)
	if err != nil {
		return "", err
	}
	if len(pub) != pubLen {
		return "", fmt.Errorf("envseal: expected %d-byte public key, got %d", pubLen, len(pub))
	}
	sum := sha256.Sum256(pub)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]) // 16 chars
	var b strings.Builder
	for i, c := range enc {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(c)
	}
	return b.String(), nil
}

// PublicFromSeed derives the base64 X25519 public key from a base64 32-byte
// seed, so a host can re-advertise its pubkey from the persisted private key
// without storing the public half.
func PublicFromSeed(seedB64 string) (string, error) {
	priv, err := privFromSeed(seedB64)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(priv.PublicKey().Bytes()), nil
}

// Seal builds an envelope delivering secrets to the host identified by
// hostPubB64, bound to (teamID, hostID, profileID). Clients (Rust/Dart) are the
// production sealers; this Go implementation exists for the interop round-trip
// and for tests. It returns the compact-JSON envelope string stored on the row.
func Seal(secrets map[string]string, hostPubB64, teamID, hostID, profileID string) (string, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return sealWith(secrets, hostPubB64, teamID, hostID, profileID, eph, nonce)
}

// sealWith is the deterministic core, exposed to tests so a fixed ephemeral key
// and nonce reproduce the committed KAT fixture byte-for-byte.
func sealWith(secrets map[string]string, hostPubB64, teamID, hostID, profileID string, eph *ecdh.PrivateKey, nonce []byte) (string, error) {
	if len(nonce) != nonceLen {
		return "", fmt.Errorf("envseal: nonce must be %d bytes, got %d", nonceLen, len(nonce))
	}
	hostPub, err := pubFromB64(hostPubB64)
	if err != nil {
		return "", fmt.Errorf("envseal: host public key: %w", err)
	}
	shared, err := eph.ECDH(hostPub)
	if err != nil {
		return "", fmt.Errorf("envseal: ECDH: %w", err)
	}
	wrapKey := hkdfSHA256(shared, hkdfInfo, keyLen)
	gcm, err := newGCM(wrapKey)
	if err != nil {
		return "", err
	}
	// Compact, deterministic JSON for the plaintext map so a re-seal with the
	// same inputs is byte-stable (encoding/json sorts map keys).
	pt, err := json.Marshal(secrets)
	if err != nil {
		return "", err
	}
	aad := buildAAD(teamID, hostID, profileID)
	ct := gcm.Seal(nil, nonce, pt, aad)
	env := Envelope{
		V:         Version,
		HostID:    hostID,
		ProfileID: profileID,
		EPK:       b64.EncodeToString(eph.PublicKey().Bytes()),
		Nonce:     b64.EncodeToString(nonce),
		CT:        b64.EncodeToString(ct),
	}
	out, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Open unseals an envelope with the host's private seed, reconstructing the AAD
// from the host's OWN trusted teamID and hostID. It refuses an envelope whose
// host_id is not this host's (ADR-056 D-3: the hub cannot re-target ciphertext
// to a host it controls). A wrong team, a tampered profile_id, or any ciphertext
// tampering fails the GCM tag check rather than leaking a partial result.
func Open(envelopeJSON, hostSeedB64, teamID, hostID string) (map[string]string, error) {
	var env Envelope
	if err := json.Unmarshal([]byte(envelopeJSON), &env); err != nil {
		return nil, fmt.Errorf("envseal: malformed envelope: %w", err)
	}
	if env.V != Version {
		return nil, fmt.Errorf("envseal: unsupported envelope version %d", env.V)
	}
	if env.HostID != hostID {
		// Present/absent-grade error only — never contents (ADR-056 D-4/D-5).
		return nil, fmt.Errorf("envseal: envelope targets host %q, not this host %q", env.HostID, hostID)
	}
	priv, err := privFromSeed(hostSeedB64)
	if err != nil {
		return nil, fmt.Errorf("envseal: host seed: %w", err)
	}
	epk, err := pubFromB64(env.EPK)
	if err != nil {
		return nil, fmt.Errorf("envseal: ephemeral key: %w", err)
	}
	nonce, err := b64.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("envseal: nonce: %w", err)
	}
	if len(nonce) != nonceLen {
		return nil, fmt.Errorf("envseal: nonce must be %d bytes, got %d", nonceLen, len(nonce))
	}
	ct, err := b64.DecodeString(env.CT)
	if err != nil {
		return nil, fmt.Errorf("envseal: ciphertext: %w", err)
	}
	shared, err := priv.ECDH(epk)
	if err != nil {
		return nil, fmt.Errorf("envseal: ECDH: %w", err)
	}
	wrapKey := hkdfSHA256(shared, hkdfInfo, keyLen)
	gcm, err := newGCM(wrapKey)
	if err != nil {
		return nil, err
	}
	aad := buildAAD(teamID, env.HostID, env.ProfileID)
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		// Wrong key/host/team/profile or tampering — one indistinguishable error.
		return nil, errors.New("envseal: unseal failed (wrong key/context or corrupt envelope)")
	}
	var secrets map[string]string
	if err := json.Unmarshal(pt, &secrets); err != nil {
		return nil, fmt.Errorf("envseal: malformed plaintext: %w", err)
	}
	return secrets, nil
}

// buildAAD is the context-binding associated data: "tp-env1" and the three IDs
// joined by 0x1F. Unambiguous because the IDs are ASCII UUIDs/slugs with no
// separator byte; documented as the interop contract in the package comment.
func buildAAD(teamID, hostID, profileID string) []byte {
	var b bytes.Buffer
	b.WriteString(aadPrefix)
	b.WriteByte(aadSep)
	b.WriteString(teamID)
	b.WriteByte(aadSep)
	b.WriteString(hostID)
	b.WriteByte(aadSep)
	b.WriteString(profileID)
	return b.Bytes()
}

// hkdfSHA256 is RFC 5869 with a zero-length (all-zero) salt, matching the vault
// crypto in all three implementations. HMAC zero-pads its key to the block
// size, so a zero-filled salt of HashLen bytes is identical to the empty salt
// used by the Rust (None) and Dart (unset nonce) sides.
func hkdfSHA256(ikm []byte, info string, length int) []byte {
	salt := make([]byte, sha256.Size)
	extract := hmac.New(sha256.New, salt)
	extract.Write(ikm)
	prk := extract.Sum(nil)

	var out, t []byte
	for counter := byte(1); len(out) < length; counter++ {
		exp := hmac.New(sha256.New, prk)
		exp.Write(t)
		exp.Write([]byte(info))
		exp.Write([]byte{counter})
		t = exp.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envseal: aes: %w", err)
	}
	return cipher.NewGCM(block)
}

func privFromSeed(seedB64 string) (*ecdh.PrivateKey, error) {
	seed, err := b64.DecodeString(seedB64)
	if err != nil {
		return nil, err
	}
	if len(seed) != keyLen {
		return nil, fmt.Errorf("expected %d-byte seed, got %d", keyLen, len(seed))
	}
	return ecdh.X25519().NewPrivateKey(seed)
}

func pubFromB64(pubB64 string) (*ecdh.PublicKey, error) {
	pub, err := b64.DecodeString(pubB64)
	if err != nil {
		return nil, err
	}
	if len(pub) != pubLen {
		return nil, fmt.Errorf("expected %d-byte public key, got %d", pubLen, len(pub))
	}
	return ecdh.X25519().NewPublicKey(pub)
}
