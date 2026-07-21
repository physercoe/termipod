import { invoke, listen, type UnlistenFn } from '../bridge';
import { isShell } from '../platform';

/// Thin typed bridge to the Tauri Rust core's `russh` SSH transport (ADR-052,
/// personal direct-SSH). Only functional under the desktop (Tauri) build — the
/// plain-browser build has no native core, so `isShell()` gates the UI.

export { isShell };

export interface SshConnectReq {
  host: string;
  port: number;
  user: string;
  password?: string;
  private_key?: string;
  passphrase?: string;
  cols: number;
  rows: number;
  /** Frontend-minted attempt id — echoed on the core's `ssh-connect-progress`
   *  phase ticks so the form can match ticks to this attempt (#319). */
  connect_id?: string;
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
/** Open a second interactive shell on an existing session's connection — the
 *  split-duplicate path (#319): a fresh channel, no re-authentication. */
export function sshDuplicate(id: string, cols: number, rows: number): Promise<string> {
  return invoke<string>('ssh_duplicate', { id, cols, rows });
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
  code: number | null;
}

/** Subscribe to PTY output for one session (bytes → xterm). */
export function onSshData(id: string, cb: (bytes: Uint8Array) => void): Promise<UnlistenFn> {
  return listen<DataPayload>('ssh-data', (e) => {
    if (e.payload.id === id) cb(new Uint8Array(e.payload.bytes));
  });
}
/** Fired when the remote shell / channel closes; carries the exit code if sent. */
export function onSshExit(id: string, cb: (code: number | null) => void): Promise<UnlistenFn> {
  return listen<ExitPayload>('ssh-exit', (e) => {
    if (e.payload.id === id) cb(e.payload.code ?? null);
  });
}

/** The handshake stages `ssh_connect` reports (#319): TCP connect + key
 *  exchange, then authentication, then shell-channel open. */
export type SshConnectPhase = 'tcp' | 'auth' | 'channel';

interface ConnectProgressPayload {
  connect_id: string;
  phase: string;
}
/** Subscribe to phase ticks for one in-flight `ssh_connect` attempt (#319). */
export function onSshConnectPhase(connectId: string, cb: (phase: SshConnectPhase) => void): Promise<UnlistenFn> {
  return listen<ConnectProgressPayload>('ssh-connect-progress', (e) => {
    if (e.payload.connect_id === connectId) cb(e.payload.phase as SshConnectPhase);
  });
}
