package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/termipod/hub/internal/envseal"
)

// EnvEnvelopeVersion is the env-secret envelope schema this host-runner can
// unseal. It rides capabilities_json so a client knows whether — and with what
// format — it may seal secrets to this host (ADR-056 D-1).
const EnvEnvelopeVersion = envseal.Version

// identityFile persists the host's X25519 identity seed (base64). The seed is
// the host's private key: it never leaves the box and is the party a client
// pins and seals env secrets to (ADR-056 D-1/D-2). Kept beside host-runner.json
// in StateDir, 0600. Losing it is a re-key: the host advertises a new public
// key and every previously-stored envelope for it is dead (D-7).
type identityFile struct {
	Seed string `json:"seed"` // base64 X25519 private scalar (32 bytes)
}

func identityPath(dir string) string {
	return filepath.Join(dir, "host-identity.json")
}

// loadOrCreateHostIdentity resolves this host's X25519 identity, returning the
// base64 public key (for capabilities) and base64 seed (kept in memory for the
// launch-time unseal, ADR-056 D-5).
//
// No StateDir → no persistence → no identity: the host cannot honour secrets
// across restarts, so it advertises none and secret-bearing spawns to it are
// rejected hub-side (D-1/D-4). rekey discards any existing key and mints a new
// identity (the `--rekey` flag).
//
// A best-effort: an unreadable or malformed key file is treated as absent and
// regenerated rather than crashing the runner — the cost is that already-stored
// envelopes for the old key become undecryptable (D-7), never a secret leak.
func loadOrCreateHostIdentity(dir string, rekey bool) (pubB64, seedB64 string, err error) {
	if dir == "" {
		return "", "", nil
	}
	path := identityPath(dir)
	if rekey {
		_ = os.Remove(path)
	}
	if !rekey {
		if seed, ok := readIdentitySeed(path); ok {
			if pub, derr := envseal.PublicFromSeed(seed); derr == nil {
				return pub, seed, nil
			}
			// Corrupt seed → fall through and regenerate.
		}
	}
	seed, pub, err := envseal.GenerateIdentity()
	if err != nil {
		return "", "", err
	}
	if err := writeIdentitySeed(path, seed); err != nil {
		return "", "", err
	}
	return pub, seed, nil
}

func readIdentitySeed(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var f identityFile
	if err := json.Unmarshal(data, &f); err != nil || f.Seed == "" {
		return "", false
	}
	return f.Seed, true
}

// writeIdentitySeed persists the seed 0600 via a temp-file rename so a crash
// mid-write can't leave a truncated key.
func writeIdentitySeed(path, seed string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(identityFile{Seed: seed}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
