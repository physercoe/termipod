//! Zero-knowledge vault crypto (ADR-055 M2.6 / D-3) — the pure-Rust crypto
//! extracted verbatim from `src-tauri/src/vault.rs`, which is itself a
//! byte-for-byte port of the mobile `lib/services/vault/vault_crypto.dart`. This
//! crate has NO Tauri and NO WASM deps, so it compiles for the native `cargo
//! test` target (proving the algorithm) and — through the sibling `vault-wasm`
//! crate — to WASM for the Electron shell. Compiling the SAME crypto (rather than
//! reimplementing it in TS) is what guarantees a desktop can seal/open the same
//! vault bundle the phone and the Tauri build do.
//!
//! Interop constants (must match the Dart side + vault.rs exactly):
//!   - AES-256-GCM, 12-byte nonce, 16-byte tag, EMPTY aad, layout nonce‖ct‖tag.
//!   - device wrap = ephemeralPub(32) ‖ (nonce‖ct‖tag); HKDF-SHA256 salt=None,
//!     info = "termipod-vault-device-v1", 32-byte output.
//!   - recovery = salt(16) ‖ (nonce‖ct‖tag); Argon2id m=19456 KiB, t=2, p=1,
//!     out=32, raw salt; password = code stripped of [\s-] and upper-cased.
//!   - all envelopes base64 (standard, padded).

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use argon2::{Algorithm, Argon2, Params, Version};
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use hkdf::Hkdf;
use rand_core::{OsRng, RngCore};
use sha2::{Digest, Sha256};
use x25519_dalek::{EphemeralSecret, PublicKey, StaticSecret};

const DEVICE_INFO: &[u8] = b"termipod-vault-device-v1";
const NONCE_LEN: usize = 12;
const TAG_LEN: usize = 16;

fn b64d(s: &str) -> Result<Vec<u8>, String> {
    STANDARD.decode(s).map_err(|e| e.to_string())
}
fn b64e(b: &[u8]) -> String {
    STANDARD.encode(b)
}

fn key32(b64: &str) -> Result<[u8; 32], String> {
    let bytes = b64d(b64)?;
    <[u8; 32]>::try_from(bytes.as_slice()).map_err(|_| "expected 32-byte key".to_string())
}

/// AES-256-GCM seal with a fresh random nonce; returns nonce‖ct‖tag.
fn aes_seal(key: &[u8; 32], plaintext: &[u8]) -> Result<Vec<u8>, String> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|e| e.to_string())?;
    let mut nonce_bytes = [0u8; NONCE_LEN];
    OsRng.fill_bytes(&mut nonce_bytes);
    let ct = cipher
        .encrypt(Nonce::from_slice(&nonce_bytes), plaintext)
        .map_err(|e| e.to_string())?;
    let mut out = Vec::with_capacity(NONCE_LEN + ct.len());
    out.extend_from_slice(&nonce_bytes);
    out.extend_from_slice(&ct);
    Ok(out)
}

/// Open a nonce‖ct‖tag blob.
fn aes_open(key: &[u8; 32], blob: &[u8]) -> Result<Vec<u8>, String> {
    if blob.len() < NONCE_LEN + TAG_LEN {
        return Err("ciphertext too short".into());
    }
    let (nonce_bytes, ct) = blob.split_at(NONCE_LEN);
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|e| e.to_string())?;
    cipher
        .decrypt(Nonce::from_slice(nonce_bytes), ct)
        .map_err(|_| "decrypt failed (wrong key/corrupt data)".to_string())
}

fn hkdf_device_key(shared: &[u8]) -> Result<[u8; 32], String> {
    let hk = Hkdf::<Sha256>::new(None, shared);
    let mut okm = [0u8; 32];
    hk.expand(DEVICE_INFO, &mut okm).map_err(|e| e.to_string())?;
    Ok(okm)
}

fn normalize_code(code: &str) -> String {
    code.chars()
        .filter(|c| !c.is_whitespace() && *c != '-')
        .collect::<String>()
        .to_uppercase()
}

