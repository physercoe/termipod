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

export type IconName =
  | 'chevron-left'
  | 'chevron-right'
  | 'chevron-up'
  | 'chevron-down'
  | 'external'
  | 'window'
  | 'expand'
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
  | 'pen'
  | 'cloud'
  | 'trash';

const PATHS: Record<IconName, JSX.Element> = {
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
  cloud: <path d="M6.5 19a4.5 4.5 0 0 1-.5-8.97A6 6 0 0 1 17.7 9.5 4.25 4.25 0 0 1 17.5 18H6.5z" />,
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
