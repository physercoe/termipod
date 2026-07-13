import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { openBrowserWindow, openExternal } from '../platform';

/// A minimal in-app browser tab (director request: external links open in a
/// dedicated tab *inside* the app with navigation buttons, not the OS browser).
///
/// The page is an `<iframe>`; navigation history is OUR stack (back / forward /
/// reload / an editable address bar), because a cross-origin iframe won't let us
/// read its own `history`. That covers app-initiated navigations (a clicked
/// reference URL, a typed address) — which is what the reading workflow needs.
///
/// A caveat we surface rather than hide: some sites refuse to be framed
/// (`X-Frame-Options` / `frame-ancestors`) and render blank. The toolbar always
/// offers "open in system browser" as the escape hatch for those.

function normalizeUrl(raw: string): string {
  const s = raw.trim();
  if (s === '') return s;
  if (/^https?:\/\//i.test(s)) return s;
  if (/^[\w.-]+\.[a-z]{2,}(\/|$|\?|#)/i.test(s)) return `https://${s}`;
  return s;
}

function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

export function BrowserView({ initialUrl }: { initialUrl: string }): JSX.Element {
  const t = useT();
  // History stack + cursor; `nonce` forces the iframe to reload on demand.
  const [history, setHistory] = useState<string[]>([initialUrl]);
  const [idx, setIdx] = useState(0);
  const [nonce, setNonce] = useState(0);
  const [address, setAddress] = useState(initialUrl);

  const current = history[idx] ?? '';
  // Reflect back/forward moves in the address bar (a typed edit stays until Enter).
  useEffect(() => setAddress(current), [current]);

  function navigate(url: string): void {
    const u = normalizeUrl(url);
    if (u === '') return;
    // Drop any forward entries, then push.
    const next = [...history.slice(0, idx + 1), u];
    setHistory(next);
    setIdx(next.length - 1);
  }

  const canBack = idx > 0;
  const canFwd = idx < history.length - 1;

  return (
    <div className="browser-view">
      <div className="browser-bar">
        <button className="browser-nav" disabled={!canBack} title={t('read.browserBack')} onClick={() => setIdx((i) => Math.max(0, i - 1))}>
          <Icon name="chevron-left" />
        </button>
        <button
          className="browser-nav"
          disabled={!canFwd}
          title={t('read.browserForward')}
          onClick={() => setIdx((i) => Math.min(history.length - 1, i + 1))}
        >
          <Icon name="chevron-right" />
        </button>
        <button className="browser-nav" title={t('read.browserReload')} onClick={() => setNonce((n) => n + 1)}>
          <Icon name="refresh" />
        </button>
        <input
          className="browser-address"
          value={address}
          spellCheck={false}
          onChange={(e) => setAddress(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') navigate(address);
          }}
        />
        <button
          className="browser-nav"
          title={t('read.openInWindow')}
          onClick={() => openBrowserWindow(current)}
        >
          <Icon name="expand" />
        </button>
        <button className="browser-nav" title={t('read.openExternal')} onClick={() => openExternal(current)}>
          <Icon name="external" />
        </button>
      </div>
      <div className="browser-hint muted small">{t('read.browserFrameHint')}</div>
      <div className="browser-frame-wrap">
        <iframe
          key={`${idx}:${nonce}`}
          className="browser-frame"
          title={hostOf(current)}
          src={current}
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
          referrerPolicy="no-referrer"
        />
      </div>
    </div>
  );
}
