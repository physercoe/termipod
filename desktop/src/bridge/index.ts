/// Runtime-agnostic shell bridge (ADR-055 D-2, plan M0).
///
/// The desktop app runs under one of three shells: **Tauri** (today's OS
/// webview), **Electron** (the migration target, ADR-055), or a plain
/// **browser** (the degrade build, no native core). Every `invoke`/`listen`
/// call in the app funnels through this module instead of importing a shell
/// SDK directly, so the migration is an import-swap — not a rewrite of the
/// 38k-line frontend (plan §1).
///
/// **Why the SDK imports are lazy.** Importing this module must NOT statically
/// pull `@tauri-apps/api` into a chunk, or the browser build (and every entry
/// chunk) would carry the Tauri IPC code. So the shell SDK is loaded with a
/// cached dynamic `import()` on first use; under the browser shell it is never
/// loaded at all. This is the same discipline the old per-call
/// `await import('@tauri-apps/api/core')` sites used, centralized here.
///
/// Types (`InvokeArgs`, `EventCallback`, `UnlistenFn`) are re-exported from the
/// Tauri SDK as **type-only** imports — erased at build time, zero runtime
/// bundle — so existing call sites keep byte-identical types across the swap.

import type { InvokeArgs } from '@tauri-apps/api/core';
import type { EventCallback, UnlistenFn } from '@tauri-apps/api/event';

export type { InvokeArgs, EventCallback, UnlistenFn };

/// The Electron preload injects this surface on `window`; it mirrors the two
/// primitives the bridge needs. Absent under Tauri/browser (dormant until M1).
interface ElectronBridge {
  invoke<T>(cmd: string, args?: InvokeArgs): Promise<T>;
  listen<T>(event: string, cb: EventCallback<T>): Promise<UnlistenFn>;
}

declare global {
  interface Window {
    __TAURI_INTERNALS__?: unknown;
    __ELECTRON_BRIDGE__?: ElectronBridge;
  }
}

export type ShellKind = 'tauri' | 'electron' | 'browser';

/// Which shell backs this build, decided at runtime from injected globals.
/// Tauri sets `__TAURI_INTERNALS__`; the Electron preload sets
/// `__ELECTRON_BRIDGE__`; anything else is the plain browser build.
export function shellKind(): ShellKind {
  if (typeof window === 'undefined') return 'browser';
  if ('__TAURI_INTERNALS__' in window) return 'tauri';
  if (window.__ELECTRON_BRIDGE__ !== undefined) return 'electron';
  return 'browser';
}

/// True when a native shell (Tauri or Electron) backs the app — i.e. the native
/// command surface is available. Successor to the old `isTauri()`; the browser
/// build answers false and the app takes its degrade paths.
export function isShell(): boolean {
  return shellKind() !== 'browser';
}

// Cached dynamic imports of the Tauri SDK — resolved once, on first use.
let coreP: Promise<typeof import('@tauri-apps/api/core')> | null = null;
let eventP: Promise<typeof import('@tauri-apps/api/event')> | null = null;
const core = (): Promise<typeof import('@tauri-apps/api/core')> => (coreP ??= import('@tauri-apps/api/core'));
const event = (): Promise<typeof import('@tauri-apps/api/event')> => (eventP ??= import('@tauri-apps/api/event'));

/// Invoke a native command. Routes to the active shell's IPC; throws in the
/// browser build (callers gate native-only commands behind `isShell()`).
export async function invoke<T = unknown>(cmd: string, args?: InvokeArgs): Promise<T> {
  switch (shellKind()) {
    case 'tauri':
      return (await core()).invoke<T>(cmd, args);
    case 'electron':
      return window.__ELECTRON_BRIDGE__!.invoke<T>(cmd, args);
    default:
      throw new Error(`bridge.invoke: no native shell for command '${cmd}'`);
  }
}

/// Subscribe to a native event. Returns an unlisten function; a no-op in the
/// browser build (no event source), so callers can always store + call it.
export async function listen<T = unknown>(name: string, cb: EventCallback<T>): Promise<UnlistenFn> {
  switch (shellKind()) {
    case 'tauri':
      return (await event()).listen<T>(name, cb);
    case 'electron':
      return window.__ELECTRON_BRIDGE__!.listen<T>(name, cb);
    default:
      return () => {};
  }
}

export { appVersion, checkUpdate, relaunchApp } from './updater';
export type { Update } from './updater';
