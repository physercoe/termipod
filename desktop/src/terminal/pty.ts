import { invoke } from '@tauri-apps/api/core';
import { listen, type UnlistenFn } from '@tauri-apps/api/event';

/// Typed bridge to the Tauri core's local-PTY commands (pty.rs, → ADR-053). The
/// desktop analogue of `ssh/tauri.ts`: same shape, but the shell runs on the
/// user's *own* machine rather than over SSH. Desktop-only — the plain-browser
/// build has no native core.

export interface PtyOpenReq {
  /** Program binary; omit to use `$SHELL` / `%COMSPEC%`. For a local agent this is
   *  the engine CLI (e.g. `claude`, `codex`). */
  shell?: string;
  /** Extra argv passed to the program (agent flags). Each is a distinct arg. */
  args?: string[];
  /** Working directory; omit for the process cwd. */
  cwd?: string;
  cols: number;
  rows: number;
}

export interface PtyOpened {
  /** Session id used for start/write/resize/close and to filter `pty-data`. */
  id: string;
  /** The shell actually launched (e.g. `C:\\Windows\\system32\\cmd.exe`,
   *  `/bin/bash`) — lets the caller decide whether the POSIX shell-integration
   *  script applies. */
  shell: string;
}

/** Open a local shell in a PTY; resolves to the session id + resolved shell. The
 *  shell is created but NOT read until {@link ptyStart} — call that only once the
 *  `pty-data`/`pty-exit` listeners are attached, so the first prompt can't race
 *  ahead of the subscriber (the black-local-shell bug). */
export function ptyOpen(req: PtyOpenReq): Promise<PtyOpened> {
  return invoke<PtyOpened>('pty_open', { req });
}
/** Begin streaming a shell opened by {@link ptyOpen}. Idempotent. */
export function ptyStart(id: string): Promise<void> {
  return invoke('pty_start', { id });
}
export function ptyWrite(id: string, data: string): Promise<void> {
  return invoke('pty_write', { id, data });
}
export function ptyResize(id: string, cols: number, rows: number): Promise<void> {
  return invoke('pty_resize', { id, cols, rows });
}
export function ptyClose(id: string): Promise<void> {
  return invoke('pty_close', { id });
}

interface DataPayload {
  id: string;
  bytes: number[];
}
interface ExitPayload {
  id: string;
}

/** Subscribe to PTY output for one local session (bytes → xterm). */
export function onPtyData(id: string, cb: (bytes: Uint8Array) => void): Promise<UnlistenFn> {
  return listen<DataPayload>('pty-data', (e) => {
    if (e.payload.id === id) cb(new Uint8Array(e.payload.bytes));
  });
}
/** Fired when the local shell exits. */
export function onPtyExit(id: string, cb: () => void): Promise<UnlistenFn> {
  return listen<ExitPayload>('pty-exit', (e) => {
    if (e.payload.id === id) cb();
  });
}
