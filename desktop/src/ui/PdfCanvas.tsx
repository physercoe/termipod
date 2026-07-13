import { useEffect, useRef, useState } from 'react';
import * as pdfjsLib from 'pdfjs-dist';
import { TextLayer } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { useOpenLink } from './OpenLinkContext';
import { ResizeHandle } from './ResizeHandle';

function escapeHtml(s: string): string {
  return s.replace(/[&<>]/g, (c) => (c === '&' ? '&amp;' : c === '<' ? '&lt;' : '&gt;'));
}

/// A self-contained PDF viewer built on bundled pdf.js — no CDN, no native
/// browser plugin. Replaces the `<iframe src=blob>` viewer, which WebView2
/// (Windows/Edge) refused to render ("此页面已被 Microsoft Edge 阻止") and whose
/// in-PDF links hijacked the whole SPA.
///
/// Each page renders to a canvas we own (works on every platform) plus a pdf.js
/// **text layer** — invisible positioned spans over the canvas that make the text
/// selectable, searchable, and copyable into notes. The link layer is built from
/// `page.getAnnotations()` so a clicked link routes through `useOpenLink()` into
/// the in-app browser tab instead of navigating the app. Pages render lazily
/// (IntersectionObserver) so a long PDF doesn't rasterise every page up front.

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

