import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import * as pdfjsLib from 'pdfjs-dist';
import { TextLayer } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { Icon } from './Icon';
import { useOpenLink } from './OpenLinkContext';
import { ResizeHandle } from './ResizeHandle';
import { useAnnotations, ANNOTATION_COLORS } from '../state/annotations';
import type { Annotation } from '../state/annotations';
import { TabStrip } from './TabStrip';

function escapeHtml(s: string): string {
  return s.replace(/[&<>]/g, (c) => (c === '&' ? '&amp;' : c === '<' ? '&lt;' : '&gt;'));
}

// Track the live devicePixelRatio. A `(resolution: Ndppx)` media query fires its
// change event when the ratio shifts (window moved to a monitor of different
// density); we then re-subscribe keyed to the new ratio.
function useDevicePixelRatio(): number {
  const [dpr, setDpr] = useState(() => window.devicePixelRatio || 1);
  useEffect(() => {
    const mql = window.matchMedia(`(resolution: ${window.devicePixelRatio || 1}dppx)`);
    const update = (): void => setDpr(window.devicePixelRatio || 1);
    mql.addEventListener('change', update);
    return () => mql.removeEventListener('change', update);
  }, [dpr]);
  return dpr;
}

// Clamp a fixed/absolute overlay's [x, y] so its measured box stays within the
// viewport — used by the annotation editor and the PDF context menu, which near
// a page/screen edge would otherwise render partly off-screen.
function clampToViewport(x: number, y: number, w: number, h: number, pad = 8): { x: number; y: number } {
  const maxX = window.innerWidth - w - pad;
  const maxY = window.innerHeight - h - pad;
  return { x: Math.max(pad, Math.min(x, maxX)), y: Math.max(pad, Math.min(y, maxY)) };
}

// The active tool, or null for read/select mode. highlight/underline are
// selection-driven; note is a click; image/ink are pointer drags; hand pans the
// scroll view — a view tool, it creates no annotation (#321).
type Tool = null | 'highlight' | 'underline' | 'note' | 'image' | 'ink' | 'hand';

// Single-key tool shortcuts (#316) — handled by the window keydown effect in
// the viewer. V (and Escape) return to select; the letters arm a tool. An
// unmapped key reads as undefined, distinct from V's explicit null.
const TOOL_SHORTCUTS: Partial<Record<string, Tool>> = {
  v: null,
  h: 'highlight',
  u: 'underline',
  n: 'note',
  a: 'image',
  i: 'ink',
};

// The shape the parent hands to add() — id/timestamps/referenceId are injected there.
type NewAnno = Omit<Annotation, 'id' | 'createdAt' | 'updatedAt' | 'referenceId'>;

// Shared empty list for pages with no annotations — a fresh `[]` per page would
// change identity on every render and defeat PageView's memo (#311).
const NO_ANNOS: Annotation[] = [];

// ---- PDF-point ⇄ CSS-px geometry -------------------------------------------
// Annotations store geometry in unscaled PDF points, origin BOTTOM-LEFT (Zotero's
// convention) in the page's UNROTATED user space. The rendered viewport can be
// rotated in 90° steps (#321), so mapping goes through pdfPtToView with the
// page's unrotated CSS-px footprint (uw/uh) — at rotation 0 it degenerates to a
// pure multiply + y-flip, the whole reason the format is scale-independent.

// Unrotated PDF point (pt) → rendered viewport point (CSS px, origin top-left).
function pdfPtToView(x: number, y: number, uw: number, uh: number, scale: number, rot: number): [number, number] {
  const px = x * scale;
  const py = y * scale;
  switch (rot) {
    case 90:
      return [py, px];
    case 180:
      return [uw - px, py];
    case 270:
      return [uh - py, uw - px];
    default:
      return [px, uh - py];
  }
}

// Inverse of pdfPtToView: a rendered viewport point (CSS px) → unrotated PDF pt.
function viewToPdfPt(vx: number, vy: number, uw: number, uh: number, scale: number, rot: number): [number, number] {
  switch (rot) {
    case 90:
      return [vy / scale, vx / scale];
    case 180:
      return [(uw - vx) / scale, vy / scale];
    case 270:
      return [(uw - vy) / scale, (uh - vx) / scale];
    default:
      return [vx / scale, (uh - vy) / scale];
  }
}

function rectPdfToPx(rect: number[], uw: number, uh: number, scale: number, rot: number): { left: number; top: number; width: number; height: number } {
  // Map all four corners — a 90° step swaps the axes, so two diagonal corners
  // alone don't bound the rotated box — and take the axis-aligned bounding box.
  const [x1, y1, x2, y2] = rect;
  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;
  for (const [cx, cy] of [
    [x1, y1],
    [x1, y2],
    [x2, y1],
    [x2, y2],
  ] as [number, number][]) {
    const [vx, vy] = pdfPtToView(cx, cy, uw, uh, scale, rot);
    left = Math.min(left, vx);
    right = Math.max(right, vx);
    top = Math.min(top, vy);
    bottom = Math.max(bottom, vy);
  }
  return { left, top, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
}

// Flatten an ink path (PDF points) to an SVG polyline `points` string in CSS px.
function pathToPoints(flat: number[], uw: number, uh: number, scale: number, rot: number): string {
  const parts: string[] = [];
  for (let i = 0; i + 1 < flat.length; i += 2) {
    const [vx, vy] = pdfPtToView(flat[i], flat[i + 1], uw, uh, scale, rot);
    parts.push(`${vx.toFixed(1)},${vy.toFixed(1)}`);
  }
  return parts.join(' ');
}

// Crop an area annotation's rectangle out of the already-rendered page canvas to
// a PNG blob (the "screenshot" of the selected region). The page canvas is at
// devicePixelRatio × the CSS footprint, so map the annotation's CSS-px box up by
// `ratio`. Returns null if the page isn't rendered or has no rect.
function captureAnnoImage(canvas: HTMLCanvasElement | null, a: Annotation, uw: number, uh: number, scale: number, rot: number): Promise<Blob | null> {
  const r = a.position.rects?.[0];
  if (canvas === null || r === undefined) return Promise.resolve(null);
  const ratio = window.devicePixelRatio || 1;
  const box = rectPdfToPx(r, uw, uh, scale, rot);
  const sx = Math.max(0, Math.round(box.left * ratio));
  const sy = Math.max(0, Math.round(box.top * ratio));
  const sw = Math.max(1, Math.round(box.width * ratio));
  const sh = Math.max(1, Math.round(box.height * ratio));
  const out = document.createElement('canvas');
  out.width = sw;
  out.height = sh;
  const ctx = out.getContext('2d');
  if (ctx === null) return Promise.resolve(null);
  ctx.drawImage(canvas, sx, sy, sw, sh, 0, 0, sw, sh);
  return new Promise((resolve) => out.toBlob((b) => resolve(b), 'image/png'));
}

async function blobToBase64(blob: Blob): Promise<string> {
  const buf = new Uint8Array(await blob.arrayBuffer());
  let bin = '';
  for (let i = 0; i < buf.length; i += 1) bin += String.fromCharCode(buf[i]);
  return btoa(bin);
}

async function invokeTauri<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const { invoke } = await import('@tauri-apps/api/core');
  return invoke<T>(cmd, args);
}

// Copy a PNG blob to the clipboard (best-effort; WebView2 clipboard image write
// can be flaky, so callers ignore failures).
async function copyImageBlob(blob: Blob): Promise<void> {
  const Ctor = (globalThis as { ClipboardItem?: new (items: Record<string, Blob>) => unknown }).ClipboardItem;
  if (Ctor === undefined) throw new Error('clipboard image unsupported');
  const item = new Ctor({ 'image/png': blob }) as ClipboardItem;
  await navigator.clipboard.write([item]);
}

/// A self-contained PDF viewer built on bundled pdf.js — no CDN, no native
/// browser plugin. Replaces the `<iframe src=blob>` viewer, which WebView2
/// (Windows/Edge) refused to render ("此页面已被 Microsoft Edge 阻止") and whose
/// in-PDF links hijacked the whole SPA.
///
/// Each page renders to a canvas we own (works on every platform) plus a pdf.js
/// **text layer** — invisible positioned spans over the canvas that make the text
/// selectable, searchable, and copyable into notes. For tagged PDFs the page's
/// **struct tree** is also mirrored into each canvas as fallback content, so the
/// prose is exposed to screen readers in reading order (#316). The link layer is
/// built from `page.getAnnotations()` so a clicked link routes through
/// `useOpenLink()` into the in-app browser tab instead of navigating the app.
/// Pages render lazily (IntersectionObserver) so a long PDF doesn't rasterise
/// every page up front.

// The worker ships as a bundled asset (Vite `?url`), never fetched remotely.
pdfjsLib.GlobalWorkerOptions.workerSrc = workerUrl;

interface LinkBox {
  left: number;
  top: number;
  width: number;
  height: number;
  url?: string; // external link (routes to the in-app browser)
  dest?: string | unknown[]; // internal GoTo destination (jump within the PDF — refs, figures)
}

// ---- Struct tree → accessible prose (#316) -----------------------------------
/// pdf.js 4.10's API-layer `TextLayer` has no struct-tree wiring — upstream that
/// lives in the viewer's StructTreeLayerBuilder, which we don't bundle. We
/// replicate its minimal core: stream the text WITH marked content (each struct
/// element's run gets wrapped in a `span.markedContent` carrying its id), then
/// mirror `page.getStructTree()` into a span tree whose nodes map PDF roles to
/// ARIA roles and `aria-owns` the matching marked-content spans. Appended INSIDE
/// the <canvas> as fallback content, the tree is never painted but is exposed to
/// screen readers in reading order. Untagged PDFs resolve a null tree and keep
/// their previous (no prose) behaviour — same as the upstream viewer.

// PDF logical-structure role → ARIA role; null means "no role" (a plain span).
// Faithful port of upstream's PDF_ROLE_TO_HTML_ROLE.
const PDF_ROLE_TO_HTML_ROLE: Record<string, string | null> = {
  Document: null,
  DocumentFragment: null,
  Part: 'group',
  Sect: 'group',
  Div: 'group',
  Aside: 'note',
  NonStruct: 'none',
  P: null,
  H: 'heading',
  Title: null,
  FENote: 'note',
  Sub: 'group',
  Lbl: null,
  Span: null,
  Em: null,
  Strong: null,
  Link: 'link',
  Annot: 'note',
  Form: 'form',
  Ruby: null,
  RB: null,
  RT: null,
  RP: null,
  Warichu: null,
  WT: null,
  WP: null,
  L: 'list',
  LI: 'listitem',
  LBody: null,
  Table: 'table',
  TR: 'row',
  TH: 'columnheader',
  TD: 'cell',
  THead: 'columnheader',
  TBody: null,
  TFoot: null,
  Caption: null,
  Figure: 'figure',
  Formula: null,
  Artifact: null,
};
const HEADING_PATTERN = /^H(\d+)$/;

// What a struct-tree node actually carries at runtime: the typed StructTreeNode
// only declares role+children, but nodes also have id/alt/lang, and leaf
// children are content pointers ({type:"content", id}) referencing the text
// layer's markedContent spans.
type StructNode = {
  role?: string;
  id?: string;
  alt?: string;
  lang?: string;
  children?: StructNode[];
};

