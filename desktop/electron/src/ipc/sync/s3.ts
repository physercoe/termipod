/// S3 (and S3-compatible) sync backend (ADR-055 M2.5d) — the Electron port of
/// `s3.rs`. Two layouts over one signer: the workspace TREE mirror (`s3_sync` /
/// `s3_sync_verify`, sharing the M2.5a decision core with the WebDAV folder
/// backend) and the Zotero object layout (`s3_zotero_sync`, sharing the M2.5c
/// Zotero helpers). Requests are signed with SigV4 from `./sigv4` (validated vs
/// `aws4`). Path-style addressing throughout (most compatible across AWS / R2 /
/// MinIO / B2 / Wasabi). The secret is passed per call, never cached.
///
/// PROXY: the `proxy` arg is honoured — every signed request goes through
/// `proxyFetch` (undici ProxyAgent when a proxy is set, direct fetch otherwise).
import type { WebContents } from 'electron';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { emit } from '../../events';
import { proxyFetch } from '../net';
import type { Handler } from '../dispatch';
import {
  decideBoth,
  willTransfer,
  enumerateLocalTree,
  elementBlocks,
  extractAll,
  xmlUnescape,
  iso8601ToMs,
  isKey,
  MAX_ENTRIES,
  MAX_FILE_BYTES,
} from './core';
import {
  buildProp,
  enumerateLocalZotero,
  parseProp,
  unzipInto,
  zipFiles,
  type LocalAtt,
} from './zotero';
import { EMPTY_SHA256, sha256Hex, signRequest, uriEncode, utcStamps } from './sigv4';

const S3_TIMEOUT_MS = 90_000;
const ZOTERO_SUB = 'zotero/';

interface S3Cfg {
  scheme: string;
  host: string; // host[:port], as it appears in the Host header
  bucket: string;
  prefix: string; // normalised: no leading '/', trailing '/' when non-empty
  region: string;
  access: string;
  secret: string;
  proxy?: string; // outbound HTTP(S) proxy, when configured
}

interface FolderSyncReport {
  uploaded: number;
  downloaded: number;
  skipped: number;
  conflicts: number;
  errors: string[];
}
interface ZoteroReport extends Omit<FolderSyncReport, never> {
  downloadedKeys: string[];
}

function makeCfg(
  endpoint: string,
  region: string,
  bucket: string,
  prefix: string,
  access: string,
  secret: string,
): S3Cfg {
  if (bucket.trim() === '') throw new Error('bucket is required');
  const reg = region.trim() === '' ? 'us-east-1' : region.trim();
  const ep = endpoint.trim() === '' ? `https://s3.${reg}.amazonaws.com` : endpoint.trim();
  let url: URL;
  try {
    url = new URL(ep);
  } catch (e) {
    throw new Error(`invalid endpoint: ${(e as Error).message}`);
  }
  const host = url.host;
  if (host === '') throw new Error('endpoint has no host');
  let pfx = prefix.trim().replace(/^\/+/, '');
  if (pfx !== '' && !pfx.endsWith('/')) pfx += '/';
  return { scheme: url.protocol.replace(':', ''), host, bucket: bucket.trim(), prefix: pfx, region: reg, access: access.trim(), secret };
}

function objectUrl(cfg: S3Cfg, rel: string): URL {
  const key = `${cfg.prefix}${rel}`;
  return new URL(`${cfg.scheme}://${cfg.host}/${uriEncode(cfg.bucket, true)}/${uriEncode(key, false)}`);
}

