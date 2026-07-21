/// Auto-update (ADR-055 M3.2) — the main-process electron-updater bridge.
///
/// The Tauri build used `tauri-plugin-updater`: the renderer got an `Update`
/// object with a `downloadAndInstall(cb)` method. electron-updater instead lives
/// in the main process and drives everything through events. This module exposes
/// three commands + one progress event so `src/bridge/updater.ts` can synthesize
/// the SAME `Update` shape the renderer already consumes — `UpdateSection.tsx`
/// is untouched:
///
///   updater_check    → { version, notes } | null   (no download)
///   updater_download → downloads, emits `updater:progress`, resolves when done
///   updater_install  → quitAndInstall (relaunches into the new version)
///   app_version      → the running app version (for the "current" label)
///
/// The feed is baked into `app-update.yml` from electron-builder's `publish`
/// config: a `generic` URL at the rolling `electron-latest` GitHub release
/// (NOT the github provider — that resolves via `releases/latest`, which the
/// mobile lane owns in this shared repo; see electron-builder.yml / the release
/// workflow). Auto-download is off so the UI drives (and shows progress);
/// checks/downloads are no-ops off a packaged build (electron-updater refuses to
/// run unpacked, and dev has no feed).
import { app } from 'electron';
import type { Handler } from './dispatch';
import { emit } from '../events';

// electron-updater is a pure-JS prod dep, lazy-loaded (main-process only) and
// esbuild-external so it resolves from node_modules at runtime. Typed loosely:
// it is externalized, and the surface used here is small and stable.
/* eslint-disable @typescript-eslint/no-explicit-any */
type AutoUpdater = any;

// Computed specifier (as in vault.ts): opaque to tsc + esbuild so the build
// needs no build-time resolution of the externalized module — it resolves from
// node_modules at runtime.
const UPDATER_MODULE = 'electron-updater';

let auP: Promise<AutoUpdater> | null = null;
async function autoUpdater(): Promise<AutoUpdater> {
  if (auP === null) {
    auP = import(UPDATER_MODULE).then((m: any) => {
      const au: AutoUpdater = m.autoUpdater ?? m.default?.autoUpdater ?? m.default;
      // We drive download + install explicitly so the UI can show progress and
      // the user confirms the restart — no silent background update.
      au.autoDownload = false;
      au.autoInstallOnAppQuit = false;
      // The feed's assets live on GitHub's S3-backed release storage, which
      // rejects the multi-range requests differential download needs — skip
      // straight to full downloads instead of failing over noisily.
      au.disableDifferentialDownload = true;
      return au;
    });
  }
  return auP;
}

/// Route updater traffic through the user's configured proxy (plan §5 —
/// carried over from the Tauri plugin's `CheckOptions.proxy`; the renderer
/// passes `proxyForConnection('update')`). electron-updater fetches through its
/// own session (`netSession`), so the proxy is applied there; with no proxy
/// configured, restore system proxy resolution (the session default) rather
/// than leaving a previously-set proxy sticky.
async function applyProxy(au: AutoUpdater, proxy: unknown): Promise<void> {
  const rules = typeof proxy === 'string' && proxy !== '' ? proxy : null;
  await au.netSession.setProxy(rules === null ? { mode: 'system' } : { proxyRules: rules });
}

/// electron-updater's `releaseNotes` is `string | {version,note}[] | null`.
function notesToString(notes: unknown): string {
  if (typeof notes === 'string') return notes;
  if (Array.isArray(notes)) {
    return notes
      .map((n) => (n && typeof n === 'object' ? String((n as any).note ?? '') : String(n)))
      .filter((s) => s !== '')
      .join('\n\n');
  }
  return '';
}

export const updaterHandlers: Record<string, Handler> = {
  app_version: (): string => app.getVersion(),

  updater_check: async (args): Promise<{ version: string; notes: string } | null> => {
    if (!app.isPackaged) return null; // electron-updater refuses to run unpacked
    const au = await autoUpdater();
    await applyProxy(au, args.proxy);
    const result = await au.checkForUpdates();
    if (result === null || result === undefined) return null;
    const info = result.updateInfo;
    if (info === undefined || info === null) return null;
    // v6 reports availability directly; fall back to a version inequality.
    const available =
      typeof result.isUpdateAvailable === 'boolean'
        ? result.isUpdateAvailable
        : info.version !== app.getVersion();
    if (!available) return null;
    return { version: String(info.version), notes: notesToString(info.releaseNotes) };
  },

  // Download the pending update, streaming progress to the renderer as
  // `updater:progress` {total, transferred, delta, percent}. Resolves once the
  // bytes are on disk (the renderer then calls updater_install).
  updater_download: async (args, ctx): Promise<boolean> => {
    if (!app.isPackaged) throw new Error('updater: not available in a dev build');
    const au = await autoUpdater();
    await applyProxy(au, args.proxy);
    let last = 0;
    const onProgress = (p: any): void => {
      const transferred = Number(p?.transferred ?? 0);
      emit(ctx.sender, 'updater:progress', {
        total: Number(p?.total ?? 0),
        transferred,
        delta: Math.max(0, transferred - last),
        percent: Number(p?.percent ?? 0),
      });
      last = transferred;
    };
    au.on('download-progress', onProgress);
    try {
      await au.downloadUpdate();
    } finally {
      au.removeListener('download-progress', onProgress);
    }
    return true;
  },

  // Quit and install the downloaded update, relaunching into it. This exits the
  // app; the returned promise never really resolves to the renderer.
  updater_install: async (): Promise<boolean> => {
    if (!app.isPackaged) throw new Error('updater: not available in a dev build');
    const au = await autoUpdater();
    au.quitAndInstall(false, true); // not-silent, force-run-after
    return true;
  },
};
