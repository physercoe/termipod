/// Rasterize an inline SVG (a rendered figure — mermaid/graphviz/vega/echarts/…)
/// to a PNG `data:` URL via an offscreen canvas, so it can be copied to the
/// clipboard or saved. Standalone + dependency-free on purpose: the right-click
/// "Copy image" path (`nativeContextMenu.ts`) needs it in the MAIN bundle, and
/// pulling it from the lazily-chunked `FigureEditor` would drag that whole
/// surface into app boot.
///
/// A figure `<svg>` typically carries a `viewBox` but often no explicit
/// `width`/`height` (just a CSS `max-width`), so the intrinsic pixel size is read
/// from `width`/`height` → `viewBox` → a default, matching `FigureEditor`.

function svgPixelSize(svg: string): { w: number; h: number } {
  const vb = /viewBox\s*=\s*["']\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*["']/.exec(svg);
  const wAttr = /<svg[^>]*\bwidth\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const hAttr = /<svg[^>]*\bheight\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const w = wAttr !== null ? Number(wAttr[1]) : vb !== null ? Number(vb[3]) : 800;
  const h = hAttr !== null ? Number(hAttr[1]) : vb !== null ? Number(vb[4]) : 600;
  return { w, h };
}

/// Serialize a live `<svg>` element and rasterize it to a PNG `data:` URL at
/// `scale`× for crispness. Rejects if the SVG can't be decoded.
export async function svgElementToPngDataUrl(el: SVGElement, scale = 2): Promise<string> {
  const svg = new XMLSerializer().serializeToString(el);
  const { w, h } = svgPixelSize(svg);
  const url = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  const img = new Image();
  await new Promise<void>((resolve, reject) => {
    img.onload = () => resolve();
    img.onerror = () => reject(new Error('svg decode failed'));
    img.src = url;
  });
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(w * scale));
  canvas.height = Math.max(1, Math.round(h * scale));
  const ctx = canvas.getContext('2d');
  if (ctx === null) throw new Error('no 2d context');
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0, w, h);
  return canvas.toDataURL('image/png');
}
