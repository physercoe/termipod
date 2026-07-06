/// Runtime detection for the two build targets. Under Tauri the webview origin
/// is `tauri://localhost` (or `http://tauri.localhost` on Windows), so a direct
/// `fetch` to the hub is cross-origin and the hub sends no CORS headers — it
/// fails with "Failed to fetch". Hub HTTP is therefore routed through the Rust
/// core (reqwest, not subject to CORS) whenever `isTauri()` is true; the plain
/// browser build keeps using `fetch`.
export function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}
