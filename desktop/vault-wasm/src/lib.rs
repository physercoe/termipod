//! WASM boundary for the vault crypto (ADR-055 M2.6 / D-3). Thin `wasm-bindgen`
//! wrappers over `vault-core` — the same pure-Rust crypto the Tauri build and the
//! mobile Dart client use — exposing the nine `vault_*` operations to the
//! Electron main process. Names + string signatures mirror the Tauri commands so
//! `src/vault/crypto.ts` drives them unchanged through the bridge (the Electron
//! handler in `src/ipc/vault.ts` calls these, M2.6b).
//!
//! The boundary is all strings: a `Result::Err` becomes a thrown `JsError` (the
//! renderer's invoke rejects), and `vault_generate_device` returns a small JSON
//! object string (its two fields are base64, so no escaping is needed).
use vault_core as vc;
use wasm_bindgen::prelude::*;

#[wasm_bindgen]
pub fn vault_generate_key() -> String {
    vc::generate_key()
}

#[wasm_bindgen]
pub fn vault_seal(key: String, plaintext: String) -> Result<String, JsError> {
    vc::seal(&key, &plaintext).map_err(|e| JsError::new(&e))
}

#[wasm_bindgen]
pub fn vault_open(key: String, ciphertext: String) -> Result<String, JsError> {
    vc::open(&key, &ciphertext).map_err(|e| JsError::new(&e))
}

/// Returns `{"public_key":"<b64>","seed":"<b64>"}` — base64 values are JSON-safe.
#[wasm_bindgen]
pub fn vault_generate_device() -> String {
    let d = vc::generate_device();
    format!("{{\"public_key\":\"{}\",\"seed\":\"{}\"}}", d.public_key, d.seed)
}

#[wasm_bindgen]
pub fn vault_wrap_for_device(key: String, device_public: String) -> Result<String, JsError> {
    vc::wrap_for_device(&key, &device_public).map_err(|e| JsError::new(&e))
}

#[wasm_bindgen]
pub fn vault_unwrap_device(seed: String, envelope: String) -> Result<String, JsError> {
    vc::unwrap_device(&seed, &envelope).map_err(|e| JsError::new(&e))
}

#[wasm_bindgen]
pub fn vault_wrap_for_recovery(key: String, code: String) -> Result<String, JsError> {
    vc::wrap_for_recovery(&key, &code).map_err(|e| JsError::new(&e))
}

#[wasm_bindgen]
pub fn vault_unwrap_recovery(code: String, envelope: String) -> Result<String, JsError> {
    vc::unwrap_recovery(&code, &envelope).map_err(|e| JsError::new(&e))
}

#[wasm_bindgen]
pub fn vault_generate_recovery_code() -> String {
    vc::generate_recovery_code()
}
