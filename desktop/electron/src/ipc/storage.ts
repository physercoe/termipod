/// Zotero storage + user attachments (ADR-055 M1.4) — port of the second half of
/// `src-tauri/src/storage.rs`. Native folder/file pickers, a bounded recursive
/// index keyed Zotero-style (`<key>/<file>`), traversal-guarded reads, and
/// add/write/delete of user-managed attachments under a fresh key dir.
import { app } from 'electron';
import { readdir, readFile, rm, realpath, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { Ctx, Handler } from './dispatch';
import { openDialog, saveDialog } from './dialogs';
import { copyFileTo, ensureDir, fromBase64, genKey, mimeFor } from './fsutil';
import { proxyFetch } from './net';
import { downloadPdfBytes } from './download';
import { emit } from '../events';

interface StorageEntry {
  key: string;
  file: string;
  rel: string;
}
interface StorageIndex {
  path: string;
  folderName: string; // serde rename in Rust
  entries: StorageEntry[];
}
interface StorageFile {
  // Raw bytes over IPC (a Node Buffer → Uint8Array via structured clone); no
  // base64 inflation (ADR-055 §7 row 4). Consumers wrap it in a Blob/data view.
  bytes: Uint8Array;
  mime: string;
}
interface AddedAttachment {
  key: string;
  file: string;
  path: string;
  contentType: string; // serde rename in Rust
}

const INDEX_MAX_DEPTH = 6;
const INDEX_MAX_ENTRIES = 200_000;
const ATTACHMENT_EXTS = [
  'pdf', 'epub', 'html', 'htm', 'txt', 'md', 'png', 'jpg', 'jpeg', 'gif', 'webp',
  'mp4', 'webm', 'mov', 'mp3', 'wav', 'm4a', 'flac',
];

/// Recursively index every file under `root`, keyed by parent-dir + filename.
/// Reads only directory entries (names), never bytes; bounded depth + count;
/// skips symlinks (no cycle runaway). Port of `storage.rs::index_dir`.
async function indexDir(root: string): Promise<StorageEntry[]> {
  const out: StorageEntry[] = [];
  async function walk(dir: string, depth: number): Promise<void> {
    if (depth > INDEX_MAX_DEPTH || out.length >= INDEX_MAX_ENTRIES) return;
    const dirents = await readdir(dir, { withFileTypes: true }).catch(() => []);
    for (const d of dirents) {
      if (d.isSymbolicLink()) continue;
      const full = path.join(dir, d.name);
      if (d.isDirectory()) {
        await walk(full, depth + 1);
      } else if (d.isFile()) {
        const key = path.basename(path.dirname(full));
        const rel = path.relative(root, full) || d.name;
        out.push({ key, file: d.name, rel });
      }
    }
  }
  await walk(root, 0);
  return out;
}

async function buildIndex(dir: string): Promise<StorageIndex> {
  const md = await stat(dir).catch(() => null);
  if (md === null || !md.isDirectory()) throw new Error('not a directory');
  return { path: dir, folderName: path.basename(dir), entries: await indexDir(dir) };
}

// Copy `src` (or write `bytes`) into `root` under a fresh Zotero-style `<key>/`
// dir, retrying the key up to 10× on collision. Shared by add + write_bytes.
async function intoKeyDir(
  root: string,
  file: string,
  place: (dest: string) => Promise<void>,
): Promise<AddedAttachment> {
  await ensureDir(root);
  let key = genKey();
  let dir = path.join(root, key);
  for (let i = 0; i < 10; i += 1) {
    if (!(await stat(dir).catch(() => null))) break;
    key = genKey();
    dir = path.join(root, key);
  }
  await ensureDir(dir);
  const dest = path.join(dir, file);
  await place(dest);
  return { key, file, path: dest, contentType: mimeFor(file) };
}

/// Scoped progress tick (same convention as the sync backends: `sync:progress`
/// keyed by the caller's `progressId`, consumed via `invokeWithProgress`).
function downloadProgress(ctx: Ctx, id: string | null, done: number, total: number): void {
  if (id !== null) emit(ctx.sender, 'sync:progress', { id, done, total });
}

export const storageHandlers: Record<string, Handler> = {
  storage_pick_folder: async (args, ctx: Ctx): Promise<StorageIndex | null> => {
    const start = typeof args.start === 'string' && args.start !== '' ? args.start : undefined;
    const defaultPath = start !== undefined && (await stat(start).catch(() => null))?.isDirectory() ? start : undefined;
    const res = await openDialog(ctx.win, { properties: ['openDirectory'], defaultPath });
    if (res.canceled || res.filePaths.length === 0) return null;
    return buildIndex(res.filePaths[0]);
  },

  storage_reindex: async (args): Promise<StorageIndex> => {
    return buildIndex(String(args.path ?? ''));
  },

  storage_read: async (args): Promise<StorageFile> => {
    const root = String(args.path ?? '');
    const rel = String(args.rel ?? '');
    const canonRoot = await realpath(root);
    const canonFull = await realpath(path.join(root, rel));
    if (canonFull !== canonRoot && !canonFull.startsWith(canonRoot + path.sep)) {
      throw new Error('path escapes storage root');
    }
    return { bytes: await readFile(canonFull), mime: mimeFor(rel) };
  },

  attachment_default_dir: async (): Promise<string> => {
    const dir = path.join(app.getPath('userData'), 'storage');
    await ensureDir(dir);
    return dir;
  },

  attachment_pick_file: async (_args, ctx: Ctx): Promise<{ path: string; name: string } | null> => {
    const res = await openDialog(ctx.win, {
      properties: ['openFile'],
      filters: [{ name: 'Documents', extensions: ATTACHMENT_EXTS }],
    });
    if (res.canceled || res.filePaths.length === 0) return null;
    const p = res.filePaths[0];
    return { path: p, name: path.basename(p) };
  },

  attachment_pick_dir: async (_args, ctx: Ctx): Promise<string | null> => {
    const res = await openDialog(ctx.win, { properties: ['openDirectory'] });
    if (res.canceled || res.filePaths.length === 0) return null;
    return res.filePaths[0];
  },

  attachment_add: async (args): Promise<AddedAttachment> => {
    const root = String(args.root ?? '');
    const src = String(args.src ?? '');
    const file = path.basename(src);
    if (file === '') throw new Error('source has no filename');
    return intoKeyDir(root, file, (dest) => copyFileTo(src, dest));
  },

  attachment_write_bytes: async (args): Promise<AddedAttachment> => {
    const root = String(args.root ?? '');
    const bytes = (args.bytes ?? new Uint8Array()) as Uint8Array;
    // Sanitise to a bare filename so nothing escapes the key folder.
    const raw = path.basename(String(args.filename ?? ''));
    const file = raw === '' ? 'image.png' : raw;
    return intoKeyDir(root, file, (dest) => writeFile(dest, bytes));
  },

  attachment_read: async (args): Promise<StorageFile> => {
    const p = String(args.path ?? '');
    return { bytes: await readFile(p), mime: mimeFor(p) };
  },

  /// Stream a (proxy-aware) HTTP download straight into the managed-attachment
  /// layout — the one-click "open-access PDF → my library" path (plan §1.5). The
  /// URL is http(s)-validated; the body is streamed with a 200 MB cap and typed
  /// errors for a non-PDF (paywall landing page). Returns the same
  /// `AddedAttachment` shape as `attachment_write_bytes`.
  attachment_download: async (args, ctx: Ctx): Promise<AddedAttachment> => {
    const root = String(args.root ?? '');
    const url = String(args.url ?? '');
    const proxy = typeof args.proxy === 'string' ? args.proxy : null;
    const fallback = String(args.filename ?? 'download.pdf');
    const progressId = typeof args.progressId === 'string' ? args.progressId : null;
    const { bytes, file } = await downloadPdfBytes(url, {
      fetchImpl: (u, init) => proxyFetch(u, init, proxy),
      fallback,
      onProgress: (done, total) => downloadProgress(ctx, progressId, done, total),
    });
    return intoKeyDir(root, file, (dest) => writeFile(dest, bytes));
  },

  save_image_as: async (args, ctx: Ctx): Promise<string | null> => {
    const defaultName = String(args.defaultName ?? '');
    const bytes = fromBase64(String(args.base64 ?? ''));
    const res = await saveDialog(ctx.win, {
      defaultPath: defaultName,
      filters: [{ name: 'PNG image', extensions: ['png'] }],
    });
    if (res.canceled || res.filePath === undefined || res.filePath === '') return null;
    await writeFile(res.filePath, bytes);
    return res.filePath;
  },

  attachment_delete: async (args): Promise<void> => {
    const p = String(args.path ?? '');
    const md = await stat(p).catch(() => null);
    if (md?.isFile()) await rm(p, { force: true });
    // Remove the now-empty <key>/ dir (Zotero layout = one file per key folder).
    // `recursive: true` is required to remove a directory at all — `fs.rm` with
    // `recursive: false` throws EISDIR on a dir (empty or not); the emptiness is
    // already guaranteed by the readdir check above.
    const parent = path.dirname(p);
    const rest = await readdir(parent).catch(() => null);
    if (rest !== null && rest.length === 0) await rm(parent, { recursive: true, force: true });
  },
};
