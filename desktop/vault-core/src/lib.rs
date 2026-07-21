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

use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use argon2::{Algorithm, Argon2, Params, Version};
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use hkdf::Hkdf;
use rand_core::{OsRng, RngCore};
use sha2::Sha256;
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
}
