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

/// Register the OS-native credential store as keyring-core's default. Call once
/// at startup, before any `Entry` is created.
///
/// keyring 4.1.3 is meant to do this lazily on the first `Entry::new`, but its
/// guard — `SET_CREDENTIAL_STORE.compare_exchange(false, true, …) == Ok(true)` —
/// never matches (a *successful* CAS returns `Ok(false)`), so no store is ever
/// registered and every keychain call fails with "No default store has been
/// set". We register it ourselves; the store choice mirrors keyring's own
/// `v1::set_credential_store` target cfgs so we pick the same backend it would.
pub fn init_default_store() {
    let result: Result<(), String> = (|| {
        #[cfg(target_os = "macos")]
        let store = apple_native_keyring_store::keychain::Store::new().map_err(|e| e.to_string())?;
        #[cfg(target_os = "windows")]
        let store = windows_native_keyring_store::Store::new().map_err(|e| e.to_string())?;
        #[cfg(all(unix, not(any(target_os = "macos", target_os = "ios", target_os = "android"))))]
        let store = zbus_secret_service_keyring_store::Store::new().map_err(|e| e.to_string())?;
        #[cfg(all(any(unix, windows), not(any(target_os = "ios", target_os = "android"))))]
        keyring_core::set_default_store(store);
        Ok(())
    })();
    if let Err(e) = result {
        eprintln!("keychain: failed to register default credential store: {e}");
    }
}

fn entry(key: &str) -> Result<Entry, String> {
    Entry::new(SERVICE, key).map_err(|e| e.to_string())
}

/// True on Windows, whose Credential Manager caps a single item at 2560 UTF-16
/// chars. The webview consolidates all secrets into ONE keychain item (to avoid
/// the per-item macOS auth prompt) and, on Windows only, splits that document
/// across sibling items to stay under the cap. cfg! is compile-time exact, so the
/// webview never guesses the platform (a wrong guess would silently drop data).
#[tauri::command]
pub fn keychain_is_windows() -> bool {
    cfg!(target_os = "windows")
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
