//! OS-keychain secret storage (parity F2). All desktop secrets — SSH passwords,
//! private keys, key passphrases, and later vault material — live in the OS
//! credential store via the `keyring` crate: Windows Credential Manager,
//! macOS Keychain, or the Linux Secret Service (pure-Rust zbus backend, so no
//! libdbus is linked at build time). The webview never persists a secret to
//! disk itself; it calls these commands with a stable key and gets the bytes
//! back only when needed. Non-secret metadata (connection names, hosts) stays
//! in the webview's own storage — only the secrets cross into the keychain.

use keyring::{Entry, Error as KeyringError};

const SERVICE: &str = "app.termipod.desktop";

fn entry(key: &str) -> Result<Entry, String> {
    Entry::new(SERVICE, key).map_err(|e| e.to_string())
}

/// Store (or overwrite) a secret under `key`.
#[tauri::command]
pub async fn keychain_set(key: String, value: String) -> Result<(), String> {
    entry(&key)?.set_password(&value).map_err(|e| e.to_string())
}

/// Read a secret; `None` when nothing is stored under `key`.
#[tauri::command]
pub async fn keychain_get(key: String) -> Result<Option<String>, String> {
    match entry(&key)?.get_password() {
        Ok(v) => Ok(Some(v)),
        Err(KeyringError::NoEntry) => Ok(None),
        Err(e) => Err(e.to_string()),
    }
}

/// Delete a secret. Missing keys are treated as success (idempotent).
#[tauri::command]
pub async fn keychain_delete(key: String) -> Result<(), String> {
    match entry(&key)?.delete_credential() {
        Ok(()) => Ok(()),
        Err(KeyringError::NoEntry) => Ok(()),
        Err(e) => Err(e.to_string()),
    }
}
