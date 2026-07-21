/// TermiPod Electron shell — main process (ADR-055 M1).
///
/// A single BrowserWindow that loads the same Vite `dist/` the Tauri shell
/// embeds, served from the `app://` secure origin. `contextIsolation` on,
/// `sandbox` on, `nodeIntegration` off — the renderer reaches native commands
/// only through the preload bridge, and only commands with a registered handler
/// (the allowlist). No frontend changes: injecting `__ELECTRON_BRIDGE__` flips
/// `src/bridge/` onto its Electron path.
///
/// M1.1 wires the shell + the platform-helper and migration command families;
/// the hub transport goes renderer-direct (plan §7 rows 1–2), keychain / files /
/// dialogs / draw.io land in later M1 slices.
import { app, BrowserWindow, ipcMain, session, shell } from 'electron';
import path from 'node:path';
import './schemes'; // registers privileged app:// + drawio:// before app ready
import { APP_ORIGIN, registerAppScheme } from './appscheme';
import { registerDrawioScheme } from './drawio';
import { installHubCors } from './hubcors';
import { dispatch, isAllowed } from './ipc/dispatch';
import { isSafeExternal } from './ipc/platform';
import { initEvents } from './events';

// The frontend build. In dev (`electron .` from desktop/electron) this resolves
// to desktop/dist; packaging (M3) will point it at the asar-embedded copy.
const DIST = process.env.TERMIPOD_DIST ?? path.join(__dirname, '..', '..', 'dist');

let mainWindow: BrowserWindow | null = null;

function createWindow(): void {
  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    title: 'TermiPod — Desktop Workbench',
    backgroundColor: '#0b0b10',
    show: false, // paint-free first frame; reveal on ready-to-show
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      spellcheck: false,
    },
  });
  mainWindow = win;
  // The app embeds arbitrary external sites (in-app browser iframe, with
  // `allow-popups`). A `window.open` from one would spawn a child window that
  // INHERITS this window's webPreferences — preload included — handing
  // `__ELECTRON_BRIDGE__` and the whole command allowlist to remote content.
  // Deny every popup; safe http(s) links go to the OS browser instead.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (isSafeExternal(url)) void shell.openExternal(url);
    return { action: 'deny' };
  });
  // And the top frame never leaves the app origin (the preload runs on
  // whatever document this window navigates to).
  win.webContents.on('will-navigate', (e, url) => {
    if (!url.startsWith(APP_ORIGIN)) e.preventDefault();
  });
  win.once('ready-to-show', () => win.show());
  win.on('closed', () => {
    if (mainWindow === win) mainWindow = null;
  });
  void win.loadURL(`${APP_ORIGIN}/index.html`);
}

// Single instance — a second launch focuses the existing window rather than
// opening a rival one (which would fight over the keychain / hub session).
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (mainWindow === null) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  // The one renderer→main call channel. The allowlist (handler map) is the
  // authority; unknown commands are rejected here, never in the untrusted
  // preload.
  ipcMain.handle('bridge:invoke', async (e, cmd: unknown, args: unknown) => {
    if (typeof cmd !== 'string' || !isAllowed(cmd)) {
      throw new Error(`bridge: command not allowed: ${String(cmd)}`);
    }
    const win = BrowserWindow.fromWebContents(e.sender) ?? mainWindow;
    return dispatch(cmd, args, { win, sender: e.sender });
  });

  void app.whenReady().then(() => {
    initEvents();
    registerAppScheme(session.defaultSession, DIST);
    registerDrawioScheme(session.defaultSession);
    // Let the renderer's app:// origin reach the hub directly (renderer-direct
    // transport; plan §7 rows 1–2) — no Rust proxy.
    installHubCors(session.defaultSession);
    createWindow();
    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
}
