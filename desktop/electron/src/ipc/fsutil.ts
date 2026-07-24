/// Shared filesystem helpers for the files/dialogs command families (ADR-055
/// M1.4). These mirror the small primitives the Tauri Rust modules
/// (`docfile.rs` / `localfs.rs` / `workspace*.rs` / `storage.rs`) relied on, so
/// the ported handlers behave byte-identically.
import os from 'node:os';
import path from 'node:path';
import { randomBytes } from 'node:crypto';
import { cp, copyFile, mkdir, readdir, readFile, realpath, stat } from 'node:fs/promises';

/// The user's home directory (Rust used HOME || USERPROFILE; os.homedir() is the
/// same resolution).
export function home(): string {
  return os.homedir();
}

/// A directory's parent, or null at a filesystem root (where dirname is a
/// fixpoint) — the "up" button's target, matching Rust `Path::parent`.
export function parentOrNull(p: string): string | null {
  const parent = path.dirname(p);
  return parent === p ? null : parent;
}

/// Refuse a recursive delete of a filesystem root or the home directory itself.
/// Defence-in-depth: the transfer panel already confirms with the user and scopes
/// the target to a selected entry, but "validate at every boundary" means the
/// main process — the authority — must refuse what it can't safely run, so a
/// future caller can't `rm -rf /` (or wipe `~`). Throws; the caller surfaces it.
export function assertSafeLocalDelete(target: string): void {
  const t = target.trim();
  if (t === '') throw new Error('refusing to delete an empty path');
  const abs = path.resolve(t);
  if (parentOrNull(abs) === null) throw new Error(`refusing to delete a filesystem root: ${abs}`);
  if (abs === path.resolve(home())) throw new Error('refusing to delete the home directory');
}

/// POSIX-path counterpart of `assertSafeLocalDelete` for SFTP's `rm -rf`. Refuses
/// the remote root (`/`), the SFTP working dir (`.`, which resolves to the login
/// home on most servers) and its `~` shorthand, and `..` — the floors a recursive
/// remote delete must never be handed. Trailing slashes are normalised first so
/// `/`, `./`, `~/` are all caught.
export function assertSafeRemoteDelete(target: string): void {
  const t = target.trim().replace(/\/+$/, '');
  if (t === '' || t === '/' || t === '.' || t === '..' || t === '~') {
    throw new Error(`refusing to delete a protected remote path: ${target.trim() || '(empty)'}`);
  }
}

/// Read a file as UTF-8, THROWING on invalid UTF-8 — matching Rust
/// `read_to_string`, so the Author file tree can skip binary/unreadable files
/// instead of opening them full of replacement characters.
export async function readTextStrict(p: string): Promise<string> {
  const buf = await readFile(p);
  return new TextDecoder('utf-8', { fatal: true }).decode(buf);
}

export async function toBase64(p: string): Promise<string> {
  return (await readFile(p)).toString('base64');
}

export function fromBase64(b64: string): Buffer {
  return Buffer.from(b64, 'base64');
}

/// MIME by extension — an exact port of `storage.rs::mime_for`.
export function mimeFor(name: string): string {
  const l = name.toLowerCase();
  if (l.endsWith('.pdf')) return 'application/pdf';
  if (l.endsWith('.html') || l.endsWith('.htm')) return 'text/html';
  if (l.endsWith('.epub')) return 'application/epub+zip';
  if (l.endsWith('.txt')) return 'text/plain';
  if (l.endsWith('.md') || l.endsWith('.markdown')) return 'text/markdown';
  if (l.endsWith('.png')) return 'image/png';
  if (l.endsWith('.jpg') || l.endsWith('.jpeg')) return 'image/jpeg';
  if (l.endsWith('.gif')) return 'image/gif';
  if (l.endsWith('.webp')) return 'image/webp';
  if (l.endsWith('.svg')) return 'image/svg+xml';
  return 'application/octet-stream';
}

// Zotero's key alphabet (no 0/1/I/O — visual ambiguity). 8 chars.
const KEY_ALPHABET = '23456789ABCDEFGHIJKLMNPQRSTUVWXYZ';

