/// Zotero-compatible WebDAV file sync (ADR-055 M2.5c) — the Electron port of
/// `webdav.rs`'s two commands (`webdav_verify` / `webdav_sync`). Mirrors Zotero's
/// on-server layout (everything under a `zotero/` collection, each attachment as
/// `<KEY>.zip` + `<KEY>.prop`) so the same server a user points Zotero at just
/// works and files appear in both apps. Content-addressed (MD5): equal hash ⇒
/// skip; else newest mtime wins; same mtime + different hash ⇒ a genuine conflict
/// left untouched (never clobbered). Consumes the shared Zotero helpers
/// (`./zotero`) + the Basic-auth header from `./webdav_url`; only the HTTP is
/// here.
import type { WebContents } from 'electron';
import { emit } from '../../events';
import type { Handler } from '../dispatch';
import { authHeader } from './webdav_url';
import { buildProp, enumerateLocalZotero, parseProp, unzipInto, zipFiles, type LocalAtt } from './zotero';
import { extractAll, isKey } from './core';

const DAV_TIMEOUT_MS = 90_000;

interface SyncReport {
  uploaded: number;
  downloaded: number;
  skipped: number;
  conflicts: number;
  downloadedKeys: string[];
  errors: string[];
}

/// The `zotero/` collection URL under the user's base URL. Zotero always nests
/// under `zotero/`, so we do too. Mirrors webdav.rs `dav_dir`.
function davDir(base: string): string {
  let b = base.trim();
  if (!b.endsWith('/')) b += '/';
  return `${b}zotero/`;
}

async function req(
  url: string,
  method: string,
  auth: string,
  init?: { body?: string | Uint8Array; contentType?: string; depth?: string },
): Promise<Response> {
  const headers: Record<string, string> = { Authorization: auth };
  if (init?.contentType !== undefined) headers['Content-Type'] = init.contentType;
  if (init?.depth !== undefined) headers.Depth = init.depth;
  return fetch(url, { method, headers, body: init?.body, signal: AbortSignal.timeout(DAV_TIMEOUT_MS) });
}

async function mkcol(dav: string, auth: string): Promise<void> {
  const s = (await req(dav, 'MKCOL', auth)).status;
  // 201 created · 405 exists · 200/301 tolerated.
  if ([200, 201, 301, 405].includes(s)) return;
  if (s === 401) throw new Error('authentication failed (check username / password)');
  throw new Error(`MKCOL zotero/ → HTTP ${s}`);
}

async function put(url: string, body: Uint8Array, contentType: string, auth: string): Promise<void> {
  const resp = await req(url, 'PUT', auth, { body, contentType });
  if (!resp.ok) throw new Error(`PUT → HTTP ${resp.status}`);
}

/// The attachment keys present on the server (those with a `.prop` marker); empty
/// on 404 (first sync just uploads). Mirrors webdav.rs `propfind_keys`.
async function propfindKeys(dav: string, auth: string): Promise<Set<string>> {
  // Depth: 1 (immediate children only) — a header-less PROPFIND is
  // Depth: infinity per RFC 4918, which servers like Apache mod_dav refuse
  // (403) by default. Mirrors webdav.rs `propfind_keys`.
  const resp = await req(dav, 'PROPFIND', auth, {
    body: '<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><getlastmodified/></prop></propfind>',
    contentType: 'application/xml; charset=utf-8',
    depth: '1',
  });
  const s = resp.status;
  if (s === 404) return new Set();
  if (s === 401) throw new Error('authentication failed (check username / password)');
  if (!(s === 207 || (s >= 200 && s < 300))) throw new Error(`PROPFIND zotero/ → HTTP ${s}`);
  const body = await resp.text();
  const keys = new Set<string>();
  for (const href of extractAll(body, 'href')) {
    const name = href.replace(/\/+$/, '');
    const base = name.slice(name.lastIndexOf('/') + 1);
    if (base.endsWith('.prop')) {
      const k = base.slice(0, -'.prop'.length);
      if (isKey(k)) keys.add(k);
    }
  }
  return keys;
}

/// The remote `{mtime, hash}` from `<KEY>.prop`; null if absent (404). Mirrors
/// webdav.rs `get_prop`.
async function getProp(dav: string, key: string, auth: string): Promise<{ mtime: number; hash: string } | null> {
  const resp = await req(`${dav}${key}.prop`, 'GET', auth);
  const s = resp.status;
  if (s === 404) return null;
  if (!(s >= 200 && s < 300)) throw new Error(`GET ${key}.prop → HTTP ${s}`);
  return parseProp(await resp.text());
}

