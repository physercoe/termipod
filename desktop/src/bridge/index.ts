/// Runtime-agnostic shell bridge (ADR-055 D-2, plan M0).
///
/// The desktop app runs under one of two shells: **Electron** (the native
/// client, ADR-055) or a plain **browser** (the degrade build, no native core).
/// Every `invoke`/`listen` call in the app funnels through this module instead
/// of importing a shell SDK directly, so the frontend stays shell-agnostic — the
/// Electron preload injects `window.__ELECTRON_BRIDGE__` and the browser build
/// takes its degrade paths.
///
/// (The Tauri shell was retired at the M3.4 cutover — see ADR-055 §M3.4 and
/// `docs/changelog-desktop.md`. The `@tauri-apps` SDK is no longer a dependency,
/// so the IPC/event/updater types below are defined locally to match the shape
/// the Electron preload emits.)

/// Argument bag for a native command. Every call site passes an object literal
/// (mirrors Tauri's old `InvokeArgs` for the subset the app used).
export type InvokeArgs = Record<string, unknown>;

/// Event envelope delivered to a `listen` callback. The Electron preload emits
/// `{ event, id, payload }` (see `electron/src/preload.ts`); call sites read
/// `.payload`.
export interface BridgeEvent<T> {
  event: string;
  id: number;
  payload: T;
}
export type EventCallback<T> = (event: BridgeEvent<T>) => void;
export type UnlistenFn = () => void;

/// The Electron preload injects this surface on `window`; it mirrors the two
/// primitives the bridge needs. Absent in the browser build.
interface ElectronBridge {
  invoke<T>(cmd: string, args?: InvokeArgs): Promise<T>;
  listen<T>(event: string, cb: EventCallback<T>): Promise<UnlistenFn>;
}

declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: ElectronBridge;
  }
}

export type ShellKind = 'electron' | 'browser';

/// Which shell backs this build, decided at runtime from injected globals. The
/// Electron preload sets `__ELECTRON_BRIDGE__`; anything else is the plain
/// browser build.
export function shellKind(): ShellKind {
  if (typeof window === 'undefined') return 'browser';
  if (window.__ELECTRON_BRIDGE__ !== undefined) return 'electron';
  return 'browser';
}

/// True when the native (Electron) shell backs the app — i.e. the native
/// command surface is available. The browser build answers false and the app
/// takes its degrade paths.
export function isShell(): boolean {
  return shellKind() !== 'browser';
}

/// Invoke a native command. Routes to the Electron IPC bridge; throws in the
/// browser build (callers gate native-only commands behind `isShell()`).
export async function invoke<T = unknown>(cmd: string, args?: InvokeArgs): Promise<T> {
  if (shellKind() === 'electron') return window.__ELECTRON_BRIDGE__!.invoke<T>(cmd, args);
  throw new Error(`bridge.invoke: no native shell for command '${cmd}'`);
}

/// Subscribe to a native event. Returns an unlisten function; a no-op in the
/// browser build (no event source), so callers can always store + call it.
export async function listen<T = unknown>(name: string, cb: EventCallback<T>): Promise<UnlistenFn> {
  if (shellKind() === 'electron') return window.__ELECTRON_BRIDGE__!.listen<T>(name, cb);
  return () => {};
}

export { appVersion, checkUpdate, relaunchApp } from './updater';
export type { Update } from './updater';
