/// Native right-click menu fallback (ADR-055 M4). Chromium/Electron ships no
/// default context menu (WebView2 did), so plain text fields and selections had
/// no Cut/Copy/Paste. This installs ONE document-level `contextmenu` listener,
/// Electron-shell-only, in the BUBBLE phase — so it runs AFTER any component's
/// own `useContextMenu` handler, which calls `preventDefault` (see
/// `ui/ContextMenu.tsx`). When a surface already showed its own menu we defer to
/// it (`e.defaultPrevented`); otherwise, over an editable field or a live text
/// selection, we ask the main process to pop a native menu (`ipc/menu.ts`).
///
/// Inert under Tauri (which keeps its custom-menu-everywhere model — a native
/// menu there would double up) and in the browser build.
import { invoke, shellKind } from './bridge';

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
    const editable = editableHost(e.target);
    const selected = hasSelection(e.target);
    if (!editable && !selected) return; // nothing useful to offer
    e.preventDefault();
    void invoke('menu_show_context', { editable, hasSelection: selected });
  });
}
