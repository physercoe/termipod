import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { Modal } from '../ui/Modal';
import { PasswordInput } from '../ui/PasswordInput';
import {
  getS3Secret,
  getWorkspaceSyncPassword,
  loadS3Config,
  loadSyncBackend,
  loadWorkspaceSyncConfig,
  saveS3Config,
  saveSyncBackend,
  saveWorkspaceSyncConfig,
  setS3Secret,
  setWorkspaceSyncPassword,
  verifyS3Sync,
  verifyWorkspaceSync,
  type S3Config,
  type SyncBackend,
} from '../state/workspaceSync';
import { useSyncJob } from '../state/syncJob';

/// Sync config + actions for the Author **workspace** folder (Obsidian-vault
/// style). A modal (WebView2 blocks `window.prompt`). Pick a backend (WebDAV or
/// S3), configure it, Verify, then Sync — a two-way, never-delete tree mirror in
/// the Rust core (`foldersync.rs` / `s3.rs`). `root` is the open workspace folder;
/// sync is disabled until one is set. The actual transfer runs as a **background
/// job** (`useSyncJob`), so this dialog can be closed while a large vault syncs;
/// the file tree refreshes on completion via the workspace store.

type Status =
  | { t: 'idle' }
  | { t: 'busy'; msg: string }
  | { t: 'ok'; msg: string }
  | { t: 'err'; msg: string };

const EMPTY_S3: S3Config = { endpoint: '', region: '', bucket: '', prefix: '', accessKeyId: '' };

export function WorkspaceSyncModal({
  root,
  onClose,
}: {
  root: string | null;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const job = useSyncJob();
  const [backend, setBackend] = useState<SyncBackend>('webdav');
  const [url, setUrl] = useState('');
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [s3, setS3] = useState<S3Config>(EMPTY_S3);
  const [s3Secret, setS3SecretVal] = useState('');
  const [status, setStatus] = useState<Status>({ t: 'idle' });

  useEffect(() => {
    setBackend(loadSyncBackend());
    const cfg = loadWorkspaceSyncConfig();
    setUrl(cfg.url);
    setUser(cfg.user);
    setS3(loadS3Config());
    void getWorkspaceSyncPassword().then(setPass);
    void getS3Secret().then(setS3SecretVal);
  }, []);

  const setS3Field = (k: keyof S3Config, v: string): void => setS3((c) => ({ ...c, [k]: v }));

  async function persist(): Promise<void> {
    saveSyncBackend(backend);
    if (backend === 's3') {
      saveS3Config(s3);
      await setS3Secret(s3Secret);
    } else {
      saveWorkspaceSyncConfig(url, user);
      await setWorkspaceSyncPassword(pass);
    }
  }

  async function onVerify(): Promise<void> {
    setStatus({ t: 'busy', msg: t('author.syncVerifying') });
    try {
      await persist();
      if (backend === 's3') await verifyS3Sync(s3, s3Secret);
      else await verifyWorkspaceSync(url, user, pass);
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

  // Kick the sync as a BACKGROUND job and return immediately — the dialog can be
  // closed and the user can keep working while a large vault transfers.
  async function onSync(): Promise<void> {
    if (root === null) return;
    try {
      await persist();
      setStatus({ t: 'idle' });
      useSyncJob.getState().start(root);
    } catch (e) {
      setStatus({ t: 'err', msg: e instanceof Error ? e.message : String(e) });
    }
  }

  const busy = status.t === 'busy';
  const configured = backend === 's3' ? s3.bucket.trim() !== '' : url.trim() !== '';
  const canAct = configured && !busy;
  const canSync = canAct && root !== null && !job.running;

  return (
    <Modal onClose={onClose} className="webdav-modal" ariaLabel={t('author.syncTitle')}>
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

        <div className="sync-backend-tabs">
          {(['webdav', 's3'] as const).map((b) => (
            <button
              key={b}
              className={`sync-backend-tab${backend === b ? ' active' : ''}`}
              onClick={() => {
                setBackend(b);
                setStatus({ t: 'idle' });
              }}
            >
              {t(b === 's3' ? 'author.syncBackendS3' : 'author.syncBackendWebdav')}
            </button>
          ))}
        </div>

        {backend === 'webdav' ? (
          <>
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
              <PasswordInput value={pass} autoComplete="off" onChange={(e) => setPass(e.target.value)} />
            </label>
          </>
        ) : (
          <>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Bucket')}</span>
              <input autoFocus value={s3.bucket} spellCheck={false} placeholder="my-vault-bucket" onChange={(e) => setS3Field('bucket', e.target.value)} />
            </label>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Region')}</span>
              <input value={s3.region} spellCheck={false} autoComplete="off" placeholder="us-east-1" onChange={(e) => setS3Field('region', e.target.value)} />
            </label>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Endpoint')}</span>
              <input value={s3.endpoint} spellCheck={false} autoComplete="off" placeholder={t('author.s3EndpointHint')} onChange={(e) => setS3Field('endpoint', e.target.value)} />
            </label>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Prefix')}</span>
              <input value={s3.prefix} spellCheck={false} autoComplete="off" placeholder="vault/" onChange={(e) => setS3Field('prefix', e.target.value)} />
            </label>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Access')}</span>
              <input value={s3.accessKeyId} spellCheck={false} autoComplete="off" onChange={(e) => setS3Field('accessKeyId', e.target.value)} />
            </label>
            <label className="webdav-field">
              <span className="webdav-label">{t('author.s3Secret')}</span>
              <PasswordInput value={s3Secret} autoComplete="off" onChange={(e) => setS3SecretVal(e.target.value)} />
            </label>
          </>
        )}

        {status.t === 'busy' && <div className="webdav-status busy">{status.msg}</div>}
        {status.t === 'ok' && <div className="webdav-status ok">{status.msg}</div>}
        {status.t === 'err' && <div className="webdav-status err">{status.msg}</div>}

        {/* Background sync job state (survives closing this dialog). */}
        {job.running && <div className="webdav-status busy">{t('author.syncBackground')}</div>}
        {!job.running && job.error !== null && <div className="webdav-status err">{job.error}</div>}
        {!job.running && job.report !== null && (
          <div className="webdav-status ok webdav-report">
            <div>
              {t('read.webdavDone')
                .replace('{up}', String(job.report.uploaded))
                .replace('{down}', String(job.report.downloaded))
                .replace('{skip}', String(job.report.skipped))}
            </div>
            {job.report.conflicts > 0 && (
              <div className="webdav-warn">
                {t.plural('read.webdavConflicts', job.report.conflicts)}
              </div>
            )}
            {job.report.errors.length > 0 && (
              <ul className="webdav-errs">
                {job.report.errors.slice(0, 6).map((e, i) => (
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
            {job.running ? t('author.syncSyncing') : t('read.webdavSync')}
          </button>
        </div>
        {job.running && <p className="muted small webdav-hint">{t('author.syncBackgroundHint')}</p>}
    </Modal>
  );
}
