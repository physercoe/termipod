/// Native right-click menu (ADR-055 M4). WebView2 (the Tauri shell) supplied a
/// default context menu; Chromium/Electron supplies none, so plain text fields
/// and selections had no Cut/Copy/Paste at all. The renderer installs a global
/// `contextmenu` fallback (electron-only — see `src/nativeContextMenu.ts`) that
/// invokes this ONLY when no in-app custom menu already handled the event, and
/// only over an editable field, a live selection, or an image. Text items use
/// Electron ROLES, which act on the focused webContents' own selection/clipboard
/// — no execCommand plumbing, and Chromium owns cut/copy/paste/undo semantics.
///
/// Images take one of two copy paths, because Chromium's own "Copy image" only
/// targets raster elements (`<img>`/`<canvas>`), not inline SVG:
///   - a raster `<img>` (note attachments, EPUB figures) → `copyImageAt(x, y)`,
///     where the renderer passes webContents-space coordinates;
///   - a rendered figure `<svg>` (mermaid/vega/echarts) → the renderer rasterizes
///     it to a PNG `data:` URL and passes it as `imageData`, which we write to
///     the clipboard as a `nativeImage`.
import { clipboard, Menu, nativeImage, type MenuItemConstructorOptions } from 'electron';
import type { Handler } from './dispatch';

export const menuHandlers: Record<string, Handler> = {
  /// Pop a native context menu at the cursor. `editable` widens it to the full
  /// edit set; `hasSelection` gates cut/copy; an image target adds "Copy image".
  /// Paste is offered only when the OS clipboard actually holds text (checked
  /// here, in the main process, so the renderer needn't touch the async
  /// clipboard-read permission path).
  menu_show_context: (args, ctx): void => {
    if (ctx.win === null) return;
    const wc = ctx.win.webContents;
    const editable = args.editable === true;
    const hasSelection = args.hasSelection === true;
    const imageLabel = typeof args.imageLabel === 'string' && args.imageLabel !== '' ? args.imageLabel : 'Copy image';
    const template: MenuItemConstructorOptions[] = [];

    // Image "Copy image" leads the menu when present.
    if (typeof args.imageData === 'string' && args.imageData !== '') {
      const data = args.imageData;
      template.push({
        label: imageLabel,
        click: () => {
          const img = nativeImage.createFromDataURL(data);
          if (!img.isEmpty()) clipboard.writeImage(img);
        },
      });
    } else if (args.image === true && typeof args.x === 'number' && typeof args.y === 'number') {
      const x = Math.round(args.x);
      const y = Math.round(args.y);
      template.push({ label: imageLabel, click: () => wc.copyImageAt(x, y) });
    }

    if (editable) {
      if (template.length > 0) template.push({ type: 'separator' });
      template.push(
        { role: 'cut', enabled: hasSelection },
        { role: 'copy', enabled: hasSelection },
        { role: 'paste', enabled: clipboard.readText() !== '' },
        { type: 'separator' },
        { role: 'selectAll' },
      );
    } else if (hasSelection) {
      if (template.length > 0) template.push({ type: 'separator' });
      template.push({ role: 'copy' }, { type: 'separator' }, { role: 'selectAll' });
    }
    if (template.length === 0) return;
    Menu.buildFromTemplate(template).popup({ window: ctx.win });
  },
};
