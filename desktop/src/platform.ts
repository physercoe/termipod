/// Platform helpers layered on the shell bridge. Under a native shell the
/// webview origin (`tauri://localhost`, or the Electron custom scheme) makes a
/// direct `fetch` to the hub cross-origin with no CORS headers — it fails with
/// "Failed to fetch". Hub HTTP is therefore routed through the native core
/// whenever `isShell()` is true; the plain browser build keeps using `fetch`.
import { invoke, isShell, shellKind } from './bridge';

// Shell detection lives in the bridge; re-exported here so the many callers
// that already import it `from '../platform'` keep working unchanged.
export { isShell, shellKind };

/// The host OS, from the native `platform_os` command ("windows" | "macos" |
/// "linux" | …). Compile-time exact (target triple) — unlike navigator.userAgent,
/// which the webview can present inconsistently and which a wrong read would turn
/// into a black-screen renderer choice (#333). Cached; `''` in the browser build.
let osCache: string | null = null;
export async function platformOs(): Promise<string> {
  if (osCache !== null) return osCache;
  if (!isShell()) {
    osCache = '';
    return osCache;
  }
  try {
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

/// The Windows OS build number (e.g. 22631) from the native `os_build_number`
/// command, or `null` off Windows / on any failure. The terminal passes it to
/// xterm's `windowsPty` so xterm applies the ConPTY reflow behaviour correct for
/// this build (native wrap sequences landed in build 21376). Cached; -1 sentinel
/// distinguishes "not yet fetched" from a legitimate `null`.
let buildCache: number | null | -1 = -1;
export async function windowsBuildNumber(): Promise<number | null> {
  if (buildCache !== -1) return buildCache;
  if (!isShell()) {
    buildCache = null;
    return buildCache;
  }
  try {
    const n = await invoke<number | null>('os_build_number');
    buildCache = typeof n === 'number' && Number.isFinite(n) ? n : null;
  } catch {
    buildCache = null;
  }
  return buildCache;
}

/// Open an external URL in the OS default browser — never in the app webview.
/// Under a native shell a raw navigation replaces the single-webview SPA and
/// strands the user (director report: a link inside a PDF "jumped the whole app"
/// with no way back); the native `open_external` command hands the URL to the OS
/// instead. The browser build opens a new tab. Only http(s)/mailto are honoured.
export function openExternal(url: string): void {
  if (url === '') return;
  if (isShell()) {
    void invoke('open_external', { url }).catch(() => {
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
  if (path === '' || !isShell()) return;
  void invoke('reveal_path', { path }).catch(() => {
    /* best effort */
  });
}

/// Open a URL in a real in-app browser *window* (a native webview, not an
/// iframe), so sites that forbid framing (`X-Frame-Options` — arxiv, Google
/// Scholar, most publishers) still load. The browser build falls back to a new
/// tab.
export function openBrowserWindow(url: string): void {
  if (url === '') return;
  if (isShell()) {
    void invoke('open_browser_window', { url }).catch(() => {
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

