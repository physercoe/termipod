import { invoke } from '@tauri-apps/api/core';
import { listen, type UnlistenFn } from '@tauri-apps/api/event';

/// Thin typed bridge to the Tauri Rust core's `russh` SSH transport (ADR-052,
/// personal direct-SSH). Only functional under the desktop (Tauri) build — the
/// plain-browser build has no native core, so `isTauri()` gates the UI.

export function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}

export interface SshConnectReq {
  host: string;
  port: number;
  user: string;
  password?: string;
  private_key?: string;
  passphrase?: string;
  cols: number;
  rows: number;
}

/** Open a session + PTY; resolves to the session id used by the calls below. */
export function sshConnect(req: SshConnectReq): Promise<string> {
  return invoke<string>('ssh_connect', { req });
}
export function sshWrite(id: string, data: string): Promise<void> {
  return invoke('ssh_write', { id, data });
}
export function sshResize(id: string, cols: number, rows: number): Promise<void> {
  return invoke('ssh_resize', { id, cols, rows });
}
export function sshClose(id: string): Promise<void> {
  return invoke('ssh_close', { id });
}

interface DataPayload {
  id: string;
  bytes: number[];
}
interface ExitPayload {
  id: string;
}

/** Subscribe to PTY output for one session (bytes → xterm). */
export function onSshData(id: string, cb: (bytes: Uint8Array) => void): Promise<UnlistenFn> {
  return listen<DataPayload>('ssh-data', (e) => {
    if (e.payload.id === id) cb(new Uint8Array(e.payload.bytes));
  });
}
/** Fired when the remote shell / channel closes. */
export function onSshExit(id: string, cb: () => void): Promise<UnlistenFn> {
  return listen<ExitPayload>('ssh-exit', (e) => {
    if (e.payload.id === id) cb();
  });
}
