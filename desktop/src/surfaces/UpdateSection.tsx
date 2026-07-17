import { useEffect, useState } from 'react';
import { getVersion } from '@tauri-apps/api/app';
import { relaunch } from '@tauri-apps/plugin-process';
import { check, type Update } from '@tauri-apps/plugin-updater';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { proxyForConnection } from '../state/proxy';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
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
  | { s: 'error'; msg: string; network: boolean };

/// Settings → Software update (ADR-052 sibling; WS8). Uses the Tauri updater
/// plugin to check the signed latest.json on the GitHub release, then
/// download + install + relaunch. Desktop-only; the browser build renders
/// nothing (no native core). The proxy is the shared Network-tab config
/// (`proxyForConnection('update')`) — no longer configured inline here.
export function UpdateSection(): JSX.Element | null {
  const t = useT();
  const [st, setSt] = useState<State>({ s: 'idle' });
  // Seed from the build-time version so it paints on the first frame (no splash);
  // the effect refreshes it from the Tauri runtime, which matches.
  const [current, setCurrent] = useState(__APP_VERSION__);

  useEffect(() => {
    if (!isTauri()) return;
    void getVersion()
      .then(setCurrent)
      .catch(() => {});
  }, []);

  if (!isTauri()) return null;

  async function checkNow(): Promise<void> {
    setSt({ s: 'checking' });
    const proxy = proxyForConnection('update');
    try {
      const update = await check(proxy ? { proxy } : undefined);
      setSt(update === null ? { s: 'uptodate' } : { s: 'available', update });
    } catch (e) {
      const m = msg(e);
      // A transport failure with no proxy in play is often the missing proxy
      // route on a corporate network — point at the Network tab.
      setSt({ s: 'error', msg: m, network: !proxy && looksLikeNetwork(m) });
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
      setSt({ s: 'error', msg: msg(e), network: false });
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
      {st.s === 'error' && st.network && (
        <p className="muted small">{t('update.proxy.networkHint')}</p>
      )}
    </section>
  );
}
