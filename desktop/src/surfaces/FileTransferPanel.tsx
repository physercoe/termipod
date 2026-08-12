import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties } from 'react';
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
import {
  enqueueSftpTransfer,
  listSftpTransfers,
  subscribeSftpTransfers,
  type SftpTransfer,
} from '../state/sftpTransfers';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { InspectFileIcon } from '../ui/InspectFileIcon';
import { useConfirm } from '../ui/ConfirmModal';
import { useContextMenu } from '../ui/ContextMenu';
import { useTextPrompt } from '../ui/PromptModal';
import { Modal } from '../ui/Modal';
import { ResizeHandle } from '../ui/ResizeHandle';
import {
  formatModifiedTime,
  formatPermissions,
  nextFileSort,
  sortFileEntries,
  type FileSort,
  type FileSortKey,
} from '../ssh/fileListing';

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

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function nextTransferId(): string {
  return `tx${crypto.randomUUID()}`;
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

type SftpColumnKey = 'name' | 'size' | 'modified' | 'permissions';
type SftpColumnWidths = Record<SftpColumnKey, number>;
type SftpPaneSide = 'local' | 'remote';

const LEGACY_SFTP_COLUMN_WIDTHS_KEY = 'termipod.sftp.columnWidths';
const SFTP_PANE_SHARE_KEY = 'termipod.sftp.localPaneShare.v2';
const SFTP_COLUMN_WIDTHS_KEYS: Record<SftpPaneSide, string> = {
  local: 'termipod.sftp.localColumnWidths',
  remote: 'termipod.sftp.remoteColumnWidths',
};
const DEFAULT_COLUMN_WIDTHS: SftpColumnWidths = { name: 240, size: 92, modified: 170, permissions: 126 };
const MIN_COLUMN_WIDTHS: SftpColumnWidths = { name: 120, size: 64, modified: 108, permissions: 88 };

function loadColumnWidths(side: SftpPaneSide): SftpColumnWidths {
  try {
    const raw = localStorage.getItem(SFTP_COLUMN_WIDTHS_KEYS[side]) ?? localStorage.getItem(LEGACY_SFTP_COLUMN_WIDTHS_KEY);
    const saved = JSON.parse(raw ?? '{}') as Partial<SftpColumnWidths>;
    return Object.fromEntries(
      (Object.keys(DEFAULT_COLUMN_WIDTHS) as SftpColumnKey[]).map((key) => {
        const value = saved[key];
        return [key, typeof value === 'number' && Number.isFinite(value) ? Math.max(MIN_COLUMN_WIDTHS[key], value) : DEFAULT_COLUMN_WIDTHS[key]];
      }),
    ) as SftpColumnWidths;
  } catch {
    return DEFAULT_COLUMN_WIDTHS;
  }
}

function columnStyle(widths: SftpColumnWidths): CSSProperties {
  return {
    gridTemplateColumns: `${widths.name}px ${widths.size}px ${widths.modified}px ${widths.permissions}px minmax(96px, 1fr)`,
  };
}

function EntryIcon({ name, isDir }: { name: string; isDir: boolean }): JSX.Element {
  return isDir ? (
    <span className="inspect-folder-icon" aria-hidden="true">
      <Icon name="folder" size={15} />
    </span>
  ) : (
    <InspectFileIcon filename={name} size={15} />
  );
}

function FileListHeader({
  sort,
  onSort,
  onResize,
  onReset,
}: {
  sort: FileSort;
  onSort: (key: FileSortKey) => void;
  onResize: (key: SftpColumnKey, dx: number) => void;
  onReset: (key: SftpColumnKey) => void;
}): JSX.Element {
  const t = useT();
  const sortable = (key: SftpColumnKey, label: string, className = ''): JSX.Element => {
    const active = sort.key === key;
    const direction = active ? sort.direction : undefined;
    return (
      <div className={`sftp-head-cell${className === '' ? '' : ` ${className}`}`}>
        <button
          className={`sftp-sort-btn${active ? ' active' : ''}`}
          onClick={() => onSort(key)}
          aria-label={`${t('a11y.sortBy').replace('{col}', label)}${direction === undefined ? '' : `, ${t(direction === 'asc' ? 'sftp.ascending' : 'sftp.descending')}`}`}
        >
          <span>{label}</span>
          {active && <Icon name={direction === 'asc' ? 'chevron-up' : 'chevron-down'} size={12} />}
        </button>
        <span
          className="sftp-col-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label={t('read.resizeColumn').replace('{col}', label)}
          title={t('read.resizeColumnHint')}
          tabIndex={0}
          onDoubleClick={() => onReset(key)}
          onKeyDown={(e) => {
            const step = e.shiftKey ? 48 : 12;
            if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
              e.preventDefault();
              onResize(key, e.key === 'ArrowLeft' ? -step : step);
            }
          }}
          onPointerDown={(e) => {
            e.preventDefault();
            e.stopPropagation();
            let lastX = e.clientX;
            const move = (ev: PointerEvent): void => {
              const dx = ev.clientX - lastX;
              lastX = ev.clientX;
              if (dx !== 0) onResize(key, dx);
            };
            const end = (): void => {
              window.removeEventListener('pointermove', move);
              window.removeEventListener('pointerup', end);
              window.removeEventListener('pointercancel', end);
              document.body.style.cursor = '';
              document.body.style.userSelect = '';
            };
            window.addEventListener('pointermove', move);
            window.addEventListener('pointerup', end);
            window.addEventListener('pointercancel', end);
            document.body.style.cursor = 'col-resize';
            document.body.style.userSelect = 'none';
          }}
        />
      </div>
    );
  };
  return (
    <div className="sftp-list-head">
      {sortable('name', t('sftp.name'))}
      {sortable('size', t('sftp.size'), 'sftp-col-size')}
      {sortable('modified', t('sftp.modified'))}
      {sortable('permissions', t('sftp.permissions'))}
      <span aria-hidden="true" />
    </div>
  );
}

