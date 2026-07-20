import { useMemo, useState, type RefObject } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { slugify } from './Markdown';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';

/// The markdown outline/nav rail — ONE shared implementation (#322) of what
/// MarkdownReader and NoteTab each carried near-verbatim: a collapsible,
/// resizable (persisted width) list of the document's headings; clicking one
/// scrolls the caller's body (`bodyRef`) to the stamped heading id. Rendered
/// only when the document has more than one heading.

export interface Head {
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
export function extractHeadings(md: string): Head[] {
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

export function MarkdownOutline({
  headings,
  bodyRef,
  widthKey,
}: {
  headings: Head[];
  /** The scrollable body hosting the rendered headings (ids stamped). */
  bodyRef: RefObject<HTMLDivElement | null>;
  /** usePanelWidth persistence key — each surface keeps its own rail width. */
  widthKey: string;
}): JSX.Element | null {
  const t = useT();
  const [open, setOpen] = useState(true);
  const [outlineW, resizeOutline] = usePanelWidth(widthKey, 240, 160, 460);
  const minDepth = useMemo(() => Math.min(6, ...headings.map((h) => h.depth)), [headings]);

  function go(slug: string): void {
    const sel = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(slug) : slug;
    bodyRef.current?.querySelector(`#${sel}`)?.scrollIntoView({ behavior: 'auto', block: 'start' });
  }

  // A lone heading (or none) is no outline — the rail hides entirely.
  if (headings.length <= 1) return null;
  return open ? (
    <>
      <div className="mdreader-outline" style={{ width: outlineW }}>
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
      <ResizeHandle onResize={resizeOutline} />
    </>
  ) : (
    <button className="mdreader-outline-show" title={t('read.mdOutline')} onClick={() => setOpen(true)}>
      <Icon name="list" />
    </button>
  );
}
