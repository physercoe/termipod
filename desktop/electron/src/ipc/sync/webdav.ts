/// Folder (Obsidian-vault style) WebDAV sync (ADR-055 M2.5b) — the Electron port
/// of `foldersync.rs`'s two commands (`folder_webdav_verify` /
/// `folder_webdav_sync`), mirroring the Author workspace tree verbatim under the
/// configured base URL. Consumes the tested decision core (`./core`); this file
/// is only the HTTP transport (PROPFIND/MKCOL/PUT/GET via `fetch`, Basic auth).
///
/// Two-way and additive — it NEVER deletes (the core's `decideBoth`): a file
/// removed on one side is left intact on the other. Bytes stream through this
/// process; credentials are passed per call and never cached.
///
/// PROXY: the `proxy` arg is accepted for contract parity but not yet applied —
/// Node's global `fetch` has no per-request proxy without an undici ProxyAgent
/// (same deferral as the M1.5 drawio download). Env proxies still apply.
import type { WebContents } from 'electron';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { emit } from '../../events';
import type { Handler } from '../dispatch';
import {
  decideBoth,
  willTransfer,
  enumerateLocalTree,
  elementBlocks,
  extractAll,
  parseHttpDateMs,
  pctDecode,
  SKIP_DIRS,
  MAX_DEPTH,
  MAX_ENTRIES,
  MAX_FILE_BYTES,
  type LocalFile,
} from './core';
import { authHeader, baseUrl, childUrl, hasCollectionTag } from './webdav_url';

const DAV_TIMEOUT_MS = 90_000;

interface RemoteFile {
  size: number;
  mtime: number | null;
}

interface FolderSyncReport {
  uploaded: number;
  downloaded: number;
  skipped: number;
  conflicts: number;
  errors: string[];
}

async function dav(
  url: string,
  method: string,
  auth: string,
  init?: { body?: string | Uint8Array; headers?: Record<string, string> },
): Promise<Response> {
  return fetch(url, {
    method,
    headers: { Authorization: auth, ...(init?.headers ?? {}) },
    body: init?.body,
    signal: AbortSignal.timeout(DAV_TIMEOUT_MS),
  });
}

const PROPFIND_BODY =
  '<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/><getcontentlength/><getlastmodified/></prop></propfind>';

// ── remote listing ───────────────────────────────────────────────────────────
async function propfindDir(
  base: URL,
  basePath: string,
  dirRel: string,
  auth: string,
  files: Map<string, RemoteFile>,
  subdirs: string[],
): Promise<void> {
  const resp = await dav(childUrl(base, dirRel, true), 'PROPFIND', auth, {
    headers: { Depth: '1', 'Content-Type': 'application/xml; charset=utf-8' },
    body: PROPFIND_BODY,
  });
  const s = resp.status;
  if (s === 404) return; // nothing remote yet — first sync just uploads
  if (s === 401) throw new Error('authentication failed (check username / password)');
  if (!(s === 207 || (s >= 200 && s < 300))) throw new Error(`PROPFIND → HTTP ${s}`);
  const body = await resp.text();

  const selfPath = new URL(childUrl(base, dirRel, true)).pathname.replace(/\/+$/, '');
  for (const block of elementBlocks(body, 'response')) {
    const href = extractAll(block, 'href')[0];
    if (href === undefined) continue;
    let absPath: string;
    try {
      absPath = new URL(href.trim(), base).pathname;
    } catch {
      continue;
    }
    if (absPath.replace(/\/+$/, '') === selfPath) continue; // the collection itself
    if (!absPath.startsWith(basePath)) continue; // outside our tree
    const encRel = absPath.slice(basePath.length);
    const isDir = hasCollectionTag(block) || absPath.endsWith('/');
    const decRel = encRel
      .replace(/\/+$/, '')
      .split('/')
      .map(pctDecode)
      .join('/');
    if (decRel === '') continue;
    const name = decRel.slice(decRel.lastIndexOf('/') + 1);
    if (name.startsWith('.')) continue;
    if (isDir) {
      if (SKIP_DIRS.includes(name)) continue;
      subdirs.push(decRel); // recurse on the DECODED path (child_url re-encodes)
    } else {
      const size = Number.parseInt((extractAll(block, 'getcontentlength')[0] ?? '').trim(), 10);
      const mtime = parseHttpDateMs((extractAll(block, 'getlastmodified')[0] ?? '').trim());
      files.set(decRel, { size: Number.isFinite(size) ? size : 0, mtime });
    }
  }
}

async function enumerateRemote(base: URL, auth: string): Promise<Map<string, RemoteFile>> {
  const basePath = base.pathname; // trailing-slash, percent-encoded
  const files = new Map<string, RemoteFile>();
  const queue: Array<[string, number]> = [['', 0]];
  while (queue.length > 0) {
    const [dirRel, depth] = queue.pop()!;
    if (depth >= MAX_DEPTH || files.size >= MAX_ENTRIES) continue;
    const subdirs: string[] = [];
    await propfindDir(base, basePath, dirRel, auth, files, subdirs);
    for (const sd of subdirs) queue.push([sd, depth + 1]);
  }
  return files;
}

