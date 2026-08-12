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
  /// 1-based source line of the heading — lets an editor-backed caller jump the
  /// CodeMirror selection to it (the rendered preview jumps by stamped `id`).
  line: number;
}

interface OutlineHead {
  head: Head;
  key: string;
  hasChildren: boolean;
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
  const lines = md.split('\n');
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
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
      if (text !== '') out.push({ depth: m[1].length, text, slug: slugify(text), line: i + 1 });
    }
  }
  return out;
}

export function MarkdownOutline({
  headings,
  bodyRef,
  widthKey,
  side = 'left',
  onJump,
  foldKey,
}: {
  headings: Head[];
  /** The scrollable body hosting the rendered headings (ids stamped). */
  bodyRef: RefObject<HTMLDivElement | null>;
  /** usePanelWidth persistence key — each surface keeps its own rail width. */
  widthKey: string;
  /** Which edge the rail docks to. `right` (Obsidian-style, the editor) flips the
   *  fold chevron, resize direction, and collapsed show-button side. */
  side?: 'left' | 'right';
  /** When set, replaces the built-in `#slug` scroll so the caller routes the jump
   *  per view mode (e.g. editor line jump vs preview scroll). */
  onJump?: (h: Head) => void;
  /** localStorage key for the fold state; omit to keep it in-memory only. */
  foldKey?: string;
}): JSX.Element | null {
  const t = useT();
  const [open, setOpen] = useState(() => (foldKey !== undefined ? localStorage.getItem(foldKey) !== '0' : true));
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [outlineW, resizeOutline] = usePanelWidth(widthKey, 240, 160, 460, side === 'right' ? -1 : 1);
  const minDepth = useMemo(() => Math.min(6, ...headings.map((h) => h.depth)), [headings]);
  const outlineHeads = useMemo<OutlineHead[]>(() => {
    const slugCounts = new Map<string, number>();
    return headings.map((head, index) => {
      const occurrence = slugCounts.get(head.slug) ?? 0;
      slugCounts.set(head.slug, occurrence + 1);
      return {
        head,
        key: `${head.slug}-${occurrence}`,
        hasChildren: index + 1 < headings.length && headings[index + 1].depth > head.depth,
      };
    });
  }, [headings]);
  const branchKeys = useMemo(() => outlineHeads.filter((item) => item.hasChildren).map((item) => item.key), [outlineHeads]);
  const visibleHeads = useMemo(() => {
    let hiddenBelowDepth: number | null = null;
    return outlineHeads.filter((item) => {
      if (hiddenBelowDepth !== null) {
        if (item.head.depth > hiddenBelowDepth) return false;
        hiddenBelowDepth = null;
      }
      if (item.hasChildren && collapsed.has(item.key)) hiddenBelowDepth = item.head.depth;
      return true;
    });
  }, [collapsed, outlineHeads]);
  const allBranchesCollapsed = branchKeys.length > 0 && branchKeys.every((key) => collapsed.has(key));
  const right = side === 'right';

  function fold(next: boolean): void {
    setOpen(next);
    if (foldKey !== undefined) {
      try {
        localStorage.setItem(foldKey, next ? '1' : '0');
      } catch {
        /* ignore */
      }
    }
  }

  function go(h: Head): void {
    if (onJump !== undefined) {
      onJump(h);
      return;
    }
    const sel = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(h.slug) : h.slug;
    bodyRef.current?.querySelector(`#${sel}`)?.scrollIntoView({ behavior: 'auto', block: 'start' });
  }

  function toggleBranch(key: string): void {
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function toggleAllBranches(): void {
    setCollapsed(allBranchesCollapsed ? new Set() : new Set(branchKeys));
  }

  // A lone heading (or none) is no outline — the rail hides entirely.
  if (headings.length <= 1) return null;
  const foldBtn = (
    <button className="pane-toggle read-fold" title={t('read.collapse')} onClick={() => fold(false)}>
      <Icon name="sidebar" size={16} className={right ? 'mirror-x' : undefined} />
    </button>
  );
  const rail = (
    <div className={`mdreader-outline${right ? ' side-right' : ''}`} style={{ width: outlineW }}>
      <div className="mdreader-outline-head">
        {right && foldBtn}
        <span className="muted small">{t('read.mdOutline')}</span>
        {branchKeys.length > 0 && (
          <button
            className="mdreader-outline-all-toggle"
            title={t(allBranchesCollapsed ? 'read.expandAllHeadings' : 'read.collapseAllHeadings')}
            aria-label={t(allBranchesCollapsed ? 'read.expandAllHeadings' : 'read.collapseAllHeadings')}
            onClick={toggleAllBranches}
          >
            <Icon name={allBranchesCollapsed ? 'chevron-right' : 'chevron-down'} size={13} />
          </button>
        )}
        <span className="spacer" />
        {!right && foldBtn}
      </div>
      <div className="mdreader-outline-list">
        {visibleHeads.map(({ head, key, hasChildren }) => {
          const isCollapsed = collapsed.has(key);
          return (
            <div
              key={key}
              className="mdreader-outline-row"
              style={{ paddingLeft: `${4 + (head.depth - minDepth) * 12}px` }}
            >
              {hasChildren ? (
                <button
                  className="mdreader-outline-branch"
                  title={t(isCollapsed ? 'read.expandHeading' : 'read.collapseHeading')}
                  aria-label={t(isCollapsed ? 'read.expandHeading' : 'read.collapseHeading')}
                  aria-expanded={!isCollapsed}
                  onClick={() => toggleBranch(key)}
                >
                  <Icon name={isCollapsed ? 'chevron-right' : 'chevron-down'} size={12} />
                </button>
              ) : (
                <span className="mdreader-outline-branch-placeholder" aria-hidden="true" />
              )}
              <button className="mdreader-outline-item" title={head.text} onClick={() => go(head)}>
                {head.text}
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
  const handle = <ResizeHandle onResize={resizeOutline} />;
  return open ? (
    <>
      {right ? (
        <>
          {handle}
          {rail}
        </>
      ) : (
        <>
          {rail}
          {handle}
        </>
      )}
    </>
  ) : (
    <button
      className={`mdreader-outline-show${right ? ' side-right' : ''}`}
      title={t('read.mdOutline')}
      onClick={() => fold(true)}
    >
      <Icon name="list" />
    </button>
  );
}
