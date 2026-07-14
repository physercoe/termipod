import { useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { Markdown, slugify } from './Markdown';

/// Document reader for a markdown attachment (`.md`/`.markdown`): the rendered
/// prose plus a left outline/nav rail built from the document's headings (parity
/// with the PDF reader's outline). Clicking a heading scrolls the body to it.
/// Math renders via the shared <Markdown> (singleDollarMath + `\(…\)`/`\[…\]`
/// normalization); heading `id`s are stamped so the outline can target them.

interface Head {
  depth: number;
  text: string;
  slug: string;
}

// Strip inline markdown so the outline label + slug match the rendered heading's
// text (which react-markdown emits with formatting removed).
function cleanInline(s: string): string {
  return s
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '') // images
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1') // links → text
    .replace(/[*_~`]+/g, '') // emphasis / inline-code markers
    .trim();
}

// ATX headings only (`#`…`######`), skipping fenced code blocks so a `# comment`
// inside a code sample is never mistaken for a heading.
function extractHeadings(md: string): Head[] {
  const out: Head[] = [];
  let inFence = false;
  let fenceChar = '';
  for (const line of md.split('\n')) {
    const fence = line.match(/^\s*(```+|~~~+)/);
    if (fence !== null) {
      const ch = fence[1][0];
      if (!inFence) {
        inFence = true;
        fenceChar = ch;
      } else if (ch === fenceChar) {
        inFence = false;
      }
      continue;
    }
    if (inFence) continue;
    const m = line.match(/^(#{1,6})\s+(.+?)\s*#*\s*$/);
    if (m !== null) {
      const text = cleanInline(m[2]);
      if (text !== '') out.push({ depth: m[1].length, text, slug: slugify(text) });
    }
  }
  return out;
}

export function MarkdownReader({ text }: { text: string }): JSX.Element {
  const t = useT();
  const headings = useMemo(() => extractHeadings(text), [text]);
  const [open, setOpen] = useState(true);
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const minDepth = useMemo(() => Math.min(6, ...headings.map((h) => h.depth)), [headings]);

  function go(slug: string): void {
    const sel = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(slug) : slug;
    bodyRef.current?.querySelector(`#${sel}`)?.scrollIntoView({ behavior: 'auto', block: 'start' });
  }

  const hasOutline = headings.length > 1;
  return (
    <div className="mdreader">
      {hasOutline &&
        (open ? (
          <div className="mdreader-outline">
            <div className="mdreader-outline-head">
              <span className="muted small">{t('read.mdOutline')}</span>
              <span className="spacer" />
              <button className="read-fold" title={t('read.collapse')} onClick={() => setOpen(false)}>
                <Icon name="chevron-left" size={14} />
              </button>
            </div>
            <div className="mdreader-outline-list">
              {headings.map((h, i) => (
                <button
                  key={`${h.slug}-${i}`}
                  className="mdreader-outline-item"
                  style={{ paddingLeft: `${8 + (h.depth - minDepth) * 12}px` }}
                  title={h.text}
                  onClick={() => go(h.slug)}
                >
                  {h.text}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <button className="mdreader-outline-show" title={t('read.mdOutline')} onClick={() => setOpen(true)}>
            <Icon name="list" />
          </button>
        ))}
      <div className="mdreader-body region-pad" ref={bodyRef}>
        <Markdown text={text} singleDollarMath headingIds />
      </div>
    </div>
  );
}
