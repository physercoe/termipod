/// Shared Zotero content-addressing helpers (ADR-055 M2.5c/d) — the pieces the
/// two Zotero-layout backends share (WebDAV in `webdav_zotero.ts`, S3 in the S3
/// backend): the `<KEY>.zip` + `<KEY>.prop` layout, MD5 hashing, and the
/// KEY-based local enumeration. Ported from `webdav.rs` (`enumerate_local` /
/// `zip_files` / `unzip_into` / `build_prop` / `md5_file`) + `s3.rs`
/// (`parse_prop`). No HTTP here — the transports consume these.
///
/// Zotero stores each attachment `<KEY>` (the `storage/<KEY>/` folder) as two
/// objects: `<KEY>.zip` (a flat ZIP of the folder's files) and `<KEY>.prop`
/// (`<properties version="1"><mtime>ms</mtime><hash>md5</hash></properties>`, the
/// completion marker written last). `hash` is the MD5 of the primary file — real
/// Zotero clients reject a `.prop` whose hash mismatches, so it is computed for
/// real.
import { createHash } from 'node:crypto';
import { readdirSync, statSync } from 'node:fs';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { extractAll, isKey } from './core.ts';

// jszip is a CommonJS `export =` module; dynamic import wraps it under `default`.
// Pure JS (pako) — kept external + lazy like the other engine modules.
type JSZipModule = typeof import('jszip');
let jszipP: Promise<JSZipModule> | null = null;
function loadJszip(): Promise<JSZipModule> {
  if (jszipP === null) jszipP = import('jszip').then((m) => m.default);
  return jszipP;
}

export function md5Hex(bytes: Buffer): string {
  return createHash('md5').update(bytes).digest('hex');
}

/// `<KEY>.prop` body: the completion marker Zotero writes last. Mirrors webdav.rs
/// `build_prop`.
export function buildProp(mtimeMs: number, hash: string): string {
  return `<properties version="1"><mtime>${mtimeMs}</mtime><hash>${hash}</hash></properties>`;
}

/// Parse a `.prop` body to `{mtime, hash}` (0 / '' when absent). Mirrors s3.rs
/// `parse_prop` / webdav.rs `get_prop` XML extraction.
export function parseProp(body: string): { mtime: number; hash: string } {
  const mRaw = (extractAll(body, 'mtime')[0] ?? '').trim();
  const mtime = /^\d+$/.test(mRaw) ? Number.parseInt(mRaw, 10) : 0;
  const hash = extractAll(body, 'hash')[0] ?? '';
  return { mtime, hash };
}

/// A live local attachment folder, keyed by its 8-char Zotero KEY.
export interface LocalAtt {
  files: string[]; // absolute paths, sorted; primary = files[0]
  mtimeMs: number;
  hash: string; // MD5 of the primary file
}

function fileMtimeMs(abs: string): number {
  try {
    return Math.trunc(statSync(abs).mtimeMs);
  } catch {
    return 0;
  }
}

/// Every `storage/<KEY>/` folder holding at least one file, keyed by KEY. The
/// hash + mtime come from the primary (alphabetically first) file — our
/// attachments are single-file (Zotero's `imported_file` model). Async so the MD5
/// reads yield to the event loop rather than blocking the main process on a large
/// store. Mirrors webdav.rs `enumerate_local`.
export async function enumerateLocalZotero(root: string): Promise<Map<string, LocalAtt>> {
  const out = new Map<string, LocalAtt>();
  let entries: string[];
  try {
    entries = readdirSync(root);
  } catch {
    return out;
  }
  for (const key of entries) {
    if (!isKey(key)) continue;
    const keyDir = path.join(root, key);
    let inner: import('node:fs').Dirent[];
    try {
      inner = readdirSync(keyDir, { withFileTypes: true });
    } catch {
      continue;
    }
    const files = inner
      .filter((e) => e.isFile() && !e.name.startsWith('.'))
      .map((e) => path.join(keyDir, e.name))
      .sort();
    if (files.length === 0) continue;
    let hash: string;
    try {
      hash = md5Hex(await readFile(files[0]));
    } catch {
      continue;
    }
    const mtimeMs = Math.max(...files.map(fileMtimeMs));
    out.set(key, { files, mtimeMs, hash });
  }
  return out;
}

/// Flat ZIP of the given files (by basename, Deflated) → bytes. Mirrors webdav.rs
/// `zip_files`.
export async function zipFiles(files: string[]): Promise<Buffer> {
  const JSZip = await loadJszip();
  const z = new JSZip();
  for (const f of files) {
    const name = path.basename(f);
    if (name === '') continue;
    z.file(name, await readFile(f));
  }
  return z.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' });
}

/// Extract a `<KEY>.zip` flat into `dest` — entry names reduced to their basename
/// so a crafted zip can't escape the folder (Zotero zips are flat). Returns the
/// count written. Mirrors webdav.rs `unzip_into`.
export async function unzipInto(bytes: Buffer, dest: string): Promise<number> {
  const JSZip = await loadJszip();
  const ar = await JSZip.loadAsync(bytes);
  await mkdir(dest, { recursive: true });
  let n = 0;
  for (const entry of Object.values(ar.files)) {
    if (entry.dir) continue;
    const name = path.basename(entry.name);
    if (name === '' || name.startsWith('.')) continue;
    const data = await entry.async('nodebuffer');
    await writeFile(path.join(dest, name), data);
    n += 1;
  }
  return n;
}
