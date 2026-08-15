import type { ReactNode } from 'react';
import { useState } from 'react';
import { useT } from '../i18n';
import type { FocusScope } from '../state/focus';
import { AttentionDock } from '../surfaces/AttentionDock';
import { FocusRegion } from '../surfaces/FocusRegion';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';
import { HeaderPaneToggle } from './WorkbenchSurface';

function loadBool(key: string, dflt: boolean): boolean {
  try {
    const v = localStorage.getItem(key);
    return v === null ? dflt : v === '1';
  } catch {
    return dflt;
  }
}

function saveBool(key: string, v: boolean): void {
  try {
    localStorage.setItem(key, v ? '1' : '0');
  } catch {
    /* ignore */
  }
}

/// Shared three-region frame for the Fleet and Projects tabs: a toolbar row, then
/// a **foldable + resizable** left nav, the shared `FocusRegion` centre, and the
/// attention dock. `storageKey` namespaces the persisted nav width + fold flag so
/// the two tabs remember their nav independently. Both pane toggles stay pinned
/// to the toolbar edges whether their pane is open or closed; resize remains on
/// the pane divider (window-tracked drag — reliable on WebView2, see
/// `ResizeHandle`).
export function MissionLayout({
  storageKey,
  toolbar,
  nav,
}: {
  storageKey: FocusScope;
  toolbar: ReactNode;
  nav: ReactNode;
}): JSX.Element {
  const t = useT();
  const [open, setOpen] = useState(() => loadBool(`termipod.${storageKey}.navOpen`, true));
  const [w, onResize] = usePanelWidth(`termipod.${storageKey}.navW`, 240, 180, 480);
  // The attention dock is resizable too (its handle is on its LEFT edge, so
  // dragging left widens it → sign -1). Resizing either side reflows the main
  // (focus) page between them, so the director can size the centre as they like.
  const [dockW, onResizeDock] = usePanelWidth(`termipod.${storageKey}.dockW`, 320, 240, 560, -1);
  // The dock folds too (like the nav) — governance stays reachable from the
  // persistent trailing toolbar toggle while the director reclaims its width.
  const [dockOpen, setDockOpen] = useState(() => loadBool(`termipod.${storageKey}.dockOpen`, true));

  function toggle(): void {
    setOpen((o) => {
      const n = !o;
      saveBool(`termipod.${storageKey}.navOpen`, n);
      return n;
    });
  }
  function toggleDock(): void {
    setDockOpen((o) => {
      const n = !o;
      saveBool(`termipod.${storageKey}.dockOpen`, n);
      return n;
    });
  }

  return (
    <>
      <div className="fleet-toolbar">
        <div className="fleet-toolbar-identity" style={open ? { width: w } : undefined}>
          <HeaderPaneToggle
            side="left"
            open={open}
            showLabel={t('nav.expand')}
            hideLabel={t('nav.collapse')}
            onToggle={toggle}
          />
          <span className="fleet-toolbar-label">
            {t(storageKey === 'fleet' ? 'nav.fleet' : 'nav.projects')}
          </span>
        </div>
        <span className="fleet-toolbar-grid-divider" aria-hidden="true" />
        <div className="fleet-toolbar-actions">
          {toolbar}
          <HeaderPaneToggle
            side="right"
            open={dockOpen}
            showLabel={t('nav.expand')}
            hideLabel={t('nav.collapse')}
            onToggle={toggleDock}
          />
        </div>
      </div>
      <div className="shell-body">
        {open && (
          <>
            <div className="region navigator mission-nav" style={{ width: w }}>
              <div className="mission-nav-scroll">{nav}</div>
            </div>
            <ResizeHandle onResize={onResize} />
          </>
        )}

        <FocusRegion scope={storageKey} />

        {dockOpen ? (
          <>
            <ResizeHandle onResize={onResizeDock} />
            <div className="region dock" style={{ width: dockW }}>
              <div className="region-header">{t('region.attention')}</div>
              <AttentionDock />
            </div>
          </>
        ) : null}
      </div>
    </>
  );
}
