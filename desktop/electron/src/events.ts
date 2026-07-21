/// Main→renderer event plumbing (ADR-055 M1). The counterpart to the preload's
/// `bridge:event` fan-out. M1.1 emits nothing (the active surfaces need no
/// native events — SSE is renderer-direct), but the contract is set here so the
/// M2 natives (pty/ssh/voice/sync) wire up without touching the preload.
///
/// It also tracks subscriptions so a producer can honour a subscribe-gate — the
/// `pty_start` two-step that prevents losing the first prompt emits only once a
/// renderer is listening (`hasSubscriber`).
import { ipcMain, type WebContents } from 'electron';

const subscribed = new Map<WebContents, Set<string>>();

export function initEvents(): void {
  ipcMain.on('bridge:subscribe', (e, event: unknown) => {
    if (typeof event !== 'string') return;
    let set = subscribed.get(e.sender);
    if (set === undefined) {
      set = new Set();
      subscribed.set(e.sender, set);
      e.sender.once('destroyed', () => subscribed.delete(e.sender));
    }
    set.add(event);
  });
  ipcMain.on('bridge:unsubscribe', (e, event: unknown) => {
    if (typeof event !== 'string') return;
    subscribed.get(e.sender)?.delete(event);
  });
}

/// Whether `sender` has a live listener for `event` (producer subscribe-gate).
export function hasSubscriber(sender: WebContents, event: string): boolean {
  return subscribed.get(sender)?.has(event) ?? false;
}

/// Push an event to a renderer in the preload's envelope shape.
export function emit(sender: WebContents, event: string, payload: unknown): void {
  if (sender.isDestroyed()) return;
  sender.send('bridge:event', { event, payload });
}
