import { useCallback, useEffect, useState } from 'react';

/// Shared reader zoom for the reflowable/flat attachment viewers (markdown, plain
/// text, html/mime). The PDF reader owns its own page-scale zoom (PdfCanvas) and
/// the EPUB reader its own font-percent zoom (EpubView); this covers the rest so
/// every viewer in the Read tab is zoomable. The factor is a plain CSS `zoom`
/// multiplier (1 = 100%), clamped and persisted per viewer key so a chosen level
/// survives reopening the document.

const MIN = 0.5;
const MAX = 3;
const STEP = 0.1;

export function clampZoom(n: number): number {
  return Math.min(MAX, Math.max(MIN, Math.round(n * 100) / 100));
}

export interface DocZoom {
  zoom: number;
  zoomIn: () => void;
  zoomOut: () => void;
  reset: () => void;
  setZoom: (n: number) => void;
}

export function useDocZoom(key: string, initial = 1): DocZoom {
  const lsKey = `termipod.read.zoom.${key}`;
  const [zoom, setZoomState] = useState(() => {
    const v = Number(localStorage.getItem(lsKey));
    return Number.isFinite(v) && v > 0 ? clampZoom(v) : initial;
  });
  // Persist on change rather than in each setter so the functional-updater paths
  // (wheel/keyboard) stay a single source of truth.
  useEffect(() => {
    try {
      localStorage.setItem(lsKey, String(zoom));
    } catch {
      /* ignore — storage may be full/unavailable */
    }
  }, [lsKey, zoom]);

  const zoomIn = useCallback(() => setZoomState((z) => clampZoom(z + STEP)), []);
  const zoomOut = useCallback(() => setZoomState((z) => clampZoom(z - STEP)), []);
  const reset = useCallback(() => setZoomState(1), []);
  const setZoom = useCallback((n: number) => setZoomState(clampZoom(n)), []);
  return { zoom, zoomIn, zoomOut, reset, setZoom };
}