// alt → aria-label, id → aria-owns, lang → lang (upstream's #setAttributes).
// Null characters are stripped — some producers embed them in strings.
function setStructAttributes(node: StructNode, element: HTMLElement): void {
  if (node.alt !== undefined) element.setAttribute('aria-label', node.alt.replace(/\0/g, ''));
  if (node.id !== undefined) element.setAttribute('aria-owns', node.id);
  if (node.lang !== undefined) element.setAttribute('lang', node.lang.replace(/\0/g, ''));
}

// Walk the struct tree into a hidden span DOM (upstream's #walk). A node whose
// only child is a content pointer collapses: the content id is aria-owns'd
// directly on the node's own span instead of nesting a leaf.
function buildStructTreeDom(node: StructNode | null): HTMLSpanElement | null {
  if (node === null) return null;
  const element = document.createElement('span');
  if (node.role !== undefined) {
    const match = HEADING_PATTERN.exec(node.role);
    if (match !== null) {
      element.setAttribute('role', 'heading');
      element.setAttribute('aria-level', match[1]);
    } else {
      const role = PDF_ROLE_TO_HTML_ROLE[node.role];
      if (role != null) element.setAttribute('role', role);
    }
  }
  setStructAttributes(node, element);
  if (node.children !== undefined) {
    if (node.children.length === 1 && node.children[0].id !== undefined) {
      setStructAttributes(node.children[0], element);
    } else {
      for (const kid of node.children) {
        const child = buildStructTreeDom(kid);
        if (child !== null) element.append(child);
      }
    }
  }
  return element;
}

// Render one annotation's visual overlay, positioned in CSS px from its stored
// PDF-point geometry. pointer-events are OFF while a tool is active (so a new
// selection/drag passes through) and ON otherwise, so a click selects it.
function AnnoOverlay({
  a,
  uw,
  uh,
  scale,
  rot,
  selected,
  interactive,
  onSelect,
}: {
  a: Annotation;
  uw: number; // unrotated page footprint (CSS px) — annotation geometry maps through it
  uh: number;
  scale: number;
  rot: number; // 90°-step view rotation (#321)
  selected: boolean;
  interactive: boolean;
  onSelect: (id: string) => void;
}): JSX.Element {
  const color = a.color ?? ANNOTATION_COLORS[0];
  const pe = interactive ? 'auto' : 'none';
  const cls = `pdfjs-anno${selected ? ' selected' : ''}`;
  const pick = (e: React.MouseEvent): void => {
    e.stopPropagation();
    onSelect(a.id);
  };

  if (a.type === 'ink') {
    const w = (a.position.width ?? 1.5) * scale;
    return (
      <svg className={cls} style={{ pointerEvents: 'none', position: 'absolute', inset: 0, overflow: 'visible' }}>
        {(a.position.paths ?? []).map((p, i) => (
          <polyline
            key={i}
            points={pathToPoints(p, uw, uh, scale, rot)}
            fill="none"
            stroke={color}
            strokeWidth={w}
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ pointerEvents: pe, cursor: interactive ? 'pointer' : undefined }}
            onClick={interactive ? pick : undefined}
          />
        ))}
      </svg>
    );
  }

  const rects = a.position.rects ?? [];
  if (a.type === 'note') {
    const box = rects.length > 0 ? rectPdfToPx(rects[0], uw, uh, scale, rot) : { left: 0, top: 0, width: 16, height: 16 };
    return (
      <button
        className={`${cls} note`}
        style={{ left: box.left, top: box.top, background: color, pointerEvents: pe }}
        title={a.comment}
        onClick={interactive ? pick : undefined}
      >
        <Icon name="note" size={12} />
      </button>
    );
  }

  // highlight / underline / image (area) — one box per rect.
  return (
    <>
      {rects.map((r, i) => {
        const b = rectPdfToPx(r, uw, uh, scale, rot);
        const style: React.CSSProperties = { left: b.left, top: b.top, width: b.width, height: b.height, pointerEvents: pe };
        if (a.type === 'highlight') style.background = `color-mix(in srgb, ${color} 40%, transparent)`;
        else if (a.type === 'underline') style.borderBottom = `2px solid ${color}`;
        else style.border = `2px solid ${color}`; // image / area
        return (
          <div
            key={i}
            className={`${cls} ${a.type}`}
            style={style}
            title={a.comment}
            onClick={interactive ? pick : undefined}
          />
        );
      })}
    </>
  );
}

// The floating editor for the selected annotation: recolor, comment, delete.
function AnnoEditor({
  a,
  uw,
  uh,
  scale,
  rot,
  onUpdate,
  onRemove,
  onClose,
  onCopyImage,
  onSaveImage,
  onAddToNote,
}: {
  a: Annotation;
  uw: number; // unrotated page footprint (CSS px) — see AnnoOverlay
  uh: number;
  scale: number;
  rot: number; // 90°-step view rotation (#321)
  onUpdate: (id: string, patch: Partial<Annotation>) => void;
  onRemove: (id: string) => void;
  onClose: () => void;
  onCopyImage?: () => void;
  onSaveImage?: () => void;
  onAddToNote?: () => void;
}): JSX.Element {
  // useT() is memoised/stable (#311), so children call it locally instead of
  // receiving `t` drilled as a prop (#322).
  const t = useT();
  // Anchor under the annotation's first rect (or its ink bbox).
  const anchor = useMemo(() => {
    const r = a.position.rects?.[0];
    if (r !== undefined) return rectPdfToPx(r, uw, uh, scale, rot);
    const p = a.position.paths?.[0];
    if (p !== undefined && p.length >= 2) {
      const [x, y] = pdfPtToView(p[0], p[1], uw, uh, scale, rot);
      return { left: x, top: y, width: 0, height: 0 };
    }
    return { left: 0, top: 0, width: 0, height: 0 };
  }, [a, uw, uh, scale, rot]);

  // Keep the popover on-screen: near a page/viewport edge it would render partly
  // off-screen, so measure and nudge it back with a transform.
  const editRef = useRef<HTMLDivElement | null>(null);
  useLayoutEffect(() => {
    const el = editRef.current;
    if (el === null) return;
    el.style.transform = '';
    const r = el.getBoundingClientRect();
    let dx = 0;
    let dy = 0;
    if (r.right > window.innerWidth - 8) dx = window.innerWidth - 8 - r.right;
    if (r.bottom > window.innerHeight - 8) dy = window.innerHeight - 8 - r.bottom;
    if (r.left + dx < 8) dx = 8 - r.left;
    if (r.top + dy < 8) dy = 8 - r.top;
    if (dx !== 0 || dy !== 0) el.style.transform = `translate(${dx}px, ${dy}px)`;
  }, [anchor]);

  return (
    <div
      ref={editRef}
      className="pdfjs-anno-editor"
      role="dialog"
      aria-label={t('a11y.annotationEditor')}
      style={{ left: anchor.left, top: anchor.top + anchor.height + 4 }}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        // Escape dismisses the popover (and stops there, so it doesn't also reach
        // the page-scroller's own key handling).
        if (e.key === 'Escape') {
          e.stopPropagation();
          onClose();
        }
      }}
    >
      <div className="pdfjs-anno-colors">
        {ANNOTATION_COLORS.map((c) => (
          <button
            key={c}
            className={`pdfjs-anno-swatch${(a.color ?? ANNOTATION_COLORS[0]) === c ? ' active' : ''}`}
            style={{ background: c }}
            title={c}
            onClick={() => onUpdate(a.id, { color: c })}
          />
        ))}
      </div>
      <textarea
        className="pdfjs-anno-comment"
        placeholder={t('read.annComment')}
        value={a.comment ?? ''}
        autoFocus={a.type === 'note'}
        spellCheck={false}
        onChange={(e) => onUpdate(a.id, { comment: e.target.value })}
      />
      {/* Tags — distinct from the comment: a chip list you add to with Enter. */}
      <div className="pdfjs-anno-tags">
        {(a.tags ?? []).map((tg) => (
          <span className="pdfjs-anno-tag" key={tg}>
            {tg}
            <button
              className="pdfjs-anno-tag-x"
              title={t('read.annTagRemove')}
              onClick={() => onUpdate(a.id, { tags: (a.tags ?? []).filter((x) => x !== tg) })}
            >
              <Icon name="close" size={11} />
            </button>
          </span>
        ))}
        <input
          className="pdfjs-anno-taginput"
          placeholder={t('read.annTagAdd')}
          spellCheck={false}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return;
            const v = e.currentTarget.value.trim();
            const cur = a.tags ?? [];
            if (v !== '' && !cur.includes(v)) onUpdate(a.id, { tags: [...cur, v] });
            e.currentTarget.value = '';
          }}
        />
      </div>
      {a.type === 'image' && (onCopyImage !== undefined || onSaveImage !== undefined) && (
        <div className="pdfjs-anno-imgacts">
          {onCopyImage !== undefined && (
            <button className="pdfjs-anno-imgbtn" onClick={onCopyImage}>
              <Icon name="copy" size={13} /> {t('read.annCopyImage')}
            </button>
          )}
          {onSaveImage !== undefined && (
            <button className="pdfjs-anno-imgbtn" onClick={onSaveImage}>
              <Icon name="download" size={13} /> {t('read.annSaveImage')}
            </button>
          )}
          {onAddToNote !== undefined && (
            <button className="pdfjs-anno-imgbtn" onClick={onAddToNote}>
              <Icon name="note" size={13} /> {t('read.annImageToNote')}
            </button>
          )}
        </div>
      )}
      <div className="pdfjs-anno-actions">
        <button className="pdfjs-anno-del" title={t('read.annDelete')} onClick={() => onRemove(a.id)}>
          <Icon name="trash" size={13} /> {t('read.annDelete')}
        </button>
        <button className="pdfjs-anno-done" onClick={onClose}>
          {t('read.annDone')}
        </button>
      </div>
    </div>
  );
}

