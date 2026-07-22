/// Platform-helper command family (ADR-055 M1) — the Electron equivalents of
/// the Tauri `platform_os` / `os_build_number` / `open_external` / `reveal_path`
/// / `open_browser_window` / `frame_check` / `system_proxy` / `system_hostname`
/// commands in `src-tauri/src/lib.rs`. Same command names + return shapes, so
/// `src/platform.ts` and `src/discovery/*` call them unchanged through the
/// bridge.
import { BrowserWindow, session, shell } from 'electron';
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
export function isSafeExternal(url: string): boolean {
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

/// Proxy from the standard env vars, or null. Kept as the FIRST source so an
/// explicit `HTTPS_PROXY`/`ALL_PROXY` still wins (a CI/terminal launch, or a
/// user override) before the OS resolver.
function envProxy(): string | null {
  const raw = process.env.HTTPS_PROXY ?? process.env.https_proxy ?? process.env.ALL_PROXY ?? process.env.all_proxy;
  if (raw === undefined || raw.trim() === '') return null;
  return raw.trim();
}

/// Turn a Chromium PAC result (`session.resolveProxy`) into a proxy URL string —
/// the same `scheme://host:port` shape the Rust `normalize_proxy` produced, so
/// the renderer's `proxyForConnection` consumers are unchanged. The result is a
/// `; `-separated list of directives (`DIRECT`, `PROXY h:p`, `HTTPS h:p`,
/// `SOCKS h:p`, `SOCKS5 h:p`); we take the first non-DIRECT one.
function pacToProxyUrl(pac: string): string | null {
  for (const entry of pac.split(';')) {
    const parts = entry.trim().split(/\s+/);
    const kind = (parts[0] ?? '').toUpperCase();
    const hostPort = parts[1] ?? '';
    if (kind === 'DIRECT' || kind === '') continue;
    if (hostPort === '') continue;
    const scheme = kind === 'HTTPS' ? 'https' : kind === 'SOCKS5' ? 'socks5' : kind === 'SOCKS' ? 'socks4' : 'http';
    return `${scheme}://${hostPort}`;
  }
  return null;
}

/// Resolve the system proxy for outbound external traffic (updater/sync/drawio;
/// the hub is intranet-direct). Env vars first, then Chromium's own resolver —
/// which reads the WINDOWS registry / WPAD / PAC and the macOS SystemConfiguration
/// that the env-only M1 stand-in missed (Windows Settings sets no env var, so a
/// system proxy there was invisible). Resolved against a representative public
/// HTTPS host: the app carries one global proxy string, so a single external
/// probe is the right granularity (per-host PAC divergence is out of scope here).
async function resolveSystemProxy(): Promise<string | null> {
  const fromEnv = envProxy();
  if (fromEnv !== null) return fromEnv;
  try {
    const pac = await session.defaultSession.resolveProxy('https://github.com');
    return pacToProxyUrl(pac);
  } catch {
    return null;
  }
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
    // Popups from third-party pages go to the OS browser, never to another
    // shell window this process owns.
    win.webContents.setWindowOpenHandler(({ url: popup }) => {
      if (isSafeExternal(popup)) void shell.openExternal(popup);
      return { action: 'deny' };
    });
    void win.loadURL(url);
  },

  frame_check: (args: Record<string, unknown>) => {
    const url = typeof args.url === 'string' ? args.url : '';
    if (url === '') return false;
    return frameRefused(url);
  },

  system_proxy: () => resolveSystemProxy(),

  system_hostname: () => {
    const h = os.hostname();
    return h === '' ? null : h;
  },
};