function PageView({
  pdf,
  pageNum,
  scale,
  query,
  dim,
  onLink,
  onDest,
}: {
  pdf: PDFDocumentProxy;
  pageNum: number;
  scale: number;
  query: string;
  dim?: { w: number; h: number };
  onLink: (url: string) => void;
  onDest: (dest: string | unknown[]) => void;
}): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const textRef = useRef<HTMLDivElement | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const textDivsRef = useRef<HTMLElement[]>([]);
  const [visible, setVisible] = useState(pageNum === 1); // first page eagerly
  const [size, setSize] = useState<{ w: number; h: number } | null>(null);
  const [links, setLinks] = useState<LinkBox[]>([]);

  // Reveal the page a little before it scrolls into view, then keep it rendered.
  useEffect(() => {
    const el = wrapRef.current;
    if (el === null || visible) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          io.disconnect();
        }
      },
      { rootMargin: '400px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [visible]);

  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    let renderTask: ReturnType<PDFPageProxy['render']> | null = null;
    let textLayer: TextLayer | null = null;
    void (async () => {
      const page = await pdf.getPage(pageNum);
      if (cancelled) return;
      const viewport = page.getViewport({ scale });
      const canvas = canvasRef.current;
      if (canvas === null) return;
      const ctx = canvas.getContext('2d');
      if (ctx === null) return;
      const ratio = window.devicePixelRatio || 1;
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

      // Text layer — selectable/searchable spans over the canvas.
      const textDiv = textRef.current;
      if (textDiv !== null) {
        textDiv.replaceChildren();
        textLayer = new TextLayer({ textContentSource: page.streamTextContent(), container: textDiv, viewport });
        try {
          await textLayer.render();
          if (!cancelled) textDivsRef.current = textLayer.textDivs;
        } catch {
          /* text layer is best-effort */
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
    };
  }, [pdf, pageNum, scale, visible]);

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

  return (
    <div ref={wrapRef} className="pdfjs-page" data-page={pageNum} style={style}>
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
    </div>
  );
}

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

export function PdfCanvas({
  data,
  fileName,
  onSaveSelection,
}: {
  data: ArrayBuffer;
  fileName?: string;
  onSaveSelection?: (text: string) => void;
}): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pageTextRef = useRef<Map<number, string>>(new Map());
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null);
  const [err, setErr] = useState(false);
  const [scale, setScale] = useState(1.2);
  const [term, setTerm] = useState(''); // the live input value
  const [query, setQuery] = useState(''); // the committed search term (drives highlight)
  const [matches, setMatches] = useState<number[]>([]); // page numbers containing the term
  const [matchPos, setMatchPos] = useState(0);
  const [saved, setSaved] = useState(false);
  const [currentPage, setCurrentPage] = useState(1);
  const [pageInput, setPageInput] = useState('1');
  const [outline, setOutline] = useState<OutlineNode[]>([]);
  const [pageDims, setPageDims] = useState<{ w: number; h: number }[]>([]);
  const [showToc, setShowToc] = useState(false);
  const [tocW, setTocW] = useState(() => {
    const v = Number(localStorage.getItem('termipod.read.pdfTocW'));
    return Number.isFinite(v) && v > 0 ? v : 240;
  });
  // The text selected in the PDF, captured on the +notes button's mousedown —
  // before the click can collapse the selection (clicking a button elsewhere in
  // the DOM clears window.getSelection, which is why +notes previously did nothing).
  const pendingSelRef = useRef('');

  useEffect(() => {
    let cancelled = false;
    let doc: PDFDocumentProxy | null = null;
    setPdf(null);
    setErr(false);
    pageTextRef.current = new Map();
    // Copy into a fresh buffer — pdf.js transfers/detaches the ArrayBuffer, which
    // would corrupt a caller that reuses it.
    const bytes = new Uint8Array(data.byteLength);
    bytes.set(new Uint8Array(data));
    void pdfjsLib
      .getDocument({ data: bytes })
      .promise.then((d) => {
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
      setPageDims([]);
      return;
    }
    let alive = true;
    void (async () => {
      const dims: { w: number; h: number }[] = [];
      for (let n = 1; n <= pdf.numPages; n += 1) {
        const page = await pdf.getPage(n);
        if (!alive) return;
        const vp = page.getViewport({ scale });
        dims.push({ w: vp.width, h: vp.height });
      }
      if (alive) setPageDims(dims);
    })();
    return () => {
      alive = false;
    };
  }, [pdf, scale]);

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

  // The in-page top coordinate (PDF user space, origin bottom-left) a destination
  // targets, if it pins one: /XYZ carries [left, top, zoom]; /FitH & /FitBH carry
  // [top]. /Fit, /FitV, etc. have no useful Y → jump to the page top.
  function destTop(explicit: unknown[]): number | undefined {
    const spec = explicit[1];
    const name = spec !== null && typeof spec === 'object' && 'name' in spec ? (spec as { name?: string }).name : undefined;
    if (name === 'XYZ') return typeof explicit[3] === 'number' ? explicit[3] : undefined;
    if (name === 'FitH' || name === 'FitBH') return typeof explicit[2] === 'number' ? explicit[2] : undefined;
    return undefined;
  }

  async function goToDest(dest: string | unknown[]): Promise<void> {
    if (pdf === null) return;
    let explicit: unknown = dest;
    if (typeof dest === 'string') explicit = await pdf.getDestination(dest);
    if (!Array.isArray(explicit) || explicit.length === 0) return;
    try {
      const pageNum = (await pdf.getPageIndex(explicit[0] as { num: number; gen: number })) + 1;
      // Convert the destination's PDF-space top into a pixel offset from the page's
      // top edge at the current scale, so we land on the exact section — not just
      // the page top.
      let yOffset = 0;
      const top = destTop(explicit);
      if (top !== undefined) {
        const page = await pdf.getPage(pageNum);
        const vp = page.getViewport({ scale });
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
      const base = page.getViewport({ scale: 1 });
      const avail = el.clientWidth - 32;
      if (avail > 0) setScale(Math.max(0.4, Math.min(3, avail / base.width)));
    });
  }

  // Scroll so page `n`'s top (plus an optional in-page `yOffset`) sits just under
  // the toolbar. The target is an ABSOLUTE content offset (invariant to the current
  // scroll position), computed from the page's rect within the container's scroll
  // frame — so an in-page offset can be added.
  //
  // A jump can otherwise land short: pages ABOVE the target may still be reserving
  // their true height (lazy render, or pageDims not yet measured), which shifts the
  // target down after the scroll begins. So we settle: recompute the target a few
  // times and re-scroll whenever it drifts, until the layout above has stabilised.
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
    container.scrollTo({ top: first, behavior: 'smooth' });
    let last = first;
    let tries = 0;
    const settle = (): void => {
      tries += 1;
      const t = targetTop();
      if (t !== null && Math.abs(t - last) > 3) {
        last = t;
        container.scrollTo({ top: t, behavior: 'smooth' });
      }
      if (tries < 8) window.setTimeout(settle, 120);
    };
    window.setTimeout(settle, 160);
  }

  async function runSearch(): Promise<void> {
    const q = term.trim().toLowerCase();
    setQuery(term.trim());
    if (pdf === null || q === '') {
      setMatches([]);
      return;
    }
    // Extract + cache each page's plain text once, then match page-level.
    const hits: number[] = [];
    for (let n = 1; n <= pdf.numPages; n += 1) {
      let text = pageTextRef.current.get(n);
      if (text === undefined) {
        const page = await pdf.getPage(n);
        const content = await page.getTextContent();
        text = content.items
          .map((it) => ('str' in it ? it.str : ''))
          .join(' ')
          .toLowerCase();
        pageTextRef.current.set(n, text);
      }
      if (text.includes(q)) hits.push(n);
    }
    setMatches(hits);
    setMatchPos(0);
    if (hits.length > 0) scrollToPage(hits[0]);
  }

  function stepMatch(delta: number): void {
    if (matches.length === 0) return;
    const next = (matchPos + delta + matches.length) % matches.length;
    setMatchPos(next);
    scrollToPage(matches[next]);
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

  if (err) return <div className="muted region-pad">{t('read.pdfRenderFailed')}</div>;

  return (
    <div className="pdfjs-view">
      <div className="pdfjs-toolbar">
        {outline.length > 0 && (
          <button
            className={`pdfjs-zoom${showToc ? ' active' : ''}`}
            title={t('read.pdfToc')}
            onClick={() => setShowToc((v) => !v)}
          >
            <Icon name="menu" />
          </button>
        )}
        <span className="muted small pdfjs-name">{fileName ?? 'PDF'}</span>
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
        <button className="pdfjs-zoom" title={t('read.zoomOut')} onClick={() => setScale((s) => Math.max(0.4, s - 0.2))}>
          <Icon name="minus" />
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomFit')} onClick={fitWidth}>
          {Math.round(scale * 100)}%
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomIn')} onClick={() => setScale((s) => Math.min(3, s + 0.2))}>
          <Icon name="plus" />
        </button>
      </div>
      <div className="pdfjs-body">
        {showToc && outline.length > 0 && (
          <>
            <div className="pdfjs-toc" style={{ width: tocW }}>
              <OutlineList nodes={outline} onGo={(d) => void goToDest(d)} depth={0} />
            </div>
            <ResizeHandle
              onResize={(dx) =>
                setTocW((w) => {
                  const n = Math.max(140, Math.min(480, w + dx));
                  try {
                    localStorage.setItem('termipod.read.pdfTocW', String(n));
                  } catch {
                    /* ignore */
                  }
                  return n;
                })
              }
            />
          </>
        )}
        <div ref={scrollRef} className="pdfjs-scroll scroll">
          {pdf === null && !err && <div className="muted region-pad">{t('read.loadingPdf')}</div>}
          {pdf !== null &&
            Array.from({ length: pdf.numPages }, (_, i) => (
              <PageView
                key={i + 1}
                pdf={pdf}
                pageNum={i + 1}
                scale={scale}
                query={query}
                dim={pageDims[i]}
                onLink={openLink}
                onDest={(d) => void goToDest(d)}
              />
            ))}
        </div>
      </div>
    </div>
  );
}
