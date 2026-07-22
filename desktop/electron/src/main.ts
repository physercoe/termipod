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
import { startKeychainMigration } from './ipc/keychain';
import { dispatch, isAllowed } from './ipc/dispatch';
import { isSafeExternal } from './ipc/platform';
import { disposeAllPtys } from './ipc/pty';
import { disposeAllSsh } from './ipc/ssh';
import { initEvents } from './events';

// The frontend build. In dev (`electron .` from desktop/electron) it resolves to
// desktop/dist; in a packaged app electron-builder ships it as an `extraResource`
// under `process.resourcesPath/dist` — a real on-disk dir, NOT inside the asar,
// because the app:// handler serves it via `net.fetch(file://)` and Chromium's
// file:// stack does not understand asar virtual paths (M3.1).
const DIST =
  process.env.TERMIPOD_DIST ??
  (app.isPackaged ? path.join(process.resourcesPath, 'dist') : path.join(__dirname, '..', '..', 'dist'));
// The app icon (assets/icon.png — the canonical source is assets/icon.{png,icns,ico},
// also wired into electron-builder.yml for the packaged bundle icons). Under
// `electron .` the dock/taskbar shows the Electron binary's default icon, not
// ours: the BrowserWindow `icon` covers Windows/Linux, and on macOS the dock
// icon must be set at runtime via app.dock.setIcon (the window option is
// ignored there). M3 packaging bakes the .icns/.ico into the bundle instead.
const ICON = path.join(__dirname, '..', 'assets', 'icon.png');

let mainWindow: BrowserWindow | null = null;

function createWindow(): void {
  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    title: 'TermiPod — Desktop Workbench',
    icon: ICON,
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
    // Packaged: point the lazy vault WASM loader at the `extraResource` copy of
    // `vault-wasm/pkg` (built by the vault-wasm CI job, shipped unpacked beside
    // dist). `??=` respects an explicit operator override. Set before any
    // `vault_*` invoke can fire (invokes only arrive after the renderer loads).
    if (app.isPackaged) {
      process.env.TERMIPOD_VAULT_WASM ??= path.join(
        process.resourcesPath,
        'vault-wasm',
        'pkg',
        'vault_wasm.js',
      );
    }
    initEvents();
    registerAppScheme(session.defaultSession, DIST);
    registerDrawioScheme(session.defaultSession);
    // Let the renderer's app:// origin reach the hub directly (renderer-direct
    // transport; plan §7 rows 1–2) — no Rust proxy.
    installHubCors(session.defaultSession);
    // One-time read of the Tauri-written secret document into the safeStorage
    // store (ADR-055 M1.3). Fire-and-forget: the window paints now, the first
    // secret access awaits it.
    startKeychainMigration();
    // macOS ignores the window icon; the dock icon is the app bundle's, which
    // under `electron .` is Electron's default — override it at runtime.
    if (process.platform === 'darwin') app.dock?.setIcon(ICON);
    createWindow();
    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  // Kill any live local shells / SSH connections so quitting never orphans a
  // child process or leaves a dangling socket.
  app.on('before-quit', () => {
    disposeAllPtys();
    disposeAllSsh();
  });

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
}
