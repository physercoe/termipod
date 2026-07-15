import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import {
  getWorkspaceSyncPassword,
  loadWorkspaceSyncConfig,
  saveWorkspaceSyncConfig,
  setWorkspaceSyncPassword,
  syncWorkspace,
  verifyWorkspaceSync,
  type FolderSyncReport,
} from '../state/workspaceSync';

/// WebDAV sync config + actions for the Author **workspace** folder
/// (Obsidian-vault style). A modal (WebView2 blocks `window.prompt`). Configure
/// the server, Verify auth, then Sync — a two-way, never-delete tree mirror in
/// the Rust core (`foldersync.rs`). `root` is the open workspace folder; sync is
/// disabled until one is set. On a run that pulls files down, `onSynced` lets the
/// caller refresh the file tree.

type Status =
  | { t: 'idle' }
  | { t: 'busy'; msg: string }
  | { t: 'ok'; msg: string }
  | { t: 'err'; msg: string }
  | { t: 'report'; report: FolderSyncReport };

export function WorkspaceSyncModal({
  root,
  onClose,
  onSynced,
}: {
  root: string | null;
  onClose: () => void;
  onSynced?: () => void;
}): JSX.Element {
  const t = useT();
  const [url, setUrl] = useState('');
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [status, setStatus] = useState<Status>({ t: 'idle' });

  useEffect(() => {
    const cfg = loadWorkspaceSyncConfig();
    setUrl(cfg.url);
    setUser(cfg.user);
    void getWorkspaceSyncPassword().then(setPass);
  }, []);

  async function persist(): Promise<void> {
    saveWorkspaceSyncConfig(url, user);
    await setWorkspaceSyncPassword(pass);
  }

  async function onVerify(): Promise<void> {
    setStatus({ t: 'busy', msg: t('author.syncVerifying') });
    try {
      await persist();
      await verifyWorkspaceSync(url, user, pass);
      setStatus({ t: 'ok', msg: t('author.syncVerified') });
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  async function onSave(): Promise<void> {
    try {
      await persist();
      setStatus({ t: 'ok', msg: t('author.syncSaved') });
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  async function onSync(): Promise<void> {
    if (root === null) return;
    setStatus({ t: 'busy', msg: t('author.syncSyncing') });
    try {
      await persist();
      const report = await syncWorkspace(root);
      setStatus({ t: 'report', report });
      if (report.downloaded > 0) onSynced?.();
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  const busy = status.t === 'busy';
  const canAct = url.trim() !== '' && !busy;
  const canSync = canAct && root !== null;

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="webdav-modal" onMouseDown={(e) => e.stopPropagation()}>
        <div className="webdav-head">
          <Icon name="cloud" size={16} />
          <strong>{t('author.syncTitle')}</strong>
          <span className="spacer" />
          <button className="link-btn" title={t('common.cancel')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        <p className="muted small webdav-hint">{t('author.syncHint')}</p>
        {root === null && <div className="webdav-status err">{t('author.syncNoFolder')}</div>}

        <label className="webdav-field">
          <span className="webdav-label">{t('read.webdavUrl')}</span>
          <input
            autoFocus
            value={url}
            spellCheck={false}
            placeholder="https://dav.example.com/vault/"
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
          <button className="primary" disabled={!canSync} onClick={() => void onSync()}>
            {t('read.webdavSync')}
          </button>
        </div>
      </div>
    </div>
  );
}