/// Sign (SigV4) and send one request. `query` is the exact canonical query string
/// already on `url` (empty for object ops). Mirrors s3.rs `send_signed`.
async function sendSigned(cfg: S3Cfg, method: string, url: URL, query: string, body?: Uint8Array): Promise<Response> {
  const [amzDate, stamp] = utcStamps(Math.floor(Date.now() / 1000));
  const payloadHash = body !== undefined ? sha256Hex(body) : EMPTY_SHA256;
  const { authorization } = signRequest({
    method,
    url,
    query,
    payloadHash,
    region: cfg.region,
    access: cfg.access,
    secret: cfg.secret,
    amzDate,
    stamp,
  });
  const headers: Record<string, string> = {
    'x-amz-date': amzDate,
    'x-amz-content-sha256': payloadHash,
    Authorization: authorization,
  };
  if (body !== undefined) headers['content-type'] = 'application/octet-stream';
  return proxyFetch(url.href, { method, headers, body, signal: AbortSignal.timeout(S3_TIMEOUT_MS) }, cfg.proxy);
}

interface RemoteObj {
  size: number;
  mtime: number | null;
}

/// ListObjectsV2, paginated. Keyed by the rel path (prefix stripped). Mirrors
/// s3.rs `list_objects`.
async function listObjects(cfg: S3Cfg): Promise<Map<string, RemoteObj>> {
  const out = new Map<string, RemoteObj>();
  let token: string | null = null;
  for (;;) {
    const params: Array<[string, string]> = [
      ['list-type', '2'],
      ['max-keys', '1000'],
    ];
    if (token !== null) params.push(['continuation-token', token]);
    if (cfg.prefix !== '') params.push(['prefix', cfg.prefix]);
    params.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
    const query = params.map(([k, v]) => `${uriEncode(k, true)}=${uriEncode(v, true)}`).join('&');
    const url = new URL(`${cfg.scheme}://${cfg.host}/${uriEncode(cfg.bucket, true)}?${query}`);
    const resp = await sendSigned(cfg, 'GET', url, query);
    const s = resp.status;
    if (s === 403) throw new Error('access denied (check the access key / secret / permissions)');
    if (s === 404) throw new Error('bucket not found (check the bucket name / endpoint / region)');
    if (!(s >= 200 && s < 300)) {
      const code = extractAll(await resp.text(), 'Code')[0] ?? '';
      throw new Error(code === '' ? `list objects → HTTP ${s}` : `list objects → HTTP ${s} (${code})`);
    }
    const body = await resp.text();
    for (const block of elementBlocks(body, 'Contents')) {
      if (out.size >= MAX_ENTRIES) break;
      const keyRaw = extractAll(block, 'Key')[0];
      if (keyRaw === undefined) continue;
      const key = xmlUnescape(keyRaw);
      if (key.endsWith('/')) continue; // a "folder" marker object
      if (!key.startsWith(cfg.prefix)) continue;
      const rel = key.slice(cfg.prefix.length);
      if (rel === '') continue;
      const size = Number.parseInt((extractAll(block, 'Size')[0] ?? '').trim(), 10);
      const mtime = iso8601ToMs((extractAll(block, 'LastModified')[0] ?? '').trim());
      out.set(rel, { size: Number.isFinite(size) ? size : 0, mtime });
    }
    const truncated = (extractAll(body, 'IsTruncated')[0] ?? '').trim().toLowerCase() === 'true';
    if (!truncated || out.size >= MAX_ENTRIES) break;
    token = extractAll(body, 'NextContinuationToken')[0] ?? null;
    if (token === null) break;
  }
  return out;
}

async function putObject(cfg: S3Cfg, rel: string, abs: string): Promise<void> {
  const bytes = await readFile(abs);
  if (bytes.length > MAX_FILE_BYTES) throw new Error('file exceeds 100 MB sync cap');
  const resp = await sendSigned(cfg, 'PUT', objectUrl(cfg, rel), '', bytes);
  if (!resp.ok) throw new Error(`PUT → HTTP ${resp.status}`);
}

