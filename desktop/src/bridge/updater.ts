/// Updater/app-lifecycle wrappers (ADR-055 D-5, plan M0/M3).
///
/// The auto-update path is the one place still bound to Tauri plugins today
/// (`@tauri-apps/plugin-updater`, `@tauri-apps/plugin-process`, and app
/// version). It becomes electron-updater in M3; for now these thin wrappers
/// keep the `@tauri-apps` imports out of `UpdateSection.tsx` (M0 acceptance:
/// no `@tauri-apps` import outside `src/bridge/`) and give the migration one
/// seam to re-point. Each plugin is a cached-free lazy import, so the browser
/// build never pulls it; callers gate on `isShell()`.

import type { CheckOptions, Update } from '@tauri-apps/plugin-updater';

export type { Update };

/// The running app version (build metadata), from the shell runtime.
export async function appVersion(): Promise<string> {
  return (await import('@tauri-apps/api/app')).getVersion();
}

/// Check the signed release manifest for an update; `null` when up to date.
export async function checkUpdate(options?: CheckOptions): Promise<Update | null> {
  return (await import('@tauri-apps/plugin-updater')).check(options);
}

/// Relaunch the app (after an update install).
export async function relaunchApp(): Promise<void> {
  return (await import('@tauri-apps/plugin-process')).relaunch();
}
