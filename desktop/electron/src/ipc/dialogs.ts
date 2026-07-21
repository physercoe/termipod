/// Native dialog helpers (ADR-055 M1.4). Electron's `showOpenDialog` /
/// `showSaveDialog` have separate with-window and without-window overloads;
/// these pick the right one so a handler can pass a possibly-null parent window
/// uniformly (a null parent yields an app-modal dialog, matching Tauri).
import { dialog, type BrowserWindow, type OpenDialogOptions, type OpenDialogReturnValue, type SaveDialogOptions, type SaveDialogReturnValue } from 'electron';

export function openDialog(win: BrowserWindow | null, opts: OpenDialogOptions): Promise<OpenDialogReturnValue> {
  return win !== null ? dialog.showOpenDialog(win, opts) : dialog.showOpenDialog(opts);
}

export function saveDialog(win: BrowserWindow | null, opts: SaveDialogOptions): Promise<SaveDialogReturnValue> {
  return win !== null ? dialog.showSaveDialog(win, opts) : dialog.showSaveDialog(opts);
}
