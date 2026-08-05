/// `author_render`'s drawing half — everything `renderDoc.ts` deliberately does
/// not contain (coworking W2; ADR-064).
///
/// Split for the reason B5 split its dry run: this file reaches a DOM, a canvas
/// and two lazily-loaded vendor libraries, none of which exist under
/// `node --test`. The decisions — which kinds render, which refusal each failure
/// gets, the context cap — live next door where a test can drive them.
///
/// **One renderer per kind, and it is the kind's OWN.** Every path below is the
/// same code the user's export button runs: `renderFigure` for figures, the
/// Excalidraw exporters for scenes, draw.io's embed `export` for diagrams. A
/// second renderer would agree with the user's screen right up until it did not,
/// and an agent that is shown a picture nobody else can see is worse than one
/// shown nothing.
///
/// **One rasterizer.** SVG is what every path produces, and PNG is always that
/// SVG through `svgToPngBase64` — except Excalidraw, which has a PNG exporter of
/// its own that handles embedded images and fonts the naive canvas path drops.
/// The figure pane imports the rasterizer from here rather than keeping its own
/// copy, so "the PNG an agent gets" and "the PNG the export button writes" are
/// the same function.
import { liveRender } from './liveRender.ts';
import { parseExcalidrawScene } from './excalidrawScene.ts';
import { renderFigure } from './figures.ts';
import { mimeForFormat, type RenderedImage, type RenderFormat } from './renderDoc.ts';
import type { Doc } from './documents.ts';

/// Base64 of a UTF-8 string, chunked.
///
/// `btoa` takes latin-1, so a diagram label outside it throws
/// `InvalidCharacterError`; and `String.fromCharCode(...bytes)` on a large SVG
/// overflows the argument stack. Both failures happen only on real documents —
/// the first on any non-English label, the second on any big drawing — which is
/// exactly why this is not a one-liner.
export function utf8ToBase64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let binary = '';
  const CHUNK = 0x8000;
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK));
  }
  return btoa(binary);
}

async function blobToBase64(blob: Blob): Promise<string> {
  const buf = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  const CHUNK = 0x8000;
  for (let i = 0; i < buf.length; i += CHUNK) {
    binary += String.fromCharCode(...buf.subarray(i, i + CHUNK));
  }
  return btoa(binary);
}

/// The intended pixel size of a rendered SVG — its explicit `width`/`height`,
/// else the `viewBox`, else a default — so the raster canvas can be sized.
///
/// (It formerly also injected explicit width/height into the `<svg>` to work
/// around WebKit reporting `naturalWidth === 0` for viewBox-only SVGs; the
/// Electron shell's Chromium rasterizes those via `drawImage(img, 0, 0, w, h)`
/// with no injection — pinned in electron/e2e/app.spec.ts.)
export function svgSize(svg: string): { w: number; h: number } {
  const vb = /viewBox\s*=\s*["']\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*["']/.exec(svg);
  const wAttr = /<svg[^>]*\bwidth\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const hAttr = /<svg[^>]*\bheight\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/.exec(svg);
  const w = wAttr !== null ? Number(wAttr[1]) : vb !== null ? Number(vb[3]) : 800;
  const h = hAttr !== null ? Number(hAttr[1]) : vb !== null ? Number(vb[4]) : 600;
  return { w, h };
}

/// Rasterize an SVG string to a base64 PNG at `scale`× via an offscreen canvas.
export async function svgToPngBase64(svg: string, scale = 2): Promise<string> {
  const { w, h } = svgSize(svg);
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
  return canvas.toDataURL('image/png').split(',')[1] ?? '';
}

/// An agent's render is 1× where the export button is 2×.
///
/// The button writes a file a person will zoom into; this lands in a context
/// window, and a retina-scaled PNG is four times the tokens for detail nothing
/// downstream reads. SVG, which is what an agent should ask for anyway, is
/// unaffected.
const AGENT_PNG_SCALE = 1;

async function figureSvg(doc: Doc): Promise<string> {
  if (doc.spec === undefined) {
    throw new Error('this figure document has no renderer set (its `spec` is empty) — set one in the Author toolbar');
  }
  return renderFigure(doc.spec, doc.body);
}

/// Excalidraw renders from the body, not from the mounted component, so a scene
/// the user does not have open renders exactly as one they do. The vendor chunk
/// is megabytes, so it is imported here and not at module load — this file is
/// reachable from the bridge host, which runs at boot.
async function excalidrawImage(doc: Doc, format: RenderFormat): Promise<RenderedImage> {
  const scene = parseExcalidrawScene(doc.body);
  if (scene === null) {
    throw new Error('this document is not a readable Excalidraw scene — it opens read-only in the editor too');
  }
  const { exportToBlob, exportToSvg } = await import('@excalidraw/excalidraw');
  const args = {
    elements: scene.elements as Parameters<typeof exportToSvg>[0]['elements'],
    appState: scene.appState as Parameters<typeof exportToSvg>[0]['appState'],
    files: (scene.files ?? null) as Parameters<typeof exportToSvg>[0]['files'],
    exportPadding: 12,
  };
  if (format === 'png') {
    // The vendor's own rasterizer rather than ours: it resolves the scene's
    // embedded image files and fonts, which an SVG round trip through a canvas
    // silently drops.
    const blob = await exportToBlob({ ...args, mimeType: 'image/png', quality: 1 });
    return { base64: await blobToBase64(blob), mimeType: mimeForFormat('png') };
  }
  const svg = await exportToSvg(args);
  return { base64: utf8ToBase64(new XMLSerializer().serializeToString(svg)), mimeType: mimeForFormat('svg') };
}

/// Render one document. Throws with the sentence the agent is shown — every
/// caller of this is `renderRequest`, which wraps a throw into `RENDER_FAILED`.
export async function renderDocument(doc: Doc, format: RenderFormat): Promise<RenderedImage> {
  if (doc.kind === 'excalidraw') return excalidrawImage(doc, format);

  let svg: string;
  if (doc.kind === 'figure') {
    svg = await figureSvg(doc);
  } else if (doc.kind === 'diagram') {
    // Only draw.io can draw an mxGraph model, and it lives in the iframe. A null
    // here means the document is not open — `renderRequest` has already refused
    // that case, so reaching it means the editor unmounted mid-call.
    const out = await liveRender(doc.id);
    if (out === null) throw new Error('the diagram editor closed before it could export');
    svg = out;
  } else {
    // Unreachable: `renderPathFor` refuses these kinds before the host is asked.
    // Kept as a throw rather than a cast so a future kind added to one list and
    // not the other fails loudly instead of rendering something arbitrary.
    throw new Error(`no renderer for a ${doc.kind} document`);
  }

  if (format === 'png') {
    return { base64: await svgToPngBase64(svg, AGENT_PNG_SCALE), mimeType: mimeForFormat('png') };
  }
  return { base64: utf8ToBase64(svg), mimeType: mimeForFormat('svg') };
}
