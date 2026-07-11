import { useRef } from 'react';

/// A thin draggable divider between two panes. Reports horizontal drag deltas;
/// the parent owns the pane width (so it can clamp + persist). Pointer capture
/// keeps the drag alive even when the cursor outruns the 6px handle.
export function ResizeHandle({ onResize }: { onResize: (dx: number) => void }): JSX.Element {
  const last = useRef<number | null>(null);
  return (
    <div
      className="resize-handle"
      role="separator"
      aria-orientation="vertical"
      onPointerDown={(e) => {
        last.current = e.clientX;
        (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => {
        if (last.current === null) return;
        const dx = e.clientX - last.current;
        last.current = e.clientX;
        if (dx !== 0) onResize(dx);
      }}
      onPointerUp={(e) => {
        last.current = null;
        (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
      }}
    />
  );
}
