import { useCallback, useEffect, useRef, useState } from 'react';
import { sftpList, sftpRead, sftpWrite, type SftpEntry } from '../ssh/tauri';
import { useT } from '../i18n';

/// SFTP file-transfer panel (parity — mobile file_transfer_provider + remote
/// file browser). Browses a remote directory over the session's SFTP subsystem,
/// downloads a file (bytes → browser download) and uploads a picked file
/// (base64 → sftp_write into the current directory).
function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
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
  const fileRef = useRef<HTMLInputElement>(null);

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
    try {
      const b64 = await sftpRead(sessionId, joinPath(dir, name));
      downloadBytes(name, b64);
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  async function upload(file: File): Promise<void> {
    setBusy(true);
    setErr(null);
    try {
      const b64 = await fileToBase64(file);
      await sftpWrite(sessionId, joinPath(dir, file.name), b64);
      await load(dir);
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
      if (fileRef.current !== null) fileRef.current.value = '';
    }
  }

  return (
    <div className="sftp-panel">
      <div className="sftp-bar">
        <button disabled={busy} onClick={() => setDir(parentPath(dir))} title={t('sftp.up')}>
          ↑
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
      <div className="sftp-list scroll">
        {entries.map((e) => (
          <div key={e.name} className="sftp-row">
            <button
              className="sftp-name-btn"
              disabled={busy}
              onClick={() => (e.is_dir ? setDir(joinPath(dir, e.name)) : void download(e.name))}
            >
              <span className="sftp-icon">{e.is_dir ? '📁' : '📄'}</span>
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
