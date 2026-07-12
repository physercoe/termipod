import { useEffect, useRef, useState } from 'react';
import * as pdfjsLib from 'pdfjs-dist';
import { TextLayer } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist';
import { useT } from '../i18n';
import { useOpenLink } from './OpenLinkContext';

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
  url: string;
}

function PageView({
  pdf,
  pageNum,
  scale,
  query,
  onLink,
}: {
  pdf: PDFDocumentProxy;
  pageNum: number;
  scale: number;
  query: string;
  onLink: (url: string) => void;
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
        const url = typeof a.url === 'string' ? a.url : undefined;
        const rect = a.rect;
        if (a.subtype !== 'Link' || url === undefined || !Array.isArray(rect)) continue;
        const [x1, y1, x2, y2] = viewport.convertToViewportRectangle(rect as number[]);
        boxes.push({
          left: Math.min(x1, x2),
          top: Math.min(y1, y2),
          width: Math.abs(x2 - x1),
          height: Math.abs(y2 - y1),
          url,
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

  // Highlight spans containing the search term (best-effort, per-span).
  useEffect(() => {
    const q = query.trim().toLowerCase();
    for (const div of textDivsRef.current) {
      const hit = q !== '' && (div.textContent ?? '').toLowerCase().includes(q);
      div.classList.toggle('pdfjs-hl', hit);
    }
  }, [query, size]);

  const style: React.CSSProperties = {
    ...(size !== null ? { width: size.w, height: size.h } : { minHeight: 400 }),
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
          className="pdfjs-link"
          style={{ left: l.left, top: l.top, width: l.width, height: l.height }}
          title={l.url}
          onClick={(e) => {
            e.preventDefault();
            onLink(l.url);
          }}
        />
      ))}
    </div>
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

  function fitWidth(): void {
    const el = scrollRef.current;
    if (el === null || pdf === null) return;
    void pdf.getPage(1).then((page) => {
      const base = page.getViewport({ scale: 1 });
      const avail = el.clientWidth - 32;
      if (avail > 0) setScale(Math.max(0.4, Math.min(3, avail / base.width)));
    });
  }

  function scrollToPage(n: number): void {
    const el = scrollRef.current?.querySelector(`[data-page="${n}"]`);
    if (el instanceof HTMLElement) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
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
    const sel = window.getSelection()?.toString().trim() ?? '';
    if (sel === '' || onSaveSelection === undefined) return;
    onSaveSelection(sel);
    setSaved(true);
    window.setTimeout(() => setSaved(false), 1500);
  }

  if (err) return <div className="muted region-pad">{t('read.pdfRenderFailed')}</div>;

  return (
    <div className="pdfjs-view">
      <div className="pdfjs-toolbar">
        <span className="muted small pdfjs-name">{fileName ?? 'PDF'}</span>
        {pdf !== null && <span className="muted small">{t('read.pdfPages').replace('{n}', String(pdf.numPages))}</span>}
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
                ‹
              </button>
              <button className="pdfjs-zoom" title={t('read.browserForward')} disabled={matches.length === 0} onClick={() => stepMatch(1)}>
                ›
              </button>
            </>
          )}
        </div>
        {onSaveSelection !== undefined && (
          <button className="pdfjs-zoom pdfjs-tonotes" title={t('read.copyToNotesHint')} onClick={saveSelection}>
            {saved ? t('read.copiedToNotes') : t('read.copyToNotes')}
          </button>
        )}
        <button className="pdfjs-zoom" title={t('read.zoomOut')} onClick={() => setScale((s) => Math.max(0.4, s - 0.2))}>
          −
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomFit')} onClick={fitWidth}>
          {Math.round(scale * 100)}%
        </button>
        <button className="pdfjs-zoom" title={t('read.zoomIn')} onClick={() => setScale((s) => Math.min(3, s + 0.2))}>
          +
        </button>
      </div>
      <div ref={scrollRef} className="pdfjs-scroll scroll">
        {pdf === null && !err && <div className="muted region-pad">{t('read.loadingPdf')}</div>}
        {pdf !== null &&
          Array.from({ length: pdf.numPages }, (_, i) => (
            <PageView key={i + 1} pdf={pdf} pageNum={i + 1} scale={scale} query={query} onLink={openLink} />
          ))}
      </div>
    </div>
  );
}
