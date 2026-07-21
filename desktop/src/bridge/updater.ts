/// Updater/app-lifecycle wrappers (ADR-055 D-5, plan M0/M3).
///
/// The auto-update path is the one place still bound to Tauri plugins today
/// (`@tauri-apps/plugin-updater`, `@tauri-apps/plugin-process`, and app
/// version). It becomes electron-updater in M3; for now these thin wrappers
/// keep the `@tauri-apps` imports out of `UpdateSection.tsx` (M0 acceptance:
/// no `@tauri-apps` import outside `src/bridge/`) and give the migration one
/// seam to re-point. Each plugin is a cached-free lazy import, so the browser
/// build never pulls it.
///
/// Every wrapper guards on `shellKind() === 'tauri'` — the Tauri plugins talk
/// to Tauri internals directly (not through the bridge), so under Electron
/// they would throw, not degrade. Until M3 wires electron-updater, a non-Tauri
/// shell answers "up to date" / build-time version instead of erroring.

import type { CheckOptions, Update } from '@tauri-apps/plugin-updater';
import { shellKind } from './index';

export type { Update };

/// The running app version (build metadata), from the shell runtime; the
/// build-time constant everywhere else.
export async function appVersion(): Promise<string> {
  if (shellKind() !== 'tauri') return __APP_VERSION__;
  return (await import('@tauri-apps/api/app')).getVersion();
}

/// Check the signed release manifest for an update; `null` when up to date
/// (and always `null` off Tauri until M3's electron-updater lands).
export async function checkUpdate(options?: CheckOptions): Promise<Update | null> {
  if (shellKind() !== 'tauri') return null;
  return (await import('@tauri-apps/plugin-updater')).check(options);
}

/// Relaunch the app (after an update install). No-op off Tauri until M3.
export async function relaunchApp(): Promise<void> {
  if (shellKind() !== 'tauri') return;
  return (await import('@tauri-apps/plugin-process')).relaunch();
}
