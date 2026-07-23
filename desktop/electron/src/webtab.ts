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
///   - guests run only in allowlisted isolated partitions (webtab_policy.ts) —
///     none of the `app://`/`drawio://` scheme handlers or the hub-CORS bearer
///     injection (installed on `defaultSession` only) is reachable, and no
///     preload is attached, so `__ELECTRON_BRIDGE__` never exists in a guest;
///   - `will-attach-webview` strips any preload + forces
///     nodeIntegration:false/contextIsolation:true and rejects any partition
///     outside the allowlist;
///   - per-partition policy (webtab_policy.ts): `persist:webtab` allows any
///     http(s) origin and routes a safe http(s) `target=_blank` in-tab;
///     `kimiweb` pins top-frame navigation to loopback origins and never opens
///     popups in-tab (safe schemes go to the OS browser);
///   - permission requests are denied (except fullscreen on webtab); downloads
///     land through a save dialog until W2b wires them to the
///     managed-attachment flow.
import { app, session, shell } from 'electron';
import path from 'node:path';
import type { Handler } from './ipc/dispatch';
import { isSafeExternal } from './ipc/platform';
import { emit } from './events';
import {
  KIMIWEB_PARTITION,
  PARTITION_POLICIES,
  WEBTAB_PARTITION,
  isHttpUrl,
  isLoopbackHttpUrl,
  partitionPolicy,
  type PartitionPolicy,
} from './webtab_policy';

// Downloads paused in `will-download`, awaiting the Read surface's chooser
// decision (W2b). Keyed by an id echoed back through `webtab_download_decide`.
const pendingDownloads = new Map<string, Electron.DownloadItem>();
let downloadSeq = 0;

function webtabSession(): Electron.Session {
  return session.fromPartition(WEBTAB_PARTITION);
}

/// The allowlist policy for a guest webContents, identified by its session
/// (`session.fromPartition` is memoized per partition string, so identity
/// comparison works). `null` = not an allowlisted guest partition — the
/// `will-attach-webview` guard already refused the attach, so this is only a
/// defensive fallback that blocks everything.
function policyForGuest(wc: Electron.WebContents): PartitionPolicy | null {
  for (const p of PARTITION_POLICIES) {
    if (wc.session === session.fromPartition(p.partition)) return p;
  }
  return null;
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
  // The http(s)-only navigation policy, enforced at the request layer:
  // `will-navigate` (below) does NOT fire for programmatic loads — and
  // `webview.loadURL` is exactly how the renderer navigates (address bar) — nor
  // for server redirects, so a `file:`/custom-scheme top-frame load would slip
  // through it. Cancel any non-http(s) top-frame request for the partition
  // (about:blank makes no request; subresources are Chromium-policed).
  ses.webRequest.onBeforeRequest((details, cb) => {
    cb({ cancel: details.resourceType === 'mainFrame' && !/^https?:\/\//i.test(details.url) });
  });
  // A download started inside a web tab (W2b): pause it and ask the renderer's
  // Read surface how to handle it — attach to the selected reference, or save to
  // disk. The guest's embedder (`hostWebContents`) is the app renderer that owns
  // the chooser; with no embedder or no answer within the timeout we fall back to
  // a save dialog so a download is never silently lost or stuck paused.
  ses.on('will-download', (_e, item, webContents) => {
    const host = webContents?.hostWebContents ?? null;
    const saveToDisk = (): void => {
      item.setSaveDialogOptions({ defaultPath: path.join(app.getPath('downloads'), item.getFilename()) });
      if (item.isPaused()) item.resume();
    };
    if (host === null || host.isDestroyed()) {
      saveToDisk();
      return;
    }
    const id = `wtdl-${(downloadSeq += 1)}`;
    pendingDownloads.set(id, item);
    item.pause();
    emit(host, 'webtab:download', {
      id,
      url: item.getURL(),
      filename: item.getFilename(),
      mime: item.getMimeType(),
    });
    // Safety net: if the renderer never answers (no Read surface listening),
    // fall back to a save dialog rather than leaving the item paused forever.
    const timer = setTimeout(() => {
      if (!pendingDownloads.has(id)) return;
      pendingDownloads.delete(id);
      saveToDisk();
    }, 60_000);
    item.once('done', () => {
      pendingDownloads.delete(id);
      clearTimeout(timer);
    });
  });

  // The kimiweb partition (P0 embedded agent web UIs): NON-persistent — the
  // bearer token rides the URL hash (`#token=…`) and a persistent partition
  // would keep it in guest history on disk; the token is re-captured at each
  // spawn anyway. All permissions denied, and the loopback-only top-frame
  // policy is enforced at the request layer for the same reason as above
  // (`will-navigate` misses programmatic loads + redirects).
  const kimi = session.fromPartition(KIMIWEB_PARTITION);
  kimi.setPermissionRequestHandler((_wc, _permission, cb) => cb(false));
  kimi.webRequest.onBeforeRequest((details, cb) => {
    cb({ cancel: details.resourceType === 'mainFrame' && !isLoopbackHttpUrl(details.url) });
  });

  app.on('web-contents-created', (_e, wc) => {
    // The embedder (main window) attaching a webview: lock the guest's
    // webPreferences down no matter what the renderer markup asked for.
    wc.on('will-attach-webview', (evt, webPreferences, params) => {
      delete webPreferences.preload;
      webPreferences.nodeIntegration = false;
      webPreferences.contextIsolation = true;
      webPreferences.sandbox = true;
      // A guest may only ever run in an allowlisted isolated partition — never
      // the default session (where the scheme handlers + hub CORS live).
      if (params.partition === undefined || partitionPolicy(params.partition) === null) {
        evt.preventDefault();
      }
    });

    // The guest itself: popup + navigation policy, looked up from the guest's
    // own partition. `getType()` is 'webview' for a guest; leave the app's own
    // top frame (handled in main.ts) untouched.
    if (wc.getType() === 'webview') {
      const policy = policyForGuest(wc);
      wc.setWindowOpenHandler(({ url }) => {
        // webtab ('inline'): a safe http(s) `target=_blank` stays in-tab (the
        // reading flow). kimiweb ('external'): nothing opens in-tab — the nav
        // policy would block a non-loopback load anyway, so safe schemes go
        // straight to the OS browser. Everything else is dropped.
        if (policy?.windowOpen === 'inline' && isHttpUrl(url)) void wc.loadURL(url);
        else if (isSafeExternal(url)) void shell.openExternal(url);
        return { action: 'deny' };
      });
      wc.on('will-navigate', (e, url) => {
        if (policy === null || !policy.allowTopFrame(url)) e.preventDefault();
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

  /// The Read-surface chooser answers a `webtab:download` (W2b): 'attach' (cancel
  /// the Electron item — the renderer re-fetches the URL into a managed
  /// attachment via `attachment_download`), 'save' (resume with a save dialog),
  /// or 'cancel' (drop it).
  webtab_download_decide: async (args): Promise<void> => {
    const id = typeof args.id === 'string' ? args.id : '';
    const action = typeof args.action === 'string' ? args.action : '';
    const item = pendingDownloads.get(id);
    if (item === undefined) return;
    pendingDownloads.delete(id);
    if (action === 'save') {
      item.setSaveDialogOptions({ defaultPath: path.join(app.getPath('downloads'), item.getFilename()) });
      if (item.isPaused()) item.resume();
    } else {
      // 'attach' (re-fetched through attachment_download) or 'cancel' — the
      // Electron download itself is unused; cancel it.
      item.cancel();
    }
  },
};
