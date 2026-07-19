import { useCallback, useState } from 'react';

/// App-wide right-click menu primitive. Tauri disables the native webview context
/// menu in release builds, so any surface that wants Copy/Paste or create actions
/// on right-click must render its own — this is the shared one.
///
/// `open(e, items)` positions the menu at the cursor; `node` renders it once in
/// the component tree. Menu actions fire on **mousedown with preventDefault** so
/// the editor keeps its selection/focus — otherwise clicking a menu button blurs
/// the editor and execCommand('copy'/'cut'/'insertText') would act on nothing.

export interface MenuItem {
  label: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
}

interface MenuState {
  x: number;
  y: number;
  items: MenuItem[];
}

const MENU_W = 200;
const ITEM_H = 30;

export function useContextMenu(): {
  open: (e: { clientX: number; clientY: number; preventDefault: () => void }, items: MenuItem[]) => void;
  node: JSX.Element | null;
} {
  const [st, setSt] = useState<MenuState | null>(null);

  const open = useCallback(
    (e: { clientX: number; clientY: number; preventDefault: () => void }, items: MenuItem[]): void => {
      e.preventDefault();
      if (items.length === 0) return;
      // Clamp to the viewport so a right-click near an edge doesn't push the menu
      // off-screen.
      const x = Math.min(e.clientX, window.innerWidth - MENU_W - 8);
      const y = Math.min(e.clientY, window.innerHeight - items.length * ITEM_H - 8);
      setSt({ x: Math.max(4, x), y: Math.max(4, y), items });
    },
    [],
  );

  const node =
    st === null ? null : (
      <>
        <div
          className="ctxmenu-backdrop"
          onMouseDown={() => setSt(null)}
          onContextMenu={(e) => {
            e.preventDefault();
            setSt(null);
          }}
        />
        <div className="app-ctxmenu" style={{ left: st.x, top: st.y, width: MENU_W }} role="menu">
          {st.items.map((it, i) => (
            <button
              key={i}
              role="menuitem"
              className={it.danger === true ? 'danger' : undefined}
              disabled={it.disabled}
              // mousedown, not click: preventDefault keeps the editor's selection
              // and focus so clipboard execCommands act on it.
              onMouseDown={(e) => {
                e.preventDefault();
                if (it.disabled === true) return;
                setSt(null);
                it.onClick();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      </>
    );

  return { open, node };
}

/// Standard Cut/Copy/Paste/Select-all items for a text-editing surface (Milkdown,
/// table cells, canvas card text). Copy/Cut/Select-all go through execCommand so
/// they use the editor's own selection + undo; paste reads the clipboard and
/// inserts via insertText (execCommand('paste') is blocked in the webview).
export function clipboardItems(t: (k: string) => string): MenuItem[] {
  const hasSelection = (window.getSelection()?.toString() ?? '') !== '';
  return [
    { label: t('ctx.cut'), disabled: !hasSelection, onClick: () => void document.execCommand('cut') },
    { label: t('ctx.copy'), disabled: !hasSelection, onClick: () => void document.execCommand('copy') },
    {
      label: t('ctx.paste'),
      onClick: () => {
        void navigator.clipboard
          .readText()
          .then((text) => {
            if (text !== '') document.execCommand('insertText', false, text);
          })
          .catch(() => {});
      },
    },
    { label: t('ctx.selectAll'), onClick: () => void document.execCommand('selectAll') },
  ];
}
