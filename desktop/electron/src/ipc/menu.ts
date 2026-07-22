/// Native right-click menu (ADR-055 M4). WebView2 (the Tauri shell) supplied a
/// default context menu; Chromium/Electron supplies none, so plain text fields
/// and selections had no Cut/Copy/Paste at all. The renderer installs a global
/// `contextmenu` fallback (electron-only — see `src/nativeContextMenu.ts`) that
/// invokes this ONLY when no in-app custom menu already handled the event, and
/// only over an editable field or a live selection. Menu items use Electron
/// ROLES, which act on the focused webContents' own selection/clipboard — no
/// execCommand plumbing, and Chromium owns cut/copy/paste/undo semantics.
import { clipboard, Menu, type MenuItemConstructorOptions } from 'electron';
import type { Handler } from './dispatch';

export const menuHandlers: Record<string, Handler> = {
  /// Pop a native context menu at the cursor. `editable` widens it to the full
  /// edit set; `hasSelection` gates cut/copy. Paste is offered only when the OS
  /// clipboard actually holds text (checked here, in the main process, so the
  /// renderer needn't touch the async clipboard-read permission path).
  menu_show_context: (args, ctx): void => {
    if (ctx.win === null) return;
    const editable = args.editable === true;
    const hasSelection = args.hasSelection === true;
    const template: MenuItemConstructorOptions[] = [];
    if (editable) {
      template.push(
        { role: 'cut', enabled: hasSelection },
        { role: 'copy', enabled: hasSelection },
        { role: 'paste', enabled: clipboard.readText() !== '' },
        { type: 'separator' },
        { role: 'selectAll' },
      );
    } else if (hasSelection) {
      template.push({ role: 'copy' }, { type: 'separator' }, { role: 'selectAll' });
    }
    if (template.length === 0) return;
    Menu.buildFromTemplate(template).popup({ window: ctx.win });
  },
};
