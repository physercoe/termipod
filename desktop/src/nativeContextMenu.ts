/// Native right-click menu fallback (ADR-055 M4). Chromium/Electron ships no
/// default context menu (WebView2 did), so plain text fields, selections, images
/// and rendered figures had no Copy at all. This installs ONE document-level
/// `contextmenu` listener, Electron-shell-only, in the BUBBLE phase — so it runs
/// AFTER any component's own `useContextMenu` handler, which calls
/// `preventDefault` (see `ui/ContextMenu.tsx`). When a surface already showed its
/// own menu we defer to it (`e.defaultPrevented`); otherwise we ask the main
/// process to pop a native menu (`ipc/menu.ts`) for whatever is under the cursor:
///   - a rendered figure `<svg>` (`.figure-preview`/`.md-figure`: mermaid, vega,
///     echarts, …) → rasterize it to PNG here and pass it for a "Copy image";
///   - a raster `<img>` (note attachments) → "Copy image" via `copyImageAt`;
///   - an editable field or a live text selection → Cut/Copy/Paste.
/// (The EPUB reader renders inside an iframe whose `contextmenu` never reaches
/// this window listener, so it forwards its own — see `ui/EpubView.tsx`.)
///
/// Inert under Tauri (which keeps its custom-menu-everywhere model — a native
/// menu there would double up) and in the browser build.
import { invoke, shellKind } from './bridge';
import { tStatic } from './i18n';
import { svgElementToPngDataUrl } from './ui/rasterizeSvg';

function editableHost(el: EventTarget | null): boolean {
  const node = el as Element | null;
  return node?.closest?.('input, textarea, [contenteditable=""], [contenteditable="true"]') != null;
}

function hasSelection(el: EventTarget | null): boolean {
  // An <input>/<textarea>'s selection lives on the element, not in the DOM
  // Selection object, so check it directly; fall back to the document selection
  // for everything else (contenteditable, plain text).
  const field = (el as Element | null)?.closest?.('input, textarea') as
    | HTMLInputElement
    | HTMLTextAreaElement
    | null;
  if (field != null) return field.selectionStart != null && field.selectionStart !== field.selectionEnd;
  return (window.getSelection()?.toString() ?? '') !== '';
}

export function installNativeContextMenu(): void {
  if (shellKind() !== 'electron') return;
  window.addEventListener('contextmenu', (e) => {
    if (e.defaultPrevented) return; // a component's own menu already handled it
    const el = e.target as Element | null;

    // A rendered figure: rasterize its SVG and offer "Copy image". Chromium's own
    // "Copy image" can't target inline SVG, so we produce the PNG here.
    const figSvg = el?.closest?.('.figure-preview, .md-figure')?.querySelector?.('svg') as SVGElement | null;
    if (figSvg != null) {
      e.preventDefault();
      void svgElementToPngDataUrl(figSvg)
        .then((imageData) => invoke('menu_show_context', { imageData, imageLabel: tStatic('common.copyImage') }))
        .catch(() => undefined);
      return;
    }

    // A raster image (e.g. a note attachment): "Copy image" at the cursor. In the
    // main frame, viewport coordinates are webContents coordinates.
    if (el?.closest?.('img') != null) {
      e.preventDefault();
      void invoke('menu_show_context', {
        image: true,
        x: e.clientX,
        y: e.clientY,
        imageLabel: tStatic('common.copyImage'),
      });
      return;
    }

    const editable = editableHost(el);
    const selected = hasSelection(el);
    if (!editable && !selected) return; // nothing useful to offer
    e.preventDefault();
    void invoke('menu_show_context', { editable, hasSelection: selected });
  });
}
