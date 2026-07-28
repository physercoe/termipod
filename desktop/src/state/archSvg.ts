/// Standalone SVG export of the architecture schematic (archgraph plan W5,
/// decision D-6 "export is SVG-first"). The on-screen figure is React Flow —
/// HTML cards plus SVG edges — so a faithful export is *generated* from the same
/// pure layout (`archLayout.ts`) rather than scraped from the DOM. That keeps the
/// exported file self-contained (inlined styles, no external CSS, no web fonts)
/// and makes the export unit-testable: pure string in, pure string out.
///
/// Themes are explicit: the caller picks `light` or `dark` and every colour is
/// inlined from that palette, so the file renders identically anywhere — which is
/// the whole point of exporting (docs, slides, issues).
import type { ArchLayoutResult } from './archLayout.ts';
import { GEO, panelRows } from './archLayout.ts';
import type { AttnKind } from './archSchematic.ts';

export type SvgTheme = 'light' | 'dark';

interface Palette {
  bg: string;
  surface: string;
  raised: string;
  border: string;
  borderStrong: string;
  text: string;
  textMuted: string;
  accent: string;
  io: string;
  attention: string;
  linear: string;
  ffn: string;
  moe: string;
  norm: string;
}

// Mirrors the app's semantic tokens for each theme (the exported file cannot
// reference CSS variables — it must carry real colours).
const PALETTES: Record<SvgTheme, Palette> = {
  dark: {
    bg: '#0d1117', surface: '#161b22', raised: '#1c232c', border: '#30363d', borderStrong: '#4d5764',
    text: '#e6edf3', textMuted: '#8b949e', accent: '#2dd4bf',
    io: '#2dd4bf', attention: '#3B82F6', linear: '#06B6D4', ffn: '#22C55E', moe: '#A855F7', norm: '#EAB308',
  },
  light: {
    bg: '#ffffff', surface: '#ffffff', raised: '#f6f8fa', border: '#d0d7de', borderStrong: '#8c959f',
    text: '#1f2328', textMuted: '#636c76', accent: '#0f766e',
    io: '#0f766e', attention: '#1d4ed8', linear: '#0e7490', ffn: '#15803d', moe: '#7e22ce', norm: '#a16207',
  },
};

function bandFor(p: Palette, kind: string, attn?: AttnKind): string {
  if (attn === 'KDA' || attn === 'GatedDeltaNet') return p.linear;
  switch (kind) {
    case 'embed':
    case 'head':
      return p.io;
    case 'attention':
      return p.attention;
    case 'ffn':
      return p.ffn;
    case 'moe':
      return p.moe;
    default:
      return p.norm;
  }
}

/// XML-escape a text run (labels come from configs and i18n — never trust them
/// to be markup-safe).
export function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&apos;');
}

/// Clip a label to roughly the card width so the exported figure never overflows
/// its boxes (SVG has no ellipsis; measure by an average glyph advance).
function clip(s: string, widthPx: number, fontPx: number): string {
  const max = Math.max(4, Math.floor(widthPx / (fontPx * 0.58)));
  return s.length <= max ? s : `${s.slice(0, max - 1)}…`;
}

export interface ArchSvgOptions {
  theme?: SvgTheme;
  /// Title drawn top-left (the model family), omitted when blank.
  title?: string;
  /// Callout lines drawn under the figure (the spec's annotations).
  annotations?: string[];
  /// Legend entries: display label + the attention kind whose colour it keys.
  legend?: Array<{ label: string; attn: AttnKind }>;
  /// Resolves a panel's i18n key suffix (`titleKey` / item `key`) to display
  /// text. The panel spec carries keys, not prose (it is i18n-free by design), so
  /// an export without this would print the raw keys. Defaults to identity.
  labelFor?: (key: string) => string;
}

const PAD_OUT = 28; // outer margin around the whole figure