async function getObject(cfg: S3Cfg, rel: string, root: string): Promise<void> {
  const resp = await sendSigned(cfg, 'GET', objectUrl(cfg, rel), '');
  if (!(resp.status >= 200 && resp.status < 300)) throw new Error(`GET → HTTP ${resp.status}`);
  const bytes = Buffer.from(await resp.arrayBuffer());
  let dest = root;
  for (const part of rel.split('/').filter((p) => p !== '')) {
    if (part === '..' || part === '.') throw new Error(`unsafe key path: ${rel}`);
    dest = path.join(dest, part);
  }
  await mkdir(path.dirname(dest), { recursive: true });
  await writeFile(dest, bytes);
}

// ── Zotero-layout helpers over S3 ────────────────────────────────────────────
async function putBytes(cfg: S3Cfg, rel: string, bytes: Uint8Array): Promise<void> {
  if (bytes.length > MAX_FILE_BYTES) throw new Error('file exceeds 100 MB sync cap');
  const resp = await sendSigned(cfg, 'PUT', objectUrl(cfg, rel), '', bytes);
  if (!resp.ok) throw new Error(`PUT → HTTP ${resp.status}`);
}

async function getBytesOpt(cfg: S3Cfg, rel: string): Promise<Buffer | null> {
  const resp = await sendSigned(cfg, 'GET', objectUrl(cfg, rel), '');
  if (resp.status === 404) return null;
  if (!(resp.status >= 200 && resp.status < 300)) throw new Error(`GET → HTTP ${resp.status}`);
  return Buffer.from(await resp.arrayBuffer());
}

async function zoteroRemoteKeys(cfg: S3Cfg): Promise<Set<string>> {
  const keys = new Set<string>();
  for (const rel of (await listObjects(cfg)).keys()) {
    if (!rel.startsWith(ZOTERO_SUB)) continue;
    const name = rel.slice(ZOTERO_SUB.length);
    if (name.endsWith('.prop')) {
      const k = name.slice(0, -'.prop'.length);
      if (isKey(k)) keys.add(k);
    }
  }
  return keys;
}

async function zoteroUpload(cfg: S3Cfg, key: string, local: LocalAtt): Promise<void> {
  await putBytes(cfg, `${ZOTERO_SUB}${key}.zip`, await zipFiles(local.files));
  await putBytes(cfg, `${ZOTERO_SUB}${key}.prop`, Buffer.from(buildProp(local.mtimeMs, local.hash)));
}

async function zoteroDownload(cfg: S3Cfg, key: string, root: string): Promise<boolean> {
  const bytes = await getBytesOpt(cfg, `${ZOTERO_SUB}${key}.zip`);
  if (bytes === null) return false;
  await unzipInto(bytes, `${root}/${key}`);
  return true;
}

function progress(sender: WebContents, id: string | null, done: number, total: number): void {
  if (id !== null) emit(sender, 'sync:progress', { id, done, total });
}

function cfgFromArgs(args: Record<string, unknown>): S3Cfg {
  const cfg = makeCfg(
    String(args.endpoint ?? ''),
    String(args.region ?? ''),
    String(args.bucket ?? ''),
    String(args.prefix ?? ''),
    String(args.accessKey ?? ''),
    String(args.secretKey ?? ''),
  );
  if (typeof args.proxy === 'string' && args.proxy !== '') cfg.proxy = args.proxy;
  return cfg;
}

