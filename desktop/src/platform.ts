/// Runtime detection for the two build targets. Under Tauri the webview origin
/// is `tauri://localhost` (or `http://tauri.localhost` on Windows), so a direct
/// `fetch` to the hub is cross-origin and the hub sends no CORS headers — it
/// fails with "Failed to fetch". Hub HTTP is therefore routed through the Rust
/// core (reqwest, not subject to CORS) whenever `isTauri()` is true; the plain
/// browser build keeps using `fetch`.
export function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}

/// Open an external URL in the OS default browser — never in the app webview.
/// Under Tauri a raw navigation replaces the single-webview SPA and strands the
/// user (director report: a link inside a PDF "jumped the whole app" with no way
/// back); the Rust `open_external` command hands the URL to the OS instead. The
/// browser build opens a new tab. Only http(s)/mailto are honoured.
export function openExternal(url: string): void {
  if (url === '') return;
  if (isTauri()) {
    // Lazy import so the browser build never pulls the Tauri IPC module.
    void import('@tauri-apps/api/core')
      .then(({ invoke }) => invoke('open_external', { url }))
      .catch(() => {
        /* best effort */
      });
  } else {
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}

/// Open a URL in a real in-app browser *window* (a Tauri webview, not an iframe),
/// so sites that forbid framing (`X-Frame-Options` — arxiv, Google Scholar, most
/// publishers) still load. The browser build falls back to a new tab.
export function openBrowserWindow(url: string): void {
  if (url === '') return;
  if (isTauri()) {
    void import('@tauri-apps/api/core')
      .then(({ invoke }) => invoke('open_browser_window', { url }))
      .catch(() => {
        /* best effort */
      });
  } else {
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}
