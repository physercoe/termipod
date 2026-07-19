import { useEffect, useRef, useState } from 'react';
import ePub, { type Rendition, type Contents, type NavItem, type Location } from 'epubjs';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';
import { EPUB_THEMES, epubThemeCss, type EpubTheme } from './epubThemes';

/// Offline EPUB reader for the Read surface. EPUB is a ZIP of XHTML; epub.js
/// parses and renders it fully client-side (no network), so it works in the
/// webview offline like the pdf.js path. Scrolled-doc flow renders one chapter at
/// a time; prev/next and a TOC move between them. Text selection can be saved to
/// notes (parity with the PDF reader's onSaveSelection).

// Reader font scale (percent of the EPUB's own base size), persisted so a chosen
// size survives reopen. Clamped to a sane range.
const FONT_KEY = 'termipod.read.epubFontPct';
const FONT_MIN = 70;
const FONT_MAX = 200;
const FONT_STEP = 10;
function loadFontPct(): number {
  const raw = Number(localStorage.getItem(FONT_KEY));
  if (!Number.isFinite(raw) || raw < FONT_MIN || raw > FONT_MAX) return 100;
  return raw;
}

// Reading-paper theme, persisted so a chosen theme survives reopen.
const THEME_KEY = 'termipod.read.epubTheme';
function loadTheme(): EpubTheme {
  const raw = localStorage.getItem(THEME_KEY);
  return raw === 'sepia' || raw === 'night' ? raw : 'default';
}
// The marker on the injected theme <style>, so a live theme change can find and
// rewrite it per section instead of stacking styles.
const THEME_STYLE_MARK = 'epub-theme';

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
  // Loading / error lifecycle: a corrupt or unparseable EPUB used to hang on a
  // blank host with no feedback; surface both states explicitly.
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  // Reading progress (0–100), populated once locations are generated in the
  // background; 0 means "not yet available" and the indicator stays hidden.
  const [pct, setPct] = useState(0);
  const [fontPct, setFontPct] = useState(loadFontPct);
  const [theme, setTheme] = useState<EpubTheme>(loadTheme);
  // Read the live theme from inside the once-registered content hook without
  // re-running the render effect.
  const themeRef = useRef(theme);
  themeRef.current = theme;

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    setStatus('loading');
    setPct(0);
    let book: ReturnType<typeof ePub>;
    let r: Rendition;
    try {
      // epub.js mutates the ArrayBuffer's view; hand it a copy so re-mounts (or a
      // concurrent PDF arrayBuffer reader) never see a detached/consumed buffer.
      book = ePub(data.slice(0));
    } catch {
      setStatus('error');
      return;
    }
    // epub.js mishandles a `'100%'` width string in scrolled-doc: it snapshots a
    // pixel width for the inner container at render time and a later `resize`
    // treats the config as already-100% and no-ops, so the book stays pinned at
    // its first (often narrow / 0-width) measurement. Hand it explicit pixels
    // from the host and drive every subsequent width through `resize()` below.
    const w0 = host.clientWidth || 800;
    const h0 = host.clientHeight || 600;
    r = book.renderTo(host, { width: w0, height: h0, flow: 'scrolled-doc', spread: 'none' });
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
        // Reading-paper theme (#321). A separate <style> so a live theme switch can
        // rewrite just this element per section. Appended after width so it wins.
        const themeStyle = doc.createElement('style');
        themeStyle.setAttribute('data-termipod', THEME_STYLE_MARK);
        themeStyle.textContent = epubThemeCss(themeRef.current);
        doc.head.appendChild(themeStyle);
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
    // Apply the persisted font size to every section (current + future). themes
    // registers an override that the content hook re-applies per chapter.
    try {
      r.themes.fontSize(`${loadFontPct()}%`);
    } catch {
      /* pre-render */
    }
    r.display()
      .then(() => setStatus('ready'))
      .catch(() => setStatus('error'));
    // TOC is best-effort — a book can render fine with an unparseable nav.
    book.loaded.navigation
      .then((nav) => setToc(nav.toc.map((i: NavItem) => ({ label: i.label.trim(), href: i.href }))))
      .catch(() => setToc([]));
    // Generate coarse locations in the background so a reading-progress percent is
    // available; skip silently if it fails (huge/edge-case books).
    book.ready
      .then(() => book.locations.generate(1200))
      .catch(() => undefined);
    r.on('relocated', (loc: Location) => {
      const p = loc?.start?.percentage;
      if (typeof p === 'number' && p > 0) setPct(Math.round(p * 100));
    });
    // Keyboard nav inside the rendered chapter (iframe has focus): epub.js
    // forwards keyup from the section document.
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'ArrowLeft' || e.key === 'PageUp') void r.prev();
      else if (e.key === 'ArrowRight' || e.key === 'PageDown' || e.key === ' ') void r.next();
    };
    r.on('keyup', onKey);
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
      try {
        r.off('keyup', onKey);
      } catch {
        /* torn down */
      }
      r.destroy();
      book.destroy();
      rendition.current = null;
    };
  }, [data]);

  // Reactively apply font-size changes to the live rendition and persist.
  useEffect(() => {
    localStorage.setItem(FONT_KEY, String(fontPct));
    const r = rendition.current;
    if (r === null) return;
    try {
      r.themes.fontSize(`${fontPct}%`);
    } catch {
      /* pre-render */
    }
  }, [fontPct]);

  const bumpFont = (delta: number): void =>
    setFontPct((p) => Math.max(FONT_MIN, Math.min(FONT_MAX, p + delta)));

  // Apply a theme change to every already-rendered section (the content hook covers
  // future ones) and persist. Rewrites the marked <style> in place so themes don't
  // stack.
  useEffect(() => {
    localStorage.setItem(THEME_KEY, theme);
    const r = rendition.current;
    if (r === null) return;
    const css = epubThemeCss(theme);
    try {
      for (const c of r.getContents() as unknown as Contents[]) {
        const doc = c.document;
        if (doc?.head === undefined || doc.head === null) continue;
        let el = doc.querySelector<HTMLStyleElement>(`style[data-termipod="${THEME_STYLE_MARK}"]`);
        if (el === null) {
          el = doc.createElement('style');
          el.setAttribute('data-termipod', THEME_STYLE_MARK);
          doc.head.appendChild(el);
        }
        el.textContent = css;
      }
    } catch {
      /* section torn down mid-update — the content hook re-applies on next render */
    }
  }, [theme]);

  const cycleTheme = (): void =>
    setTheme((cur) => EPUB_THEMES[(EPUB_THEMES.indexOf(cur) + 1) % EPUB_THEMES.length]);

  // Toolbar-level keyboard nav (when focus is on the toolbar, not the iframe).
  const onHostKey = (e: React.KeyboardEvent): void => {
    if (e.key === 'ArrowLeft') void rendition.current?.prev();
    else if (e.key === 'ArrowRight') void rendition.current?.next();
  };

  return (
    <div className="epub-view" onKeyDown={onHostKey}>
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
        <span className="epub-font">
          <button
            className="small"
            title={t('read.epubFontSmaller')}
            disabled={fontPct <= FONT_MIN}
            onClick={() => bumpFont(-FONT_STEP)}
          >
            A−
          </button>
          <span className="epub-font-pct" title={t('read.epubFontSize')}>
            {fontPct}%
          </span>
          <button
            className="small"
            title={t('read.epubFontLarger')}
            disabled={fontPct >= FONT_MAX}
            onClick={() => bumpFont(FONT_STEP)}
          >
            A+
          </button>
        </span>
        <button
          className="small"
          title={t('read.epubTheme')}
          aria-label={t(`read.epubTheme.${theme}`)}
          onClick={cycleTheme}
        >
          <Icon name="book" /> {t(`read.epubTheme.${theme}`)}
        </button>
        {pct > 0 && (
          <span className="epub-progress muted small" title={t('read.epubProgress')}>
            {pct}%
          </span>
        )}
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
        <div className="epub-stage">
          {status === 'loading' && <div className="epub-state muted">{t('read.epubLoading')}</div>}
          {status === 'error' && <div className="epub-state error">{t('read.epubError')}</div>}
          <div className="epub-host" ref={hostRef} style={status === 'ready' ? undefined : { visibility: 'hidden' }} />
        </div>
      </div>
    </div>
  );
}