export const s3Handlers: Record<string, Handler> = {
  s3_sync_verify: async (args): Promise<string> => {
    const cfg = cfgFromArgs(args);
    const query = 'list-type=2&max-keys=1';
    const url = new URL(`${cfg.scheme}://${cfg.host}/${uriEncode(cfg.bucket, true)}?${query}`);
    const resp = await sendSigned(cfg, 'GET', url, query);
    const s = resp.status;
    if (s === 403) throw new Error('access denied (check the access key / secret / permissions)');
    if (s === 404) throw new Error('bucket not found (check the bucket name / endpoint / region)');
    if (s >= 200 && s < 300) return 'ok';
    const code = extractAll(await resp.text(), 'Code')[0] ?? '';
    throw new Error(code === '' ? `HTTP ${s}` : `HTTP ${s} (${code})`);
  },

  s3_sync: async (args, ctx): Promise<FolderSyncReport> => {
    const root = String(args.root ?? '');
    const cfg = cfgFromArgs(args);
    const progressId = typeof args.progressId === 'string' ? args.progressId : null;
    const sender = ctx.sender;

    const locals = enumerateLocalTree(root);
    const remotes = await listObjects(cfg);
    const all = new Set<string>([...locals.keys(), ...remotes.keys()]);

    let total = 0;
    for (const rel of all) {
      const l = locals.get(rel);
      const r = remotes.get(rel);
      if (willTransfer(l ? { size: l.size, mtime: l.mtimeMs } : null, r ? { size: r.size, mtime: r.mtime } : null)) total += 1;
    }
    progress(sender, progressId, 0, total);

    const report: FolderSyncReport = { uploaded: 0, downloaded: 0, skipped: 0, conflicts: 0, errors: [] };
    let emitted = 0;
    for (const rel of all) {
      const local = locals.get(rel);
      const remote = remotes.get(rel);
      try {
        if (local !== undefined && remote === undefined) {
          await putObject(cfg, rel, local.abs);
          report.uploaded += 1;
        } else if (local === undefined && remote !== undefined) {
          if (remote.size > MAX_FILE_BYTES) report.skipped += 1;
          else {
            await getObject(cfg, rel, root);
            report.downloaded += 1;
          }
        } else if (local !== undefined && remote !== undefined) {
          switch (decideBoth(local.size, local.mtimeMs, remote.size, remote.mtime)) {
            case 'skip':
              report.skipped += 1;
              break;
            case 'upload':
              await putObject(cfg, rel, local.abs);
              report.uploaded += 1;
              break;
            case 'download':
              await getObject(cfg, rel, root);
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
      const done = report.uploaded + report.downloaded;
      if (done !== emitted) {
        emitted = done;
        progress(sender, progressId, done, total);
      }
    }
    return report;
  },

  s3_zotero_sync: async (args, ctx): Promise<ZoteroReport> => {
    const root = String(args.root ?? '');
    const cfg = cfgFromArgs(args);
    const progressId = typeof args.progressId === 'string' ? args.progressId : null;
    const sender = ctx.sender;

    const report: ZoteroReport = { uploaded: 0, downloaded: 0, skipped: 0, conflicts: 0, downloadedKeys: [], errors: [] };
    const locals = await enumerateLocalZotero(root);
    const remoteKeys = await zoteroRemoteKeys(cfg);
    const all = new Set<string>([...locals.keys(), ...remoteKeys]);

    const total = all.size;
    progress(sender, progressId, 0, total);
    let done = 0;

    for (const key of all) {
      const local = locals.get(key);
      const remote = remoteKeys.has(key);
      try {
        if (local !== undefined && !remote) {
          await zoteroUpload(cfg, key, local);
          report.uploaded += 1;
        } else if (local === undefined && remote) {
          if (await zoteroDownload(cfg, key, root)) {
            report.downloaded += 1;
            report.downloadedKeys.push(key);
          } else {
            report.skipped += 1;
          }
        } else if (local !== undefined && remote) {
          const pb = await getBytesOpt(cfg, `${ZOTERO_SUB}${key}.prop`);
          if (pb === null) {
            await zoteroUpload(cfg, key, local);
            report.uploaded += 1;
          } else {
            const { mtime: rmtime, hash: rhash } = parseProp(pb.toString('utf8'));
            if (rhash !== '' && rhash.toLowerCase() === local.hash.toLowerCase()) {
              report.skipped += 1;
            } else if (local.mtimeMs > rmtime) {
              await zoteroUpload(cfg, key, local);
              report.uploaded += 1;
            } else if (rmtime > local.mtimeMs) {
              if (await zoteroDownload(cfg, key, root)) {
                report.downloaded += 1;
                report.downloadedKeys.push(key);
              } else {
                report.skipped += 1;
              }
            } else {
              report.conflicts += 1;
            }
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
