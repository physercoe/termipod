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
import { clipboard, Menu, nativeImage, shell, type MenuItemConstructorOptions } from 'electron';
import type { Handler } from './dispatch';
import { isSafeExternal } from './platform';

export const menuHandlers: Record<string, Handler> = {
  /// Pop a native context menu at the cursor. `editable` widens it to the full
  /// edit set; `hasSelection` gates cut/copy; `selectAll` keeps document-frame
  /// whitespace useful; an image target adds "Copy image".
  /// Paste is offered only when the OS clipboard actually holds text (checked
  /// here, in the main process, so the renderer needn't touch the async
  /// clipboard-read permission path).
  menu_show_context: (args, ctx): void => {
    if (ctx.win === null) return;
    const wc = ctx.win.webContents;
    const editable = args.editable === true;
    const hasSelection = args.hasSelection === true;
    const linkUrl = typeof args.linkUrl === 'string' && isSafeExternal(args.linkUrl) ? args.linkUrl : '';
    const openLinkLabel = typeof args.openLinkLabel === 'string' && args.openLinkLabel !== '' ? args.openLinkLabel : 'Open link in browser';
    const copyLinkLabel = typeof args.copyLinkLabel === 'string' && args.copyLinkLabel !== '' ? args.copyLinkLabel : 'Copy link address';
    const imageLabel = typeof args.imageLabel === 'string' && args.imageLabel !== '' ? args.imageLabel : 'Copy image';
    const template: MenuItemConstructorOptions[] = [];

    if (linkUrl !== '') {
      template.push(
        { label: openLinkLabel, click: () => void shell.openExternal(linkUrl) },
        { label: copyLinkLabel, click: () => clipboard.writeText(linkUrl) },
      );
    }

    // Image "Copy image" leads the menu when present.
    if (typeof args.imageData === 'string' && args.imageData !== '') {
      const data = args.imageData;
      if (template.length > 0) template.push({ type: 'separator' });
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
      if (template.length > 0) template.push({ type: 'separator' });
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
    } else if (hasSelection || args.selectAll === true) {
      if (template.length > 0) template.push({ type: 'separator' });
      if (hasSelection) template.push({ role: 'copy' }, { type: 'separator' });
      template.push({ role: 'selectAll' });
    }
    if (template.length === 0) return;
    Menu.buildFromTemplate(template).popup({ window: ctx.win });
  },
};
