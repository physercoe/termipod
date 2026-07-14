import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import {
  getWebdavPassword,
  loadWebdavConfig,
  saveWebdavConfig,
  setWebdavPassword,
  syncWebdav,
  verifyWebdav,
  type SyncReport,
} from '../state/webdav';

/// WebDAV file-sync config + actions for the Read surface (Zotero-compatible).
/// A modal (WebView2 blocks `window.prompt`, so we render our own — see
/// PromptModal). Configure the server, Verify write access, then Sync files;
/// both directions run in the Rust core (`webdav.rs`).

type Status =
  | { t: 'idle' }
  | { t: 'busy'; msg: string }
  | { t: 'ok'; msg: string }
  | { t: 'err'; msg: string }
  | { t: 'report'; report: SyncReport };

export function WebdavModal({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const [url, setUrl] = useState('');
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [status, setStatus] = useState<Status>({ t: 'idle' });

  useEffect(() => {
    const cfg = loadWebdavConfig();
    setUrl(cfg.url);
    setUser(cfg.user);
    void getWebdavPassword().then(setPass);
  }, []);

  // Persist config (url/user → localStorage, password → keychain) before an action.
  async function persist(): Promise<void> {
    saveWebdavConfig(url, user);
    await setWebdavPassword(pass);
  }

  async function onVerify(): Promise<void> {
    setStatus({ t: 'busy', msg: t('read.webdavVerifying') });
    try {
      await persist();
      await verifyWebdav(url, user, pass);
      setStatus({ t: 'ok', msg: t('read.webdavVerified') });
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  async function onSave(): Promise<void> {
    try {
      await persist();
      setStatus({ t: 'ok', msg: t('read.webdavSaved') });
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  async function onSync(): Promise<void> {
    setStatus({ t: 'busy', msg: t('read.webdavSyncing') });
    try {
      await persist();
      const report = await syncWebdav();
      setStatus({ t: 'report', report });
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  const busy = status.t === 'busy';
  const canAct = url.trim() !== '' && !busy;

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="webdav-modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="webdav-head">
          <Icon name="cloud" size={16} />
          <strong>{t('read.webdavTitle')}</strong>
          <span className="spacer" />
          <button className="link-btn" title={t('common.cancel')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        <p className="muted small webdav-hint">{t('read.webdavHint')}</p>

        <label className="webdav-field">
          <span className="webdav-label">{t('read.webdavUrl')}</span>
          <input
            autoFocus
            value={url}
            spellCheck={false}
            placeholder="https://dav.example.com/"
            onChange={(e) => setUrl(e.target.value)}
          />
        </label>
        <label className="webdav-field">
          <span className="webdav-label">{t('read.webdavUser')}</span>
          <input value={user} spellCheck={false} autoComplete="off" onChange={(e) => setUser(e.target.value)} />
        </label>
        <label className="webdav-field">
          <span className="webdav-label">{t('read.webdavPass')}</span>
          <input type="password" value={pass} autoComplete="off" onChange={(e) => setPass(e.target.value)} />
        </label>

        {status.t === 'busy' && <div className="webdav-status busy">{status.msg}</div>}
        {status.t === 'ok' && <div className="webdav-status ok">{status.msg}</div>}
        {status.t === 'err' && <div className="webdav-status err">{status.msg}</div>}
        {status.t === 'report' && (
          <div className="webdav-status ok webdav-report">
            <div>
              {t('read.webdavDone')
                .replace('{up}', String(status.report.uploaded))
                .replace('{down}', String(status.report.downloaded))
                .replace('{skip}', String(status.report.skipped))}
            </div>
            {status.report.conflicts > 0 && (
              <div className="webdav-warn">
                {t('read.webdavConflicts').replace('{n}', String(status.report.conflicts))}
              </div>
            )}
            {status.report.errors.length > 0 && (
              <ul className="webdav-errs">
                {status.report.errors.slice(0, 6).map((e, i) => (
                  <li key={i}>{e}</li>
                ))}
              </ul>
            )}
          </div>
        )}

        <div className="webdav-actions">
          <button disabled={!canAct} onClick={() => void onVerify()}>
            {t('read.webdavVerify')}
          </button>
          <button disabled={busy} onClick={() => void onSave()}>
            {t('read.webdavSave')}
          </button>
          <span className="spacer" />
          <button className="primary" disabled={!canAct} onClick={() => void onSync()}>
            {t('read.webdavSync')}
          </button>
        </div>
      </div>
    </div>
  );
}