fn argon2_recovery_key(code: &str, salt: &[u8]) -> Result<[u8; 32], String> {
    let params = Params::new(19456, 2, 1, Some(32)).map_err(|e| e.to_string())?;
    let a = Argon2::new(Algorithm::Argon2id, Version::V0x13, params);
    let mut okm = [0u8; 32];
    a.hash_password_into(normalize_code(code).as_bytes(), salt, &mut okm)
        .map_err(|e| e.to_string())?;
    Ok(okm)
}

// ---- public operations (the 9 vault commands, sans the Tauri wrapper) -------

/// A device keypair: `public_key` is enrolled at the hub; `seed` is kept in the
/// keychain.
pub struct DeviceKeys {
    pub public_key: String,
    pub seed: String,
}

/// A fresh random 256-bit vault key (base64).
pub fn generate_key() -> String {
    let mut k = [0u8; 32];
    OsRng.fill_bytes(&mut k);
    b64e(&k)
}

/// Seal the plaintext bundle JSON under the vault key → base64 ciphertext.
pub fn seal(key: &str, plaintext: &str) -> Result<String, String> {
    Ok(b64e(&aes_seal(&key32(key)?, plaintext.as_bytes())?))
}

/// Open base64 ciphertext under the vault key → plaintext bundle JSON.
pub fn open(key: &str, ciphertext: &str) -> Result<String, String> {
    let pt = aes_open(&key32(key)?, &b64d(ciphertext)?)?;
    String::from_utf8(pt).map_err(|e| e.to_string())
}

/// A new X25519 device keypair (public_key + seed, both base64).
pub fn generate_device() -> DeviceKeys {
    let secret = StaticSecret::random_from_rng(OsRng);
    let public = PublicKey::from(&secret);
    DeviceKeys {
        public_key: b64e(public.as_bytes()),
        seed: b64e(&secret.to_bytes()),
    }
}

/// Wrap the vault key to a device's public key → base64 envelope.
pub fn wrap_for_device(key: &str, device_public: &str) -> Result<String, String> {
    let vault_key = key32(key)?;
    let device_pub = key32(device_public)?;
    let eph = EphemeralSecret::random_from_rng(OsRng);
    let eph_pub = PublicKey::from(&eph);
    let shared = eph.diffie_hellman(&PublicKey::from(device_pub));
    let wrap_key = hkdf_device_key(shared.as_bytes())?;
    let sealed = aes_seal(&wrap_key, &vault_key)?;
    let mut out = Vec::with_capacity(32 + sealed.len());
    out.extend_from_slice(eph_pub.as_bytes());
    out.extend_from_slice(&sealed);
    Ok(b64e(&out))
}

/// Unwrap a device envelope with this device's seed → vault key (base64).
pub fn unwrap_device(seed: &str, envelope: &str) -> Result<String, String> {
    let seed_bytes = key32(seed)?;
    let env = b64d(envelope)?;
    if env.len() < 32 {
        return Err("envelope too short".into());
    }
    let (eph_pub_bytes, sealed) = env.split_at(32);
    let eph_pub = <[u8; 32]>::try_from(eph_pub_bytes).map_err(|_| "bad ephemeral key".to_string())?;
    let secret = StaticSecret::from(seed_bytes);
    let shared = secret.diffie_hellman(&PublicKey::from(eph_pub));
    let wrap_key = hkdf_device_key(shared.as_bytes())?;
    Ok(b64e(&aes_open(&wrap_key, sealed)?))
}

/// Wrap the vault key under a recovery code → base64 envelope.
pub fn wrap_for_recovery(key: &str, code: &str) -> Result<String, String> {
    let vault_key = key32(key)?;
    let mut salt = [0u8; 16];
    OsRng.fill_bytes(&mut salt);
    let wrap_key = argon2_recovery_key(code, &salt)?;
    let sealed = aes_seal(&wrap_key, &vault_key)?;
    let mut out = Vec::with_capacity(16 + sealed.len());
    out.extend_from_slice(&salt);
    out.extend_from_slice(&sealed);
    Ok(b64e(&out))
}

/// Unwrap a recovery envelope with the recovery code → vault key (base64).
pub fn unwrap_recovery(code: &str, envelope: &str) -> Result<String, String> {
    let env = b64d(envelope)?;
    if env.len() < 16 {
        return Err("envelope too short".into());
    }
    let (salt, sealed) = env.split_at(16);
    let wrap_key = argon2_recovery_key(code, salt)?;
    Ok(b64e(&aes_open(&wrap_key, sealed)?))
}