// ── transfers ────────────────────────────────────────────────────────────────
async function mkcolParents(base: URL, rel: string, auth: string, made: Set<string>): Promise<void> {
  const parts = rel.split('/').filter((p) => p !== '');
  if (parts.length < 2) return; // top-level file — parent is the verified base
  let acc = '';
  for (const part of parts.slice(0, -1)) {
    acc = acc === '' ? part : `${acc}/${part}`;
    if (made.has(acc)) continue;
    const resp = await dav(childUrl(base, acc, true), 'MKCOL', auth);
    const s = resp.status;
    // 201 created · 405 exists · 200/301 tolerated.
    if (![200, 201, 301, 405].includes(s)) throw new Error(`MKCOL ${acc}/ → HTTP ${s}`);
    made.add(acc);
  }
}

async function upload(base: URL, rel: string, local: LocalFile, auth: string, made: Set<string>): Promise<void> {
  if (local.size > MAX_FILE_BYTES) throw new Error('file exceeds 100 MB sync cap');
  await mkcolParents(base, rel, auth, made);
  const bytes = await readFile(local.abs);
  const resp = await dav(childUrl(base, rel, false), 'PUT', auth, {
    headers: { 'Content-Type': 'application/octet-stream' },
    body: bytes,
  });
  if (!resp.ok) throw new Error(`PUT → HTTP ${resp.status}`);
}

async function download(base: URL, rel: string, root: string, auth: string): Promise<void> {
  const resp = await dav(childUrl(base, rel, false), 'GET', auth);
  if (!(resp.status >= 200 && resp.status < 300)) throw new Error(`GET → HTTP ${resp.status}`);
  const bytes = Buffer.from(await resp.arrayBuffer());
  // Confine the write to the workspace root — a hostile server can't escape it.
  let dest = root;
  for (const part of rel.split('/').filter((p) => p !== '')) {
    if (part === '..' || part === '.') throw new Error(`unsafe remote path: ${rel}`);
    dest = path.join(dest, part);
  }
  await mkdir(path.dirname(dest), { recursive: true });
  await writeFile(dest, bytes);
}

function progress(sender: WebContents, id: string | null, done: number, total: number): void {
  if (id !== null) emit(sender, 'sync:progress', { id, done, total });
}

export const folderWebdavHandlers: Record<string, Handler> = {
  folder_webdav_verify: async (args): Promise<string> => {
    const base = baseUrl(String(args.url ?? ''));
    const auth = authHeader(String(args.user ?? ''), String(args.pass ?? ''));
    const resp = await dav(base.href, 'PROPFIND', auth, {
      headers: { Depth: '0', 'Content-Type': 'application/xml; charset=utf-8' },
      body: '<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>',
    });
    const s = resp.status;
    if (s === 401) throw new Error('authentication failed (check username / password)');
    if (s === 404) throw new Error('folder not found at that URL (check the path)');
    if (s === 207 || (s >= 200 && s < 300)) return 'ok';
    throw new Error(`PROPFIND → HTTP ${s}`);
  },

  folder_webdav_sync: async (args, ctx): Promise<FolderSyncReport> => {
    const root = String(args.root ?? '');
    const base = baseUrl(String(args.url ?? ''));
    const auth = authHeader(String(args.user ?? ''), String(args.pass ?? ''));
    const progressId = typeof args.progressId === 'string' ? args.progressId : null;
    const sender = ctx.sender;

    const locals = enumerateLocalTree(root);
    const remotes = await enumerateRemote(base, auth);

    const all = new Set<string>([...locals.keys(), ...remotes.keys()]);
    // Pre-count actual transfers (skips excluded) for the N/M chip.
    let total = 0;
    for (const rel of all) {
      const l = locals.get(rel);
      const r = remotes.get(rel);
      if (willTransfer(l ? { size: l.size, mtime: l.mtimeMs } : null, r ? { size: r.size, mtime: r.mtime } : null)) {
        total += 1;
      }
    }
    progress(sender, progressId, 0, total);

    const report: FolderSyncReport = { uploaded: 0, downloaded: 0, skipped: 0, conflicts: 0, errors: [] };
    const made = new Set<string>();
    let emitted = 0;

    for (const rel of all) {
      const local = locals.get(rel);
      const remote = remotes.get(rel);
      try {
        if (local !== undefined && remote === undefined) {
          await upload(base, rel, local, auth, made);
          report.uploaded += 1;
        } else if (local === undefined && remote !== undefined) {
          if (remote.size > MAX_FILE_BYTES) report.skipped += 1;
          else {
            await download(base, rel, root, auth);
            report.downloaded += 1;
          }
        } else if (local !== undefined && remote !== undefined) {
          switch (decideBoth(local.size, local.mtimeMs, remote.size, remote.mtime)) {
            case 'skip':
              report.skipped += 1;
              break;
            case 'upload':
              await upload(base, rel, local, auth, made);
              report.uploaded += 1;
              break;
            case 'download':
              await download(base, rel, root, auth);
              report.downloaded += 1;
              break;
            case 'conflict':
              report.conflicts += 1;
              break;
          }
        }
      } catch (e) {
        report.errors.push(`${rel}: ${(e as Error).message}`);
      }
      // Advance the chip only when a transfer actually completed, on change only.
      const done = report.uploaded + report.downloaded;
      if (done !== emitted) {
        emitted = done;
        progress(sender, progressId, done, total);
      }
    }
    return report;
  },
};
