import { useT } from '../i18n';
import { Icon } from './Icon';
import type { DocZoom } from './useDocZoom';

/// A compact floating zoom pill (−  100%  +) for the flat/reflowable attachment
/// viewers (markdown, plain text, html/mime). The percent doubles as a reset
/// button (click → 100%). Positioned by the host viewer via a wrapper class.

export function ZoomBar({ z, className }: { z: DocZoom; className?: string }): JSX.Element {
  const t = useT();
  return (
    <div className={`doc-zoombar${className !== undefined ? ` ${className}` : ''}`} role="group" aria-label={t('read.zoomLevel')}>
      <button className="doc-zoom-btn" title={t('read.zoomOut')} aria-label={t('read.zoomOut')} onClick={z.zoomOut}>
        <Icon name="minus" size={14} />
      </button>
      <button className="doc-zoom-pct" title={t('read.zoomReset')} aria-label={t('read.zoomReset')} onClick={z.reset}>
        {Math.round(z.zoom * 100)}%
      </button>
      <button className="doc-zoom-btn" title={t('read.zoomIn')} aria-label={t('read.zoomIn')} onClick={z.zoomIn}>
        <Icon name="plus" size={14} />
      </button>
    </div>
  );
}

// Ctrl/Cmd+wheel → zoom, mirroring the PDF reader's wheel-zoom. Returned as a
// handler so each viewer can attach it to its scroll container.
export function wheelZoom(z: DocZoom): (e: React.WheelEvent) => void {
  return (e) => {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    if (e.deltaY < 0) z.zoomIn();
    else if (e.deltaY > 0) z.zoomOut();
  };
}
