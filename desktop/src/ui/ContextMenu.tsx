import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';

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
  const menuRef = useRef<HTMLDivElement>(null);
  const t = useT();

  // On open, move focus to the first enabled item so the menu is keyboard-operable
  // (and a screen reader announces it). Items are real <button>s, so focus is real.
  useEffect(() => {
    if (st === null) return;
    const first = menuRef.current?.querySelector<HTMLButtonElement>('button:not([disabled])');
    first?.focus();
  }, [st]);

  // Roving keyboard navigation within the menu. Arrow/Home/End move focus between
  // enabled items; Escape closes; Enter/Space activates (via the button's native
  // click). Clipboard items lose the editor's selection under keyboard activation
  // (focus already left the editor) — the mouse path preserves it; keyboard is
  // best-effort, which is acceptable for the rare keyboard-driven context menu.
  const onMenuKey = useCallback((e: React.KeyboardEvent<HTMLDivElement>): void => {
    const menu = menuRef.current;
    if (menu === null) return;
    const items = Array.from(menu.querySelectorAll<HTMLButtonElement>('button:not([disabled])'));
    if (items.length === 0) return;
    const cur = items.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      items[(cur + 1 + items.length) % items.length]?.focus();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      items[(cur - 1 + items.length) % items.length]?.focus();
    } else if (e.key === 'Home') {
      e.preventDefault();
      items[0]?.focus();
    } else if (e.key === 'End') {
      e.preventDefault();
      items[items.length - 1]?.focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      setSt(null);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      (document.activeElement as HTMLButtonElement | null)?.click();
    }
  }, []);

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
        <div
          ref={menuRef}
          className="app-ctxmenu"
          style={{ left: st.x, top: st.y, width: MENU_W }}
          role="menu"
          aria-label={t('a11y.contextMenu')}
          onKeyDown={onMenuKey}
        >
          {st.items.map((it, i) => (
            <button
              key={i}
              role="menuitem"
              tabIndex={-1}
              className={it.danger === true ? 'danger' : undefined}
              disabled={it.disabled}
              // mousedown, not click: preventDefault keeps the editor's selection
              // and focus so clipboard execCommands act on it. (Mouse path — the
              // menu unmounts before any click fires, so onClick never doubles it.)
              onMouseDown={(e) => {
                e.preventDefault();
                if (it.disabled === true) return;
                setSt(null);
                it.onClick();
              }}
              // Keyboard path: onMenuKey synthesizes a .click() for Enter/Space,
              // which lands here (a real mouse click can't, per the note above).
              onClick={() => {
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
