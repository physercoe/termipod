import { BrowserWindow, shell } from 'electron';
import { kimiwebStart, kimiwebStop } from './kimiweb';
import type { Handler } from './ipc/dispatch';

/// Detached-window management for the assistant dock: pop the kimi-web SPA out
/// of the in-app dock into its own native window, and fold it back. Split from
/// kimiweb.ts so that module stays electron-free (its unit tests run under
/// plain `node --test`; this one is exercised through the esbuild bundle).
///
/// The window counts as one refcounted user of the shared `kimi web` server
/// (`kimiwebStart` on open, `kimiwebStop` on close), so detaching keeps the
/// server alive even if the dock's own panel unmounts, and closing the window
/// releases it like any other consumer. Content is the loopback embed URL
/// only; the window gets no preload/bridge (untrusted-content posture, same as
/// `open_browser_window`), popups route to the OS browser.

let win: BrowserWindow | null = null;

function detached(): boolean {
  return win !== null && !win.isDestroyed();
}

async function kimiwebDetach(): Promise<{ url: string }> {
  const url = await kimiwebStart(); // the window's refcount hold (released in 'closed')
  if (detached()) {
    // Already popped out — focus it and release the extra hold we just took.
    win!.focus();
    await kimiwebStop();
    return { url };
  }
  const w = new BrowserWindow({
    width: 1100,
    height: 800,
    title: 'TermiPod Assistant',
    webPreferences: { contextIsolation: true, sandbox: true, nodeIntegration: false },
  });
  win = w;
  w.setMenuBarVisibility(false);
  // Links out of the SPA go to the OS browser; the window itself never leaves
  // the loopback server it was opened on.
  w.webContents.setWindowOpenHandler(({ url: popup }) => {
    if (popup.startsWith('https://') || popup.startsWith('http://')) void shell.openExternal(popup);
    return { action: 'deny' };
  });
  w.webContents.on('will-navigate', (e, target) => {
    if (!target.startsWith('http://127.0.0.1:') && !target.startsWith('http://localhost:')) e.preventDefault();
  });
  w.on('closed', () => {
    win = null;
    void kimiwebStop(); // release the window's hold; server dies if it was the last
  });
  await w.loadURL(url);
  return { url };
}

/// Fold the detached window back into the dock: just close it — its 'closed'
/// handler releases the server hold; the dock remounts its own panel.
function kimiwebAttach(): void {
  if (detached()) win!.close();
}

/// Close the detached window on quit (the server itself dies via disposeKimiWeb).
export function disposeKimiWebWin(): void {
  if (detached()) win!.destroy();
  win = null;
}

export const kimiwebWinHandlers: Record<string, Handler> = {
  kimiweb_detach: async (): Promise<{ url: string }> => kimiwebDetach(),
  kimiweb_attach: async (): Promise<void> => {
    kimiwebAttach();
  },
  kimiweb_win_status: async (): Promise<{ detached: boolean }> => ({ detached: detached() }),
};
