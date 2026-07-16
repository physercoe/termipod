import type { JobId } from '../state/workbench';

/// Professional inline-SVG icons for the workbench activity bar — one stroke
/// glyph per job, replacing the round-1 emoji (which render inconsistently
/// across platforms and read as unpolished). All 24×24, `fill:none`,
/// `stroke:currentColor` so they inherit the rail's active/hover colour, and
/// CSP-safe (no icon font, no external asset). Hand-authored geometric line
/// icons in the VS Code / Linear idiom.
const PATHS: Record<JobId, JSX.Element> = {
  // mission-control — a dashboard grid
  fleet: (
    <>
      <rect x="3" y="3" width="7" height="9" rx="1.3" />
      <rect x="14" y="3" width="7" height="5" rx="1.3" />
      <rect x="14" y="12" width="7" height="9" rx="1.3" />
      <rect x="3" y="16" width="7" height="5" rx="1.3" />
    </>
  ),
  // projects — stacked layers (units of directed work)
  projects: (
    <>
      <path d="M12 3l8 4.5-8 4.5-8-4.5z" />
      <path d="M4 12l8 4.5 8-4.5" />
      <path d="M4 16.5l8 4.5 8-4.5" opacity="0.55" />
    </>
  ),
  // read — an open book
  read: (
    <>
      <path d="M12 7v13" />
      <path d="M3 18V5a1 1 0 0 1 1-1h4.5a3 3 0 0 1 3.5 3 3 3 0 0 1 3.5-3H20a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-5.5a2 2 0 0 0-2.5 1.5A2 2 0 0 0 9.5 19H4a1 1 0 0 1-1-1z" />
    </>
  ),
  // author — a pen
  author: (
    <>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
    </>
  ),
  // debug — code brackets
  debug: (
    <>
      <path d="M8 8l-4 4 4 4" />
      <path d="M16 8l4 4-4 4" />
      <path d="M13.5 5l-3 14" />
    </>
  ),
  // compare — overlaid metric curves
  compare: (
    <>
      <path d="M4 4v16h16" />
      <path d="M7.5 14.5l3-4 3 2 4-6.5" />
      <path d="M7.5 17.5l3-1.5 3 1 4-3" opacity="0.5" />
    </>
  ),
  // record — a clipboard log
  record: (
    <>
      <rect x="5" y="4.5" width="14" height="16.5" rx="2" />
      <path d="M9 4.5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v1a1 1 0 0 1-1 1h-4a1 1 0 0 1-1-1z" />
      <path d="M9 11.5h6" />
      <path d="M9 15.5h4" />
    </>
  ),
  // terminal — a window with a shell prompt
  terminal: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M7 9l3 3-3 3" />
      <path d="M13 15h4" />
    </>
  ),
  // settings — a gear
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </>
  ),
};

export function JobIcon({ id, size = 22 }: { id: JobId; size?: number }): JSX.Element {
  return (
    <svg
      className="job-icon"
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
      {PATHS[id]}
    </svg>
  );
}
