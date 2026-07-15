import { invoke } from '@tauri-apps/api/core';
import { listen, type UnlistenFn } from '@tauri-apps/api/event';
import { isTauri } from '../platform';

/// Thin typed bridge to the Tauri Rust core's `russh` SSH transport (ADR-052,
/// personal direct-SSH). Only functional under the desktop (Tauri) build — the
/// plain-browser build has no native core, so `isTauri()` gates the UI.

export { isTauri };

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
/** Run a one-shot command over a fresh exec channel on an existing session and
 * resolve its stdout (+stderr). General remote-exec substrate. */
export function sshExec(id: string, command: string): Promise<string> {
  return invoke<string>('ssh_exec', { id, command });
}

export interface SftpEntry {
  name: string;
  is_dir: boolean;
  size: number;
}
/** List a remote directory over an SFTP subsystem on the session. */
export function sftpList(id: string, path: string): Promise<SftpEntry[]> {
  return invoke<SftpEntry[]>('sftp_list', { id, path });
}
/** Download a remote file; resolves to its base64-encoded bytes. Emits
 *  `sftp-progress` ticks tagged with `transferId` as it streams. */
export function sftpRead(id: string, path: string, transferId: string): Promise<string> {
  return invoke<string>('sftp_read', { id, path, transferId });
}
/** Upload base64 bytes to a remote path (create/overwrite). Emits
 *  `sftp-progress` ticks tagged with `transferId` as it streams. */
export function sftpWrite(id: string, path: string, dataB64: string, transferId: string): Promise<void> {
  return invoke('sftp_write', { id, path, dataB64, transferId });
}

interface SftpProgressPayload {
  transfer_id: string;
  done: number;
}
/** Subscribe to byte-progress ticks for one transfer (`done` = bytes moved). */
export function onSftpProgress(transferId: string, cb: (done: number) => void): Promise<UnlistenFn> {
  return listen<SftpProgressPayload>('sftp-progress', (e) => {
    if (e.payload.transfer_id === transferId) cb(e.payload.done);
  });
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
