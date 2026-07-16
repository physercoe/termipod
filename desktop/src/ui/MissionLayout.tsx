import type { ReactNode } from 'react';
import { useState } from 'react';
import { useT } from '../i18n';
import { AttentionDock } from '../surfaces/AttentionDock';
import { FocusRegion } from '../surfaces/FocusRegion';
import { Icon } from './Icon';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';

function loadBool(key: string, dflt: boolean): boolean {
  try {
    const v = localStorage.getItem(key);
    return v === null ? dflt : v === '1';
  } catch {
    return dflt;
  }
}

/// Shared three-region frame for the Fleet and Projects tabs: a toolbar row, then
/// a **foldable + resizable** left nav, the shared `FocusRegion` centre, and the
/// attention dock. `storageKey` namespaces the persisted nav width + fold flag so
/// the two tabs remember their nav independently. The fold toggle lives at the
/// left of the toolbar (VS Code idiom); resize is the divider on the nav's right
/// edge (window-tracked drag — reliable on WebView2, see `ResizeHandle`).
export function MissionLayout({
  storageKey,
  toolbar,
  nav,
}: {
  storageKey: string;
  toolbar: ReactNode;
  nav: ReactNode;
}): JSX.Element {
  const t = useT();
  const [open, setOpen] = useState(() => loadBool(`termipod.${storageKey}.navOpen`, true));
  const [w, onResize] = usePanelWidth(`termipod.${storageKey}.navW`, 240, 180, 480);

  function toggle(): void {
    setOpen((o) => {
      const n = !o;
      try {
        localStorage.setItem(`termipod.${storageKey}.navOpen`, n ? '1' : '0');
      } catch {
        /* ignore */
      }
      return n;
    });
  }

  return (
    <>
      <div className="fleet-toolbar">
        <button
          className={open ? 'nav-fold-btn active' : 'nav-fold-btn'}
          title={open ? t('nav.collapse') : t('nav.expand')}
          aria-label={open ? t('nav.collapse') : t('nav.expand')}
          aria-pressed={open}
          onClick={toggle}
        >
          <Icon name="sidebar" size={16} />
        </button>
        {toolbar}
      </div>
      <div className="shell-body">
        {open && (
          <>
            <div className="region navigator" style={{ width: w }}>
              {nav}
            </div>
            <ResizeHandle onResize={onResize} />
          </>
        )}

        <FocusRegion />

        <div className="region dock">
          <div className="region-header">{t('region.attention')}</div>
          <AttentionDock />
        </div>
      </div>
    </>
  );
}
