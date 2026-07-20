/// Runtime detection for the two build targets. Under Tauri the webview origin
/// is `tauri://localhost` (or `http://tauri.localhost` on Windows), so a direct
/// `fetch` to the hub is cross-origin and the hub sends no CORS headers — it
/// fails with "Failed to fetch". Hub HTTP is therefore routed through the Rust
/// core (reqwest, not subject to CORS) whenever `isTauri()` is true; the plain
/// browser build keeps using `fetch`.
export function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}

/// The host OS, from the Rust `platform_os` command ("windows" | "macos" |
/// "linux" | …). Compile-time exact (target triple) — unlike navigator.userAgent,
/// which the webview can present inconsistently and which a wrong read would turn
/// into a black-screen renderer choice (#333). Cached; `''` in the browser build.
let osCache: string | null = null;
export async function platformOs(): Promise<string> {
  if (osCache !== null) return osCache;
  if (!isTauri()) {
    osCache = '';
    return osCache;
  }
  try {
    const { invoke } = await import('@tauri-apps/api/core');
    osCache = await invoke<string>('platform_os');
  } catch {
    osCache = '';
  }
  return osCache;
}

/// True on Windows — where xterm's WebGL renderer on WebView2/ANGLE rendered a
/// black screen and could wedge the GPU process (v0.3.11), so WebGL is skipped
/// there (#333). Resolves false in the browser build.
export async function isWindows(): Promise<boolean> {
  return (await platformOs()) === 'windows';
}

/// The Windows OS build number (e.g. 22631) from the Rust `os_build_number`
/// command, or `null` off Windows / on any failure. The terminal passes it to
/// xterm's `windowsPty` so xterm applies the ConPTY reflow behaviour correct for
/// this build (native wrap sequences landed in build 21376). Cached; -1 sentinel
/// distinguishes "not yet fetched" from a legitimate `null`.
let buildCache: number | null | -1 = -1;
export async function windowsBuildNumber(): Promise<number | null> {
  if (buildCache !== -1) return buildCache;
  if (!isTauri()) {
    buildCache = null;
    return buildCache;
  }
  try {
    const { invoke } = await import('@tauri-apps/api/core');
    const n = await invoke<number | null>('os_build_number');
    buildCache = typeof n === 'number' && Number.isFinite(n) ? n : null;
  } catch {
    buildCache = null;
  }
  return buildCache;
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

/// Reveal a local file in the OS file manager (Finder / Explorer / files app),
/// selecting it where the platform supports it. Desktop-only; a no-op in the
/// browser build (no filesystem access).
export function revealPath(path: string): void {
  if (path === '' || !isTauri()) return;
  void import('@tauri-apps/api/core')
    .then(({ invoke }) => invoke('reveal_path', { path }))
    .catch(() => {
      /* best effort */
    });
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

/// The host (`example.com`) of a URL, or the raw string if it doesn't parse.
/// Shared by the reader's web tabs + the in-app browser (was duplicated).
export function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

/// Whether the page at `url` refuses to be embedded in an iframe
/// (`X-Frame-Options` / CSP `frame-ancestors`) — the Rust `frame_check` command
/// preflights the response headers, which the webview's own fetch can't read
/// (CORS). Drives the in-app browser tab's refused-frame error (#322). The
/// browser build has no way to check, so it answers false and lets the frame try.
export async function frameCheck(url: string): Promise<boolean> {
  if (url === '' || !isTauri()) return false;
  try {
    const { invoke } = await import('@tauri-apps/api/core');
    return await invoke<boolean>('frame_check', { url });
  } catch {
    return false; // unreachable / unsupported scheme — not a refusal
  }
}
