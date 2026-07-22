import { useCallback, useEffect, useRef, useState } from 'react';
import { onSftpProgress, sftpList, sftpRead, sftpWrite, type SftpEntry } from '../ssh/native';
import { localHome, localList, localRead, localWrite, type LocalEntry, type LocalListing } from '../state/localfs';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { useConfirm } from '../ui/ConfirmModal';

/// Two-pane file transfer (FileZilla-style): the local machine on the left, the
/// remote host (over the session's SFTP subsystem) on the right. Browse either
/// side; transfer a file with one click — upload (local → remote) drops it into
/// the remote pane's current directory, download (remote → local) drops it into
/// the local pane's current directory. Transfers stream in chunks with a live
/// progress bar off the `sftp-progress` events.
function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

interface Transfer {
  id: string;
  name: string;
  dir: 'up' | 'down';
  done: number;
  total: number;
  status: 'active' | 'done' | 'error';
  error?: string;
}

// Unique transfer id via crypto.randomUUID — available now the renderer serves
// from the secure `app://` origin (ADR-055 §7 row 12).
function nextTransferId(): string {
  return `tx${crypto.randomUUID()}`;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

// Remote paths are always POSIX ('/'); local targets use the listed absolute dir
// plus '/' + name — Rust's std::fs accepts forward slashes on Windows too.
function joinPosix(dir: string, name: string): string {
  if (dir === '/') return `/${name}`;
  return `${dir.replace(/\/$/, '')}/${name}`;
}
function parentPosix(dir: string): string {
  if (dir === '/' || dir === '') return '/';
  const trimmed = dir.replace(/\/$/, '');
  const idx = trimmed.lastIndexOf('/');
  return idx <= 0 ? '/' : trimmed.slice(0, idx);
}

export function FileTransferPanel({ sessionId }: { sessionId: string }): JSX.Element {
  const t = useT();
  const confirm = useConfirm();
  // Remote (SFTP) pane.
  const [rdir, setRdir] = useState('.');
  const [rentries, setREntries] = useState<SftpEntry[]>([]);
  const [rbusy, setRBusy] = useState(false);
  // Local pane — `local` carries the absolute path + parent so navigation never
  // re-joins paths client-side; `lpath` is the editable path field.
  const [local, setLocal] = useState<LocalListing | null>(null);
  const [lpath, setLpath] = useState('');
  const [lbusy, setLBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [transfer, setTransfer] = useState<Transfer | null>(null);
  const clearTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const settle = useCallback((id: string, patch: Partial<Transfer>): void => {
    setTransfer((prev) => (prev !== null && prev.id === id ? { ...prev, ...patch } : prev));
    if (patch.status === 'done') {
      if (clearTimer.current !== undefined) clearTimeout(clearTimer.current);
      clearTimer.current = setTimeout(() => {
        setTransfer((prev) => (prev !== null && prev.id === id ? null : prev));
      }, 4000);
    }
  }, []);

  useEffect(
    () => () => {
      if (clearTimer.current !== undefined) clearTimeout(clearTimer.current);
    },
    [],
  );

  const loadRemote = useCallback(
    async (path: string): Promise<void> => {
      setRBusy(true);
      setErr(null);
      try {
        setREntries(await sftpList(sessionId, path));
      } catch (e) {
        setErr(msg(e));
      } finally {
        setRBusy(false);
      }
    },
    [sessionId],
  );

  const loadLocal = useCallback(async (path: string): Promise<void> => {
    setLBusy(true);
    setErr(null);
    try {
      const listing = await localList(path);
      setLocal(listing);
      setLpath(listing.path);
    } catch (e) {
      setErr(msg(e));
    } finally {
      setLBusy(false);
    }
  }, []);

  useEffect(() => {
    void loadRemote(rdir);
  }, [rdir, loadRemote]);

  // Seed the local pane at the user's home on mount.
  useEffect(() => {
    void localHome()
      .then((h) => loadLocal(h))
      .catch((e) => setErr(msg(e)));
  }, [loadLocal]);

  // A transfer would clobber an existing file — confirm first. The destination
  // directory is already listed in memory (what the user is looking at), so the
  // existence check needs no extra round-trip and matches the visible pane.
  async function confirmOverwrite(name: string, exists: boolean): Promise<boolean> {
    if (!exists) return true;
    return confirm.ask({
      message: t('sftp.overwrite').replace('{name}', name),
      confirmLabel: t('sftp.overwriteConfirm'),
      danger: true,
    });
  }

  // Download: remote → the local pane's current directory.
  async function download(entry: SftpEntry): Promise<void> {
    if (local === null) return;
    const exists = local.entries.some((e) => e.name === entry.name && !e.is_dir);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    setTransfer({ id: tid, name: entry.name, dir: 'down', done: 0, total: entry.size, status: 'active' });
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done }));
    try {
      const bytes = await sftpRead(sessionId, joinPosix(rdir, entry.name), tid);
      await localWrite(joinPosix(local.path, entry.name), bytes);
      settle(tid, { status: 'done', done: entry.size > 0 ? entry.size : 0 });
      await loadLocal(local.path);
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
    }
  }

  // Upload: a local file → the remote pane's current directory.
  async function upload(entry: LocalEntry): Promise<void> {
    const exists = rentries.some((e) => e.name === entry.name && !e.is_dir);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    setTransfer({ id: tid, name: entry.name, dir: 'up', done: 0, total: entry.size, status: 'active' });
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done }));
    try {
      const bytes = await localRead(entry.path);
      await sftpWrite(sessionId, joinPosix(rdir, entry.name), bytes, tid);
      settle(tid, { status: 'done', done: entry.size });
      await loadRemote(rdir);
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
    }
  }

  return (
    <div className="sftp-panel">
      {confirm.node}
      {err !== null && <div className="error sftp-err">{err}</div>}
      {transfer !== null && (
        <div className={`sftp-transfer ${transfer.status}`}>
          <div className="sftp-transfer-head">
            <span className="sftp-transfer-dir">{transfer.dir === 'up' ? '⬆' : '⬇'}</span>
            <span className="sftp-transfer-name mono">{transfer.name}</span>
            <span className="spacer" />
            <span className="muted small">
              {transfer.status === 'error'
                ? t('sftp.failed')
                : transfer.status === 'done'
                  ? t('sftp.done')
                  : transfer.total > 0
                    ? `${formatBytes(transfer.done)} / ${formatBytes(transfer.total)}`
                    : formatBytes(transfer.done)}
            </span>
          </div>
          <div className="sftp-progress-track">
            <div
              className={`sftp-progress-fill ${transfer.total === 0 && transfer.status === 'active' ? 'indeterminate' : ''}`}
              style={
                transfer.total > 0
                  ? { width: `${Math.min(100, Math.round((transfer.done / transfer.total) * 100))}%` }
                  : transfer.status !== 'active'
                    ? { width: '100%' }
                    : undefined
              }
            />
          </div>
          {transfer.status === 'error' && transfer.error !== undefined && (
            <div className="error small">{transfer.error}</div>
          )}
        </div>
      )}

      <div className="sftp-dual">
        {/* Local pane */}
        <div className="sftp-pane">
          <div className="sftp-pane-head">{t('sftp.local')}</div>
          <div className="sftp-bar">
            <button
              disabled={lbusy || local?.parent == null}
              onClick={() => local?.parent != null && void loadLocal(local.parent)}
              title={t('sftp.up')}
            >
              <Icon name="chevron-up" />
            </button>
            <input
              className="sftp-path mono"
              value={lpath}
              onChange={(e) => setLpath(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void loadLocal(lpath);
              }}
            />
            <button disabled={lbusy} onClick={() => void loadLocal(lpath)}>
              {t('sftp.go')}
            </button>
          </div>
          <div className="sftp-list scroll">
            {(local?.entries ?? []).map((e) => (
              <div key={e.path} className="sftp-row">
                <button
                  className="sftp-name-btn"
                  disabled={busy}
                  onClick={() => (e.is_dir ? void loadLocal(e.path) : void upload(e))}
                >
                  <Icon name={e.is_dir ? 'folder' : 'file-text'} size={15} className="sftp-icon" />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="spacer" />
                <span className="muted small">{e.is_dir ? '' : formatBytes(e.size)}</span>
                {!e.is_dir && (
                  <button className="link-btn" disabled={busy} title={t('sftp.upload')} onClick={() => void upload(e)}>
                    {t('sftp.toRemote')} <Icon name="chevron-right" size={13} />
                  </button>
                )}
              </div>
            ))}
            {!lbusy && (local?.entries.length ?? 0) === 0 && (
              <div className="muted small region-pad">{t('sftp.empty')}</div>
            )}
          </div>
        </div>

        {/* Remote pane */}
        <div className="sftp-pane">
          <div className="sftp-pane-head">{t('sftp.remote')}</div>
          <div className="sftp-bar">
            <button disabled={rbusy} onClick={() => setRdir(parentPosix(rdir))} title={t('sftp.up')}>
              <Icon name="chevron-up" />
            </button>
            <input
              className="sftp-path mono"
              value={rdir}
              onChange={(e) => setRdir(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void loadRemote(rdir);
              }}
            />
            <button disabled={rbusy} onClick={() => void loadRemote(rdir)}>
              {t('sftp.go')}
            </button>
          </div>
          <div className="sftp-list scroll">
            {rentries.map((e) => (
              <div key={e.name} className="sftp-row">
                <button
                  className="sftp-name-btn"
                  disabled={busy}
                  onClick={() => (e.is_dir ? setRdir(joinPosix(rdir, e.name)) : void download(e))}
                >
                  <Icon name={e.is_dir ? 'folder' : 'file-text'} size={15} className="sftp-icon" />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="spacer" />
                <span className="muted small">{e.is_dir ? '' : formatBytes(e.size)}</span>
                {!e.is_dir && (
                  <button className="link-btn" disabled={busy} title={t('sftp.download')} onClick={() => void download(e)}>
                    <Icon name="chevron-left" size={13} /> {t('sftp.toLocal')}
                  </button>
                )}
              </div>
            ))}
            {!rbusy && rentries.length === 0 && <div className="muted small region-pad">{t('sftp.empty')}</div>}
          </div>
        </div>
      </div>
    </div>
  );
}