/** A pre-scanned directory tree: directory relpaths (for recreating empty
 *  dirs) plus file relpaths with sizes (for the aggregate progress total). */
interface DirScan {
  dirs: string[];
  files: { rel: string; size: number }[];
}

/** Recursive pre-scan of a local directory (for upload). */
async function scanLocalDir(root: string): Promise<DirScan> {
  const dirs: string[] = [];
  const files: { rel: string; size: number }[] = [];
  const walk = async (abs: string, rel: string): Promise<void> => {
    const listing = await localList(abs);
    for (const entry of listing.entries) {
      const nextRel = rel === '' ? entry.name : `${rel}/${entry.name}`;
      if (entry.is_dir) {
        dirs.push(nextRel);
        await walk(entry.path, nextRel);
      } else {
        files.push({ rel: nextRel, size: entry.size });
      }
    }
  };
  await walk(root, '');
  return { dirs, files };
}

/** Recursive pre-scan of a remote directory (for download). */
async function scanRemoteDir(sessionId: string, root: string): Promise<DirScan> {
  const dirs: string[] = [];
  const files: { rel: string; size: number }[] = [];
  const walk = async (abs: string, rel: string): Promise<void> => {
    const listing = await sftpList(sessionId, abs);
    for (const entry of listing) {
      const nextRel = rel === '' ? entry.name : `${rel}/${entry.name}`;
      if (entry.is_dir) {
        dirs.push(nextRel);
        await walk(joinPosix(abs, entry.name), nextRel);
      } else {
        files.push({ rel: nextRel, size: entry.size });
      }
    }
  };
  await walk(root, '');
  return { dirs, files };
}

