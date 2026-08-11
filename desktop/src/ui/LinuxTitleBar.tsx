import type { MouseEvent } from 'react';
import { invoke } from '../bridge';

type MenuSection = 'file' | 'edit' | 'view' | 'window';

const MENU_SECTIONS: Array<{ id: MenuSection; label: string; accessKey: string }> = [
  { id: 'file', label: 'File', accessKey: 'f' },
  { id: 'edit', label: 'Edit', accessKey: 'e' },
  { id: 'view', label: 'View', accessKey: 'v' },
  { id: 'window', label: 'Window', accessKey: 'w' },
];

export function LinuxTitleBar(): JSX.Element {
  function openMenu(
    section: MenuSection,
    event: MouseEvent<HTMLButtonElement>,
  ): void {
    const rect = event.currentTarget.getBoundingClientRect();
    void invoke<void>('menu_show_application', {
      section,
      x: Math.round(rect.left),
      y: Math.round(rect.bottom),
    }).catch(() => {
      /* Native menu display is best-effort; the title row remains usable. */
    });
  }

  return (
    <header className="linux-titlebar" aria-label="Window title bar">
      <div className="linux-titlebar-inner">
        <span className="linux-titlebar-icon" role="img" aria-label="TermiPod">
          <svg viewBox="128 128 256 256" aria-hidden="true">
            <defs>
              <linearGradient id="linux-brand-accent" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="var(--brand-mark-accent-start)" />
                <stop offset="48%" stopColor="var(--brand-mark-accent-mid)" />
                <stop offset="100%" stopColor="var(--brand-mark-accent-end)" />
              </linearGradient>
              <linearGradient id="linux-brand-spark" x1="0%" y1="0%" x2="0%" y2="100%">
                <stop offset="0%" stopColor="var(--brand-mark-spark-start)" />
                <stop offset="100%" stopColor="var(--brand-mark-spark-end)" />
              </linearGradient>
            </defs>
            <path d="M162 182 L232 252 L162 322" fill="none" stroke="url(#linux-brand-accent)" strokeWidth="31" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M326.5 223 Q330.85 247.65 355.5 252 Q330.85 256.35 326.5 281 Q322.15 256.35 297.5 252 Q322.15 247.65 326.5 223Z" fill="url(#linux-brand-spark)" />
          </svg>
        </span>
        <nav className="linux-titlebar-menu" aria-label="Application menu">
          {MENU_SECTIONS.map((section) => (
            <button
              key={section.id}
              type="button"
              className="linux-titlebar-menu-item"
              aria-haspopup="menu"
              accessKey={section.accessKey}
              onClick={(event) => openMenu(section.id, event)}
            >
              {section.label}
            </button>
          ))}
        </nav>
        <span className="linux-titlebar-drag" aria-hidden="true" />
      </div>
    </header>
  );
}
