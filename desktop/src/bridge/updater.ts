/// Updater/app-lifecycle wrappers (ADR-055 D-5, plan M3).
///
/// One backend behind the seam so `UpdateSection.tsx` is shell-agnostic:
///   - **Electron**: the main-process `electron-updater` bridge
///     (`updater_check`/`_download`/`_install` + `app_version`, from
///     `electron/src/ipc/updater.ts`). electron-updater is event-driven, so we
///     synthesize an `Update` object with a `downloadAndInstall(cb)` that
///     translates the main-process `updater:progress` events into the
///     `Started/Progress/Finished` callback shape `UpdateSection` consumes.
///   - **browser**: answers "up to date" / build-time version.
///
/// (The Tauri backend was retired at the M3.4 cutover; these local types mirror
/// the shape the renderer consumes — see `bridge/index.ts`.)

import { shellKind, invoke, listen } from './index';

/// Options for an update check. `proxy` rides along to the download.
export interface CheckOptions {
  proxy?: string;
}

/// A download-progress event forwarded to `UpdateSection`.
export type DownloadEvent =
  | { event: 'Started'; data: { contentLength?: number } }
  | { event: 'Progress'; data: { chunkLength: number } }
  | { event: 'Finished' };

/// A pending update. Only `version` / `body` / `downloadAndInstall` are consumed
/// by the renderer.
export interface Update {
  version: string;
  body: string;
  downloadAndInstall(onEvent?: (e: DownloadEvent) => void): Promise<void>;
}

/// The running app version (build metadata), from the shell runtime; the
/// build-time constant everywhere else.
export async function appVersion(): Promise<string> {
  if (shellKind() === 'electron') return invoke<string>('app_version');
  return __APP_VERSION__;
}

/// Build the Electron-side `Update`. `downloadAndInstall` drives the
/// main-process download, forwards progress as `DownloadEvent`s, then
/// quits-and-installs. The proxy from check time rides along to the download.
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
  return { version, body: notes, downloadAndInstall };
}

/// Check the signed release manifest for an update; `null` when up to date
/// (and always `null` in the browser build).
export async function checkUpdate(options?: CheckOptions): Promise<Update | null> {
  if (shellKind() === 'electron') {
    const proxy = options?.proxy ?? null;
    const r = await invoke<{ version: string; notes: string } | null>('updater_check', { proxy });
    return r === null ? null : electronUpdate(r.version, r.notes, proxy);
  }
  return null;
}

/// Relaunch the app after an update install. Under Electron the install path
/// (`quitAndInstall`) already relaunches, so this is a no-op (kept as a stable
/// seam for `UpdateSection`).
export async function relaunchApp(): Promise<void> {
  // No-op: Electron's `updater_install` quits-and-relaunches; the browser build
  // has nothing to relaunch.
}
