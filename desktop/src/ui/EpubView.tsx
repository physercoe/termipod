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
    const r = book.renderTo(host, { width: '100%', height: '100%', flow: 'scrolled-doc', spread: 'none' });
    rendition.current = r;
    void r.display();
    void book.loaded.navigation.then((nav) => {
      setToc(nav.toc.map((i: NavItem) => ({ label: i.label.trim(), href: i.href })));
    });
    r.on('selected', (_cfiRange: string, contents: Contents) => {
      setSel(contents.window.getSelection()?.toString().trim() ?? '');
    });
    return () => {
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
