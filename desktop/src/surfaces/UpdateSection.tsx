import { useEffect, useState } from 'react';
import { getVersion } from '@tauri-apps/api/app';
import { invoke } from '@tauri-apps/api/core';
import { relaunch } from '@tauri-apps/plugin-process';
import { check, type Update } from '@tauri-apps/plugin-updater';
import { useT } from '../i18n';
import { isTauri } from '../platform';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

const PROXY_KEY = 'termipod.update.proxy';
// Cache of the last auto-detected system/env proxy, so it paints on the first
// frame instead of flashing in after the async `system_proxy` invoke resolves
// (director feedback: proxy line splashed in on every Settings open).
const DETECTED_KEY = 'termipod.update.proxy.detected';

function readCache(key: string): string | null {
  try {
    const v = localStorage.getItem(key);
    return v !== null && v !== '' ? v : null;
  } catch {
    return null;
  }
}

/** A "couldn't reach the server" transport failure (vs. an HTTP/parse error). */
function looksLikeNetwork(m: string): boolean {
  return /error sending request|failed to fetch|dns|connect|timed out|timeout|network/i.test(m);
}

type State =
  | { s: 'idle' }
  | { s: 'checking' }
  | { s: 'uptodate' }
  | { s: 'available'; update: Update }
  | { s: 'downloading'; pct: number | null }
  | { s: 'installing' }
  | { s: 'error'; msg: string };

/// Settings → Software update (ADR-052 sibling; WS8). Uses the Tauri updater
/// plugin to check the signed latest.json on the GitHub release, then
/// download + install + relaunch. Desktop-only; the browser build renders
/// nothing (no native core).
export function UpdateSection(): JSX.Element | null {
  const t = useT();
  const [st, setSt] = useState<State>({ s: 'idle' });
  // Seed from the build-time version so it paints on the first frame (no splash);
  // the effect refreshes it from the Tauri runtime, which matches.
  const [current, setCurrent] = useState(__APP_VERSION__);
  // Manual override the user typed (persisted); '' = auto-detect. Seeded
  // synchronously from localStorage so a saved proxy paints on first frame
  // (no flash-in after the effect runs).
  const [proxyOverride, setProxyOverride] = useState<string>(() => readCache(PROXY_KEY) ?? '');
  // What auto-detect (env vars / Windows system proxy) found, for display.
  // Seeded synchronously from the last-detected cache so a known proxy paints on
  // the first frame; the effect below refreshes it (usually the same value, so
  // no visible change) and re-caches.
  const [detected, setDetected] = useState<string | null>(() => readCache(DETECTED_KEY));
  const [showProxy, setShowProxy] = useState(false);

  useEffect(() => {
    if (!isTauri()) return;
    void getVersion()
      .then(setCurrent)
      .catch(() => {});
    void invoke<string | null>('system_proxy')
      .then((p) => {
        const val = p ?? null;
        setDetected(val);
        try {
          if (val !== null && val !== '') localStorage.setItem(DETECTED_KEY, val);
          else localStorage.removeItem(DETECTED_KEY);
        } catch {
          /* storage unavailable — display still works from state */
        }
      })
      .catch(() => setDetected(null));
  }, []);

  if (!isTauri()) return null;

  /** Manual override wins; otherwise the auto-detected system/env proxy. */
  function effectiveProxy(): string | undefined {
    const o = proxyOverride.trim();
    if (o) return o;
    return detected ?? undefined;
  }

  function saveOverride(v: string): void {
    setProxyOverride(v);
    if (v.trim()) localStorage.setItem(PROXY_KEY, v.trim());
    else localStorage.removeItem(PROXY_KEY);
  }

  async function checkNow(): Promise<void> {
    setSt({ s: 'checking' });
    const proxy = effectiveProxy();
    try {
      const update = await check(proxy ? { proxy } : undefined);
      setSt(update === null ? { s: 'uptodate' } : { s: 'available', update });
    } catch (e) {
      const m = msg(e);
      // A transport failure with no proxy in play on a corporate intranet is
      // almost always the missing system-proxy route — surface the hint.
      if (!proxy && looksLikeNetwork(m)) setShowProxy(true);
      setSt({ s: 'error', msg: m });
    }
  }

  async function install(update: Update): Promise<void> {
    let total = 0;
    let got = 0;
    setSt({ s: 'downloading', pct: null });
    try {
      await update.downloadAndInstall((ev) => {
        if (ev.event === 'Started') total = ev.data.contentLength ?? 0;
        else if (ev.event === 'Progress') {
          got += ev.data.chunkLength;
          setSt({ s: 'downloading', pct: total > 0 ? Math.round((got / total) * 100) : null });
        } else if (ev.event === 'Finished') setSt({ s: 'installing' });
      });
      await relaunch();
    } catch (e) {
      setSt({ s: 'error', msg: msg(e) });
    }
  }

  return (
    <section className="setting-group">
      <h3>{t('settings.update')}</h3>
      <div className="setting-row">
        <label>{t('update.current')}</label>
        <span className="muted">{current || '—'}</span>
      </div>

      {st.s === 'available' ? (
        <div className="update-avail">
          <div>
            {t('update.available')} <strong>{st.update.version}</strong>
          </div>
          {st.update.body !== undefined && st.update.body !== '' && (
            <pre className="mono update-notes">{st.update.body}</pre>
          )}
          <div className="setting-row">
            <span className="spacer" />
            <button className="primary" onClick={() => void install(st.update)}>
              {t('update.install')}
            </button>
          </div>
        </div>
      ) : (
        <div className="setting-row">
          <span className="muted">
            {st.s === 'checking' && t('update.checking')}
            {st.s === 'uptodate' && t('update.upToDate')}
            {st.s === 'downloading' && `${t('update.downloading')} ${st.pct !== null ? `${st.pct}%` : ''}`}
            {st.s === 'installing' && t('update.restarting')}
            {st.s === 'error' && (
              <span className="error">
                {t('update.error')} {st.msg}
              </span>
            )}
          </span>
          <button
            onClick={() => void checkNow()}
            disabled={st.s === 'checking' || st.s === 'downloading' || st.s === 'installing'}
          >
            {t('update.check')}
          </button>
        </div>
      )}

      <div className="setting-row">
        <button className="link-btn" onClick={() => setShowProxy((v) => !v)}>
          {showProxy ? t('update.proxy.hide') : t('update.proxy.show')}
        </button>
        {effectiveProxy() && <span className="muted">{effectiveProxy()}</span>}
      </div>
      {showProxy && (
        <div className="update-proxy">
          <p className="muted small">{t('update.proxy.help')}</p>
          <div className="setting-row">
            <input
              type="text"
              placeholder={detected ?? 'http://proxy.corp:8080'}
              value={proxyOverride}
              onChange={(e) => saveOverride(e.target.value)}
              spellCheck={false}
            />
          </div>
          <p className="muted small">
            {detected
              ? `${t('update.proxy.detected')} ${detected}`
              : t('update.proxy.none')}
          </p>
        </div>
      )}
    </section>
  );
}