async function upload(dav: string, key: string, local: LocalAtt, auth: string): Promise<void> {
  const zipped = await zipFiles(local.files);
  await put(`${dav}${key}.zip`, zipped, 'application/zip', auth);
  // Prop last — its presence marks a completed upload (Zotero's rule).
  await put(`${dav}${key}.prop`, Buffer.from(buildProp(local.mtimeMs, local.hash)), 'text/xml; charset=utf-8', auth);
}

/// Download + extract `<KEY>.zip` into `root/<KEY>/`; false when the zip is
/// missing (404). Mirrors webdav.rs `download`.
async function download(dav: string, key: string, root: string, auth: string): Promise<boolean> {
  const resp = await req(`${dav}${key}.zip`, 'GET', auth);
  const s = resp.status;
  if (s === 404) return false;
  if (!(s >= 200 && s < 300)) throw new Error(`GET ${key}.zip → HTTP ${s}`);
  const bytes = Buffer.from(await resp.arrayBuffer());
  await unzipInto(bytes, `${root}/${key}`);
  return true;
}

function progress(sender: WebContents, id: string | null, done: number, total: number): void {
  if (id !== null) emit(sender, 'sync:progress', { id, done, total });
}

export const webdavZoteroHandlers: Record<string, Handler> = {
  webdav_verify: async (args): Promise<string> => {
    const dav = davDir(String(args.url ?? ''));
    const auth = authHeader(String(args.user ?? ''), String(args.pass ?? ''));
    await mkcol(dav, auth);
    // Prove write access with a probe file.
    await put(`${dav}lastsync.txt`, Buffer.from(String(Date.now())), 'text/plain', auth);
    return 'ok';
  },

  webdav_sync: async (args, ctx): Promise<SyncReport> => {
    const root = String(args.root ?? '');
    const dav = davDir(String(args.url ?? ''));
    const auth = authHeader(String(args.user ?? ''), String(args.pass ?? ''));
    const progressId = typeof args.progressId === 'string' ? args.progressId : null;
    const sender = ctx.sender;

    // Best-effort ensure the collection exists; PROPFIND/PUT below surface a real
    // connectivity/auth problem.
    try {
      await mkcol(dav, auth);
    } catch {
      /* ignore — real errors surface below */
    }

    const report: SyncReport = { uploaded: 0, downloaded: 0, skipped: 0, conflicts: 0, downloadedKeys: [], errors: [] };
    const locals = await enumerateLocalZotero(root);
    const remoteKeys = await propfindKeys(dav, auth);

    const all = new Set<string>([...locals.keys(), ...remoteKeys]);
    // The per-key .prop fetch makes the transfer count unknowable up front —
    // report items-processed / total-keys (still a real N/M bar).
    const total = all.size;
    progress(sender, progressId, 0, total);
    let done = 0;

    for (const key of all) {
      const local = locals.get(key);
      const remote = remoteKeys.has(key);
      try {
        if (local !== undefined && !remote) {
          await upload(dav, key, local, auth);
          report.uploaded += 1;
        } else if (local === undefined && remote) {
          if (await download(dav, key, root, auth)) {
            report.downloaded += 1;
            report.downloadedKeys.push(key);
          } else {
            report.skipped += 1;
          }
        } else if (local !== undefined && remote) {
          const prop = await getProp(dav, key, auth);
          if (prop === null) {
            // Zip listed but prop vanished — re-upload to restore it.
            await upload(dav, key, local, auth);
            report.uploaded += 1;
          } else if (prop.hash !== '' && prop.hash.toLowerCase() === local.hash.toLowerCase()) {
            report.skipped += 1;
          } else if (local.mtimeMs > prop.mtime) {
            await upload(dav, key, local, auth);
            report.uploaded += 1;
          } else if (prop.mtime > local.mtimeMs) {
            if (await download(dav, key, root, auth)) {
              report.downloaded += 1;
              report.downloadedKeys.push(key);
            } else {
              report.skipped += 1;
            }
          } else {
            report.conflicts += 1; // same mtime, different content — don't guess
          }
        }
      } catch (e) {
        report.errors.push(`${key}: ${(e as Error).message}`);
      }
      done += 1;
      progress(sender, progressId, done, total);
    }
    return report;
  },
};
