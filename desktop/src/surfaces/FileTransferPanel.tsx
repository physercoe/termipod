import { useCallback, useEffect, useRef, useState } from 'react';
import {
  onSftpProgress,
  sftpDelete,
  sftpList,
  sftpMkdir,
  sftpRead,
  sftpRename,
  sftpWrite,
  type SftpEntry,
} from '../ssh/native';
import {
  localDelete,
  localHome,
  localList,
  localMkdir,
  localRead,
  localRename,
  localWrite,
  type LocalEntry,
  type LocalListing,
} from '../state/localfs';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { useConfirm } from '../ui/ConfirmModal';
import { useContextMenu } from '../ui/ContextMenu';
import { useTextPrompt } from '../ui/PromptModal';
import { Modal } from '../ui/Modal';

/// Two-pane file transfer (FileZilla-style): the local machine on the left, the
/// remote host (over the session's SFTP subsystem) on the right.
///
/// Interaction model: a single left click on a row only SELECTS it — nothing
/// transfers or navigates implicitly. Double-click opens a directory (or
/// previews a file), and every action lives on the right-click context menu
/// (open/view, upload/download — files AND directories recursively — new
/// folder/file, rename, delete) plus the per-row transfer shortcut button.
/// Transfers stream in chunks with a live progress bar off the `sftp-progress`
/// events; directory transfers pre-scan the tree and report aggregate bytes
/// plus a `n/m files` note.

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
  /** Secondary detail for multi-file transfers — `n/m · relative/path`. */
  note?: string;
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
// plus '/' + name — Node's fs accepts forward slashes on Windows too.
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

/** A name the user typed for New/Rename: non-empty, no path separators. */
function nameValid(name: string): boolean {
  const n = name.trim();
  return n !== '' && n !== '.' && n !== '..' && !n.includes('/');
}

/** Files above this size are not previewed in the View modal. */
const PREVIEW_MAX = 5 * 1024 * 1024;

/** A pre-scanned directory tree: directory relpaths (for recreating empty
 *  dirs) plus file relpaths with sizes (for the aggregate progress total). */
interface DirScan {
  dirs: string[];
  files: { rel: string; size: number }[];
}