/// A fresh recovery code: 20 random bytes → base32 (RFC 4648), dash-grouped by 4.
pub fn generate_recovery_code() -> String {
    let mut bytes = [0u8; 20];
    OsRng.fill_bytes(&mut bytes);
    let s = data_encoding::BASE32_NOPAD.encode(&bytes);
    s.as_bytes()
        .chunks(4)
        .map(|c| std::str::from_utf8(c).unwrap_or(""))
        .collect::<Vec<_>>()
        .join("-")
}

// ---- env-secret envelope (ADR-056 D-3) ---------------------------------------
//
// The client side of env-profile secret delivery, byte-compatible with the Go
// OPEN implementation in `hub/internal/envseal` (locked by that package's
// testdata/envseal_kat.json). Same sealed-box primitive as the vault device
// wrap above, with two deltas: a domain-separated HKDF info and a NON-empty
// AEAD AAD binding "tp-env1" | team | host | profile (0x1F separated). Only the
// host holding the target private key can open the result; the AAD stops a
// malicious hub re-targeting it.

const ENV_INFO: &[u8] = b"termipod-env-host-v1";
const ENV_AAD_PREFIX: &[u8] = b"tp-env1";
const ENV_AAD_SEP: u8 = 0x1f;

fn hkdf_env_key(shared: &[u8]) -> Result<[u8; 32], String> {
    let hk = Hkdf::<Sha256>::new(None, shared);
    let mut okm = [0u8; 32];
    hk.expand(ENV_INFO, &mut okm).map_err(|e| e.to_string())?;
    Ok(okm)
}

fn env_aad(team_id: &str, host_id: &str, profile_id: &str) -> Vec<u8> {
    let mut a = Vec::new();
    a.extend_from_slice(ENV_AAD_PREFIX);
    a.push(ENV_AAD_SEP);
    a.extend_from_slice(team_id.as_bytes());
    a.push(ENV_AAD_SEP);
    a.extend_from_slice(host_id.as_bytes());
    a.push(ENV_AAD_SEP);
    a.extend_from_slice(profile_id.as_bytes());
    a
}

/// AES-256-GCM with an explicit nonce and AAD; returns ct‖tag (the nonce is
/// carried separately in the envelope).
fn aes_seal_aad(key: &[u8; 32], nonce: &[u8; 12], plaintext: &[u8], aad: &[u8]) -> Result<Vec<u8>, String> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|e| e.to_string())?;
    cipher
        .encrypt(Nonce::from_slice(nonce), Payload { msg: plaintext, aad })
        .map_err(|e| e.to_string())
}

