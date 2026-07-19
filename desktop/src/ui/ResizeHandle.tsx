import { useCallback, useRef, useState } from 'react';
import { useT } from '../i18n';

/// A localStorage-backed, clamped pane width + its `onResize(dx)` handler, for a
/// left panel whose ResizeHandle sits on its RIGHT edge (dragging right widens it
/// → `sign` is +1; pass -1 for a right-docked panel). Mirrors ReadSurface's
/// loadWidth/saveWidth/clamp so the reader outlines resize + persist like the
/// library rails do.
export function usePanelWidth(
  key: string,
  fallback: number,
  min: number,
  max: number,
  sign = 1,
): [number, (dx: number) => void] {
  const [w, setW] = useState(() => {
    const v = Number(localStorage.getItem(key));
    return Number.isFinite(v) && v > 0 ? Math.min(max, Math.max(min, v)) : fallback;
  });
  // Persist is debounced: a drag fires onResize on every pointermove (dozens/s),
  // and a synchronous localStorage.setItem each time is a main-thread stall
  // (#311). The width state still updates live for smooth dragging; only the
  // write to storage is deferred to ~250ms after the gesture settles.
  const persistTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const onResize = useCallback(
    (dx: number) => {
      setW((cur) => {
        const n = Math.min(max, Math.max(min, cur + sign * dx));
        if (persistTimer.current !== undefined) clearTimeout(persistTimer.current);
        persistTimer.current = setTimeout(() => {
          try {
            localStorage.setItem(key, String(n));
          } catch {
            /* ignore */
          }
        }, 250);
        return n;
      });
    },
    [key, min, max, sign],
  );
  return [w, onResize];
}

/// A thin draggable divider between two panes. Reports horizontal drag deltas;
/// the parent owns the pane width (so it can clamp + persist).
///
/// The drag is tracked via listeners attached to `window` for the lifetime of the
/// gesture — NOT `setPointerCapture` on the handle. On WebView2 (Windows) pointer
/// capture on a 6px element is unreliable: when it doesn't take, `pointermove`
/// only fires while the cursor is still over the handle, so the pane can't be
/// dragged past the strip's own width and reads as "fixed / cannot adjust"
/// (director report). Window listeners keep the drag alive no matter where the
/// cursor goes, on every platform.
export function ResizeHandle({ onResize }: { onResize: (dx: number) => void }): JSX.Element {
  const t = useT();
  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  return (
    <div
      className="resize-handle"
      role="separator"
      aria-orientation="vertical"
      // Keyboard-operable (#316): focus the divider and nudge with arrows (Shift =
      // larger step). aria-valuenow needs the live width, which the parent owns —
      // the arrow deltas go through the same onResize as the drag.
      tabIndex={0}
      aria-label={t('a11y.resizePanel')}
      onKeyDown={(e) => {
        const step = e.shiftKey ? 96 : 24;
        if (e.key === 'ArrowLeft') {
          e.preventDefault();
          onResizeRef.current(-step);
        } else if (e.key === 'ArrowRight') {
          e.preventDefault();
          onResizeRef.current(step);
        }
      }}
      onPointerDown={(e) => {
        e.preventDefault();
        let lastX = e.clientX;
        const move = (ev: PointerEvent): void => {
          const dx = ev.clientX - lastX;
          lastX = ev.clientX;
          if (dx !== 0) onResizeRef.current(dx);
        };
        const end = (): void => {
          window.removeEventListener('pointermove', move);
          window.removeEventListener('pointerup', end);
          window.removeEventListener('pointercancel', end);
          document.body.style.cursor = '';
          document.body.style.userSelect = '';
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', end);
        window.addEventListener('pointercancel', end);
        // Hold the resize cursor + suppress text selection for the whole drag.
        document.body.style.cursor = 'col-resize';
        document.body.style.userSelect = 'none';
      }}
    />
  );
}

/// A horizontal divider between two stacked panes — reports VERTICAL drag deltas
/// (dy, positive = dragged down). The vertical twin of ResizeHandle; the parent
/// owns the pane height so it can clamp + persist. Same window-listener gesture
/// model (WebView2 pointer-capture is unreliable on a thin strip).
export function VResizeHandle({ onResize }: { onResize: (dy: number) => void }): JSX.Element {
  const t = useT();
  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  return (
    <div
      className="resize-handle-v"
      role="separator"
      aria-orientation="horizontal"
      tabIndex={0}
      aria-label={t('a11y.resizePanel')}
      onKeyDown={(e) => {
        const step = e.shiftKey ? 96 : 24;
        if (e.key === 'ArrowUp') {
          e.preventDefault();
          onResizeRef.current(-step);
        } else if (e.key === 'ArrowDown') {
          e.preventDefault();
          onResizeRef.current(step);
        }
      }}
      onPointerDown={(e) => {
        e.preventDefault();
        let lastY = e.clientY;
        const move = (ev: PointerEvent): void => {
          const dy = ev.clientY - lastY;
          lastY = ev.clientY;
          if (dy !== 0) onResizeRef.current(dy);
        };
        const end = (): void => {
          window.removeEventListener('pointermove', move);
          window.removeEventListener('pointerup', end);
          window.removeEventListener('pointercancel', end);
          document.body.style.cursor = '';
          document.body.style.userSelect = '';
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', end);
        window.addEventListener('pointercancel', end);
        document.body.style.cursor = 'row-resize';
        document.body.style.userSelect = 'none';
      }}
    />
  );
}
