import type { ReactNode } from 'react';
import { useState } from 'react';
import { useT } from '../i18n';
import type { FocusScope } from '../state/focus';
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
/// the two tabs remember their nav independently. While open, the fold toggle
/// sits at the nav's own top-right edge, matching every other workbench pane;
/// collapsed, it returns to the toolbar as the reveal affordance. Resize is the
/// divider on the nav's right edge (window-tracked drag — reliable on WebView2,
/// see `ResizeHandle`).
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
  // The dock folds too (like the nav) — governance stays reachable but the
  // director can reclaim the width. Collapsed, it leaves a thin re-open rail.
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
        {!open && (
          <button
            className="pane-toggle nav-fold-btn"
            title={t('nav.expand')}
            aria-label={t('nav.expand')}
            aria-pressed={false}
            onClick={toggle}
          >
            <Icon name="sidebar" size={16} />
          </button>
        )}
        {toolbar}
      </div>
      <div className="shell-body">
        {open && (
          <>
            <div className="region navigator mission-nav" style={{ width: w }}>
              <div className="mission-nav-scroll">{nav}</div>
              <button
                className="pane-toggle nav-fold-btn mission-nav-fold"
                title={t('nav.collapse')}
                aria-label={t('nav.collapse')}
                aria-pressed
                onClick={toggle}
              >
                <Icon name="sidebar" size={16} />
              </button>
            </div>
            <ResizeHandle onResize={onResize} />
          </>
        )}

        <FocusRegion scope={storageKey} />

        {dockOpen ? (
          <>
            <ResizeHandle onResize={onResizeDock} />
            <div className="region dock" style={{ width: dockW }}>
              <div className="region-header foldable">
                <span>{t('region.attention')}</span>
                <button
                  className="pane-toggle dock-fold-btn"
                  title={t('nav.collapse')}
                  aria-label={t('nav.collapse')}
                  onClick={toggleDock}
                >
                  <Icon name="sidebar" size={14} className="mirror-x" />
                </button>
              </div>
              <AttentionDock />
            </div>
          </>
        ) : (
          <button
            className="dock-rail"
            title={t('nav.expand')}
            aria-label={t('nav.expand')}
            onClick={toggleDock}
          >
            <Icon name="sidebar" size={16} className="mirror-x" />
            <span className="dock-rail-label">{t('region.attention')}</span>
          </button>
        )}
      </div>
    </>
  );
}
