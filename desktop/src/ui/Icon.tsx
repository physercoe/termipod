/// The app-wide inline-SVG icon set — the same idiom as JobIcon (24×24,
/// `fill:none`, `stroke:currentColor`, 1.8 stroke, round caps/joins, CSP-safe,
/// no icon font), extended from the activity bar to every toolbar/control so the
/// UI speaks ONE icon language. Replaces the grab-bag of Unicode/emoji glyphs
/// (›‹↗✕⧉⟳▾⌄▸▲▼☰−+↑↓📂 📄📖🖼🎬🎵🌐📝) that rendered at inconsistent weights,
/// sizes, and baselines across platforms and read as unpolished.
///
/// Usage: <Icon name="chevron-right" /> (defaults to 16px, the control size;
/// pass `size` for larger). Colour is inherited, so it follows active/hover/muted
/// states automatically.

import type { DocKind } from '../state/documents';

export type IconName =
  | 'chevron-left'
  | 'chevron-right'
  | 'chevron-up'
  | 'chevron-down'
  | 'external'
  | 'window'
  | 'expand'
  | 'fit-page'
  | 'rotate-cw'
  | 'hand'
  | 'close'
  | 'refresh'
  | 'menu'
  | 'plus'
  | 'minus'
  | 'upload'
  | 'download'
  | 'folder'
  | 'file-text'
  | 'book'
  | 'image'
  | 'film'
  | 'music'
  | 'globe'
  | 'note'
  | 'diagram'
  | 'copy'
  | 'check'
  | 'bold'
  | 'italic'
  | 'heading'
  | 'code'
  | 'list'
  | 'list-ordered'
  | 'quote'
  | 'link'
  | 'highlight'
  | 'underline'
  | 'square'
  | 'mic'
  | 'pen'
  | 'cloud'
  | 'split-h'
  | 'split-v'
  | 'dock-bottom'
  | 'dock-right'
  | 'trash'
  | 'key'
  | 'eye'
  | 'eye-off'
  | 'star'
  | 'search'
  | 'lock'
  | 'unlock'
  | 'undo'
  | 'terminal'
  | 'sliders'
  | 'canvas'
  | 'table'
  | 'sidebar';

