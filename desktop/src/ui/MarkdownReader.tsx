import { useMemo, useRef } from 'react';
import { useT } from '../i18n';
import { Markdown } from './Markdown';
import { extractHeadings, MarkdownOutline } from './MarkdownOutline';
import { useDocZoom } from './useDocZoom';
import { ZoomBar, wheelZoom } from './ZoomBar';

/// Document reader for a markdown attachment (`.md`/`.markdown`): the rendered
/// prose plus a left outline/nav rail built from the document's headings (parity
/// with the PDF reader's outline). Clicking a heading scrolls the body to it.
/// Math renders via the shared <Markdown> (singleDollarMath + `\(…\)`/`\[…\]`
/// normalization); heading `id`s are stamped so the outline can target them.
///
/// The outline rail itself is the shared `MarkdownOutline` (also used by the
/// note tab) — heading extraction + rail chrome live there (#322).

export function MarkdownReader({ text }: { text: string }): JSX.Element {
  const t = useT();
  const headings = useMemo(() => extractHeadings(text), [text]);
  const zoom = useDocZoom('md');
  const bodyRef = useRef<HTMLDivElement | null>(null);

  return (
    <div className="mdreader">
      <MarkdownOutline headings={headings} bodyRef={bodyRef} widthKey="termipod.read.mdOutlineW" />
      <div
        className="mdreader-body region-pad"
        ref={bodyRef}
        tabIndex={0}
        onWheel={wheelZoom(zoom)}
        onKeyDown={(e) => {
          if (!(e.ctrlKey || e.metaKey)) return;
          if (e.key === '=' || e.key === '+') {
            e.preventDefault();
            zoom.zoomIn();
          } else if (e.key === '-') {
            e.preventDefault();
            zoom.zoomOut();
          } else if (e.key === '0') {
            e.preventDefault();
            zoom.reset();
          }
        }}
      >
        {text.trim() === '' ? (
          <div className="muted mdreader-empty">{t('read.mdEmpty')}</div>
        ) : (
          <div className="mdreader-zoom" style={{ zoom: zoom.zoom }}>
            <Markdown text={text} singleDollarMath headingIds />
          </div>
        )}
      </div>
      {/* Pinned to the non-scrolling reader row so it stays put while the body scrolls. */}
      <ZoomBar z={zoom} className="doc-zoombar-float" />
    </div>
  );
}
