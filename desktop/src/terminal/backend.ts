import type { UnlistenFn } from '@tauri-apps/api/event';
import { onPtyData, onPtyExit, ptyClose, ptyResize, ptyWrite } from './pty';
import { onSshData, onSshExit, sshClose, sshResize, sshWrite } from '../ssh/tauri';

/// One I/O contract over the two session transports the dock multiplexes:
/// remote shells (`ssh` — russh, `ssh/tauri.ts`) and local shells (`local` —
/// portable-pty, `pty.ts`). `<Screen>` speaks only this interface, so it needn't
/// know which core command backs a given tab. Connect/open stays transport-
/// specific (SSH needs a credential form; local is a one-shot spawn) — only the
/// steady-state write/resize/close/stream is unified here.

export type TermKind = 'ssh' | 'local';

export function sessionWrite(kind: TermKind, id: string, data: string): Promise<void> {
  return kind === 'ssh' ? sshWrite(id, data) : ptyWrite(id, data);
}
export function sessionResize(kind: TermKind, id: string, cols: number, rows: number): Promise<void> {
  return kind === 'ssh' ? sshResize(id, cols, rows) : ptyResize(id, cols, rows);
}
export function sessionClose(kind: TermKind, id: string): Promise<void> {
  return kind === 'ssh' ? sshClose(id) : ptyClose(id);
}
export function onSessionData(
  kind: TermKind,
  id: string,
  cb: (bytes: Uint8Array) => void,
): Promise<UnlistenFn> {
  return kind === 'ssh' ? onSshData(id, cb) : onPtyData(id, cb);
}
export function onSessionExit(kind: TermKind, id: string, cb: () => void): Promise<UnlistenFn> {
  return kind === 'ssh' ? onSshExit(id, cb) : onPtyExit(id, cb);
}