export function FileTransferPanel({ sessionId }: { sessionId: string }): JSX.Element {
  const t = useT();
  const confirm = useConfirm();
  const ctxMenu = useContextMenu();
  const prompt = useTextPrompt();
  // Remote (SFTP) pane.
  const [rdir, setRdir] = useState('.');
  const [rentries, setREntries] = useState<SftpEntry[]>([]);
  const [rbusy, setRBusy] = useState(false);
  // Local pane — `local` carries the absolute path + parent so navigation never
  // re-joins paths client-side; `lpath` is the editable path field.
  const [local, setLocal] = useState<LocalListing | null>(null);
  const [lpath, setLpath] = useState('');
  const [lbusy, setLBusy] = useState(false);
  // Selection is by NAME (what the listing shows) — purely visual, no action.
  const [selL, setSelL] = useState<string | null>(null);
  const [selR, setSelR] = useState<string | null>(null);
  const [view, setView] = useState<{ title: string; body: string } | null>(null);
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

  // A transfer would clobber an existing entry — confirm first. The destination
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

  /** Recursive pre-scan of a local directory (for upload). */
  async function scanLocalDir(root: string): Promise<DirScan> {
    const dirs: string[] = [];
    const files: { rel: string; size: number }[] = [];
    const walk = async (abs: string, rel: string): Promise<void> => {
      const listing = await localList(abs);
      for (const e of listing.entries) {
        const r = rel === '' ? e.name : `${rel}/${e.name}`;
        if (e.is_dir) {
          dirs.push(r);
          await walk(e.path, r);
        } else {
          files.push({ rel: r, size: e.size });
        }
      }
    };
    await walk(root, '');
    return { dirs, files };
  }

  /** Recursive pre-scan of a remote directory (for download). */
  async function scanRemoteDir(root: string): Promise<DirScan> {
    const dirs: string[] = [];
    const files: { rel: string; size: number }[] = [];
    const walk = async (abs: string, rel: string): Promise<void> => {
      const listing = await sftpList(sessionId, abs);
      for (const e of listing) {
        const r = rel === '' ? e.name : `${rel}/${e.name}`;
        if (e.is_dir) {
          dirs.push(r);
          await walk(joinPosix(abs, e.name), r);
        } else {
          files.push({ rel: r, size: e.size });
        }
      }
    };
    await walk(root, '');
    return { dirs, files };
  }

  // Download: remote → the local pane's current directory. Directories go
  // recursively: pre-scan, mkdir the tree, then stream each file.
  async function download(entry: SftpEntry): Promise<void> {
    if (local === null) return;
    const exists = local.entries.some((e) => e.name === entry.name);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    const src = joinPosix(rdir, entry.name);
    const dest = joinPosix(local.path, entry.name);
    // `base` = bytes finished in earlier files; the backend's progress ticks are
    // per-file cumulative, so aggregate = base + tick.
    let base = 0;
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done: base + done }));
    try {
      if (!entry.is_dir) {
        setTransfer({ id: tid, name: entry.name, dir: 'down', done: 0, total: entry.size, status: 'active' });
        const bytes = await sftpRead(sessionId, src, tid);
        await localWrite(dest, bytes);
        settle(tid, { status: 'done', done: entry.size > 0 ? entry.size : 0 });
      } else {
        setTransfer({ id: tid, name: entry.name, dir: 'down', done: 0, total: 0, status: 'active', note: t('sftp.scanning') });
        const { dirs, files } = await scanRemoteDir(src);
        const total = files.reduce((s, f) => s + f.size, 0);
        settle(tid, { total, note: undefined });
        await localMkdir(dest);
        for (const d of dirs) await localMkdir(joinPosix(dest, d));
        let i = 0;
        for (const f of files) {
          i += 1;
          settle(tid, { note: `${i}/${files.length} · ${f.rel}` });
          const bytes = await sftpRead(sessionId, joinPosix(src, f.rel), tid);
          await localWrite(joinPosix(dest, f.rel), bytes);
          base += f.size;
          settle(tid, { done: base });
        }
        settle(tid, { status: 'done', done: total, note: undefined });
      }
      await loadLocal(local.path);
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
    }
  }

  // Upload: a local file OR directory → the remote pane's current directory.
  async function upload(entry: LocalEntry): Promise<void> {
    const exists = rentries.some((e) => e.name === entry.name);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setBusy(true);
    setErr(null);
    const tid = nextTransferId();
    const dest = joinPosix(rdir, entry.name);
    let base = 0;
    const unlisten = await onSftpProgress(tid, (done) => settle(tid, { done: base + done }));
    try {
      if (!entry.is_dir) {
        setTransfer({ id: tid, name: entry.name, dir: 'up', done: 0, total: entry.size, status: 'active' });
        const bytes = await localRead(entry.path);
        await sftpWrite(sessionId, dest, bytes, tid);
        settle(tid, { status: 'done', done: entry.size });
      } else {
        setTransfer({ id: tid, name: entry.name, dir: 'up', done: 0, total: 0, status: 'active', note: t('sftp.scanning') });
        const { dirs, files } = await scanLocalDir(entry.path);
        const total = files.reduce((s, f) => s + f.size, 0);
        settle(tid, { total, note: undefined });
        await sftpMkdir(sessionId, dest);
        for (const d of dirs) await sftpMkdir(sessionId, joinPosix(dest, d));
        let i = 0;
        for (const f of files) {
          i += 1;
          settle(tid, { note: `${i}/${files.length} · ${f.rel}` });
          const bytes = await localRead(joinPosix(entry.path, f.rel));
          await sftpWrite(sessionId, joinPosix(dest, f.rel), bytes, tid);
          base += f.size;
          settle(tid, { done: base });
        }
        settle(tid, { status: 'done', done: total, note: undefined });
      }
      await loadRemote(rdir);
    } catch (e) {
      setErr(msg(e));
      settle(tid, { status: 'error', error: msg(e) });
    } finally {
      unlisten();
      setBusy(false);
    }
  }

  // ---- File operations (context menu) ----

  /** Read + show a file in the preview modal. Text only — binary and files
   *  over PREVIEW_MAX are explained instead of dumped. */
  async function viewFile(name: string, size: number, read: () => Promise<Uint8Array>): Promise<void> {
    if (size > PREVIEW_MAX) {
      setView({ title: name, body: t('sftp.tooLarge').replace('{size}', formatBytes(PREVIEW_MAX)) });
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const bytes = await read();
      // Binary sniff: a NUL in the first 8 KB means this isn't text.
      const sniff = bytes.subarray(0, 8192);
      let binary = false;
      for (const b of sniff) {
        if (b === 0) {
          binary = true;
          break;
        }
      }
      setView({
        title: name,
        body: binary ? t('sftp.binaryPreview') : new TextDecoder('utf-8', { fatal: false }).decode(bytes),
      });
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  async function op<T>(run: () => Promise<T>): Promise<void> {
    setBusy(true);
    setErr(null);
    try {
      await run();
    } catch (e) {
      setErr(msg(e));
    } finally {
      setBusy(false);
    }
  }

  function viewRemote(entry: SftpEntry): Promise<void> {
    return viewFile(entry.name, entry.size, () => sftpRead(sessionId, joinPosix(rdir, entry.name), nextTransferId()));
  }
  function viewLocal(entry: LocalEntry): Promise<void> {
    return viewFile(entry.name, entry.size, () => localRead(entry.path));
  }

  async function deleteRemote(entry: SftpEntry): Promise<void> {
    const ok = await confirm.ask({
      message: t(entry.is_dir ? 'sftp.deleteDirMsg' : 'sftp.deleteFileMsg').replace('{name}', entry.name),
      confirmLabel: t('sftp.delete'),
      danger: true,
    });
    if (!ok) return;
    await op(async () => {
      await sftpDelete(sessionId, joinPosix(rdir, entry.name));
      setSelR(null);
      await loadRemote(rdir);
    });
  }
  async function deleteLocal(entry: LocalEntry): Promise<void> {
    const ok = await confirm.ask({
      message: t(entry.is_dir ? 'sftp.deleteDirMsg' : 'sftp.deleteFileMsg').replace('{name}', entry.name),
      confirmLabel: t('sftp.delete'),
      danger: true,
    });
    if (!ok) return;
    await op(async () => {
      await localDelete(entry.path);
      setSelL(null);
      if (local !== null) await loadLocal(local.path);
    });
  }

  async function renameRemote(entry: SftpEntry): Promise<void> {
    const name = await prompt.ask(t('sftp.renamePrompt').replace('{name}', entry.name), entry.name);
    if (name === null || name === entry.name) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await sftpRename(sessionId, joinPosix(rdir, entry.name), joinPosix(rdir, name.trim()));
      await loadRemote(rdir);
    });
  }
  async function renameLocal(entry: LocalEntry): Promise<void> {
    if (local === null) return;
    const name = await prompt.ask(t('sftp.renamePrompt').replace('{name}', entry.name), entry.name);
    if (name === null || name === entry.name) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await localRename(entry.path, joinPosix(local.path, name.trim()));
      await loadLocal(local.path);
    });
  }

  async function newRemoteFolder(): Promise<void> {
    const name = await prompt.ask(t('sftp.newFolderPrompt'));
    if (name === null) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await sftpMkdir(sessionId, joinPosix(rdir, name.trim()));
      await loadRemote(rdir);
    });
  }
  async function newRemoteFile(): Promise<void> {
    const name = await prompt.ask(t('sftp.newFilePrompt'));
    if (name === null) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await sftpWrite(sessionId, joinPosix(rdir, name.trim()), new Uint8Array(), nextTransferId());
      await loadRemote(rdir);
    });
  }
  async function newLocalFolder(): Promise<void> {
    if (local === null) return;
    const name = await prompt.ask(t('sftp.newFolderPrompt'));
    if (name === null) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await localMkdir(joinPosix(local.path, name.trim()));
      await loadLocal(local.path);
    });
  }
  async function newLocalFile(): Promise<void> {
    if (local === null) return;
    const name = await prompt.ask(t('sftp.newFilePrompt'));
    if (name === null) return;
    if (!nameValid(name)) {
      setErr(t('sftp.invalidName'));
      return;
    }
    await op(async () => {
      await localWrite(joinPosix(local.path, name.trim()), new Uint8Array());
      await loadLocal(local.path);
    });
  }

  return (
    <div className="sftp-panel">
      {confirm.node}
      {prompt.node}
      {ctxMenu.node}
      {view !== null && (
        <Modal onClose={() => setView(null)} className="sftp-view-modal" ariaLabel={view.title}>
          <div className="sftp-view-head mono">{view.title}</div>
          <pre className="sftp-view-body scroll">{view.body}</pre>
        </Modal>
      )}
      {err !== null && <div className="error sftp-err">{err}</div>}
      {transfer !== null && (
        <div className={`sftp-transfer ${transfer.status}`}>
          <div className="sftp-transfer-head">
            <span className="sftp-transfer-dir">{transfer.dir === 'up' ? '⬆' : '⬇'}</span>
            <span className="sftp-transfer-name mono">{transfer.name}</span>
            {transfer.note !== undefined && <span className="muted small sftp-transfer-note">{transfer.note}</span>}
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
          <div
            className="sftp-list scroll"
            onContextMenu={(e) =>
              ctxMenu.open(e, [
                { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newLocalFolder() },
                { label: t('sftp.newFile'), disabled: busy, onClick: () => void newLocalFile() },
                { label: t('sftp.refresh'), onClick: () => local !== null && void loadLocal(local.path) },
              ])
            }
          >
            {(local?.entries ?? []).map((e) => (
              <div
                key={e.path}
                className={`sftp-row${selL === e.name ? ' selected' : ''}`}
                onClick={() => setSelL(e.name)}
                onContextMenu={(ev) => {
                  ev.stopPropagation();
                  setSelL(e.name);
                  ctxMenu.open(ev, [
                    {
                      label: t(e.is_dir ? 'sftp.open' : 'sftp.view'),
                      onClick: () => (e.is_dir ? void loadLocal(e.path) : void viewLocal(e)),
                    },
                    { label: t('sftp.upload'), disabled: busy, onClick: () => void upload(e) },
                    { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newLocalFolder() },
                    { label: t('sftp.newFile'), disabled: busy, onClick: () => void newLocalFile() },
                    { label: t('sftp.rename'), disabled: busy, onClick: () => void renameLocal(e) },
                    { label: t('sftp.delete'), danger: true, disabled: busy, onClick: () => void deleteLocal(e) },
                  ]);
                }}
              >
                <button
                  className="sftp-name-btn"
                  disabled={busy}
                  title={e.name}
                  onClick={() => setSelL(e.name)}
                  onDoubleClick={() => (e.is_dir ? void loadLocal(e.path) : void viewLocal(e))}
                  onKeyDown={(ev) => {
                    // Keyboard activation = the double-click (primary) action;
                    // a plain mouse click only selects.
                    if (ev.key === 'Enter') {
                      ev.preventDefault();
                      if (e.is_dir) void loadLocal(e.path);
                      else void viewLocal(e);
                    }
                  }}
                >
                  <Icon name={e.is_dir ? 'folder' : 'file-text'} size={15} className="sftp-icon" />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="spacer" />
                <span className="muted small">{e.is_dir ? '' : formatBytes(e.size)}</span>
                <button className="link-btn" disabled={busy} title={t('sftp.upload')} onClick={() => void upload(e)}>
                  {t('sftp.toRemote')} <Icon name="chevron-right" size={13} />
                </button>
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
          <div
            className="sftp-list scroll"
            onContextMenu={(e) =>
              ctxMenu.open(e, [
                { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newRemoteFolder() },
                { label: t('sftp.newFile'), disabled: busy, onClick: () => void newRemoteFile() },
                { label: t('sftp.refresh'), onClick: () => void loadRemote(rdir) },
              ])
            }
          >
            {rentries.map((e) => (
              <div
                key={e.name}
                className={`sftp-row${selR === e.name ? ' selected' : ''}`}
                onClick={() => setSelR(e.name)}
                onContextMenu={(ev) => {
                  ev.stopPropagation();
                  setSelR(e.name);
                  ctxMenu.open(ev, [
                    {
                      label: t(e.is_dir ? 'sftp.open' : 'sftp.view'),
                      onClick: () => (e.is_dir ? setRdir(joinPosix(rdir, e.name)) : void viewRemote(e)),
                    },
                    { label: t('sftp.download'), disabled: busy, onClick: () => void download(e) },
                    { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newRemoteFolder() },
                    { label: t('sftp.newFile'), disabled: busy, onClick: () => void newRemoteFile() },
                    { label: t('sftp.rename'), disabled: busy, onClick: () => void renameRemote(e) },
                    { label: t('sftp.delete'), danger: true, disabled: busy, onClick: () => void deleteRemote(e) },
                  ]);
                }}
              >
                <button
                  className="sftp-name-btn"
                  disabled={busy}
                  title={e.name}
                  onClick={() => setSelR(e.name)}
                  onDoubleClick={() => (e.is_dir ? setRdir(joinPosix(rdir, e.name)) : void viewRemote(e))}
                  onKeyDown={(ev) => {
                    if (ev.key === 'Enter') {
                      ev.preventDefault();
                      if (e.is_dir) setRdir(joinPosix(rdir, e.name));
                      else void viewRemote(e);
                    }
                  }}
                >
                  <Icon name={e.is_dir ? 'folder' : 'file-text'} size={15} className="sftp-icon" />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="spacer" />
                <span className="muted small">{e.is_dir ? '' : formatBytes(e.size)}</span>
                <button className="link-btn" disabled={busy} title={t('sftp.download')} onClick={() => void download(e)}>
                  <Icon name="chevron-left" size={13} /> {t('sftp.toLocal')}
                </button>
              </div>
            ))}
            {!rbusy && rentries.length === 0 && <div className="muted small region-pad">{t('sftp.empty')}</div>}
          </div>
        </div>
      </div>
    </div>
  );
}
