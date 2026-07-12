import { useEffect, useRef, useState } from 'react';
import * as pdfjsLib from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist';
import { useT } from '../i18n';
import { useOpenLink } from './OpenLinkContext';

/// A self-contained PDF viewer built on bundled pdf.js — no CDN, no native
/// browser plugin. This replaces the `<iframe src=blob>` viewer, which WebView2
/// (Windows/Edge) refused to render ("此页面已被 Microsoft Edge 阻止") and whose
/// in-PDF links hijacked the whole SPA with no way back.
///
/// pdf.js renders each page to a canvas we own, so it works on every platform;
/// and because we build the link layer ourselves from `page.getAnnotations()`,
/// a clicked link routes through `useOpenLink()` into the in-app browser tab
/// instead of navigating the app. Pages render lazily (IntersectionObserver) so
/// a long PDF doesn't rasterise every page up front.

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
  onLink,
}: {
  pdf: PDFDocumentProxy;
  pageNum: number;
  scale: number;
  onLink: (url: string) => void;
}): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);
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
    let task: ReturnType<PDFPageProxy['render']> | null = null;
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
      task = page.render({ canvasContext: ctx, viewport, transform: ratio !== 1 ? [ratio, 0, 0, ratio, 0, 0] : undefined });
      try {
        await task.promise;
      } catch {
        return; // render cancelled (scale changed / unmounted)
      }
      if (cancelled) return;
      const annots = await page.getAnnotations();
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
      task?.cancel();
    };
  }, [pdf, pageNum, scale, visible]);

  return (
    <div
      ref={wrapRef}
      className="pdfjs-page"
      style={size !== null ? { width: size.w, height: size.h } : { minHeight: 400 }}
    >
      <canvas ref={canvasRef} className="pdfjs-canvas" style={size !== null ? { width: size.w, height: size.h } : undefined} />
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

export function PdfCanvas({ data, fileName }: { data: ArrayBuffer; fileName?: string }): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null);
  const [err, setErr] = useState(false);
  const [scale, setScale] = useState(1.2);

  useEffect(() => {
    let cancelled = false;
    let doc: PDFDocumentProxy | null = null;
    setPdf(null);
    setErr(false);
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
    const first = pdf;
    if (el === null || first === null) return;
    void first.getPage(1).then((page) => {
      const base = page.getViewport({ scale: 1 });
      const avail = el.clientWidth - 32;
      if (avail > 0) setScale(Math.max(0.4, Math.min(3, avail / base.width)));
    });
  }

  if (err) return <div className="muted region-pad">{t('read.pdfRenderFailed')}</div>;

  return (
    <div className="pdfjs-view">
      <div className="pdfjs-toolbar">
        <span className="muted small pdfjs-name">{fileName ?? 'PDF'}</span>
        <span className="spacer" />
        {pdf !== null && <span className="muted small">{t('read.pdfPages').replace('{n}', String(pdf.numPages))}</span>}
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
            <PageView key={i + 1} pdf={pdf} pageNum={i + 1} scale={scale} onLink={openLink} />
          ))}
      </div>
    </div>
  );
}
