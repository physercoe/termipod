/// Command dispatch + allowlist (ADR-055 M1) — the successor of Tauri's
/// `capabilities/default.json`.
///
/// Every bridge `invoke(cmd, args)` from the renderer arrives here. A command is
/// callable iff it has a registered handler; there is no separate allowlist to
/// drift out of sync — the handler map IS the allowlist. Unknown commands are
/// rejected in the main process (the authority), so a compromised renderer
/// can't reach anything not wired here.
///
/// Families land slice by slice (plan §3 order): platform helpers + migration
/// (M1.1) → keychain → docs/workspace/localfs/storage/attachments + dialogs →
/// draw.io. The hub transport is deliberately NOT here — it goes renderer-direct
/// (plan §7 rows 1–2).
import type { BrowserWindow, WebContents } from 'electron';
import { platformHandlers } from './platform';
import { migrationHandlers } from './migration';
import { docfileHandlers } from './docfile';
import { localfsHandlers } from './localfs';
import { workspaceHandlers } from './workspace';
import { storageHandlers } from './storage';
import { keychainHandlers } from './keychain';
import { ptyHandlers } from './pty';
import { sshHandlers } from './ssh';
import { sshKeyHandlers } from './ssh_keys';
import { voiceHandlers } from './voice';
import { scriptHandlers } from './script';
import { folderWebdavHandlers } from './sync/webdav';
import { webdavZoteroHandlers } from './sync/webdav_zotero';
import { drawioHandlers } from '../drawio';

export interface Ctx {
  win: BrowserWindow | null;
  sender: WebContents;
}

export type Handler = (args: Record<string, unknown>, ctx: Ctx) => Promise<unknown> | unknown;

const handlers: Record<string, Handler> = {
  ...platformHandlers,
  ...migrationHandlers,
  ...docfileHandlers,
  ...localfsHandlers,
  ...workspaceHandlers,
  ...storageHandlers,
  ...keychainHandlers,
  ...ptyHandlers,
  ...sshHandlers,
  ...sshKeyHandlers,
  ...voiceHandlers,
  ...scriptHandlers,
  ...folderWebdavHandlers,
  ...webdavZoteroHandlers,
  ...drawioHandlers,
};

export function isAllowed(cmd: string): boolean {
  return Object.prototype.hasOwnProperty.call(handlers, cmd);
}

export async function dispatch(cmd: string, args: unknown, ctx: Ctx): Promise<unknown> {
  const handler = handlers[cmd];
  if (handler === undefined) throw new Error(`bridge: no handler for command '${cmd}'`);
  const a = args !== null && typeof args === 'object' ? (args as Record<string, unknown>) : {};
  return handler(a, ctx);
}
