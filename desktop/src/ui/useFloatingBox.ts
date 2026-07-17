import { useCallback, useState, type PointerEvent as ReactPointerEvent } from 'react';

/// A position + size for a floating dialog.
export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/// Resize edges we support. Only the right / bottom / bottom-right corner move —
/// the panel's top-left stays put (you reposition it by dragging the header),
/// which keeps the clamp math free of the west/north x↔w coupling that drifts
/// when a shrink hits the min-size wall.
export type ResizeEdge = 'e' | 's' | 'se';

function clampBox(b: Box, min: { w: number; h: number }): Box {
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const w = Math.max(min.w, Math.min(b.w, vw));
  const h = Math.max(min.h, Math.min(b.h, vh));
  const x = Math.max(0, Math.min(b.x, vw - w));
  const y = Math.max(0, Math.min(b.y, vh - h));
  return { x, y, w, h };
}

/// A localStorage-backed floating box with header-drag (move) and edge-drag
/// (resize) gestures. Both gestures track the pointer via listeners on `window`
/// for the gesture's lifetime rather than `setPointerCapture` — capture on a thin
/// strip is unreliable on WebView2 (see `ResizeHandle`), so window listeners keep
/// the drag alive wherever the cursor goes, on every platform.
export function useFloatingBox(
  key: string,
  makeDefault: () => Box,
  min: { w: number; h: number },
): {
  box: Box;
  startMove: (e: ReactPointerEvent) => void;
  startResize: (edge: ResizeEdge) => (e: ReactPointerEvent) => void;
} {
  const [box, setBox] = useState<Box>(() => {
    try {
      const raw = localStorage.getItem(key);
      if (raw !== null) {
        const p = JSON.parse(raw) as Partial<Box>;
        if (
          typeof p.x === 'number' &&
          typeof p.y === 'number' &&
          typeof p.w === 'number' &&
          typeof p.h === 'number'
        ) {
          return clampBox({ x: p.x, y: p.y, w: p.w, h: p.h }, min);
        }
      }
    } catch {
      /* fall through to default */
    }
    return clampBox(makeDefault(), min);
  });

  const persist = useCallback(
    (b: Box) => {
      try {
        localStorage.setItem(key, JSON.stringify(b));
      } catch {
        /* ignore */
      }
    },
    [key],
  );

  const startGesture = useCallback(
    (e: ReactPointerEvent, cursor: string, apply: (dx: number, dy: number, cur: Box) => Box) => {
      e.preventDefault();
      e.stopPropagation();
      let lastX = e.clientX;
      let lastY = e.clientY;
      const move = (ev: PointerEvent): void => {
        const dx = ev.clientX - lastX;
        const dy = ev.clientY - lastY;
        lastX = ev.clientX;
        lastY = ev.clientY;
        if (dx !== 0 || dy !== 0) setBox((cur) => clampBox(apply(dx, dy, cur), min));
      };
      const end = (): void => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', end);
        window.removeEventListener('pointercancel', end);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        setBox((cur) => {
          persist(cur);
          return cur;
        });
      };
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', end);
      window.addEventListener('pointercancel', end);
      document.body.style.cursor = cursor;
      document.body.style.userSelect = 'none';
    },
    [min, persist],
  );

  const startMove = useCallback(
    (e: ReactPointerEvent) => {
      startGesture(e, 'grabbing', (dx, dy, cur) => ({ ...cur, x: cur.x + dx, y: cur.y + dy }));
    },
    [startGesture],
  );

  const startResize = useCallback(
    (edge: ResizeEdge) => (e: ReactPointerEvent) => {
      const cursor = edge === 'se' ? 'nwse-resize' : edge === 'e' ? 'ew-resize' : 'ns-resize';
      startGesture(e, cursor, (dx, dy, cur) => ({
        ...cur,
        w: edge.includes('e') ? cur.w + dx : cur.w,
        h: edge.includes('s') ? cur.h + dy : cur.h,
      }));
    },
    [startGesture],
  );

  return { box, startMove, startResize };
}
