import type { MouseEvent } from 'react';
import { invoke } from '../bridge';
import { JobIcon } from './JobIcon';

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
          <JobIcon id="fleet" size={16} />
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