function TransferProgress({ transfer }: { transfer: SftpTransfer }): JSX.Element {
  const t = useT();
  const statusLabel =
    transfer.status === 'queued'
      ? t('sftp.queued')
      : transfer.status === 'error'
        ? t('sftp.failed')
        : transfer.status === 'done'
          ? t('sftp.done')
          : transfer.total > 0
            ? `${formatBytes(transfer.done)} / ${formatBytes(transfer.total)}`
            : formatBytes(transfer.done);
  return (
    <div className={`sftp-transfer ${transfer.status}`}>
      <div className="sftp-transfer-head">
        <span className="sftp-transfer-dir">{transfer.dir === 'up' ? '⬆' : '⬇'}</span>
        <span className="sftp-transfer-name mono">{transfer.name}</span>
        {transfer.note !== undefined && <span className="muted small sftp-transfer-note">{transfer.note}</span>}
        <span className="spacer" />
        <span className="muted small">{statusLabel}</span>
      </div>
      <div className="sftp-progress-track">
        <div
          className={`sftp-progress-fill ${transfer.total === 0 && transfer.status === 'active' ? 'indeterminate' : ''}`}
          style={
            transfer.status === 'queued'
              ? { width: '0%' }
              : transfer.total > 0
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
  );
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
  const [rSort, setRSort] = useState<FileSort>({ key: 'name', direction: 'asc' });
  // Local pane — `local` carries the absolute path + parent so navigation never
  // re-joins paths client-side; `lpath` is the editable path field.
  const [local, setLocal] = useState<LocalListing | null>(null);
  const [lpath, setLpath] = useState('');
  const [lbusy, setLBusy] = useState(false);
  const [lSort, setLSort] = useState<FileSort>({ key: 'name', direction: 'asc' });
  // Selection is by NAME (what the listing shows) — purely visual, no action.
  const [selL, setSelL] = useState<string | null>(null);
  const [selR, setSelR] = useState<string | null>(null);
  const [view, setView] = useState<{ title: string; body: string } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // `busy` covers foreground file operations only (preview/rename/delete).
  // Transfers run in the external session queue and never lock either pane.
  const [busy, setBusy] = useState(false);
  const [localPaneShare, setLocalPaneShare] = useState(() => {
    const saved = Number(localStorage.getItem(SFTP_PANE_SHARE_KEY));
    return Number.isFinite(saved) && saved >= 20 && saved <= 80 ? saved : 50;
  });
  const [localColumnWidths, setLocalColumnWidths] = useState(() => loadColumnWidths('local'));
  const [remoteColumnWidths, setRemoteColumnWidths] = useState(() => loadColumnWidths('remote'));
  const dualRef = useRef<HTMLDivElement>(null);
  const panePersistTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const columnPersistTimers = useRef<Partial<Record<SftpPaneSide, ReturnType<typeof setTimeout>>>>({});
  const sortedLocalEntries = useMemo(() => sortFileEntries(local?.entries ?? [], lSort), [local?.entries, lSort]);
  const sortedRemoteEntries = useMemo(() => sortFileEntries(rentries, rSort), [rentries, rSort]);
  const subscribeTransfers = useCallback(
    (listener: () => void) => subscribeSftpTransfers(sessionId, listener),
    [sessionId],
  );
  const getTransfers = useCallback(() => listSftpTransfers(sessionId), [sessionId]);
  const transfers = useSyncExternalStore(subscribeTransfers, getTransfers, getTransfers);
  const observedTerminalTransfers = useRef(
    new Set(transfers.filter((transfer) => transfer.status === 'done' || transfer.status === 'error').map((transfer) => transfer.id)),
  );

  useEffect(
    () => () => {
      if (panePersistTimer.current !== undefined) clearTimeout(panePersistTimer.current);
      if (columnPersistTimers.current.local !== undefined) clearTimeout(columnPersistTimers.current.local);
      if (columnPersistTimers.current.remote !== undefined) clearTimeout(columnPersistTimers.current.remote);
    },
    [],
  );

  const resizePanes = useCallback((dx: number): void => {
    const available = dualRef.current?.clientWidth ?? 0;
    if (available <= 0) return;
    setLocalPaneShare((current) => {
      const next = Math.min(80, Math.max(20, current + (dx / available) * 100));
      if (panePersistTimer.current !== undefined) clearTimeout(panePersistTimer.current);
      panePersistTimer.current = setTimeout(() => {
        try {
          localStorage.setItem(SFTP_PANE_SHARE_KEY, String(next));
        } catch {
          /* ignore */
        }
      }, 250);
      return next;
    });
  }, []);

  const resizeColumn = useCallback((side: SftpPaneSide, key: SftpColumnKey, dx: number): void => {
    const setWidths = side === 'local' ? setLocalColumnWidths : setRemoteColumnWidths;
    setWidths((current) => {
      const next = { ...current, [key]: Math.max(MIN_COLUMN_WIDTHS[key], current[key] + dx) };
      if (columnPersistTimers.current[side] !== undefined) clearTimeout(columnPersistTimers.current[side]);
      columnPersistTimers.current[side] = setTimeout(() => {
        try {
          localStorage.setItem(SFTP_COLUMN_WIDTHS_KEYS[side], JSON.stringify(next));
        } catch {
          /* ignore */
        }
      }, 250);
      return next;
    });
  }, []);

  const resetColumn = useCallback((side: SftpPaneSide, key: SftpColumnKey): void => {
    const setWidths = side === 'local' ? setLocalColumnWidths : setRemoteColumnWidths;
    setWidths((current) => {
      const next = { ...current, [key]: DEFAULT_COLUMN_WIDTHS[key] };
      try {
        localStorage.setItem(SFTP_COLUMN_WIDTHS_KEYS[side], JSON.stringify(next));
      } catch {
        /* ignore */
      }
      return next;
    });
  }, []);

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

  // Refresh whichever pane received bytes when a background job settles while
  // this view is mounted. If it settled while unmounted, the normal mount loads
  // already fetch the latest directory contents.
  useEffect(() => {
    let refreshLocal = false;
    let refreshRemote = false;
    for (const transfer of transfers) {
      if (transfer.status !== 'done' && transfer.status !== 'error') continue;
      if (observedTerminalTransfers.current.has(transfer.id)) continue;
      observedTerminalTransfers.current.add(transfer.id);
      if (transfer.status === 'done' && transfer.dir === 'down') refreshLocal = true;
      if (transfer.status === 'done' && transfer.dir === 'up') refreshRemote = true;
    }
    if (refreshLocal && local !== null) void loadLocal(local.path);
    if (refreshRemote) void loadRemote(rdir);
  }, [transfers, local, loadLocal, loadRemote, rdir]);

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

  // Download: remote → the local pane's current directory. Directories go
  // recursively: pre-scan, mkdir the tree, then stream each file. The queue is
  // outside React, so this continues if the user switches back to Terminal.
  async function download(entry: SftpEntry): Promise<void> {
    if (local === null) return;
    const exists = local.entries.some((e) => e.name === entry.name);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setErr(null);
    const src = joinPosix(rdir, entry.name);
    const dest = joinPosix(local.path, entry.name);
    enqueueSftpTransfer({
      sessionId,
      name: entry.name,
      dir: 'down',
      total: entry.is_dir ? 0 : entry.size,
      run: async ({ id: transferId, update }) => {
        // `base` = bytes finished in earlier files; backend progress ticks are
        // per-file cumulative, so aggregate = base + tick.
        let base = 0;
        const unlisten = await onSftpProgress(transferId, (done) => update({ done: base + done }));
        try {
          if (!entry.is_dir) {
            const bytes = await sftpRead(sessionId, src, transferId);
            await localWrite(dest, bytes);
            update({ done: entry.size > 0 ? entry.size : 0 });
            return;
          }
          update({ note: t('sftp.scanning') });
          const { dirs, files } = await scanRemoteDir(sessionId, src);
          const total = files.reduce((sum, file) => sum + file.size, 0);
          update({ total, note: undefined });
          await localMkdir(dest);
          for (const dir of dirs) await localMkdir(joinPosix(dest, dir));
          for (const [index, file] of files.entries()) {
            update({ note: `${index + 1}/${files.length} · ${file.rel}` });
            const bytes = await sftpRead(sessionId, joinPosix(src, file.rel), transferId);
            await localWrite(joinPosix(dest, file.rel), bytes);
            base += file.size;
            update({ done: base });
          }
        } finally {
          unlisten();
        }
      },
    });
  }

  // Upload: a local file OR directory → the remote pane's current directory.
  async function upload(entry: LocalEntry): Promise<void> {
    const exists = rentries.some((e) => e.name === entry.name);
    if (!(await confirmOverwrite(entry.name, exists))) return;
    setErr(null);
    const dest = joinPosix(rdir, entry.name);
    enqueueSftpTransfer({
      sessionId,
      name: entry.name,
      dir: 'up',
      total: entry.is_dir ? 0 : entry.size,
      run: async ({ id: transferId, update }) => {
        let base = 0;
        const unlisten = await onSftpProgress(transferId, (done) => update({ done: base + done }));
        try {
          if (!entry.is_dir) {
            const bytes = await localRead(entry.path);
            await sftpWrite(sessionId, dest, bytes, transferId);
            update({ done: entry.size });
            return;
          }
          update({ note: t('sftp.scanning') });
          const { dirs, files } = await scanLocalDir(entry.path);
          const total = files.reduce((sum, file) => sum + file.size, 0);
          update({ total, note: undefined });
          await sftpMkdir(sessionId, dest);
          for (const dir of dirs) await sftpMkdir(sessionId, joinPosix(dest, dir));
          for (const [index, file] of files.entries()) {
            update({ note: `${index + 1}/${files.length} · ${file.rel}` });
            const bytes = await localRead(joinPosix(entry.path, file.rel));
            await sftpWrite(sessionId, joinPosix(dest, file.rel), bytes, transferId);
            base += file.size;
            update({ done: base });
          }
        } finally {
          unlisten();
        }
      },
    });
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
      {transfers.length > 0 && (
        <div className="sftp-transfers scroll">
          {transfers.map((transfer) => (
            <TransferProgress key={transfer.id} transfer={transfer} />
          ))}
        </div>
      )}

      <div
        className="sftp-dual"
        ref={dualRef}
        style={{
          gridTemplateColumns: `minmax(0, calc(${localPaneShare}% - 0.5px)) 1px minmax(0, 1fr)`,
        }}
      >
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
            style={columnStyle(localColumnWidths)}
            onContextMenu={(e) =>
              ctxMenu.open(e, [
                { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newLocalFolder() },
                { label: t('sftp.newFile'), disabled: busy, onClick: () => void newLocalFile() },
                { label: t('sftp.refresh'), onClick: () => local !== null && void loadLocal(local.path) },
              ])
            }
          >
            <FileListHeader
              sort={lSort}
              onSort={(key) => setLSort((current) => nextFileSort(current, key))}
              onResize={(key, dx) => resizeColumn('local', key, dx)}
              onReset={(key) => resetColumn('local', key)}
            />
            {sortedLocalEntries.map((e) => (
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
                    { label: t('sftp.refresh'), onClick: () => local !== null && void loadLocal(local.path) },
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
                  <EntryIcon name={e.name} isDir={e.is_dir} />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="muted small sftp-col-size">{e.is_dir ? '' : formatBytes(e.size)}</span>
                <span className="muted small sftp-col-modified">{formatModifiedTime(e.modified_ms)}</span>
                <span className="muted small mono sftp-col-permissions">{formatPermissions(e.mode, e.is_dir)}</span>
                <button className="link-btn sftp-col-action" disabled={busy} title={t('sftp.upload')} onClick={() => void upload(e)}>
                  {t('sftp.toRemote')} <Icon name="chevron-right" size={13} />
                </button>
              </div>
            ))}
            {!lbusy && (local?.entries.length ?? 0) === 0 && (
              <div className="muted small region-pad">{t('sftp.empty')}</div>
            )}
          </div>
        </div>

        <ResizeHandle onResize={resizePanes} />

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
            style={columnStyle(remoteColumnWidths)}
            onContextMenu={(e) =>
              ctxMenu.open(e, [
                { label: t('sftp.newFolder'), disabled: busy, onClick: () => void newRemoteFolder() },
                { label: t('sftp.newFile'), disabled: busy, onClick: () => void newRemoteFile() },
                { label: t('sftp.refresh'), onClick: () => void loadRemote(rdir) },
              ])
            }
          >
            <FileListHeader
              sort={rSort}
              onSort={(key) => setRSort((current) => nextFileSort(current, key))}
              onResize={(key, dx) => resizeColumn('remote', key, dx)}
              onReset={(key) => resetColumn('remote', key)}
            />
            {sortedRemoteEntries.map((e) => (
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
                    { label: t('sftp.refresh'), onClick: () => void loadRemote(rdir) },
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
                  <EntryIcon name={e.name} isDir={e.is_dir} />
                  <span className="sftp-name">{e.name}</span>
                </button>
                <span className="muted small sftp-col-size">{e.is_dir ? '' : formatBytes(e.size)}</span>
                <span className="muted small sftp-col-modified">{formatModifiedTime(e.modified_ms)}</span>
                <span className="muted small mono sftp-col-permissions">{formatPermissions(e.mode, e.is_dir)}</span>
                <button className="link-btn sftp-col-action" disabled={busy} title={t('sftp.download')} onClick={() => void download(e)}>
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
