import { useEffect, useRef, useState } from 'react';
import ePub, { type Rendition, type Contents, type NavItem } from 'epubjs';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';

/// Offline EPUB reader for the Read surface. EPUB is a ZIP of XHTML; epub.js
/// parses and renders it fully client-side (no network), so it works in the
/// webview offline like the pdf.js path. Scrolled-doc flow renders one chapter at
/// a time; prev/next and a TOC move between them. Text selection can be saved to
/// notes (parity with the PDF reader's onSaveSelection).

export function EpubView({
  data,
  onSaveSelection,
}: {
  data: ArrayBuffer;
  fileName: string;
  onSaveSelection?: (text: string) => void;
}): JSX.Element {
  const t = useT();
  const hostRef = useRef<HTMLDivElement | null>(null);
  const rendition = useRef<Rendition | null>(null);
  const [toc, setToc] = useState<{ label: string; href: string }[]>([]);
  const [showToc, setShowToc] = useState(false);
  const [tocW, resizeToc] = usePanelWidth('termipod.read.epubTocW', 260, 160, 480);
  const [sel, setSel] = useState('');

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    // epub.js mutates the ArrayBuffer's view; hand it a copy so re-mounts (or a
    // concurrent PDF arrayBuffer reader) never see a detached/consumed buffer.
    const book = ePub(data.slice(0));
    // epub.js mishandles a `'100%'` width string in scrolled-doc: it snapshots a
    // pixel width for the inner container at render time and a later `resize`
    // treats the config as already-100% and no-ops, so the book stays pinned at
    // its first (often narrow / 0-width) measurement. Hand it explicit pixels
    // from the host and drive every subsequent width through `resize()` below.
    const w0 = host.clientWidth || 800;
    const h0 = host.clientHeight || 600;
    const r = book.renderTo(host, { width: w0, height: h0, flow: 'scrolled-doc', spread: 'none' });
    rendition.current = r;
    // WIDTH FIX (3rd pass). In scrolled flow the iframe IS full stage width
    // (verified in epub.js: iframe locks to the stage; `contents.size()` sets
    // `body{width}` WITHOUT !important). So a narrow, left-aligned column with a
    // large right blank is the EPUB's OWN `max-width` — and crucially it's often
    // on a WRAPPER (`div`/`section`/`article`), not `body`, which the previous
    // `html,body`-only overrides never touched. We also bypass epub.js's theme
    // machinery entirely and inject a raw <style> straight into each section's
    // <head> (last child ⇒ wins ties), so nothing about rule serialization can
    // silently drop it. Media stays capped so images/tables don't overflow.
    const WIDTH_CSS = [
      'html, body { max-width: none !important; width: auto !important; margin: 0 !important; }',
      'body { padding: 0 clamp(1rem, 5vw, 4rem) !important; box-sizing: border-box !important;',
      '  column-count: 1 !important; column-width: auto !important; line-height: 1.65; }',
      // The narrow cap usually lives on a top-level wrapper — free the common
      // block wrappers, but leave media capped to the column just below.
      'body > *, body div, body section, body article, body main, body header, body footer {',
      '  max-width: none !important; width: auto !important; }',
      'img, svg, image, video, table, figure { max-width: 100% !important; }',
      'img, svg, image { height: auto !important; }',
    ].join('\n');
    r.hooks.content.register((contents: Contents) => {
      try {
        const doc = contents.document;
        if (doc?.head === undefined || doc.head === null) return;
        const style = doc.createElement('style');
        style.setAttribute('data-termipod', 'epub-width');
        style.textContent = WIDTH_CSS;
        doc.head.appendChild(style);
        // Belt-and-suspenders: an INLINE `!important` beats any author stylesheet
        // rule regardless of specificity (a stylesheet rule can't, if the EPUB
        // caps width with an `!important` class selector). Clear the cap on the
        // body + its top-level block wrappers, where the narrow column lives.
        const clear = (el: HTMLElement): void => {
          el.style.setProperty('max-width', 'none', 'important');
          el.style.setProperty('width', 'auto', 'important');
        };
        if (doc.documentElement !== null) clear(doc.documentElement);
        if (doc.body !== null) {
          clear(doc.body);
          Array.from(doc.body.children).forEach((c) => {
            if (/^(DIV|SECTION|ARTICLE|MAIN|HEADER|FOOTER)$/.test(c.tagName)) clear(c as HTMLElement);
          });
        }
      } catch {
        /* section torn down / cross-origin — ignore */
      }
    });
    void r.display();
    void book.loaded.navigation.then((nav) => {
      setToc(nav.toc.map((i: NavItem) => ({ label: i.label.trim(), href: i.href })));
    });
    r.on('selected', (_cfiRange: string, contents: Contents) => {
      setSel(contents.window.getSelection()?.toString().trim() ?? '');
    });
    // epub.js snapshots the container width at render time and does NOT reflow on
    // its own — so the book stays narrow/fixed when the pane grows (details panel
    // toggled, window resized, or a 0-width initial mount). Re-measure on resize,
    // but ONLY when the host's own box actually changed: a bare `resize()` on
    // every RO tick can feed back through the iframe's content height and strobe
    // ("splashing"), so guard on the rounded host dimensions.
    let lastW = -1;
    let lastH = -1;
    const ro = new ResizeObserver(() => {
      const w = Math.round(host.clientWidth);
      const h = Math.round(host.clientHeight);
      if (w === lastW && h === lastH) return;
      lastW = w;
      lastH = h;
      if (w === 0 || h === 0) return;
      try {
        r.resize(w, h);
      } catch {
        /* rendition torn down mid-resize */
      }
    });
    ro.observe(host);
    return () => {
      ro.disconnect();
      r.destroy();
      book.destroy();
      rendition.current = null;
    };
  }, [data]);

  return (
    <div className="epub-view">
      <div className="epub-toolbar">
        <button className="small" onClick={() => setShowToc((s) => !s)} disabled={toc.length === 0}>
          {t('read.epubToc')}
        </button>
        <button className="small" title={t('read.epubPrev')} onClick={() => void rendition.current?.prev()}>
          <Icon name="chevron-left" />
        </button>
        <button className="small" title={t('read.epubNext')} onClick={() => void rendition.current?.next()}>
          <Icon name="chevron-right" />
        </button>
        <span className="spacer" />
        {onSaveSelection !== undefined && sel !== '' && (
          <button
            className="primary small"
            onClick={() => {
              onSaveSelection(sel);
              setSel('');
            }}
          >
            {t('read.epubSaveSel')}
          </button>
        )}
      </div>
      <div className="epub-body">
        {showToc && (
          <>
            <div className="epub-toc" style={{ width: tocW }}>
              {toc.map((i) => (
                <button
                  key={i.href}
                  className="epub-toc-item"
                  title={i.label !== '' ? i.label : i.href}
                  onClick={() => {
                    void rendition.current?.display(i.href);
                    setShowToc(false);
                  }}
                >
                  {i.label !== '' ? i.label : i.href}
                </button>
              ))}
            </div>
            <ResizeHandle onResize={resizeToc} />
          </>
        )}
        <div className="epub-host" ref={hostRef} />
      </div>
    </div>
  );
}
