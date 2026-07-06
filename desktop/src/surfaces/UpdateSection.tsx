import { useEffect, useState } from 'react';
import { getVersion } from '@tauri-apps/api/app';
import { relaunch } from '@tauri-apps/plugin-process';
import { check, type Update } from '@tauri-apps/plugin-updater';
import { useT } from '../i18n';
import { isTauri } from '../platform';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
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
  const [current, setCurrent] = useState('');

  useEffect(() => {
    if (!isTauri()) return;
    void getVersion()
      .then(setCurrent)
      .catch(() => {});
  }, []);

  if (!isTauri()) return null;

  async function checkNow(): Promise<void> {
    setSt({ s: 'checking' });
    try {
      const update = await check();
      setSt(update === null ? { s: 'uptodate' } : { s: 'available', update });
    } catch (e) {
      setSt({ s: 'error', msg: msg(e) });
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
    </section>
  );
}