const PATHS: Record<IconName, JSX.Element> = {
  // sidebar — a panel with a left column, for the nav fold toggle
  sidebar: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16" />
    </>
  ),
  'chevron-left': <path d="M15 6l-6 6 6 6" />,
  'chevron-right': <path d="M9 6l6 6-6 6" />,
  'chevron-up': <path d="M6 15l6-6 6 6" />,
  'chevron-down': <path d="M6 9l6 6 6-6" />,
  external: (
    <>
      <path d="M18 13v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h6" />
      <path d="M15 4h5v5" />
      <path d="M20 4l-8 8" />
    </>
  ),
  window: (
    <>
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <path d="M4 9h16" />
    </>
  ),
  expand: (
    <>
      <path d="M15 3h6v6" />
      <path d="M9 21H3v-6" />
      <path d="M21 3l-7 7" />
      <path d="M3 21l7-7" />
    </>
  ),
  'fit-page': (
    <>
      <rect x="7" y="3" width="10" height="18" rx="1.5" />
      <path d="M12 7.5v9" />
      <path d="M9.8 9.8 12 7.5l2.2 2.3" />
      <path d="M9.8 14.2 12 16.5l2.2-2.3" />
    </>
  ),
  'rotate-cw': (
    <>
      <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
      <path d="M21 3v5h-5" />
    </>
  ),
  hand: (
    <>
      <path d="M18 11V6a2 2 0 0 0-2-2 2 2 0 0 0-2 2" />
      <path d="M14 10V4a2 2 0 0 0-2-2 2 2 0 0 0-2 2v2" />
      <path d="M10 10.5V6a2 2 0 0 0-2-2 2 2 0 0 0-2 2v8" />
      <path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15" />
    </>
  ),
  close: (
    <>
      <path d="M6 6l12 12" />
      <path d="M18 6L6 18" />
    </>
  ),
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.34-5.66" />
      <path d="M20 4v4h-4" />
    </>
  ),
  menu: (
    <>
      <path d="M4 7h16" />
      <path d="M4 12h16" />
      <path d="M4 17h16" />
    </>
  ),
  plus: (
    <>
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </>
  ),
  minus: <path d="M5 12h14" />,
  upload: (
    <>
      <path d="M12 16V4" />
      <path d="M7 9l5-5 5 5" />
      <path d="M5 20h14" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v12" />
      <path d="M7 11l5 5 5-5" />
      <path d="M5 20h14" />
    </>
  ),
  folder: <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  'file-text': (
    <>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
      <path d="M14 3v5h5" />
      <path d="M9 13h6" />
      <path d="M9 17h6" />
    </>
  ),
  book: (
    <>
      <path d="M4 5a1 1 0 0 1 1-1h6v16H5a1 1 0 0 1-1-1z" />
      <path d="M20 5a1 1 0 0 0-1-1h-6v16h6a1 1 0 0 0 1-1z" />
    </>
  ),
  image: (
    <>
      <rect x="4" y="5" width="16" height="14" rx="2" />
      <circle cx="9" cy="10" r="1.6" />
      <path d="M4 16l4.5-4.5 4 4 3-3L20 16" />
    </>
  ),
  film: (
    <>
      <rect x="4" y="5" width="16" height="14" rx="2" />
      <path d="M10 9l5 3-5 3z" />
    </>
  ),
  music: (
    <>
      <path d="M9 18V6l10-2v11" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="16.5" cy="15" r="2.5" />
    </>
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18" />
      <path d="M12 3a14 14 0 0 1 0 18" />
      <path d="M12 3a14 14 0 0 0 0 18" />
    </>
  ),
  note: (
    <>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9z" />
      <path d="M14 3v6h5" />
      <path d="M8.5 13h4" />
    </>
  ),
  diagram: (
    <>
      <rect x="3" y="4" width="7" height="6" rx="1.2" />
      <rect x="14" y="14" width="7" height="6" rx="1.2" />
      <path d="M6.5 10v4a1 1 0 0 0 1 1H14" />
    </>
  ),
  copy: (
    <>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M6 15H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v1" />
    </>
  ),
  check: <path d="M5 12.5l4.5 4.5L19 6.5" />,
  bold: (
    <>
      <path d="M7 5h6a3.5 3.5 0 0 1 0 7H7z" />
      <path d="M7 12h7a3.5 3.5 0 0 1 0 7H7z" />
    </>
  ),
  italic: (
    <>
      <path d="M10 4h7" />
      <path d="M7 20h7" />
      <path d="M15 4l-6 16" />
    </>
  ),
  heading: (
    <>
      <path d="M6 4v16" />
      <path d="M18 4v16" />
      <path d="M6 12h12" />
    </>
  ),
  code: (
    <>
      <path d="M16 6l4 6-4 6" />
      <path d="M8 6l-4 6 4 6" />
    </>
  ),
  list: (
    <>
      <path d="M9 6h11" />
      <path d="M9 12h11" />
      <path d="M9 18h11" />
      <circle cx="4.5" cy="6" r="1.1" />
      <circle cx="4.5" cy="12" r="1.1" />
      <circle cx="4.5" cy="18" r="1.1" />
    </>
  ),
  'list-ordered': (
    <>
      <path d="M10 6h10" />
      <path d="M10 12h10" />
      <path d="M10 18h10" />
      <path d="M4 4.5l1-.5V8" />
      <path d="M3.5 12h2" />
      <path d="M3.5 18h2" />
    </>
  ),
  quote: (
    <>
      <path d="M4 5v14" />
      <path d="M9 8h11" />
      <path d="M9 12h11" />
      <path d="M9 16h7" />
    </>
  ),
  link: (
    <>
      <path d="M10 13a5 5 0 0 0 7 0l2-2a5 5 0 0 0-7-7l-1 1" />
      <path d="M14 11a5 5 0 0 0-7 0l-2 2a5 5 0 0 0 7 7l1-1" />
    </>
  ),
  // A highlighter marker with its ink base-line — the "highlight" tool.
  highlight: (
    <>
      <path d="M4 21h7" />
      <path d="M14.5 4.5l5 5" />
      <path d="M18.5 6.5l-9 9-3.5.5.5-3.5 9-9a1.4 1.4 0 0 1 2 0l1 1a1.4 1.4 0 0 1 0 2z" />
    </>
  ),
  underline: (
    <>
      <path d="M6 4v7a6 6 0 0 0 12 0V4" />
      <path d="M5 20h14" />
    </>
  ),
  square: <rect x="4" y="4" width="16" height="16" rx="2" />,
  mic: (
    <>
      <rect x="9" y="3" width="6" height="11" rx="3" />
      <path d="M5 11a7 7 0 0 0 14 0" />
      <path d="M12 18v3" />
    </>
  ),
  cloud: <path d="M6.5 19a4.5 4.5 0 0 1-.5-8.97A6 6 0 0 1 17.7 9.5 4.25 4.25 0 0 1 17.5 18H6.5z" />,
  // split-h — two panes side by side (a vertical divider): split right
  'split-h': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M12 4v16" />
    </>
  ),
  // split-v — two panes stacked (a horizontal divider): split down
  'split-v': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 12h18" />
    </>
  ),
  // dock-bottom — panel docked along the bottom edge.
  'dock-bottom': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 15h18" />
    </>
  ),
  // dock-right — panel docked along the right edge.
  'dock-right': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M15 4v16" />
    </>
  ),
  pen: (
    <>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" />
    </>
  ),
  trash: (
    <>
      <path d="M4 7h16" />
      <path d="M9 7V4h6v3" />
      <path d="M6 7l1 13a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-13" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="15" r="4" />
      <path d="M10.8 12.2L20 3" />
      <path d="M16 7l3 3" />
      <path d="M14 9l2.5 2.5" />
    </>
  ),
  eye: (
    <>
      <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  'eye-off': (
    <>
      <path d="M4 4l16 16" />
      <path d="M9.9 5.2A9.8 9.8 0 0 1 12 5c6.5 0 10 7 10 7a17.6 17.6 0 0 1-3.3 4" />
      <path d="M6.5 7.6A17.4 17.4 0 0 0 2 12s3.5 7 10 7a9.8 9.8 0 0 0 3.5-.7" />
      <path d="M9.5 9.5a3 3 0 0 0 4.2 4.2" />
    </>
  ),
  star: <path d="M12 3.5l2.6 5.3 5.9.85-4.25 4.15 1 5.85L12 16.9l-5.25 2.75 1-5.85L2.5 9.65l5.9-.85z" />,
  search: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="M20 20l-3.6-3.6" />
    </>
  ),
  lock: (
    <>
      <rect x="4.5" y="10" width="15" height="10.5" rx="2" />
      <path d="M8 10V7a4 4 0 0 1 8 0v3" />
    </>
  ),
  unlock: (
    <>
      <rect x="4.5" y="10" width="15" height="10.5" rx="2" />
      <path d="M8 10V7a4 4 0 0 1 7.5-1.8" />
    </>
  ),
  undo: (
    <>
      <path d="M9 14 4 9l5-5" />
      <path d="M4 9h11a5 5 0 0 1 0 10h-1" />
    </>
  ),
  terminal: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M7 9l3 3-3 3" />
      <path d="M13 15h4" />
    </>
  ),
  sliders: (
    <>
      <path d="M4 7h9" />
      <path d="M17 7h3" />
      <circle cx="15" cy="7" r="2" />
      <path d="M4 17h3" />
      <path d="M11 17h9" />
      <circle cx="9" cy="17" r="2" />
    </>
  ),
  canvas: (
    <>
      <circle cx="18" cy="6" r="2.2" />
      <circle cx="6" cy="12" r="2.2" />
      <circle cx="18" cy="18" r="2.2" />
      <path d="M8 11l8-4" />
      <path d="M8 13l8 4" />
    </>
  ),
  table: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 9h18" />
      <path d="M3 14.5h18" />
      <path d="M9 4v16" />
    </>
  ),
};

export function Icon({
  name,
  size = 16,
  className,
}: {
  name: IconName;
  size?: number;
  className?: string;
}): JSX.Element {
  return (
    <svg
      className={className !== undefined ? `ui-icon ${className}` : 'ui-icon'}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {PATHS[name]}
    </svg>
  );
}

/// The tab/nav glyph for an Author document kind. Shared so the tab strip and the
/// nav list can't drift apart.
export function docKindIcon(kind: DocKind): IconName {
  switch (kind) {
    case 'diagram':
      return 'diagram';
    case 'canvas':
      return 'canvas';
    case 'table':
      return 'table';
    default:
      return 'note';
  }
}
