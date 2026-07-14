import { useCallback, useRef, useState } from 'react';

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
  const onResize = useCallback(
    (dx: number) => {
      setW((cur) => {
        const n = Math.min(max, Math.max(min, cur + sign * dx));
        try {
          localStorage.setItem(key, String(n));
        } catch {
          /* ignore */
        }
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
  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  return (
    <div
      className="resize-handle"
      role="separator"
      aria-orientation="vertical"
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
