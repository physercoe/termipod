import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { frameCheck, hostOf, openBrowserWindow, openExternal } from '../platform';

/// A minimal in-app browser tab (director request: external links open in a
/// dedicated tab *inside* the app with navigation buttons, not the OS browser).
///
/// The page is an `<iframe>`; navigation history is OUR stack (back / forward /
/// reload / an editable address bar), because a cross-origin iframe won't let us
/// read its own `history`. That covers app-initiated navigations (a clicked
/// reference URL, a typed address) — which is what the reading workflow needs.
///
/// Some sites refuse to be framed (`X-Frame-Options` / `frame-ancestors`) and
/// render blank. Instead of a static caveat, each navigation preflights the
/// page's framing headers (`frameCheck`) and a refusal renders an actionable
/// error with an "open in system browser" escape hatch (#322); the toolbar's
/// browser-window button covers the sites that merely misbehave.

function normalizeUrl(raw: string): string {
  const s = raw.trim();
  if (s === '') return s;
  if (/^https?:\/\//i.test(s)) return s;
  if (/^[\w.-]+\.[a-z]{2,}(\/|$|\?|#)/i.test(s)) return `https://${s}`;
  return s;
}


export function BrowserView({ initialUrl }: { initialUrl: string }): JSX.Element {
  const t = useT();
  // History stack + cursor; `nonce` forces the iframe to reload on demand.
  const [history, setHistory] = useState<string[]>([initialUrl]);
  const [idx, setIdx] = useState(0);
  const [nonce, setNonce] = useState(0);
  const [address, setAddress] = useState(initialUrl);
  // The URL whose preflight reported a framing refusal (null = load the iframe).
  const [refused, setRefused] = useState<string | null>(null);

  const current = history[idx] ?? '';
  // Reflect back/forward moves in the address bar (a typed edit stays until Enter).
  useEffect(() => setAddress(current), [current]);

  // Preflight every navigation (and every manual reload, so a retry works):
  // a refused frame is detected, not guessed (#322). Best-effort — a failed or
  // unavailable check leaves the iframe to try.
  useEffect(() => {
    setRefused(null);
    let dead = false;
    void frameCheck(current).then((r) => {
      if (!dead && r) setRefused(current);
    });
    return () => {
      dead = true;
    };
  }, [current, nonce]);

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
      <div className="browser-frame-wrap">
        {refused !== null && refused === current ? (
          <div className="browser-refused">
            <p className="muted">{t('read.browserFrameRefused').replace('{host}', hostOf(current))}</p>
            <button className="browser-nav" onClick={() => openExternal(current)}>
              <Icon name="external" /> {t('read.openExternal')}
            </button>
          </div>
        ) : (
          <iframe
            key={`${idx}:${nonce}`}
            className="browser-frame"
            title={hostOf(current)}
            src={current}
            sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
            referrerPolicy="no-referrer"
          />
        )}
      </div>
    </div>
  );
}
