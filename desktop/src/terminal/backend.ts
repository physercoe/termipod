import type { UnlistenFn } from '../bridge';
import { onPtyData, onPtyExit, ptyClose, ptyResize, ptyStart, ptyWrite } from './pty';
import { onSshData, onSshExit, sshClose, sshResize, sshWrite } from '../ssh/native';

/// One I/O contract over the two session transports the dock multiplexes:
/// remote shells (`ssh` — russh, `ssh/native.ts`) and local shells (`local` —
/// portable-pty, `pty.ts`). `<Screen>` speaks only this interface, so it needn't
/// know which core command backs a given tab. Connect/open stays transport-
/// specific (SSH needs a credential form; local is a one-shot spawn) — only the
/// steady-state write/resize/close/stream is unified here.

export type TermKind = 'ssh' | 'local';

/// Begin streaming a session once the caller's data/exit listeners are attached.
/// Local shells gate their reader on this (`pty_start`) to avoid dropping the
/// first prompt; SSH starts reading at connect time, so this is a no-op there.
export function sessionStart(kind: TermKind, id: string): Promise<void> {
  return kind === 'local' ? ptyStart(id) : Promise.resolve();
}
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
export function onSessionExit(
  kind: TermKind,
  id: string,
  cb: (code: number | null) => void,
): Promise<UnlistenFn> {
  return kind === 'ssh' ? onSshExit(id, cb) : onPtyExit(id, cb);
}