/// Render a laid-out schematic to a standalone SVG document.
export function archToSvg(laid: ArchLayoutResult, opts: ArchSvgOptions = {}): string {
  const p = PALETTES[opts.theme ?? 'dark'];
  const title = opts.title ?? '';
  const annotations = opts.annotations ?? [];
  const legend = opts.legend ?? [];
  const label = opts.labelFor ?? ((k: string) => k);

  // Extents across every element, so nothing is clipped.
  const xs: number[] = [];
  const ys: number[] = [];
  const track = (x: number, y: number, w: number, h: number): void => {
    xs.push(x, x + w);
    ys.push(y, y + h);
  };
  for (const c of laid.cards) track(c.x, c.y, c.w, c.h);
  for (const b of laid.boxes) track(b.x, b.y, b.w, b.h);
  for (const pn of laid.panels) track(pn.x, pn.y, pn.w, pn.h);
  if (laid.strip !== null) track(laid.strip.x, laid.strip.y, laid.strip.w, laid.strip.h);
  const minX = Math.min(...xs, 0);
  const minY = Math.min(...ys, 0);
  const maxX = Math.max(...xs, GEO.W);
  const maxY = Math.max(...ys, GEO.H);

  const titleH = title !== '' ? 34 : 0;
  const noteH = annotations.length > 0 ? annotations.length * 18 + 10 : 0;
  const legendH = legend.length > 0 ? 24 : 0;
  const originX = PAD_OUT - minX;
  const originY = PAD_OUT + titleH - minY;
  const width = maxX - minX + PAD_OUT * 2;
  const height = maxY - minY + PAD_OUT * 2 + titleH + noteH + legendH;

  const out: string[] = [];
  out.push(`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" font-family="system-ui, -apple-system, Segoe UI, sans-serif">`);
  out.push(`<rect width="${width}" height="${height}" fill="${p.bg}"/>`);
  if (title !== '') {
    out.push(`<text x="${PAD_OUT}" y="${PAD_OUT + 16}" fill="${p.text}" font-size="15" font-weight="600">${esc(title)}</text>`);
  }

  const gx = (v: number): number => v + originX;
  const gy = (v: number): number => v + originY;

  // Containers first (behind), sorted so the outer cycle box paints before its
  // inner run boxes — same z discipline as the renderer.
  for (const b of [...laid.boxes].sort((a, c) => a.z - c.z)) {
    out.push(
      `<rect x="${gx(b.x)}" y="${gy(b.y)}" width="${b.w}" height="${b.h}" rx="8" fill="none" stroke="${p.borderStrong}" stroke-width="1" stroke-dasharray="5 4"/>`,
    );
    out.push(
      `<rect x="${gx(b.x) + 10}" y="${gy(b.y) - 8}" width="${Math.min(b.label.length * 6.4 + 10, b.w - 20)}" height="15" fill="${p.bg}"/>`,
    );
    out.push(
      `<text x="${gx(b.x) + 14}" y="${gy(b.y) + 4}" fill="${p.textMuted}" font-size="11" font-weight="600">${esc(clip(b.label, b.w - 28, 11))}</text>`,
    );
  }

  // Main flow + residual/leader edges (straight orthogonal runs — an exported
  // figure wants clarity over the on-screen bezier).
  const cardById = new Map(laid.cards.map((c) => [c.id, c]));
  const panelById = new Map(laid.panels.map((pn) => [pn.id, pn]));
  for (const e of laid.edges) {
    const s = cardById.get(e.source);
    if (s === undefined) continue;
    if (e.kind === 'main') {
      const tgt = cardById.get(e.target);
      if (tgt === undefined) continue;
      const x = gx(s.x + s.w / 2);
      out.push(`<path d="M ${x} ${gy(s.y + s.h)} L ${x} ${gy(tgt.y)}" stroke="${p.borderStrong}" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>`);
    } else if (e.kind === 'residual') {
      const tgt = cardById.get(e.target);
      if (tgt === undefined) continue;
      const rx = gx(s.x + s.w) + 16;
      out.push(
        `<path d="M ${gx(s.x + s.w)} ${gy(s.y + s.h / 2)} L ${rx} ${gy(s.y + s.h / 2)} L ${rx} ${gy(tgt.y + tgt.h / 2)} L ${gx(tgt.x + tgt.w)} ${gy(tgt.y + tgt.h / 2)}" stroke="${p.accent}" stroke-width="1.2" stroke-dasharray="5 3" fill="none"/>`,
      );
    } else {
      const tgt = panelById.get(e.target);
      if (tgt === undefined) continue;
      out.push(
        `<path d="M ${gx(s.x + s.w)} ${gy(s.y + s.h / 2)} L ${gx(tgt.x)} ${gy(tgt.y + 20)}" stroke="${p.borderStrong}" stroke-width="1" stroke-dasharray="2 4" fill="none"/>`,
      );
    }
  }

  // The pattern strip.
  if (laid.strip !== null) {
    const st = laid.strip;
    const cellH = st.h / Math.max(st.cells.length, 1);
    out.push(`<rect x="${gx(st.x)}" y="${gy(st.y)}" width="${st.w}" height="${st.h}" fill="none" stroke="${p.border}" rx="3"/>`);
    st.cells.forEach((c, i) => {
      const fill = c.attn === 'KDA' || c.attn === 'GatedDeltaNet' ? p.linear : c.attn === 'sliding' ? `${p.attention}77` : p.attention;
      out.push(`<rect x="${gx(st.x)}" y="${gy(st.y) + i * cellH}" width="${st.w}" height="${Math.max(cellH - 0.5, 0.5)}" fill="${fill}"/>`);
      out.push(`<rect x="${gx(st.x)}" y="${gy(st.y) + i * cellH}" width="4" height="${Math.max(cellH - 0.5, 0.5)}" fill="${c.ffn === 'moe' ? p.moe : p.ffn}"/>`);
    });
  }

  // Component cards.
  for (const c of laid.cards) {
    const band = bandFor(p, c.node.kind, c.attn);
    out.push(`<rect x="${gx(c.x)}" y="${gy(c.y)}" width="${c.w}" height="${c.h}" rx="6" fill="${p.surface}" stroke="${p.border}"/>`);
    out.push(`<rect x="${gx(c.x)}" y="${gy(c.y)}" width="3" height="${c.h}" rx="1.5" fill="${band}"/>`);
    const hasSub = c.node.sub !== undefined && c.node.sub !== '';
    const labelY = gy(c.y) + (hasSub ? c.h / 2 - 2 : c.h / 2 + 4);
    out.push(`<text x="${gx(c.x) + 14}" y="${labelY}" fill="${p.text}" font-size="12.5" font-weight="600">${esc(clip(c.node.label, c.w - 26, 12.5))}</text>`);
    if (hasSub) {
      out.push(`<text x="${gx(c.x) + 14}" y="${labelY + 15}" fill="${p.textMuted}" font-size="11">${esc(clip(c.node.sub ?? '', c.w - 26, 11))}</text>`);
    }
  }

  // Zoom-in panels.
  for (const pn of laid.panels) {
    out.push(`<rect x="${gx(pn.x)}" y="${gy(pn.y)}" width="${pn.w}" height="${pn.h}" rx="6" fill="${p.surface}" stroke="${p.borderStrong}" stroke-dasharray="2 3"/>`);
    out.push(`<text x="${gx(pn.x) + 12}" y="${gy(pn.y) + 20}" fill="${p.textMuted}" font-size="11" font-weight="600">${esc(clip(label(pn.panel.titleKey), pn.w - 24, 11))}</text>`);
    let ry = gy(pn.y) + GEO.PANEL_HEAD + 8;
    const chipItems = pn.panel.items.filter((i) => i.shape === 'expert' || i.shape === 'more');
    for (const it of pn.panel.items) {
      if (it.shape === 'expert' || it.shape === 'more') continue;
      const band = it.shape === 'router' ? p.moe : it.shape === 'shared' ? p.ffn : p.attention;
      out.push(`<rect x="${gx(pn.x) + 10}" y="${ry}" width="${pn.w - 20}" height="24" rx="4" fill="${p.raised}" stroke="${p.border}"/>`);
      out.push(`<rect x="${gx(pn.x) + 10}" y="${ry}" width="3" height="24" fill="${band}"/>`);
      out.push(`<text x="${gx(pn.x) + 20}" y="${ry + 16}" fill="${p.text}" font-size="11">${esc(clip(label(it.key), (pn.w - 40) * 0.55, 11))}</text>`);
      if (it.value !== undefined) {
        out.push(`<text x="${gx(pn.x) + pn.w - 16}" y="${ry + 16}" fill="${p.textMuted}" font-size="10.5" text-anchor="end">${esc(it.value)}</text>`);
      }
      ry += GEO.PANEL_ROW;
    }
    if (chipItems.length > 0) {
      let cx = gx(pn.x) + 10;
      for (const it of chipItems) {
        const chipText = it.shape === 'more' ? (it.value ?? '') : label(it.key);
        const w = Math.min(chipText.length * 6 + 12, 64);
        out.push(`<rect x="${cx}" y="${ry}" width="${w}" height="22" rx="4" fill="none" stroke="${p.moe}" ${it.shape === 'more' ? 'stroke-dasharray="3 2"' : ''}/>`);
        out.push(`<text x="${cx + w / 2}" y="${ry + 15}" fill="${p.text}" font-size="10" text-anchor="middle">${esc(clip(chipText, w - 6, 10))}</text>`);
        cx += w + 6;
      }
    }
  }

  // Annotations + legend under the figure.
  let ty = gy(maxY) + 26;
  for (const a of annotations) {
    out.push(`<text x="${PAD_OUT}" y="${ty}" fill="${p.textMuted}" font-size="11.5">• ${esc(a)}</text>`);
    ty += 18;
  }
  if (legend.length > 0) {
    let lx = PAD_OUT;
    for (const l of legend) {
      const fill = l.attn === 'KDA' || l.attn === 'GatedDeltaNet' ? p.linear : l.attn === 'sliding' ? `${p.attention}77` : p.attention;
      out.push(`<rect x="${lx}" y="${ty - 8}" width="9" height="9" rx="2" fill="${fill}"/>`);
      out.push(`<text x="${lx + 14}" y="${ty}" fill="${p.textMuted}" font-size="11">${esc(l.label)}</text>`);
      lx += 14 + l.label.length * 6.2 + 18;
    }
  }

  out.push(
    `<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="${p.borderStrong}"/></marker></defs>`,
  );
  out.push('</svg>');
  return out.join('\n');
}

/// Rough pixel size of the document `archToSvg` would produce — used by the PNG
/// converter to size its canvas without parsing the SVG back.
export function archSvgSize(laid: ArchLayoutResult, opts: ArchSvgOptions = {}): { width: number; height: number } {
  const m = /width="(\d+(?:\.\d+)?)" height="(\d+(?:\.\d+)?)"/.exec(archToSvg(laid, opts));
  return m === null ? { width: 0, height: 0 } : { width: Number(m[1]), height: Number(m[2]) };
}

/// Panel height helper re-exported for callers sizing an export (keeps the SVG
/// and the on-screen panel geometry in lockstep).
export { panelRows };
