/// Real in-app browser tab (the Read surface's web tab) — main-process policy
/// for `<webview>` guests, all in one module so the renderer contract stays one
/// component (`src/surfaces/BrowserView.tsx`) and the `WebContentsView` fallback,
/// if webview flakiness ever bites, is a swap inside this boundary.
///
/// Why `<webview>` and not an `<iframe>`: `X-Frame-Options`/`frame-ancestors`
/// do NOT apply to a webview guest (it is a top-level frame, not an embedded
/// one), so arXiv / publisher landing pages / GitHub / Scholar actually load —
/// the iframe reader was a bounce page for nearly every site the reading
/// workflow needs. See docs/plans/read-web-tabs-and-pdf-attachments.md.
///
/// Everything security-relevant is enforced HERE, in the authority process, not
/// in renderer markup a compromised renderer could forge:
///   - guests run in an isolated, persistent `persist:webtab` partition — none
///     of the `app://`/`drawio://` scheme handlers or the hub-CORS bearer
///     injection (installed on `defaultSession` only) is reachable, and no
///     preload is attached, so `__ELECTRON_BRIDGE__` never exists in a guest;
///   - `will-attach-webview` strips any preload + forces
///     nodeIntegration:false/contextIsolation:true and rejects any partition
///     other than `persist:webtab`;
///   - popups are denied (a safe http(s) `target=_blank` becomes an in-tab
///     navigation; anything else goes to the OS browser);
///   - navigation is http(s)-only; permission requests are denied except
///     fullscreen; downloads land through a save dialog until W2b wires them to
///     the managed-attachment flow.
import { app, session, shell } from 'electron';
import path from 'node:path';
import type { Handler } from './ipc/dispatch';
import { isSafeExternal } from './ipc/platform';

const WEBTAB_PARTITION = 'persist:webtab';

function webtabSession(): Electron.Session {
  return session.fromPartition(WEBTAB_PARTITION);
}

/// The stock-Chrome user agent Electron derives from — with the `Electron/x.y`
/// and app-name tokens stripped. Several sites (Scholar, Cloudflare) degrade or
/// block non-Chrome UAs; the remaining string is a plain Chrome UA.
function stockChromeUA(): string {
  return session.defaultSession
    .getUserAgent()
    .replace(/ Electron\/\S+/i, '')
    .replace(new RegExp(` ${app.getName()}\\/\\S+`, 'i'), '')
    .trim();
}

/// Apply the app's proxy to the webtab session — same semantics as the updater:
/// an explicit proxy when configured, else Chromium's own system resolution.
async function applyProxy(proxy: string | null): Promise<void> {
  const ses = webtabSession();
  await ses.setProxy(proxy === null || proxy === '' ? { mode: 'system' } : { proxyRules: proxy });
}

/// Wire the webtab partition + the embedder/guest hardening. Called once from
/// `main.ts` whenReady, before any window exists (so `web-contents-created`
/// catches the main window's own webContents and its `will-attach-webview`).
export function setupWebtab(): void {
  const ses = webtabSession();
  ses.setUserAgent(stockChromeUA());
  // Default to system-proxy resolution; the renderer pushes an explicit override
  // via `webtab_set_proxy` when Settings → Network configures one.
  void applyProxy(null);
  // Deny every permission a preview browser has no business granting; allow only
  // fullscreen (video). Applies to the whole partition.
  ses.setPermissionRequestHandler((_wc, permission, cb) => cb(permission === 'fullscreen'));
  // Downloads default (until W2b's chooser): force a save dialog into Downloads
  // so a click on a PDF link isn't silently swallowed or auto-dumped.
  ses.on('will-download', (_e, item) => {
    item.setSaveDialogOptions({ defaultPath: path.join(app.getPath('downloads'), item.getFilename()) });
  });

  app.on('web-contents-created', (_e, wc) => {
    // The embedder (main window) attaching a webview: lock the guest's
    // webPreferences down no matter what the renderer markup asked for.
    wc.on('will-attach-webview', (evt, webPreferences, params) => {
      delete webPreferences.preload;
      webPreferences.nodeIntegration = false;
      webPreferences.contextIsolation = true;
      webPreferences.sandbox = true;
      // A guest may only ever run in the isolated persistent partition — never
      // the default session (where the scheme handlers + hub CORS live).
      if (params.partition !== WEBTAB_PARTITION) evt.preventDefault();
    });

    // The guest itself: popup + navigation policy. `getType()` is 'webview' for
    // a guest; leave the app's own top frame (handled in main.ts) untouched.
    if (wc.getType() === 'webview') {
      wc.setWindowOpenHandler(({ url }) => {
        // A safe http(s) `target=_blank` stays in-tab (reading flow); a mailto:
        // (or other safe scheme) goes to the OS; everything else is dropped.
        if (/^https?:\/\//i.test(url)) void wc.loadURL(url);
        else if (isSafeExternal(url)) void shell.openExternal(url);
        return { action: 'deny' };
      });
      wc.on('will-navigate', (e, url) => {
        if (!/^https?:\/\//i.test(url)) e.preventDefault();
      });
    }
  });
}

export const webtabHandlers: Record<string, Handler> = {
  /// Push the app's effective proxy (or null for system resolution) to the
  /// webtab session. The renderer calls this from BrowserView with
  /// `proxyForConnection('webtab')`.
  webtab_set_proxy: async (args): Promise<void> => {
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : null;
    await applyProxy(proxy);
  },

  /// Clear the persistent web-tab browsing data (cookies, storage, cache) —
  /// Settings → Network "Clear web-tab browsing data".
  webtab_clear_data: async (): Promise<void> => {
    await webtabSession().clearStorageData();
  },
};
