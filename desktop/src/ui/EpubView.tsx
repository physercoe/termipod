import { useEffect, useRef, useState } from 'react';
import ePub, { type Rendition, type Contents, type NavItem } from 'epubjs';
import { useT } from '../i18n';
import { Icon } from './Icon';

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
    // Many EPUBs pin the text to a narrow column (a `max-width`/`width` or CSS
    // multi-column on <html>/<body>/a wrapper); override aggressively so the text
    // uses the pane width (with comfortable side padding) instead of a fixed
    // column that leaves the rest of the wide pane blank.
    r.themes.default({
      'html, body': {
        'max-width': 'none !important',
        width: 'auto !important',
        'column-count': '1 !important',
        'column-width': 'auto !important',
      },
      body: {
        margin: '0 !important',
        padding: '0 clamp(1rem, 5vw, 4rem) !important',
        'box-sizing': 'border-box !important',
        'line-height': '1.65',
      },
      img: { 'max-width': '100% !important', height: 'auto !important' },
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
    // toggled, window resized, or a 0-width initial mount). Re-measure on resize.
    const ro = new ResizeObserver(() => {
      try {
        r.resize(host.clientWidth, host.clientHeight);
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
          <div className="epub-toc">
            {toc.map((i) => (
              <button
                key={i.href}
                className="epub-toc-item"
                onClick={() => {
                  void rendition.current?.display(i.href);
                  setShowToc(false);
                }}
              >
                {i.label !== '' ? i.label : i.href}
              </button>
            ))}
          </div>
        )}
        <div className="epub-host" ref={hostRef} />
      </div>
    </div>
  );
}
