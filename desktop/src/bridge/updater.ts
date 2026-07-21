/// Updater/app-lifecycle wrappers (ADR-055 D-5, plan M0/M3).
///
/// Two backends live behind one seam so `UpdateSection.tsx` is shell-agnostic:
///   - **Tauri**: `@tauri-apps/plugin-updater`/`-process` (talk to Tauri
///     internals directly, not through the bridge — lazy-imported so the
///     browser build never pulls them).
///   - **Electron** (M3.2): the main-process `electron-updater` bridge
///     (`updater_check`/`_download`/`_install` + `app_version`, from
///     `electron/src/ipc/updater.ts`). electron-updater is event-driven, so we
///     synthesize the SAME `Update` object the Tauri plugin returns — a
///     `downloadAndInstall(cb)` that translates the main-process
///     `updater:progress` events into the plugin's `Started/Progress/Finished`
///     callback shape. The renderer can't tell the two apart.
///   - **browser**: answers "up to date" / build-time version.

import type { CheckOptions, Update, DownloadEvent } from '@tauri-apps/plugin-updater';
import { shellKind, invoke, listen } from './index';

export type { Update };

/// The running app version (build metadata), from the shell runtime; the
/// build-time constant everywhere else.
export async function appVersion(): Promise<string> {
  switch (shellKind()) {
    case 'tauri':
      return (await import('@tauri-apps/api/app')).getVersion();
    case 'electron':
      return invoke<string>('app_version');
    default:
      return __APP_VERSION__;
  }
}

/// Build the Electron-side `Update` — the same shape `UpdateSection` consumes
/// from the Tauri plugin. `downloadAndInstall` drives the main-process download,
/// forwards progress as the plugin's `DownloadEvent`s, then quits-and-installs.
/// The proxy from check time rides along to the download, matching the Tauri
/// plugin (its `Update` keeps the `CheckOptions` proxy for the download).
function electronUpdate(version: string, notes: string, proxy: string | null): Update {
  const downloadAndInstall = async (onEvent?: (e: DownloadEvent) => void): Promise<void> => {
    let started = false;
    const un = await listen<{ total: number; delta: number }>('updater:progress', (e) => {
      const p = e.payload;
      if (!started) {
        started = true;
        onEvent?.({ event: 'Started', data: { contentLength: p.total } });
      }
      onEvent?.({ event: 'Progress', data: { chunkLength: p.delta } });
    });
    try {
      await invoke('updater_download', { proxy });
      onEvent?.({ event: 'Finished' });
    } finally {
      un();
    }
    // Exits the app and relaunches into the new version.
    await invoke('updater_install');
  };
  // Only `version` / `body` / `downloadAndInstall` are consumed by the renderer;
  // the rest of the plugin's Update surface is unused here.
  return { version, body: notes, downloadAndInstall } as unknown as Update;
}

/// Check the signed release manifest for an update; `null` when up to date
/// (and always `null` in the browser build).
export async function checkUpdate(options?: CheckOptions): Promise<Update | null> {
  switch (shellKind()) {
    case 'tauri':
      return (await import('@tauri-apps/plugin-updater')).check(options);
    case 'electron': {
      const proxy = options?.proxy ?? null;
      const r = await invoke<{ version: string; notes: string } | null>('updater_check', { proxy });
      return r === null ? null : electronUpdate(r.version, r.notes, proxy);
    }
    default:
      return null;
  }
}

/// Relaunch the app (after an update install). Under Electron the install path
/// (`quitAndInstall`) already relaunches, so this is a no-op there.
export async function relaunchApp(): Promise<void> {
  if (shellKind() !== 'tauri') return;
  return (await import('@tauri-apps/plugin-process')).relaunch();
}