/// A fresh 8-char Zotero-style attachment key (port of `storage.rs::gen_key`).
export function genKey(): string {
  const raw = randomBytes(8);
  let out = '';
  for (let i = 0; i < raw.length; i += 1) out += KEY_ALPHABET[raw[i] % KEY_ALPHABET.length];
  return out;
}

/// Heavy / generated directories a recursive walk lists but never descends —
/// shared by the Author workspace walk (`workspace_list`) and the Inspect tree
/// name-index (`tree_index`). A single source of truth so the two walks agree on
/// what "skip" means; the tree still *shows* these as tagged nodes (never
/// auto-expanded), only the recursive descent stops.
export const SKIP_DIRS = new Set([
  'node_modules', '.git', 'target', 'dist', 'build', '.next', '.venv', 'venv',
  '__pycache__', '.cache', '.idea', '.vscode', '.svn', '.hg',
]);

/// One entry of a recursive name-index: a root-relative POSIX-joined path and
/// whether it is a directory. Deliberately not an absolute path or a full stat —
/// the Inspect tree filter only needs to match names and rebuild `root + rel`.
export interface NameIndexEntry {
  rel: string;
  is_dir: boolean;
}

/// A bounded recursive name-index of `root` (Inspect tree filter, plan §3 item
/// 5). Unlike the Author `workspace_list` walk this **includes hidden files** (a
/// `.github/` / `.env.example` is an inspection target) and never descends
/// `SKIP_DIRS` (they are listed as tagged nodes but not walked). `truncated` is
/// set whenever a cap was hit — depth OR the entry ceiling — so the UI never
/// implies it indexed everything. Throws if `root` is not a directory.
export async function walkNameIndex(root: string, maxDepth: number, maxEntries: number): Promise<{ entries: NameIndexEntry[]; truncated: boolean }> {
  const rootStat = await stat(root).catch(() => null);
  if (rootStat === null || !rootStat.isDirectory()) throw new Error(`not a folder: ${root}`);

  const out: NameIndexEntry[] = [];
  let truncated = false;
  const walk = async (dir: string, rel: string, depth: number): Promise<void> => {
    const names = await readdir(dir).catch(() => [] as string[]);
    for (const name of names) {
      if (out.length >= maxEntries) {
        truncated = true;
        return;
      }
      const md = await stat(path.join(dir, name)).catch(() => null);
      const isDir = md?.isDirectory() ?? false;
      const childRel = rel === '' ? name : `${rel}/${name}`;
      out.push({ rel: childRel, is_dir: isDir });
      if (isDir && !SKIP_DIRS.has(name)) {
        if (depth + 1 >= maxDepth) truncated = true;
        else await walk(path.join(dir, name), childRel, depth + 1);
      }
    }
  };
  await walk(root, '', 0);
  return { entries: out, truncated };
}

/// dirs-first, then case-insensitive alphabetical (the ordering every listing
/// uses). Sorts in place; `dir` reads whether the entry is a directory.
export function sortDirsFirst<T>(entries: T[], dir: (e: T) => boolean, name: (e: T) => string): void {
  entries.sort((a, b) => {
    const da = dir(a) ? 1 : 0;
    const db = dir(b) ? 1 : 0;
    if (da !== db) return db - da;
    return name(a).toLowerCase().localeCompare(name(b).toLowerCase());
  });
}

/// Whether `child` is `ancestor` or lives under it — the move/copy self-descent
/// guard. Prefers canonicalized paths, falls back to a literal prefix test
/// (port of `workspacefs.rs::is_within`).
export async function isWithin(child: string, ancestor: string): Promise<boolean> {
  try {
    const c = await realpath(child);
    const a = await realpath(ancestor);
    return c === a || c.startsWith(a + path.sep);
  } catch {
    return child === ancestor || child.startsWith(ancestor + path.sep);
  }
}

/// Recursively copy a file or directory tree (port of
/// `workspacefs.rs::copy_recursive`). Callers guarantee the target is absent.
export async function copyRecursive(src: string, dst: string): Promise<void> {
  await cp(src, dst, { recursive: true, errorOnExist: false, force: true });
}

/// Copy a single file (attachments are one file per key dir).
export async function copyFileTo(src: string, dst: string): Promise<void> {
  await copyFile(src, dst);
}

/// mkdir -p.
export async function ensureDir(dir: string): Promise<void> {
  await mkdir(dir, { recursive: true });
}
