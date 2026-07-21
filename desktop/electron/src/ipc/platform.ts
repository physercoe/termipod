/// Platform-helper command family (ADR-055 M1) — the Electron equivalents of
/// the Tauri `platform_os` / `os_build_number` / `open_external` / `reveal_path`
/// / `open_browser_window` / `frame_check` / `system_proxy` / `system_hostname`
/// commands in `src-tauri/src/lib.rs`. Same command names + return shapes, so
/// `src/platform.ts` and `src/discovery/*` call them unchanged through the
/// bridge.
import { BrowserWindow, shell } from 'electron';
import os from 'node:os';
import type { Handler } from './dispatch';

/// process.platform → the frontend's OS token ("macos" | "windows" | "linux").
/// Mirrors the Rust `platform_os` mapping so `isWindows()` / the xterm renderer
/// ladder behave identically.
function osToken(): string {
  switch (process.platform) {
    case 'darwin':
      return 'macos';
    case 'win32':
      return 'windows';
    case 'linux':
      return 'linux';
    default:
      return process.platform;
  }
}

/// The Windows build number from `os.release()` ("10.0.22631" → 22631), else
/// null. The terminal feeds it to xterm's `windowsPty` for correct ConPTY
/// reflow (native wrap sequences landed in build 21376).
function windowsBuildNumber(): number | null {
  if (process.platform !== 'win32') return null;
  const parts = os.release().split('.');
  if (parts.length < 3) return null;
  const n = Number.parseInt(parts[2], 10);
  return Number.isFinite(n) ? n : null;
}

/// Only hand safe schemes to the OS (matches the frontend's own guard); never
/// let an arbitrary scheme reach `shell.openExternal`.
function isSafeExternal(url: string): boolean {
  try {
    const scheme = new URL(url).protocol;
    return scheme === 'http:' || scheme === 'https:' || scheme === 'mailto:';
  } catch {
    return false;
  }
}

/// Whether `url` refuses iframe embedding (`X-Frame-Options` deny/sameorigin, or
/// a CSP `frame-ancestors` that isn't `*`). Preflighted in main because the
/// renderer can't read those headers cross-origin. Best-effort: a failed probe
/// answers false (not a refusal) so the frame is at least attempted.
async function frameRefused(url: string): Promise<boolean> {
  try {
    const res = await fetch(url, { method: 'GET', redirect: 'follow' });
    const xfo = (res.headers.get('x-frame-options') ?? '').toLowerCase();
    if (xfo.includes('deny') || xfo.includes('sameorigin')) return true;
    const csp = res.headers.get('content-security-policy') ?? '';
    const fa = /frame-ancestors\s+([^;]+)/i.exec(csp);
    if (fa !== null && !fa[1].includes('*')) return true;
    return false;
  } catch {
    return false;
  }
}

/// A single global proxy string for outbound hub/updater HTTP, from the standard
/// env vars, or null. A faithful M1 stand-in for the Rust `system_proxy`
/// (registry/scutil probe); `session.resolveProxy` per-URL is the richer M2/M4
/// path (plan §7 row 3).
function envProxy(): string | null {
  const raw = process.env.HTTPS_PROXY ?? process.env.https_proxy ?? process.env.ALL_PROXY ?? process.env.all_proxy;
  if (raw === undefined || raw.trim() === '') return null;
  return raw.trim();
}

export const platformHandlers: Record<string, Handler> = {
  platform_os: () => osToken(),

  os_build_number: () => windowsBuildNumber(),

  open_external: (args: Record<string, unknown>) => {
    const url = typeof args.url === 'string' ? args.url : '';
    if (url !== '' && isSafeExternal(url)) void shell.openExternal(url);
  },

  reveal_path: (args: Record<string, unknown>) => {
    const p = typeof args.path === 'string' ? args.path : '';
    if (p !== '') shell.showItemInFolder(p);
  },

  open_browser_window: (args: Record<string, unknown>) => {
    const url = typeof args.url === 'string' ? args.url : '';
    if (url === '' || !isSafeExternal(url)) return;
    // A real, separate window (not an iframe) so X-Frame-Options sites load.
    // No preload / no bridge: this is untrusted third-party web content.
    const win = new BrowserWindow({
      width: 1100,
      height: 800,
      title: 'TermiPod',
      webPreferences: { contextIsolation: true, sandbox: true, nodeIntegration: false },
    });
    void win.loadURL(url);
  },

  frame_check: (args: Record<string, unknown>) => {
    const url = typeof args.url === 'string' ? args.url : '';
    if (url === '') return false;
    return frameRefused(url);
  },

  system_proxy: () => envProxy(),

  system_hostname: () => {
    const h = os.hostname();
    return h === '' ? null : h;
  },
};
