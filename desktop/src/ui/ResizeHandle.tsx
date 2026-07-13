import { useRef } from 'react';

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
