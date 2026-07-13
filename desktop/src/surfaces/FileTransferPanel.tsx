import { useCallback, useEffect, useRef, useState } from 'react';
import { onSftpProgress, sftpList, sftpRead, sftpWrite, type SftpEntry } from '../ssh/tauri';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';

/// SFTP file-transfer panel (parity — mobile file_transfer_provider + remote
/// file browser). Browses a remote directory over the session's SFTP subsystem,
/// downloads a file (bytes → browser download) and uploads a picked file
/// (base64 → sftp_write into the current directory). Transfers stream in chunks
/// and surface a live progress bar via `sftp-progress` events.
function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** A transfer in flight (or its just-finished terminal state), for the bar. */
interface Transfer {
  id: string;
  name: string;
  dir: 'up' | 'down';
  done: number;
  total: number;
  status: 'active' | 'done' | 'error';
  error?: string;
}

// Monotonic transfer id — unique within the session, no secure-context
// dependency (crypto.randomUUID isn't guaranteed under the tauri:// scheme).
let txSeq = 0;
function nextTransferId(): string {
  txSeq += 1;
  return `tx${txSeq}`;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function joinPath(dir: string, name: string): string {
  if (dir === '/' ) return `/${name}`;
  return `${dir.replace(/\/$/, '')}/${name}`;
}
function parentPath(dir: string): string {
  if (dir === '/' || dir === '') return '/';
  const trimmed = dir.replace(/\/$/, '');
  const idx = trimmed.lastIndexOf('/');
  return idx <= 0 ? '/' : trimmed.slice(0, idx);
}

async function fileToBase64(file: File): Promise<string> {
  const buf = new Uint8Array(await file.arrayBuffer());
  let binary = '';
  for (let i = 0; i < buf.length; i += 1) binary += String.fromCharCode(buf[i]);
  return btoa(binary);
}

function downloadBytes(name: string, base64: string): void {
  const bin = atob(base64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  const url = URL.createObjectURL(new Blob([bytes]));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function FileTransferPanel({ sessionId }: { sessionId: string }): JSX.Element {
  const t = useT();
  const [dir, setDir] = useState('.');
  const [entries, setEntries] = useState<SftpEntry[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [transfer, setTransfer] = useState<Transfer | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const clearTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Keep a finished transfer visible briefly, then fade it; errors linger until
  // the next action so they aren't missed.
  const settle = useCallback((id: string, patch: Partial<Transfer>): void => {
    setTransfer((prev) => (prev !== null && prev.id === id ? { ...prev, ...patch } : prev));
    if (patch.status === 'done') {
      if (clearTimer.current !== undefined) clearTimeout(clearTimer.current);
      clearTimer.current = setTimeout(() => {
        setTransfer((prev) => (prev !== null && prev.id === id ? null : prev));
      }, 4000);
    }
  }, []);

  useEffect(() => () => {
    if (clearTimer.current !== undefined) clearTimeout(clearTimer.current);
  }, []);

  const load = useCallback(async (path: string): Promise<void> => {
    setBusy(true);
    setErr(null);
    try {
      setEntries(await sftpList(sessionId, path));
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }, [sessionId]);

  useEffect(() => {
    void load(dir);
  }, [dir, load]);

  async function download(name: string): Promise<void> {
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    const total = entries.find((e) => e.name === name)?.size ?? 0;
    setTransfer({ id: tid, name, dir: 'down', done: 0, total, status: 'active' });
    // Register the progress listener BEFORE the transfer so no early tick is lost.
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done }));
    try {
      const b64 = await sftpRead(sessionId, joinPath(dir, name), tid);
      downloadBytes(name, b64);
      settle(tid, { status: 'done', done: total > 0 ? total : 0 });
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
    }
  }

  async function upload(file: File): Promise<void> {
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    setTransfer({ id: tid, name: file.name, dir: 'up', done: 0, total: file.size, status: 'active' });
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done }));
    try {
      const b64 = await fileToBase64(file);
      await sftpWrite(sessionId, joinPath(dir, file.name), b64, tid);
      settle(tid, { status: 'done', done: file.size });
      await load(dir);
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
      if (fileRef.current !== null) fileRef.current.value = '';
    }
  }

  return (
    <div className="sftp-panel">
      <div className="sftp-bar">
        <button disabled={busy} onClick={() => setDir(parentPath(dir))} title={t('sftp.up')}>
          <Icon name="chevron-up" />
        </button>
        <input
          className="sftp-path mono"
          value={dir}
          onChange={(e) => setDir(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void load(dir);
          }}
        />
        <button disabled={busy} onClick={() => void load(dir)}>
          {t('sftp.go')}
        </button>
        <span className="spacer" />
        <input ref={fileRef} type="file" hidden onChange={(e) => e.target.files?.[0] && void upload(e.target.files[0])} />
        <button disabled={busy} onClick={() => fileRef.current?.click()}>
          {t('sftp.upload')}
        </button>
      </div>
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
      <div className="sftp-list scroll">
        {entries.map((e) => (
          <div key={e.name} className="sftp-row">
            <button
              className="sftp-name-btn"
              disabled={busy}
              onClick={() => (e.is_dir ? setDir(joinPath(dir, e.name)) : void download(e.name))}
            >
              <Icon name={e.is_dir ? 'folder' : 'file-text'} size={15} className="sftp-icon" />
              <span className="sftp-name">{e.name}</span>
            </button>
            <span className="spacer" />
            <span className="muted small">{e.is_dir ? '' : `${e.size} B`}</span>
            {!e.is_dir && (
              <button className="link-btn" disabled={busy} onClick={() => void download(e.name)}>
                {t('sftp.download')}
              </button>
            )}
          </div>
        ))}
        {!busy && entries.length === 0 && <div className="muted small region-pad">{t('sftp.empty')}</div>}
      </div>
    </div>
  );
}