/// json_string quotes and escapes a string for embedding in the envelope JSON.
/// host_id/profile_id are slugs in practice, but escape defensively so a value
/// with a quote/backslash/control byte can't produce malformed JSON.
fn json_string(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

fn build_env_json(host_id: &str, profile_id: &str, eph_pub: &[u8], nonce: &[u8], ct: &[u8]) -> String {
    format!(
        r#"{{"v":1,"host_id":{},"profile_id":{},"epk":"{}","nonce":"{}","ct":"{}"}}"#,
        json_string(host_id),
        json_string(profile_id),
        b64e(eph_pub),
        b64e(nonce),
        b64e(ct),
    )
}

fn seal_env_common(
    shared: &[u8],
    eph_pub: &[u8; 32],
    nonce: &[u8; 12],
    team_id: &str,
    host_id: &str,
    profile_id: &str,
    plaintext: &[u8],
) -> Result<String, String> {
    let wrap_key = hkdf_env_key(shared)?;
    let aad = env_aad(team_id, host_id, profile_id);
    let ct = aes_seal_aad(&wrap_key, nonce, plaintext, &aad)?;
    Ok(build_env_json(host_id, profile_id, eph_pub, nonce, &ct))
}

/// Seal `plaintext` (the canonical JSON of the resolved `{KEY: value}` secret
/// map — sorted keys, compact, matching Go's `json.Marshal`) to the target
/// host's public key, returning the envelope JSON stored on the spawn row.
/// The caller (TS/desktop) is responsible for the canonical plaintext so the
/// three implementations agree byte-for-byte.
pub fn seal_env_secret(
    host_pub_b64: &str,
    team_id: &str,
    host_id: &str,
    profile_id: &str,
    plaintext: &str,
) -> Result<String, String> {
    let host_pub = key32(host_pub_b64)?;
    let eph = EphemeralSecret::random_from_rng(OsRng);
    let eph_pub = PublicKey::from(&eph);
    let shared = eph.diffie_hellman(&PublicKey::from(host_pub));
    let mut nonce = [0u8; 12];
    OsRng.fill_bytes(&mut nonce);
    seal_env_common(
        shared.as_bytes(),
        eph_pub.as_bytes(),
        &nonce,
        team_id,
        host_id,
        profile_id,
        plaintext.as_bytes(),
    )
}

/// env_fingerprint is the human-comparable short code for a host public key
/// (ADR-056 D-2): base32(SHA-256(pubkey)[:10]) in four dash-separated groups of
/// four. Identical to the Go (`envseal.Fingerprint`) and Dart implementations;
/// the operator compares the client's code against the host console banner
/// before secrets are sealed to the host.
pub fn env_fingerprint(pub_b64: &str) -> Result<String, String> {
    let pub_bytes = key32(pub_b64)?;
    let mut hasher = Sha256::new();
    hasher.update(pub_bytes);
    let digest = hasher.finalize();
    let enc = data_encoding::BASE32_NOPAD.encode(&digest[..10]);
    Ok(enc
        .as_bytes()
        .chunks(4)
        .map(|c| std::str::from_utf8(c).unwrap_or(""))
        .collect::<Vec<_>>()
        .join("-"))
}

#[cfg(test)]
mod tests {
    use super::*;

    // ── KNOWN-ANSWER tests: pin the AES-256-GCM byte layout (nonce‖ct‖tag) to the
    // NIST vectors (K=0^256, IV=0^96, no AAD), confirmed independently against
    // Node's `crypto` aes-256-gcm. A byte-compat drift in the AEAD core fails here.
    fn hex(s: &str) -> Vec<u8> {
        data_encoding::HEXLOWER.decode(s.as_bytes()).unwrap()
    }

    #[test]
    fn aes_gcm_nist_kat_empty_plaintext() {
        let key = [0u8; 32];
        let mut blob = vec![0u8; NONCE_LEN]; // 12-byte zero nonce, empty ct
        blob.extend_from_slice(&hex("530f8afbc74536b9a963b4f1c4cb738b")); // tag
        assert!(aes_open(&key, &blob).unwrap().is_empty());
    }

    #[test]
    fn aes_gcm_nist_kat_one_block() {
        let key = [0u8; 32];
        let mut blob = vec![0u8; NONCE_LEN];
        blob.extend_from_slice(&hex("cea7403d4d606b6e074ec5d3baf39d18")); // ct
        blob.extend_from_slice(&hex("d0d1c8a799996bf0265b98b5d48ab919")); // tag
        assert_eq!(aes_open(&key, &blob).unwrap(), vec![0u8; 16]);
    }

    #[test]
    fn seal_open_roundtrip() {
        let key = generate_key();
        let pt = r#"{"connections":[],"sshKeys":{},"passwords":{}}"#;
        let ct = seal(&key, pt).unwrap();
        assert_eq!(open(&key, &ct).unwrap(), pt);
    }

    #[test]
    fn open_with_wrong_key_fails() {
        let ct = seal(&generate_key(), "secret").unwrap();
        assert!(open(&generate_key(), &ct).is_err());
    }

    #[test]
    fn device_wrap_unwrap_roundtrip() {
        let vault_key = generate_key();
        let device = generate_device();
        let env = wrap_for_device(&vault_key, &device.public_key).unwrap();
        assert_eq!(unwrap_device(&device.seed, &env).unwrap(), vault_key);
    }

    #[test]
    fn recovery_wrap_unwrap_roundtrip() {
        let vault_key = generate_key();
        let code = generate_recovery_code();
        let env = wrap_for_recovery(&vault_key, &code).unwrap();
        // Normalization: dashes/spaces/case are ignored.
        let messy = format!("  {}  ", code.to_lowercase());
        assert_eq!(unwrap_recovery(&messy, &env).unwrap(), vault_key);
    }

    #[test]
    fn recovery_code_shape() {
        let code = generate_recovery_code();
        assert_eq!(code.len(), 32 + 7); // 32 base32 chars + 7 dashes
        assert_eq!(code.split('-').count(), 8);
    }

    // ---- env-secret envelope KAT (ADR-056 D-3) --------------------------------
    // Byte-for-byte interop with the Go host OPEN side. These are the fixed
    // inputs from hub/internal/envseal/testdata/envseal_kat.json; sealing with
    // the same host key, ephemeral key and nonce MUST reproduce the fixture's
    // epk + ct. A drift in HKDF info, AAD encoding, plaintext bytes, or the
    // AES-GCM core fails here. (The full envelope JSON framing is not asserted —
    // Go opens by parsing, so only the crypto values are the contract.)
    const KAT_HOST_SEED: &str = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=";
    const KAT_EPH_SEED: &str = "IB8eHRwbGhkYFxYVFBMSERAPDg0MCwoJCAcGBQQDAgE=";
    const KAT_NONCE: &str = "AAECAwQFBgcICQoL";
    const KAT_TEAM: &str = "team_kat";
    const KAT_HOST: &str = "host_kat";
    const KAT_PROFILE: &str = "envp_kat";
    // Canonical plaintext = Go json.Marshal(map[string]string) — sorted keys,
    // compact. The client must reproduce these exact bytes.
    const KAT_PLAINTEXT: &str =
        r#"{"DATABASE_URL":"postgres://kat/db","OPENAI_API_KEY":"sk-kat-0123456789"}"#;
    const KAT_EXPECT_EPK: &str = "DXmWAPb/ruLhIea496Bdxmh0tR2zEC0NcfeZoJy0xGE=";
    const KAT_EXPECT_CT: &str =
        "8VAHXTTmaZ/oFU5T9nGqF0FAwMValMz0f5CmdBNDx6BYwavfkOqbTMlqtt20JGTkbhytOeWiCRf7iSkWM9ima9jK/Np91l+s2Zy9A7Rd4ea7C1hChGlPXis=";

    fn parse_env_field(env: &str, key: &str) -> String {
        // Minimal JSON field extractor for the test (avoids a serde dep).
        let needle = format!("\"{}\":\"", key);
        let start = env.find(&needle).expect("field present") + needle.len();
        let rest = &env[start..];
        let end = rest.find('"').expect("closing quote");
        rest[..end].to_string()
    }

    #[test]
    fn env_secret_kat_matches_go_fixture() {
        // Seal deterministically with the fixed ephemeral key + nonce.
        let host_pub_bytes = {
            let seed = key32(KAT_HOST_SEED).unwrap();
            let secret = StaticSecret::from(seed);
            b64e(PublicKey::from(&secret).as_bytes())
        };
        let eph = StaticSecret::from(key32(KAT_EPH_SEED).unwrap());
        let eph_pub = PublicKey::from(&eph);
        let shared = eph.diffie_hellman(&PublicKey::from(key32(&host_pub_bytes).unwrap()));
        let nonce_v = b64d(KAT_NONCE).unwrap();
        let nonce: [u8; 12] = nonce_v.as_slice().try_into().unwrap();

        let env = seal_env_common(
            shared.as_bytes(),
            eph_pub.as_bytes(),
            &nonce,
            KAT_TEAM,
            KAT_HOST,
            KAT_PROFILE,
            KAT_PLAINTEXT.as_bytes(),
        )
        .unwrap();

        assert_eq!(parse_env_field(&env, "epk"), KAT_EXPECT_EPK, "epk drift");
        assert_eq!(parse_env_field(&env, "ct"), KAT_EXPECT_CT, "ct drift — construction mismatch");
    }

    #[test]
    fn env_fingerprint_kat() {
        // Must match Go envseal.Fingerprint + Dart envFingerprint for the KAT
        // host public key (ADR-056 D-2 trust short code).
        let host_pub = {
            let secret = StaticSecret::from(key32(KAT_HOST_SEED).unwrap());
            b64e(PublicKey::from(&secret).as_bytes())
        };
        assert_eq!(env_fingerprint(&host_pub).unwrap(), "VKUP-75YD-WUFS-FF7U");
    }
}