/// One rendered page. Memoised (#311): an annotation edit updates the store on
/// every keystroke (comment typing) and the viewer itself re-renders on each
/// scroll-driven page-indicator tick — without memo that re-rendered EVERY
/// PageView, canvas/text-layer effects and all. Every prop is stabilised at the
/// call site (callbacks via useCallback / a latest-ref trampoline, `annos` via
/// structural sharing, `t` stable per language since 3389529a), so a page whose
/// inputs didn't change now skips re-rendering entirely. Behavior is unchanged:
/// selection, the editor popover, the lazy IntersectionObserver render, and the
/// highlight marks all key off the same props as before.
const PageView = memo(function PageView({
  pdf,
  pageNum,
  scale,
  rotation,
  query,
  dim,
  onLink,
  onDest,
  annos,
  tool,
  color,
  selectedId,
  onCreate,
  onSelect,
  onUpdate,
  onRemove,
  onToolDone,
  onImageToNote,
  readOnly = false,
}: {
  pdf: PDFDocumentProxy;
  pageNum: number;
  scale: number;
  rotation: number; // 90°-step view rotation, added to the page's own (#321)
  query: string;
  dim?: { w: number; h: number };
  onLink: (url: string) => void;
  onDest: (dest: string | unknown[]) => void;
  annos: Annotation[];
  tool: Tool;
  color: string;
  selectedId: string | null;
  onCreate: (a: NewAnno) => string;
  onSelect: (id: string | null) => void;
  onUpdate: (id: string, patch: Partial<Annotation>) => void;
  onRemove: (id: string) => void;
  onToolDone: () => void;
  onImageToNote?: (dataUri: string) => void; // append an area screenshot to the notes
  readOnly?: boolean; // the split-view mirror pane: overlays visible but not editable
}): JSX.Element {
  // Local useT() (stable, #311) instead of a drilled `t` prop.
  const t = useT();
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const textRef = useRef<HTMLDivElement | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const textDivsRef = useRef<HTMLElement[]>([]);
  const [visible, setVisible] = useState(pageNum === 1); // first page eagerly
  const [size, setSize] = useState<{ w: number; h: number } | null>(null);
  // Live device-pixel-ratio: dragging the window across mixed-DPI monitors used
  // to leave the canvas rasterised at the old ratio until zoom changed. Tracking
  // it re-runs the render effect so the page re-rasterises crisply.
  const dpr = useDevicePixelRatio();
  const [links, setLinks] = useState<LinkBox[]>([]);
  // Live preview for a drag-in-progress area rect, in CSS px. The ink stroke
  // preview draws into the `inkRef` canvas overlay instead — ref-only, so a
  // pointermove doesn't re-render the whole page component (#321).
  const [draft, setDraft] = useState<{ rect: number[] } | null>(null);
  const inkRef = useRef<HTMLCanvasElement | null>(null);

  // Lazily render a page the first time it comes within ~1000px of the viewport,
  // then LATCH it rendered — never un-render on scroll-away.
  //
  // v0.3.76 also un-rendered pages scrolled far away (clearing the canvas/text and
  // reserving the pre-measured `dim` footprint) to cap memory on huge PDFs (#311).
  // That churn was buggy in practice: scrolling left stale link boxes and
  // annotation layers floating on blanked pages, and overlays drifted onto the
  // wrong pages. Correctness beats the memory optimisation — keep the lazy first
  // paint (a never-visited page still costs nothing) but drop the destructive
  // un-render. A device-tested re-approach to offscreen eviction can come later.
  useEffect(() => {
    if (visible) return; // already latched
    const el = wrapRef.current;
    if (el === null) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries[entries.length - 1].isIntersecting) setVisible(true);
      },
      { rootMargin: '1000px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [visible]);

  useEffect(() => {
    if (!visible) {
      // Freed offscreen: drop the canvas backing store + text layer to reclaim
      // memory, and clear `size` so the footprint falls back to the scale-correct
      // pre-measured `dim` (a stale `size` would reserve the wrong height across a
      // zoom while un-rendered).
      const canvas = canvasRef.current;
      if (canvas !== null) {
        canvas.width = 0;
        canvas.height = 0;
        canvas.replaceChildren(); // struct-tree fallback content (#316)
      }
      textRef.current?.replaceChildren();
      textDivsRef.current = [];
      setSize(null);
      return;
    }
    let cancelled = false;
    let renderTask: ReturnType<PDFPageProxy['render']> | null = null;
    let textLayer: TextLayer | null = null;
    void (async () => {
      const page = await pdf.getPage(pageNum);
      if (cancelled) return;
      // pdf.js treats the viewport rotation as ABSOLUTE (it defaults to the
      // page's own), so the user rotation stacks on top of `page.rotate` (#321).
      const viewport = page.getViewport({ scale, rotation: (page.rotate + rotation) % 360 });
      const canvas = canvasRef.current;
      if (canvas === null) return;
      const ctx = canvas.getContext('2d');
      if (ctx === null) return;
      const ratio = dpr;
      canvas.width = Math.floor(viewport.width * ratio);
      canvas.height = Math.floor(viewport.height * ratio);
      setSize({ w: viewport.width, h: viewport.height });
      renderTask = page.render({
        canvasContext: ctx,
        viewport,
        transform: ratio !== 1 ? [ratio, 0, 0, ratio, 0, 0] : undefined,
      });
      try {
        await renderTask.promise;
      } catch {
        return; // render cancelled (scale changed / unmounted)
      }
      if (cancelled) return;

      // Text layer — selectable/searchable spans over the canvas. Streamed WITH
      // marked content so each struct element's run is wrapped in an id'd
      // span.markedContent the struct tree below can aria-owns (#316). The
      // wrappers are zero-size anchors; layout/selection/copy are unchanged.
      const textDiv = textRef.current;
      if (textDiv !== null) {
        textDiv.replaceChildren();
        textLayer = new TextLayer({
          textContentSource: page.streamTextContent({ includeMarkedContent: true }),
          container: textDiv,
          viewport,
        });
        try {
          await textLayer.render();
          if (!cancelled) textDivsRef.current = textLayer.textDivs;
        } catch {
          /* text layer is best-effort */
        }
      }

      // Struct tree → canvas fallback content (#316): invisible, but screen
      // readers get the page prose in reading order. Untagged PDFs resolve a
      // null tree — nothing appended. The split-view mirror pane skips it: its
      // markedContent ids duplicate the primary pane's, and exposing the same
      // prose twice is noise.
      if (!cancelled && !readOnly) {
        const tree = (await page.getStructTree().catch(() => null)) as StructNode | null;
        const dom = buildStructTreeDom(tree);
        if (!cancelled && dom !== null) {
          dom.classList.add('structTree');
          canvas.replaceChildren(dom);
        }
      }

      // Link overlay — external links route to the in-app browser tab.
      const annots = await page.getAnnotations();
      if (cancelled) return;
      const boxes: LinkBox[] = [];
      for (const a of annots as Array<Record<string, unknown>>) {
        const rect = a.rect;
        if (a.subtype !== 'Link' || !Array.isArray(rect)) continue;
        const url = typeof a.url === 'string' ? a.url : undefined;
        // Internal links (refs, figures, ToC) carry a `dest` — a named string or an
        // explicit destination array — instead of a URL. Capture both.
        const dest =
          typeof a.dest === 'string' || Array.isArray(a.dest) ? (a.dest as string | unknown[]) : undefined;
        if (url === undefined && dest === undefined) continue;
        const [x1, y1, x2, y2] = viewport.convertToViewportRectangle(rect as number[]);
        boxes.push({
          left: Math.min(x1, x2),
          top: Math.min(y1, y2),
          width: Math.abs(x2 - x1),
          height: Math.abs(y2 - y1),
          url,
          dest,
        });
      }
      if (!cancelled) setLinks(boxes);
    })();
    return () => {
      cancelled = true;
      renderTask?.cancel();
      textLayer?.cancel();
      // Drop the struct-tree fallback so a stale tree (previous doc/scale) can't
      // outlive its text layer — the re-run re-appends a fresh one (#316).
      canvasRef.current?.replaceChildren();
    };
  }, [pdf, pageNum, scale, visible, dpr, rotation]);

  // Highlight the actual matched substrings by wrapping them in <mark>. The text
  // layer's own text is transparent (the canvas shows the glyphs), so the mark's
  // translucent background paints a highlight box exactly over the match. We stash
  // each div's original text so clearing/refreshing the query restores it.
  useEffect(() => {
    const q = query.trim();
    const ql = q.toLowerCase();
    for (const div of textDivsRef.current) {
      let orig = div.getAttribute('data-orig');
      if (orig === null) {
        orig = div.textContent ?? '';
        div.setAttribute('data-orig', orig);
      }
      const base = orig.toLowerCase();
      if (q === '' || !base.includes(ql)) {
        if (div.getAttribute('data-hl') === '1') {
          div.textContent = orig;
          div.removeAttribute('data-hl');
        }
        continue;
      }
      let html = '';
      let from = 0;
      let idx = base.indexOf(ql, 0);
      while (idx !== -1) {
        html += escapeHtml(orig.slice(from, idx));
        html += `<mark class="pdfjs-mark">${escapeHtml(orig.slice(idx, idx + q.length))}</mark>`;
        from = idx + q.length;
        idx = base.indexOf(ql, from);
      }
      html += escapeHtml(orig.slice(from));
      div.innerHTML = html;
      div.setAttribute('data-hl', '1');
    }
  }, [query, size]);

  // Reserve the page's true footprint even before it rasterises: use the rendered
  // `size`, else the pre-measured `dim` (from the parent's viewport pass), else a
  // placeholder. Pre-reserving the correct height is what keeps scroll geometry
  // stable so ToC/link jumps land on the right spot instead of drifting as lazy
  // pages above the target render and grow.
  const footprint = size ?? dim ?? null;
  const style: React.CSSProperties = {
    ...(footprint !== null ? { width: footprint.w, height: footprint.h } : { minHeight: 400 }),
    // Contract from pdf.js: the text layer sizes itself via calc(var(--scale-factor) * rawDims).
    ['--scale-factor' as string]: String(scale),
  };
  const fh = footprint?.h ?? 0;
  const fw = footprint?.w ?? 0;
  // The footprint WITHOUT the user rotation (at 90°/270° the rendered box is the
  // swapped unrotated one). Annotation geometry is stored in unrotated PDF
  // points, so overlays and hit-mapping go through uw/uh + `rotation` (#321).
  const uw = rotation % 180 !== 0 ? fh : fw;
  const uh = rotation % 180 !== 0 ? fw : fh;

  // The point (local CSS px) of a pointer event within this page's box.
  const localPoint = (e: React.PointerEvent | PointerEvent): [number, number] => {
    const box = wrapRef.current?.getBoundingClientRect();
    if (box === undefined) return [0, 0];
    return [e.clientX - box.left, e.clientY - box.top];
  };

  // A capture surface (crosshair) sits over the page only for the click/drag tools
  // (note / image / ink); highlight & underline stay selection-driven and need the
  // text layer, so no surface for them. Pointer tracking uses window listeners, not
  // setPointerCapture — unreliable on WebView2 (feedback_pointer_capture_webview2).
  const onSurfaceDown = (e: React.PointerEvent): void => {
    if (footprint === null) return;
    e.preventDefault();
    const [x0, y0] = localPoint(e);

    if (tool === 'note') {
      const [px, py] = viewToPdfPt(x0, y0, uw, uh, scale, rotation);
      const s = 12 / scale; // ~12px marker box in PDF units
      const id = onCreate({
        type: 'note',
        color,
        pageIndex: pageNum - 1,
        comment: '',
        position: { pageIndex: pageNum - 1, rects: [[px, py - s, px + s, py]] },
        tags: [],
      });
      onSelect(id);
      onToolDone();
      return;
    }

    if (tool === 'image') {
      const move = (ev: PointerEvent): void => {
        const [x, y] = localPoint(ev);
        setDraft({ rect: [Math.min(x0, x), Math.min(y0, y), Math.max(x0, x), Math.max(y0, y)] });
      };
      const up = (ev: PointerEvent): void => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        const [x, y] = localPoint(ev);
        setDraft(null);
        if (Math.abs(x - x0) < 4 && Math.abs(y - y0) < 4) return; // ignore a stray click
        const [ax, ay] = viewToPdfPt(Math.min(x0, x), Math.max(y0, y), uw, uh, scale, rotation);
        const [bx, by] = viewToPdfPt(Math.max(x0, x), Math.min(y0, y), uw, uh, scale, rotation);
        const id = onCreate({
          type: 'image',
          color,
          pageIndex: pageNum - 1,
          position: {
            pageIndex: pageNum - 1,
            rects: [[Math.min(ax, bx), Math.min(ay, by), Math.max(ax, bx), Math.max(ay, by)]],
          },
          tags: [],
        });
        onSelect(id);
        onToolDone();
      };
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up);
      return;
    }

    if (tool === 'ink') {
      const pts: number[] = [x0, y0];
      // The in-progress stroke draws into a lightweight canvas overlay (ref-only)
      // instead of a draft state that re-rendered the whole page component — text
      // layer, annotation overlays and all — on every pointermove (#321). React
      // state (the annotation store) is touched once, on pointer-up. Segments
      // draw incrementally so a move costs one lineTo, never a full repaint.
      const pv = inkRef.current;
      const pctx = pv?.getContext('2d') ?? null;
      if (pv !== null && pctx !== null) {
        pv.width = Math.max(1, Math.floor(fw * dpr));
        pv.height = Math.max(1, Math.floor(fh * dpr));
        pctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        pctx.strokeStyle = color;
        pctx.lineWidth = 1.5 * scale;
        pctx.lineCap = 'round';
        pctx.lineJoin = 'round';
      }
      let lastX = x0;
      let lastY = y0;
      const move = (ev: PointerEvent): void => {
        const [x, y] = localPoint(ev);
        pts.push(x, y);
        if (pctx !== null) {
          pctx.beginPath();
          pctx.moveTo(lastX, lastY);
          pctx.lineTo(x, y);
          pctx.stroke();
        }
        lastX = x;
        lastY = y;
      };
      const up = (): void => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        if (pv !== null && pctx !== null) {
          pctx.setTransform(1, 0, 0, 1, 0, 0);
          pctx.clearRect(0, 0, pv.width, pv.height);
        }
        if (pts.length < 4) return;
        const pdfPts: number[] = [];
        for (let i = 0; i + 1 < pts.length; i += 2) {
          const [px, py] = viewToPdfPt(pts[i], pts[i + 1], uw, uh, scale, rotation);
          pdfPts.push(px, py);
        }
        onCreate({
          type: 'ink',
          color,
          pageIndex: pageNum - 1,
          position: { pageIndex: pageNum - 1, paths: [pdfPts], width: 1.5 },
          tags: [],
        });
      };
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up);
    }
  };

  const selected = readOnly ? null : annos.find((a) => a.id === selectedId) ?? null;
  const surfaceTool = !readOnly && (tool === 'note' || tool === 'image' || tool === 'ink');

  // Area-annotation screenshot: crop the region from this page's canvas, then
  // copy it to the clipboard or save it as a PNG (Zotero's area-tool actions).
  async function copyAreaImage(a: Annotation): Promise<void> {
    const blob = await captureAnnoImage(canvasRef.current, a, uw, uh, scale, rotation);
    if (blob !== null) await copyImageBlob(blob).catch(() => undefined);
  }
  async function saveAreaImage(a: Annotation): Promise<void> {
    const blob = await captureAnnoImage(canvasRef.current, a, uw, uh, scale, rotation);
    if (blob === null) return;
    const base64 = await blobToBase64(blob);
    await invokeTauri('save_image_as', { defaultName: `figure-p${a.pageIndex + 1}.png`, base64 }).catch(() => undefined);
  }
  async function addAreaToNote(a: Annotation): Promise<void> {
    const blob = await captureAnnoImage(canvasRef.current, a, uw, uh, scale, rotation);
    if (blob === null) return;
    const base64 = await blobToBase64(blob);
    onImageToNote?.(`data:image/png;base64,${base64}`);
  }

  return (
    // role=region (like the upstream viewer), NOT img: img would mask all
    // descendants as presentational, hiding the struct-tree prose inside the
    // canvas from screen readers (#316).
    <div
      ref={wrapRef}
      className="pdfjs-page"
      data-page={pageNum}
      role="region"
      aria-label={t('read.pdfPage').replace('{n}', String(pageNum))}
      style={style}
    >
      <canvas ref={canvasRef} className="pdfjs-canvas" style={size !== null ? { width: size.w, height: size.h } : undefined} />
      <div ref={textRef} className="textLayer" />
      {links.map((l, i) => (
        <button
          key={i}
          className={l.url !== undefined ? 'pdfjs-link' : 'pdfjs-link internal'}
          style={{ left: l.left, top: l.top, width: l.width, height: l.height }}
          title={l.url}
          onClick={(e) => {
            e.preventDefault();
            if (l.url !== undefined) onLink(l.url);
            else if (l.dest !== undefined) onDest(l.dest);
          }}
        />
      ))}
      {footprint !== null && annos.length > 0 && (
        <div className={`pdfjs-anno-layer${tool !== null && !readOnly ? ' tooling' : ''}`}>
          {annos.map((a) => (
            <AnnoOverlay
              key={a.id}
              a={a}
              uw={uw}
              uh={uh}
              scale={scale}
              rot={rotation}
              selected={a.id === selectedId}
              interactive={!readOnly && tool === null}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
      {surfaceTool && footprint !== null && (
        <div className={`pdfjs-draw-surface ${tool}`} onPointerDown={onSurfaceDown}>
          {tool === 'ink' && (
            <canvas
              ref={inkRef}
              style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none' }}
            />
          )}
          {draft?.rect !== undefined && (
            <div
              className="pdfjs-draft-rect"
              style={{ left: draft.rect[0], top: draft.rect[1], width: draft.rect[2] - draft.rect[0], height: draft.rect[3] - draft.rect[1], borderColor: color }}
            />
          )}
        </div>
      )}
      {selected !== null && footprint !== null && (
        <AnnoEditor
          a={selected}
          uw={uw}
          uh={uh}
          scale={scale}
          rot={rotation}
          onUpdate={onUpdate}
          onRemove={onRemove}
          onClose={() => onSelect(null)}
          onCopyImage={selected.type === 'image' ? () => void copyAreaImage(selected) : undefined}
          onSaveImage={selected.type === 'image' && isTauri() ? () => void saveAreaImage(selected) : undefined}
          onAddToNote={
            selected.type === 'image' && onImageToNote !== undefined ? () => void addAreaToNote(selected) : undefined
          }
        />
      )}
    </div>
  );
});

// A node in the PDF's outline (table of contents / bookmarks).
interface OutlineNode {
  title: string;
  dest: string | unknown[] | null;
  items: OutlineNode[];
}

function OutlineList({
  nodes,
  onGo,
  depth,
}: {
  nodes: OutlineNode[];
  onGo: (dest: string | unknown[]) => void;
  depth: number;
}): JSX.Element {
  return (
    <ul className="pdfjs-toc-list">
      {nodes.map((n, i) => (
        <li key={i}>
          <button
            className="pdfjs-toc-item"
            style={{ paddingLeft: 8 + depth * 12 }}
            disabled={n.dest === null}
            title={n.title}
            onClick={() => n.dest !== null && onGo(n.dest)}
          >
            {n.title}
          </button>
          {n.items.length > 0 && <OutlineList nodes={n.items} onGo={onGo} depth={depth + 1} />}
        </li>
      ))}
    </ul>
  );
}

// The panel-icon + fallback label for each annotation kind (Zotero-style list).
function annoIcon(type: Annotation['type']): 'highlight' | 'underline' | 'note' | 'square' | 'pen' {
  switch (type) {
    case 'underline':
      return 'underline';
    case 'note':
      return 'note';
    case 'image':
      return 'square';
    case 'ink':
      return 'pen';
    default:
      return 'highlight';
  }
}
function annoLabelKey(type: Annotation['type']): string {
  switch (type) {
    case 'underline':
      return 'read.annUnderline';
    case 'note':
      return 'read.annNote';
    case 'image':
      return 'read.annArea';
    case 'ink':
      return 'read.annDraw';
    default:
      return 'read.annHighlight';
  }
}

// The left-panel Annotations tab — every annotation on the PDF, in reading order,
// with a colour dot, kind icon, a text/comment preview, and its page. Clicking a
// row jumps to it (and selects it). Mirrors Zotero's annotations sidebar.
function AnnotationList({
  annos,
  selectedId,
  onGo,
}: {
  annos: Annotation[];
  selectedId: string | null;
  onGo: (a: Annotation) => void;
}): JSX.Element {
  // Local useT() (stable, #311) instead of a drilled `t` prop (#322).
  const t = useT();
  if (annos.length === 0) {
    return <div className="pdfjs-anno-empty muted small">{t('read.annEmpty')}</div>;
  }
  return (
    <ul className="pdfjs-anno-list">
      {annos.map((a) => {
        const text = (a.text ?? '').trim();
        const comment = (a.comment ?? '').trim();
        const primary = text || comment || t(annoLabelKey(a.type));
        return (
          <li key={a.id}>
            <button
              className={`pdfjs-anno-row${a.id === selectedId ? ' active' : ''}`}
              title={primary}
              onClick={() => onGo(a)}
            >
              <span className="pdfjs-anno-dot" style={{ background: a.color ?? '#ffd400' }} />
              <Icon name={annoIcon(a.type)} size={13} />
              <span className="pdfjs-anno-rowtext">{primary}</span>
              <span className="pdfjs-anno-pg muted small">{a.pageIndex + 1}</span>
            </button>
            {text !== '' && comment !== '' && <div className="pdfjs-anno-rowcomment muted small">{comment}</div>}
            {(a.tags ?? []).length > 0 && (
              <div className="pdfjs-anno-rowtags">
                {(a.tags ?? []).map((tg) => (
                  <span className="pdfjs-anno-rowtag" key={tg}>
                    {tg}
                  </span>
                ))}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// One page thumbnail — rendered lazily to a small canvas (IntersectionObserver),
// clicking jumps to the page. The active page is outlined and scrolled into view.
function Thumb({
  pdf,
  pageNum,
  active,
  rotation,
  onGo,
}: {
  pdf: PDFDocumentProxy;
  pageNum: number;
  active: boolean;
  rotation: number; // 90°-step view rotation — thumbs rotate with the pages (#321)
  onGo: (n: number) => void;
}): JSX.Element {
  const btnRef = useRef<HTMLButtonElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [visible, setVisible] = useState(pageNum <= 5);

  useEffect(() => {
    const el = btnRef.current;
    if (el === null || visible) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          io.disconnect();
        }
      },
      { rootMargin: '250px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [visible]);

  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    let task: ReturnType<PDFPageProxy['render']> | null = null;
    void (async () => {
      const page = await pdf.getPage(pageNum);
      if (cancelled) return;
      const rot = (page.rotate + rotation) % 360;
      const base = page.getViewport({ scale: 1, rotation: rot });
      const vp = page.getViewport({ scale: 132 / base.width, rotation: rot });
      const canvas = canvasRef.current;
      if (canvas === null) return;
      const ctx = canvas.getContext('2d');
      if (ctx === null) return;
      canvas.width = Math.floor(vp.width);
      canvas.height = Math.floor(vp.height);
      task = page.render({ canvasContext: ctx, viewport: vp });
      try {
        await task.promise;
      } catch {
        /* cancelled (unmount) */
      }
    })();
    return () => {
      cancelled = true;
      task?.cancel();
    };
  }, [pdf, pageNum, visible, rotation]);

  useEffect(() => {
    if (active) btnRef.current?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  return (
    <button
      ref={btnRef}
      className={active ? 'pdfjs-thumb active' : 'pdfjs-thumb'}
      onClick={() => onGo(pageNum)}
    >
      <canvas ref={canvasRef} className="pdfjs-thumb-canvas" />
      <span className="pdfjs-thumb-n">{pageNum}</span>
    </button>
  );
}

function ThumbList({
  pdf,
  current,
  rotation,
  onGo,
}: {
  pdf: PDFDocumentProxy;
  current: number;
  rotation: number;
  onGo: (n: number) => void;
}): JSX.Element {
  return (
    <div className="pdfjs-thumbs">
      {Array.from({ length: pdf.numPages }, (_, i) => (
        <Thumb key={i + 1} pdf={pdf} pageNum={i + 1} active={current === i + 1} rotation={rotation} onGo={onGo} />
      ))}
    </div>
  );
}

// Persisted zoom level, shared across PDFs so the reader reopens at the level the
// user last chose instead of always 1.2× (#321).
const PDF_SCALE_KEY = 'termipod.pdf.scale';
function loadPdfScale(): number {
  try {
    const n = Number(localStorage.getItem(PDF_SCALE_KEY));
    if (Number.isFinite(n) && n >= 0.4 && n <= 3) return n;
  } catch {
    /* ignore */
  }
  return 1.2;
}
function savePdfScale(n: number): void {
  try {
    localStorage.setItem(PDF_SCALE_KEY, String(n));
  } catch {
    /* ignore */
  }
}

// Persisted view rotation (90° steps), shared across PDFs like the zoom level —
// a landscape scan stays rotated the way the user left it (#321).
const PDF_ROTATION_KEY = 'termipod.pdf.rotation';
function loadPdfRotation(): number {
  try {
    const n = Number(localStorage.getItem(PDF_ROTATION_KEY));
    if (Number.isFinite(n) && n % 90 === 0) return ((n % 360) + 360) % 360;
  } catch {
    /* ignore */
  }
  return 0;
}
function savePdfRotation(n: number): void {
  try {
    localStorage.setItem(PDF_ROTATION_KEY, String(n));
  } catch {
    /* ignore */
  }
}

export function PdfCanvas({
  data,
  referenceId,
  onSaveSelection,
  onImageToNote,
  docUrl,
  detailsOpen,
  onToggleDetails,
}: {
  data: ArrayBuffer;
  fileName?: string; // accepted for API compatibility; no longer shown (redundant with the reader title)
  referenceId?: string;
  onSaveSelection?: (text: string) => void;
  onImageToNote?: (dataUri: string) => void; // append an area screenshot to the reference's notes
  // Reader-chrome actions hosted in this toolbar so the reader needs no separate
  // title/action row above the PDF (saves vertical space). Open the original URL
  // and toggle the details/metadata side panel.
  docUrl?: string;
  detailsOpen?: boolean;
  onToggleDetails?: () => void;
}): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const scrollRef = useRef<HTMLDivElement | null>(null);
  // Per-page cache of each text item's lowercased string. Counting occurrences
  // per item (not over the whole joined page text) mirrors how the highlight
  // <mark>s are produced — one span at a time — so the match counter and the
  // rendered marks stay in lockstep (a cross-item match is invisible to both).
  const pageItemsRef = useRef<Map<number, string[]>>(new Map());
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null);
  const [err, setErr] = useState(false);
  const [loadPct, setLoadPct] = useState(0); // 0–1 download/parse progress
  const [scale, setScale] = useState(loadPdfScale);
  const [rotation, setRotation] = useState(loadPdfRotation); // 90° steps, persisted (#321)
  // Annotations for this reference, grouped by page (0-based). Only usable when a
  // referenceId is present (annotations attach to a reference).
  const allAnnos = useAnnotations((s) => s.items);
  const addAnno = useAnnotations((s) => s.add);
  const updateAnno = useAnnotations((s) => s.update);
  const removeAnno = useAnnotations((s) => s.remove);
  const undoAnno = useAnnotations((s) => s.undo);
  const [tool, setTool] = useState<Tool>(null);
  const [annoColor, setAnnoColor] = useState(ANNOTATION_COLORS[0]);
  const [selectedAnno, setSelectedAnno] = useState<string | null>(null);
  // Split view (Zotero-style): the reading area shows the same PDF in two panes —
  // 'vertical' = side by side, 'horizontal' = stacked. The mirror pane is a
  // read-only reading view; annotation happens in the primary pane.
  const [split, setSplit] = useState<'none' | 'vertical' | 'horizontal'>('none');
  // Right-click menu anchor + whether the click landed on a live selection in the
  // primary pane (so annotation actions apply).
  const [menu, setMenu] = useState<{ x: number; y: number; onSel: boolean; text: string } | null>(null);
  const viewRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  // Only PDF-geometry annotations belong here — exclude EPUB (CFI) and image
  // (normalized-space) annotations that may share the same reference.
  const isPdfAnno = (a: Annotation): boolean => a.position.cfi === undefined && a.position.space !== 'image';
  // Structural sharing (#311): reuse the previous render's per-page array when
  // its contents are unchanged, so editing one annotation doesn't change the
  // `annos` prop identity of every OTHER annotated page — with PageView memoised,
  // only the edited page re-renders. (The store keeps untouched Annotation
  // objects reference-equal, so an element-wise compare is exact.)
  const prevAnnosRef = useRef<Map<number, Annotation[]> | null>(null);
  const annosByPage = useMemo(() => {
    const m = new Map<number, Annotation[]>();
    if (referenceId !== undefined) {
      for (const a of allAnnos) {
        if (a.referenceId !== referenceId || !isPdfAnno(a)) continue;
        const list = m.get(a.pageIndex) ?? [];
        list.push(a);
        m.set(a.pageIndex, list);
      }
    }
    const prev = prevAnnosRef.current;
    if (prev !== null) {
      for (const [k, list] of m) {
        const p = prev.get(k);
        if (p !== undefined && p.length === list.length && list.every((a, i) => a === p[i])) m.set(k, p);
      }
    }
    prevAnnosRef.current = m;
    return m;
  }, [allAnnos, referenceId]);
  // Flat, reading-order list for the Annotations panel tab.
  const refAnnos = useMemo(() => {
    if (referenceId === undefined) return [];
    return allAnnos
      .filter((a) => a.referenceId === referenceId && isPdfAnno(a))
      .sort((x, y) => x.pageIndex - y.pageIndex || (x.sortIndex ?? '').localeCompare(y.sortIndex ?? ''));
  }, [allAnnos, referenceId]);
  const [term, setTerm] = useState(''); // the live input value
  const [query, setQuery] = useState(''); // the committed search term (drives highlight)
  // Flat, match-granular hit list: one entry per occurrence, with the page and
  // that occurrence's index within the page (so stepping lands on the exact mark,
  // not just the page). `matchPos` indexes into this list.
  const [matches, setMatches] = useState<{ page: number; occ: number }[]>([]);
  const [matchPos, setMatchPos] = useState(0);
  const searchSeq = useRef(0);
  const [saved, setSaved] = useState(false);
  const [currentPage, setCurrentPage] = useState(1);
  const [pageInput, setPageInput] = useState('1');
  const [outline, setOutline] = useState<OutlineNode[]>([]);
  // Base (scale-1) page footprints, measured ONCE per document. Aspect is
  // scale-invariant, so the per-scale dims are a cheap synchronous multiply — no
  // need to re-fetch every page on each zoom step (#311). A 90°/270° rotation
  // swaps the axes — the footprints stay correct without re-measuring (#321).
  const [baseDims, setBaseDims] = useState<{ w: number; h: number }[]>([]);
  const pageDims = useMemo(
    () =>
      baseDims.map((d) => {
        const w = d.w * scale;
        const h = d.h * scale;
        return rotation % 180 !== 0 ? { w: h, h: w } : { w, h };
      }),
    [baseDims, scale, rotation],
  );
  const [showToc, setShowToc] = useState(false);
  const [panelTab, setPanelTab] = useState<'outline' | 'thumbs' | 'annos'>('thumbs');
  const [tocW, setTocW] = useState(() => {
    const v = Number(localStorage.getItem('termipod.read.pdfTocW'));
    return Number.isFinite(v) && v > 0 ? v : 240;
  });
  // Mirrors tocW so the resize handler can read the live width across rapid drag
  // ticks (and decide to auto-collapse) without a functional-updater side effect.
  const tocWRef = useRef(tocW);
  tocWRef.current = tocW;
  // The text selected in the PDF, captured on the +notes button's mousedown —
  // before the click can collapse the selection (clicking a button elsewhere in
  // the DOM clears window.getSelection, which is why +notes previously did nothing).
  const pendingSelRef = useRef('');

  useEffect(() => {
    let cancelled = false;
    let doc: PDFDocumentProxy | null = null;
    setPdf(null);
    setErr(false);
    setLoadPct(0);
    pageItemsRef.current = new Map();
    // Copy into a fresh buffer — pdf.js transfers/detaches the ArrayBuffer, which
    // would corrupt a caller that reuses it.
    const bytes = new Uint8Array(data.byteLength);
    bytes.set(new Uint8Array(data));
    const task = pdfjsLib.getDocument({ data: bytes });
    // Progress for large files — a 100 MB scan otherwise shows a static spinner.
    task.onProgress = ({ loaded, total }: { loaded: number; total: number }) => {
      if (!cancelled && total > 0) setLoadPct(Math.min(1, loaded / total));
    };
    void task.promise
      .then((d) => {
        if (cancelled) {
          void d.destroy();
          return;
        }
        doc = d;
        setPdf(d);
      })
      .catch(() => {
        if (!cancelled) setErr(true);
      });
    return () => {
      cancelled = true;
      void doc?.destroy();
    };
  }, [data]);

  // Load the document outline (table of contents / bookmarks), if any.
  useEffect(() => {
    if (pdf === null) {
      setOutline([]);
      return;
    }
    let alive = true;
    void pdf.getOutline().then((ol) => {
      if (alive) setOutline((ol as OutlineNode[] | null) ?? []);
    });
    return () => {
      alive = false;
    };
  }, [pdf]);

  // Measure every page's footprint at the current scale up front (cheap — a
  // viewport calc, no rasterisation). This lets each not-yet-rendered PageView
  // reserve its true height, so the scroll container's geometry is correct before
  // any page paints — the precondition for ToC/link jumps landing accurately.
  useEffect(() => {
    if (pdf === null) {
      setBaseDims([]);
      return;
    }
    let alive = true;
    void (async () => {
      const dims: { w: number; h: number }[] = [];
      for (let n = 1; n <= pdf.numPages; n += 1) {
        const page = await pdf.getPage(n);
        if (!alive) return;
        const vp = page.getViewport({ scale: 1 });
        dims.push({ w: vp.width, h: vp.height });
      }
      if (alive) setBaseDims(dims);
    })();
    return () => {
      alive = false;
    };
  }, [pdf]);

  // Track the most-visible page as the user scrolls, to drive the page indicator.
  useEffect(() => {
    const root = scrollRef.current;
    if (root === null || pdf === null) return;
    const ratios = new Map<number, number>();
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const n = Number((e.target as HTMLElement).dataset.page);
          ratios.set(n, e.isIntersecting ? e.intersectionRatio : 0);
        }
        let best = 1;
        let bestR = -1;
        for (const [n, r] of ratios) {
          if (r > bestR) {
            bestR = r;
            best = n;
          }
        }
        setCurrentPage(best);
      },
      { root, threshold: [0, 0.25, 0.5, 0.75, 1] },
    );
    root.querySelectorAll('[data-page]').forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, [pdf]);

  useEffect(() => setPageInput(String(currentPage)), [currentPage]);
  // Editable zoom percent. The field mirrors `scale`; typing a value (Enter/blur)
  // sets an explicit zoom instead of the fit-to-width auto-resize on the % button.
  const [zoomInput, setZoomInput] = useState('120');
  useEffect(() => setZoomInput(String(Math.round(scale * 100))), [scale]);
  function applyZoom(): void {
    const n = parseInt(zoomInput, 10);
    if (Number.isFinite(n)) setScale(Math.max(0.4, Math.min(3, n / 100)));
    else setZoomInput(String(Math.round(scale * 100)));
  }

  // The in-page top coordinate (PDF user space, origin bottom-left) a destination
  // targets, if it pins one. The destination array is [pageRef, /Type, ...args];
  // the Y arg's position depends on the fit type (PDF spec §12.3.2.2):
  //   /XYZ  left top zoom          → top is arg[1]  (explicit[3])
  //   /FitH top                    → top is arg[0]  (explicit[2])
  //   /FitBH top                   → top is arg[0]  (explicit[2])
  //   /FitR left bottom right top  → top is arg[3]  (explicit[5])
  //   /Fit /FitB /FitV /FitBV      → no Y → jump to the page top
  // A pinned coord may be null ("retain current"); treat that as no-Y too.
  // Missing any of these (the old code only knew XYZ/FitH/FitBH) made refs whose
  // links use another fit type land at the page top instead of the ref line.
  function destTop(explicit: unknown[]): number | undefined {
    const spec = explicit[1];
    const name =
      spec !== null && typeof spec === 'object' && 'name' in spec
        ? (spec as { name?: string }).name
        : typeof spec === 'string'
          ? spec
          : undefined;
    const num = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined);
    if (name === 'XYZ') return num(explicit[3]);
    if (name === 'FitH' || name === 'FitBH') return num(explicit[2]);
    if (name === 'FitR') return num(explicit[5]);
    return undefined;
  }

  async function goToDest(dest: string | unknown[]): Promise<void> {
    if (pdf === null) return;
    let explicit: unknown = dest;
    // A named destination resolves to an explicit array. Resolve recursively in
    // case getDestination hands back another name (rare, but possible).
    if (typeof dest === 'string') explicit = await pdf.getDestination(dest);
    if (!Array.isArray(explicit) || explicit.length === 0) return;
    try {
      // The first element identifies the target page. Normally a Ref
      // ({num,gen}) → getPageIndex; but some producers encode it as a bare
      // 0-based page index (a number). Handle both — mis-shaped page refs were
      // the reason ref links jumped to the wrong page.
      const target = explicit[0];
      let pageNum: number;
      if (typeof target === 'number' && Number.isInteger(target)) {
        pageNum = target + 1;
      } else if (target !== null && typeof target === 'object') {
        pageNum = (await pdf.getPageIndex(target as { num: number; gen: number })) + 1;
      } else {
        return; // unresolvable page reference
      }
      if (pageNum < 1 || pageNum > pdf.numPages) return;
      // Convert the destination's PDF-space top into a pixel offset from the page's
      // top edge at the current scale, so we land on the exact section — not just
      // the page top.
      let yOffset = 0;
      const top = destTop(explicit);
      if (top !== undefined) {
        const page = await pdf.getPage(pageNum);
        const vp = page.getViewport({ scale, rotation: (page.rotate + rotation) % 360 });
        yOffset = Math.max(0, vp.convertToViewportPoint(0, top)[1]);
      }
      scrollToPage(pageNum, yOffset);
    } catch {
      /* unresolved dest */
    }
  }

  function gotoPage(n: number): void {
    if (pdf === null) return;
    const clamped = Math.max(1, Math.min(pdf.numPages, n));
    scrollToPage(clamped);
  }

  function fitWidth(): void {
    const el = scrollRef.current;
    if (el === null || pdf === null) return;
    void pdf.getPage(1).then((page) => {
      const base = page.getViewport({ scale: 1, rotation: (page.rotate + rotation) % 360 });
      const avail = el.clientWidth - 32;
      if (avail > 0) setScale(Math.max(0.4, Math.min(3, avail / base.width)));
    });
  }

  // Fit the WHOLE page (both axes) into the scroll viewport — the counterpart of
  // fit-width for reading a full page at a glance (#321). The resulting scale
  // persists like any other zoom choice.
  function fitPage(): void {
    const el = scrollRef.current;
    if (el === null || pdf === null) return;
    void pdf.getPage(1).then((page) => {
      const base = page.getViewport({ scale: 1, rotation: (page.rotate + rotation) % 360 });
      const availW = el.clientWidth - 32;
      const availH = el.clientHeight - 32;
      if (availW > 0 && availH > 0) {
        setScale(Math.max(0.4, Math.min(3, Math.min(availW / base.width, availH / base.height))));
      }
    });
  }

  // Remember the chosen zoom for the next PDF opened (#321).
  useEffect(() => {
    savePdfScale(scale);
  }, [scale]);

  // Remember the chosen view rotation the same way (#321).
  useEffect(() => {
    savePdfRotation(rotation);
  }, [rotation]);

  // Incremental search — debounce the input so the counter and highlight marks
  // update as you type (Enter still triggers an immediate run via the input).
  useEffect(() => {
    if (pdf === null) return;
    const id = window.setTimeout(() => void runSearch(), 300);
    return () => window.clearTimeout(id);
    // runSearch intentionally omitted (redefined each render); term/pdf drive it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term, pdf]);

  // Scroll so page `n`'s top (plus an optional in-page `yOffset`) sits just under
  // the toolbar. The target is an ABSOLUTE content offset (invariant to the current
  // scroll position), computed from the page's rect within the container's scroll
  // frame — so an in-page offset can be added.
  //
  // The jump is INSTANT (behavior:'auto'), like Zotero — not a smooth animation.
  // Smooth scrolling animates through every intermediate page, which (a) reads as
  // "scrolling page by page" and (b) lazy-renders each page it passes, so their
  // heights shift versus the reserved placeholders and the accumulated drift lands
  // the scroll on the WRONG page. An instant jump skips the intermediate pages
  // entirely (they keep their reserved height → no drift → correct page).
  //
  // We still settle: once the target vicinity renders, pages ABOVE that were still
  // reserving an estimated height can shift the target, so recompute and re-jump
  // (instantly) a few times until the layout stabilises.
  function scrollToPage(n: number, yOffset = 0): void {
    const container = scrollRef.current;
    if (container === null) return;
    const targetTop = (): number | null => {
      const el = container.querySelector(`[data-page="${n}"]`);
      if (!(el instanceof HTMLElement)) return null;
      const off = el.getBoundingClientRect().top - container.getBoundingClientRect().top;
      return Math.max(0, container.scrollTop + off + yOffset - 8);
    };
    const first = targetTop();
    if (first === null) return;
    container.scrollTo({ top: first, behavior: 'auto' });
    let last = first;
    let tries = 0;
    const settle = (): void => {
      tries += 1;
      const t = targetTop();
      if (t !== null && Math.abs(t - last) > 3) {
        last = t;
        container.scrollTo({ top: t, behavior: 'auto' });
      }
      if (tries < 8) window.setTimeout(settle, 90);
    };
    window.setTimeout(settle, 100);
  }

  async function runSearch(): Promise<void> {
    const trimmed = term.trim();
    const q = trimmed.toLowerCase();
    setQuery(trimmed);
    if (pdf === null || q === '') {
      setMatches([]);
      setMatchPos(0);
      return;
    }
    // A monotonically-increasing sequence lets an in-flight scan bail the moment a
    // newer query supersedes it (incremental search fires this on every keystroke).
    const seq = (searchSeq.current += 1);
    const hits: { page: number; occ: number }[] = [];
    for (let n = 1; n <= pdf.numPages; n += 1) {
      let items = pageItemsRef.current.get(n);
      if (items === undefined) {
        const page = await pdf.getPage(n);
        const content = await page.getTextContent();
        items = content.items.map((it) => ('str' in it ? it.str.toLowerCase() : ''));
        pageItemsRef.current.set(n, items);
      }
      if (searchSeq.current !== seq) return; // superseded mid-scan
      let occ = 0;
      for (const s of items) {
        let i = s.indexOf(q, 0);
        while (i !== -1) {
          hits.push({ page: n, occ });
          occ += 1;
          i = s.indexOf(q, i + q.length);
        }
      }
    }
    if (searchSeq.current !== seq) return;
    setMatches(hits);
    setMatchPos(0);
    if (hits.length > 0) goToMatch(hits[0]);
  }

  // Scroll to a specific occurrence: jump to its page, then once that page paints
  // its highlight <mark>s, centre the occ-th one and flag it `.current` for a
  // distinct active-match tint (Zotero/Chrome-style current vs. other matches).
  function goToMatch(m: { page: number; occ: number }): void {
    const container = scrollRef.current;
    if (container === null) return;
    scrollToPage(m.page);
    let tries = 0;
    const land = (): void => {
      tries += 1;
      const pageEl = container.querySelector(`[data-page="${m.page}"]`);
      const marks = pageEl?.querySelectorAll<HTMLElement>('mark.pdfjs-mark');
      if (marks !== undefined && marks.length > m.occ) {
        container.querySelectorAll('mark.pdfjs-mark.current').forEach((el) => el.classList.remove('current'));
        const mk = marks[m.occ];
        mk.classList.add('current');
        mk.scrollIntoView({ block: 'center', behavior: 'auto' });
      } else if (tries < 12) {
        window.setTimeout(land, 100);
      }
    };
    window.setTimeout(land, 140);
  }

  function stepMatch(delta: number): void {
    if (matches.length === 0) return;
    const next = (matchPos + delta + matches.length) % matches.length;
    setMatchPos(next);
    goToMatch(matches[next]);
  }

  function saveSelection(): void {
    // Prefer the selection captured on mousedown; fall back to the live selection.
    const sel = pendingSelRef.current || (window.getSelection()?.toString().trim() ?? '');
    if (sel === '' || onSaveSelection === undefined) return;
    onSaveSelection(sel);
    pendingSelRef.current = '';
    setSaved(true);
    window.setTimeout(() => setSaved(false), 1500);
  }

  // Bind the reference id and hand to the store; returns the new annotation id.
  // A useCallback (not a plain function) so the memoised PageView gets a stable
  // `onCreate` prop across viewer re-renders (#311).
  const createAnnotation = useCallback(
    (a: NewAnno): string => {
      if (referenceId === undefined) return '';
      return addAnno({ ...a, referenceId });
    },
    [referenceId, addAnno],
  );

  // Turn the current text selection into a highlight/underline. The selection's
  // client rects (screen px) are grouped by the page they fall on, then each rect
  // maps to PDF points via that page's own box + the current scale — so the
  // stored geometry is scale-independent. Multi-line selections yield several
  // rects; a selection spanning pages yields one annotation per page.
  function commitTextSelection(type: 'highlight' | 'underline'): void {
    if (referenceId === undefined) return;
    const sel = window.getSelection();
    const container = scrollRef.current;
    if (sel === null || sel.isCollapsed || sel.rangeCount === 0 || container === null) return;
    const text = sel.toString().trim();
    const pageEls = Array.from(container.querySelectorAll<HTMLElement>('[data-page]'));
    const byPage = new Map<number, number[][]>();
    for (let ri = 0; ri < sel.rangeCount; ri += 1) {
      for (const r of Array.from(sel.getRangeAt(ri).getClientRects())) {
        if (r.width < 1 || r.height < 1) continue;
        const cx = r.left + r.width / 2;
        const cy = r.top + r.height / 2;
        const pageEl = pageEls.find((el) => {
          const b = el.getBoundingClientRect();
          return cx >= b.left && cx <= b.right && cy >= b.top && cy <= b.bottom;
        });
        if (pageEl === undefined) continue;
        const pIdx = Number(pageEl.dataset.page) - 1;
        const b = pageEl.getBoundingClientRect();
        // b is the ROTATED on-screen box; annotation geometry is unrotated (#321).
        const uw = rotation % 180 !== 0 ? b.height : b.width;
        const uh = rotation % 180 !== 0 ? b.width : b.height;
        const [x1, yTop] = viewToPdfPt(r.left - b.left, r.top - b.top, uw, uh, scale, rotation);
        const [x2, yBot] = viewToPdfPt(r.right - b.left, r.bottom - b.top, uw, uh, scale, rotation);
        const rect = [Math.min(x1, x2), Math.min(yTop, yBot), Math.max(x1, x2), Math.max(yTop, yBot)];
        const list = byPage.get(pIdx) ?? [];
        list.push(rect);
        byPage.set(pIdx, list);
      }
    }
    for (const [pIdx, rects] of byPage) {
      createAnnotation({ type, color: annoColor, pageIndex: pIdx, text, position: { pageIndex: pIdx, rects }, tags: [] });
    }
    sel.removeAllRanges();
  }

  // Toggle a tool; picking highlight/underline while text is selected acts on it
  // immediately (Zotero-style), otherwise arms the tool for the next interaction.
  function pickTool(next: Tool): void {
    setSelectedAnno(null);
    if ((next === 'highlight' || next === 'underline') && !(window.getSelection()?.isCollapsed ?? true)) {
      commitTextSelection(next);
      return;
    }
    setTool((cur) => (cur === next ? null : next));
  }

  const canAnnotate = referenceId !== undefined && pdf !== null;

  // Tool button tooltip: localized label + its single-key shortcut (#316).
  function toolTitle(labelKey: string, key: string): string {
    return t('read.annToolTitle').replace('{label}', t(labelKey)).replace('{key}', key);
  }

  // Single-key tool switching (#316). Window-level so it works wherever focus
  // sits in the viewer, but inert in text fields (find box, page input, comment
  // textarea, contenteditable notes) and with modifiers held, so zoom/undo and
  // native typing keep their keys. The refs (tocWRef pattern) let the listener
  // bind once per canAnnotate instead of every render.
  const toolRef = useRef(tool);
  toolRef.current = tool;
  const pickToolRef = useRef(pickTool);
  pickToolRef.current = pickTool;
  useEffect(() => {
    if (!canAnnotate) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
      const el = e.target as HTMLElement;
      if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable) return;
      if (e.key === 'Escape') {
        // Back to select only when a tool is armed — otherwise the context menu
        // / annotation editor consume their own Escape.
        if (toolRef.current !== null) {
          e.preventDefault();
          setTool(null);
        }
        return;
      }
      const next = TOOL_SHORTCUTS[e.key.toLowerCase()];
      if (next === undefined) return;
      e.preventDefault();
      pickToolRef.current(next);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [canAnnotate]);

  // The annotation's anchor in unrotated PDF space: the top-left corner of its
  // first rect, or the highest point of its first ink path. Jump targeting maps
  // it through the current rotation (#321).
  function annoAnchor(a: Annotation): [number, number] | undefined {
    const r = a.position.rects?.[0];
    if (r !== undefined) return [Math.min(r[0], r[2]), Math.max(r[1], r[3])];
    const p = a.position.paths?.[0];
    if (p !== undefined && p.length >= 2) {
      let bx = p[0];
      let by = p[1];
      for (let i = 2; i + 1 < p.length; i += 2) {
        if (p[i + 1] > by) {
          by = p[i + 1];
          bx = p[i];
        }
      }
      return [bx, by];
    }
    return undefined;
  }

  // Jump to an annotation from the panel list: scroll its page + in-page offset
  // into view and select it (opens its editor / highlights the row).
  function goToAnnotation(a: Annotation): void {
    setSelectedAnno(a.id);
    const fp = pageDims[a.pageIndex];
    const anchor = annoAnchor(a);
    let yOffset = 0;
    if (fp !== undefined && anchor !== undefined) {
      // pageDims is the ROTATED footprint; the geometry mapping wants unrotated.
      const uw = rotation % 180 !== 0 ? fp.h : fp.w;
      const uh = rotation % 180 !== 0 ? fp.w : fp.h;
      yOffset = Math.max(0, pdfPtToView(anchor[0], anchor[1], uw, uh, scale, rotation)[1] - 40);
    }
    scrollToPage(a.pageIndex + 1, yOffset);
  }

  // Right-click menu. Annotation actions apply only when the selection is live in
  // the PRIMARY pane (the mirror is read-only), so we gate them on that. The split
  // toggle is always offered.
  function openContextMenu(e: React.MouseEvent): void {
    const sel = window.getSelection();
    const text = sel?.toString().trim() ?? '';
    const inPrimary =
      sel !== null && !sel.isCollapsed && sel.anchorNode !== null && (scrollRef.current?.contains(sel.anchorNode) ?? false);
    const onSel = canAnnotate && inPrimary && text !== '';
    // Only pre-empt the native menu when we have something to offer (always true:
    // the split toggle), and snapshot the selection so a menu click can't lose it.
    e.preventDefault();
    if (onSel) pendingSelRef.current = text;
    setSelectedAnno(null);
    setMenu({ x: e.clientX, y: e.clientY, onSel, text });
  }

  // Menu action: copy the selected text (basic clipboard action, available on any
  // selection — not gated on annotate permission).
  function menuCopy(): void {
    const text = menu?.text ?? '';
    if (text !== '') void navigator.clipboard?.writeText(text).catch(() => undefined);
    setMenu(null);
  }

  // Menu action: highlight/underline the snapshotted selection.
  function menuAnnotate(type: 'highlight' | 'underline'): void {
    commitTextSelection(type);
    setMenu(null);
  }

  // Close the menu on any scroll / resize / Escape / outside pointer. A pointer
  // inside the menu is ignored so an item click reaches its handler first.
  useEffect(() => {
    if (menu === null) return;
    const close = (): void => setMenu(null);
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setMenu(null);
    };
    const onDown = (e: PointerEvent): void => {
      if (menuRef.current?.contains(e.target as Node) ?? false) return;
      setMenu(null);
    };
    window.addEventListener('resize', close);
    window.addEventListener('keydown', onKey);
    window.addEventListener('pointerdown', onDown, { capture: true });
    const sc = scrollRef.current;
    sc?.addEventListener('scroll', close);
    return () => {
      window.removeEventListener('resize', close);
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('pointerdown', onDown, { capture: true } as EventListenerOptions);
      sc?.removeEventListener('scroll', close);
    };
  }, [menu]);

  // Keep the context menu on-screen near a viewport edge (measure then clamp).
  useLayoutEffect(() => {
    if (menu === null) return;
    const el = menuRef.current;
    if (el === null) return;
    const r = el.getBoundingClientRect();
    const { x, y } = clampToViewport(menu.x, menu.y, r.width, r.height);
    el.style.left = `${x}px`;
    el.style.top = `${y}px`;
  }, [menu]);

  // Stabilised PageView props (#311): inline arrows here would change identity on
  // every viewer render (each scroll-driven page-indicator tick, each annotation
  // store update), silently defeating the memo. goToDest closes over pdf/scale
  // and is redefined per render, so it gets a latest-ref trampoline — a fixed
  // prop identity that always calls the current closure.
  const destRef = useRef(goToDest);
  destRef.current = goToDest;
  const onDest = useCallback((d: string | unknown[]) => void destRef.current(d), []);
  const onRemoveAnno = useCallback(
    (id: string) => {
      removeAnno(id);
      setSelectedAnno(null);
    },
    [removeAnno],
  );
  const onToolDone = useCallback(() => setTool(null), []);

  // The page list for one pane. The mirror pane passes readOnly so its overlays
  // are visible but not editable (annotation happens in the primary pane).
  const pageList = (readOnly: boolean): JSX.Element[] =>
    pdf === null
      ? []
      : Array.from({ length: pdf.numPages }, (_, i) => (
          <PageView
            key={i + 1}
            pdf={pdf}
            pageNum={i + 1}
            scale={scale}
            rotation={rotation}
            query={query}
            dim={pageDims[i]}
            onLink={openLink}
            onDest={onDest}
            annos={annosByPage.get(i) ?? NO_ANNOS}
            tool={tool}
            color={annoColor}
            selectedId={selectedAnno}
            onCreate={createAnnotation}
            onSelect={setSelectedAnno}
            onUpdate={updateAnno}
            onRemove={onRemoveAnno}
            onToolDone={onToolDone}
            onImageToNote={readOnly ? undefined : onImageToNote}
            readOnly={readOnly}
          />
        ));

  if (err) return <div className="muted region-pad">{t('read.pdfRenderFailed')}</div>;

  return (
    <div className="pdfjs-view" ref={viewRef}>
      <div className="pdfjs-toolbar">
        {pdf !== null && (
          <button
            className={`pdfjs-zoom${showToc ? ' active' : ''}`}
            title={t('read.pdfToc')}
            onClick={() => {
              // Opening: prefer Annotations if the PDF has any, else the outline
              // when present, else thumbnails.
              if (!showToc) {
                setPanelTab(refAnnos.length > 0 ? 'annos' : outline.length > 0 ? 'outline' : 'thumbs');
              }
              setShowToc((v) => !v);
            }}
          >
            <Icon name="menu" />
          </button>
        )}
        {pdf !== null && (
          <div className="pdfjs-pagenav">
            <button className="pdfjs-zoom" title={t('read.pdfPrevPage')} disabled={currentPage <= 1} onClick={() => gotoPage(currentPage - 1)}>
              <Icon name="chevron-left" />
            </button>
            <input
              className="pdfjs-page-input"
              value={pageInput}
              spellCheck={false}
              inputMode="numeric"
              onChange={(e) => setPageInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  const n = parseInt(pageInput, 10);
                  if (Number.isFinite(n)) gotoPage(n);
                }
              }}
            />
            <span className="muted small">/ {pdf.numPages}</span>
            <button className="pdfjs-zoom" title={t('read.pdfNextPage')} disabled={currentPage >= pdf.numPages} onClick={() => gotoPage(currentPage + 1)}>
              <Icon name="chevron-right" />
            </button>
          </div>
        )}
        {canAnnotate && (
          <>
            {/* Centre the annotation tools between the left (nav) and right
                (find/zoom) clusters — a spacer on each side. */}
            <span className="spacer" />
            <div className="pdfjs-anno-tools">
            <button className={`pdfjs-zoom${tool === 'highlight' ? ' active' : ''}`} title={toolTitle('read.annHighlight', 'H')} onClick={() => pickTool('highlight')}>
              <Icon name="highlight" />
            </button>
            <button className={`pdfjs-zoom${tool === 'underline' ? ' active' : ''}`} title={toolTitle('read.annUnderline', 'U')} onClick={() => pickTool('underline')}>
              <Icon name="underline" />
            </button>
            <button className={`pdfjs-zoom${tool === 'note' ? ' active' : ''}`} title={toolTitle('read.annNote', 'N')} onClick={() => pickTool('note')}>
              <Icon name="note" />
            </button>
            <button className={`pdfjs-zoom${tool === 'image' ? ' active' : ''}`} title={toolTitle('read.annArea', 'A')} onClick={() => pickTool('image')}>
              <Icon name="square" />
            </button>
            <button className={`pdfjs-zoom${tool === 'ink' ? ' active' : ''}`} title={toolTitle('read.annDraw', 'I')} onClick={() => pickTool('ink')}>
              <Icon name="pen" />
            </button>
            {tool !== null && (
              <div className="pdfjs-anno-palette" title={t('read.annColor')}>
                {ANNOTATION_COLORS.map((c) => (
                  <button
                    key={c}
                    className={`pdfjs-anno-swatch${annoColor === c ? ' active' : ''}`}
                    style={{ background: c }}
                    onClick={() => setAnnoColor(c)}
                  />
                ))}
              </div>
            )}
            </div>
          </>
        )}
        <span className="spacer" />
        <div className="pdfjs-find">
          <input
            className="pdfjs-find-input"
            value={term}
            placeholder={t('read.pdfFind')}
            spellCheck={false}
            onChange={(e) => setTerm(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void runSearch();
            }}
          />
          {query !== '' && (
            <>
              <span className="muted small pdfjs-find-count">
                {matches.length > 0 ? `${matchPos + 1}/${matches.length}` : '0'}
              </span>
              <button className="pdfjs-zoom" title={t('read.browserBack')} disabled={matches.length === 0} onClick={() => stepMatch(-1)}>
                <Icon name="chevron-left" />
              </button>
              <button className="pdfjs-zoom" title={t('read.browserForward')} disabled={matches.length === 0} onClick={() => stepMatch(1)}>
                <Icon name="chevron-right" />
              </button>
            </>
          )}
        </div>
        {onSaveSelection !== undefined && (
          <button
            className="pdfjs-zoom pdfjs-tonotes"
            title={t('read.copyToNotesHint')}
            onMouseDown={(e) => {
              // Keep the text selection alive through the click, and snapshot it.
              e.preventDefault();
              pendingSelRef.current = window.getSelection()?.toString().trim() ?? '';
            }}
            onClick={saveSelection}
          >
            {saved ? t('read.copiedToNotes') : t('read.copyToNotes')}
          </button>
        )}
        <button
          className={`pdfjs-zoom${tool === 'hand' ? ' active' : ''}`}
          title={t('read.panTool')}
          onClick={() => pickTool('hand')}
        >
          <Icon name="hand" />
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomOut')} onClick={() => setScale((s) => Math.max(0.4, s - 0.2))}>
          <Icon name="minus" />
        </button>
        <div className="pdfjs-zoominput">
          <input
            className="pdfjs-page-input"
            value={zoomInput}
            spellCheck={false}
            inputMode="numeric"
            title={t('read.zoomLevel')}
            onChange={(e) => setZoomInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') applyZoom();
            }}
            onBlur={applyZoom}
          />
          <span className="muted small">%</span>
        </div>
        <button className="pdfjs-zoom" title={t('read.zoomIn')} onClick={() => setScale((s) => Math.min(3, s + 0.2))}>
          <Icon name="plus" />
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomFit')} onClick={fitWidth}>
          <Icon name="expand" />
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomFitPage')} onClick={fitPage}>
          <Icon name="fit-page" />
        </button>
        <button
          className="pdfjs-zoom"
          title={t('read.rotateCw')}
          onClick={() => setRotation((r) => (r + 90) % 360)}
        >
          <Icon name="rotate-cw" />
        </button>
        {docUrl !== undefined && docUrl !== '' && (
          <button className="pdfjs-zoom" title={t('read.openUrl')} onClick={() => openLink(docUrl)}>
            <Icon name="external" />
          </button>
        )}
        {onToggleDetails !== undefined && (
          <button
            className={`pdfjs-zoom${detailsOpen ? ' active' : ''}`}
            title={detailsOpen ? t('read.hideDetails') : t('read.showDetails')}
            onClick={onToggleDetails}
          >
            <Icon name={detailsOpen ? 'chevron-right' : 'chevron-left'} />
          </button>
        )}
      </div>
      <div className="pdfjs-body">
        {showToc && pdf !== null && (
          <>
            <div className="pdfjs-toc" style={{ width: tocW }}>
              <TabStrip
                className="pdfjs-panel-tabs"
                ariaLabel={t('read.pdfDocument')}
                active={panelTab}
                onSelect={(id) => setPanelTab(id as 'outline' | 'thumbs' | 'annos')}
                tabs={[
                  ...(canAnnotate ? [{ id: 'annos', label: t('read.pdfAnnotations') }] : []),
                  ...(outline.length > 0 ? [{ id: 'outline', label: t('read.pdfOutline') }] : []),
                  { id: 'thumbs', label: t('read.pdfThumbs') },
                ]}
              />
              <div className="pdfjs-panel-body scroll">
                {panelTab === 'annos' && canAnnotate ? (
                  <AnnotationList annos={refAnnos} selectedId={selectedAnno} onGo={goToAnnotation} />
                ) : panelTab === 'outline' && outline.length > 0 ? (
                  <OutlineList nodes={outline} onGo={(d) => void goToDest(d)} depth={0} />
                ) : (
                  <ThumbList pdf={pdf} current={currentPage} rotation={rotation} onGo={(n) => scrollToPage(n)} />
                )}
              </div>
            </div>
            <ResizeHandle
              onResize={(dx) => {
                const raw = tocWRef.current + dx;
                // Dragged past the min toward the edge → auto-collapse the panel
                // (reopen with the ☰ button).
                if (raw < 110) {
                  setShowToc(false);
                  return;
                }
                const n = Math.max(140, Math.min(480, raw));
                tocWRef.current = n;
                setTocW(n);
                try {
                  localStorage.setItem('termipod.read.pdfTocW', String(n));
                } catch {
                  /* ignore */
                }
              }}
            />
          </>
        )}
        <div
          className={`pdfjs-panes${split !== 'none' ? ` split-${split}` : ''}`}
          onContextMenu={openContextMenu}
        >
          <div
            ref={scrollRef}
            className={`pdfjs-scroll scroll${tool !== null ? ` tool-${tool}` : ''}`}
            tabIndex={0}
            role="region"
            aria-label={t('read.pdfDocument')}
            onPointerDown={(e) => {
              // Hand tool: drag anywhere to pan the scroll view (Acrobat/Zotero).
              // Window listeners, not setPointerCapture — unreliable on WebView2
              // (same reason as the draw surface, feedback_pointer_capture_webview2).
              if (tool !== 'hand' || e.button !== 0) return;
              const el = scrollRef.current;
              if (el === null) return;
              const startX = e.clientX;
              const startY = e.clientY;
              const startL = el.scrollLeft;
              const startT = el.scrollTop;
              let moved = false;
              el.classList.add('panning');
              const move = (ev: PointerEvent): void => {
                if (Math.abs(ev.clientX - startX) + Math.abs(ev.clientY - startY) > 4) moved = true;
                el.scrollLeft = startL - (ev.clientX - startX);
                el.scrollTop = startT - (ev.clientY - startY);
              };
              const up = (): void => {
                window.removeEventListener('pointermove', move);
                window.removeEventListener('pointerup', up);
                el.classList.remove('panning');
                if (moved) {
                  // A drag-pan still ends in a click event — swallow it so a pan
                  // released over a link doesn't open it. The timeout drops the
                  // listener when the release landed off-element (no click follows).
                  const swallow = (ev: Event): void => {
                    ev.stopPropagation();
                    ev.preventDefault();
                  };
                  el.addEventListener('click', swallow, { capture: true, once: true });
                  window.setTimeout(() => el.removeEventListener('click', swallow, { capture: true }), 150);
                }
              };
              window.addEventListener('pointermove', move);
              window.addEventListener('pointerup', up);
            }}
            onWheel={(e) => {
              // Ctrl/Cmd+wheel zooms (trackpad pinch also emits ctrlKey), like a
              // browser/PDF viewer; a plain wheel scrolls normally.
              if (e.ctrlKey || e.metaKey) {
                e.preventDefault();
                setScale((s) => Math.max(0.4, Math.min(3, s - Math.sign(e.deltaY) * 0.1)));
              }
            }}
            onKeyDown={(e) => {
              // Zoom keys (#316): Ctrl/Cmd +/-/0, matching browsers + the terminal.
              // Arrow/PageUp/Down/Home/End scroll natively (focusable scroll region).
              if (!(e.ctrlKey || e.metaKey)) return;
              if (e.key === '=' || e.key === '+') {
                e.preventDefault();
                setScale((s) => Math.min(3, s + 0.1));
              } else if (e.key === '-') {
                e.preventDefault();
                setScale((s) => Math.max(0.4, s - 0.1));
              } else if (e.key === '0') {
                e.preventDefault();
                fitWidth();
              } else if ((e.key === 'z' || e.key === 'Z') && !e.shiftKey) {
                // Undo the last annotation change (#322) — but let a text field
                // (a comment textarea) keep its own native undo.
                const tag = (e.target as HTMLElement).tagName;
                if (tag !== 'TEXTAREA' && tag !== 'INPUT') {
                  e.preventDefault();
                  undoAnno();
                }
              }
            }}
            onMouseUp={() => {
              // Selection-driven tools commit on mouse-up, once the drag-select ends.
              if (tool === 'highlight' || tool === 'underline') commitTextSelection(tool);
            }}
            onClick={() => {
              // A click anywhere on the page (text/canvas) deselects the current
              // annotation, so its editor popover dismisses — matching "click
              // elsewhere to close". Annotation overlay boxes and the editor both
              // stopPropagation, so a click that selects/edits an annotation never
              // reaches here; only true empty-area clicks do.
              if (tool === null) setSelectedAnno(null);
            }}
          >
            {pdf === null && !err && (
              <div className="muted region-pad pdfjs-loading">
                <span>{t('read.loadingPdf')}</span>
                {loadPct > 0 && loadPct < 1 && (
                  <span className="pdfjs-loadbar" aria-hidden>
                    <span className="pdfjs-loadbar-fill" style={{ width: `${Math.round(loadPct * 100)}%` }} />
                  </span>
                )}
              </div>
            )}
            {pageList(false)}
          </div>
          {split !== 'none' && pdf !== null && (
            <div className="pdfjs-scroll scroll pdfjs-mirror">{pageList(true)}</div>
          )}
        </div>
      </div>
      {menu !== null && (
        <div
          ref={menuRef}
          className="pdfjs-ctxmenu"
          style={{ left: menu.x, top: menu.y }}
          onContextMenu={(e) => e.preventDefault()}
        >
          {menu.text !== '' && (
            <>
              <button className="pdfjs-ctx-item" onMouseDown={(e) => e.preventDefault()} onClick={menuCopy}>
                <Icon name="copy" size={14} /> {t('read.ctxCopy')}
              </button>
              <div className="pdfjs-ctx-sep" />
            </>
          )}
          {menu.onSel && (
            <>
              <button className="pdfjs-ctx-item" onMouseDown={(e) => e.preventDefault()} onClick={() => menuAnnotate('highlight')}>
                <Icon name="highlight" size={14} /> {t('read.annHighlight')}
              </button>
              <button className="pdfjs-ctx-item" onMouseDown={(e) => e.preventDefault()} onClick={() => menuAnnotate('underline')}>
                <Icon name="underline" size={14} /> {t('read.annUnderline')}
              </button>
              {onSaveSelection !== undefined && (
                <button
                  className="pdfjs-ctx-item"
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => {
                    saveSelection();
                    setMenu(null);
                  }}
                >
                  <Icon name="note" size={14} /> {t('read.ctxToNotes')}
                </button>
              )}
              <div className="pdfjs-ctx-sep" />
            </>
          )}
          <button
            className={`pdfjs-ctx-item${split === 'none' ? ' active' : ''}`}
            onClick={() => {
              setSplit('none');
              setMenu(null);
            }}
          >
            {t('read.splitNone')}
          </button>
          <button
            className={`pdfjs-ctx-item${split === 'vertical' ? ' active' : ''}`}
            onClick={() => {
              setSplit('vertical');
              setMenu(null);
            }}
          >
            {t('read.splitVertical')}
          </button>
          <button
            className={`pdfjs-ctx-item${split === 'horizontal' ? ' active' : ''}`}
            onClick={() => {
              setSplit('horizontal');
              setMenu(null);
            }}
          >
            {t('read.splitHorizontal')}
          </button>
        </div>
      )}
    </div>
  );
}
